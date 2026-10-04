package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/rakunlabs/pika/internal/secret/crypto"
	"github.com/rakunlabs/pika/internal/secret/envelope"
)

// Server-managed vault keys.
//
// When the admin sets Settings.Vault.KeyMode = "server", each user's
// vault key is sealed with the server encryption key (keymgr) instead
// of being wrapped with a master-password-derived key. Item encryption
// does not change: the SPA still encrypts every item with the vault
// key, it just receives that key from the server instead of deriving
// it locally. Switching modes therefore only rewrites the account row.

// vaultKeySize is the length of a raw vault key (XChaCha20-Poly1305).
const vaultKeySize = 32

// VaultConvertToServerRequest moves a user-managed vault to server
// management. The SPA sends the vault key it just unwrapped with the
// master password, plus the Secret Key hash as proof that the caller
// actually completed an unlock.
type VaultConvertToServerRequest struct {
	VaultKey      []byte `json:"vault_key"`
	SecretKeyHash []byte `json:"secret_key_hash"`
}

// VaultServerSetupRequest creates a server-managed vault.
type VaultServerSetupRequest struct {
	SessionLockSeconds int `json:"session_lock_seconds"`
}

// VaultServerKeyResponse carries the raw vault key of a
// server-managed vault.
type VaultServerKeyResponse struct {
	VaultKey []byte `json:"vault_key"`
}

// VaultKeyMode returns the deployment-wide key mode chosen by the
// admin. Settings read errors fall back to user mode, the stricter
// option.
func (s *Service) VaultKeyMode(ctx context.Context) string {
	settings, err := s.Settings(ctx)
	if err != nil || settings == nil || settings.Vault == nil {
		return VaultKeyModeUser
	}
	return NormalizeVaultKeyMode(settings.Vault.KeyMode)
}

func newVaultAccountView(row *VaultAccount, count int64) *VaultAccountView {
	view := &VaultAccountView{
		UserID:                 row.UserID,
		KeyMode:                NormalizeVaultKeyMode(row.KeyMode),
		KDF:                    row.KDF,
		WrappedVaultKey:        row.WrappedVaultKey,
		WrappedVaultKeyVersion: row.WrappedVaultKeyVersion,
		RecoveryKitID:          row.RecoveryKitID,
		SessionLockSeconds:     row.SessionLockSeconds,
		ItemCount:              count,
		CreatedAt:              row.CreatedAt,
		UpdatedAt:              row.UpdatedAt,
	}
	if view.KeyMode == VaultKeyModeServer {
		// The sealed blob is only meaningful to the server; the SPA
		// fetches the raw key through ServerVaultKey instead.
		view.WrappedVaultKey = nil
	}
	return view
}

func (vs *VaultService) sealVaultKey(key []byte) ([]byte, error) {
	mgr := vs.svc.keyManager
	if mgr == nil || !mgr.IsUnlocked() {
		return nil, ErrVaultServerKeyLocked
	}
	sealed, err := envelope.Seal(mgr, key)
	if err != nil {
		if errors.Is(err, envelope.ErrLocked) {
			return nil, ErrVaultServerKeyLocked
		}
		return nil, err
	}
	return sealed, nil
}

func (vs *VaultService) openVaultKey(blob []byte) ([]byte, error) {
	mgr := vs.svc.keyManager
	if mgr == nil || !mgr.IsUnlocked() {
		return nil, ErrVaultServerKeyLocked
	}
	key, err := envelope.Open(mgr, blob)
	if err != nil {
		if errors.Is(err, envelope.ErrLocked) {
			return nil, ErrVaultServerKeyLocked
		}
		return nil, fmt.Errorf("vault: open server-managed vault key: %w", err)
	}
	return key, nil
}

func (vs *VaultService) requireKeyMode(ctx context.Context, mode string) error {
	if vs.svc.VaultKeyMode(ctx) != mode {
		return fmt.Errorf("vault: this deployment uses %q vault key mode: %w", vs.svc.VaultKeyMode(ctx), ErrBadRequest)
	}
	return nil
}

