package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rakunlabs/pika/internal/hook"
)

// VaultService coordinates personal-vault operations on top of the
// underlying storage. Mirrors TOTPService / PasskeyService in shape:
//
//   - Owns no DB tables on its own — every read/write goes through
//     the service.Storage interface.
//   - Owns an in-memory rate limiter so a brute-force attempt against
//     the secret-key challenge endpoint can't tarpit the DB.
//   - Holds no plaintext secrets at any point: the server only sees
//     opaque encrypted payloads and the Argon2id-derived parameters
//     needed to UNWRAP the vault key client-side. The wrapped key
//     itself is sensitive but useless without the master password.
//
// The split from *Service keeps the vault-specific code path easy to
// disable (nil-check pattern, like Service.totp) and prevents
// accidental imports of the vault concept from non-vault code.
type VaultService struct {
	svc *Service

	// unlockAttempts gates the secret-key challenge endpoint. The
	// challenge isn't strictly needed for security — the master
	// password is the actual gate — but it lets an honest user know
	// "you typed the wrong Secret Key from your emergency kit"
	// without burning an Argon2id derivation. Without a rate limit
	// an attacker could enumerate by trying random keys.
	attemptsMu sync.Mutex
	attempts   map[string]*vaultUnlockAttempts

	// attemptsMaxPerMinute is the threshold per (user_id, ip) tuple
	// after which the server returns 429 regardless of correctness.
	attemptsMaxPerMinute int
	// attemptsWindow is the rolling window used by the limiter.
	attemptsWindow time.Duration
}

// vaultUnlockAttempts is a single bucket in the rate limiter map. The
// timestamps slice is bounded to attemptsMaxPerMinute entries (older
// ones are evicted on each check), so memory stays bounded even if a
// single user is being targeted.
type vaultUnlockAttempts struct {
	timestamps []time.Time
}

// NewVaultService wires the vault coordinator onto a parent Service.
// Callers that don't want to expose the personal vault feature can
// skip this — the SPA hides the /vault route when /api/v1/info reports
// vault_enabled=false (set by the API layer when this is nil).
func NewVaultService(svc *Service) *VaultService {
	vs := &VaultService{
		svc:                  svc,
		attempts:             make(map[string]*vaultUnlockAttempts),
		attemptsMaxPerMinute: 10,
		attemptsWindow:       time.Minute,
	}
	go vs.gcLoop()
	return vs
}

// SetVaultService attaches a VaultService to a parent Service.
// Mirrors SetPasskeyService / SetTOTPService.
func (s *Service) SetVaultService(v *VaultService) {
	s.vault = v
}

// VaultCoord returns the bound VaultService, or nil when the
// deployment has the personal-vault feature disabled at boot.
// Callers MUST nil-check before invoking methods.
//
// Note: this is the build-time / boot-time gate (set when
// cmd/pika does NOT call NewVaultService). The runtime admin
// toggle goes through VaultEnabled() instead — handlers that
// want to honor BOTH gates should use VaultCoordFor(ctx).
func (s *Service) VaultCoord() *VaultService {
	return s.vault
}

// VaultEnabled reports whether the personal-vault feature should
// be served to clients right now. It combines:
//
//   - The boot-time gate (VaultCoord() != nil — set by cmd/pika
//     calling SetVaultService).
//   - The runtime admin toggle (Settings.Vault.Disabled), readable
//     and writable from the Security panel.
//
// Returns false on a settings-read failure (fail-closed) so a
// transient storage hiccup doesn't expose the API surface against
// the admin's wishes; callers that need to distinguish "disabled"
// from "couldn't tell" should call this themselves.
//
// Cheap enough to call from the request hot path — Settings() is
// served from the singleton row that gets read on most requests
// anyway. We deliberately don't cache the bool here because the
// toggle should take effect immediately when the admin flips it.
func (s *Service) VaultEnabled(ctx context.Context) bool {
	if s.vault == nil {
		return false
	}
	settings, err := s.Settings(ctx)
	if err != nil || settings == nil {
		return false
	}
	if settings.Vault != nil && settings.Vault.Disabled {
		return false
	}
	return true
}

// VaultCoordFor returns the bound VaultService when the feature
// is currently enabled, or nil when it is disabled at either gate
// (boot-time or admin runtime toggle). HTTP handlers should
// prefer this over VaultCoord() so the admin toggle takes effect
// without needing a process restart.
func (s *Service) VaultCoordFor(ctx context.Context) *VaultService {
	if !s.VaultEnabled(ctx) {
		return nil
	}
	return s.vault
}

// VaultSetupRequest is the payload posted by the SPA at Setup time.
// Every field is client-derived; the server stores them verbatim.
// See VaultAccount for what each field is and why we persist it.
type VaultSetupRequest struct {
	SecretKeyHash          []byte         `json:"secret_key_hash"`
	KDF                    VaultKDFParams `json:"kdf"`
	WrappedVaultKey        []byte         `json:"wrapped_vault_key"`
	WrappedVaultKeyVersion int            `json:"wrapped_vault_key_version"`
	SessionLockSeconds     int            `json:"session_lock_seconds"`
}

