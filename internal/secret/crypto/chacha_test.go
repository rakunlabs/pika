package crypto

import (
	"bytes"
	"errors"
	"testing"
)

func newTestEncryptor(t *testing.T) *ChaCha20Encryptor {
	t.Helper()
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := NewChaCha20(key)
	if err != nil {
		t.Fatalf("NewChaCha20: %v", err)
	}
	return enc
}

func TestGenerateKey(t *testing.T) {
	k1, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	k2, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if len(k1) != KeySize || len(k2) != KeySize {
		t.Fatalf("key sizes = %d, %d; want %d", len(k1), len(k2), KeySize)
	}
	if bytes.Equal(k1, k2) {
		t.Fatal("two generated keys are identical")
	}
}

func TestNewChaCha20_KeySize(t *testing.T) {
	for _, n := range []int{0, 1, 16, KeySize - 1, KeySize + 1, 64} {
		if _, err := NewChaCha20(make([]byte, n)); !errors.Is(err, ErrInvalidKeySize) {
			t.Errorf("NewChaCha20(len=%d) err = %v; want ErrInvalidKeySize", n, err)
		}
	}
	if _, err := NewChaCha20(make([]byte, KeySize)); err != nil {
		t.Errorf("NewChaCha20(len=%d) err = %v; want nil", KeySize, err)
	}
}

func TestNewChaCha20_CopiesKey(t *testing.T) {
	key, _ := GenerateKey()
	enc, err := NewChaCha20(key)
	if err != nil {
		t.Fatalf("NewChaCha20: %v", err)
	}
	ct, err := enc.Encrypt([]byte("hello"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Mutating the caller's slice must not affect the encryptor.
	for i := range key {
		key[i] = 0
	}
	pt, err := enc.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt after caller key mutation: %v", err)
	}
	if string(pt) != "hello" {
		t.Fatalf("plaintext = %q", pt)
	}
}

func TestChaCha20_RoundTrip(t *testing.T) {
	enc := newTestEncryptor(t)
	cases := map[string][]byte{
		"empty": {},
		"short": []byte("x"),
		"text":  []byte("the quick brown fox jumps over the lazy dog"),
		"large": bytes.Repeat([]byte{0xAB}, 1<<16),
	}
	for name, pt := range cases {
		t.Run(name, func(t *testing.T) {
			ct, err := enc.Encrypt(pt)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			if want := NonceSize + len(pt) + 16; len(ct) != want {
				t.Fatalf("ciphertext len = %d; want %d", len(ct), want)
			}
			got, err := enc.Decrypt(ct)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if !bytes.Equal(got, pt) {
				t.Fatalf("round trip mismatch")
			}
		})
	}
}

func TestChaCha20_NilPlaintext(t *testing.T) {
	enc := newTestEncryptor(t)
	ct, err := enc.Encrypt(nil)
	if err != nil {
		t.Fatalf("Encrypt(nil): %v", err)
	}
	got, err := enc.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d bytes; want 0", len(got))
	}
}

func TestChaCha20_NonceUniqueness(t *testing.T) {
	enc := newTestEncryptor(t)
	pt := []byte("same plaintext")
	seen := make(map[string]struct{})
	for range 100 {
		ct, err := enc.Encrypt(pt)
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		nonce := string(ct[:NonceSize])
		if _, ok := seen[nonce]; ok {
			t.Fatal("nonce reused")
		}
		seen[nonce] = struct{}{}
	}

	a, _ := enc.Encrypt(pt)
	b, _ := enc.Encrypt(pt)
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of same plaintext are identical")
	}
}

