package blobstore

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

// TestS3Integration runs against a real S3-compatible server when
// PIKA_TEST_S3_ENDPOINT is set (e.g. a local MinIO):
//
//	PIKA_TEST_S3_ENDPOINT=http://127.0.0.1:9000 PIKA_TEST_S3_BUCKET=vault \
//	PIKA_TEST_S3_ACCESS_KEY=minioadmin PIKA_TEST_S3_SECRET_KEY=minioadmin go test ./internal/blobstore
func TestS3Integration(t *testing.T) {
	endpoint := os.Getenv("PIKA_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("PIKA_TEST_S3_ENDPOINT not set")
	}
	s, err := NewS3(S3Config{
		Endpoint:        endpoint,
		Bucket:          os.Getenv("PIKA_TEST_S3_BUCKET"),
		Prefix:          "pika-test",
		AccessKeyID:     os.Getenv("PIKA_TEST_S3_ACCESS_KEY"),
		SecretAccessKey: os.Getenv("PIKA_TEST_S3_SECRET_KEY"),
		UsePathStyle:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Check(ctx); err != nil {
		t.Fatal("check:", err)
	}
	data := strings.Repeat("pika vault file ", 100000)
	key := "vault/u1/a b+c$.bin"
	if err := s.Put(ctx, key, strings.NewReader(data), int64(len(data)), "application/octet-stream"); err != nil {
		t.Fatal("put:", err)
	}
	rc, err := s.Get(ctx, key)
	if err != nil {
		t.Fatal("get:", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != data {
		t.Fatalf("content mismatch: %d vs %d bytes", len(got), len(data))
	}
	if err := s.Put(ctx, "vault/u1/empty", strings.NewReader(""), 0, ""); err != nil {
		t.Fatal("put empty:", err)
	}
	for _, k := range []string{key, "vault/u1/empty", key} {
		if err := s.Delete(ctx, k); err != nil {
			t.Fatal("delete:", err)
		}
	}
	if _, err := s.Get(ctx, key); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
