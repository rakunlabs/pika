package bw

import (
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

// Restore replays a Badger streaming backup into the database. Existing
// data with overlapping keys is overwritten by the restore stream.
//
// Important: Restore does NOT wipe keys that exist in the current DB but
// are absent from the backup stream — it's an upsert, not a swap.
func (s *Storage) Restore(r io.Reader) error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Restore(r)
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
