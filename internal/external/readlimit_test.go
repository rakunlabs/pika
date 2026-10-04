package external

import (
	"errors"
	"strings"
	"testing"
)

func TestReadBodyLimit(t *testing.T) {
	t.Cleanup(func() { SetMaxResponseBytes(DefaultMaxResponseBytes) })

	SetMaxResponseBytes(10)
	if b, err := readBody(strings.NewReader("0123456789")); err != nil || len(b) != 10 {
		t.Fatalf("at limit: len=%d err=%v", len(b), err)
	}
	if _, err := readBody(strings.NewReader("0123456789x")); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("over limit: err=%v, want ErrResponseTooLarge", err)
	}

	SetMaxResponseBytes(0)
	if b, err := readBody(strings.NewReader(strings.Repeat("a", 100))); err != nil || len(b) != 100 {
		t.Fatalf("unlimited: len=%d err=%v", len(b), err)
	}
}
