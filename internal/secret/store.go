package secret

import (
	"context"
	"log/slog"
	"sync"

	"github.com/rakunlabs/pika/internal/secret/crypto"
	"github.com/rakunlabs/pika/internal/service"
)

// Storage wraps another storage with encryption.
// Keys are stored in plaintext, values are encrypted.
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

// Get retrieves and decrypts the value for the given key.
func (s *Storage) Get(ctx context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ciphertext, err := s.backend.Get(ctx, key)
	if err != nil {
		return nil, err
	}

	return s.encryptor.Decrypt(ciphertext)
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

// Delete removes the key-value pair.
func (s *Storage) Delete(ctx context.Context, key string) error {
	return s.backend.Delete(ctx, key)
}

// For iterates over all decrypted key-value pairs with the given prefix.
func (s *Storage) For(ctx context.Context, prefix string, fn func(ctx context.Context, key string, value []byte) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.backend.For(ctx, prefix, func(ctx context.Context, key string, value []byte) error {
		plaintext, err := s.encryptor.Decrypt(value)
		if err != nil {
			slog.Warn("failed to decrypt value", "key", key, "error", err)
			return nil // skip
		}
		return fn(ctx, key, plaintext)
	})
}

// Tx executes a function within a transaction with encryption.
func (s *Storage) Tx(ctx context.Context, fn func(ctx context.Context, tx service.Storage) error) error {
	return s.backend.Tx(ctx, func(ctx context.Context, tx service.Storage) error {
		txStorage := &txEncryptedStorage{
			backend:   tx,
			encryptor: s.encryptor,
		}
		return fn(ctx, txStorage)
	})
}

// RotateKey re-encrypts all values with a new key.
// The new encryptor must be able to decrypt data encrypted with the old key
// (use RotatedEncryptor which tries new key first, then old key).
func (s *Storage) RotateKey(ctx context.Context, newEncryptor crypto.Encryptor) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Collect all keys
	var keys []string
	err := s.backend.For(ctx, "", func(ctx context.Context, key string, value []byte) error {
		keys = append(keys, key)
		return nil
	})
	if err != nil {
		return err
	}

	slog.Info("starting key rotation", "total_keys", len(keys))

	// Re-encrypt each value
	for i, key := range keys {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		ciphertext, err := s.backend.Get(ctx, key)
		if err != nil {
			slog.Warn("rotation: failed to read", "key", key, "error", err)
			continue
		}

		// Decrypt with old key
		plaintext, err := s.encryptor.Decrypt(ciphertext)
		if err != nil {
			slog.Warn("rotation: failed to decrypt", "key", key, "error", err)
			continue
		}

		// Re-encrypt with new key
		newCiphertext, err := newEncryptor.Encrypt(plaintext)
		if err != nil {
			slog.Warn("rotation: failed to re-encrypt", "key", key, "error", err)
			continue
		}

		if err := s.backend.Set(ctx, key, newCiphertext); err != nil {
			slog.Warn("rotation: failed to write", "key", key, "error", err)
			continue
		}

		if (i+1)%100 == 0 {
			slog.Info("rotation progress", "processed", i+1, "total", len(keys))
		}
	}

	// Switch to new encryptor
	s.encryptor = newEncryptor

	slog.Info("key rotation completed", "total_keys", len(keys))
	return nil
}

// Close closes the underlying storage.
func (s *Storage) Close() error {
	if closer, ok := s.backend.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

// txEncryptedStorage wraps transaction storage with encryption.
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
			slog.Warn("failed to decrypt value in tx", "key", key, "error", err)
			return nil
		}
		return fn(ctx, key, plaintext)
	})
}

func (t *txEncryptedStorage) Tx(ctx context.Context, fn func(ctx context.Context, tx service.Storage) error) error {
	return t.backend.Tx(ctx, func(ctx context.Context, tx service.Storage) error {
		return fn(ctx, &txEncryptedStorage{backend: tx, encryptor: t.encryptor})
	})
}
