package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/rakunlabs/pika/internal/service"
)

type settingsStorage struct {
	q Querier
}

func (s *settingsStorage) Get(ctx context.Context) (*service.Settings, error) {
	var dataJSON string

	err := s.q.QueryRowContext(ctx,
		`SELECT data FROM settings WHERE id = 'default'`,
	).Scan(&dataJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}

	var settings service.Settings
	if err := json.Unmarshal([]byte(dataJSON), &settings); err != nil {
		return nil, err
	}

	return &settings, nil
}

func (s *settingsStorage) Set(ctx context.Context, settings *service.Settings) error {
	dataJSON, err := json.Marshal(settings)
	if err != nil {
		return err
	}

	_, err = s.q.ExecContext(ctx,
		`INSERT INTO settings (id, data) VALUES ('default', ?)
		 ON CONFLICT(id) DO UPDATE SET data=excluded.data`,
		string(dataJSON),
	)
	return err
}
