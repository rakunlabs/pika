// Package keymgr owns the lifecycle of the server-side encryption key
// (the at-rest key used by internal/secret.Storage to wrap/unwrap
// encrypted columns).
//
// Background — what this replaces:
//
// Pika previously read the at-rest key from `cfg.Secret.EncryptionKey`
// (env var PIKA_SECRET_ENCRYPTION_KEY), SHA-256'd it on boot, and held
// the resulting AEAD instance immutably for the process lifetime. That
// approach has two operational problems:
//
//  1. The key sits on disk in config.yaml or in the process's env
//     block — visible to any operator who can `cat` the file or
//     `cat /proc/$pid/environ`. A compromised host operator gets the
//     key for free.
//  2. There's no way to rotate without redeploying with a new env
//     and restarting; the rotation API is a no-op precisely because
//     the key was an immutable boot constant.
//
// The Manager flips the model: the server starts in a "locked" state
// with no key in memory. An administrator unlocks the server through
// an authenticated HTTP endpoint (POST /api/v1/key/unlock); the key
// lives in the Manager's atomic pointer for the rest of the process
// lifetime. A restart re-locks. This mirrors HashiCorp Vault's
// sealed/unsealed pattern, scoped to Pika's single-key model.
//
// Concurrency: every consumer of the encryption key (storage layer,
// rotation handler, status endpoint) reads through a single
// atomic.Pointer. Reads are lock-free; the only contended path is
// the unlock/lock/rotate transition, which is rare and synchronized
// via a sync.Mutex. We deliberately use atomic.Pointer rather than a
// mutex-protected field for reads because hot paths (Storage.Encrypt)
// will call this on every encrypted operation; an RWMutex would add a
// fence on each call and the read pattern is "load once, reuse across
// many operations" which the atomic primitive handles cleanly.
//
// Note: the Manager itself does NOT hash arbitrary key material into
// 32 bytes for callers — the chacha20-poly1305 cipher requires
// exactly KeySize bytes, so callers must supply pre-derived key
// material (typically SHA-256 of an admin-entered passphrase). This
// keeps the KDF policy in one place (the service/keyops layer) and
// out of the storage hot path.
package keymgr

import (
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/rakunlabs/pika/internal/secret/crypto"
)

// ErrLocked is returned by State() / Encryptor() when the server has
// not yet been unlocked. Storage helpers translate this into HTTP 503
// at the API layer; service code may inspect it directly to decide
// whether to attempt a fallback (e.g. the bootstrap-time settings
// reader skips encrypted fields when the manager is locked).
var ErrLocked = errors.New("server encryption key is locked")

// State describes the externally visible lifecycle of the key.
//
// Initialized vs Unlocked is a deliberate split:
//   - Initialized: a verifier record exists in the database. This is
//     a one-time event that happens the first time an admin sets the
//     server key. Future restarts find the verifier already there.
//   - Unlocked: the key is currently loaded into memory. False on
//     every fresh boot (the Manager doesn't persist the key
//     anywhere; that's the whole point).
//
// The combination matters for the UI: a fresh install needs an
// "Initialize" form (set the key for the first time + write
// verifier); a returning install needs an "Unlock" form (verify
// against the existing verifier). The endpoints are separate
// (POST /api/v1/key/initialize vs /api/v1/key/unlock) so the
// service layer can reject an "initialize" call when a verifier
// already exists, instead of silently overwriting it and rendering
// previously-encrypted data unrecoverable.
type State struct {
	Initialized bool `json:"initialized"`
	Unlocked    bool `json:"unlocked"`
}

// Manager owns the live encryptor. Its zero value is NOT usable —
// callers must go through New().
type Manager struct {
	// active is the live encryptor or nil when locked. Stored via
	// atomic.Pointer so hot-path readers (Storage column ops) don't
	// block on a mutex.
	active atomic.Pointer[crypto.Encryptor]

	// initialized tracks whether a verifier record exists. Updated
	// only inside the transition mutex. Boolean is fine here — no
	// reader treats this as a perf-critical field.
	initialized atomic.Bool

	// transitionMu serializes Init/Unlock/Lock/Rotate. The atomic
	// fields above stay consistent because every transition takes
	// this lock before swapping the pointer + flipping the bool.
	transitionMu sync.Mutex
}

// New constructs a fresh Manager in the "locked, uninitialized"
// state. The caller (cmd/pika) is expected to call MarkInitialized()
// after consulting the database to discover whether a verifier
// record already exists.
func New() *Manager {
	return &Manager{}
}

// State returns a snapshot of the current lifecycle. Safe to call
// from any goroutine; never blocks.
func (m *Manager) State() State {
	return State{
		Initialized: m.initialized.Load(),
		Unlocked:    m.active.Load() != nil,
	}
}

// Encryptor returns the live encryptor and true, or (nil, false)
// when the server is locked. Callers that need encryption MUST
// honor the boolean — passing a nil encryptor downstream is a bug.
func (m *Manager) Encryptor() (crypto.Encryptor, bool) {
	enc := m.active.Load()
	if enc == nil {
		return nil, false
	}
	return *enc, true
}

// MarkInitialized records the existence of a verifier in the
// database. Called once at boot (after the storage layer comes up)
// so subsequent State() calls report the correct value to the UI.
//
// Idempotent. Cannot be undone — there is no "re-mark uninitialized"
// path because losing a verifier implies catastrophic data loss; in
// that case the operator must reset the database, not the manager.
func (m *Manager) MarkInitialized() {
	m.initialized.Store(true)
}

// Unlock installs the given encryptor as the active key, transitioning
// the manager into the unlocked state. Replaces any existing key
// (re-unlocking with the same or different key is a no-op for the
// data; rotation has its own path).
//
// Returns an error only when enc is nil — callers that want to lock
// should use Lock() instead.
func (m *Manager) Unlock(enc crypto.Encryptor) error {
	if enc == nil {
		return errors.New("keymgr: cannot unlock with nil encryptor")
	}
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()

	prev := m.active.Load()
	m.active.Store(&enc)
	if prev == nil {
		slog.Info("server unlocked")
	} else {
		// "unlock-while-unlocked" only happens during rotation today,
		// but log it explicitly so any future caller can spot
		// unexpected double-unlock paths.
		slog.Info("server encryption key swapped")
	}
	return nil
}

// Lock zeroizes the live key reference. After Lock the Manager
// reports Unlocked=false; subsequent storage ops that need
// encryption return ErrLocked. Idempotent: locking an already-locked
// manager is a no-op.
//
// We can't actually zeroize the underlying key bytes here because
// crypto.Encryptor is an interface and the implementation owns the
// memory; the GC reclaims the encryptor instance after the pointer
// swap. For a TPM/HSM-backed implementation in the future, the
// encryptor's own Close()/Destroy() would belong here.
func (m *Manager) Lock() {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()

	if m.active.Load() == nil {
		return
	}
	m.active.Store(nil)
	slog.Info("server locked")
}

// Initialized reports whether a verifier record exists in storage.
// Cheap; safe to call on every request from middleware.
func (m *Manager) Initialized() bool {
	return m.initialized.Load()
}

// IsUnlocked is sugar for State().Unlocked. Provided so callers
// don't need to construct a State just to check one bit.
func (m *Manager) IsUnlocked() bool {
	return m.active.Load() != nil
}
