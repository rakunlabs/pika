package blobstore

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
	"time"
)

// MemBlobStore is an in-memory BlobStore implementation. Primary use
// is unit tests across the registry package — every protocol head
// drops its real backend into the same conformance suite as MemBlobStore
// to validate the contract.
//
// Concurrency: every method is safe for concurrent use. The blob
// table and the session table are protected by separate mutexes so
// uploads never block reads.
type MemBlobStore struct {
	mu    sync.RWMutex
	blobs map[string]memBlob

	smu      sync.Mutex
	sessions map[string]*memSession
}

type memBlob struct {
	data    []byte
	modTime time.Time
}

// NewMemBlobStore constructs an empty in-memory store.
func NewMemBlobStore() *MemBlobStore {
	return &MemBlobStore{
		blobs:    make(map[string]memBlob),
		sessions: make(map[string]*memSession),
	}
}

// Get returns a reader over the blob's bytes. Returns ErrNotFound
// when the digest is unknown.
func (m *MemBlobStore) Get(d Digest) (ReadSeekCloser, *BlobInfo, error) {
	m.mu.RLock()
	b, ok := m.blobs[d.String()]
	m.mu.RUnlock()
	if !ok {
		return nil, nil, fmt.Errorf("digest %s: %w", d, ErrNotFound)
	}
	// Copy the slice so writes through future uploads can't mutate
	// the bytes a caller is reading. bytes.Reader is read-only over
	// its backing slice so we already get a fresh handle — but we
	// defensive-copy to be safe across reseats of the same slot.
	rc := &memReader{Reader: bytes.NewReader(b.data)}
	return rc, &BlobInfo{Digest: d, Size: int64(len(b.data)), ModTime: b.modTime}, nil
}

// Stat returns metadata about a blob without copying its bytes.
func (m *MemBlobStore) Stat(d Digest) (*BlobInfo, error) {
	m.mu.RLock()
	b, ok := m.blobs[d.String()]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("digest %s: %w", d, ErrNotFound)
	}
	return &BlobInfo{Digest: d, Size: int64(len(b.data)), ModTime: b.modTime}, nil
}

// Delete removes a blob. Returns ErrNotFound when the digest is
// unknown so callers can distinguish "double-delete" from real
// errors.
func (m *MemBlobStore) Delete(d Digest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.blobs[d.String()]; !ok {
		return fmt.Errorf("digest %s: %w", d, ErrNotFound)
	}
	delete(m.blobs, d.String())
	return nil
}

// StartUpload allocates a fresh upload session with a random id.
func (m *MemBlobStore) StartUpload() (UploadSession, error) {
	id, err := randomID()
	if err != nil {
		return nil, fmt.Errorf("memblob: generate session id: %w", err)
	}
	s := &memSession{
		id:    id,
		store: m,
		buf:   &bytes.Buffer{},
	}
	m.smu.Lock()
	m.sessions[id] = s
	m.smu.Unlock()
	return s, nil
}

// ResumeUpload re-opens an existing session by id.
func (m *MemBlobStore) ResumeUpload(id string) (UploadSession, error) {
	m.smu.Lock()
	defer m.smu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %s: %w", id, ErrSessionNotFound)
	}
	if s.closed {
		return nil, fmt.Errorf("session %s: %w", id, ErrSessionClosed)
	}
	return s, nil
}

// ListBlobs walks every blob. The snapshot of digests is taken under
// the read lock so concurrent writes don't race the iteration; fn
// is then invoked outside the lock so callers can call back into the
// store without deadlocking.
func (m *MemBlobStore) ListBlobs(fn func(Digest, *BlobInfo) error) error {
	m.mu.RLock()
	snapshot := make([]Digest, 0, len(m.blobs))
	sizes := make(map[string]int64, len(m.blobs))
	mts := make(map[string]time.Time, len(m.blobs))
	for k, v := range m.blobs {
		d, err := ParseDigest(k)
		if err != nil {
			// Shouldn't happen — we control insertion — but fail
			// gracefully rather than panic on a corrupted in-memory
			// state.
			continue
		}
		snapshot = append(snapshot, d)
		sizes[k] = int64(len(v.data))
		mts[k] = v.modTime
	}
	m.mu.RUnlock()

	for _, d := range snapshot {
		k := d.String()
		if err := fn(d, &BlobInfo{Digest: d, Size: sizes[k], ModTime: mts[k]}); err != nil {
			return err
		}
	}
	return nil
}

// memSession is the in-memory UploadSession. Bytes are appended to a
// bytes.Buffer; Commit verifies the digest and copies into the blob
// table.
type memSession struct {
	id    string
	store *MemBlobStore

	mu     sync.Mutex
	buf    *bytes.Buffer
	closed bool
}

func (s *memSession) ID() string {
	return s.id
}

func (s *memSession) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, fmt.Errorf("session %s: %w", s.id, ErrSessionClosed)
	}
	// bytes.Buffer.Write never returns a short write.
	return s.buf.Write(p)
}

func (s *memSession) Offset() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(s.buf.Len())
}

func (s *memSession) Commit(expected Digest) (Digest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Digest{}, fmt.Errorf("session %s: %w", s.id, ErrSessionClosed)
	}

	alg := expected.Algorithm
	if alg == "" {
		// Default to sha256 when the caller doesn't pin one — keeps
		// the in-memory store useful for tests that don't care about
		// algorithm negotiation. Real protocols always pin.
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
		// Keep the session open so the caller can retry or cancel.
		return got, fmt.Errorf("expected %s, got %s: %w", expected, got, ErrDigestMismatch)
	}

	// Defensive copy: the buffer's slice may be reused after Commit.
	cp := make([]byte, len(data))
	copy(cp, data)

	s.store.mu.Lock()
	s.store.blobs[got.String()] = memBlob{data: cp, modTime: time.Now().UTC()}
	s.store.mu.Unlock()

	// Close the session and remove from the session table so the
	// id can't be reused.
	s.closed = true
	s.store.smu.Lock()
	delete(s.store.sessions, s.id)
	s.store.smu.Unlock()

	return got, nil
}

func (s *memSession) Cancel() error {
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

// memReader wraps bytes.Reader to satisfy ReadSeekCloser. The Close
// is a no-op; the reader is backed entirely by an in-memory slice.
type memReader struct {
	*bytes.Reader
}

func (m *memReader) Close() error { return nil }

// randomID generates a 16-byte hex string id for upload sessions.
// Used by every BlobStore impl in this package — extracted here so
// the format is uniform.
func randomID() (string, error) {
	buf := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
