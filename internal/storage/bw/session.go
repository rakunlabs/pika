package bw

import (
	"context"
	"time"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/query"
)

// sessionStorage implements service.SessionStorage. The expiry- and
// user-id-indexed fields on sessionRow let DeleteByUserID and
// CountByUserID reach the bw query planner's index seek.
type sessionStorage struct {
	store  *Storage
	bucket *bw.Bucket[sessionRow]
	scope  scope
}

func (s *Storage) sessionsAt(sc scope) *sessionStorage {
	return &sessionStorage{store: s, bucket: s.sessions, scope: sc}
}

func (s *Storage) Sessions() service.SessionStorage   { return s.sessionsAt(s.dbScope()) }
func (t *txStorage) Sessions() service.SessionStorage { return t.base.sessionsAt(t.scope) }

func (s *sessionStorage) Create(ctx context.Context, session *service.Session) error {
	return bucketInsert(ctx, s.scope, s.bucket, sessionRowFromService(session))
}

func (s *sessionStorage) Get(ctx context.Context, id string) (*service.Session, error) {
	row, err := bucketGet(ctx, s.scope, s.bucket, id)
	if err != nil {
		return nil, err
	}
	return row.toService(), nil
}

func (s *sessionStorage) Delete(ctx context.Context, id string) error {
	return bucketDelete(ctx, s.scope, s.bucket, id)
}

func (s *sessionStorage) DeleteByUserID(ctx context.Context, userID string) error {
	rows, err := s.findByUserID(ctx, userID)
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

// DeleteExpired walks the bucket once and removes any session whose
// expires_at is in the past. A typed range query on expires_at is
// possible via bw's index planner but the typed time comparison
// translates oddly through the URL-style query syntax; a full walk is
// adequate because pika's session bucket rarely exceeds a few hundred
// rows.
func (s *sessionStorage) DeleteExpired(ctx context.Context) error {
	now := time.Now()
	var ids []string
	if err := bucketWalk(ctx, s.scope, s.bucket, nil, func(r *sessionRow) error {
		if !r.ExpiresAt.IsZero() && r.ExpiresAt.Before(now) {
			ids = append(ids, r.ID)
		}
		return nil
	}); err != nil {
		return err
	}
	for _, id := range ids {
		if err := bucketDelete(ctx, s.scope, s.bucket, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *sessionStorage) CountByUserID(ctx context.Context, userID string) (int64, error) {
	rows, err := s.findByUserID(ctx, userID)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	var n int64
	for _, r := range rows {
		if r.ExpiresAt.After(now) {
			n++
		}
	}
	return n, nil
}

// findByUserID uses the bw query engine's user_id index. The typed
// Find/FindTx pair handles both standalone and in-tx callers; we no
// longer need a manual full-bucket fallback for the in-tx case
// because FindTx observes pending writes the same way mutations do.
func (s *sessionStorage) findByUserID(ctx context.Context, userID string) ([]*sessionRow, error) {
	q, err := query.Parse("user_id=" + userID)
	if err != nil {
		return nil, err
	}
	return bucketFind(ctx, s.scope, s.bucket, q)
}
