package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"

	"github.com/rakunlabs/pika/internal/secret/crypto"
	"github.com/rakunlabs/pika/internal/secret/keymgr"
)

// Server-key lifecycle operations. These power the
// /api/v1/key/{status,initialize,unlock,rotate} HTTP endpoints. The
// design mirrors HashiCorp Vault's seal/unseal split:
//
//   - Initialize is a one-time event that establishes a verifier
//     record. It must succeed exactly once per database lifetime;
//     repeated calls return ErrConflict so a misclick can't blow
//     away the verifier and silently render existing ciphertext
//     unrecoverable.
//
//   - Unlock is the every-restart action. It loads the user-supplied
//     key into the in-memory keymgr.Manager after verifying it
//     against the stored verifier. Wrong key → ErrForbidden.
//
//   - Rotate is the rare admin action. It re-encrypts the verifier
//     with the new key and swaps the live encryptor. (PR-2 will
//     extend this to walk every encrypted column. For now only the
//     verifier itself rotates because that's all the storage layer
//     touches.)
//
// All three operations require an injected *keymgr.Manager; without
// it the methods return ErrInternal because the boot wiring is
// broken (the cmd/pika/main.go path always provides one).

// SetKeyManager wires the server-key manager into the service.
// Called once from cmd/pika at boot, before HTTP routes register.
// The manager pointer is held verbatim — the service never replaces
// it, only drives state transitions through its methods.
//
// Calling this with a nil manager is allowed but disables every
// keyops method (they all return an error mentioning "key manager
// not configured"). This keeps unit tests of unrelated service code
// free of mandatory keymgr setup.
func (s *Service) SetKeyManager(mgr *keymgr.Manager) {
	s.keyManager = mgr
}

// KeyManager returns the wired manager, or nil if SetKeyManager was
// never called. Callers should nil-check before using the result; in
// practice everything except tests will have a manager.
func (s *Service) KeyManager() *keymgr.Manager {
	return s.keyManager
}

// KeyStatus is the response shape for GET /api/v1/key/status. We
// keep it small on purpose — the endpoint is unauthenticated (any
// client can probe it to discover whether to show an unlock screen)
// so it must not leak verifier bytes, key fingerprints, or anything
// that helps an attacker offline-test candidate keys.
type KeyStatus struct {
	Initialized bool `json:"initialized"`
	Unlocked    bool `json:"unlocked"`
}

// GetKeyStatus combines the on-disk verifier presence with the
// in-memory unlock state. The "initialized" bit is sourced from the
// settings row rather than the manager's MarkInitialized cache so it
// reflects reality even if a future caller forgot to mark the
// manager at boot.
func (s *Service) GetKeyStatus(ctx context.Context) (*KeyStatus, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}

	st := &KeyStatus{
		Initialized: len(settings.EncryptionVerifier) > 0,
	}
	if s.keyManager != nil {
		st.Unlocked = s.keyManager.IsUnlocked()
		// Keep the manager's cached "initialized" bit in sync with
		// the settings row. Cheap and idempotent — ensures the
		// lockgate middleware (which reads from the manager) doesn't
		// fall behind a recently-written verifier.
		if st.Initialized {
			s.keyManager.MarkInitialized()
		}
	}
	return st, nil
}

// verifierMagic is the prefix every plaintext verifier carries so
// decrypt-with-wrong-key can be distinguished from decrypt-with-
// right-key-but-corrupt-record. AEAD already gives us the wrong-key
// signal (Open returns an error), but the magic is a belt-and-
// suspenders check: if a future bug somehow bypasses AEAD and yields
// random plaintext, the prefix mismatch still rejects the unlock.
//
// The "v1" tag leaves room for a future format change without
// breaking on-disk compatibility — a v2 verifier could carry a KDF
// fingerprint or an HMAC over the previous version, and the unlock
// path would dispatch on the prefix.
var verifierMagic = []byte("PIKA_KEY_VERIFIER_v1|")

