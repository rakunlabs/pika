package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/rakunlabs/pika/internal/service"
)

// fileStorage implements service.FileStorage.
type fileStorage struct {
	q Querier
}

func (s *fileStorage) Get(ctx context.Context, path string, version int64) (*service.File, error) {
	var metaJSON string
	var data []byte

	err := s.q.QueryRowContext(ctx,
		`SELECT meta, data FROM files WHERE path = ? AND version = ?`, path, version,
	).Scan(&metaJSON, &data)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}

	var file service.File
	if err := json.Unmarshal([]byte(metaJSON), &file.Meta); err != nil {
		return nil, err
	}
	file.Data = data

	return &file, nil
}

func (s *fileStorage) Set(ctx context.Context, path string, version int64, file *service.File) error {
	metaJSON, err := json.Marshal(file.Meta)
	if err != nil {
		return err
	}

	_, err = s.q.ExecContext(ctx,
		`INSERT INTO files (path, version, meta, data) VALUES (?, ?, ?, ?)
		 ON CONFLICT(path, version) DO UPDATE SET meta=excluded.meta, data=excluded.data`,
		path, version, string(metaJSON), file.Data,
	)
	return err
}

func (s *fileStorage) Delete(ctx context.Context, path string, version int64) error {
	_, err := s.q.ExecContext(ctx,
		`DELETE FROM files WHERE path = ? AND version = ?`, path, version,
	)
	return err
}

func (s *fileStorage) DeleteAllVersions(ctx context.Context, path string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM files WHERE path = ?`, path)
	return err
}

func (s *fileStorage) DeletePrefix(ctx context.Context, prefix string) error {
	_, err := s.q.ExecContext(ctx,
		`DELETE FROM files WHERE path = ? OR path LIKE ?`,
		prefix, prefix+"/%",
	)
	return err
}

func (s *fileStorage) List(ctx context.Context) ([]service.FileEntry, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT path, version, meta, data FROM files ORDER BY path, version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []service.FileEntry
	for rows.Next() {
		var path string
		var version int64
		var metaJSON string
		var data []byte

		if err := rows.Scan(&path, &version, &metaJSON, &data); err != nil {
			return nil, err
		}

		var file service.File
		if err := json.Unmarshal([]byte(metaJSON), &file.Meta); err != nil {
			return nil, err
		}
		file.Data = data

		entries = append(entries, service.FileEntry{Path: path, Version: version, File: &file})
	}

	return entries, rows.Err()
}

func (s *fileStorage) DeleteAll(ctx context.Context) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM files`)
	return err
}

// fileVersionStorage implements service.FileVersionStorage.
type fileVersionStorage struct {
	q Querier
}

func (s *fileVersionStorage) Get(ctx context.Context, path string) (service.FileVersions, error) {
	var versionsJSON string

	err := s.q.QueryRowContext(ctx,
		`SELECT versions FROM file_versions WHERE path = ?`, path,
	).Scan(&versionsJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}

	var versions service.FileVersions
	if err := json.Unmarshal([]byte(versionsJSON), &versions); err != nil {
		return nil, err
	}

	return versions, nil
}

func (s *fileVersionStorage) Set(ctx context.Context, path string, versions service.FileVersions) error {
	versionsJSON, err := json.Marshal(versions)
	if err != nil {
		return err
	}

	_, err = s.q.ExecContext(ctx,
		`INSERT INTO file_versions (path, versions) VALUES (?, ?)
		 ON CONFLICT(path) DO UPDATE SET versions=excluded.versions`,
		path, string(versionsJSON),
	)
	return err
}

func (s *fileVersionStorage) Delete(ctx context.Context, path string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM file_versions WHERE path = ?`, path)
	return err
}

func (s *fileVersionStorage) DeletePrefix(ctx context.Context, prefix string) error {
	_, err := s.q.ExecContext(ctx,
		`DELETE FROM file_versions WHERE path = ? OR path LIKE ?`,
		prefix, prefix+"/%",
	)
	return err
}

func (s *fileVersionStorage) List(ctx context.Context) ([]service.FileVersionEntry, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT path, versions FROM file_versions ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []service.FileVersionEntry
	for rows.Next() {
		var path, versionsJSON string

		if err := rows.Scan(&path, &versionsJSON); err != nil {
			return nil, err
		}

		var versions service.FileVersions
		if err := json.Unmarshal([]byte(versionsJSON), &versions); err != nil {
			return nil, err
		}

		entries = append(entries, service.FileVersionEntry{Path: path, Versions: versions})
	}

	return entries, rows.Err()
}

func (s *fileVersionStorage) DeleteAll(ctx context.Context) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM file_versions`)
	return err
}
