package bw

import (
	"context"
	"errors"
	"time"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/pika/internal/service"
)

// passkeyChallengeStorage implements service.PasskeyChallengeStorage.
// It is the cluster-aware backend for in-flight WebAuthn ceremony
// state: writes go through the leader (via the cluster middleware)
// and replicate to every follower before the originating request
// returns, so a finish call landing on a different instance than the
// matching begin still sees the row.
type passkeyChallengeStorage struct {
	store  *Storage
	bucket *bw.Bucket[passkeyChallengeRow]
	scope  scope
}

func (s *Storage) passkeyChallengesAt(sc scope) *passkeyChallengeStorage {
	return &passkeyChallengeStorage{store: s, bucket: s.passkeyChallenges, scope: sc}
}

func (s *Storage) PasskeyChallenges() service.PasskeyChallengeStorage {
	return s.passkeyChallengesAt(s.dbScope())
}

func (t *txStorage) PasskeyChallenges() service.PasskeyChallengeStorage {
	return t.base.passkeyChallengesAt(t.scope)
}

// Save writes a challenge row. The caller is responsible for
// generating the id (we don't auto-generate so the strategy can hand
// the id back to the SPA verbatim). Existing rows under the same id
// are overwritten — the WebAuthn ids are 32-byte random strings so
// collisions are not a real concern, but bw's upsert semantics make
// this safe even under retry.
func (s *passkeyChallengeStorage) Save(ctx context.Context, c *service.PasskeyChallenge) error {
	if c == nil || c.ID == "" {
		return service.ErrBadRequest
	}
	// Set CreatedAt the first time the row is saved. We could let the
	// caller do this but centralizing it keeps every persisted row
	// stamped with the bw-side clock.
	if c.CreatedAt.IsZero() {
		c.CreatedAt = nowUTC()
	}
	// bucketInsert upserts (writes regardless of whether the key
	// exists) — different from bucketInsertNew which returns
	// ErrConflict on duplicate. Upsert is what we want: a retry that
	// hits an existing row should overwrite it, not 409.
	return bucketInsert(ctx, s.scope, s.bucket, passkeyChallengeRowFromService(c))
}

// Get returns the challenge row by id. Expired rows are still
// returned — the strategy is responsible for checking ExpiresAt and
// rejecting on its own — because the verification path treats expiry
// as an authentication failure rather than a not-found.
func (s *passkeyChallengeStorage) Get(ctx context.Context, id string) (*service.PasskeyChallenge, error) {
	row, err := bucketGet(ctx, s.scope, s.bucket, id)
	if err != nil {
		return nil, err
	}
	return row.toService(), nil
}

// Delete removes a challenge row. Idempotent: deleting a row that
// doesn't exist returns nil so the ceremony's one-shot delete on
// finish doesn't fail when the row was already cleaned up by the
// expiry sweep.
func (s *passkeyChallengeStorage) Delete(ctx context.Context, id string) error {
	if err := bucketDelete(ctx, s.scope, s.bucket, id); err != nil {
		// bw returns ErrNotFound when the row is gone; mask it here so
		// callers don't have to wrap. Any other error bubbles up.
		if errors.Is(err, service.ErrNotFound) {
			return nil
		}
		return err
	}
	return nil
}

// DeleteExpired walks the bucket once and removes every row whose
// ExpiresAt is in the past. The challenge bucket stays small under
// normal load (≤ ChallengeTTL × concurrent_logins ≈ a few hundred
// rows in the worst case), so a full walk per sweep is acceptable.
// Returns the number of rows removed for observability.
func (s *passkeyChallengeStorage) DeleteExpired(ctx context.Context) (int, error) {
	now := time.Now()
	var ids []string
	err := bucketWalk(ctx, s.scope, s.bucket, nil, func(r *passkeyChallengeRow) error {
		if !r.ExpiresAt.IsZero() && r.ExpiresAt.Before(now) {
			ids = append(ids, r.ID)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := bucketDelete(ctx, s.scope, s.bucket, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}
