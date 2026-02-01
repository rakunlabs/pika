package crypto

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	// KeySize is the size of ChaCha20-Poly1305 key (256 bits).
	KeySize = chacha20poly1305.KeySize

	// NonceSize is the size of XChaCha20-Poly1305 nonce (24 bytes).
	NonceSize = chacha20poly1305.NonceSizeX

	// VersionSize is the size of the key version in bytes (uint32).
	VersionSize = 4

	// HeaderSize is the total header size (version + nonce).
	HeaderSize = VersionSize + NonceSize
)

// ChaCha20Poly1305 implements Encryptor using XChaCha20-Poly1305.
type ChaCha20Poly1305 struct {
	keyStore KeyStore
	aead     cipher.AEAD
	version  uint32
}

// NewChaCha20Poly1305 creates a new ChaCha20-Poly1305 encryptor.
func NewChaCha20Poly1305(keyStore KeyStore) (*ChaCha20Poly1305, error) {
	// Get the active key
	key, version, err := keyStore.GetActiveKey()
	if err != nil {
		return nil, fmt.Errorf("failed to get active key: %w", err)
	}

	if len(key) != KeySize {
		return nil, ErrInvalidKeySize
	}

	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// Parse version as uint32
	var v uint32
	if _, err := fmt.Sscanf(version, "v%d", &v); err != nil {
		v = 1
	}

	return &ChaCha20Poly1305{
		keyStore: keyStore,
		aead:     aead,
		version:  v,
	}, nil
}

// Encrypt encrypts plaintext using XChaCha20-Poly1305.
// Ciphertext format: [version:4bytes][nonce:24bytes][ciphertext+tag]
func (c *ChaCha20Poly1305) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Create ciphertext buffer: version + nonce + ciphertext
	ciphertext := make([]byte, HeaderSize, HeaderSize+len(plaintext)+c.aead.Overhead())

	// Write version
	binary.BigEndian.PutUint32(ciphertext[:VersionSize], c.version)

	// Write nonce
	copy(ciphertext[VersionSize:HeaderSize], nonce)

	// Encrypt and append
	ciphertext = c.aead.Seal(ciphertext, nonce, plaintext, nil)

	return ciphertext, nil
}

// Decrypt decrypts ciphertext using XChaCha20-Poly1305.
// It automatically retrieves the correct key based on the embedded version.
func (c *ChaCha20Poly1305) Decrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < HeaderSize+c.aead.Overhead() {
		return nil, ErrInvalidCiphertext
	}

	// Extract version
	version := binary.BigEndian.Uint32(ciphertext[:VersionSize])

	// Extract nonce
	nonce := ciphertext[VersionSize:HeaderSize]

	// Get encrypted data
	encryptedData := ciphertext[HeaderSize:]

	// Get the key for this version
	versionStr := fmt.Sprintf("v%d", version)
	key, err := c.keyStore.GetKey(versionStr)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrKeyNotFound, versionStr)
	}

	if len(key) != KeySize {
		return nil, ErrInvalidKeySize
	}

	// Create AEAD for this key
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// Decrypt
	plaintext, err := aead.Open(nil, nonce, encryptedData, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	return plaintext, nil
}

// CurrentKeyVersion returns the current active key version.
func (c *ChaCha20Poly1305) CurrentKeyVersion() string {
	return fmt.Sprintf("v%d", c.version)
}

// GenerateKey generates a new random key suitable for ChaCha20-Poly1305.
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}
	return key, nil
}
