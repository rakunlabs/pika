package blobstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLocalBlobStore runs the BlobStore conformance suite against a
// fresh temp-directory-backed store. Each subtest gets its own fresh
// store so test order doesn't matter.
func TestLocalBlobStore(t *testing.T) {
	dir := t.TempDir()
	bs, err := NewLocalBlobStore(dir)
	if err != nil {
		t.Fatalf("NewLocalBlobStore: %v", err)
	}
	runConformance(t, bs)
}

func TestLocalBlobStore_NewRejectsEmptyRoot(t *testing.T) {
	if _, err := NewLocalBlobStore(""); err == nil {
		t.Fatal("expected error for empty root")
	}
}

func TestLocalBlobStore_CreatesLayout(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewLocalBlobStore(dir); err != nil {
		t.Fatalf("NewLocalBlobStore: %v", err)
	}
	for _, sub := range []string{"blobs", "_uploads"} {
		st, err := os.Stat(filepath.Join(dir, sub))
		if err != nil {
			t.Fatalf("expected %s subdirectory, got %v", sub, err)
		}
		if !st.IsDir() {
			t.Fatalf("%s is not a directory", sub)
		}
	}
}

func TestLocalBlobStore_BlobPathSharding(t *testing.T) {
	dir := t.TempDir()
	bs, err := NewLocalBlobStore(dir)
	if err != nil {
		t.Fatalf("NewLocalBlobStore: %v", err)
	}
	data := []byte("sharding-test")
	d := sha256Of(data)
	commitBlob(t, bs, data, d)

	// Verify the on-disk layout: blobs/sha256/{first2}/{full}.
	want := filepath.Join(dir, "blobs", "sha256", d.Hex[:2], d.Hex)
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected blob at %s, got %v", want, err)
	}
}

func TestLocalBlobStore_PruneAbandonedUploads(t *testing.T) {
	dir := t.TempDir()
	bs, err := NewLocalBlobStore(dir)
	if err != nil {
		t.Fatalf("NewLocalBlobStore: %v", err)
	}

	// Live session — must NOT be pruned.
	live, err := bs.StartUpload()
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	defer live.Cancel()

	// Abandoned tmp file — write it directly and back-date its mtime.
	abandonedPath := filepath.Join(dir, "_uploads", "abandoned-id")
	if err := os.WriteFile(abandonedPath, []byte("abandoned"), 0o644); err != nil {
		t.Fatalf("write abandoned: %v", err)
	}
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(abandonedPath, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// Dry-run first: must match exactly what the destructive pass
	// would and leave the file in place.
	dryCount, dryBytes, err := bs.PruneAbandonedUploads(24*time.Hour, true)
	if err != nil {
		t.Fatalf("PruneAbandonedUploads dry: %v", err)
	}
	if dryCount != 1 {
		t.Fatalf("dry count = %d, want 1", dryCount)
	}
	if dryBytes != int64(len("abandoned")) {
		t.Fatalf("dry bytes = %d, want %d", dryBytes, len("abandoned"))
	}
	if _, err := os.Stat(abandonedPath); err != nil {
		t.Fatalf("dry-run removed file (it shouldn't): %v", err)
	}

	removed, bytes, err := bs.PruneAbandonedUploads(24*time.Hour, false)
	if err != nil {
		t.Fatalf("PruneAbandonedUploads: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if bytes != int64(len("abandoned")) {
		t.Fatalf("bytes = %d, want %d", bytes, len("abandoned"))
	}
	if _, err := os.Stat(abandonedPath); !os.IsNotExist(err) {
		t.Fatalf("abandoned file should be gone, got %v", err)
	}
	// Live session's tmp file must still exist.
	livePath := filepath.Join(dir, "_uploads", live.ID())
	if _, err := os.Stat(livePath); err != nil {
		t.Fatalf("live session file disappeared: %v", err)
	}
}

func TestLocalBlobStore_RestartReinvocation(t *testing.T) {
	// Sanity: closing and re-opening the store over the same dir
	// keeps committed blobs but loses in-flight sessions (by design;
	// process-local state). Validates that the on-disk layout is the
	// only durable surface.
	dir := t.TempDir()

	bs1, err := NewLocalBlobStore(dir)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	data := []byte("survives-restart")
	d := sha256Of(data)
	commitBlob(t, bs1, data, d)

	// "Restart": create a second store rooted at the same dir.
	bs2, err := NewLocalBlobStore(dir)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	if _, err := bs2.Stat(d); err != nil {
		t.Fatalf("blob lost across restart: %v", err)
	}
}

func TestLocalBlobStore_ConcurrentCommitSameContent(t *testing.T) {
	// Two sessions writing the same payload commit successfully. The
	// second commit observes the rename target already exists and
	// treats it as a no-op (CAS dedupe).
	dir := t.TempDir()
	bs, err := NewLocalBlobStore(dir)
	if err != nil {
		t.Fatalf("NewLocalBlobStore: %v", err)
	}
	data := []byte("dedup-payload")
	d := sha256Of(data)

	s1, _ := bs.StartUpload()
	s2, _ := bs.StartUpload()
	if _, err := s1.Write(data); err != nil {
		t.Fatalf("s1 write: %v", err)
	}
	if _, err := s2.Write(data); err != nil {
		t.Fatalf("s2 write: %v", err)
	}
	if _, err := s1.Commit(d); err != nil {
		t.Fatalf("s1 commit: %v", err)
	}
	if _, err := s2.Commit(d); err != nil {
		t.Fatalf("s2 commit (dedup): %v", err)
	}
	if _, err := bs.Stat(d); err != nil {
		t.Fatalf("Stat: %v", err)
	}
}
