package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/rakunlabs/pika/internal/service"
)

type permissionStorage struct {
	q Querier
}

func (s *permissionStorage) Create(ctx context.Context, perm *service.Permission) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO permissions (id, key, name, description, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		perm.ID, perm.Key, perm.Name, perm.Description,
		perm.CreatedAt.Format(timeFormat),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return service.ErrConflict
		}
		return err
	}

	// Insert capability keys
	if len(perm.Keys) > 0 {
		if err := s.SetPermissionKeys(ctx, perm.ID, perm.Keys); err != nil {
			return err
		}
	}

	return nil
}

func (s *permissionStorage) Get(ctx context.Context, id string) (*service.Permission, error) {
	row := s.q.QueryRowContext(ctx,
		`SELECT id, key, name, description, created_at FROM permissions WHERE id = ?`, id,
	)
	p, err := scanPermission(row)
	if err != nil {
		return nil, err
	}

	keys, err := s.getPermissionKeys(ctx, id)
	if err != nil {
		return nil, err
	}
	p.Keys = keys
	return p, nil
}

func (s *permissionStorage) List(ctx context.Context) ([]service.Permission, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, key, name, description, created_at FROM permissions ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var perms []service.Permission
	for rows.Next() {
		var p service.Permission
		var createdAt string
		if err := rows.Scan(&p.ID, &p.Key, &p.Name, &p.Description, &createdAt); err != nil {
			return nil, err
		}
		p.CreatedAt = parseTime(createdAt)
		perms = append(perms, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if perms == nil {
		perms = []service.Permission{}
	}

	// Batch-load keys for all permissions
	for i := range perms {
		keys, err := s.getPermissionKeys(ctx, perms[i].ID)
		if err != nil {
			return nil, err
		}
		perms[i].Keys = keys
	}

	return perms, nil
}

func (s *permissionStorage) Update(ctx context.Context, perm *service.Permission) error {
	_, err := s.q.ExecContext(ctx,
		`UPDATE permissions SET key=?, name=?, description=? WHERE id=?`,
		perm.Key, perm.Name, perm.Description, perm.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return service.ErrConflict
		}
		return err
	}

	// Update capability keys
	return s.SetPermissionKeys(ctx, perm.ID, perm.Keys)
}

func (s *permissionStorage) Delete(ctx context.Context, id string) error {
	// permission_keys rows are cascade-deleted by the FK
	_, err := s.q.ExecContext(ctx, `DELETE FROM permissions WHERE id = ?`, id)
	return err
}

func (s *permissionStorage) SetPermissionKeys(ctx context.Context, permissionID string, keys []string) error {
	// Delete existing keys
	if _, err := s.q.ExecContext(ctx, `DELETE FROM permission_keys WHERE permission_id = ?`, permissionID); err != nil {
		return err
	}

	if len(keys) == 0 {
		return nil
	}

	var sb strings.Builder
	sb.WriteString(`INSERT INTO permission_keys (permission_id, key) VALUES `)
	args := make([]interface{}, 0, len(keys)*2)
	for i, k := range keys {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(?, ?)")
		args = append(args, permissionID, k)
	}

	_, err := s.q.ExecContext(ctx, sb.String(), args...)
	return err
}

func (s *permissionStorage) SetUserPermissions(ctx context.Context, userID string, permissionIDs []string) error {
	if _, err := s.q.ExecContext(ctx, `DELETE FROM user_permissions WHERE user_id = ?`, userID); err != nil {
		return err
	}

	if len(permissionIDs) == 0 {
		return nil
	}

	var sb strings.Builder
	sb.WriteString(`INSERT INTO user_permissions (user_id, permission_id) VALUES `)
	args := make([]interface{}, 0, len(permissionIDs)*2)
	for i, pid := range permissionIDs {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(?, ?)")
		args = append(args, userID, pid)
	}

	_, err := s.q.ExecContext(ctx, sb.String(), args...)
	return err
}

func (s *permissionStorage) GetUserPermissions(ctx context.Context, userID string) ([]service.Permission, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT p.id, p.key, p.name, p.description, p.created_at
		 FROM permissions p
		 INNER JOIN user_permissions up ON up.permission_id = p.id
		 WHERE up.user_id = ?
		 ORDER BY p.name`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var perms []service.Permission
	for rows.Next() {
		var p service.Permission
		var createdAt string
		if err := rows.Scan(&p.ID, &p.Key, &p.Name, &p.Description, &createdAt); err != nil {
			return nil, err
		}
		p.CreatedAt = parseTime(createdAt)
		perms = append(perms, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if perms == nil {
		perms = []service.Permission{}
	}

	// Load keys for each permission
	for i := range perms {
		keys, err := s.getPermissionKeys(ctx, perms[i].ID)
		if err != nil {
			return nil, err
		}
		perms[i].Keys = keys
	}

	return perms, nil
}

// GetUserCapabilityKeys returns the deduplicated set of capability keys
// granted to a user through all their assigned permissions.
func (s *permissionStorage) GetUserCapabilityKeys(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT DISTINCT pk.key
		 FROM permission_keys pk
		 INNER JOIN user_permissions up ON up.permission_id = pk.permission_id
		 WHERE up.user_id = ?`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if keys == nil {
		keys = []string{}
	}
	return keys, nil
}

// HasCapabilityKey checks if any permission in the system grants the given capability key.
func (s *permissionStorage) HasCapabilityKey(ctx context.Context, key string) (bool, error) {
	var count int64
	err := s.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM permission_keys WHERE key = ?`, key,
	).Scan(&count)
	return count > 0, err
}

// UserHasCapability checks if a user has been granted the given capability key
// through any of their assigned permissions.
func (s *permissionStorage) UserHasCapability(ctx context.Context, userID string, key string) (bool, error) {
	var count int64
	err := s.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM permission_keys pk
		 INNER JOIN user_permissions up ON up.permission_id = pk.permission_id
		 WHERE up.user_id = ? AND pk.key = ?`, userID, key,
	).Scan(&count)
	return count > 0, err
}

// getPermissionKeys returns the capability keys for a given permission.
func (s *permissionStorage) getPermissionKeys(ctx context.Context, permissionID string) ([]string, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT key FROM permission_keys WHERE permission_id = ? ORDER BY key`, permissionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if keys == nil {
		keys = []string{}
	}
	return keys, nil
}

func scanPermission(row *sql.Row) (*service.Permission, error) {
	var p service.Permission
	var createdAt string
	err := row.Scan(&p.ID, &p.Key, &p.Name, &p.Description, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}
	p.CreatedAt = parseTime(createdAt)
	return &p, nil
}
