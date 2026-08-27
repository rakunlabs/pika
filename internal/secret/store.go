package secret

import (
	"context"
	"io"

	"github.com/rakunlabs/pika/internal/secret/keymgr"
	"github.com/rakunlabs/pika/internal/service"
)

// Storage wraps another storage with encryption support.
//
// Currently delegates all operations to the backend directly —
// column-level encryption is the next phase. The wrapper still earns
// its keep today by:
//
//   - Carrying the *keymgr.Manager reference through Tx() and the
//     rotation handler so the entire system shares a single key
//     authority.
//   - Providing the future hook point for per-table Encrypt/Decrypt
//     calls (PR-2).
//
// Until column encryption lands, all Get/Set methods are pure
// passthroughs and the Manager's locked state has no effect on read
// or write paths. The lockgate HTTP middleware enforces the
// "server-locked → 503" semantics at the request boundary, which is
// what users actually see.
type Storage struct {
	backend service.Storage
	keymgr  *keymgr.Manager
}

// New creates a new encrypted storage wrapper.
//
// The Manager replaces the prior immutable crypto.Encryptor field;
// callers no longer pass a key directly. The Manager starts locked
// (no key in memory) and is unlocked at runtime via the
// /api/v1/key/unlock HTTP endpoint. See internal/secret/keymgr for
// the lifecycle.
func New(backend service.Storage, mgr *keymgr.Manager) *Storage {
	return &Storage{
		backend: backend,
		keymgr:  mgr,
	}
}

// KeyManager exposes the underlying lifecycle owner so service-layer
// code (e.g. the key/unlock handler, the verifier writer) can drive
// transitions without re-importing keymgr through indirect paths.
// The api layer also reads State() through this for status responses.
func (s *Storage) KeyManager() *keymgr.Manager {
	return s.keymgr
}

func (s *Storage) Users() service.UserStorage {
	return s.backend.Users()
}

func (s *Storage) UserIdentities() service.UserIdentityStorage {
	return s.backend.UserIdentities()
}

func (s *Storage) Tokens() service.TokenStorage {
	return s.backend.Tokens()
}

func (s *Storage) Sessions() service.SessionStorage {
	return s.backend.Sessions()
}

func (s *Storage) Permissions() service.PermissionStorage {
	return s.backend.Permissions()
}

func (s *Storage) Folders() service.FolderStorage {
	return s.backend.Folders()
}

func (s *Storage) Files() service.FileStorage {
	return s.backend.Files()
}

func (s *Storage) FileVersions() service.FileVersionStorage {
	return s.backend.FileVersions()
}

func (s *Storage) Settings() service.SettingsStorage {
	// Wrap so the at-rest sensitive payload is decrypted on Get
	// and re-encrypted on Set without consumers having to know the
	// envelope exists. See settings_seal.go for the field-by-field
	// extract/inject logic.
	return &settingsStorageWrapper{backend: s.backend.Settings(), parent: s}
}

func (s *Storage) UserPreferences() service.UserPreferencesStorage {
	return s.backend.UserPreferences()
}

func (s *Storage) Passkeys() service.PasskeyStorage {
	return s.backend.Passkeys()
}

func (s *Storage) PasskeyChallenges() service.PasskeyChallengeStorage {
	return s.backend.PasskeyChallenges()
}

func (s *Storage) UserTOTPs() service.UserTOTPStorage {
	return s.backend.UserTOTPs()
}

func (s *Storage) VaultAccounts() service.VaultAccountStorage {
	return s.backend.VaultAccounts()
}

func (s *Storage) VaultItems() service.VaultItemStorage {
	return s.backend.VaultItems()
}

func (s *Storage) VaultItemVersions() service.VaultItemVersionStorage {
	return s.backend.VaultItemVersions()
}

// Tx executes a function within a transaction. The inner Storage
// shares the same Manager pointer so any Encrypt/Decrypt call inside
// a transaction sees the same key state as the outer scope.
func (s *Storage) Tx(ctx context.Context, fn func(ctx context.Context, tx service.Storage) error) error {
	return s.backend.Tx(ctx, func(ctx context.Context, tx service.Storage) error {
		encTx := &Storage{
			backend: tx,
			keymgr:  s.keymgr,
		}
		return fn(ctx, encTx)
	})
}

// Backup forwards to the backend's Backup method. Encryption-at-rest in
// the application layer would defeat Badger's streaming backup, so we
// rely on disk-level encryption (or pika's own encryption_password on
// the export envelope) for confidentiality.
func (s *Storage) Backup(w io.Writer, since uint64) (uint64, error) {
	return s.backend.Backup(w, since)
}

// BackupUntil forwards to the backend's point-in-time backup.
func (s *Storage) BackupUntil(w io.Writer, until uint64) (uint64, error) {
	return s.backend.BackupUntil(w, until)
}

// Restore forwards to the backend's Restore method.
func (s *Storage) Restore(r io.Reader) error {
	return s.backend.Restore(r)
}

// ApplyBackup forwards to the backend's merge-restore.
func (s *Storage) ApplyBackup(r io.Reader) error {
	return s.backend.ApplyBackup(r)
}

// Wipe forwards to the backend's Wipe.
func (s *Storage) Wipe() error {
	return s.backend.Wipe()
}

// Version forwards to the backend's monotonic version counter.
func (s *Storage) Version() uint64 {
	return s.backend.Version()
}

// Close closes the underlying storage.
func (s *Storage) Close() error {
	if closer, ok := s.backend.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}
