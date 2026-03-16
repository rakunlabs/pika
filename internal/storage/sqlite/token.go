package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/doug-martin/goqu/v9"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/query"
	"github.com/rakunlabs/query/adapter/adaptergoqu"
)

type tokenStorage struct {
	q Querier
}

func (s *tokenStorage) Create(ctx context.Context, token *service.Token) error {
	scopesJSON, err := json.Marshal(token.Scopes)
	if err != nil {
		return err
	}

	var expiresAt *string
	if token.ExpiresAt != nil {
		v := token.ExpiresAt.Format(timeFormat)
		expiresAt = &v
	}

	_, err = s.q.ExecContext(ctx,
		`INSERT INTO tokens (id, name, hashed_key, scopes, created_at, created_by, expires_at, active)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		token.ID, token.Name, token.HashedKey, string(scopesJSON),
		token.CreatedAt.Format(timeFormat), token.CreatedBy,
		expiresAt, boolToInt(token.Active),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return service.ErrConflict
		}
		return err
	}
	return nil
}

func (s *tokenStorage) Get(ctx context.Context, id string) (*service.Token, error) {
	row := s.q.QueryRowContext(ctx,
		`SELECT id, name, hashed_key, scopes, created_at, created_by, expires_at, active
		 FROM tokens WHERE id = ?`, id,
	)
	return scanToken(row)
}

func (s *tokenStorage) FindByHash(ctx context.Context, hashedKey string) (*service.Token, error) {
	row := s.q.QueryRowContext(ctx,
		`SELECT id, name, hashed_key, scopes, created_at, created_by, expires_at, active
		 FROM tokens WHERE hashed_key = ?`, hashedKey,
	)
	return scanToken(row)
}

func (s *tokenStorage) List(ctx context.Context, q *query.Query) ([]service.Token, int64, error) {
	dialect := goqu.Dialect("sqlite3")

	// Count query
	countDS := dialect.From("tokens").Select(goqu.COUNT("*"))
	if q != nil {
		exprs := adaptergoqu.Expression(q)
		for _, e := range exprs {
			countDS = countDS.Where(e)
		}
	}

	countSQL, countArgs, err := countDS.ToSQL()
	if err != nil {
		return nil, 0, err
	}

	var total int64
	if err := s.q.QueryRowContext(ctx, countSQL, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// Data query
	ds := dialect.From("tokens").Select("id", "name", "hashed_key", "scopes", "created_at", "created_by", "expires_at", "active")
	if q != nil {
		ds = adaptergoqu.Select(q, ds)
	}

	sqlStr, args, err := ds.ToSQL()
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.q.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var tokens []service.Token
	for rows.Next() {
		token, err := scanTokenRows(rows)
		if err != nil {
			return nil, 0, err
		}
		tokens = append(tokens, *token)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	if tokens == nil {
		tokens = []service.Token{}
	}

	return tokens, total, nil
}

func (s *tokenStorage) Update(ctx context.Context, token *service.Token) error {
	scopesJSON, err := json.Marshal(token.Scopes)
	if err != nil {
		return err
	}

	var expiresAt *string
	if token.ExpiresAt != nil {
		v := token.ExpiresAt.Format(timeFormat)
		expiresAt = &v
	}

	_, err = s.q.ExecContext(ctx,
		`UPDATE tokens SET name=?, scopes=?, expires_at=?, active=? WHERE id=?`,
		token.Name, string(scopesJSON), expiresAt, boolToInt(token.Active), token.ID,
	)
	return err
}

func (s *tokenStorage) Delete(ctx context.Context, id string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM tokens WHERE id = ?`, id)
	return err
}

func scanToken(row *sql.Row) (*service.Token, error) {
	var t service.Token
	var scopesJSON string
	var createdAt, createdBy string
	var expiresAt sql.NullString
	var active int

	err := row.Scan(&t.ID, &t.Name, &t.HashedKey, &scopesJSON, &createdAt, &createdBy, &expiresAt, &active)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}

	if err := json.Unmarshal([]byte(scopesJSON), &t.Scopes); err != nil {
		return nil, err
	}

	t.CreatedAt = parseTime(createdAt)
	t.CreatedBy = createdBy
	t.Active = active != 0

	if expiresAt.Valid {
		v := parseTime(expiresAt.String)
		t.ExpiresAt = &v
	}

	return &t, nil
}

func scanTokenRows(rows *sql.Rows) (*service.Token, error) {
	var t service.Token
	var scopesJSON string
	var createdAt, createdBy string
	var expiresAt sql.NullString
	var active int

	err := rows.Scan(&t.ID, &t.Name, &t.HashedKey, &scopesJSON, &createdAt, &createdBy, &expiresAt, &active)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(scopesJSON), &t.Scopes); err != nil {
		return nil, err
	}

	t.CreatedAt = parseTime(createdAt)
	t.CreatedBy = createdBy
	t.Active = active != 0

	if expiresAt.Valid {
		v := parseTime(expiresAt.String)
		t.ExpiresAt = &v
	}

	return &t, nil
}
