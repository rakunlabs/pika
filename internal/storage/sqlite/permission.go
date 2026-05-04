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

	// Insert per-key path patterns
	if err := s.applyKeyPatterns(ctx, perm.ID, perm.Keys, perm.KeyPatterns); err != nil {
		return err
	}

	return nil
}

// applyKeyPatterns persists the per-key pattern map. Patterns are only
// written for keys that are also in the granted-keys set; entries for
// non-granted keys are silently dropped (defensive — the FK would reject
// them anyway).
func (s *permissionStorage) applyKeyPatterns(ctx context.Context, permissionID string, keys []string, patterns map[string][]string) error {
	granted := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		granted[k] = struct{}{}
	}
	for k, ps := range patterns {
		if _, ok := granted[k]; !ok {
			continue
		}
		if err := s.SetPermissionKeyPatterns(ctx, permissionID, k, ps); err != nil {
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
	patterns, err := s.getPermissionKeyPatterns(ctx, id)
	if err != nil {
		return nil, err
	}
	p.KeyPatterns = patterns
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
		patterns, err := s.getPermissionKeyPatterns(ctx, perms[i].ID)
		if err != nil {
			return nil, err
		}
		perms[i].KeyPatterns = patterns
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

	// Update capability keys (this also clears patterns for any removed keys
	// because permission_key_patterns has a composite FK on
	// (permission_id, key) with ON DELETE CASCADE).
	if err := s.SetPermissionKeys(ctx, perm.ID, perm.Keys); err != nil {
		return err
	}

	return s.applyKeyPatterns(ctx, perm.ID, perm.Keys, perm.KeyPatterns)
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

// SetUserPermissions replaces every assignment for a user with rows
// tagged 'local'. Used by the admin-UI permission editor — admins are
// in charge of every row, so wholesale replacement is fine.
func (s *permissionStorage) SetUserPermissions(ctx context.Context, userID string, permissionIDs []string) error {
	if _, err := s.q.ExecContext(ctx, `DELETE FROM user_permissions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	return s.insertUserPermissions(ctx, userID, "local", permissionIDs)
}

// SetUserPermissionsBySource replaces only the rows of a single source.
// Local rows (source='local') are preserved; the sync engine never
// trampling admin-curated grants is the whole point of the source column.
func (s *permissionStorage) SetUserPermissionsBySource(ctx context.Context, userID, source string, permissionIDs []string) error {
	if source == "" {
		source = "local"
	}
	if _, err := s.q.ExecContext(ctx,
		`DELETE FROM user_permissions WHERE user_id = ? AND source = ?`,
		userID, source); err != nil {
		return err
	}
	return s.insertUserPermissions(ctx, userID, source, permissionIDs)
}

func (s *permissionStorage) insertUserPermissions(ctx context.Context, userID, source string, permissionIDs []string) error {
	if len(permissionIDs) == 0 {
		return nil
	}

	// Dedupe — the same (user, permission, source) triple PK would otherwise
	// reject the bulk insert if a caller passes the same id twice.
	seen := make(map[string]struct{}, len(permissionIDs))

	var sb strings.Builder
	sb.WriteString(`INSERT OR IGNORE INTO user_permissions (user_id, permission_id, source) VALUES `)
	args := make([]interface{}, 0, len(permissionIDs)*3)
	first := true
	for _, pid := range permissionIDs {
		if pid == "" {
			continue
		}
		if _, dup := seen[pid]; dup {
			continue
		}
		seen[pid] = struct{}{}

		if !first {
			sb.WriteString(", ")
		}
		first = false
		sb.WriteString("(?, ?, ?)")
		args = append(args, userID, pid, source)
	}
	if first {
		return nil
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
		patterns, err := s.getPermissionKeyPatterns(ctx, perms[i].ID)
		if err != nil {
			return nil, err
		}
		perms[i].KeyPatterns = patterns
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

// getPermissionKeyPatterns returns the per-key path-pattern map for a given
// permission. Keys with no patterns are omitted from the map (callers treat
// "missing" identically to "empty slice" — both mean unrestricted).
func (s *permissionStorage) getPermissionKeyPatterns(ctx context.Context, permissionID string) (map[string][]string, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT key, pattern FROM permission_key_patterns WHERE permission_id = ? ORDER BY key, pattern`,
		permissionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string][]string{}
	for rows.Next() {
		var key, pat string
		if err := rows.Scan(&key, &pat); err != nil {
			return nil, err
		}
		out[key] = append(out[key], pat)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SetPermissionKeyPatterns replaces the pattern set for a single
// (permission, key) grant. The (permission_id, key) row must already exist
// in permission_keys; SQLite's FK enforcement will reject otherwise.
func (s *permissionStorage) SetPermissionKeyPatterns(ctx context.Context, permissionID, key string, patterns []string) error {
	if _, err := s.q.ExecContext(ctx,
		`DELETE FROM permission_key_patterns WHERE permission_id = ? AND key = ?`,
		permissionID, key,
	); err != nil {
		return err
	}

	if len(patterns) == 0 {
		return nil
	}

	// Deduplicate to avoid PK collisions on the (permission_id, key, pattern)
	// composite, then bulk-insert.
	seen := make(map[string]struct{}, len(patterns))
	var sb strings.Builder
	sb.WriteString(`INSERT INTO permission_key_patterns (permission_id, key, pattern) VALUES `)
	args := make([]interface{}, 0, len(patterns)*3)
	first := true
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}

		if !first {
			sb.WriteString(", ")
		}
		first = false
		sb.WriteString("(?, ?, ?)")
		args = append(args, permissionID, key, p)
	}
	if first {
		// All patterns were empty/duplicates → nothing to insert.
		return nil
	}

	_, err := s.q.ExecContext(ctx, sb.String(), args...)
	return err
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
