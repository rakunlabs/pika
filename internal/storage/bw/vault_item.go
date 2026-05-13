package bw

import (
	"context"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/query"
)

// vaultItemStorage implements service.VaultItemStorage.
//
// Access pattern: every API path lists or fetches items for a single
// user. user_id is indexed on the row so ListByUserID + filter is a
// bounded scan over that user's partition. type is also indexed so
// the SPA's type filter narrows the scan further without an in-memory
// pass.
//
// Defense-in-depth: Get/Delete take both userID and itemID and the
// implementation verifies the row's UserID matches before returning.
// Without this an attacker who guesses another user's item id could
// fetch their encrypted payload (still useless without the key, but
// the metadata — title, tags, hostnames — would leak).
type vaultItemStorage struct {
	store  *Storage
	bucket *bw.Bucket[vaultItemRow]
	scope  scope
}

func (s *Storage) vaultItemsAt(sc scope) *vaultItemStorage {
	return &vaultItemStorage{store: s, bucket: s.vaultItems, scope: sc}
}

func (s *Storage) VaultItems() service.VaultItemStorage {
	return s.vaultItemsAt(s.dbScope())
}

func (t *txStorage) VaultItems() service.VaultItemStorage {
	return t.base.vaultItemsAt(t.scope)
}

func (s *vaultItemStorage) Create(ctx context.Context, item *service.VaultItem) error {
	if item == nil || item.ID == "" || item.UserID == "" {
		return service.ErrBadRequest
	}
	return bucketInsertNew(ctx, s.scope, s.bucket, vaultItemRowFromService(item))
}

func (s *vaultItemStorage) Get(ctx context.Context, userID, itemID string) (*service.VaultItem, error) {
	if userID == "" || itemID == "" {
		return nil, service.ErrNotFound
	}
	row, err := bucketGet(ctx, s.scope, s.bucket, itemID)
	if err != nil {
		return nil, err
	}
	if row.UserID != userID {
		// Mask cross-user access as a plain "not found" so an
		// attacker can't distinguish "item exists but isn't mine"
		// from "no such item".
		return nil, service.ErrNotFound
	}
	return row.toService(), nil
}

// List returns the items belonging to userID that match the filter.
// We narrow with bw on user_id (always) and type (when set) then run
// the rest of the predicate (favorite/archived/trash) in memory. The
// expected per-user item count is small enough (<10k) that the
// in-memory pass is fine.
//
// Title/tag/hostname-based search is intentionally absent here:
// those values are encrypted at rest, so the server cannot evaluate
// a substring or equality predicate against them. The SPA decrypts
// and runs the free-text + tag filter client-side after the listing
// returns.
func (s *vaultItemStorage) List(ctx context.Context, userID string, filter service.VaultItemFilter) ([]service.VaultItem, error) {
	if userID == "" {
		return nil, service.ErrBadRequest
	}

	q := query.New().AddWhere(query.NewExpressionCmp(query.OperatorEq, "user_id", userID))
	if filter.Type != "" {
		q = q.AddWhere(query.NewExpressionCmp(query.OperatorEq, "type", string(filter.Type)))
	}

	rows, err := bucketFind(ctx, s.scope, s.bucket, q)
	if err != nil {
		return nil, err
	}

	out := make([]service.VaultItem, 0, len(rows))
	for _, r := range rows {
		// Trash vs active filter: by default the trash is hidden;
		// TrashOnly flips the predicate.
		inTrash := r.DeletedAt != nil
		switch {
		case filter.TrashOnly && !inTrash:
			continue
		case !filter.TrashOnly && inTrash:
			continue
		}

		// Archived filter (only relevant when not in trash).
		if !filter.TrashOnly {
			switch {
			case filter.ArchivedOnly && !r.Archived:
				continue
			case !filter.ArchivedOnly && !filter.IncludeArchived && r.Archived:
				continue
			}
		}

		if filter.FavoriteOnly && !r.Favorite {
			continue
		}

		out = append(out, *r.toService())
	}
	return out, nil
}

func (s *vaultItemStorage) Update(ctx context.Context, item *service.VaultItem) error {
	if item == nil || item.ID == "" || item.UserID == "" {
		return service.ErrBadRequest
	}
	// Verify ownership before writing. Update on a foreign id would
	// silently re-key the row to the caller's user_id otherwise.
	existing, err := bucketGet(ctx, s.scope, s.bucket, item.ID)
	if err != nil {
		return err
	}
	if existing.UserID != item.UserID {
		return service.ErrNotFound
	}
	return bucketUpdate(ctx, s.scope, s.bucket, vaultItemRowFromService(item))
}

func (s *vaultItemStorage) Delete(ctx context.Context, userID, itemID string) error {
	if userID == "" || itemID == "" {
		return service.ErrBadRequest
	}
	existing, err := bucketGet(ctx, s.scope, s.bucket, itemID)
	if err != nil {
		return err
	}
	if existing.UserID != userID {
		return service.ErrNotFound
	}
	if err := bucketDelete(ctx, s.scope, s.bucket, itemID); err != nil {
		return err
	}
	// Cascade item version history; the parent service may also call
	// the version store directly inside a tx, but covering it here
	// makes Delete safe to call outside the service layer too.
	return s.store.vaultItemVersionsAt(s.scope).DeleteAllByItem(ctx, itemID)
}

// DeleteAllByUser drops every item belonging to a user. Used by the
// user-delete cascade and by the Reset path on the service layer.
// Version history is dropped by a parallel call on vaultItemVersions —
// not from here, to keep the cascade composition explicit.
func (s *vaultItemStorage) DeleteAllByUser(ctx context.Context, userID string) error {
	q := query.New().AddWhere(query.NewExpressionCmp(query.OperatorEq, "user_id", userID))
	rows, err := bucketFind(ctx, s.scope, s.bucket, q)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := bucketDelete(ctx, s.scope, s.bucket, r.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *vaultItemStorage) Count(ctx context.Context, userID string) (int64, error) {
	q := query.New().AddWhere(query.NewExpressionCmp(query.OperatorEq, "user_id", userID))
	n, err := bucketCount(ctx, s.scope, s.bucket, q)
	if err != nil {
		return 0, err
	}
	return int64(n), nil
}