// SetupServerManaged creates a vault whose key is generated and
// sealed by the server. Only allowed when the deployment is in server
// mode. Returns the account view and the raw vault key so the SPA can
// open the vault immediately.
func (vs *VaultService) SetupServerManaged(ctx context.Context, userID string, req *VaultServerSetupRequest) (*VaultAccountView, []byte, error) {
	if vs == nil {
		return nil, nil, ErrVaultNotInitialized
	}
	if userID == "" {
		return nil, nil, fmt.Errorf("vault: user id required: %w", ErrBadRequest)
	}
	if err := vs.requireKeyMode(ctx, VaultKeyModeServer); err != nil {
		return nil, nil, err
	}
	if existing, err := vs.svc.store.VaultAccounts().Get(ctx, userID); err == nil && existing != nil {
		return nil, nil, ErrVaultAlreadyInitialized
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, nil, err
	}
	if _, err := vs.svc.store.Users().Get(ctx, userID); err != nil {
		return nil, nil, fmt.Errorf("vault: load user: %w", err)
	}

	key := make([]byte, vaultKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, nil, fmt.Errorf("vault: generate vault key: %w", err)
	}
	sealed, err := vs.sealVaultKey(key)
	if err != nil {
		return nil, nil, err
	}

	lockSeconds := 0
	if req != nil {
		lockSeconds = req.SessionLockSeconds
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	row := &VaultAccount{
		UserID:                 userID,
		KeyMode:                VaultKeyModeServer,
		WrappedVaultKey:        sealed,
		WrappedVaultKeyVersion: 1,
		RecoveryKitID:          newRecoveryKitID(),
		SessionLockSeconds:     clampLockSeconds(lockSeconds),
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	if err := vs.svc.store.VaultAccounts().Set(ctx, row); err != nil {
		return nil, nil, err
	}
	return newVaultAccountView(row, 0), key, nil
}

// ServerVaultKey returns the raw vault key of a server-managed vault.
// Allowed regardless of the current deployment mode so a vault can
// still be opened (and converted) after the admin switches back to
// user mode.
func (vs *VaultService) ServerVaultKey(ctx context.Context, userID string) ([]byte, error) {
	if vs == nil {
		return nil, ErrVaultNotInitialized
	}
	row, err := vs.svc.store.VaultAccounts().Get(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrVaultNotInitialized
		}
		return nil, err
	}
	if NormalizeVaultKeyMode(row.KeyMode) != VaultKeyModeServer {
		return nil, fmt.Errorf("vault: vault is protected by a master password: %w", ErrBadRequest)
	}
	return vs.openVaultKey(row.WrappedVaultKey)
}

// ConvertToServer moves a user-managed vault to server management.
// The SPA must have unlocked the vault with the master password; the
// Secret Key hash is checked so a session alone can't swap in an
// arbitrary key and make existing items unreadable.
func (vs *VaultService) ConvertToServer(ctx context.Context, userID string, req *VaultConvertToServerRequest) (*VaultAccountView, error) {
	if vs == nil {
		return nil, ErrVaultNotInitialized
	}
	if userID == "" || req == nil {
		return nil, fmt.Errorf("vault: user id and request required: %w", ErrBadRequest)
	}
	if len(req.VaultKey) != vaultKeySize {
		return nil, fmt.Errorf("vault: vault_key must be %d bytes: %w", vaultKeySize, ErrBadRequest)
	}
	if err := vs.requireKeyMode(ctx, VaultKeyModeServer); err != nil {
		return nil, err
	}
	row, err := vs.svc.store.VaultAccounts().Get(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrVaultNotInitialized
		}
		return nil, err
	}
	if NormalizeVaultKeyMode(row.KeyMode) == VaultKeyModeServer {
		count, _ := vs.svc.store.VaultItems().Count(ctx, userID)
		return newVaultAccountView(row, count), nil
	}
	if len(req.SecretKeyHash) == 0 || subtle.ConstantTimeCompare(row.SecretKeyHash, req.SecretKeyHash) != 1 {
		return nil, fmt.Errorf("vault: secret key mismatch: %w", ErrUnauthorized)
	}

	sealed, err := vs.sealVaultKey(req.VaultKey)
	if err != nil {
		return nil, err
	}
	row.KeyMode = VaultKeyModeServer
	row.WrappedVaultKey = sealed
	row.WrappedVaultKeyVersion = 1
	row.SecretKeyHash = nil
	row.KDF = VaultKDFParams{}
	row.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
	if err := vs.svc.store.VaultAccounts().Set(ctx, row); err != nil {
		return nil, err
	}
	count, _ := vs.svc.store.VaultItems().Count(ctx, userID)
	return newVaultAccountView(row, count), nil
}

