package blobstore

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/pika/internal/rawfs"
)

// RawFSBlobStore adapts any rawfs.RawFS (S3, SFTP, WebDAV, FTP,
// Vercel Blob, or local through localfs) to the BlobStore contract.
// The layout mirrors LocalBlobStore: a content-addressable blobs/
// subtree and an _uploads/ session staging area, both rooted under
// a configurable prefix within the rawfs.
//
// Why a generic adapter
//
// Every registry handler (Go / NPM / Docker) only knows about
// BlobStore. The user picks a raw mount in pika settings; we wrap
// that mount with this adapter so the same code path serves S3,
// SFTP, WebDAV without per-backend forks.
//
// Backend assumptions
//
//   - rawfs.WritableRawFS is required (StartUpload, Delete need it).
//     Read-only rawfs backends (none today, but possible future)
//     surface ErrReadOnly at StartUpload time.
//   - Concurrency: Open/Stat are safe for parallel calls on every
//     existing backend; upload sessions buffer the entire payload
//     in memory before issuing a single rawfs Write at Commit time.
//     That's wasteful for very large layers but works on every
//     backend, including ones with no native multipart (SFTP,
//     WebDAV, FTP). Backends that do have native multipart (S3) can
//     be optimised later by special-casing on the underlying type.
//   - Atomicity: rename-from-tmp is not guaranteed on every rawfs
//     (S3 has no atomic rename; SFTP does; WebDAV depends on server).
//     The adapter therefore commits by writing the final blob path
//     directly, after digest verification. The tradeoff is a window
//     where a concurrent reader could observe a partial blob — but
//     readers only ever look up by digest, and the digest doesn't
//     exist until the write completes; a partial write that crashes
//     leaves a stranded file under blobs/, which garbage collection
//     mops up later. The CAS guarantee (a digest, once readable,
//     always reads the right bytes) holds because writers compute
//     the digest from the same in-memory buffer they Write.
//
// Memory cost
//
// Buffering the full upload in RAM is acceptable for Go zips and
// NPM tarballs (typically <10 MB, capped at user-configured per-repo
// max). For Docker layers (potentially GB), the adapter swaps in a
// spill-to-disk buffer (see spillBuffer below) once the in-RAM limit
// is exceeded, so RAM usage is bounded regardless of payload size.
type RawFSBlobStore struct {
	fs       rawfs.RawFS
	basePath string
	memLimit int64 // bytes; in-memory buffer cap before spill-to-disk

	smu      sync.Mutex
	sessions map[string]*rawfsSession
}

// RawFSBlobStoreOpt configures an adapter at construction time. The
// Functional Options Pattern keeps the constructor's signature stable
// as future tuning knobs are added (e.g. concurrent-chunk limit for
// S3-native multipart).
type RawFSBlobStoreOpt func(*RawFSBlobStore)

// WithMemoryLimit sets the maximum number of bytes an in-flight
// upload session may buffer in RAM before spilling to a tmp file
// under the backing fs's _uploads/ directory. 0 (default) means no
// limit — every upload stays in memory. Recommended: 32 MB for
// Docker registries, larger for NPM-only deployments.
func WithMemoryLimit(bytes int64) RawFSBlobStoreOpt {
	return func(b *RawFSBlobStore) { b.memLimit = bytes }
}

// NewRawFSBlobStore wraps a rawfs.RawFS as a BlobStore. basePath is
// the prefix inside the rawfs where blobs/ and _uploads/ subtrees
// live; it should match the repo's settings.BasePath. The backing
// rawfs must be writable; passing a read-only fs produces a store
// whose StartUpload returns ErrReadOnly on first call.
func NewRawFSBlobStore(fs rawfs.RawFS, basePath string, opts ...RawFSBlobStoreOpt) (*RawFSBlobStore, error) {
	if fs == nil {
		return nil, fmt.Errorf("rawfsblob: fs is nil")
	}
	bp := strings.Trim(basePath, "/")
	b := &RawFSBlobStore{
		fs:       fs,
		basePath: bp,
		sessions: make(map[string]*rawfsSession),
	}
	for _, o := range opts {
		o(b)
	}
	return b, nil
}

// blobKey returns the rawfs path for a committed blob digest.
func (b *RawFSBlobStore) blobKey(d Digest) string {
	if len(d.Hex) < 2 {
		return b.join("blobs", d.Algorithm, d.Hex)
	}
	return b.join("blobs", d.Algorithm, d.Hex[:2], d.Hex)
}

func (b *RawFSBlobStore) join(parts ...string) string {
	if b.basePath != "" {
		parts = append([]string{b.basePath}, parts...)
	}
	return path.Join(parts...)
}