// VaultAccountView is the safe-to-expose subset of a VaultAccount the
// SPA needs to unlock and configure the vault. Notably:
//
//   - SecretKeyHash is NOT included — it's a server-side verifier
//     only; exposing it would help an offline attacker confirm a
//     guessed Secret Key without making a request.
//   - WrappedVaultKey IS included; the client needs to unwrap it
//     locally to obtain the vault key. The wrapped form is useless
//     without the master password + Secret Key.
type VaultAccountView struct {
	UserID                 string         `json:"user_id"`
	KDF                    VaultKDFParams `json:"kdf"`
	WrappedVaultKey        []byte         `json:"wrapped_vault_key"`
	WrappedVaultKeyVersion int            `json:"wrapped_vault_key_version"`
	RecoveryKitID          string         `json:"recovery_kit_id,omitempty"`
	SessionLockSeconds     int            `json:"session_lock_seconds,omitempty"`
	ItemCount              int64          `json:"item_count"`
	CreatedAt              time.Time      `json:"created_at"`
	UpdatedAt              time.Time      `json:"updated_at"`
}

// VaultStatus is the lightweight status view returned by the public
// /vault/status endpoint. It carries no sensitive material — the SPA
// uses it to decide whether to render the Setup flow or the Unlock
// flow without needing to make an authenticated unwrap attempt.
type VaultStatus struct {
	Initialized bool  `json:"initialized"`
	ItemCount   int64 `json:"item_count"`
}

// CreateVaultItemRequest is the payload to create a new item. Every
// secret-bearing field is an AEAD ciphertext produced by the SPA; the
// server stores them opaquely. Type and Favorite are cleartext —
// see VaultItem doc for the rationale.
type CreateVaultItemRequest struct {
	Type               VaultItemType `json:"type"`
	EncryptedTitle     []byte        `json:"encrypted_title"`
	EncryptedTags      []byte        `json:"encrypted_tags,omitempty"`
	EncryptedHostnames []byte        `json:"encrypted_hostnames,omitempty"`
	// EncryptedFolder is optional; empty means "no folder". See
	// VaultItem.EncryptedFolder for the rationale.
	EncryptedFolder  []byte `json:"encrypted_folder,omitempty"`
	EncryptedPayload []byte `json:"encrypted_payload"`
	Favorite         bool   `json:"favorite,omitempty"`
}

// UpdateVaultItemRequest is the payload for editing an item. Every
// editable field is a pointer or "empty-means-skip" so the SPA can
// patch only what changed (e.g. flipping favorite without
// re-encrypting the payload).
//
// ExpectedVersion is the optimistic-concurrency token: must match the
// current stored Version, otherwise UpdateItem returns
// ErrVaultVersionConflict.
//
// The encrypted byte fields use the "empty = skip" convention rather
// than a pointer — a pointer to a slice composes awkwardly with Go's
// JSON omitempty for slices, and a zero-length ciphertext is never a
// valid encrypted value (XChaCha20-Poly1305 alone produces a
// minimum 40-byte blob: 24-byte nonce + 16-byte MAC). The service
// layer rejects ciphertexts shorter than that floor.
type UpdateVaultItemRequest struct {
	ExpectedVersion    int64          `json:"expected_version"`
	Type               *VaultItemType `json:"type,omitempty"`
	EncryptedTitle     []byte         `json:"encrypted_title,omitempty"`
	EncryptedTags      []byte         `json:"encrypted_tags,omitempty"`
	EncryptedHostnames []byte         `json:"encrypted_hostnames,omitempty"`
	// EncryptedFolder: "" (nil slice) = leave as-is; a non-empty
	// AEAD ciphertext replaces the stored folder. To CLEAR the
	// folder, the client sends ClearFolder=true (the empty-bytes
	// convention can't distinguish "skip" from "clear" because
	// every other ciphertext field uses len(x)==0 to mean "skip").
	EncryptedFolder  []byte `json:"encrypted_folder,omitempty"`
	ClearFolder      bool   `json:"clear_folder,omitempty"`
	EncryptedPayload []byte `json:"encrypted_payload,omitempty"`
	Favorite         *bool  `json:"favorite,omitempty"`
	Archived         *bool  `json:"archived,omitempty"`
}

// vault returned by *Service.VaultCoord is the read-side handle on
// the coordinator. The field lives on *Service for the same reason
// totp / passkeys do — keeps the wiring single-source.
//
// Compile-time tie: ensure the field exists on Service. We add the
// field separately on service.go but document the contract here.
//
// (The actual field declaration is in service.go.)

// ─── Account lifecycle ────────────────────────────────────────────

// Status reports whether the calling user has an initialized vault
// and how many items it holds. Never errors on the "not initialized"
// case — that's a normal state for new users. Storage errors are
// propagated so the API layer can return 500.
func (vs *VaultService) Status(ctx context.Context, userID string) (*VaultStatus, error) {
	if vs == nil || userID == "" {
		return &VaultStatus{}, nil
	}
	if _, err := vs.svc.store.VaultAccounts().Get(ctx, userID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return &VaultStatus{Initialized: false}, nil
		}
		return nil, err
	}
	count, err := vs.svc.store.VaultItems().Count(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &VaultStatus{Initialized: true, ItemCount: count}, nil
}

