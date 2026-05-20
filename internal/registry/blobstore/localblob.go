package blobstore

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LocalBlobStore is a BlobStore backed by a directory on the local
// filesystem. Layout:
//
//	{root}/
//	├── blobs/sha256/{first2}/{full-hex}   committed blobs (CAS)
//	└── _uploads/{uuid}                     in-flight upload sessions
//
// The first-two-bytes sharding avoids putting millions of files in a
// single directory, which slows fs lookups on some backends (extX,
// btrfs are fine; NTFS, some FUSE mounts care).
//
// Atomicity: uploads write to a tmp file under _uploads/, then a
// successful Commit renames the tmp into place under blobs/. os.Rename
// is atomic on a single filesystem; the rename succeeds-or-fails, never
// half-applied. The blobs/ tree is therefore safe from "see a partial
// upload" races even under concurrent Commit + GC.
//
// In-process session state is kept in memory so the package can be
// restarted without leaking files (a Cancel on shutdown isn't required
// — leftover tmp files in _uploads/ are pruned by garbage collection).
type LocalBlobStore struct {
	root string

	smu      sync.Mutex
	sessions map[string]*localSession
}

// NewLocalBlobStore returns a store rooted at the given directory.
// The directory (and the two sub-trees) are created if missing.
func NewLocalBlobStore(root string) (*LocalBlobStore, error) {
	if root == "" {
		return nil, fmt.Errorf("localblob: root path is empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("localblob: resolve root: %w", err)
	}
	for _, sub := range []string{"blobs", "_uploads"} {
		if err := os.MkdirAll(filepath.Join(abs, sub), 0o755); err != nil {
			return nil, fmt.Errorf("localblob: mkdir %s: %w", sub, err)
		}
	}
	return &LocalBlobStore{
		root:     abs,
		sessions: make(map[string]*localSession),
	}, nil
}

// Root returns the absolute root directory. Mostly for tests and
// diagnostic output.
func (l *LocalBlobStore) Root() string {
	return l.root
}

// blobPath returns the on-disk path for a committed blob digest.
// Mirrors the layout documented on the type.
func (l *LocalBlobStore) blobPath(d Digest) string {
	if len(d.Hex) < 2 {
		// Defensive: ParseDigest enforces the minimum lengths, but
		// callers may hand-construct Digest values in tests.
		return filepath.Join(l.root, "blobs", d.Algorithm, d.Hex)
	}
	return filepath.Join(l.root, "blobs", d.Algorithm, d.Hex[:2], d.Hex)
}

func (l *LocalBlobStore) uploadPath(id string) string {
	return filepath.Join(l.root, "_uploads", id)
}

// Get opens a blob for reading. The returned *os.File satisfies
// ReadSeekCloser directly.
func (l *LocalBlobStore) Get(d Digest) (ReadSeekCloser, *BlobInfo, error) {
	p := l.blobPath(d)
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("digest %s: %w", d, ErrNotFound)
		}
		return nil, nil, fmt.Errorf("digest %s: open: %w", d, err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("digest %s: stat: %w", d, err)
	}
	return f, &BlobInfo{Digest: d, Size: st.Size(), ModTime: st.ModTime()}, nil
}

// Stat returns metadata about a blob without opening it.
func (l *LocalBlobStore) Stat(d Digest) (*BlobInfo, error) {
	st, err := os.Stat(l.blobPath(d))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("digest %s: %w", d, ErrNotFound)
		}
		return nil, fmt.Errorf("digest %s: stat: %w", d, err)
	}
	return &BlobInfo{Digest: d, Size: st.Size(), ModTime: st.ModTime()}, nil
}

// Delete removes a blob. On success the blob's tree leaf is removed;
// the sharded parent directories are left behind because other blobs
// may share them.
func (l *LocalBlobStore) Delete(d Digest) error {
	p := l.blobPath(d)
	if err := os.Remove(p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("digest %s: %w", d, ErrNotFound)
		}
		return fmt.Errorf("digest %s: remove: %w", d, err)
	}
	return nil
}