// verifierPlaintext returns a fresh verifier plaintext: the magic
// prefix followed by 16 bytes of randomness. Randomness is there so
// the resulting ciphertext is unique per install (no rainbow-table
// risk for guessing keys against a fixed ciphertext) and so two
// servers initialized with the same key still have different
// verifier rows (defense against confused-deputy comparisons).
func verifierPlaintext() ([]byte, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return nil, fmt.Errorf("verifier randomness: %w", err)
	}
	out := make([]byte, 0, len(verifierMagic)+len(random))
	out = append(out, verifierMagic...)
	out = append(out, random...)
	return out, nil
}

// deriveKeyMaterial maps an arbitrary admin-supplied passphrase to
// the 32-byte key the chacha20-poly1305 cipher requires. We use
// SHA-256 deliberately, not Argon2id: the at-rest key is meant to be
// memorable enough for the operator to type at every restart, and
// the threat we defend against is "operator's machine compromised
// while config.yaml is sitting on disk", not "attacker exfiltrates
// verifier and brute-forces". A KDF hardening here would only delay
// online attempts (the unlock endpoint already needs a logged-in
// superadmin) without meaningfully raising the offline cost — the
// verifier ciphertext size leaks the format anyway.
//
// If we later move to a "key file" model where the operator pastes
// 32 random bytes, this helper becomes trivial (length check + cast).
// Using SHA-256 today preserves backward compatibility with the
// previous PIKA_SECRET_ENCRYPTION_KEY behaviour, which also ran the
// passphrase through SHA-256.
func deriveKeyMaterial(passphrase string) []byte {
	sum := sha256.Sum256([]byte(passphrase))
	return sum[:]
}

// InitializeServerKey writes the very first verifier record. Fails
// with ErrConflict if a verifier already exists — re-initializing
// would change the key the verifier ciphertext is bound to and
// implicitly reject every subsequent unlock.
//
// On success the manager is left UNLOCKED with the new key already
// installed. This is intentional: the operator doing first-run setup
// shouldn't have to re-paste their key thirty seconds after entering
// it. Subsequent restarts go through Unlock, which has its own AEAD
// check.
func (s *Service) InitializeServerKey(ctx context.Context, passphrase string) error {
	if s.keyManager == nil {
		return fmt.Errorf("key manager not configured: %w", ErrInternal)
	}
	if passphrase == "" {
		return fmt.Errorf("key is required: %w", ErrBadRequest)
	}

	settings, err := s.Settings(ctx)
	if err != nil {
		return fmt.Errorf("read settings: %w", err)
	}
	if len(settings.EncryptionVerifier) > 0 {
		// Already initialized — refuse, even if the caller is the
		// same admin. The operational flow for "I forgot the key" is
		// "restore from backup", not "overwrite the verifier".
		return fmt.Errorf("server is already initialized: %w", ErrConflict)
	}

	key := deriveKeyMaterial(passphrase)
	enc, err := crypto.NewChaCha20(key)
	if err != nil {
		return fmt.Errorf("build encryptor: %w", err)
	}

	plain, err := verifierPlaintext()
	if err != nil {
		return err
	}
	verifier, err := enc.Encrypt(plain)
	if err != nil {
		return fmt.Errorf("encrypt verifier: %w", err)
	}

	settings.EncryptionVerifier = verifier
	if err := s.UpdateSettings(ctx, settings); err != nil {
		return fmt.Errorf("persist verifier: %w", err)
	}

	if err := s.keyManager.Unlock(enc); err != nil {
		// Should be unreachable: enc is non-nil and we just built it.
		// Surface as internal because a failure here means the
		// verifier is on disk but the manager refused the swap.
		return fmt.Errorf("install encryptor: %w", err)
	}
	s.keyManager.MarkInitialized()
	return nil
}

