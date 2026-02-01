package secret

import (
	"context"
	"log/slog"
	"sync"

	"github.com/rakunlabs/pika/internal/secret/crypto"
	"github.com/rakunlabs/pika/internal/service"
)

// Storage wraps another storage implementation with encryption.
// Keys are stored in plaintext for searchability, values are encrypted.
type Storage struct {
	backend   service.Storage
	encryptor crypto.Encryptor
	scheduler *crypto.RotationScheduler

	// For background re-encryption
	mu sync.RWMutex
}

// New creates a new encrypted storage wrapper.
func New(backend service.Storage, encryptor crypto.Encryptor, scheduler *crypto.RotationScheduler) *Storage {
	s := &Storage{
		backend:   backend,
		encryptor: encryptor,
		scheduler: scheduler,
	}

	// Set up the re-encryption function if scheduler is provided
	if scheduler != nil {
		scheduler.SetReencryptFunc(s.reencryptAll)
	}

	return s
}

// Get retrieves and decrypts the value for the given key.
func (s *Storage) Get(ctx context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ciphertext, err := s.backend.Get(ctx, key)
	if err != nil {
		return nil, err
	}

	plaintext, err := s.encryptor.Decrypt(ciphertext)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}

// Set encrypts and stores the key-value pair.
func (s *Storage) Set(ctx context.Context, key string, value []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ciphertext, err := s.encryptor.Encrypt(value)
	if err != nil {
		return err
	}

	return s.backend.Set(ctx, key, ciphertext)
}

// Delete removes the key-value pair for the given key.
func (s *Storage) Delete(ctx context.Context, key string) error {
	return s.backend.Delete(ctx, key)
}

// For iterates over all decrypted key-value pairs where the key starts with the given prefix.
// The callback function is called for each row. Return an error to stop iteration.
func (s *Storage) For(ctx context.Context, prefix string, fn func(ctx context.Context, key string, value []byte) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.backend.For(ctx, prefix, func(ctx context.Context, key string, value []byte) error {
		plaintext, err := s.encryptor.Decrypt(value)
		if err != nil {
			// Log and skip items that can't be decrypted
			slog.Warn("failed to decrypt value during iteration", "key", key, "error", err)
			return nil // continue iteration
		}

		return fn(ctx, key, plaintext)
	})
}

// Tx executes a function within a transaction.
// The transaction storage also wraps values with encryption.
func (s *Storage) Tx(ctx context.Context, fn func(ctx context.Context, tx service.Storage) error) error {
	return s.backend.Tx(ctx, func(ctx context.Context, tx service.Storage) error {
		// Wrap the transaction storage with encryption
		txStorage := &txEncryptedStorage{
			backend:   tx,
			encryptor: s.encryptor,
		}
		return fn(ctx, txStorage)
	})
}

// txEncryptedStorage wraps a transaction storage with encryption.
type txEncryptedStorage struct {
	backend   service.Storage
	encryptor crypto.Encryptor
}

func (t *txEncryptedStorage) Get(ctx context.Context, key string) ([]byte, error) {
	ciphertext, err := t.backend.Get(ctx, key)
	if err != nil {
		return nil, err
	}

	return t.encryptor.Decrypt(ciphertext)
}

func (t *txEncryptedStorage) Set(ctx context.Context, key string, value []byte) error {
	ciphertext, err := t.encryptor.Encrypt(value)
	if err != nil {
		return err
	}

	return t.backend.Set(ctx, key, ciphertext)
}

func (t *txEncryptedStorage) Delete(ctx context.Context, key string) error {
	return t.backend.Delete(ctx, key)
}

func (t *txEncryptedStorage) For(ctx context.Context, prefix string, fn func(ctx context.Context, key string, value []byte) error) error {
	return t.backend.For(ctx, prefix, func(ctx context.Context, key string, value []byte) error {
		plaintext, err := t.encryptor.Decrypt(value)
		if err != nil {
			slog.Warn("failed to decrypt value during transaction iteration", "key", key, "error", err)
			return nil // continue iteration
		}

		return fn(ctx, key, plaintext)
	})
}

func (t *txEncryptedStorage) Tx(ctx context.Context, fn func(ctx context.Context, tx service.Storage) error) error {
	// Nested transactions are not supported, delegate to the backend which will handle it
	return t.backend.Tx(ctx, func(ctx context.Context, tx service.Storage) error {
		txStorage := &txEncryptedStorage{
			backend:   tx,
			encryptor: t.encryptor,
		}
		return fn(ctx, txStorage)
	})
}

// Close closes the underlying storage.
func (s *Storage) Close() error {
	if closer, ok := s.backend.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

// reencryptAll re-encrypts all values with the current key.
// This is called during key rotation and runs in the background.
func (s *Storage) reencryptAll(ctx context.Context, oldKeyVersion string, _ []byte) error {
	// Collect all keys first using For
	var keys []string
	err := s.backend.For(ctx, "/", func(ctx context.Context, key string, value []byte) error {
		keys = append(keys, key)
		return nil // continue iteration
	})
	if err != nil {
		return err
	}

	total := int64(len(keys))
	if s.scheduler != nil {
		s.scheduler.SetProgress(0, total)
	}

	// Re-encrypt each value
	for i, key := range keys {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Read current value (will be decrypted)
		plaintext, err := s.Get(ctx, key)
		if err != nil {
			slog.Warn("failed to read value during re-encryption",
				"key", key, "error", err)
			continue
		}

		// Write back with new key (will be encrypted with current key)
		if err := s.Set(ctx, key, plaintext); err != nil {
			slog.Warn("failed to write value during re-encryption",
				"key", key, "error", err)
			continue
		}

		if s.scheduler != nil {
			s.scheduler.SetProgress(int64(i+1), total)
		}
	}

	return nil
}

// StartRotation starts the rotation scheduler.
func (s *Storage) StartRotation(ctx context.Context) error {
	if s.scheduler != nil {
		return s.scheduler.Start(ctx)
	}
	return nil
}

// StopRotation stops the rotation scheduler.
func (s *Storage) StopRotation() {
	if s.scheduler != nil {
		s.scheduler.Stop()
	}
}

// TriggerRotation manually triggers a key rotation.
func (s *Storage) TriggerRotation(ctx context.Context) (string, error) {
	if s.scheduler != nil {
		return s.scheduler.TriggerRotation(ctx)
	}
	return "", nil
}

// IsReencrypting returns whether background re-encryption is in progress.
func (s *Storage) IsReencrypting() bool {
	if s.scheduler != nil {
		return s.scheduler.IsReencrypting()
	}
	return false
}

// ReencryptionProgress returns the current re-encryption progress.
func (s *Storage) ReencryptionProgress() (processed, total int64) {
	if s.scheduler != nil {
		return s.scheduler.ReencryptionProgress()
	}
	return 0, 0
}
