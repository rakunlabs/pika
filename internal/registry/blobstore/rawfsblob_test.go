package blobstore

import (
	"path/filepath"
	"testing"

	"github.com/rakunlabs/pika/internal/rawfs/localfs"
)

// TestRawFSBlobStore_OverLocalFS runs the full conformance suite
// against the RawFSBlobStore adapter wrapped around the local-fs
// rawfs implementation. This exercises every path through the
// adapter without needing a real S3/SFTP/WebDAV backend.
func TestRawFSBlobStore_OverLocalFS(t *testing.T) {
	dir := t.TempDir()
	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("localfs.New: %v", err)
	}
	bs, err := NewRawFSBlobStore(fs, "registry")
	if err != nil {
		t.Fatalf("NewRawFSBlobStore: %v", err)
	}
	runConformance(t, bs)
}

func TestRawFSBlobStore_BasePathLayout(t *testing.T) {
	dir := t.TempDir()
	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("localfs.New: %v", err)
	}
	bs, err := NewRawFSBlobStore(fs, "regs/acme")
	if err != nil {
		t.Fatalf("NewRawFSBlobStore: %v", err)
	}

	data := []byte("layout-test")
	d := sha256Of(data)
	commitBlob(t, bs, data, d)

	want := filepath.Join(dir, "regs", "acme", "blobs", "sha256", d.Hex[:2], d.Hex)
	if _, err := filepath.Abs(want); err != nil {
		t.Fatalf("abs: %v", err)
	}
	// Validate it's on disk.
	if _, err := fs.Stat("regs/acme/blobs/sha256/" + d.Hex[:2] + "/" + d.Hex); err != nil {
		t.Fatalf("expected blob under basePath, got %v", err)
	}
}

func TestRawFSBlobStore_NoBasePath(t *testing.T) {
	dir := t.TempDir()
	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("localfs.New: %v", err)
	}
	bs, err := NewRawFSBlobStore(fs, "")
	if err != nil {
		t.Fatalf("NewRawFSBlobStore: %v", err)
	}
	data := []byte("no-base")
	d := sha256Of(data)
	commitBlob(t, bs, data, d)

	if _, err := fs.Stat("blobs/sha256/" + d.Hex[:2] + "/" + d.Hex); err != nil {
		t.Fatalf("expected blob at root, got %v", err)
	}
}

func TestRawFSBlobStore_MemoryLimit(t *testing.T) {
	dir := t.TempDir()
	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("localfs.New: %v", err)
	}
	bs, err := NewRawFSBlobStore(fs, "", WithMemoryLimit(64))
	if err != nil {
		t.Fatalf("NewRawFSBlobStore: %v", err)
	}
	s, err := bs.StartUpload()
	if err != nil {
		t.Fatalf("StartUpload: %v", err)
	}
	defer s.Cancel()

	// First write fits within the 64-byte limit.
	if _, err := s.Write(make([]byte, 32)); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	// Second write totals 96 bytes — should be partially accepted
	// (32 more) and return an error.
	n, err := s.Write(make([]byte, 64))
	if err == nil {
		t.Fatal("expected memory-limit error on second Write")
	}
	if n != 32 {
		t.Fatalf("partial Write: got n=%d, want 32", n)
	}
}

func TestRawFSBlobStore_NilFS(t *testing.T) {
	if _, err := NewRawFSBlobStore(nil, ""); err == nil {
		t.Fatal("expected error for nil fs")
	}
}
