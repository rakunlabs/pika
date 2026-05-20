// Package blobstore is the content-addressable storage abstraction
// every protocol head in internal/registry sits on top of.
//
// Why a separate abstraction (instead of using rawfs directly)
//
// The three registry protocols we host all need the same primitives:
// stream-and-finalize chunked uploads with a digest commit step,
// digest-keyed reads (no human-readable paths), and the ability to
// resume a partial upload. rawfs is path-keyed and read-or-Write,
// which is a poor fit:
//
//   - Docker requires an upload session machine (POST init →
//     PATCH chunk → PUT finalize+digest), spec-mandated.
//   - NPM publish ships a base64-encoded tarball whose integrity
//     (sha512) we must verify before the row goes live.
//   - Go zip uploads need the same digest verification to be safe
//     against MITM.
//
// Wrapping every protocol around path-based writes would mean
// duplicating the upload state machine in every package. BlobStore
// gives us one upload pipeline and one digest verifier that every
// protocol head can reuse.
//
// Why content-addressable
//
// Docker is the hard constraint — its blob layer is keyed by digest,
// not by name, and tags are pointer files into the blob set. Once we
// have a CAS substrate, Go zips and NPM tarballs deduplicate "for
// free" if we ever decide to point their per-version metadata files
// at CAS blobs (deferred to a later phase per PLAN.md).
//
// Concurrency model
//
// Implementations must be safe for concurrent Get / Stat / ListBlobs
// across goroutines. Upload sessions are NOT required to be safe for
// concurrent access to the same session — but two distinct sessions
// (different IDs) must be able to run side by side. The contract is
// "one writer per session, many concurrent sessions".
package blobstore

import (
	"errors"
	"io"
	"time"
)

// Common errors. Wrap with sentinel so callers can use errors.Is.
//
// ErrNotFound is returned by Get/Stat/Delete when the digest has no
// blob behind it. ErrDigestMismatch is the Commit failure when the
// computed digest does not match the expected one — Commit must
// then leave the blob in a state safe to retry against (typically:
// the partial bytes are kept in the upload session, callers can
// re-Commit with the correct expected digest, or Cancel to discard).
//
// ErrSessionNotFound is returned by ResumeUpload when the supplied
// id has no live session. ErrSessionClosed signals that the session
// has already been committed or cancelled and cannot accept further
// writes.
var (
	ErrNotFound        = errors.New("blobstore: not found")
	ErrDigestMismatch  = errors.New("blobstore: digest mismatch")
	ErrSessionNotFound = errors.New("blobstore: upload session not found")
	ErrSessionClosed   = errors.New("blobstore: upload session closed")
	ErrReadOnly        = errors.New("blobstore: backend is read-only")
)

// BlobInfo carries metadata about a stored blob. Size is mandatory;
// ModTime is best-effort and may be zero on backends that do not
// preserve modification time (in-memory, some FUSE filesystems).
type BlobInfo struct {
	Digest  Digest
	Size    int64
	ModTime time.Time
}

// ReadSeekCloser combines the three interfaces an HTTP range-request
// handler needs. Matches the shape returned by rawfs.RawFS.Open so
// glue code can route between the two without re-buffering.
type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

// BlobStore is the content-addressable store contract. Implementations
// live next to this file: memblob (tests), localblob (local fs),
// rawfsblob (generic adapter on top of any rawfs.RawFS).
//
// All methods are safe for concurrent use. Upload sessions are
// per-call: each StartUpload / ResumeUpload returns a fresh session
// owned by the caller goroutine.
type BlobStore interface {
	// Get opens a blob for reading. Returns ErrNotFound when the
	// digest is unknown. The caller must Close the returned reader.
	Get(d Digest) (ReadSeekCloser, *BlobInfo, error)

	// Stat returns metadata about a blob without opening it. Returns
	// ErrNotFound when the digest is unknown.
	Stat(d Digest) (*BlobInfo, error)

	// Delete removes a blob. Returns ErrNotFound when the digest is
	// unknown. Idempotent across retries — a backend that observes
	// concurrent deletes of the same digest should return success on
	// both, or ErrNotFound on the second, but never a partial-write
	// error.
	Delete(d Digest) error

	// StartUpload begins a new upload session. The session id is
	// stable across the session's lifetime; callers persist it to
	// reissue (e.g. Docker's Location header points back at the
	// session).
	StartUpload() (UploadSession, error)

	// ResumeUpload re-opens an existing session by id. Returns
	// ErrSessionNotFound when the id is unknown or already closed.
	ResumeUpload(id string) (UploadSession, error)

	// ListBlobs walks every blob in the store. fn is invoked once
	// per blob; returning an error from fn stops the walk and
	// returns that error from ListBlobs. Used by garbage collection.
	//
	// Order is unspecified. Implementations may visit the same blob
	// at most once per call but make no guarantees about whether
	// blobs written concurrently with the walk are visited.
	ListBlobs(fn func(Digest, *BlobInfo) error) error
}