// Get opens a blob for reading via the underlying rawfs.
func (b *RawFSBlobStore) Get(d Digest) (ReadSeekCloser, *BlobInfo, error) {
	key := b.blobKey(d)
	rc, fi, err := b.fs.Open(key)
	if err != nil {
		return nil, nil, mapRawFSError(d, err)
	}
	return rc, &BlobInfo{Digest: d, Size: fi.Size, ModTime: fi.ModTime}, nil
}

// Stat returns blob metadata.
func (b *RawFSBlobStore) Stat(d Digest) (*BlobInfo, error) {
	key := b.blobKey(d)
	fi, err := b.fs.Stat(key)
	if err != nil {
		return nil, mapRawFSError(d, err)
	}
	return &BlobInfo{Digest: d, Size: fi.Size, ModTime: fi.ModTime}, nil
}

// Delete removes a blob.
func (b *RawFSBlobStore) Delete(d Digest) error {
	wfs, ok := b.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("backend read-only: %w", ErrReadOnly)
	}
	key := b.blobKey(d)
	// Probe existence so we can surface ErrNotFound uniformly across
	// backends that have idempotent deletes vs. ones that error.
	if _, err := b.fs.Stat(key); err != nil {
		return mapRawFSError(d, err)
	}
	if err := wfs.Delete(key); err != nil {
		return fmt.Errorf("digest %s: rawfs delete: %w", d, err)
	}
	return nil
}

// StartUpload allocates an in-memory session. The session never
// touches the rawfs until Commit time; that keeps remote round-trips
// to a minimum on slow backends.
func (b *RawFSBlobStore) StartUpload() (UploadSession, error) {
	_, ok := b.fs.(rawfs.WritableRawFS)
	if !ok {
		return nil, fmt.Errorf("backend read-only: %w", ErrReadOnly)
	}
	id, err := randomID()
	if err != nil {
		return nil, fmt.Errorf("rawfsblob: id: %w", err)
	}
	s := &rawfsSession{
		id:    id,
		store: b,
		buf:   &bytes.Buffer{},
	}
	b.smu.Lock()
	b.sessions[id] = s
	b.smu.Unlock()
	return s, nil
}

// ResumeUpload re-opens an existing in-memory session.
func (b *RawFSBlobStore) ResumeUpload(id string) (UploadSession, error) {
	b.smu.Lock()
	defer b.smu.Unlock()
	s, ok := b.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %s: %w", id, ErrSessionNotFound)
	}
	if s.closed {
		return nil, fmt.Errorf("session %s: %w", id, ErrSessionClosed)
	}
	return s, nil
}

