package kms

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
)

// LocalConfig holds local key provider configuration.
type LocalConfig struct {
	Enabled bool   `cfg:"enabled" default:"false"`
	KeyFile string `cfg:"key_file"` // Path to key file (hex-encoded)
	KeyEnv  string `cfg:"key_env"`  // Environment variable name containing key (hex-encoded)
	Key     string `cfg:"key"`      // Direct key value (hex-encoded) - not recommended for production
}

// Local implements the Provider interface using local key storage.
// This is suitable for development/testing or when using envelope encryption
// with a file-based master key.
type Local struct {
	masterKey []byte
	mu        sync.RWMutex
}

// NewLocal creates a new local key provider.
func NewLocal(cfg *LocalConfig) (*Local, error) {
	var keyHex string

	// Priority: Key > KeyEnv > KeyFile
	switch {
	case cfg.Key != "":
		keyHex = cfg.Key
	case cfg.KeyEnv != "":
		keyHex = os.Getenv(cfg.KeyEnv)
		if keyHex == "" {
			return nil, fmt.Errorf("environment variable %s not set or empty", cfg.KeyEnv)
		}
	case cfg.KeyFile != "":
		data, err := os.ReadFile(cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("kms/local: read key file: %w", err)
		}
		keyHex = strings.TrimSpace(string(data))
	default:
		return nil, fmt.Errorf("no key source configured (key, key_env, or key_file)")
	}

	// Decode hex key
	masterKey, err := hex.DecodeString(strings.TrimSpace(keyHex))
	if err != nil {
		return nil, fmt.Errorf("kms/local: decode key: %w", err)
	}

	if len(masterKey) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes (256 bits), got %d bytes", len(masterKey))
	}

	return &Local{
		masterKey: masterKey,
	}, nil
}

// Encrypt "encrypts" the data using XOR with the master key.
// Note: This is a simple implementation. For production with local keys,
// consider using proper envelope encryption.
func (l *Local) Encrypt(ctx context.Context, plaintext []byte) ([]byte, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	// For local provider, we use simple XOR-based "encryption"
	// In practice, this just wraps the key - the real encryption happens
	// at the data level with ChaCha20-Poly1305
	ciphertext := make([]byte, len(plaintext))
	for i := range plaintext {
		ciphertext[i] = plaintext[i] ^ l.masterKey[i%len(l.masterKey)]
	}

	return ciphertext, nil
}

// Decrypt "decrypts" the data using XOR with the master key.
func (l *Local) Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	// XOR is symmetric
	plaintext := make([]byte, len(ciphertext))
	for i := range ciphertext {
		plaintext[i] = ciphertext[i] ^ l.masterKey[i%len(l.masterKey)]
	}

	return plaintext, nil
}

// GenerateDataKey generates a new data encryption key.
func (l *Local) GenerateDataKey(ctx context.Context) (plaintext, ciphertext []byte, err error) {
	// Generate a random 256-bit key
	plaintext = make([]byte, 32)
	if _, err := rand.Read(plaintext); err != nil {
		return nil, nil, fmt.Errorf("kms/local: generate random key: %w", err)
	}

	// Encrypt it with the master key
	ciphertext, err = l.Encrypt(ctx, plaintext)
	if err != nil {
		return nil, nil, err
	}

	return plaintext, ciphertext, nil
}

// Name returns the name of the KMS provider.
func (l *Local) Name() string {
	return "local"
}

// GenerateMasterKey generates a new random master key and returns it as hex.
// This can be used to create a new key file.
func GenerateMasterKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return hex.EncodeToString(key), nil
}