// Account returns the unlock-time view of the calling user's vault.
// Used by the SPA between login and unlock: the SPA needs the KDF
// parameters and wrapped key to attempt the Argon2id derivation
// in-browser. Returns ErrVaultNotInitialized when the user has no
// vault row yet.
func (vs *VaultService) Account(ctx context.Context, userID string) (*VaultAccountView, error) {
	if vs == nil {
		return nil, ErrVaultNotInitialized
	}
	if userID == "" {
		return nil, fmt.Errorf("vault: user id required: %w", ErrBadRequest)
	}
	row, err := vs.svc.store.VaultAccounts().Get(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrVaultNotInitialized
		}
		return nil, err
	}
	count, err := vs.svc.store.VaultItems().Count(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &VaultAccountView{
		UserID:                 row.UserID,
		KDF:                    row.KDF,
		WrappedVaultKey:        row.WrappedVaultKey,
		WrappedVaultKeyVersion: row.WrappedVaultKeyVersion,
		RecoveryKitID:          row.RecoveryKitID,
		SessionLockSeconds:     row.SessionLockSeconds,
		ItemCount:              count,
		CreatedAt:              row.CreatedAt,
		UpdatedAt:              row.UpdatedAt,
	}, nil
}

// Setup initializes the vault for a user. The wire payload is fully
// constructed client-side: the server is the trustee of the wrapped
// key and the KDF parameters, not their producer. Re-initialization
// is rejected with ErrVaultAlreadyInitialized — the dedicated Reset
// path is required to wipe and start over (because items encrypted
// with the previous key would otherwise become unreadable garbage on
// the next list).
func (vs *VaultService) Setup(ctx context.Context, userID string, req *VaultSetupRequest) (*VaultAccountView, error) {
	if vs == nil {
		return nil, ErrVaultNotInitialized
	}
	if userID == "" || req == nil {
		return nil, fmt.Errorf("vault: user id and request required: %w", ErrBadRequest)
	}
	if err := validateKDF(req.KDF); err != nil {
		return nil, err
	}
	if len(req.SecretKeyHash) < 16 || len(req.SecretKeyHash) > 64 {
		return nil, fmt.Errorf("vault: secret_key_hash size out of range: %w", ErrBadRequest)
	}
	if len(req.WrappedVaultKey) < 32 || len(req.WrappedVaultKey) > 256 {
		return nil, fmt.Errorf("vault: wrapped_vault_key size out of range: %w", ErrBadRequest)
	}
	if req.WrappedVaultKeyVersion <= 0 {
		req.WrappedVaultKeyVersion = 1
	}

	// Re-init guard. The check + insert race is mitigated by
	// userTOTP-style "Set is upsert" semantics in the storage, but
	// we want a clear error here for the SPA — otherwise a
	// second-tab Setup would silently overwrite the first tab's
	// keys, locking the user out of their (encrypted) items.
	if existing, err := vs.svc.store.VaultAccounts().Get(ctx, userID); err == nil && existing != nil {
		return nil, ErrVaultAlreadyInitialized
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	if _, err := vs.svc.store.Users().Get(ctx, userID); err != nil {
		return nil, fmt.Errorf("vault: load user: %w", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	row := &VaultAccount{
		UserID:                 userID,
		SecretKeyHash:          append([]byte(nil), req.SecretKeyHash...),
		KDF:                    req.KDF,
		WrappedVaultKey:        append([]byte(nil), req.WrappedVaultKey...),
		WrappedVaultKeyVersion: req.WrappedVaultKeyVersion,
		RecoveryKitID:          newRecoveryKitID(),
		SessionLockSeconds:     clampLockSeconds(req.SessionLockSeconds),
		CreatedAt:              now,
		UpdatedAt:              now,
	}

	if err := vs.svc.store.VaultAccounts().Set(ctx, row); err != nil {
		return nil, err
	}

	return &VaultAccountView{
		UserID:                 row.UserID,
		KDF:                    row.KDF,
		WrappedVaultKey:        row.WrappedVaultKey,
		WrappedVaultKeyVersion: row.WrappedVaultKeyVersion,
		RecoveryKitID:          row.RecoveryKitID,
		SessionLockSeconds:     row.SessionLockSeconds,
		ItemCount:              0,
		CreatedAt:              row.CreatedAt,
		UpdatedAt:              row.UpdatedAt,
	}, nil
}

// UnlockCheckRequest is the payload posted to UnlockCheck. The SPA
// sends a SHA-256 (or BLAKE2b) of the Secret Key the user typed; the
// server compares it constant-time against the stored hash. This is
// not the master-password verifier — the master password is verified
// implicitly when the SPA decrypts the wrapped key. UnlockCheck is
// just a friendly "you typed the wrong Secret Key" hint, rate
// limited to defeat enumeration.
type UnlockCheckRequest struct {
	SecretKeyHash []byte `json:"secret_key_hash"`
}

// UnlockCheck verifies the supplied secret-key hash matches the
// stored hash, with rate limiting per (user, IP) tuple.
//
// Returns:
//   - nil error on match.
//   - ErrUnauthorized on mismatch (uniform with the rate-limit
//     response so an attacker can't tell "wrong key" from "throttled").
//
// The (user, ip) key is supplied by the API layer so this method
// doesn't need to know anything about HTTP. Pass empty ip to disable
// rate limiting (e.g. in tests).
func (vs *VaultService) UnlockCheck(ctx context.Context, userID, ip string, req *UnlockCheckRequest) error {
	if vs == nil {
		return ErrVaultNotInitialized
	}
	if userID == "" || req == nil || len(req.SecretKeyHash) == 0 {
		return fmt.Errorf("vault: user id and secret_key_hash required: %w", ErrBadRequest)
	}

	if ip != "" && !vs.recordAttempt(userID, ip) {
		// Over the threshold — uniform "unauthorized" so the
		// attacker doesn't learn whether the underlying key was
		// right. The hook event lets operators alert on this.
		vs.svc.emitHook(hook.Event{
			Type:      hook.EventVaultUnlockFailed,
			Timestamp: time.Now().UTC(),
			User:      AuditUserFromContext(ctx),
		})
		return fmt.Errorf("vault: too many attempts: %w", ErrUnauthorized)
	}

	row, err := vs.svc.store.VaultAccounts().Get(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrVaultNotInitialized
		}
		return err
	}
	if subtle.ConstantTimeCompare(row.SecretKeyHash, req.SecretKeyHash) != 1 {
		vs.svc.emitHook(hook.Event{
			Type:      hook.EventVaultUnlockFailed,
			Timestamp: time.Now().UTC(),
			User:      AuditUserFromContext(ctx),
		})
		return fmt.Errorf("vault: secret key mismatch: %w", ErrUnauthorized)
	}
	return nil
}

// RotateMasterPassword re-wraps the vault key under a new
// password-derived account key. The SPA already did the heavy
// lifting client-side — Argon2id with the new password and salt,
// re-encrypted the existing vault_key (NOT a new one — items don't
// need to be re-encrypted because the vault_key didn't change).
//
// Item rows are untouched: every existing encrypted_payload is still
// readable with the same vault_key, just unlocked through a different
// password now. This makes rotation cheap (O(1)) regardless of vault
// size.
//
// The caller MUST have re-authenticated their session before invoking
// this path (the API handler enforces; the service layer doesn't see
// the password directly).
func (vs *VaultService) RotateMasterPassword(ctx context.Context, userID string, req *VaultSetupRequest) (*VaultAccountView, error) {
	if vs == nil {
		return nil, ErrVaultNotInitialized
	}
	if userID == "" || req == nil {
		return nil, fmt.Errorf("vault: user id and request required: %w", ErrBadRequest)
	}
	if err := validateKDF(req.KDF); err != nil {
		return nil, err
	}
	if len(req.WrappedVaultKey) < 32 || len(req.WrappedVaultKey) > 256 {
		return nil, fmt.Errorf("vault: wrapped_vault_key size out of range: %w", ErrBadRequest)
	}

	row, err := vs.svc.store.VaultAccounts().Get(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrVaultNotInitialized
		}
		return nil, err
	}

	row.KDF = req.KDF
	row.WrappedVaultKey = append([]byte(nil), req.WrappedVaultKey...)
	if req.WrappedVaultKeyVersion > 0 {
		row.WrappedVaultKeyVersion = req.WrappedVaultKeyVersion
	}
	if len(req.SecretKeyHash) > 0 {
		row.SecretKeyHash = append([]byte(nil), req.SecretKeyHash...)
	}
	row.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
	if err := vs.svc.store.VaultAccounts().Set(ctx, row); err != nil {
		return nil, err
	}

	count, _ := vs.svc.store.VaultItems().Count(ctx, userID)
	return &VaultAccountView{
		UserID:                 row.UserID,
		KDF:                    row.KDF,
		WrappedVaultKey:        row.WrappedVaultKey,
		WrappedVaultKeyVersion: row.WrappedVaultKeyVersion,
		RecoveryKitID:          row.RecoveryKitID,
		SessionLockSeconds:     row.SessionLockSeconds,
		ItemCount:              count,
		CreatedAt:              row.CreatedAt,
		UpdatedAt:              row.UpdatedAt,
	}, nil
}

// RegenerateRecoveryKitID produces a fresh kit ID. Callers re-render
// the emergency kit PDF/QR and discard the old one. The Secret Key
// itself is NOT regenerated (regenerating would force a re-wrap of
// every item).
//
// Returns the new kit ID. Requires the caller to have re-authenticated
// — the API handler enforces. Passing a userID with no vault row
// returns ErrVaultNotInitialized.
func (vs *VaultService) RegenerateRecoveryKitID(ctx context.Context, userID string) (string, error) {
	if vs == nil {
		return "", ErrVaultNotInitialized
	}
	row, err := vs.svc.store.VaultAccounts().Get(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", ErrVaultNotInitialized
		}
		return "", err
	}
	row.RecoveryKitID = newRecoveryKitID()
	row.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
	if err := vs.svc.store.VaultAccounts().Set(ctx, row); err != nil {
		return "", err
	}
	return row.RecoveryKitID, nil
}

