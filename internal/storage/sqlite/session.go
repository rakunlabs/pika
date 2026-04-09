package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/rakunlabs/pika/internal/service"
)

type sessionStorage struct {
	q Querier
}

func (s *sessionStorage) Create(ctx context.Context, session *service.Session) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, username, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?)`,
		session.ID, session.UserID, session.Username,
		session.CreatedAt.Format(timeFormat), session.ExpiresAt.Format(timeFormat),
	)
	return err
}

func (s *sessionStorage) Get(ctx context.Context, id string) (*service.Session, error) {
	row := s.q.QueryRowContext(ctx,
		`SELECT id, user_id, username, created_at, expires_at
		 FROM sessions WHERE id = ?`, id,
	)

	var sess service.Session
	var createdAt, expiresAt string
	err := row.Scan(&sess.ID, &sess.UserID, &sess.Username, &createdAt, &expiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}

	sess.CreatedAt = parseTime(createdAt)
	sess.ExpiresAt = parseTime(expiresAt)
	return &sess, nil
}

func (s *sessionStorage) Delete(ctx context.Context, id string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (s *sessionStorage) DeleteByUserID(ctx context.Context, userID string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (s *sessionStorage) DeleteExpired(ctx context.Context) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`,
		time.Now().Format(timeFormat),
	)
	return err
}

func (s *sessionStorage) CountByUserID(ctx context.Context, userID string) (int64, error) {
	var count int64
	err := s.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE user_id = ? AND expires_at > ?`,
		userID, time.Now().Format(timeFormat),
	).Scan(&count)
	return count, err
}
