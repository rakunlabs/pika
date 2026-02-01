package crypto

import (
	"errors"
	"time"
)

var (
	ErrInvalidCiphertext = errors.New("invalid ciphertext")
	ErrDecryptionFailed  = errors.New("decryption failed")
	ErrKeyNotFound       = errors.New("key version not found")
	ErrInvalidKeySize    = errors.New("invalid key size")
)

// Config holds the crypto configuration.
type Config struct {
	Enabled   bool           `cfg:"enabled" default:"false"`
	Algorithm string         `cfg:"algorithm" default:"chacha20-poly1305"`
	Rotation  RotationConfig `cfg:"rotation"`
}

// RotationConfig holds the key rotation configuration.
type RotationConfig struct {
	Enabled          bool          `cfg:"enabled" default:"false"`
	Interval         time.Duration `cfg:"interval" default:"720h"`     // 30 days
	MaxKeyAge        time.Duration `cfg:"max_key_age" default:"2160h"` // 90 days
	ReencryptOnStart bool          `cfg:"reencrypt_on_start" default:"false"`
}

// Encryptor defines the interface for encryption operations.
type Encryptor interface {
	// Encrypt encrypts plaintext and returns ciphertext with embedded key version.
	Encrypt(plaintext []byte) (ciphertext []byte, err error)

	// Decrypt decrypts ciphertext and returns plaintext.
	// It automatically detects the key version from the ciphertext.
	Decrypt(ciphertext []byte) (plaintext []byte, err error)

	// CurrentKeyVersion returns the current active key version.
	CurrentKeyVersion() string
}

// KeyInfo holds information about an encryption key.
type KeyInfo struct {
	Version   string    `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	RotatedAt time.Time `json:"rotated_at,omitempty"`
	Status    KeyStatus `json:"status"`
}

// KeyStatus represents the status of an encryption key.
type KeyStatus string

const (
	KeyStatusActive   KeyStatus = "active"
	KeyStatusRetired  KeyStatus = "retired"
	KeyStatusArchived KeyStatus = "archived"
)

// KeyStore defines the interface for storing encryption keys.
type KeyStore interface {
	// GetKey retrieves a key by version.
	GetKey(version string) ([]byte, error)

	// GetActiveKey retrieves the current active key and its version.
	GetActiveKey() (key []byte, version string, err error)

	// StoreKey stores a new key with the given version.
	StoreKey(version string, key []byte) error

	// ListKeys lists all key information.
	ListKeys() ([]KeyInfo, error)

	// UpdateKeyStatus updates the status of a key.
	UpdateKeyStatus(version string, status KeyStatus) error
}