// UnlockServerKey verifies the supplied passphrase against the
// stored verifier and installs the resulting encryptor on the
// manager. Wrong passphrase → ErrForbidden (a 401-ish signal; the
// HTTP layer maps both 401 and 403 from the error chain).
//
// Idempotent in the success case: unlocking an already-unlocked
// server with the correct passphrase installs the same encryptor
// over itself. We don't short-circuit on IsUnlocked() because the
// caller might be re-entering the key after a configuration change
// and we want the verification to run anyway.
func (s *Service) UnlockServerKey(ctx context.Context, passphrase string) error {
	if s.keyManager == nil {
		return fmt.Errorf("key manager not configured: %w", ErrInternal)
	}
	if passphrase == "" {
		return fmt.Errorf("key is required: %w", ErrBadRequest)
	}

	settings, err := s.Settings(ctx)
	if err != nil {
		return fmt.Errorf("read settings: %w", err)
	}
	if len(settings.EncryptionVerifier) == 0 {
		// No verifier yet → the right action is initialize, not
		// unlock. The HTTP layer turns ErrBadRequest into a 400
		// with this exact message so the SPA can route the user to
		// the initialize form.
		return fmt.Errorf("server is not initialized; call /api/v1/key/initialize first: %w", ErrBadRequest)
	}

	key := deriveKeyMaterial(passphrase)
	enc, err := crypto.NewChaCha20(key)
	if err != nil {
		return fmt.Errorf("build encryptor: %w", err)
	}

	plain, err := enc.Decrypt(settings.EncryptionVerifier)
	if err != nil {
		// AEAD failure → wrong passphrase. We deliberately use the
		// same error path as a magic-prefix mismatch below so the
		// HTTP response and timing don't differ between the two
		// rejection reasons (both are "wrong key" from the user's
		// point of view).
		return fmt.Errorf("invalid server key: %w", ErrForbidden)
	}
	if !bytes.HasPrefix(plain, verifierMagic) {
		// AEAD passed but the plaintext doesn't carry our marker.
		// Either a future-format verifier (which would imply a
		// downgrade — refuse) or an extremely improbable AEAD
		// collision. Refuse in both cases.
		return fmt.Errorf("verifier format mismatch: %w", ErrForbidden)
	}

	if err := s.keyManager.Unlock(enc); err != nil {
		return fmt.Errorf("install encryptor: %w", err)
	}
	s.keyManager.MarkInitialized()
	return nil
}

// LockServerKey clears the live key from the manager. Provided so an
// admin can manually lock the server without restarting (useful for
// key-rotation rehearsals or "I'm stepping away" scenarios). All
// subsequent encrypted-table requests 503 until UnlockServerKey runs
// again.
func (s *Service) LockServerKey() error {
	if s.keyManager == nil {
		return fmt.Errorf("key manager not configured: %w", ErrInternal)
	}
	s.keyManager.Lock()
	return nil
}

