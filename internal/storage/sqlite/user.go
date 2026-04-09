package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/sqlite3"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/query"
	"github.com/rakunlabs/query/adapter/adaptergoqu"
)

type userStorage struct {
	q Querier
}

func (s *userStorage) Create(ctx context.Context, user *service.User) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO users (id, username, password_hash, disabled, is_superadmin, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		user.ID, user.Username, user.PasswordHash, boolToInt(user.Disabled),
		boolToInt(user.IsSuperadmin),
		user.CreatedAt.Format(timeFormat), user.UpdatedAt.Format(timeFormat),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return service.ErrConflict
		}
		return err
	}
	return nil
}

func (s *userStorage) Get(ctx context.Context, id string) (*service.User, error) {
	row := s.q.QueryRowContext(ctx,
		`SELECT id, username, password_hash, disabled, is_superadmin, created_at, updated_at
		 FROM users WHERE id = ?`, id,
	)
	return scanUser(row)
}

func (s *userStorage) GetByUsername(ctx context.Context, username string) (*service.User, error) {
	row := s.q.QueryRowContext(ctx,
		`SELECT id, username, password_hash, disabled, is_superadmin, created_at, updated_at
		 FROM users WHERE username = ?`, username,
	)
	return scanUser(row)
}

func (s *userStorage) List(ctx context.Context, q *query.Query) ([]service.User, int64, error) {
	dialect := goqu.Dialect("sqlite3")

	// Count query
	countDS := dialect.From("users").Select(goqu.COUNT("*"))
	if q != nil {
		countDS = adaptergoqu.Select(q, countDS,
			adaptergoqu.WithDefaultSelect("COUNT(*)"),
		)
		// Remove limit/offset/sort for count — rebuild with just WHERE
		countDS = dialect.From("users").Select(goqu.COUNT("*"))
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
	ds := dialect.From("users").Select("id", "username", "password_hash", "disabled", "is_superadmin", "created_at", "updated_at")
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

	var users []service.User
	for rows.Next() {
		user, err := scanUserRows(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, *user)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	if users == nil {
		users = []service.User{}
	}

	return users, total, nil
}

func (s *userStorage) Update(ctx context.Context, user *service.User) error {
	_, err := s.q.ExecContext(ctx,
		`UPDATE users SET username=?, password_hash=?, disabled=?, is_superadmin=?, updated_at=? WHERE id=?`,
		user.Username, user.PasswordHash, boolToInt(user.Disabled),
		boolToInt(user.IsSuperadmin),
		user.UpdatedAt.Format(timeFormat), user.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return service.ErrConflict
		}
		return err
	}
	return nil
}

func (s *userStorage) Delete(ctx context.Context, id string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return err
}

func (s *userStorage) Count(ctx context.Context) (int64, error) {
	var count int64
	err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

// scanUser scans a single user row.
func scanUser(row *sql.Row) (*service.User, error) {
	var u service.User
	var disabled, isSuperadmin int
	var createdAt, updatedAt string
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &disabled, &isSuperadmin, &createdAt, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}
	u.Disabled = disabled != 0
	u.IsSuperadmin = isSuperadmin != 0
	u.CreatedAt = parseTime(createdAt)
	u.UpdatedAt = parseTime(updatedAt)
	return &u, nil
}

func scanUserRows(rows *sql.Rows) (*service.User, error) {
	var u service.User
	var disabled, isSuperadmin int
	var createdAt, updatedAt string
	err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &disabled, &isSuperadmin, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	u.Disabled = disabled != 0
	u.IsSuperadmin = isSuperadmin != 0
	u.CreatedAt = parseTime(createdAt)
	u.UpdatedAt = parseTime(updatedAt)
	return &u, nil
}
