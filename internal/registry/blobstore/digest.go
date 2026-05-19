package blobstore

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"strings"
)

// Digest pairs an algorithm name with a hex-encoded fingerprint. The
// canonical string form is "algorithm:hex" (OCI distribution spec
// vocabulary), e.g. "sha256:abc123...". That format is what every
// protocol head sees on the wire (Docker, OCI registries, NPM
// integrity strings differ — see helpers below).
//
// Supported algorithms:
//
//	"sha256" — Docker / OCI default, Go modules, generic.
//	"sha512" — NPM integrity ("sha512-..." base64; we store hex here).
//
// New algorithms are added by extending NewHasher and Validate; the
// type itself doesn't enumerate them so we can add new ones without
// API churn in handlers.
type Digest struct {
	Algorithm string
	Hex       string
}

// String returns the canonical "algorithm:hex" form. Empty when the
// digest is the zero value.
func (d Digest) String() string {
	if d.Algorithm == "" || d.Hex == "" {
		return ""
	}
	return d.Algorithm + ":" + d.Hex
}

// IsZero reports whether the digest is the zero value. Used by upload
// handlers to detect "no expected digest was supplied".
func (d Digest) IsZero() bool {
	return d.Algorithm == "" && d.Hex == ""
}

// Equal compares two digests case-insensitively on the hex segment.
// Algorithm names are compared exactly because every spec we host
// uses lowercase canonical names.
func (d Digest) Equal(other Digest) bool {
	if d.Algorithm != other.Algorithm {
		return false
	}
	return strings.EqualFold(d.Hex, other.Hex)
}

// ParseDigest parses a canonical "algorithm:hex" string. Returns an
// error when the shape is wrong or the algorithm is unknown.
//
// Hex length is verified against the algorithm's natural output
// length (sha256: 64, sha512: 128). That stops malicious clients
// from supplying a truncated digest that happens to share a prefix.
func ParseDigest(s string) (Digest, error) {
	idx := strings.IndexByte(s, ':')
	if idx <= 0 || idx == len(s)-1 {
		return Digest{}, fmt.Errorf("digest %q: missing algorithm:hex separator", s)
	}
	alg := s[:idx]
	hx := s[idx+1:]

	wantLen, ok := algorithmHexLength(alg)
	if !ok {
		return Digest{}, fmt.Errorf("digest %q: unknown algorithm %q", s, alg)
	}
	if len(hx) != wantLen {
		return Digest{}, fmt.Errorf("digest %q: hex length %d, expected %d for %s", s, len(hx), wantLen, alg)
	}
	if _, err := hex.DecodeString(hx); err != nil {
		return Digest{}, fmt.Errorf("digest %q: invalid hex: %w", s, err)
	}
	// Normalize hex to lowercase. Specs allow either case on the
	// wire but storage keys must be deterministic.
	return Digest{Algorithm: alg, Hex: strings.ToLower(hx)}, nil
}

// algorithmHexLength returns the expected hex length for the named
// algorithm. Used by ParseDigest. ok=false means the algorithm is
// not supported.
func algorithmHexLength(alg string) (int, bool) {
	switch alg {
	case "sha256":
		return sha256.Size * 2, true
	case "sha512":
		return sha512.Size * 2, true
	default:
		return 0, false
	}
}

// NewHasher returns an io.Writer-like hash.Hash for the named
// algorithm. The returned hasher computes the digest as bytes are
// fed into it; call Sum() to finalise. Returns an error for unknown
// algorithms.
func NewHasher(alg string) (hash.Hash, error) {
	switch alg {
	case "sha256":
		return sha256.New(), nil
	case "sha512":
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("unsupported digest algorithm %q", alg)
	}
}

// DigestFromHash extracts the canonical Digest from a finalised
// hash. The algorithm is recovered from the hash's Size: sha256
// hashes report Size 32, sha512 hashes report Size 64.
func DigestFromHash(alg string, h hash.Hash) Digest {
	return Digest{
		Algorithm: alg,
		Hex:       hex.EncodeToString(h.Sum(nil)),
	}
}

// HashingWriter wraps an io.Writer with a streaming hasher. Bytes
// are forwarded to the wrapped writer and simultaneously fed to the
// hash. Use the constructor NewHashingWriter so the algorithm is
// validated up front.
//
// Used by upload sessions to compute the digest on the fly without
// re-reading the data.
type HashingWriter struct {
	w   io.Writer
	h   hash.Hash
	alg string
	n   int64
}

// NewHashingWriter constructs a HashingWriter that forwards to w and
// computes the named algorithm's digest of every byte written.
func NewHashingWriter(w io.Writer, alg string) (*HashingWriter, error) {
	h, err := NewHasher(alg)
	if err != nil {
		return nil, err
	}
	return &HashingWriter{w: w, h: h, alg: alg}, nil
}

// Write implements io.Writer. Returns the number of bytes consumed
// by the wrapped writer; the hash is updated by exactly that count.
func (hw *HashingWriter) Write(p []byte) (int, error) {
	n, err := hw.w.Write(p)
	if n > 0 {
		// Hash exactly what the wrapped writer accepted; a short
		// write must not poison the digest with bytes that never
		// reached the underlying stream.
		_, _ = hw.h.Write(p[:n])
		hw.n += int64(n)
	}
	return n, err
}

// Digest finalises and returns the streaming digest. Calling Digest
// does not Reset the hasher — additional Write calls continue from
// where the snapshot was taken (matches hash.Hash semantics).
func (hw *HashingWriter) Digest() Digest {
	return DigestFromHash(hw.alg, hw.h)
}

// BytesWritten returns the cumulative byte count consumed by the
// wrapped writer.
func (hw *HashingWriter) BytesWritten() int64 {
	return hw.n
}