// StartUpload opens a new tmp file under _uploads/ and registers a
// session keyed by its id.
func (l *LocalBlobStore) StartUpload() (UploadSession, error) {
	id, err := randomID()
	if err != nil {
		return nil, fmt.Errorf("localblob: id: %w", err)
	}
	p := l.uploadPath(id)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("localblob: create tmp: %w", err)
	}
	s := &localSession{
		id:    id,
		store: l,
		path:  p,
		f:     f,
	}
	l.smu.Lock()
	l.sessions[id] = s
	l.smu.Unlock()
	return s, nil
}

// ResumeUpload re-opens an in-flight session. Sessions only persist
// across the lifetime of the running process; a session whose tmp
// file is present on disk but not in the session table is not
// considered live — call StartUpload instead.
func (l *LocalBlobStore) ResumeUpload(id string) (UploadSession, error) {
	l.smu.Lock()
	defer l.smu.Unlock()
	s, ok := l.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %s: %w", id, ErrSessionNotFound)
	}
	if s.closed {
		return nil, fmt.Errorf("session %s: %w", id, ErrSessionClosed)
	}
	return s, nil
}

// ListBlobs walks the blobs/ tree. The walk is best-effort safe under
// concurrent writes: a blob being committed mid-walk may or may not
// appear in this iteration but never appears half-written.
func (l *LocalBlobStore) ListBlobs(fn func(Digest, *BlobInfo) error) error {
	blobsRoot := filepath.Join(l.root, "blobs")
	return filepath.WalkDir(blobsRoot, func(path string, dirent os.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Skip subtree errors but keep walking siblings.
			return nil //nolint:nilerr
		}
		if dirent.IsDir() {
			return nil
		}
		// Path: {root}/blobs/{alg}/{first2}/{full-hex}
		rel, err := filepath.Rel(blobsRoot, path)
		if err != nil {
			return nil //nolint:nilerr
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 3 {
			// Not a CAS leaf; skip silently.
			return nil
		}
		alg, _, hx := parts[0], parts[1], parts[2]
		// Build a Digest and validate hex length matches the algorithm.
		if wantLen, ok := algorithmHexLength(alg); !ok || len(hx) != wantLen {
			return nil
		}
		d := Digest{Algorithm: alg, Hex: hx}
		st, err := dirent.Info()
		if err != nil {
			return nil //nolint:nilerr
		}
		return fn(d, &BlobInfo{Digest: d, Size: st.Size(), ModTime: st.ModTime()})
	})
}

// localSession is the local-fs UploadSession. Bytes are appended to
// the tmp file; Commit verifies the digest, then renames the tmp file
// into the CAS slot.
type localSession struct {
	id    string
	store *LocalBlobStore
	path  string

	mu     sync.Mutex
	f      *os.File
	n      int64
	closed bool
}

func (s *localSession) ID() string { return s.id }

func (s *localSession) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, fmt.Errorf("session %s: %w", s.id, ErrSessionClosed)
	}
	n, err := s.f.Write(p)
	s.n += int64(n)
	return n, err
}

func (s *localSession) Offset() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

