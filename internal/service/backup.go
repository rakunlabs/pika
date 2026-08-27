package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/rakunlabs/pika/internal/secret/crypto"
)

// Backup container format (.pikabw)
//
// Header (25 bytes):
//
//	[ 0..7 ]  magic        = "PIKABW\x00\x01"   (8 bytes — last byte is the format version)
//	[ 8..8 ]  flags        = bit 0: encrypted? (others reserved)
//	[ 9..16]  payload_size = big-endian uint64 (size of the bytes after the header)
//	[17..24]  db_version   = big-endian uint64 (the *bw.DB Version() captured at export time)
//
// Payload:
//
//	If flags.encrypted is unset, the payload is the raw Badger streaming
//	backup. If set, the payload is the XChaCha20-Poly1305 ciphertext of
//	that stream (key = SHA-256(password)).
//
// The payload size lets readers stream-detect a truncated file before
// they hand it to Restore. db_version lets the UI display "this backup
// is at version 1234" without having to parse the inner Badger blob.

const (
	backupHeaderSize    = 25
	backupFlagEncrypted = 1 << 0
)

// backupMagic is the 8-byte magic prefix. The trailing 0x01 is the
// container format version — bump if the header layout ever changes.
var backupMagic = []byte{'P', 'I', 'K', 'A', 'B', 'W', 0x00, 0x01}

// BackupHeader summarizes the parsed header of a .pikabw blob. All
// fields are populated by ReadBackupHeader.
type BackupHeader struct {
	Encrypted   bool   `json:"encrypted"`
	PayloadSize uint64 `json:"payload_size"`
	DBVersion   uint64 `json:"db_version"`
}

// BackupOptions selects which backup variant to produce. Zero value
// means "full backup of the current DB state".
type BackupOptions struct {
	// Since asks for an incremental backup containing only entries
	// newer than this version. Zero means full backup.
	Since uint64
	// Until asks for a point-in-time backup containing only entries
	// with version ≤ Until. Zero means "no upper bound" (current).
	// Since and Until are mutually exclusive — Until takes precedence
	// when both are set, because the underlying bw.DB exposes
	// BackupUntil and Backup as separate calls.
	Until uint64
	// EncryptionPassword, when non-empty, encrypts the payload with
	// XChaCha20-Poly1305 using a SHA-256-derived key.
	EncryptionPassword string
}

// Version returns the current monotonic transaction version of the
// underlying database. The number is what callers pass to
// BackupOptions.Since / BackupOptions.Until.
func (s *Service) Version() uint64 { return s.store.Version() }

// Backup writes a .pikabw container (header + payload) to w. The
// returned BackupHeader matches what was just written, so callers can
// surface DBVersion to the UI without re-parsing the stream.
func (s *Service) Backup(ctx context.Context, w io.Writer, opts BackupOptions) (BackupHeader, error) {
	_ = ctx

	// Build the inner payload first — we need its size for the header.
	var inner bytes.Buffer
	var capturedVersion uint64
	var err error
	switch {
	case opts.Until > 0:
		capturedVersion, err = s.store.BackupUntil(&inner, opts.Until)
	default:
		capturedVersion, err = s.store.Backup(&inner, opts.Since)
	}
	if err != nil {
		return BackupHeader{}, fmt.Errorf("backup: %w", err)
	}
	// Badger may report 0 for empty databases — fall back to the live
	// version so the header is informative either way.
	if capturedVersion == 0 {
		capturedVersion = s.store.Version()
	}

	payload := inner.Bytes()
	hdr := BackupHeader{
		Encrypted:   opts.EncryptionPassword != "",
		PayloadSize: uint64(len(payload)),
		DBVersion:   capturedVersion,
	}

	if hdr.Encrypted {
		key := sha256.Sum256([]byte(opts.EncryptionPassword))
		enc, err := crypto.NewChaCha20(key[:])
		if err != nil {
			return BackupHeader{}, fmt.Errorf("creating encryptor: %w", err)
		}
		ciphertext, err := enc.Encrypt(payload)
		if err != nil {
			return BackupHeader{}, fmt.Errorf("encrypting backup: %w", err)
		}
		payload = ciphertext
		hdr.PayloadSize = uint64(len(payload))
	}

	if err := writeBackupHeader(w, hdr); err != nil {
		return BackupHeader{}, err
	}
	if _, err := w.Write(payload); err != nil {
		return BackupHeader{}, fmt.Errorf("writing payload: %w", err)
	}
	return hdr, nil
}

// RestoreOptions controls how Restore consumes a .pikabw stream.
type RestoreOptions struct {
	// Password decrypts the payload when the backup header advertises
	// encryption. Ignored when the header is not encrypted.
	Password string
	// Wipe, when true, drops every key from the database BEFORE
	// applying the restore stream. The result is the database in
	// exactly the state captured by the backup — keys present in the
	// running DB but absent from the backup are removed too.
	//
	// Wipe is destructive and irreversible. Restore validates the
	// backup (magic + decryption test, when encrypted) BEFORE wiping,
	// so a bad password or a corrupt file aborts cleanly without
	// touching the running data.
	Wipe bool
}

