// Package blobstore stores opaque binary objects on a pluggable backend
// (local disk or S3-compatible object storage). It is used by the personal
// vault file browser: metadata (names, folders, sizes) lives in the main
// database, the bytes live here under immutable keys.
package blobstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrNotFound is returned by Get when the object does not exist.
var ErrNotFound = errors.New("blob not found")

// KeyMaxBytes bounds the object key length (S3's own limit).
const KeyMaxBytes = 1024

// Store is the minimal object-store contract.
type Store interface {
	// Put streams size bytes from r into key, replacing any existing
	// object. size must be the exact payload length.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	// Get opens the object. The returned reader may implement
	// io.ReadSeeker (local disk) so callers can serve Range requests.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete is idempotent: a missing object is not an error.
	Delete(ctx context.Context, key string) error
	// Check probes connectivity and writability.
	Check(ctx context.Context) error
}

// ValidateKey rejects keys that could escape the store root or confuse
// S3 path handling.
func ValidateKey(key string) error {
	if key == "" || len(key) > KeyMaxBytes {
		return fmt.Errorf("invalid blob key length")
	}
	if strings.HasPrefix(key, "/") || strings.ContainsRune(key, '\\') {
		return fmt.Errorf("blob key must be relative")
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("blob key contains an invalid segment")
		}
	}
	for i := 0; i < len(key); i++ {
		if c := key[i]; c < 0x20 || c == 0x7f {
			return fmt.Errorf("blob key contains control characters")
		}
	}
	return nil
}