// UploadSession is the one-writer, one-finaliser handle returned by
// StartUpload / ResumeUpload. The state machine is:
//
//	created → (Write*) → Commit | Cancel
//
// Once Commit or Cancel returns, every subsequent method returns
// ErrSessionClosed. The session id is stable from creation until
// close.
//
// Commit is the atomicity boundary: a partial blob never becomes
// addressable by digest. Backends that don't have native atomic
// publish (e.g. local fs) use tmp-file + rename; backends that do
// (S3 multipart) use the native primitive.
type UploadSession interface {
	// ID returns the session identifier. Stable across the session.
	ID() string

	// Write appends bytes to the session. Returns the number of
	// bytes written and any error. Implementations may buffer
	// internally; an error part-way through Write leaves the
	// session in a re-Writable state (the next Write picks up
	// after the bytes that were actually consumed).
	Write(p []byte) (int, error)

	// Offset returns the number of bytes written so far. Used by
	// Docker's PATCH upload to report Range progress.
	Offset() int64

	// Commit finalises the upload. The expected digest is verified
	// against the bytes received: if it doesn't match, the session
	// stays open and ErrDigestMismatch is returned so the caller
	// can either Cancel or try again with the right digest.
	//
	// On success the bytes become addressable via Get(d) and the
	// session is closed. The returned Digest is identical to
	// expected when the input was valid.
	Commit(expected Digest) (Digest, error)

	// Cancel discards the session. Idempotent; calling Cancel on an
	// already-closed session is a no-op (returns nil).
	Cancel() error
}

// ReadOnlyBlobStore is a marker interface implementations of
// BlobStore satisfy when StartUpload / ResumeUpload always return
// ErrReadOnly. The marker is preferred over a probe-with-side-effects
// because StartUpload may allocate a session id even when the caller
// doesn't intend to write.
//
// Today every implementation in this package is writable. The marker
// is here for future remote-mirror backends that wrap a read-only
// upstream.
type ReadOnlyBlobStore interface {
	BlobStore
	ReadOnly() bool
}

// AbandonedUploadPruner is the optional interface a BlobStore
// implements when it can clean up upload sessions whose tmp files
// were left behind by interrupted clients (network drop, container
// kill mid-PATCH, process restart). Garbage collection type-asserts
// to this interface and folds the prune into the same admin pass so
// operators have one button to reclaim everything reclaimable.
//
// Only LocalBlobStore implements this today: MemBlobStore has no
// persistent tmp area to prune, and RawFSBlobStore keeps sessions
// purely in memory (sessions die with the process; restart leaves
// no tmp residue to clean). Future S3-multipart backends would
// implement this to abort orphaned multipart uploads.
type AbandonedUploadPruner interface {
	// PruneAbandonedUploads removes tmp files older than maxAge
	// that are not associated with a live in-process session.
	// When dryRun is true the method returns the count and bytes
	// it would have removed without touching the filesystem.
	//
	// Returns (count, bytes, error) where count is the number of
	// abandoned upload tmp files matched and bytes is the total
	// reclaimable size. A non-nil error reports the first failure
	// encountered while removing files; partial progress is still
	// reflected in count/bytes.
	PruneAbandonedUploads(maxAge time.Duration, dryRun bool) (int, int64, error)
}