// SetSessionLockSeconds updates the per-user auto-lock TTL. Bounded
// to [60, 86400] seconds (1 minute to 1 day) so the SPA can't be
// tricked into never locking. Zero = use SPA default.
func (vs *VaultService) SetSessionLockSeconds(ctx context.Context, userID string, seconds int) error {
	if vs == nil {
		return ErrVaultNotInitialized
	}
	row, err := vs.svc.store.VaultAccounts().Get(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrVaultNotInitialized
		}
		return err
	}
	row.SessionLockSeconds = clampLockSeconds(seconds)
	row.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
	return vs.svc.store.VaultAccounts().Set(ctx, row)
}

// Reset wipes the user's vault: account row, every item, every
// version. After Reset the user can Setup again from scratch. Caller
// MUST have re-authenticated; the API handler enforces.
//
// Returns the count of items + versions destroyed, useful for the
// confirmation toast on the SPA. Reset is wrapped in a single tx so a
// partial failure leaves the state consistent (either everything is
// gone or nothing changes).
func (vs *VaultService) Reset(ctx context.Context, userID string) (itemsDeleted int64, err error) {
	if vs == nil {
		return 0, ErrVaultNotInitialized
	}
	if userID == "" {
		return 0, fmt.Errorf("vault: user id required: %w", ErrBadRequest)
	}
	err = vs.svc.store.Tx(ctx, func(ctx context.Context, tx Storage) error {
		n, cerr := tx.VaultItems().Count(ctx, userID)
		if cerr != nil {
			return cerr
		}
		itemsDeleted = n
		if cerr := tx.VaultItems().DeleteAllByUser(ctx, userID); cerr != nil {
			return cerr
		}
		if cerr := tx.VaultItemVersions().DeleteAllByUser(ctx, userID); cerr != nil {
			return cerr
		}
		return tx.VaultAccounts().Delete(ctx, userID)
	})
	return itemsDeleted, err
}

