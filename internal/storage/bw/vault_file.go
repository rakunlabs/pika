package bw

import (
	"context"
	"time"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/query"
)

// vaultFileRow is one node (file or folder) in a user's vault file tree.
// user_id is indexed for the per-user listing; parent_id is indexed so
// child lookups during create/move don't scan the whole user partition.
type vaultFileRow struct {
	ID          string    `bw:"id,pk"`
	UserID      string    `bw:"user_id,index"`
	ParentID    string    `bw:"parent_id,index"`
	Name        string    `bw:"name"`
	IsDir       bool      `bw:"is_dir"`
	Size        int64     `bw:"size"`
	ContentType string    `bw:"content_type"`
	StorageKey  string    `bw:"storage_key"`
	Backend     string    `bw:"backend"`
	CreatedAt   time.Time `bw:"created_at"`
	UpdatedAt   time.Time `bw:"updated_at"`
}

func (r *vaultFileRow) toService() *service.VaultFile {
	return &service.VaultFile{
		ID:          r.ID,
		UserID:      r.UserID,
		ParentID:    r.ParentID,
		Name:        r.Name,
		IsDir:       r.IsDir,
		Size:        r.Size,
		ContentType: r.ContentType,
		StorageKey:  r.StorageKey,
		Backend:     r.Backend,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

func vaultFileRowFromService(f *service.VaultFile) *vaultFileRow {
	return &vaultFileRow{
		ID:          f.ID,
		UserID:      f.UserID,
		ParentID:    f.ParentID,
		Name:        f.Name,
		IsDir:       f.IsDir,
		Size:        f.Size,
		ContentType: f.ContentType,
		StorageKey:  f.StorageKey,
		Backend:     f.Backend,
		CreatedAt:   f.CreatedAt,
		UpdatedAt:   f.UpdatedAt,
	}
}

// vaultFileStorage implements service.VaultFileStorage. Every read
// verifies ownership so a guessed id from another user reads as
// "not found".
type vaultFileStorage struct {
	store  *Storage
	bucket *bw.Bucket[vaultFileRow]
	scope  scope
}

func (s *Storage) vaultFilesAt(sc scope) *vaultFileStorage {
	return &vaultFileStorage{store: s, bucket: s.vaultFiles, scope: sc}
}

func (s *Storage) VaultFiles() service.VaultFileStorage {
	return s.vaultFilesAt(s.dbScope())
}

func (t *txStorage) VaultFiles() service.VaultFileStorage {
	return t.base.vaultFilesAt(t.scope)
}

func (s *vaultFileStorage) Create(ctx context.Context, f *service.VaultFile) error {
	if f == nil || f.ID == "" || f.UserID == "" {
		return service.ErrBadRequest
	}
	return bucketInsertNew(ctx, s.scope, s.bucket, vaultFileRowFromService(f))
}

func (s *vaultFileStorage) Get(ctx context.Context, userID, id string) (*service.VaultFile, error) {
	if userID == "" || id == "" {
		return nil, service.ErrNotFound
	}
	row, err := bucketGet(ctx, s.scope, s.bucket, id)
	if err != nil {
		return nil, err
	}
	if row.UserID != userID {
		return nil, service.ErrNotFound
	}
	return row.toService(), nil
}

func (s *vaultFileStorage) find(ctx context.Context, q *query.Query) ([]service.VaultFile, error) {
	rows, err := bucketFind(ctx, s.scope, s.bucket, q)
	if err != nil {
		return nil, err
	}
	out := make([]service.VaultFile, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r.toService())
	}
	return out, nil
}

func (s *vaultFileStorage) ListByUser(ctx context.Context, userID string) ([]service.VaultFile, error) {
	if userID == "" {
		return nil, service.ErrBadRequest
	}
	return s.find(ctx, query.New().AddWhere(query.NewExpressionCmp(query.OperatorEq, "user_id", userID)))
}

func (s *vaultFileStorage) ListChildren(ctx context.Context, userID, parentID string) ([]service.VaultFile, error) {
	if userID == "" {
		return nil, service.ErrBadRequest
	}
	q := query.New().AddWhere(
		query.NewExpressionCmp(query.OperatorEq, "user_id", userID),
		query.NewExpressionCmp(query.OperatorEq, "parent_id", parentID),
	)
	return s.find(ctx, q)
}

func (s *vaultFileStorage) Update(ctx context.Context, f *service.VaultFile) error {
	if f == nil || f.ID == "" || f.UserID == "" {
		return service.ErrBadRequest
	}
	if _, err := s.Get(ctx, f.UserID, f.ID); err != nil {
		return err
	}
	return bucketUpdate(ctx, s.scope, s.bucket, vaultFileRowFromService(f))
}

func (s *vaultFileStorage) Delete(ctx context.Context, userID, id string) error {
	if _, err := s.Get(ctx, userID, id); err != nil {
		return err
	}
	return bucketDelete(ctx, s.scope, s.bucket, id)
}

func (s *vaultFileStorage) DeleteAllByUser(ctx context.Context, userID string) error {
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
