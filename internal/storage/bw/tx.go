package bw

import (
	"context"
	"io"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/pika/internal/service"
)

// scope abstracts "where does this read/write happen" so the per-
// entity stores can run identically inside or outside a *bw.Tx. The
// helpers in scope.go consult scope.tx and pick either the standalone
// Bucket method or its *Tx-suffixed counterpart.
//
// Outside of a tx (scope.tx == nil) every call goes through the
// standalone bucket method, which manages its own short-lived view or
// update tx. Inside a tx every call goes through the matching
// *Tx-suffixed method so it observes the tx's pending writes — bw
// added GetTx/FindTx/WalkTx/CountTx in v0.2.0 specifically so this
// package no longer needs to drop down to *badger.Txn.
type scope struct {
	store *Storage
	tx    *bw.Tx // nil when this scope runs outside a tx
}

// dbScope returns a scope that auto-wraps each call into a fresh tx
// inside whatever bucket method is invoked.
func (s *Storage) dbScope() scope { return scope{store: s} }

// txScope returns a scope bound to an existing *bw.Tx.
func (s *Storage) txScope(tx *bw.Tx) scope { return scope{store: s, tx: tx} }

// Tx implements service.Storage.Tx. It opens a single bw read-write
// tx and hands every entity store a tx-bound scope. If fn returns an
// error the tx is discarded; otherwise it is committed atomically.
func (s *Storage) Tx(ctx context.Context, fn func(ctx context.Context, tx service.Storage) error) error {
	return s.db.Update(func(btx *bw.Tx) error {
		txStore := &txStorage{base: s, scope: s.txScope(btx)}
		return fn(ctx, txStore)
	})
}

// txStorage is service.Storage scoped to one bw write transaction.
// Every returned per-entity store funnels mutations through *bw.Tx
// (InsertTx/UpdateTx/DeleteTx) and reads through the matching tx-aware
// helpers (GetTx/FindTx/WalkTx/CountTx) so they observe the in-flight
// writes.
type txStorage struct {
	base  *Storage
	scope scope
}

// Tx forwards a nested call back into fn with the same tx storage.
// bw transactions are not nestable but the service code never relies
// on that; flattening keeps the pattern simple while still
// serializing under the outer tx.
func (t *txStorage) Tx(ctx context.Context, fn func(ctx context.Context, tx service.Storage) error) error {
	return fn(ctx, t)
}

// Backup forwards to the underlying *bw.DB. Backups inside a tx are
// not supported — Badger's streaming backup snapshots the whole DB,
// not a tx view — so this just forwards to the base storage. In
// practice the service layer never calls Backup from inside a Tx.
func (t *txStorage) Backup(w io.Writer, since uint64) (uint64, error) {
	return t.base.Backup(w, since)
}

// BackupUntil forwards to the underlying *bw.DB.
func (t *txStorage) BackupUntil(w io.Writer, until uint64) (uint64, error) {
	return t.base.BackupUntil(w, until)
}

// Restore forwards to the underlying *bw.DB.
func (t *txStorage) Restore(r io.Reader) error {
	return t.base.Restore(r)
}

// ApplyBackup forwards to the underlying *bw.DB. Like Restore, this is
// never called from inside a Tx in practice — the interface requires
// the method, so it forwards.
func (t *txStorage) ApplyBackup(r io.Reader) error {
	return t.base.ApplyBackup(r)
}

// Wipe forwards to the underlying *bw.DB. Calling Wipe inside a tx
// is pathological — Badger's DropAll blocks all writes — but the
// interface requires the method, so we forward it. The caller is
// expected to never invoke this path.
func (t *txStorage) Wipe() error {
	return t.base.Wipe()
}

// Version forwards to the underlying *bw.DB.
func (t *txStorage) Version() uint64 {
	return t.base.Version()
}