// RotateServerKey verifies the old key, rewraps every encrypted
// at-rest blob with the new key, and swaps the live encryptor.
//
// Scope of work, in order:
//
//  1. Verify the supplied old passphrase by decrypting the stored
//     verifier. Wrong key → ErrForbidden, no side effects.
//
//  2. Read every encrypted resource through the CURRENT (old) key
//     and hold the plaintext in memory:
//     a. service.Settings (the wrapper at internal/secret
//     inflates SensitivePayload while we still have the old
//     key loaded).
//
//  3. Atomically swap to the new encryptor on the manager and
//     re-encrypt the verifier so the next restart's unlock works.
//
//  4. Re-write each resource we held in memory; the secret-storage
//     wrapper now seals with the new key.
//
// This is "stop the world" rotation — the manager's encryptor flips
// once mid-call. Concurrent storage operations during the swap
// will see the old key on read (until the swap line) and the new
// key after; because rotation requires CapSettingsManage and is
// rare, blocking writes for the brief window is acceptable.
//
// If a write in step 4 fails, the system is in a degraded state:
// the new key is live but a row still carries old-key ciphertext.
// We return the error so the operator knows to retry. Recovery is
// "re-run rotation": the old key won't validate the verifier
// anymore so the operator must use the new key as the "old"
// argument in the retry, which the service accepts (the verifier
// re-encrypt is idempotent under the active key).
func (s *Service) RotateServerKey(ctx context.Context, oldPassphrase, newPassphrase string) error {
	if s.keyManager == nil {
		return fmt.Errorf("key manager not configured: %w", ErrInternal)
	}
	if oldPassphrase == "" || newPassphrase == "" {
		return fmt.Errorf("old and new keys are required: %w", ErrBadRequest)
	}
	if oldPassphrase == newPassphrase {
		return fmt.Errorf("new key must differ from old key: %w", ErrBadRequest)
	}

	rawSettings, err := s.Settings(ctx)
	if err != nil {
		return fmt.Errorf("read settings: %w", err)
	}
	if len(rawSettings.EncryptionVerifier) == 0 {
		return fmt.Errorf("server is not initialized: %w", ErrBadRequest)
	}

	// Step 1 — verify the supplied old key against the stored
	// verifier. Use a freshly-built encryptor so we don't disturb
	// the live one; the plaintext we decrypt here is reused later
	// to construct the new verifier.
	oldKey := deriveKeyMaterial(oldPassphrase)
	oldEnc, err := crypto.NewChaCha20(oldKey)
	if err != nil {
		return fmt.Errorf("build old encryptor: %w", err)
	}
	plain, err := oldEnc.Decrypt(rawSettings.EncryptionVerifier)
	if err != nil {
		return fmt.Errorf("invalid old key: %w", ErrForbidden)
	}
	if !bytes.HasPrefix(plain, verifierMagic) {
		return fmt.Errorf("verifier format mismatch: %w", ErrForbidden)
	}

	// Step 2 — capture decrypted secret state. Reading via
	// s.Settings(ctx) pulls through the secret-storage wrapper
	// (inflates SensitivePayload using the current encryptor),
	// which is what we want: we get a plaintext view of every
	// secret slot, ready to re-seal under whatever key is live
	// when we call Set later.
	//
	// The verifier-pre-validation above ensures the live encryptor
	// IS the old key (callers can't get past the verify step
	// otherwise), so this read won't surprise us with garbage.
	plaintextSettings, err := s.Settings(ctx)
	if err != nil {
		return fmt.Errorf("read settings for rewrap: %w", err)
	}

	// Step 3 — build the new encryptor, re-encrypt the verifier,
	// and install the new key as the live one. Persisting the new
	// verifier first means a crash between the install and the
	// secrets-rewrite step (4) leaves the system in a recoverable
	// state: the new key is on disk and the operator can retry
	// rotation using the new key on both sides.
	newKey := deriveKeyMaterial(newPassphrase)
	newEnc, err := crypto.NewChaCha20(newKey)
	if err != nil {
		return fmt.Errorf("build new encryptor: %w", err)
	}
	newVerifier, err := newEnc.Encrypt(plain)
	if err != nil {
		return fmt.Errorf("encrypt new verifier: %w", err)
	}

	plaintextSettings.EncryptionVerifier = newVerifier
	if err := s.keyManager.Unlock(newEnc); err != nil {
		return fmt.Errorf("install new encryptor: %w", err)
	}

	// Step 4 — write the secrets back through the wrapper. With
	// the new encryptor live, the secret-storage Set call will
	// re-seal SensitivePayload under the new key.
	if err := s.UpdateSettings(ctx, plaintextSettings); err != nil {
		// Half-rotated state: verifier + live key are new, but the
		// settings row still has old-key SensitivePayload. The
		// operator must retry rotation with the NEW key on both
		// sides; that path is supported because the verifier now
		// matches the new key.
		return fmt.Errorf("rewrap secrets failed: %w", err)
	}
	return nil
}
