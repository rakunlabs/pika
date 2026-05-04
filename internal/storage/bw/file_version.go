package bw

import (
	"context"
	"strings"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/pika/internal/service"
)

// fileVersionStorage implements service.FileVersionStorage. Each row
// holds the full FileVersions slice for a given path; updates rewrite
// the slice in one go (cheap because version metadata is small).
type fileVersionStorage struct {
	store  *Storage
	bucket *bw.Bucket[fileVersionRow]
	scope  scope
}

func (s *Storage) fileVersionsAt(sc scope) *fileVersionStorage {
	return &fileVersionStorage{store: s, bucket: s.fileVersions, scope: sc}
}

func (s *Storage) FileVersions() service.FileVersionStorage   { return s.fileVersionsAt(s.dbScope()) }
func (t *txStorage) FileVersions() service.FileVersionStorage { return t.base.fileVersionsAt(t.scope) }

func (s *fileVersionStorage) Get(ctx context.Context, path string) (service.FileVersions, error) {
	row, err := bucketGet(ctx, s.scope, s.bucket, path)
	if err != nil {
		return nil, err
	}
	return row.Versions, nil
}

func (s *fileVersionStorage) Set(ctx context.Context, path string, versions service.FileVersions) error {
	return bucketInsert(ctx, s.scope, s.bucket, &fileVersionRow{Path: path, Versions: versions})
}

func (s *fileVersionStorage) Delete(ctx context.Context, path string) error {
	return bucketDelete(ctx, s.scope, s.bucket, path)
}

func (s *fileVersionStorage) DeletePrefix(ctx context.Context, prefix string) error {
	var keys []string
	if err := bucketWalk(ctx, s.scope, s.bucket, nil, func(r *fileVersionRow) error {
		if r.Path == prefix || strings.HasPrefix(r.Path, prefix+"/") {
			keys = append(keys, r.Path)
		}
		return nil
	}); err != nil {
		return err
	}
	for _, k := range keys {
		if err := bucketDelete(ctx, s.scope, s.bucket, k); err != nil {
			return err
		}
	}
	return nil
}

func (s *fileVersionStorage) List(ctx context.Context) ([]service.FileVersionEntry, error) {
	var entries []service.FileVersionEntry
	err := bucketWalk(ctx, s.scope, s.bucket, nil, func(r *fileVersionRow) error {
		entries = append(entries, service.FileVersionEntry{
			Path:     r.Path,
			Versions: r.Versions,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *fileVersionStorage) DeleteAll(ctx context.Context) error {
	var keys []string
	if err := bucketWalk(ctx, s.scope, s.bucket, nil, func(r *fileVersionRow) error {
		keys = append(keys, r.Path)
		return nil
	}); err != nil {
		return err
	}
	for _, k := range keys {
		if err := bucketDelete(ctx, s.scope, s.bucket, k); err != nil {
			return err
		}
	}
	return nil
}