// Commit re-reads the tmp file end-to-end, computes the digest, and
// renames into the CAS slot if it matches. The re-read avoids state
// drift between the on-the-fly hasher and the bytes the kernel
// actually persisted (page-cache vs. fsync semantics on weird
// filesystems).
func (s *localSession) Commit(expected Digest) (Digest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Digest{}, fmt.Errorf("session %s: %w", s.id, ErrSessionClosed)
	}

	alg := expected.Algorithm
	if alg == "" {
		alg = "sha256"
	}

	// fsync before hashing — guarantees the bytes we hash are what
	// will be present after the rename.
	if err := s.f.Sync(); err != nil {
		return Digest{}, fmt.Errorf("session %s: fsync: %w", s.id, err)
	}
	if _, err := s.f.Seek(0, io.SeekStart); err != nil {
		return Digest{}, fmt.Errorf("session %s: seek: %w", s.id, err)
	}
	h, err := NewHasher(alg)
	if err != nil {
		return Digest{}, err
	}
	if _, err := io.Copy(h, s.f); err != nil {
		return Digest{}, fmt.Errorf("session %s: read: %w", s.id, err)
	}
	got := DigestFromHash(alg, h)
	if !expected.IsZero() && !expected.Equal(got) {
		// Keep session open: caller can re-Commit with right digest
		// or Cancel. Restore the seek pointer to EOF so subsequent
		// Writes don't overwrite.
		if _, seekErr := s.f.Seek(s.n, io.SeekStart); seekErr != nil {
			// If we can't restore the seek pointer, the session is
			// effectively corrupted — force close so the caller
			// doesn't trip silently later.
			s.forceCloseLocked()
			return got, fmt.Errorf("expected %s, got %s; session lost: %w",
				expected, got, ErrDigestMismatch)
		}
		return got, fmt.Errorf("expected %s, got %s: %w", expected, got, ErrDigestMismatch)
	}

	// Close tmp file before rename — some filesystems object to
	// renaming an open file.
	if err := s.f.Close(); err != nil {
		return Digest{}, fmt.Errorf("session %s: close tmp: %w", s.id, err)
	}

	dst := s.store.blobPath(got)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return Digest{}, fmt.Errorf("session %s: mkdir target: %w", s.id, err)
	}
	if err := os.Rename(s.path, dst); err != nil {
		// If the rename failed but the target already exists with
		// the right digest (concurrent commit of the same content),
		// treat as success: CAS is content-addressable, two writers
		// of the same blob is fine.
		if _, statErr := os.Stat(dst); statErr == nil {
			_ = os.Remove(s.path)
		} else {
			return Digest{}, fmt.Errorf("session %s: rename: %w", s.id, err)
		}
	}

	s.closed = true
	s.store.smu.Lock()
	delete(s.store.sessions, s.id)
	s.store.smu.Unlock()
	return got, nil
}

func (s *localSession) Cancel() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.forceCloseLocked()
	return nil
}

// forceCloseLocked is the close+cleanup path used by Cancel and by
// fatal Commit failures. Caller must hold s.mu.
func (s *localSession) forceCloseLocked() {
	s.closed = true
	if s.f != nil {
		_ = s.f.Close()
		s.f = nil
	}
	if s.path != "" {
		_ = os.Remove(s.path)
	}
	s.store.smu.Lock()
	delete(s.store.sessions, s.id)
	s.store.smu.Unlock()
}

// PruneAbandonedUploads deletes any tmp file under _uploads/ whose
// modification time is older than maxAge AND is not tied to a live
// session. Called by garbage collection on demand; not on a timer.
//
// When dryRun is true the method matches files exactly as the
// destructive pass would but skips the os.Remove call, so callers
// can surface "estimated garbage" without committing to a sweep.
//
// Returns the count of files matched, their total size in bytes,
// and the first error (if any) encountered while listing or
// removing files. Partial progress is reflected in count/bytes on
// non-fatal errors.
func (l *LocalBlobStore) PruneAbandonedUploads(maxAge time.Duration, dryRun bool) (int, int64, error) {
	entries, err := os.ReadDir(filepath.Join(l.root, "_uploads"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Fresh store with no _uploads yet; nothing to prune.
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("localblob: read uploads dir: %w", err)
	}
	threshold := time.Now().Add(-maxAge)

	live := make(map[string]struct{})
	l.smu.Lock()
	for id := range l.sessions {
		live[id] = struct{}{}
	}
	l.smu.Unlock()

	removed := 0
	var bytes int64
	var firstErr error
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, ok := live[e.Name()]; ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if info.ModTime().After(threshold) {
			continue
		}
		if dryRun {
			removed++
			bytes += info.Size()
			continue
		}
		if err := os.Remove(filepath.Join(l.root, "_uploads", e.Name())); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		removed++
		bytes += info.Size()
	}
	return removed, bytes, firstErr
}

// Compile-time assertion that LocalBlobStore satisfies
// AbandonedUploadPruner. Catches signature drift at build time
// rather than during a runtime type-assertion in the GC pass.
var _ AbandonedUploadPruner = (*LocalBlobStore)(nil)
