package bw

import (
	"context"
	"strings"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/pika/internal/service"
)

// fileStorage implements service.FileStorage. The bucket primary key
// is composite: "<path>\x00<bigendian uint64 version>" — defined in
// fileRowKey. Path is also indexed on the row schema so future
// queries by path could use the index, though current callers iterate
// the whole bucket and filter — pika file counts are small enough
// that the difference is invisible.
//
// Path collisions with the prefix-byte (0x00) are not possible
// because pika's path validation rejects NUL bytes upstream at the
// service layer.
type fileStorage struct {
	store  *Storage
	bucket *bw.Bucket[fileRow]
	scope  scope
}

func (s *Storage) filesAt(sc scope) *fileStorage {
	return &fileStorage{store: s, bucket: s.files, scope: sc}
}

func (s *Storage) Files() service.FileStorage   { return s.filesAt(s.dbScope()) }
func (t *txStorage) Files() service.FileStorage { return t.base.filesAt(t.scope) }

func (s *fileStorage) Get(ctx context.Context, path string, version int64) (*service.File, error) {
	row, err := bucketGet(ctx, s.scope, s.bucket, fileRowKey(path, version))
	if err != nil {
		return nil, err
	}
	return row.toService(), nil
}

func (s *fileStorage) Set(ctx context.Context, path string, version int64, file *service.File) error {
	return bucketInsert(ctx, s.scope, s.bucket, fileRowFromService(path, version, file))
}

func (s *fileStorage) Delete(ctx context.Context, path string, version int64) error {
	return bucketDelete(ctx, s.scope, s.bucket, fileRowKey(path, version))
}

// DeleteAllVersions removes every (path, version) tuple whose path
// matches exactly. We iterate the bucket once and filter on the
// decoded fileRow.Path — same big-O as the previous raw-key approach
// but reads through the typed bucket so it shares one code path with
// every other walk in this package.
func (s *fileStorage) DeleteAllVersions(ctx context.Context, path string) error {
	type pkv struct {
		path    string
		version int64
	}
	var hits []pkv
	if err := bucketWalk(ctx, s.scope, s.bucket, nil, func(r *fileRow) error {
		if r.Path == path {
			hits = append(hits, pkv{r.Path, r.Version})
		}
		return nil
	}); err != nil {
		return err
	}
	for _, h := range hits {
		if err := bucketDelete(ctx, s.scope, s.bucket, fileRowKey(h.path, h.version)); err != nil {
			return err
		}
	}
	return nil
}

// DeletePrefix removes every (path, version) where path equals prefix
// or starts with "<prefix>/" — same shape as the SQL LIKE clause used
// to.
func (s *fileStorage) DeletePrefix(ctx context.Context, prefix string) error {
	type pkv struct {
		path    string
		version int64
	}
	var hits []pkv
	if err := bucketWalk(ctx, s.scope, s.bucket, nil, func(r *fileRow) error {
		if r.Path == prefix || strings.HasPrefix(r.Path, prefix+"/") {
			hits = append(hits, pkv{r.Path, r.Version})
		}
		return nil
	}); err != nil {
		return err
	}
	for _, h := range hits {
		if err := bucketDelete(ctx, s.scope, s.bucket, fileRowKey(h.path, h.version)); err != nil {
			return err
		}
	}
	return nil
}

func (s *fileStorage) List(ctx context.Context) ([]service.FileEntry, error) {
	var entries []service.FileEntry
	err := bucketWalk(ctx, s.scope, s.bucket, nil, func(r *fileRow) error {
		entries = append(entries, service.FileEntry{
			Path:    r.Path,
			Version: r.Version,
			File:    r.toService(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *fileStorage) DeleteAll(ctx context.Context) error {
	type pkv struct {
		path    string
		version int64
	}
	var hits []pkv
	if err := bucketWalk(ctx, s.scope, s.bucket, nil, func(r *fileRow) error {
		hits = append(hits, pkv{r.Path, r.Version})
		return nil
	}); err != nil {
		return err
	}
	for _, h := range hits {
		if err := bucketDelete(ctx, s.scope, s.bucket, fileRowKey(h.path, h.version)); err != nil {
			return err
		}
	}
	return nil
}
