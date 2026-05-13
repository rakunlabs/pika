package bw

import (
	"context"
	"sort"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/query"
)

// vaultItemVersionStorage implements service.VaultItemVersionStorage.
//
// History is append-only from the service layer: every UpdateItem
// inserts one row, keyed by the composite (item_id, version) — see
// vaultItemVersionKey in buckets.go. The composite key is enforced by
// bw's WithKeyFn registration so a buggy re-append of the same version
// is rejected as ErrConflict.
//
// Two index columns serve the two delete paths:
//
//   - item_id: DeleteAllByItem (called by hard delete on an item)
//   - user_id: DeleteAllByUser (called by user delete cascade and
//     Reset on the vault account)
type vaultItemVersionStorage struct {
	store  *Storage
	bucket *bw.Bucket[vaultItemVersionRow]
	scope  scope
}

func (s *Storage) vaultItemVersionsAt(sc scope) *vaultItemVersionStorage {
	return &vaultItemVersionStorage{store: s, bucket: s.vaultItemVersions, scope: sc}
}

func (s *Storage) VaultItemVersions() service.VaultItemVersionStorage {
	return s.vaultItemVersionsAt(s.dbScope())
}

func (t *txStorage) VaultItemVersions() service.VaultItemVersionStorage {
	return t.base.vaultItemVersionsAt(t.scope)
}

// Append inserts a single history entry. The service layer is the
// only allowed caller (it owns the version-incrementing logic). We
// take the userID separately so the row can record it for the
// DeleteAllByUser index; the service.VaultItemVersion model itself
// is user-agnostic since the API never returns it cross-user.
func (s *vaultItemVersionStorage) Append(ctx context.Context, v *service.VaultItemVersion) error {
	if v == nil || v.ItemID == "" {
		return service.ErrBadRequest
	}
	// Look up the live item to discover its owning user_id so we
	// don't need to plumb userID through the public Append signature.
	// In a tx the caller has already loaded the item to mutate it;
	// the extra Get here is a small price for a clean public API.
	row, err := bucketGet(ctx, s.scope, s.store.vaultItems, v.ItemID)
	if err != nil {
		return err
	}
	return bucketInsertNew(ctx, s.scope, s.bucket, vaultItemVersionRowFromService(row.UserID, v))
}

func (s *vaultItemVersionStorage) ListByItem(ctx context.Context, itemID string) ([]service.VaultItemVersion, error) {
	if itemID == "" {
		return nil, service.ErrBadRequest
	}
	q := query.New().AddWhere(query.NewExpressionCmp(query.OperatorEq, "item_id", itemID))
	rows, err := bucketFind(ctx, s.scope, s.bucket, q)
	if err != nil {
		return nil, err
	}
	out := make([]service.VaultItemVersion, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r.toService())
	}
	// Newest version first — easier to render the "previous edits"
	// list with the most recent revision at the top.
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out, nil
}

func (s *vaultItemVersionStorage) Get(ctx context.Context, itemID string, version int64) (*service.VaultItemVersion, error) {
	if itemID == "" {
		return nil, service.ErrBadRequest
	}
	key := vaultItemVersionKey(itemID, version)
	row, err := bucketGet(ctx, s.scope, s.bucket, key)
	if err != nil {
		return nil, err
	}
	return row.toService(), nil
}

func (s *vaultItemVersionStorage) DeleteAllByItem(ctx context.Context, itemID string) error {
	q := query.New().AddWhere(query.NewExpressionCmp(query.OperatorEq, "item_id", itemID))
	rows, err := bucketFind(ctx, s.scope, s.bucket, q)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := bucketDelete(ctx, s.scope, s.bucket, vaultItemVersionKey(r.ItemID, r.Version)); err != nil {
			return err
		}
	}
	return nil
}

func (s *vaultItemVersionStorage) DeleteAllByUser(ctx context.Context, userID string) error {
	q := query.New().AddWhere(query.NewExpressionCmp(query.OperatorEq, "user_id", userID))
	rows, err := bucketFind(ctx, s.scope, s.bucket, q)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := bucketDelete(ctx, s.scope, s.bucket, vaultItemVersionKey(r.ItemID, r.Version)); err != nil {
			return err
		}
	}
	return nil
}