// ─── Item CRUD ────────────────────────────────────────────────────

// CreateItem inserts a new vault item for the user. The server
// generates the row id and timestamps; the client supplies type,
// the encrypted blobs (title, tags, hostnames, payload), and a few
// cleartext lifecycle flags. Returns the stored row including the
// new ID and Version=1.
//
// Encrypted-byte size validation:
//
//   - encrypted_title is required and bounded to [aeadMinLen, 1024].
//     1KiB ciphertext lets the user store a ~950-byte plaintext
//     title — far more than any sane label.
//   - encrypted_tags / encrypted_hostnames are optional; when present
//     they must be at least aeadMinLen bytes and at most 8KiB.
//   - encrypted_payload is required and bounded to [aeadMinLen, 1MiB].
//
// aeadMinLen (40 bytes) is the smallest possible XChaCha20-Poly1305
// output: 24-byte nonce + 16-byte MAC + zero plaintext. Anything
// shorter cannot be a valid ciphertext and rejecting it catches the
// "client sent base64 of an empty string" bug class.
func (vs *VaultService) CreateItem(ctx context.Context, userID string, req *CreateVaultItemRequest) (*VaultItem, error) {
	if vs == nil {
		return nil, ErrVaultNotInitialized
	}
	if userID == "" || req == nil {
		return nil, fmt.Errorf("vault: user id and request required: %w", ErrBadRequest)
	}
	if !req.Type.IsValid() {
		return nil, ErrVaultUnknownItemType
	}
	if err := validateCiphertext("encrypted_title", req.EncryptedTitle, true, 1024); err != nil {
		return nil, err
	}
	if err := validateCiphertext("encrypted_tags", req.EncryptedTags, false, 8*1024); err != nil {
		return nil, err
	}
	if err := validateCiphertext("encrypted_hostnames", req.EncryptedHostnames, false, 8*1024); err != nil {
		return nil, err
	}
	// Folder name AEAD ciphertext. Optional; cap at 1 KiB to match
	// the title cap — folder names are short labels, not free-form
	// notes, so the same bound applies.
	if err := validateCiphertext("encrypted_folder", req.EncryptedFolder, false, 1024); err != nil {
		return nil, err
	}
	if err := validateCiphertext("encrypted_payload", req.EncryptedPayload, true, 1<<20); err != nil {
		return nil, err
	}

	// Ownership/initialization check — without this, a user could
	// pile up items even after their account was reset, leaving
	// orphans on the next Setup.
	if _, err := vs.svc.store.VaultAccounts().Get(ctx, userID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrVaultNotInitialized
		}
		return nil, err
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	item := &VaultItem{
		ID:                 newItemID(),
		UserID:             userID,
		Type:               req.Type,
		EncryptedTitle:     append([]byte(nil), req.EncryptedTitle...),
		EncryptedTags:      append([]byte(nil), req.EncryptedTags...),
		EncryptedHostnames: append([]byte(nil), req.EncryptedHostnames...),
		EncryptedFolder:    append([]byte(nil), req.EncryptedFolder...),
		EncryptedPayload:   append([]byte(nil), req.EncryptedPayload...),
		Favorite:           req.Favorite,
		CreatedAt:          now,
		UpdatedAt:          now,
		Version:            1,
	}
	if err := vs.svc.store.VaultItems().Create(ctx, item); err != nil {
		return nil, err
	}

	vs.svc.emitHook(hook.Event{
		Type:      hook.EventVaultItemCreated,
		Timestamp: now,
		User:      AuditUserFromContext(ctx),
		Path:      item.ID,
	})

	return item, nil
}