func TestChaCha20_Tamper(t *testing.T) {
	enc := newTestEncryptor(t)
	ct, err := enc.Encrypt([]byte("integrity matters"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	for _, idx := range []int{0, NonceSize - 1, NonceSize, len(ct) / 2, len(ct) - 1} {
		tampered := bytes.Clone(ct)
		tampered[idx] ^= 0x01
		if _, err := enc.Decrypt(tampered); !errors.Is(err, ErrDecryptionFailed) {
			t.Errorf("Decrypt(tampered@%d) err = %v; want ErrDecryptionFailed", idx, err)
		}
	}

	// Truncation that still keeps the minimum length.
	if _, err := enc.Decrypt(ct[:len(ct)-1]); !errors.Is(err, ErrDecryptionFailed) {
		t.Errorf("Decrypt(truncated) err = %v; want ErrDecryptionFailed", err)
	}
	// Appended byte.
	if _, err := enc.Decrypt(append(bytes.Clone(ct), 0)); !errors.Is(err, ErrDecryptionFailed) {
		t.Errorf("Decrypt(extended) err = %v; want ErrDecryptionFailed", err)
	}
}

func TestChaCha20_ShortCiphertext(t *testing.T) {
	enc := newTestEncryptor(t)
	for _, n := range []int{0, 1, NonceSize, NonceSize + 15} {
		if _, err := enc.Decrypt(make([]byte, n)); !errors.Is(err, ErrInvalidCiphertext) {
			t.Errorf("Decrypt(len=%d) err = %v; want ErrInvalidCiphertext", n, err)
		}
	}
}

func TestChaCha20_WrongKey(t *testing.T) {
	a := newTestEncryptor(t)
	b := newTestEncryptor(t)
	ct, err := a.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := b.Decrypt(ct); !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("Decrypt with wrong key err = %v; want ErrDecryptionFailed", err)
	}
}

func TestRotatedEncryptor(t *testing.T) {
	old := newTestEncryptor(t)
	oldCT, err := old.Encrypt([]byte("old data"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	newKey, _ := GenerateKey()
	rot, err := old.RotateTo(newKey)
	if err != nil {
		t.Fatalf("RotateTo: %v", err)
	}

	pt, err := rot.Decrypt(oldCT)
	if err != nil || string(pt) != "old data" {
		t.Fatalf("Decrypt old ciphertext = %q, %v", pt, err)
	}

	newCT, err := rot.Encrypt([]byte("new data"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	pt, err = rot.Decrypt(newCT)
	if err != nil || string(pt) != "new data" {
		t.Fatalf("Decrypt new ciphertext = %q, %v", pt, err)
	}

	// New ciphertexts must be sealed with the new key only.
	if _, err := old.Decrypt(newCT); err == nil {
		t.Fatal("old key decrypted ciphertext produced after rotation")
	}
	newOnly, _ := NewChaCha20(newKey)
	if _, err := newOnly.Decrypt(newCT); err != nil {
		t.Fatalf("new key cannot decrypt rotated ciphertext: %v", err)
	}

	// Unknown key fails with both.
	other := newTestEncryptor(t)
	otherCT, _ := other.Encrypt([]byte("x"))
	if _, err := rot.Decrypt(otherCT); !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("Decrypt foreign ciphertext err = %v; want ErrDecryptionFailed", err)
	}
}

func TestRotateTo_InvalidKey(t *testing.T) {
	enc := newTestEncryptor(t)
	if _, err := enc.RotateTo(make([]byte, 5)); !errors.Is(err, ErrInvalidKeySize) {
		t.Fatalf("RotateTo(bad) err = %v; want ErrInvalidKeySize", err)
	}
}

func TestChaCha20_Concurrent(t *testing.T) {
	enc := newTestEncryptor(t)
	done := make(chan error, 8)
	for range 8 {
		go func() {
			for range 50 {
				ct, err := enc.Encrypt([]byte("c"))
				if err != nil {
					done <- err
					return
				}
				if _, err := enc.Decrypt(ct); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}()
	}
	for range 8 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

var _ Encryptor = (*ChaCha20Encryptor)(nil)
var _ Encryptor = (*RotatedEncryptor)(nil)
