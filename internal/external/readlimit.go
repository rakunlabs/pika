package external

import (
	"errors"
	"fmt"
	"io"
	"sync/atomic"
)

// DefaultMaxResponseBytes is the default cap on a single response body
// read from an external backend.
const DefaultMaxResponseBytes int64 = 16 << 20

// ErrResponseTooLarge is returned when an external backend response
// exceeds the configured limit.
var ErrResponseTooLarge = errors.New("external response exceeds size limit")

var maxResponseBytes atomic.Int64

func init() {
	maxResponseBytes.Store(DefaultMaxResponseBytes)
}

// SetMaxResponseBytes sets the cap on external backend response bodies.
// n <= 0 disables the limit.
func SetMaxResponseBytes(n int64) {
	maxResponseBytes.Store(n)
}

// MaxResponseBytes returns the current external response cap (0 = unlimited).
func MaxResponseBytes() int64 {
	if n := maxResponseBytes.Load(); n > 0 {
		return n
	}
	return 0
}

// readBody reads r up to the configured limit and fails with
// ErrResponseTooLarge instead of buffering an unbounded body.
func readBody(r io.Reader) ([]byte, error) {
	limit := MaxResponseBytes()
	if limit == 0 {
		return io.ReadAll(r)
	}
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%w (%d bytes)", ErrResponseTooLarge, limit)
	}
	return b, nil
}

// readErrorBody reads at most a small prefix of an error response, for
// inclusion in logs and error messages.
func readErrorBody(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 4<<10))
	return string(b)
}
