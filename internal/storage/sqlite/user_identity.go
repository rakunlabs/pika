package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/rakunlabs/pika/internal/service"
)

// generateIdentityID returns a random 16-byte hex string for UserIdentity.ID.
// Matches the shape of existing user/session IDs in the DB.
func generateIdentityID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

type userIdentityStorage struct {
	q Querier
}

const userIdentitySelectCols = `id, user_id, provider, subject, email, display_name, created_at, last_login_at`

// Upsert creates or refreshes the (provider, subject) link.
//
// Semantics:
//   - New row: caller supplies UserID + Provider + Subject; the method
//     fills ID (if empty), CreatedAt (if zero), and stores Email/DisplayName
//     as the initial snapshot. LastLoginAt is set to now.
//   - Existing row: UserID is NEVER overwritten. A provider's `sub` is an
//     immutable assertion about identity; silently relinking to a
//     different user on re-login would be a serious trust bug. Email,
//     DisplayName, and LastLoginAt are refreshed from the new values.
//
// Returns the resulting row as stored.
func (s *userIdentityStorage) Upsert(ctx context.Context, id *service.UserIdentity) (*service.UserIdentity, error) {
	// Try the update-first path to avoid touching CreatedAt on existing
	// rows. If no rows match, fall through to INSERT.
	now := time.Now()

	res, err := s.q.ExecContext(ctx,
		`UPDATE user_identities
		   SET email = ?, display_name = ?, last_login_at = ?
		 WHERE provider = ? AND subject = ?`,
		nullableString(id.Email), nullableString(id.DisplayName),
		now.Format(timeFormat),
		id.Provider, id.Subject,
	)
	if err != nil {
		return nil, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected > 0 {
		return s.FindByProviderSubject(ctx, id.Provider, id.Subject)
	}

	// Insert path — caller is trusted to provide a non-empty UserID.
	if id.ID == "" {
		newID, err := generateIdentityID()
		if err != nil {
			return nil, err
		}
		id.ID = newID
	}
	if id.CreatedAt.IsZero() {
		id.CreatedAt = now
	}
	nowRef := now
	id.LastLoginAt = &nowRef

	_, err = s.q.ExecContext(ctx,
		`INSERT INTO user_identities (id, user_id, provider, subject, email, display_name, created_at, last_login_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id.ID, id.UserID, id.Provider, id.Subject,
		nullableString(id.Email), nullableString(id.DisplayName),
		id.CreatedAt.Format(timeFormat),
		id.LastLoginAt.Format(timeFormat),
	)
	if err != nil {
		if isUniqueViolation(err) {
			// Racing Upsert: another goroutine inserted between our
			// UPDATE (0 rows) and our INSERT. Re-read and return.
			return s.FindByProviderSubject(ctx, id.Provider, id.Subject)
		}
		return nil, err
	}
	return id, nil
}

func (s *userIdentityStorage) FindByProviderSubject(ctx context.Context, provider, subject string) (*service.UserIdentity, error) {
	row := s.q.QueryRowContext(ctx,
		`SELECT `+userIdentitySelectCols+` FROM user_identities WHERE provider = ? AND subject = ?`,
		provider, subject,
	)
	return scanUserIdentity(row)
}

func (s *userIdentityStorage) ListByUserID(ctx context.Context, userID string) ([]service.UserIdentity, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+userIdentitySelectCols+` FROM user_identities WHERE user_id = ? ORDER BY created_at ASC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []service.UserIdentity
	for rows.Next() {
		ident, err := scanUserIdentityRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *ident)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []service.UserIdentity{}
	}
	return out, nil
}

func (s *userIdentityStorage) ListByProvider(ctx context.Context, provider string) ([]service.UserIdentity, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+userIdentitySelectCols+` FROM user_identities WHERE provider = ? ORDER BY created_at ASC`,
		provider,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []service.UserIdentity
	for rows.Next() {
		ident, err := scanUserIdentityRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *ident)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []service.UserIdentity{}
	}
	return out, nil
}

func (s *userIdentityStorage) Delete(ctx context.Context, id string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM user_identities WHERE id = ?`, id)
	return err
}

func (s *userIdentityStorage) DeleteByUserID(ctx context.Context, userID string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM user_identities WHERE user_id = ?`, userID)
	return err
}

// scanUserIdentity scans a single row into a *UserIdentity. Returns
// ErrNotFound on sql.ErrNoRows. last_login_at is left nil when NULL.
func scanUserIdentity(row *sql.Row) (*service.UserIdentity, error) {
	var id service.UserIdentity
	var email, displayName, lastLoginAt sql.NullString
	var createdAt string
	err := row.Scan(
		&id.ID, &id.UserID, &id.Provider, &id.Subject,
		&email, &displayName,
		&createdAt, &lastLoginAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}
	id.Email = email.String
	id.DisplayName = displayName.String
	id.CreatedAt = parseTime(createdAt)
	if lastLoginAt.Valid && lastLoginAt.String != "" {
		t := parseTime(lastLoginAt.String)
		id.LastLoginAt = &t
	}
	return &id, nil
}

func scanUserIdentityRow(rows *sql.Rows) (*service.UserIdentity, error) {
	var id service.UserIdentity
	var email, displayName, lastLoginAt sql.NullString
	var createdAt string
	err := rows.Scan(
		&id.ID, &id.UserID, &id.Provider, &id.Subject,
		&email, &displayName,
		&createdAt, &lastLoginAt,
	)
	if err != nil {
		return nil, err
	}
	id.Email = email.String
	id.DisplayName = displayName.String
	id.CreatedAt = parseTime(createdAt)
	if lastLoginAt.Valid && lastLoginAt.String != "" {
		t := parseTime(lastLoginAt.String)
		id.LastLoginAt = &t
	}
	return &id, nil
}
