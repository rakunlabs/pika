package bw

import (
	"context"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/query"
)

// Helpers in this file dispatch a typed bw bucket call to the right
// path depending on whether the per-store scope owns a *bw.Tx. Inside
// a tx every call has to use the *Tx-suffixed variants so it observes
// the in-flight writes; outside a tx the standalone helpers are
// preferred because they manage their own short-lived view tx.
//
// The previous version of this file (now removed) reached down to
// *badger.Txn and decoded values via db.Codec() to work around bw's
// historical lack of GetTx/FindTx/WalkTx/CountTx. With those landed
// upstream the workaround is gone and every per-store file calls
// these helpers directly.

// bucketGet routes Bucket.Get / GetTx based on the scope.
func bucketGet[T any](ctx context.Context, sc scope, b *bw.Bucket[T], key any) (*T, error) {
	if sc.tx != nil {
		row, err := b.GetTx(sc.tx, key)
		return row, translateErr(err)
	}
	row, err := b.Get(ctx, key)
	return row, translateErr(err)
}

// bucketInsert routes Bucket.Insert / InsertTx.
func bucketInsert[T any](ctx context.Context, sc scope, b *bw.Bucket[T], row *T) error {
	if sc.tx != nil {
		return translateErr(b.InsertTx(sc.tx, row))
	}
	return translateErr(b.Insert(ctx, row))
}

// bucketInsertNew routes Bucket.InsertNew / InsertNewTx — same as
// bucketInsert but returns ErrConflict when the pk already exists.
func bucketInsertNew[T any](ctx context.Context, sc scope, b *bw.Bucket[T], row *T) error {
	if sc.tx != nil {
		return translateErr(b.InsertNewTx(sc.tx, row))
	}
	return translateErr(b.InsertNew(ctx, row))
}

// bucketUpdate routes Bucket.Update / UpdateTx.
func bucketUpdate[T any](ctx context.Context, sc scope, b *bw.Bucket[T], row *T) error {
	if sc.tx != nil {
		return translateErr(b.UpdateTx(sc.tx, row))
	}
	return translateErr(b.Update(ctx, row))
}

// bucketDelete routes Bucket.Delete / DeleteTx.
func bucketDelete[T any](ctx context.Context, sc scope, b *bw.Bucket[T], key any) error {
	if sc.tx != nil {
		return translateErr(b.DeleteTx(sc.tx, key))
	}
	return translateErr(b.Delete(ctx, key))
}

// bucketWalk routes Bucket.Walk / WalkTx. q may be nil.
func bucketWalk[T any](ctx context.Context, sc scope, b *bw.Bucket[T], q *query.Query, fn func(*T) error) error {
	if sc.tx != nil {
		return translateErr(b.WalkTx(sc.tx, q, fn))
	}
	return translateErr(b.Walk(ctx, q, fn))
}

// bucketFind routes Bucket.Find / FindTx.
func bucketFind[T any](ctx context.Context, sc scope, b *bw.Bucket[T], q *query.Query) ([]*T, error) {
	if sc.tx != nil {
		rows, err := b.FindTx(sc.tx, q)
		return rows, translateErr(err)
	}
	rows, err := b.Find(ctx, q)
	return rows, translateErr(err)
}

// bucketCount routes Bucket.Count / CountTx.
func bucketCount[T any](ctx context.Context, sc scope, b *bw.Bucket[T], q *query.Query) (uint64, error) {
	if sc.tx != nil {
		n, err := b.CountTx(sc.tx, q)
		return n, translateErr(err)
	}
	n, err := b.Count(ctx, q)
	return n, translateErr(err)
}
