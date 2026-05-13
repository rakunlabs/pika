package bw

import (
	"context"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/pika/internal/service"
)

// vaultAccountStorage implements service.VaultAccountStorage.
//
// One-to-one with users (PK = user_id) — mirrors userTOTPStorage in
// every respect. The row is sensitive (carries the wrapped vault key
// and the Argon2id salt) but contains no plaintext secrets; even with
// a DB dump an attacker still has to brute-force the master password
// to make use of it.
//
// All writes funnel through the scope helpers so they participate in
// any enclosing transaction — most importantly the user-delete cascade
// (user.go:cascadeDelete), which must drop the vault account inside
// the same tx as the user delete to avoid leaving an orphan row that
// would gate a "user already initialized" error for a freshly-recycled
// user_id.
type vaultAccountStorage struct {
	store  *Storage
	bucket *bw.Bucket[vaultAccountRow]
	scope  scope
}

func (s *Storage) vaultAccountsAt(sc scope) *vaultAccountStorage {
	return &vaultAccountStorage{store: s, bucket: s.vaultAccounts, scope: sc}
}

func (s *Storage) VaultAccounts() service.VaultAccountStorage {
	return s.vaultAccountsAt(s.dbScope())
}

func (t *txStorage) VaultAccounts() service.VaultAccountStorage {
	return t.base.vaultAccountsAt(t.scope)
}

func (s *vaultAccountStorage) Get(ctx context.Context, userID string) (*service.VaultAccount, error) {
	row, err := bucketGet(ctx, s.scope, s.bucket, userID)
	if err != nil {
		return nil, err
	}
	return row.toService(), nil
}

// Set is upsert: the service layer treats Setup, master-password
// rotation, and session-lock TTL changes as successive writes against
// the same user_id row. Like userTOTPStorage.Set there's no need for a
// distinct Create / Update split.
func (s *vaultAccountStorage) Set(ctx context.Context, a *service.VaultAccount) error {
	if a == nil || a.UserID == "" {
		return service.ErrBadRequest
	}
	return bucketInsert(ctx, s.scope, s.bucket, vaultAccountRowFromService(a))
}

func (s *vaultAccountStorage) Delete(ctx context.Context, userID string) error {
	return bucketDelete(ctx, s.scope, s.bucket, userID)
}
