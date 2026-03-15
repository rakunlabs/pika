package crypto

import (
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"sync"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	// KeySize is the required key size (256 bits / 32 bytes).
	KeySize = chacha20poly1305.KeySize

	// NonceSize is the XChaCha20-Poly1305 nonce size (24 bytes).
	NonceSize = chacha20poly1305.NonceSizeX
)

// ChaCha20Encryptor implements Encryptor using XChaCha20-Poly1305.
type ChaCha20Encryptor struct {
	mu   sync.RWMutex
	key  []byte
	aead cipher.AEAD
}

// NewChaCha20(key) creates a new encryptor with the given 32-byte key.
func NewChaCha20(key []byte) (*ChaCha20Encryptor, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKeySize
	}

	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("creating cipher: %w", err)
	}

	keyCopy := make([]byte, KeySize)
	copy(keyCopy, key)

	return &ChaCha20Encryptor{
		key:  keyCopy,
		aead: aead,
	}, nil
}

// Encrypt encrypts plaintext. Format: [nonce:24][ciphertext+tag].
func (c *ChaCha20Encryptor) Encrypt(plaintext []byte) ([]byte, error) {
	c.mu.RLock()
	aead := c.aead
	c.mu.RUnlock()

	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generating nonce: %w", err)
	}

	ciphertext := aead.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt decrypts ciphertext.
func (c *ChaCha20Encryptor) Decrypt(ciphertext []byte) ([]byte, error) {
	c.mu.RLock()
	aead := c.aead
	c.mu.RUnlock()

	if len(ciphertext) < NonceSize+aead.Overhead() {
		return nil, ErrInvalidCiphertext
	}

	nonce := ciphertext[:NonceSize]
	data := ciphertext[NonceSize:]

	plaintext, err := aead.Open(nil, nonce, data, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	return plaintext, nil
}

// RotateTo creates a new encryptor with the new key while keeping
// the old key for decryption of existing data. Returns a RotatedEncryptor.
func (c *ChaCha20Encryptor) RotateTo(newKey []byte) (*RotatedEncryptor, error) {
	newEnc, err := NewChaCha20(newKey)
	if err != nil {
		return nil, err
	}

	return &RotatedEncryptor{
		current: newEnc,
		old:     c,
	}, nil
}

// RotatedEncryptor encrypts with the new key but can decrypt with both old and new.
type RotatedEncryptor struct {
	current *ChaCha20Encryptor
	old     *ChaCha20Encryptor
}

func (r *RotatedEncryptor) Encrypt(plaintext []byte) ([]byte, error) {
	return r.current.Encrypt(plaintext)
}

func (r *RotatedEncryptor) Decrypt(ciphertext []byte) ([]byte, error) {
	// Try new key first
	plaintext, err := r.current.Decrypt(ciphertext)
	if err == nil {
		return plaintext, nil
	}

	// Fall back to old key
	return r.old.Decrypt(ciphertext)
}

// GenerateKey generates a new random 32-byte key.
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generating key: %w", err)
	}
	return key, nil
}
