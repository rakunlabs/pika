package bw

import (
	"fmt"
	"io"
)

// Backup writes a Badger streaming backup to w starting at the given
// version. Pass since=0 for a full backup. Returns the latest version
// included so a follow-up incremental can chain off it.
//
// The output is the native Badger streaming format — opaque binary, not
// JSON. Restoring requires the same major Badger version.
func (s *Storage) Backup(w io.Writer, since uint64) (uint64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	return s.db.Backup(w, since, false)
}

// BackupUntil writes a point-in-time backup containing only entries
// with internal version ≤ until. The first return value is the latest
// version actually included in the stream.
func (s *Storage) BackupUntil(w io.Writer, until uint64) (uint64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	return s.db.BackupUntil(w, until)
}

// Restore replaces the database with the contents of a Badger streaming
// backup: bw drops every existing key first, so keys absent from the
// stream do not survive. Use ApplyBackup when you want a merge.
func (s *Storage) Restore(r io.Reader) error {
	if s == nil || s.db == nil {
		return nil
	}

	return s.reregisterAfter(s.db.Restore(r))
}

// ApplyBackup merges a Badger streaming backup into the database.
// Existing data with overlapping keys is overwritten by the stream, but
// keys absent from it are preserved.
func (s *Storage) ApplyBackup(r io.Reader) error {
	if s == nil || s.db == nil {
		return nil
	}

	return s.reregisterAfter(s.db.ApplyBackup(r))
}

// reregisterAfter re-opens every bucket once a restore has settled.
//
// bw invalidates the cached bucket handles whenever a restore replaces
// the on-disk schema state underneath them (Restore always, ApplyBackup
// for the buckets whose schema actually changed), and every subsequent
// operation on a stale handle fails with bw.ErrStaleBucket. The handles
// are long-lived fields on Storage, so without this the process would
// keep serving errors until it was restarted.
//
// Re-registration runs even when the restore failed: bw invalidates on
// its error paths too, and a half-applied restore that leaves the
// process permanently broken is worse than one that leaves it merely
// inconsistent — which the caller already has to handle.
func (s *Storage) reregisterAfter(restoreErr error) error {
	if err := s.registerBuckets(); err != nil {
		if restoreErr != nil {
			return restoreErr
		}

		return fmt.Errorf("re-registering buckets after restore: %w", err)
	}

	return restoreErr
}

// Version returns the current monotonic transaction version of the
// underlying *bw.DB. Increases on every write transaction commit.
func (s *Storage) Version() uint64 {
	if s == nil || s.db == nil {
		return 0
	}
	return s.db.Version()
}

// Wipe drops every key from the underlying database — data, indexes,
// unique reservations, schema metadata — and resets every registered
// FTS index. The in-process bw bucket handles cached on this Storage
// stay valid (their schema/encoder fields are derived from Go types,
// not on-disk state) so the next write into a bucket transparently
// repopulates whatever bw needs.
//
// Wipe is destructive and irreversible. Callers should pair it with
// a confirmation prompt and a pre-validated restore stream.
//
// The heavy lifting (DropAll + FTS reset) lives in bw.DB.Wipe; this
// method is a thin pass-through.
func (s *Storage) Wipe() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Wipe()
}
