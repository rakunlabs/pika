package service_test

import (
	"errors"
	"testing"

	"github.com/rakunlabs/pika/internal/secret"
	"github.com/rakunlabs/pika/internal/secret/keymgr"
	"github.com/rakunlabs/pika/internal/service"
	bwstore "github.com/rakunlabs/pika/internal/storage/bw"
)

// newKeyopsService builds a Service backed by a secret.Storage
// wrapper with an attached *keymgr.Manager. Returns the service +
// the manager so tests can inspect lock state directly.
//
// Each test gets a fresh in-memory Badger store; the manager
// starts locked. Tests choose when to initialize / unlock.
func newKeyopsService(t *testing.T) (*service.Service, *keymgr.Manager) {
	t.Helper()

	store, err := bwstore.New(t.Context(), &bwstore.Config{InMemory: true})
	if err != nil {
		t.Fatalf("bw.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mgr := keymgr.New()
	enc := secret.New(store, mgr)
	svc := service.New(enc)
	svc.SetKeyManager(mgr)
	return svc, mgr
}

// TestInitializeUnlocksManager — InitializeServerKey must leave
// the manager in the unlocked state and write a verifier so a
// subsequent UnlockServerKey works.
func TestInitializeUnlocksManager(t *testing.T) {
	svc, mgr := newKeyopsService(t)

	if mgr.IsUnlocked() {
		t.Fatalf("manager unlocked before initialize")
	}

	if err := svc.InitializeServerKey(t.Context(), "operator-passphrase"); err != nil {
		t.Fatalf("InitializeServerKey: %v", err)
	}

	if !mgr.IsUnlocked() {
		t.Errorf("manager still locked after initialize")
	}

	st, err := svc.GetKeyStatus(t.Context())
	if err != nil {
		t.Fatalf("GetKeyStatus: %v", err)
	}
	if !st.Initialized || !st.Unlocked {
		t.Errorf("status = %+v, want both true", st)
	}
}

// TestInitializeRejectsSecondCall — a verifier is one-shot. The
// second initialize attempt must return ErrConflict so a misclick
// can't blow away the verifier.
func TestInitializeRejectsSecondCall(t *testing.T) {
	svc, _ := newKeyopsService(t)

	if err := svc.InitializeServerKey(t.Context(), "k1"); err != nil {
		t.Fatalf("first initialize: %v", err)
	}
	err := svc.InitializeServerKey(t.Context(), "k2")
	if !errors.Is(err, service.ErrConflict) {
		t.Errorf("second initialize: got %v, want ErrConflict", err)
	}
}

// TestUnlockRoundTrip — initialize → lock → unlock with the same
// key works; unlock with a different key fails with ErrForbidden
// and leaves the manager locked.
func TestUnlockRoundTrip(t *testing.T) {
	svc, mgr := newKeyopsService(t)

	if err := svc.InitializeServerKey(t.Context(), "right-key"); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	mgr.Lock()
	if mgr.IsUnlocked() {
		t.Fatalf("manager still unlocked after explicit Lock")
	}

	// Wrong key first.
	if err := svc.UnlockServerKey(t.Context(), "wrong-key"); !errors.Is(err, service.ErrForbidden) {
		t.Errorf("wrong-key unlock: got %v, want ErrForbidden", err)
	}
	if mgr.IsUnlocked() {
		t.Errorf("wrong-key unlock left manager unlocked")
	}

	// Right key.
	if err := svc.UnlockServerKey(t.Context(), "right-key"); err != nil {
		t.Errorf("right-key unlock: %v", err)
	}
	if !mgr.IsUnlocked() {
		t.Errorf("right-key unlock left manager locked")
	}
}

// TestUnlockBeforeInitializeFails — calling Unlock before any
// verifier exists must return a clear "not initialized" error so
// the SPA can route to the initialize form.
func TestUnlockBeforeInitializeFails(t *testing.T) {
	svc, _ := newKeyopsService(t)

	err := svc.UnlockServerKey(t.Context(), "anything")
	if !errors.Is(err, service.ErrBadRequest) {
		t.Errorf("unlock before init: got %v, want ErrBadRequest", err)
	}
}

// TestRotatePreservesVerifierIdentity — after rotation the new
// key must unlock the server (post-restart simulation), and the
// old key must NOT.
func TestRotatePreservesVerifierIdentity(t *testing.T) {
	svc, mgr := newKeyopsService(t)

	if err := svc.InitializeServerKey(t.Context(), "old-key"); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := svc.RotateServerKey(t.Context(), "old-key", "new-key"); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	// Simulate restart.
	mgr.Lock()

	if err := svc.UnlockServerKey(t.Context(), "old-key"); !errors.Is(err, service.ErrForbidden) {
		t.Errorf("old key unlock after rotate: got %v, want ErrForbidden", err)
	}
	if err := svc.UnlockServerKey(t.Context(), "new-key"); err != nil {
		t.Errorf("new key unlock after rotate: %v", err)
	}
}

// TestRotateRejectsSameKey — rotation to the same key is a no-op
// at best and a footgun at worst (operator likely meant to type a
// different new key). Reject explicitly so the typo surfaces.
func TestRotateRejectsSameKey(t *testing.T) {
	svc, _ := newKeyopsService(t)
	if err := svc.InitializeServerKey(t.Context(), "k"); err != nil {
		t.Fatalf("init: %v", err)
	}
	err := svc.RotateServerKey(t.Context(), "k", "k")
	if !errors.Is(err, service.ErrBadRequest) {
		t.Errorf("same-key rotate: got %v, want ErrBadRequest", err)
	}
}

// TestRotateRejectsWrongCurrentKey — the rotation flow validates
// `current_key` against the verifier before swapping. Wrong
// current must surface as ErrForbidden, no state change.
func TestRotateRejectsWrongCurrentKey(t *testing.T) {
	svc, mgr := newKeyopsService(t)
	if err := svc.InitializeServerKey(t.Context(), "right"); err != nil {
		t.Fatalf("init: %v", err)
	}
	err := svc.RotateServerKey(t.Context(), "wrong", "new")
	if !errors.Is(err, service.ErrForbidden) {
		t.Errorf("wrong current: got %v, want ErrForbidden", err)
	}
	// Verify the live encryptor wasn't disturbed.
	if !mgr.IsUnlocked() {
		t.Errorf("rejected rotation accidentally locked the manager")
	}
	// Original key still works (round-trip via lock + unlock).
	mgr.Lock()
	if err := svc.UnlockServerKey(t.Context(), "right"); err != nil {
		t.Errorf("original key broken after rejected rotate: %v", err)
	}
}
