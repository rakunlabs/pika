package secret

import (
	"context"
	"io"
	"log/slog"
	"sync"

	"github.com/rakunlabs/pika/internal/secret/crypto"
	"github.com/rakunlabs/pika/internal/service"
)

// Storage wraps another storage with encryption support.
// Currently delegates all operations to the backend directly.
// Encryption at the column level would require native storage layer support.
// The RotateKey method is retained for future use.
type Storage struct {
	backend   service.Storage
	encryptor crypto.Encryptor

	mu sync.RWMutex
}

// New creates a new encrypted storage wrapper.
func New(backend service.Storage, encryptor crypto.Encryptor) *Storage {
	return &Storage{
		backend:   backend,
		encryptor: encryptor,
	}
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
	return s.backend.Settings()
}

// Tx executes a function within a transaction.
func (s *Storage) Tx(ctx context.Context, fn func(ctx context.Context, tx service.Storage) error) error {
	return s.backend.Tx(ctx, func(ctx context.Context, tx service.Storage) error {
		encTx := &Storage{
			backend:   tx,
			encryptor: s.encryptor,
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

// Wipe forwards to the backend's Wipe.
func (s *Storage) Wipe() error {
	return s.backend.Wipe()
}

// Version forwards to the backend's monotonic version counter.
func (s *Storage) Version() uint64 {
	return s.backend.Version()
}

// RotateKey is retained for future use when column-level encryption is supported.
// Currently a no-op placeholder.
func (s *Storage) RotateKey(ctx context.Context, newEncryptor crypto.Encryptor) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	slog.Info("key rotation requested (currently no-op in SQL-backed storage)")

	// Switch to new encryptor for future use
	s.encryptor = newEncryptor

	return nil
}

// Close closes the underlying storage.
func (s *Storage) Close() error {
	if closer, ok := s.backend.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}
