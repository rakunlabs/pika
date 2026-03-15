package crypto

import (
	"errors"
)

var (
	ErrInvalidCiphertext = errors.New("invalid ciphertext")
	ErrDecryptionFailed  = errors.New("decryption failed")
	ErrInvalidKeySize    = errors.New("invalid key size: must be 32 bytes")
)

// Encryptor defines the interface for encryption operations.
type Encryptor interface {
	Encrypt(plaintext []byte) (ciphertext []byte, err error)
	Decrypt(ciphertext []byte) (plaintext []byte, err error)
}