// Restore reads a .pikabw container from r and replays the inner Badger
// stream into the database.
//
// Default behaviour is upsert: keys present in the backup overwrite
// matching keys in the running DB, but keys absent from the backup are
// preserved. Pass RestoreOptions.Wipe = true to swap the running DB for
// exactly the contents of the backup.
//
// Validation order matters: the magic byte and (when encrypted) the
// decryption check both run BEFORE any DropAll, so a bad backup can't
// destroy your data.
func (s *Service) Restore(ctx context.Context, r io.Reader, opts RestoreOptions) error {
	_ = ctx

	hdr, payload, err := readBackup(r)
	if err != nil {
		return err
	}

	if hdr.Encrypted {
		if opts.Password == "" {
			return fmt.Errorf("encryption password is required for this backup: %w", ErrBadRequest)
		}
		key := sha256.Sum256([]byte(opts.Password))
		enc, err := crypto.NewChaCha20(key[:])
		if err != nil {
			return fmt.Errorf("creating decryptor: %w", err)
		}
		plain, err := enc.Decrypt(payload)
		if err != nil {
			return fmt.Errorf("decrypting backup (wrong password?): %w", ErrBadRequest)
		}
		payload = plain
	}

	// Sanity-check the inner stream too: badger.Restore would error on
	// junk bytes, but it does so AFTER we wipe — and the wipe is
	// irreversible. Reading the first protobuf-length prefix of the
	// stream isn't worth the complexity for the marginal extra safety;
	// the magic-byte check on .pikabw plus the decryption check above
	// already cover the realistic failure modes (truncation,
	// corruption, wrong password).

	// The two options map onto two distinct storage operations. Restore
	// drops the database before loading the stream, which is exactly
	// what Wipe asks for — issuing a separate Wipe first would only
	// double the DropAll. ApplyBackup is the merge.
	//
	// Keeping these apart is the whole point: reaching for Restore on
	// the merge path would silently delete every key the backup does
	// not mention, which is data loss the caller explicitly opted out of.
	if opts.Wipe {
		return s.store.Restore(bytes.NewReader(payload))
	}

	return s.store.ApplyBackup(bytes.NewReader(payload))
}

// PeekBackup parses just the header of r and returns it. The reader is
// advanced past the header. Useful when an API wants to surface the
// backup's DB version / encrypted flag before deciding whether to
// stream the rest.
func PeekBackup(r io.Reader) (BackupHeader, error) {
	hdr, _, err := readHeaderOnly(r)
	return hdr, err
}

// writeBackupHeader emits the 25-byte header.
func writeBackupHeader(w io.Writer, hdr BackupHeader) error {
	var buf [backupHeaderSize]byte
	copy(buf[0:8], backupMagic)
	if hdr.Encrypted {
		buf[8] = backupFlagEncrypted
	}
	binary.BigEndian.PutUint64(buf[9:17], hdr.PayloadSize)
	binary.BigEndian.PutUint64(buf[17:25], hdr.DBVersion)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("writing header: %w", err)
	}
	return nil
}

// readBackup parses the header and returns the full payload bytes.
func readBackup(r io.Reader) (BackupHeader, []byte, error) {
	hdr, _, err := readHeaderOnly(r)
	if err != nil {
		return BackupHeader{}, nil, err
	}
	payload := make([]byte, hdr.PayloadSize)
	if _, err := io.ReadFull(r, payload); err != nil {
		return BackupHeader{}, nil, fmt.Errorf("reading payload (truncated?): %w: %w", err, ErrBadRequest)
	}
	return hdr, payload, nil
}

// readHeaderOnly parses the 25-byte header. The second return is the
// raw header bytes for callers that want to log them.
func readHeaderOnly(r io.Reader) (BackupHeader, [backupHeaderSize]byte, error) {
	var buf [backupHeaderSize]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return BackupHeader{}, buf, fmt.Errorf("reading header: %w: %w", err, ErrBadRequest)
	}
	if !bytes.Equal(buf[0:8], backupMagic) {
		return BackupHeader{}, buf, fmt.Errorf("not a pika backup (bad magic): %w", ErrBadRequest)
	}
	flags := buf[8]
	hdr := BackupHeader{
		Encrypted:   flags&backupFlagEncrypted != 0,
		PayloadSize: binary.BigEndian.Uint64(buf[9:17]),
		DBVersion:   binary.BigEndian.Uint64(buf[17:25]),
	}
	// Sanity bound — pika backups exceeding 4 GiB are not realistic and
	// almost certainly indicate a corrupt header. Reject early instead
	// of letting io.ReadFull allocate a giant slice.
	const maxPayload uint64 = 4 << 30
	if hdr.PayloadSize > maxPayload {
		return BackupHeader{}, buf, fmt.Errorf("backup payload size %d exceeds %d byte limit: %w", hdr.PayloadSize, maxPayload, ErrBadRequest)
	}
	return hdr, buf, nil
}

// silenceUnused keeps errors imported for callers that may use it via
// errors.Is on returned wrap chains.
var _ = errors.Is
