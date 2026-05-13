package bw

import (
	"context"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/pika/internal/service"
)

// userTOTPStorage implements service.UserTOTPStorage.
//
// TOTP is one-to-one with users — keyed entirely by user_id, no
// auxiliary lookups needed (unlike passkey, which is keyed by a
// synthetic id and needs FindByCredentialID at login time).
//
// All writes funnel through the scope helpers so they participate in
// any enclosing transaction — most importantly the user-delete cascade
// (user.go:cascadeDelete), which must drop the user's TOTP row inside
// the same tx as the user delete so a partial failure can't leave a
// dangling TOTP row that gates login for a missing user.
type userTOTPStorage struct {
	store  *Storage
	bucket *bw.Bucket[userTOTPRow]
	scope  scope
}

func (s *Storage) userTOTPAt(sc scope) *userTOTPStorage {
	return &userTOTPStorage{store: s, bucket: s.userTOTP, scope: sc}
}

func (s *Storage) UserTOTPs() service.UserTOTPStorage {
	return s.userTOTPAt(s.dbScope())
}

func (t *txStorage) UserTOTPs() service.UserTOTPStorage {
	return t.base.userTOTPAt(t.scope)
}

func (s *userTOTPStorage) Get(ctx context.Context, userID string) (*service.UserTOTP, error) {
	row, err := bucketGet(ctx, s.scope, s.bucket, userID)
	if err != nil {
		return nil, err
	}
	return row.toService(), nil
}

// Set is upsert: the service layer treats begin-enrollment, finish-
// enrollment, regenerate-recovery-codes, and update-last-used as
// successive writes against the same user_id row. No need for a
// distinct Create / Update split — bw's bucketInsert overwrites on
// existing key, which matches the desired semantics.
func (s *userTOTPStorage) Set(ctx context.Context, t *service.UserTOTP) error {
	if t == nil || t.UserID == "" {
		return service.ErrBadRequest
	}
	return bucketInsert(ctx, s.scope, s.bucket, userTOTPRowFromService(t))
}

func (s *userTOTPStorage) Delete(ctx context.Context, userID string) error {
	return bucketDelete(ctx, s.scope, s.bucket, userID)
}