// GetItem returns a single item (verifies ownership in the storage
// layer; returns ErrNotFound on cross-user access).
func (vs *VaultService) GetItem(ctx context.Context, userID, itemID string) (*VaultItem, error) {
	if vs == nil {
		return nil, ErrVaultNotInitialized
	}
	return vs.svc.store.VaultItems().Get(ctx, userID, itemID)
}

// ListItems returns items matching the filter for the calling user.
func (vs *VaultService) ListItems(ctx context.Context, userID string, filter VaultItemFilter) ([]VaultItem, error) {
	if vs == nil {
		return nil, ErrVaultNotInitialized
	}
	return vs.svc.store.VaultItems().List(ctx, userID, filter)
}

// UpdateItem applies a patch to an existing item. Optimistic
// concurrency: the request's ExpectedVersion must match the stored
// Version; on mismatch ErrVaultVersionConflict is returned (HTTP 409).
//
// Every successful update:
//  1. Snapshots the pre-update state to vault_item_versions.
//  2. Bumps Version by 1.
//  3. Touches UpdatedAt.
//  4. Emits a vault.item.updated hook.
//
// The whole sequence runs inside a single Tx so a partial failure
// (e.g. snapshot succeeds but write fails) leaves the state
// consistent.
func (vs *VaultService) UpdateItem(ctx context.Context, userID, itemID string, req *UpdateVaultItemRequest) (*VaultItem, error) {
	if vs == nil {
		return nil, ErrVaultNotInitialized
	}
	if userID == "" || itemID == "" || req == nil {
		return nil, fmt.Errorf("vault: user id, item id, and request required: %w", ErrBadRequest)
	}

	var updated *VaultItem
	err := vs.svc.store.Tx(ctx, func(ctx context.Context, tx Storage) error {
		existing, err := tx.VaultItems().Get(ctx, userID, itemID)
		if err != nil {
			return err
		}
		if existing.Version != req.ExpectedVersion {
			return ErrVaultVersionConflict
		}

		// Snapshot before mutation. The snapshot row carries the
		// PREVIOUS state — that's what's useful for "show me what
		// this looked like before the last edit". EncryptedTitle
		// follows the same encryption posture as the live row, so a
		// snapshot never reveals the readable title.
		snapshot := &VaultItemVersion{
			ItemID:           existing.ID,
			Version:          existing.Version,
			EncryptedTitle:   append([]byte(nil), existing.EncryptedTitle...),
			EncryptedPayload: append([]byte(nil), existing.EncryptedPayload...),
			UpdatedAt:        existing.UpdatedAt,
			Author:           AuditUserFromContext(ctx),
		}
		if err := tx.VaultItemVersions().Append(ctx, snapshot); err != nil {
			return err
		}

		// Apply the patch. Empty-byte-slice = skip for ciphertext
		// fields; nil pointer = skip for booleans / type. The byte
		// size validators reject malformed ciphertexts with
		// ErrBadRequest so a buggy client can't silently corrupt
		// the row.
		if req.Type != nil {
			if !req.Type.IsValid() {
				return ErrVaultUnknownItemType
			}
			existing.Type = *req.Type
		}
		if len(req.EncryptedTitle) > 0 {
			if err := validateCiphertext("encrypted_title", req.EncryptedTitle, true, 1024); err != nil {
				return err
			}
			existing.EncryptedTitle = append([]byte(nil), req.EncryptedTitle...)
		}
		if len(req.EncryptedTags) > 0 {
			if err := validateCiphertext("encrypted_tags", req.EncryptedTags, true, 8*1024); err != nil {
				return err
			}
			existing.EncryptedTags = append([]byte(nil), req.EncryptedTags...)
		}
		if len(req.EncryptedHostnames) > 0 {
			if err := validateCiphertext("encrypted_hostnames", req.EncryptedHostnames, true, 8*1024); err != nil {
				return err
			}
			existing.EncryptedHostnames = append([]byte(nil), req.EncryptedHostnames...)
		}
		// Folder is patched via TWO inputs:
		//  - len(EncryptedFolder) > 0 → set to this new ciphertext
		//  - ClearFolder == true     → drop any current folder
		// We process ClearFolder AFTER the set so a single request
		// can't both set and clear in one go (deterministic outcome
		// for a buggy client).
		if len(req.EncryptedFolder) > 0 {
			if err := validateCiphertext("encrypted_folder", req.EncryptedFolder, true, 1024); err != nil {
				return err
			}
			existing.EncryptedFolder = append([]byte(nil), req.EncryptedFolder...)
		}
		if req.ClearFolder {
			existing.EncryptedFolder = nil
		}
		if len(req.EncryptedPayload) > 0 {
			if err := validateCiphertext("encrypted_payload", req.EncryptedPayload, true, 1<<20); err != nil {
				return err
			}
			existing.EncryptedPayload = append([]byte(nil), req.EncryptedPayload...)
		}
		if req.Favorite != nil {
			existing.Favorite = *req.Favorite
		}
		if req.Archived != nil {
			existing.Archived = *req.Archived
		}
		existing.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
		existing.Version++

		if err := tx.VaultItems().Update(ctx, existing); err != nil {
			return err
		}
		updated = existing
		return nil
	})
	if err != nil {
		return nil, err
	}

	vs.svc.emitHook(hook.Event{
		Type:      hook.EventVaultItemUpdated,
		Timestamp: time.Now().UTC(),
		User:      AuditUserFromContext(ctx),
		Path:      updated.ID,
	})

	return updated, nil
}

