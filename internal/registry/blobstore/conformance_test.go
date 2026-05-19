package blobstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"testing"
)

// Conformance suite — every BlobStore implementation in this package
// is driven through the same suite via runConformance. Adding a new
// implementation means dropping a new top-level test that constructs
// the impl, defers cleanup, and calls runConformance(t, impl).

// runConformance exercises every contract guarantee BlobStore makes.
// Implementations must pass without modification; if a test fails
// for a specific backend, the bug is in the backend (or the contract
// has changed and the suite needs an update — never silently relax).
func runConformance(t *testing.T, bs BlobStore) {
	t.Helper()
	t.Run("StatNotFound", func(t *testing.T) { testStatNotFound(t, bs) })
	t.Run("GetNotFound", func(t *testing.T) { testGetNotFound(t, bs) })
	t.Run("UploadCommitRead", func(t *testing.T) { testUploadCommitRead(t, bs) })
	t.Run("UploadDigestMismatch", func(t *testing.T) { testUploadDigestMismatch(t, bs) })
	t.Run("UploadCancel", func(t *testing.T) { testUploadCancel(t, bs) })
	t.Run("ResumeUpload", func(t *testing.T) { testResumeUpload(t, bs) })
	t.Run("ResumeMissing", func(t *testing.T) { testResumeMissing(t, bs) })
	t.Run("Delete", func(t *testing.T) { testDelete(t, bs) })
	t.Run("DeleteMissing", func(t *testing.T) { testDeleteMissing(t, bs) })
	t.Run("ListBlobs", func(t *testing.T) { testListBlobs(t, bs) })
	t.Run("Range", func(t *testing.T) { testRange(t, bs) })
	t.Run("ConcurrentReads", func(t *testing.T) { testConcurrentReads(t, bs) })
	t.Run("DoubleCommitRejected", func(t *testing.T) { testDoubleCommit(t, bs) })
	t.Run("CancelIdempotent", func(t *testing.T) { testCancelIdempotent(t, bs) })
}

// sha256Of computes the sha256 digest of data as a Digest value. Used
// in tests so the suite doesn't depend on the BlobStore's own
// hashing path to be correct (don't grade homework with the cheat
// sheet).
func sha256Of(data []byte) Digest {
	sum := sha256.Sum256(data)
	return Digest{Algorithm: "sha256", Hex: hex.EncodeToString(sum[:])}
}

func testStatNotFound(t *testing.T, bs BlobStore) {
	_, err := bs.Stat(sha256Of([]byte("never-written")))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stat missing: want ErrNotFound, got %v", err)
	}
}

func testGetNotFound(t *testing.T, bs BlobStore) {
	_, _, err := bs.Get(sha256Of([]byte("never-written-either")))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing: want ErrNotFound, got %v", err)
	}
}

func testUploadCommitRead(t *testing.T, bs BlobStore) {
	data := []byte("hello-world")
	want := sha256Of(data)

	sess, err := bs.StartUpload()
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	if n, err := sess.Write(data); err != nil || n != len(data) {
		t.Fatalf("Write: n=%d err=%v", n, err)
	}
	if got := sess.Offset(); got != int64(len(data)) {
		t.Fatalf("Offset: %d, want %d", got, len(data))
	}

	got, err := sess.Commit(want)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("Commit returned %s, want %s", got, want)
	}

	// Now readable.
	info, err := bs.Stat(want)
	if err != nil {
		t.Fatalf("Stat after commit: %v", err)
	}
	if info.Size != int64(len(data)) {
		t.Fatalf("Stat size %d, want %d", info.Size, len(data))
	}

	rc, _, err := bs.Get(want)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	read, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(read, data) {
		t.Fatalf("read %q, want %q", read, data)
	}
}

