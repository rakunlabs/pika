package blobstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestS3SignatureFixedVector uses AWS's published "PUT Object, single
// chunk" example so a refactor of canonicalisation cannot silently break
// signing.
func TestS3SignatureFixedVector(t *testing.T) {
	const wantAuth = "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=date;host;x-amz-content-sha256;x-amz-date;x-amz-storage-class, " +
		"Signature=7c0f3caf24a16d5948905b8ebf67d29fb415e93fddaed9ca6aeb5ac2348cfee4"

	s, err := NewS3(S3Config{
		Endpoint:        "https://s3.amazonaws.com",
		Region:          "us-east-1",
		Bucket:          "examplebucket",
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("Welcome to Amazon S3.")
	sum := sha256.Sum256(payload)

	u := s.objectURL("test$file.text")
	if u.Host != "examplebucket.s3.amazonaws.com" || u.EscapedPath() != "/test%24file.text" {
		t.Fatalf("url %s %s", u.Host, u.EscapedPath())
	}
	req, _ := http.NewRequest(http.MethodPut, u.String(), bytes.NewReader(payload))
	req.Header.Set("Date", "Fri, 24 May 2013 00:00:00 GMT")
	req.Header.Set("X-Amz-Storage-Class", "REDUCED_REDUNDANCY")
	s.sign(req, hex.EncodeToString(sum[:]), time.Date(2013, time.May, 24, 0, 0, 0, 0, time.UTC))

	if got := req.Header.Get("Authorization"); got != wantAuth {
		t.Fatalf("authorization:\n got %s\nwant %s", got, wantAuth)
	}
}

func TestS3PathStyleURL(t *testing.T) {
	s, err := NewS3(S3Config{
		Endpoint: "http://127.0.0.1:9000", Bucket: "b", Prefix: "/pika/",
		AccessKeyID: "a", SecretAccessKey: "s", UsePathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	full, err := s.fullKey("u/x y")
	if err != nil {
		t.Fatal(err)
	}
	u := s.objectURL(full)
	if u.Host != "127.0.0.1:9000" || u.EscapedPath() != "/b/pika/u/x%20y" {
		t.Fatalf("got %s %s", u.Host, u.EscapedPath())
	}
}

func TestValidateKey(t *testing.T) {
	for _, k := range []string{"", "/a", "a//b", "a/../b", "a\\b", "a/./b", "a\x00b"} {
		if ValidateKey(k) == nil {
			t.Errorf("expected %q to be rejected", k)
		}
	}
	if err := ValidateKey("vault/user/abc"); err != nil {
		t.Fatal(err)
	}
}

func TestLocalRoundTrip(t *testing.T) {
	ctx := context.Background()
	l, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Check(ctx); err != nil {
		t.Fatal(err)
	}
	data := "hello world"
	if err := l.Put(ctx, "a/b/c", strings.NewReader(data), int64(len(data)), ""); err != nil {
		t.Fatal(err)
	}
	rc, err := l.Get(ctx, "a/b/c")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != data {
		t.Fatalf("got %q", got)
	}
	if err := l.Delete(ctx, "a/b/c"); err != nil {
		t.Fatal(err)
	}
	if err := l.Delete(ctx, "a/b/c"); err != nil {
		t.Fatal("delete must be idempotent:", err)
	}
	if _, err := l.Get(ctx, "a/b/c"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