// SoftDeleteItem moves an item to trash (sets DeletedAt). The item
// can be restored or purged from there. Reuses Update so the version
// history is preserved consistently.
func (vs *VaultService) SoftDeleteItem(ctx context.Context, userID, itemID string) error {
	if vs == nil {
		return ErrVaultNotInitialized
	}
	return vs.svc.store.Tx(ctx, func(ctx context.Context, tx Storage) error {
		existing, err := tx.VaultItems().Get(ctx, userID, itemID)
		if err != nil {
			return err
		}
		if existing.DeletedAt != nil {
			// Already in trash; no-op. Idempotent so the SPA can
			// retry a delete after a network flake without an error.
			return nil
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		existing.DeletedAt = &now
		existing.UpdatedAt = now
		existing.Version++
		return tx.VaultItems().Update(ctx, existing)
	})
}

// RestoreItem un-trashes a previously soft-deleted item. Returns the
// restored row. Idempotent on already-active items.
func (vs *VaultService) RestoreItem(ctx context.Context, userID, itemID string) (*VaultItem, error) {
	if vs == nil {
		return nil, ErrVaultNotInitialized
	}
	var restored *VaultItem
	err := vs.svc.store.Tx(ctx, func(ctx context.Context, tx Storage) error {
		existing, err := tx.VaultItems().Get(ctx, userID, itemID)
		if err != nil {
			return err
		}
		if existing.DeletedAt == nil {
			restored = existing
			return nil
		}
		existing.DeletedAt = nil
		existing.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
		existing.Version++
		if err := tx.VaultItems().Update(ctx, existing); err != nil {
			return err
		}
		restored = existing
		return nil
	})
	return restored, err
}

// PurgeItem hard-deletes an item (and its history). Used by the
// trash's "delete forever" button. The item must already be in trash
// (DeletedAt != nil); attempting to purge an active item returns
// ErrBadRequest so an SPA bug can't accidentally bypass the
// soft-delete step.
func (vs *VaultService) PurgeItem(ctx context.Context, userID, itemID string) error {
	if vs == nil {
		return ErrVaultNotInitialized
	}
	err := vs.svc.store.Tx(ctx, func(ctx context.Context, tx Storage) error {
		existing, err := tx.VaultItems().Get(ctx, userID, itemID)
		if err != nil {
			return err
		}
		if existing.DeletedAt == nil {
			return fmt.Errorf("vault: item must be in trash before purge: %w", ErrBadRequest)
		}
		if err := tx.VaultItemVersions().DeleteAllByItem(ctx, itemID); err != nil {
			return err
		}
		return tx.VaultItems().Delete(ctx, userID, itemID)
	})
	if err != nil {
		return err
	}
	vs.svc.emitHook(hook.Event{
		Type:      hook.EventVaultItemDeleted,
		Timestamp: time.Now().UTC(),
		User:      AuditUserFromContext(ctx),
		Path:      itemID,
	})
	return nil
}

// TouchLastUsed updates an item's LastUsedAt timestamp. The SPA calls
// this when the user copies a field — gives a "recently used" sort
// option on the list. Does NOT version-increment or snapshot; it's
// metadata, not content.
func (vs *VaultService) TouchLastUsed(ctx context.Context, userID, itemID string) error {
	if vs == nil {
		return ErrVaultNotInitialized
	}
	return vs.svc.store.Tx(ctx, func(ctx context.Context, tx Storage) error {
		existing, err := tx.VaultItems().Get(ctx, userID, itemID)
		if err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		existing.LastUsedAt = &now
		return tx.VaultItems().Update(ctx, existing)
	})
}

// ListItemVersions returns the snapshot history for an item, newest
// first. Verifies ownership through a Get on the live item first;
// otherwise an attacker who guessed an item id could enumerate its
// edit count via the empty / populated response.
func (vs *VaultService) ListItemVersions(ctx context.Context, userID, itemID string) ([]VaultItemVersion, error) {
	if vs == nil {
		return nil, ErrVaultNotInitialized
	}
	if _, err := vs.svc.store.VaultItems().Get(ctx, userID, itemID); err != nil {
		return nil, err
	}
	return vs.svc.store.VaultItemVersions().ListByItem(ctx, itemID)
}

// ─── Helpers ──────────────────────────────────────────────────────

// recordAttempt registers an unlock-check attempt for (userID, ip)
// and returns true if the request is within the rate limit, false
// if it's over. Bucket is cleaned on every call so the in-memory
// footprint stays bounded.
func (vs *VaultService) recordAttempt(userID, ip string) bool {
	key := userID + "@" + ip
	now := time.Now()
	cutoff := now.Add(-vs.attemptsWindow)

	vs.attemptsMu.Lock()
	defer vs.attemptsMu.Unlock()

	b, ok := vs.attempts[key]
	if !ok {
		b = &vaultUnlockAttempts{}
		vs.attempts[key] = b
	}

	// Drop timestamps older than the window.
	kept := b.timestamps[:0]
	for _, t := range b.timestamps {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	b.timestamps = kept

	if len(b.timestamps) >= vs.attemptsMaxPerMinute {
		return false
	}
	b.timestamps = append(b.timestamps, now)
	return true
}

// gcLoop evicts empty buckets from the attempts map. Bucket entries
// are cleaned per-call but empty buckets linger; this loop drops
// them on a slow timer.
func (vs *VaultService) gcLoop() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		cutoff := now.Add(-vs.attemptsWindow)
		vs.attemptsMu.Lock()
		for k, b := range vs.attempts {
			alive := false
			for _, ts := range b.timestamps {
				if ts.After(cutoff) {
					alive = true
					break
				}
			}
			if !alive {
				delete(vs.attempts, k)
			}
		}
		vs.attemptsMu.Unlock()
	}
}