func testUploadDigestMismatch(t *testing.T, bs BlobStore) {
	sess, err := bs.StartUpload()
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	if _, err := sess.Write([]byte("payload-A")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Commit with the digest of a different payload.
	bogus := sha256Of([]byte("payload-B"))
	_, err = sess.Commit(bogus)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("expected ErrDigestMismatch, got %v", err)
	}

	// Session must remain open so the caller can recover.
	if _, err := sess.Write([]byte("-more")); err != nil {
		t.Fatalf("Write after mismatch: %v", err)
	}
	want := sha256Of([]byte("payload-A-more"))
	if _, err := sess.Commit(want); err != nil {
		t.Fatalf("Commit after mismatch + recover: %v", err)
	}
	if _, err := bs.Stat(want); err != nil {
		t.Fatalf("Stat after recovery: %v", err)
	}
}

func testUploadCancel(t *testing.T, bs BlobStore) {
	sess, err := bs.StartUpload()
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	if _, err := sess.Write([]byte("never-finalised")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	id := sess.ID()
	if err := sess.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	// Resume must fail (session is gone).
	if _, err := bs.ResumeUpload(id); err == nil {
		t.Fatal("ResumeUpload after cancel: expected error")
	}
	// And no blob materialised.
	if _, err := bs.Stat(sha256Of([]byte("never-finalised"))); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancelled blob should not exist: %v", err)
	}
}

func testResumeUpload(t *testing.T, bs BlobStore) {
	sess, err := bs.StartUpload()
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	id := sess.ID()
	if _, err := sess.Write([]byte("part-1-")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Drop the handle and resume by id.
	resumed, err := bs.ResumeUpload(id)
	if err != nil {
		t.Fatalf("ResumeUpload: %v", err)
	}
	if resumed.ID() != id {
		t.Fatalf("ResumeUpload id=%s, want %s", resumed.ID(), id)
	}
	if got := resumed.Offset(); got != int64(len("part-1-")) {
		t.Fatalf("Offset after resume: %d", got)
	}
	if _, err := resumed.Write([]byte("part-2")); err != nil {
		t.Fatalf("Write after resume: %v", err)
	}
	want := sha256Of([]byte("part-1-part-2"))
	if _, err := resumed.Commit(want); err != nil {
		t.Fatalf("Commit after resume: %v", err)
	}
}

func testResumeMissing(t *testing.T, bs BlobStore) {
	if _, err := bs.ResumeUpload("00000000000000000000000000000000"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("ResumeUpload missing: want ErrSessionNotFound, got %v", err)
	}
}

func testDelete(t *testing.T, bs BlobStore) {
	data := []byte("to-be-deleted")
	want := sha256Of(data)
	commitBlob(t, bs, data, want)

	if err := bs.Delete(want); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := bs.Stat(want); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after Delete, Stat should be ErrNotFound, got %v", err)
	}
}

func testDeleteMissing(t *testing.T, bs BlobStore) {
	if err := bs.Delete(sha256Of([]byte("ghost"))); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing: want ErrNotFound, got %v", err)
	}
}

func testListBlobs(t *testing.T, bs BlobStore) {
	want := map[string]int64{}
	for i, payload := range [][]byte{[]byte("alpha"), []byte("beta"), []byte("gamma-gamma")} {
		_ = i
		d := sha256Of(payload)
		commitBlob(t, bs, payload, d)
		want[d.String()] = int64(len(payload))
	}

	got := map[string]int64{}
	err := bs.ListBlobs(func(d Digest, info *BlobInfo) error {
		got[d.String()] = info.Size
		return nil
	})
	if err != nil {
		t.Fatalf("ListBlobs: %v", err)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("ListBlobs missing %s (size %d), got size %d", k, v, got[k])
		}
	}
}

func testRange(t *testing.T, bs BlobStore) {
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i % 251)
	}
	d := sha256Of(data)
	commitBlob(t, bs, data, d)

	rc, _, err := bs.Get(d)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()

	// Seek to byte 512 and read 64 bytes.
	if _, err := rc.Seek(512, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	buf := make([]byte, 64)
	if _, err := io.ReadFull(rc, buf); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if !bytes.Equal(buf, data[512:576]) {
		t.Fatalf("range read mismatch")
	}
}

