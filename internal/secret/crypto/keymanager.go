package crypto

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// KeyManager manages encryption keys with versioning and rotation.
type KeyManager struct {
	db      *sql.DB
	kms     KMSProvider
	mu      sync.RWMutex
	cache   map[string][]byte // version -> decrypted key
	activeV string
}

// KMSProvider defines the interface for Key Management Services.
type KMSProvider interface {
	// Encrypt encrypts the data encryption key (DEK).
	Encrypt(ctx context.Context, plaintext []byte) (ciphertext []byte, err error)

	// Decrypt decrypts the wrapped DEK.
	Decrypt(ctx context.Context, ciphertext []byte) (plaintext []byte, err error)

	// GenerateDataKey generates a new DEK.
	GenerateDataKey(ctx context.Context) (plaintext, ciphertext []byte, err error)
}

// NewKeyManager creates a new key manager.
func NewKeyManager(db *sql.DB, kms KMSProvider) (*KeyManager, error) {
	km := &KeyManager{
		db:    db,
		kms:   kms,
		cache: make(map[string][]byte),
	}

	// Initialize keys from database
	if err := km.loadKeys(context.Background()); err != nil {
		return nil, err
	}

	return km, nil
}

// loadKeys loads all keys from the database into memory.
func (km *KeyManager) loadKeys(ctx context.Context) error {
	rows, err := km.db.QueryContext(ctx,
		"SELECT version, key_data, status FROM pika_keys ORDER BY version DESC")
	if err != nil {
		// Table might not exist yet
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var version, status string
		var keyData []byte

		if err := rows.Scan(&version, &keyData, &status); err != nil {
			return err
		}

		// Decrypt key data using KMS if available
		var decryptedKey []byte
		if km.kms != nil {
			decryptedKey, err = km.kms.Decrypt(ctx, keyData)
			if err != nil {
				return fmt.Errorf("failed to decrypt key %s: %w", version, err)
			}
		} else {
			// Key is stored as hex when no KMS
			decryptedKey, err = hex.DecodeString(string(keyData))
			if err != nil {
				return fmt.Errorf("failed to decode key %s: %w", version, err)
			}
		}

		km.cache[version] = decryptedKey

		// Track active key
		if status == string(KeyStatusActive) {
			km.activeV = version
		}
	}

	return rows.Err()
}

// GetKey retrieves a key by version.
func (km *KeyManager) GetKey(version string) ([]byte, error) {
	km.mu.RLock()
	defer km.mu.RUnlock()

	key, ok := km.cache[version]
	if !ok {
		return nil, ErrKeyNotFound
	}

	// Return a copy to prevent mutation
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	return keyCopy, nil
}

// GetActiveKey retrieves the current active key and its version.
func (km *KeyManager) GetActiveKey() (key []byte, version string, err error) {
	km.mu.RLock()
	defer km.mu.RUnlock()

	if km.activeV == "" {
		// No active key, generate one
		km.mu.RUnlock()
		version, err = km.CreateKey(context.Background())
		km.mu.RLock()
		if err != nil {
			return nil, "", err
		}
	} else {
		version = km.activeV
	}

	key, ok := km.cache[version]
	if !ok {
		return nil, "", ErrKeyNotFound
	}

	// Return a copy to prevent mutation
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	return keyCopy, version, nil
}

// StoreKey stores a new key with the given version.
func (km *KeyManager) StoreKey(version string, key []byte) error {
	ctx := context.Background()

	// Encrypt key with KMS if available
	var keyData []byte
	var err error
	if km.kms != nil {
		keyData, err = km.kms.Encrypt(ctx, key)
		if err != nil {
			return fmt.Errorf("failed to encrypt key: %w", err)
		}
	} else {
		// Store as hex when no KMS
		keyData = []byte(hex.EncodeToString(key))
	}

	// Store in database
	_, err = km.db.ExecContext(ctx,
		`INSERT INTO pika_keys (version, key_data, created_at, status) 
		 VALUES (?, ?, ?, ?)`,
		version, keyData, time.Now(), string(KeyStatusActive))
	if err != nil {
		return fmt.Errorf("failed to store key: %w", err)
	}

	// Update cache
	km.mu.Lock()
	km.cache[version] = key
	km.activeV = version
	km.mu.Unlock()

	return nil
}

// CreateKey creates a new encryption key and stores it.
func (km *KeyManager) CreateKey(ctx context.Context) (string, error) {
	// Determine next version
	km.mu.RLock()
	nextVersion := len(km.cache) + 1
	km.mu.RUnlock()

	version := fmt.Sprintf("v%d", nextVersion)

	var key, encryptedKey []byte
	var err error

	if km.kms != nil {
		// Use KMS to generate key
		key, encryptedKey, err = km.kms.GenerateDataKey(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to generate key via KMS: %w", err)
		}
	} else {
		// Generate local key
		key, err = GenerateKey()
		if err != nil {
			return "", err
		}
		encryptedKey = []byte(hex.EncodeToString(key))
	}

	// Retire current active key
	if km.activeV != "" {
		if err := km.UpdateKeyStatus(km.activeV, KeyStatusRetired); err != nil {
			return "", err
		}
	}

	// Store in database
	_, err = km.db.ExecContext(ctx,
		`INSERT INTO pika_keys (version, key_data, created_at, status) 
		 VALUES (?, ?, ?, ?)`,
		version, encryptedKey, time.Now(), string(KeyStatusActive))
	if err != nil {
		return "", fmt.Errorf("failed to store key: %w", err)
	}

	// Update cache
	km.mu.Lock()
	km.cache[version] = key
	km.activeV = version
	km.mu.Unlock()

	return version, nil
}

// ListKeys lists all key information.
func (km *KeyManager) ListKeys() ([]KeyInfo, error) {
	ctx := context.Background()

	rows, err := km.db.QueryContext(ctx,
		`SELECT version, created_at, rotated_at, status FROM pika_keys ORDER BY version DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []KeyInfo
	for rows.Next() {
		var ki KeyInfo
		var rotatedAt sql.NullTime

		if err := rows.Scan(&ki.Version, &ki.CreatedAt, &rotatedAt, &ki.Status); err != nil {
			return nil, err
		}

		if rotatedAt.Valid {
			ki.RotatedAt = rotatedAt.Time
		}

		keys = append(keys, ki)
	}

	return keys, rows.Err()
}

// UpdateKeyStatus updates the status of a key.
func (km *KeyManager) UpdateKeyStatus(version string, status KeyStatus) error {
	ctx := context.Background()

	_, err := km.db.ExecContext(ctx,
		`UPDATE pika_keys SET status = ?, rotated_at = ? WHERE version = ?`,
		string(status), time.Now(), version)
	if err != nil {
		return fmt.Errorf("failed to update key status: %w", err)
	}

	return nil
}

// RotateKey performs key rotation by creating a new key.
func (km *KeyManager) RotateKey(ctx context.Context) (string, error) {
	return km.CreateKey(ctx)
}

// GetActiveKeyVersion returns the current active key version.
func (km *KeyManager) GetActiveKeyVersion() string {
	km.mu.RLock()
	defer km.mu.RUnlock()
	return km.activeV
}