// validateKDF rejects obviously-broken Argon2id parameters. The
// frontend is expected to send sane values; this is defense-in-depth
// against a tampered client trying to weaken the derivation. Floors
// are taken from OWASP's minimum recommendation for Argon2id in
// interactive-login contexts.
func validateKDF(p VaultKDFParams) error {
	if p.Algorithm != "argon2id" {
		return fmt.Errorf("vault: unsupported kdf algorithm %q: %w", p.Algorithm, ErrBadRequest)
	}
	if p.Memory < 19*1024 {
		// OWASP minimum 19 MiB.
		return fmt.Errorf("vault: kdf memory below floor (19MiB): %w", ErrBadRequest)
	}
	if p.Memory > 1024*1024 {
		// Cap at 1 GiB so a malicious client can't ask the SPA to
		// allocate stupid amounts — also protects browsers that
		// would crash trying to honor a multi-GiB Argon2 call.
		return fmt.Errorf("vault: kdf memory above ceiling (1GiB): %w", ErrBadRequest)
	}
	if p.Iterations < 2 || p.Iterations > 64 {
		return fmt.Errorf("vault: kdf iterations out of range (2..64): %w", ErrBadRequest)
	}
	if p.Parallelism < 1 || p.Parallelism > 8 {
		return fmt.Errorf("vault: kdf parallelism out of range (1..8): %w", ErrBadRequest)
	}
	if len(p.Salt) < 16 || len(p.Salt) > 64 {
		return fmt.Errorf("vault: kdf salt size out of range (16..64): %w", ErrBadRequest)
	}
	return nil
}

// clampLockSeconds maps user-supplied auto-lock TTLs to a sane
// closed interval. 0 is preserved (means "use SPA default"); other
// values are clamped to [60, 86400].
func clampLockSeconds(s int) int {
	if s == 0 {
		return 0
	}
	if s < 60 {
		return 60
	}
	if s > 86400 {
		return 86400
	}
	return s
}

// aeadMinLen is the smallest length a valid XChaCha20-Poly1305
// ciphertext can take: a 24-byte public nonce concatenated with a
// 16-byte Poly1305 tag, with zero bytes of plaintext in between.
// Anything shorter cannot have come out of an authenticated encrypt
// call and is almost certainly a client bug (e.g. base64 of an
// empty buffer). validateCiphertext uses this as the hard floor.
const aeadMinLen = 24 + 16

// validateCiphertext rejects malformed/zero ciphertexts before they
// reach the storage layer. required=true means the field MUST be
// present (used by EncryptedTitle and EncryptedPayload); required=
// false allows an empty slice to mean "no value" (used by
// EncryptedTags / EncryptedHostnames where the user may have not
// added any tags or any URL fields). max is the upper byte bound.
//
// All errors carry ErrBadRequest so the HTTP handler returns 400
// rather than 500.
func validateCiphertext(name string, b []byte, required bool, max int) error {
	if len(b) == 0 {
		if required {
			return fmt.Errorf("vault: %s is required: %w", name, ErrBadRequest)
		}
		return nil
	}
	if len(b) < aeadMinLen {
		return fmt.Errorf("vault: %s shorter than the minimum AEAD framing (%d bytes): %w", name, aeadMinLen, ErrBadRequest)
	}
	if len(b) > max {
		return fmt.Errorf("vault: %s exceeds the %d-byte cap: %w", name, max, ErrBadRequest)
	}
	return nil
}

// newItemID returns a 16-byte hex-encoded random id for a vault item.
// Same shape as newCredentialID in passkey.go so id strings look
// consistent across the API surface.
func newItemID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Like the rest of pika: crypto/rand failure is fatal.
		panic(fmt.Errorf("vault: rng failure: %w", err))
	}
	return hex.EncodeToString(b)
}

// newRecoveryKitID returns a 16-byte hex-encoded random id used to
// version emergency kits. Not security-critical on its own; just a
// way to tell "this kit was generated at Setup time" from "this kit
// was issued by Regenerate".
func newRecoveryKitID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Errorf("vault: rng failure: %w", err))
	}
	return hex.EncodeToString(b)
}

// sha256OfBytes is the canonical hash used for the secret-key
// verifier. Centralized here so a future migration to BLAKE2b is a
// single-file change; not currently exported.
//
//nolint:unused // reserved for the rotation helper in PR-2.
func sha256OfBytes(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}
