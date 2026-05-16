// Package envelope wraps plaintext bytes in a self-describing
// encrypted container suitable for at-rest storage.
//
// Wire format (little-endian byte order, version-tagged):
//
//	[0]      magic byte: 0x01 (single-cipher chacha20-poly1305 envelope)
//	[1..N]   ciphertext as produced by crypto.ChaCha20Encryptor.Encrypt
//	         (nonce || sealed_payload || tag)
//
// The leading magic byte exists so future format upgrades (a v2 with
// per-field associated data, an HSM-backed v3, etc.) can land
// without breaking on-disk compatibility — Open() looks at byte 0
// and dispatches. Today only v1 exists; mismatches return
// ErrUnknownFormat.
//
// # Why a separate package
//
// internal/secret/store.go and internal/service/keyops.go both need
// the same Encrypt/Decrypt envelope, but they have different
// callers (storage operations vs. lifecycle endpoints). Putting the
// envelope here breaks the cycle: storage and service both depend
// on this package, neither depends on the other for crypto.
//
// # Locked-state behaviour
//
// Both Seal and Open consult the *keymgr.Manager. If the server is
// locked, Seal returns ErrLocked (writes fail closed — better to
// 5xx than to silently store plaintext); Open also returns
// ErrLocked so the caller can decide whether to substitute a
// "sealed" placeholder or surface the lock state to the user.
package envelope

import (
	"errors"
	"fmt"

	"github.com/rakunlabs/pika/internal/secret/keymgr"
)

// ErrLocked indicates the server's at-rest key is not currently in
// memory. Wrapping the same sentinel as keymgr.ErrLocked would
// create an import cycle once that package grows; we re-define our
// own and assert equality at the boundary.
var ErrLocked = errors.New("envelope: server is locked")

// ErrUnknownFormat is returned by Open when the magic byte doesn't
// match a known version. Should be impossible in practice on
// freshly-rotated data; surfaces if a downgrade is attempted or the
// row was written by a future binary.
var ErrUnknownFormat = errors.New("envelope: unknown format byte")

// formatV1 is the magic byte for the only currently-defined wire
// format: a single chacha20-poly1305 envelope produced by the live
// keymgr encryptor.
const formatV1 byte = 0x01

// Seal encrypts plaintext with the live key from mgr and prepends
// the format byte. Returns ErrLocked if the manager has no live
// key. nil/empty plaintext seals to a deterministic short blob (the
// magic byte + an AEAD tag for the empty input) — callers that want
// to skip nil values should test before calling.
func Seal(mgr *keymgr.Manager, plaintext []byte) ([]byte, error) {
	if mgr == nil {
		return nil, fmt.Errorf("envelope: nil manager")
	}
	enc, ok := mgr.Encryptor()
	if !ok {
		return nil, ErrLocked
	}
	ct, err := enc.Encrypt(plaintext)
	if err != nil {
		return nil, fmt.Errorf("envelope: encrypt: %w", err)
	}
	out := make([]byte, 0, len(ct)+1)
	out = append(out, formatV1)
	out = append(out, ct...)
	return out, nil
}

// Open decrypts an envelope previously produced by Seal. Dispatches
// on the first byte; an unrecognized magic returns ErrUnknownFormat
// so the caller knows the failure is structural, not authentication
// (the AEAD-failure path returns crypto.ErrDecryptionFailed
// wrapped).
//
// nil/empty input is rejected with a wrapped ErrUnknownFormat
// because no valid envelope can be that short — callers that want
// to treat "no ciphertext" as "no data" should test the input
// before calling.
func Open(mgr *keymgr.Manager, ciphertext []byte) ([]byte, error) {
	if mgr == nil {
		return nil, fmt.Errorf("envelope: nil manager")
	}
	if len(ciphertext) == 0 {
		return nil, fmt.Errorf("envelope: empty input: %w", ErrUnknownFormat)
	}
	switch ciphertext[0] {
	case formatV1:
		enc, ok := mgr.Encryptor()
		if !ok {
			return nil, ErrLocked
		}
		pt, err := enc.Decrypt(ciphertext[1:])
		if err != nil {
			return nil, fmt.Errorf("envelope: decrypt: %w", err)
		}
		return pt, nil
	default:
		return nil, fmt.Errorf("envelope: byte 0x%02x: %w", ciphertext[0], ErrUnknownFormat)
	}
}

// IsSealed inspects the leading magic byte and reports whether the
// blob is a recognized envelope. Useful for migration code that
// wants to rewrap legacy plaintext rows: a `false` return means
// "treat as plaintext, then Seal it on next write".
func IsSealed(blob []byte) bool {
	if len(blob) == 0 {
		return false
	}
	return blob[0] == formatV1
}
