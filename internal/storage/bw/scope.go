package bw

import (
	"cmp"
	"context"
	"reflect"
	"slices"
	"strings"
	"time"

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

// bucketFindSorted is bucketFind for buckets whose sortable fields include
// time.Time columns, which bw's typed sort can't order (it treats structs
// as equal). When q sorts on one of timeFields, the filtered rows are
// fetched unsorted and sorted/paged here; otherwise bw does everything.
func bucketFindSorted[T any](ctx context.Context, sc scope, b *bw.Bucket[T], q *query.Query, timeFields map[string]func(*T) time.Time) ([]*T, error) {
	if q == nil || len(q.Sort) == 0 || !sortsOnAny(q.Sort, timeFields) {
		return bucketFind(ctx, sc, b, q)
	}

	scan := *q
	scan.Sort = nil
	scan.Offset = nil
	scan.Limit = nil
	rows, err := bucketFind(ctx, sc, b, &scan)
	if err != nil {
		return nil, err
	}

	spec := q.Sort
	slices.SortStableFunc(rows, func(a, c *T) int {
		for _, s := range spec {
			var r int
			if get, ok := timeFields[s.Field]; ok {
				r = get(a).Compare(get(c))
			} else {
				r = compareBWField(a, c, s.Field)
			}
			if s.Desc {
				r = -r
			}
			if r != 0 {
				return r
			}
		}
		return 0
	})

	offset, limit := q.GetOffset(), q.GetLimit()
	if offset >= uint64(len(rows)) {
		return []*T{}, nil
	}
	rows = rows[offset:]
	if limit > 0 && uint64(len(rows)) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func sortsOnAny[T any](spec []query.ExpressionSort, fields map[string]func(*T) time.Time) bool {
	for _, s := range spec {
		if _, ok := fields[s.Field]; ok {
			return true
		}
	}
	return false
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// compareBWField compares the field tagged `bw:"<name>"` on two rows of
// the same type. Only strings, ints, uints and bools are ordered; other
// kinds compare equal.
func compareBWField[T any](a, b *T, name string) int {
	av, bv := reflect.ValueOf(a).Elem(), reflect.ValueOf(b).Elem()
	t := av.Type()
	for i := range t.NumField() {
		tag, _, _ := strings.Cut(t.Field(i).Tag.Get("bw"), ",")
		if tag != name {
			continue
		}
		x, y := av.Field(i), bv.Field(i)
		switch x.Kind() {
		case reflect.String:
			return strings.Compare(x.String(), y.String())
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return cmp.Compare(x.Int(), y.Int())
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return cmp.Compare(x.Uint(), y.Uint())
		case reflect.Bool:
			return cmp.Compare(boolInt(x.Bool()), boolInt(y.Bool()))
		}
		return 0
	}
	return 0
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
