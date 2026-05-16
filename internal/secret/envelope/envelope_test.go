package envelope_test

import (
	"crypto/rand"
	"errors"
	"testing"

	"github.com/rakunlabs/pika/internal/secret/crypto"
	"github.com/rakunlabs/pika/internal/secret/envelope"
	"github.com/rakunlabs/pika/internal/secret/keymgr"
)

func unlockedManager(t *testing.T) *keymgr.Manager {
	t.Helper()
	key := make([]byte, crypto.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	enc, err := crypto.NewChaCha20(key)
	if err != nil {
		t.Fatalf("new chacha: %v", err)
	}
	m := keymgr.New()
	if err := m.Unlock(enc); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	return m
}

// TestRoundTrip — encrypted blob decrypts back to the same bytes,
// magic byte sits at position 0, and length is plaintext+1+overhead
// (we don't pin the exact overhead since chacha20-poly1305 may
// future-proof its tag — only structural shape).
func TestRoundTrip(t *testing.T) {
	m := unlockedManager(t)
	plain := []byte("hello world")

	sealed, err := envelope.Seal(m, plain)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if len(sealed) <= len(plain)+1 {
		t.Fatalf("sealed blob suspiciously short: %d bytes", len(sealed))
	}
	if sealed[0] != 0x01 {
		t.Fatalf("magic byte: want 0x01, got 0x%02x", sealed[0])
	}

	got, err := envelope.Open(m, sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatalf("Open returned %q, want %q", got, plain)
	}
}

// TestSealLockedFailsClosed — sealing while locked must error so
// callers don't accidentally store plaintext. The wrapped sentinel
// is what storage.Tx() inspects to roll back.
func TestSealLockedFailsClosed(t *testing.T) {
	m := keymgr.New() // never unlocked

	if _, err := envelope.Seal(m, []byte("data")); !errors.Is(err, envelope.ErrLocked) {
		t.Fatalf("Seal on locked manager: got %v, want ErrLocked", err)
	}
}

// TestOpenLockedFailsClosed — same property for reads. The HTTP
// layer turns this into a 503 (via the lockgate; this is the inner
// fallback for routes that bypass the gate, e.g. background jobs).
func TestOpenLockedFailsClosed(t *testing.T) {
	m := unlockedManager(t)
	sealed, err := envelope.Seal(m, []byte("data"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	m.Lock()

	if _, err := envelope.Open(m, sealed); !errors.Is(err, envelope.ErrLocked) {
		t.Fatalf("Open on locked manager: got %v, want ErrLocked", err)
	}
}

// TestUnknownFormat — bytes with a bogus leading byte are rejected
// structurally (not as decryption failures). Important for
// migrations where we want to distinguish "this row predates the
// envelope format" from "wrong key".
func TestUnknownFormat(t *testing.T) {
	m := unlockedManager(t)
	bogus := []byte{0xff, 0x00, 0x00}

	_, err := envelope.Open(m, bogus)
	if !errors.Is(err, envelope.ErrUnknownFormat) {
		t.Fatalf("Open(bogus): got %v, want ErrUnknownFormat", err)
	}
}

// TestEmptyInput — zero-length blob is treated as unknown-format,
// not as an empty-but-valid envelope.
func TestEmptyInput(t *testing.T) {
	m := unlockedManager(t)
	_, err := envelope.Open(m, nil)
	if !errors.Is(err, envelope.ErrUnknownFormat) {
		t.Fatalf("Open(nil): got %v, want ErrUnknownFormat", err)
	}
}

// TestIsSealedHeuristic — the helper is only as good as the magic
// byte; verify the obvious matches and rejects.
func TestIsSealedHeuristic(t *testing.T) {
	if envelope.IsSealed(nil) {
		t.Errorf("IsSealed(nil) = true, want false")
	}
	if envelope.IsSealed([]byte{0xff}) {
		t.Errorf("IsSealed(0xff) = true, want false")
	}
	if !envelope.IsSealed([]byte{0x01, 0x00}) {
		t.Errorf("IsSealed(0x01..) = false, want true")
	}
}

// TestWrongKeyFails — re-opening with a different key surfaces a
// decryption error rather than returning garbage. The exact error
// is the chacha layer's; we just check the call fails.
func TestWrongKeyFails(t *testing.T) {
	m1 := unlockedManager(t)
	sealed, err := envelope.Seal(m1, []byte("secret"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	m2 := unlockedManager(t)
	if _, err := envelope.Open(m2, sealed); err == nil {
		t.Fatalf("Open with wrong key returned no error")
	}
}
