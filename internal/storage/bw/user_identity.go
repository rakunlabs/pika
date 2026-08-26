package bw

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/query"
)

// userIdentityStorage implements service.UserIdentityStorage on top
// of the user_identities bucket. (provider, subject) is enforced
// unique through a composite group on the row schema.
type userIdentityStorage struct {
	store  *Storage
	bucket *bw.Bucket[userIdentityRow]
	scope  scope
}

func (s *Storage) userIdentitiesAt(sc scope) *userIdentityStorage {
	return &userIdentityStorage{store: s, bucket: s.userIdentities, scope: sc}
}

func (s *Storage) UserIdentities() service.UserIdentityStorage {
	return s.userIdentitiesAt(s.dbScope())
}

func (t *txStorage) UserIdentities() service.UserIdentityStorage {
	return t.base.userIdentitiesAt(t.scope)
}

func generateIdentityID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Upsert creates or refreshes the (provider, subject) link. UserID
// is never overwritten on existing rows — re-linking on re-login
// would silently switch identities, which is a security bug.
func (s *userIdentityStorage) Upsert(ctx context.Context, id *service.UserIdentity) (*service.UserIdentity, error) {
	now := time.Now().UTC()

	existing, err := s.findByProviderSubject(ctx, id.Provider, id.Subject)
	if err != nil && !errors.Is(err, service.ErrNotFound) {
		return nil, err
	}

	if existing != nil {
		existing.Email = id.Email
		existing.DisplayName = id.DisplayName
		existing.LastLoginAt = &now
		if err := bucketInsert(ctx, s.scope, s.bucket, existing); err != nil {
			return nil, err
		}
		return existing.toService(), nil
	}

	if id.ID == "" {
		newID, err := generateIdentityID()
		if err != nil {
			return nil, err
		}
		id.ID = newID
	}
	if id.CreatedAt.IsZero() {
		id.CreatedAt = now
	}
	id.LastLoginAt = &now

	row := userIdentityRowFromService(id)
	if err := bucketInsert(ctx, s.scope, s.bucket, row); err != nil {
		return nil, err
	}
	return row.toService(), nil
}

// findByProviderSubject uses the composite unique index by querying
// both fields. The bw planner picks the composite index when every
// participating field appears as an equality term, which collapses
// the lookup to a single seek.
func (s *userIdentityStorage) findByProviderSubject(ctx context.Context, provider, subject string) (*userIdentityRow, error) {
	q, err := query.Parse("provider=" + provider + "&subject=" + subject)
	if err != nil {
		return nil, err
	}
	rows, err := bucketFind(ctx, s.scope, s.bucket, q)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, service.ErrNotFound
	}
	return rows[0], nil
}

func (s *userIdentityStorage) FindByProviderSubject(ctx context.Context, provider, subject string) (*service.UserIdentity, error) {
	row, err := s.findByProviderSubject(ctx, provider, subject)
	if err != nil {
		return nil, err
	}
	return row.toService(), nil
}

func (s *userIdentityStorage) ListByUserID(ctx context.Context, userID string) ([]service.UserIdentity, error) {
	rows, err := s.findByField(ctx, "user_id", userID)
	if err != nil {
		return nil, err
	}
	out := make([]service.UserIdentity, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r.toService())
	}
	return out, nil
}

func (s *userIdentityStorage) findByField(ctx context.Context, field, value string) ([]*userIdentityRow, error) {
	q, err := query.Parse(field + "=" + value + "&_sort=created_at")
	if err != nil {
		return nil, err
	}
	return bucketFind(ctx, s.scope, s.bucket, q)
}

func (s *userIdentityStorage) Delete(ctx context.Context, id string) error {
	return bucketDelete(ctx, s.scope, s.bucket, id)
}

func (s *userIdentityStorage) DeleteByUserID(ctx context.Context, userID string) error {
	rows, err := s.findByField(ctx, "user_id", userID)
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