// ConvertToUser moves a server-managed vault back to master-password
// protection. The SPA fetched the raw key via ServerVaultKey, wrapped
// it with a key derived from the new master password + a fresh Secret
// Key, and posts the same payload shape as Setup.
func (vs *VaultService) ConvertToUser(ctx context.Context, userID string, req *VaultSetupRequest) (*VaultAccountView, error) {
	if vs == nil {
		return nil, ErrVaultNotInitialized
	}
	if userID == "" || req == nil {
		return nil, fmt.Errorf("vault: user id and request required: %w", ErrBadRequest)
	}
	if err := vs.requireKeyMode(ctx, VaultKeyModeUser); err != nil {
		return nil, err
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
	row, err := vs.svc.store.VaultAccounts().Get(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrVaultNotInitialized
		}
		return nil, err
	}
	if NormalizeVaultKeyMode(row.KeyMode) != VaultKeyModeServer {
		return nil, fmt.Errorf("vault: vault is already protected by a master password: %w", ErrConflict)
	}

	row.KeyMode = VaultKeyModeUser
	row.SecretKeyHash = append([]byte(nil), req.SecretKeyHash...)
	row.KDF = req.KDF
	row.WrappedVaultKey = append([]byte(nil), req.WrappedVaultKey...)
	row.WrappedVaultKeyVersion = req.WrappedVaultKeyVersion
	if row.WrappedVaultKeyVersion <= 0 {
		row.WrappedVaultKeyVersion = 1
	}
	row.RecoveryKitID = newRecoveryKitID()
	if req.SessionLockSeconds > 0 {
		row.SessionLockSeconds = clampLockSeconds(req.SessionLockSeconds)
	}
	row.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
	if err := vs.svc.store.VaultAccounts().Set(ctx, row); err != nil {
		return nil, err
	}
	count, _ := vs.svc.store.VaultItems().Count(ctx, userID)
	return newVaultAccountView(row, count), nil
}

// serverVaultKeys holds the plaintext vault keys of every
// server-managed vault while the server key is being rotated.
type serverVaultKeys map[string][]byte

// openServerVaultKeys decrypts every server-managed vault key with
// the given (old) encryptor.
func (s *Service) openServerVaultKeys(ctx context.Context, enc crypto.Encryptor) (serverVaultKeys, error) {
	accounts, err := s.store.VaultAccounts().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list vault accounts: %w", err)
	}
	keys := serverVaultKeys{}
	for _, a := range accounts {
		if NormalizeVaultKeyMode(a.KeyMode) != VaultKeyModeServer {
			continue
		}
		key, err := envelope.OpenWith(enc, a.WrappedVaultKey)
		if err != nil {
			return nil, fmt.Errorf("open vault key for user %s: %w", a.UserID, err)
		}
		keys[a.UserID] = key
	}
	return keys, nil
}

// resealServerVaultKeys writes every collected vault key back sealed
// with the given (new) encryptor.
func (s *Service) resealServerVaultKeys(ctx context.Context, enc crypto.Encryptor, keys serverVaultKeys) error {
	for userID, key := range keys {
		row, err := s.store.VaultAccounts().Get(ctx, userID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return err
		}
		if NormalizeVaultKeyMode(row.KeyMode) != VaultKeyModeServer {
			continue
		}
		sealed, err := envelope.SealWith(enc, key)
		if err != nil {
			return fmt.Errorf("seal vault key for user %s: %w", userID, err)
		}
		row.WrappedVaultKey = sealed
		row.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
		if err := s.store.VaultAccounts().Set(ctx, row); err != nil {
			return fmt.Errorf("save vault key for user %s: %w", userID, err)
		}
	}
	return nil
}
