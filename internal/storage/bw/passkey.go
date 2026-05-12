package bw

import (
	"context"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/query"
)

// passkeyStorage implements service.PasskeyStorage.
//
// Two lookup paths drive the design:
//
//   - The security page reads ListByUserID; user_id is indexed.
//   - The login ceremony reads FindByCredentialID with the raw bytes
//     the authenticator returned in the rawId field; credential_id is
//     marked unique so the lookup is an O(1) index hit and a buggy
//     authenticator that reuses an ID can't create two rows.
//
// All writes are routed through the scope helpers so they participate
// in an in-flight transaction when one exists (e.g. user delete cascading
// to passkey rows).
type passkeyStorage struct {
	store  *Storage
	bucket *bw.Bucket[passkeyCredentialRow]
	scope  scope
}

func (s *Storage) passkeysAt(sc scope) *passkeyStorage {
	return &passkeyStorage{store: s, bucket: s.passkeys, scope: sc}
}

func (s *Storage) Passkeys() service.PasskeyStorage {
	return s.passkeysAt(s.dbScope())
}

func (t *txStorage) Passkeys() service.PasskeyStorage {
	return t.base.passkeysAt(t.scope)
}

func (s *passkeyStorage) Create(ctx context.Context, c *service.PasskeyCredential) error {
	// InsertNew rejects duplicate IDs with ErrConflict; the unique
	// constraint on credential_id catches authenticator-side dupes.
	return bucketInsertNew(ctx, s.scope, s.bucket, passkeyCredentialRowFromService(c))
}

func (s *passkeyStorage) Get(ctx context.Context, id string) (*service.PasskeyCredential, error) {
	row, err := bucketGet(ctx, s.scope, s.bucket, id)
	if err != nil {
		return nil, err
	}
	return row.toService(), nil
}

// FindByCredentialID resolves the assertion's rawId back to the
// owning row. Returns ErrNotFound when no row matches — the strategy
// surfaces that as a generic "passkey not recognized" so an attacker
// can't enumerate enrolled credentials by timing or by error message.
func (s *passkeyStorage) FindByCredentialID(ctx context.Context, credentialID []byte) (*service.PasskeyCredential, error) {
	if len(credentialID) == 0 {
		return nil, service.ErrNotFound
	}
	q := query.New().AddWhere(query.NewExpressionCmp(query.OperatorEq, "credential_id", credentialID))
	rows, err := bucketFind(ctx, s.scope, s.bucket, q)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, service.ErrNotFound
	}
	return rows[0].toService(), nil
}

func (s *passkeyStorage) ListByUserID(ctx context.Context, userID string) ([]service.PasskeyCredential, error) {
	q := query.New().AddWhere(query.NewExpressionCmp(query.OperatorEq, "user_id", userID))
	rows, err := bucketFind(ctx, s.scope, s.bucket, q)
	if err != nil {
		return nil, err
	}
	out := make([]service.PasskeyCredential, len(rows))
	for i, r := range rows {
		out[i] = *r.toService()
	}
	return out, nil
}

func (s *passkeyStorage) Update(ctx context.Context, c *service.PasskeyCredential) error {
	return bucketUpdate(ctx, s.scope, s.bucket, passkeyCredentialRowFromService(c))
}

func (s *passkeyStorage) Delete(ctx context.Context, id string) error {
	return bucketDelete(ctx, s.scope, s.bucket, id)
}

// DeleteByUserID removes every credential belonging to a user. Called
// from the user-delete cascade so a deleted account doesn't leave
// dangling auth material. The bulk-list-then-delete shape matches
// userIdentityStorage's cascade and stays in the surrounding scope
// (so the cascade runs inside the same tx as the user delete).
func (s *passkeyStorage) DeleteByUserID(ctx context.Context, userID string) error {
	rows, err := s.ListByUserID(ctx, userID)
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
