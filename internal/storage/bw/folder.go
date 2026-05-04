package bw

import (
	"context"
	"strings"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/pika/internal/service"
)

// folderStorage implements service.FolderStorage on top of the folders
// bucket. Path is the bucket primary key. Prefix-deletes scan the
// bucket once and remove any path that equals the prefix or starts
// with "<prefix>/" — same semantics as the SQL `path LIKE prefix/%`
// clause used by the previous SQLite backend.
type folderStorage struct {
	store  *Storage
	bucket *bw.Bucket[folderRow]
	scope  scope
}

func (s *Storage) foldersAt(sc scope) *folderStorage {
	return &folderStorage{store: s, bucket: s.folders, scope: sc}
}

func (s *Storage) Folders() service.FolderStorage   { return s.foldersAt(s.dbScope()) }
func (t *txStorage) Folders() service.FolderStorage { return t.base.foldersAt(t.scope) }

func (s *folderStorage) Get(ctx context.Context, path string) (*service.Folder, error) {
	row, err := bucketGet(ctx, s.scope, s.bucket, path)
	if err != nil {
		return nil, err
	}
	return row.toService(), nil
}

func (s *folderStorage) Set(ctx context.Context, path string, folder *service.Folder) error {
	return bucketInsert(ctx, s.scope, s.bucket, folderRowFromService(path, folder))
}

func (s *folderStorage) Delete(ctx context.Context, path string) error {
	return bucketDelete(ctx, s.scope, s.bucket, path)
}

func (s *folderStorage) DeletePrefix(ctx context.Context, prefix string) error {
	// Collect matching keys first, then delete in a second pass —
	// keeps the iterator from being invalidated by mid-scan deletes.
	var keys []string
	if err := bucketWalk(ctx, s.scope, s.bucket, nil, func(r *folderRow) error {
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

func (s *folderStorage) List(ctx context.Context) ([]service.FolderEntry, error) {
	var entries []service.FolderEntry
	err := bucketWalk(ctx, s.scope, s.bucket, nil, func(r *folderRow) error {
		entries = append(entries, service.FolderEntry{
			Path:   r.Path,
			Folder: r.toService(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *folderStorage) DeleteAll(ctx context.Context) error {
	var keys []string
	if err := bucketWalk(ctx, s.scope, s.bucket, nil, func(r *folderRow) error {
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
