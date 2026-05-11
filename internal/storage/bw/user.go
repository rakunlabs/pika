package bw

import (
	"context"
	"errors"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/query"
)

// userStorage implements service.UserStorage. The bucket primary key
// is the user ID. Username carries the `unique` flag so duplicate
// inserts return ErrConflict the same way the SQLite layer's UNIQUE
// constraint did (translateErr maps bw.ErrConflict → ErrConflict).
//
// The user_permissions junction table from the SQL schema is collapsed
// onto the userRow here — see Grants []userGrant. permissionStorage
// reads/writes that slice through this storage's getRow/updateRow.
type userStorage struct {
	store  *Storage
	bucket *bw.Bucket[userRow]
	scope  scope
}

func (s *Storage) usersAt(sc scope) *userStorage {
	return &userStorage{store: s, bucket: s.users, scope: sc}
}

func (s *Storage) Users() service.UserStorage   { return s.usersAt(s.dbScope()) }
func (t *txStorage) Users() service.UserStorage { return t.base.usersAt(t.scope) }

func (s *userStorage) Create(ctx context.Context, user *service.User) error {
	// InsertNew flavors fail loudly on duplicate IDs — Create is only
	// called for fresh user records.
	return bucketInsertNew(ctx, s.scope, s.bucket, userRowFromService(user, nil))
}

// getRow returns the underlying userRow (including Grants), bypassing
// the toService() projection. permissionStorage uses this to read +
// write grants atomically with the user record.
func (s *userStorage) getRow(ctx context.Context, id string) (*userRow, error) {
	return bucketGet(ctx, s.scope, s.bucket, id)
}

func (s *userStorage) Get(ctx context.Context, id string) (*service.User, error) {
	row, err := s.getRow(ctx, id)
	if err != nil {
		return nil, err
	}
	return row.toService(), nil
}

// findByField issues an indexed query on a single string field. The
// "username" lookup hits the unique index and the "email" lookup hits
// the secondary index; both return at most one row.
//
// We construct the Query directly instead of going through query.Parse
// — the value can contain characters that are special in query strings
// (=, &, +, %), and parsing "field=value" would either escape them
// incorrectly or, worse, split the value at the first reserved char.
// Building the expression by hand is also cheaper.
func (s *userStorage) findByField(ctx context.Context, field, value string) (*userRow, error) {
	if value == "" {
		return nil, service.ErrNotFound
	}
	q := query.New().AddWhere(query.NewExpressionCmp(query.OperatorEq, field, value))
	rows, err := bucketFind(ctx, s.scope, s.bucket, q)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, service.ErrNotFound
	}
	return rows[0], nil
}

func (s *userStorage) GetByUsername(ctx context.Context, username string) (*service.User, error) {
	row, err := s.findByField(ctx, "username", username)
	if err != nil {
		return nil, err
	}
	return row.toService(), nil
}

func (s *userStorage) GetByEmail(ctx context.Context, email string) (*service.User, error) {
	row, err := s.findByField(ctx, "email", email)
	if err != nil {
		return nil, err
	}
	return row.toService(), nil
}

func (s *userStorage) List(ctx context.Context, q *query.Query) ([]service.User, int64, error) {
	rows, err := bucketFind(ctx, s.scope, s.bucket, q)
	if err != nil {
		return nil, 0, err
	}
	out := make([]service.User, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r.toService())
	}

	var countQ *query.Query
	if q != nil {
		c := *q
		c.Offset = nil
		c.Limit = nil
		c.Sort = nil
		countQ = &c
	}
	total, err := bucketCount(ctx, s.scope, s.bucket, countQ)
	if err != nil {
		return nil, 0, err
	}
	return out, int64(total), nil
}

func (s *userStorage) Update(ctx context.Context, user *service.User) error {
	// Preserve Grants — the service-layer User struct has no grants
	// field on it, so a naive Update would wipe a user's permissions.
	existing, err := s.getRow(ctx, user.ID)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			return err
		}
		return err
	}
	return bucketUpdate(ctx, s.scope, s.bucket, userRowFromService(user, existing.Grants))
}

// listRowsAll returns every user row including its Grants slice. Used
// by permissionStorage.Delete to strip a deleted permission from every
// user atomically.
func (s *userStorage) listRowsAll(ctx context.Context) ([]*userRow, error) {
	return bucketFind(ctx, s.scope, s.bucket, nil)
}

// updateRow replaces the full row (including Grants). permissionStorage
// uses this to persist grant changes atomically with the user record.
func (s *userStorage) updateRow(ctx context.Context, row *userRow) error {
	return bucketUpdate(ctx, s.scope, s.bucket, row)
}

func (s *userStorage) Delete(ctx context.Context, id string) error {
	// Cascade: delete linked identities and any sessions for this user.
	// The SQL backend used FK ON DELETE CASCADE; here we mirror it with
	// explicit deletes inside whatever scope we already have.
	if err := s.cascadeDelete(ctx, id); err != nil {
		return err
	}
	return bucketDelete(ctx, s.scope, s.bucket, id)
}

func (s *userStorage) cascadeDelete(ctx context.Context, userID string) error {
	idents, err := s.store.userIdentitiesAt(s.scope).ListByUserID(ctx, userID)
	if err != nil {
		return err
	}
	for _, ident := range idents {
		if err := s.store.userIdentitiesAt(s.scope).Delete(ctx, ident.ID); err != nil {
			return err
		}
	}
	return s.store.sessionsAt(s.scope).DeleteByUserID(ctx, userID)
}

func (s *userStorage) Count(ctx context.Context) (int64, error) {
	n, err := bucketCount(ctx, s.scope, s.bucket, nil)
	if err != nil {
		return 0, err
	}
	return int64(n), nil
}