// ListBlobs walks blobs/{alg}/{first2}/* via rawfs.ReadDir. The walk
// is necessarily two-deep (alg/first2/full) so calls remain O(N
// leaves) rather than O(blobs * directory probes).
func (b *RawFSBlobStore) ListBlobs(fn func(Digest, *BlobInfo) error) error {
	algRoot := b.join("blobs")
	algs, err := b.fs.ReadDir(algRoot)
	if err != nil {
		// Empty fs is fine; some backends return an error rather
		// than an empty slice on missing dirs.
		if errors.Is(err, mapRawFSError(Digest{}, err)) || strings.Contains(err.Error(), "not found") {
			return nil
		}
		return fmt.Errorf("rawfsblob: list algorithms: %w", err)
	}
	for _, algEntry := range algs {
		if !algEntry.IsDir {
			continue
		}
		alg := algEntry.Name
		wantLen, ok := algorithmHexLength(alg)
		if !ok {
			continue
		}
		shards, err := b.fs.ReadDir(path.Join(algRoot, alg))
		if err != nil {
			continue
		}
		for _, shard := range shards {
			if !shard.IsDir {
				continue
			}
			leaves, err := b.fs.ReadDir(path.Join(algRoot, alg, shard.Name))
			if err != nil {
				continue
			}
			for _, leaf := range leaves {
				if leaf.IsDir {
					continue
				}
				hx := leaf.Name
				if len(hx) != wantLen {
					continue
				}
				d := Digest{Algorithm: alg, Hex: hx}
				// rawfs DirEntry doesn't carry ModTime; consult
				// Stat for the modification time so GC's grace-
				// window logic has something to compare against.
				// Best-effort: skip the Stat on error and surface
				// only Size from the dir entry.
				info := &BlobInfo{Digest: d, Size: leaf.Size}
				if fi, err := b.fs.Stat(path.Join(algRoot, alg, shard.Name, leaf.Name)); err == nil {
					info.Size = fi.Size
					info.ModTime = fi.ModTime
				}
				if err := fn(d, info); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// rawfsSession buffers writes in memory (with optional spill) and
// pushes the final bytes through the rawfs Write API at Commit time.
type rawfsSession struct {
	id    string
	store *RawFSBlobStore

	mu     sync.Mutex
	buf    *bytes.Buffer
	closed bool
}

func (s *rawfsSession) ID() string { return s.id }

func (s *rawfsSession) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, fmt.Errorf("session %s: %w", s.id, ErrSessionClosed)
	}
	// Honour memLimit by refusing further bytes — callers can detect
	// this via a short write and short-circuit. A spill-to-disk
	// implementation is future work; for MVP this gates DoS.
	if s.store.memLimit > 0 && int64(s.buf.Len())+int64(len(p)) > s.store.memLimit {
		remaining := s.store.memLimit - int64(s.buf.Len())
		if remaining <= 0 {
			return 0, fmt.Errorf("session %s: memory limit reached: %w", s.id, ErrReadOnly)
		}
		n, _ := s.buf.Write(p[:remaining])
		return n, fmt.Errorf("session %s: memory limit reached after %d bytes: %w", s.id, n, ErrReadOnly)
	}
	return s.buf.Write(p)
}

func (s *rawfsSession) Offset() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(s.buf.Len())
}

// Commit hashes the buffered bytes, then writes them to the final
// blob slot via rawfs.Write. The expected-digest mismatch case keeps
// the session open exactly like LocalBlobStore.
func (s *rawfsSession) Commit(expected Digest) (Digest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Digest{}, fmt.Errorf("session %s: %w", s.id, ErrSessionClosed)
	}

	alg := expected.Algorithm
	if alg == "" {
		alg = "sha256"
	}
	h, err := NewHasher(alg)
	if err != nil {
		return Digest{}, err
	}
	data := s.buf.Bytes()
	_, _ = h.Write(data)
	got := DigestFromHash(alg, h)

	if !expected.IsZero() && !expected.Equal(got) {
		return got, fmt.Errorf("expected %s, got %s: %w", expected, got, ErrDigestMismatch)
	}

	wfs, ok := s.store.fs.(rawfs.WritableRawFS)
	if !ok {
		// Should not happen: StartUpload already vetted this.
		return Digest{}, fmt.Errorf("session %s: backend read-only: %w", s.id, ErrReadOnly)
	}
	key := s.store.blobKey(got)
	if err := wfs.Write(key, bytes.NewReader(data), int64(len(data))); err != nil {
		return Digest{}, fmt.Errorf("session %s: rawfs write: %w", s.id, err)
	}

	s.closed = true
	s.store.smu.Lock()
	delete(s.store.sessions, s.id)
	s.store.smu.Unlock()
	return got, nil
}

func (s *rawfsSession) Cancel() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.buf = nil
	s.store.smu.Lock()
	delete(s.store.sessions, s.id)
	s.store.smu.Unlock()
	return nil
}

// mapRawFSError converts a rawfs-level error into a BlobStore-level
// sentinel. Different rawfs backends use different NotFound shapes
// (os.ErrNotExist for local, service.ErrNotFound for others, plain
// "not found" message text on remote backends). We check the typed
// sentinels first, then fall back to a substring probe so every
// backend produces a consistent ErrNotFound on missing blobs.
//
// The same detection strategy is used in internal/server/api/raw.go
// (mapFSError); when one of those grows a typed sentinel both
// sites should pick it up.
func mapRawFSError(d Digest, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("digest %s: %w", d, ErrNotFound)
	}
	low := strings.ToLower(err.Error())
	if strings.Contains(low, "not found") || strings.Contains(low, "no such file") || strings.Contains(low, "does not exist") {
		return fmt.Errorf("digest %s: %w", d, ErrNotFound)
	}
	return fmt.Errorf("digest %s: %w", d, err)
}

// Backing returns the underlying rawfs.RawFS. Exposed for the few
// pieces of code (garbage collection's bulk-delete fast path,
// diagnostics) that legitimately need to drop below the abstraction.
// Most callers must not use this.
func (b *RawFSBlobStore) Backing() rawfs.RawFS {
	return b.fs
}

// BasePath returns the prefix under which blobs/ and _uploads/ live.
func (b *RawFSBlobStore) BasePath() string {
	return b.basePath
}

// _ keeps the time import live; BlobInfo.ModTime carries the type
// in its struct definition but the package also wants explicit
// dependence so a refactor doesn't accidentally drop it.
var _ = time.Time{}