func testConcurrentReads(t *testing.T, bs BlobStore) {
	data := []byte("concurrent-payload")
	d := sha256Of(data)
	commitBlob(t, bs, data, d)

	const N = 32
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			rc, _, err := bs.Get(d)
			if err != nil {
				t.Errorf("concurrent Get: %v", err)
				return
			}
			defer rc.Close()
			got, err := io.ReadAll(rc)
			if err != nil {
				t.Errorf("ReadAll: %v", err)
				return
			}
			if !bytes.Equal(got, data) {
				t.Errorf("read mismatch")
			}
		}()
	}
	wg.Wait()
}

func testDoubleCommit(t *testing.T, bs BlobStore) {
	sess, err := bs.StartUpload()
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	if _, err := sess.Write([]byte("once")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	d := sha256Of([]byte("once"))
	if _, err := sess.Commit(d); err != nil {
		t.Fatalf("first Commit: %v", err)
	}
	// Second commit on a closed session must fail.
	if _, err := sess.Commit(d); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("second Commit: want ErrSessionClosed, got %v", err)
	}
	// Write must also fail.
	if _, err := sess.Write([]byte("after-close")); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("Write after close: want ErrSessionClosed, got %v", err)
	}
}

func testCancelIdempotent(t *testing.T, bs BlobStore) {
	sess, err := bs.StartUpload()
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	if err := sess.Cancel(); err != nil {
		t.Fatalf("first Cancel: %v", err)
	}
	if err := sess.Cancel(); err != nil {
		t.Fatalf("second Cancel: want nil (idempotent), got %v", err)
	}
}

// commitBlob is a helper used across tests: start a session, write
// data, commit at expected digest. Aborts the test on any error.
func commitBlob(t *testing.T, bs BlobStore, data []byte, expected Digest) {
	t.Helper()
	sess, err := bs.StartUpload()
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	if _, err := sess.Write(data); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := sess.Commit(expected); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

// TestMemBlobStore runs the full conformance suite against the
// in-memory implementation.
func TestMemBlobStore(t *testing.T) {
	runConformance(t, NewMemBlobStore())
}

// TestDigestParse covers parser shape rules separately — they aren't
// part of the conformance suite because they don't go through a
// BlobStore.
func TestDigestParse(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"sha256:" + hex.EncodeToString(make([]byte, 32)), false},
		{"sha512:" + hex.EncodeToString(make([]byte, 64)), false},
		{"sha256:short", true},
		{"sha256:", true},
		{":abc", true},
		{"unknown:00", true},
		{"sha256:NOT-HEX-AT-ALL-NOT-HEX-AT-ALL-NOT-HEX-AT-ALL-NOT-HEX-AT-ALL-XX", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			_, err := ParseDigest(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseDigest(%q) err=%v wantErr=%v", tc.in, err, tc.wantErr)
			}
		})
	}
}

func TestDigestEqualCaseInsensitive(t *testing.T) {
	a := Digest{Algorithm: "sha256", Hex: "ABCDEF"}
	b := Digest{Algorithm: "sha256", Hex: "abcdef"}
	if !a.Equal(b) {
		t.Fatal("Equal should ignore case on hex")
	}
}

func TestHashingWriter(t *testing.T) {
	buf := &bytes.Buffer{}
	hw, err := NewHashingWriter(buf, "sha256")
	if err != nil {
		t.Fatalf("NewHashingWriter: %v", err)
	}
	payload := []byte("streaming-payload")
	if n, err := hw.Write(payload); err != nil || n != len(payload) {
		t.Fatalf("Write n=%d err=%v", n, err)
	}
	if got := hw.BytesWritten(); got != int64(len(payload)) {
		t.Fatalf("BytesWritten %d", got)
	}
	if !bytes.Equal(buf.Bytes(), payload) {
		t.Fatalf("forwarded bytes %q", buf.Bytes())
	}
	want := sha256Of(payload)
	got := hw.Digest()
	if !got.Equal(want) {
		t.Fatalf("Digest %s, want %s", got, want)
	}
}
