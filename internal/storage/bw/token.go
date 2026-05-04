package bw

import (
	"context"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/query"
)

// tokenStorage implements service.TokenStorage. HashedKey is unique
// on the row schema, so FindByHash routes through bw's typed query
// engine and the unique-index seek when called outside a tx; in-tx
// callers go through FindTx for the same query and observe pending
// writes.
type tokenStorage struct {
	store  *Storage
	bucket *bw.Bucket[tokenRow]
	scope  scope
}

func (s *Storage) tokensAt(sc scope) *tokenStorage {
	return &tokenStorage{store: s, bucket: s.tokens, scope: sc}
}

func (s *Storage) Tokens() service.TokenStorage   { return s.tokensAt(s.dbScope()) }
func (t *txStorage) Tokens() service.TokenStorage { return t.base.tokensAt(t.scope) }

func (s *tokenStorage) Create(ctx context.Context, token *service.Token) error {
	return bucketInsert(ctx, s.scope, s.bucket, tokenRowFromService(token))
}

func (s *tokenStorage) Get(ctx context.Context, id string) (*service.Token, error) {
	row, err := bucketGet(ctx, s.scope, s.bucket, id)
	if err != nil {
		return nil, err
	}
	return row.toService(), nil
}

// FindByHash looks up a token by its (unique) HashedKey. bw's planner
// uses the unique index for an O(1) seek; we still get back at most
// one row.
func (s *tokenStorage) FindByHash(ctx context.Context, hashedKey string) (*service.Token, error) {
	q, err := query.Parse("hashed_key=" + hashedKey)
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
	return rows[0].toService(), nil
}

func (s *tokenStorage) List(ctx context.Context, q *query.Query) ([]service.Token, int64, error) {
	rows, err := bucketFind(ctx, s.scope, s.bucket, q)
	if err != nil {
		return nil, 0, err
	}
	out := make([]service.Token, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r.toService())
	}

	// Count without paging — clone q with offset/limit/sort cleared.
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

func (s *tokenStorage) Update(ctx context.Context, token *service.Token) error {
	return bucketUpdate(ctx, s.scope, s.bucket, tokenRowFromService(token))
}

func (s *tokenStorage) Delete(ctx context.Context, id string) error {
	return bucketDelete(ctx, s.scope, s.bucket, id)
}
