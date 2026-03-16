package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/rakunlabs/pika/internal/service"
)

type folderStorage struct {
	q Querier
}

func (s *folderStorage) Get(ctx context.Context, path string) (*service.Folder, error) {
	var foldersJSON, filesJSON string
	var variantsJSON sql.NullString

	err := s.q.QueryRowContext(ctx,
		`SELECT folders, files, variants FROM folders WHERE path = ?`, path,
	).Scan(&foldersJSON, &filesJSON, &variantsJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}

	var folder service.Folder
	if err := json.Unmarshal([]byte(foldersJSON), &folder.Folders); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(filesJSON), &folder.Files); err != nil {
		return nil, err
	}
	if variantsJSON.Valid && variantsJSON.String != "" {
		if err := json.Unmarshal([]byte(variantsJSON.String), &folder.Variants); err != nil {
			return nil, err
		}
	}

	return &folder, nil
}

func (s *folderStorage) Set(ctx context.Context, path string, folder *service.Folder) error {
	foldersJSON, err := json.Marshal(folder.Folders)
	if err != nil {
		return err
	}
	filesJSON, err := json.Marshal(folder.Files)
	if err != nil {
		return err
	}

	var variantsJSON *string
	if folder.Variants != nil && len(folder.Variants) > 0 {
		v, err := json.Marshal(folder.Variants)
		if err != nil {
			return err
		}
		s := string(v)
		variantsJSON = &s
	}

	_, err = s.q.ExecContext(ctx,
		`INSERT INTO folders (path, folders, files, variants) VALUES (?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET folders=excluded.folders, files=excluded.files, variants=excluded.variants`,
		path, string(foldersJSON), string(filesJSON), variantsJSON,
	)
	return err
}

func (s *folderStorage) Delete(ctx context.Context, path string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM folders WHERE path = ?`, path)
	return err
}

func (s *folderStorage) DeletePrefix(ctx context.Context, prefix string) error {
	// Delete exact match and all paths starting with prefix/
	_, err := s.q.ExecContext(ctx,
		`DELETE FROM folders WHERE path = ? OR path LIKE ?`,
		prefix, prefix+"/%",
	)
	return err
}
