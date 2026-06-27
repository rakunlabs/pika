package bw

import (
	"context"
	"errors"
	"sort"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/pika/internal/service"
)

// permissionStorage implements service.PermissionStorage.
//
// Key design departure from the SQLite layer: there is no junction
// table. permission_keys / permission_key_patterns collapse onto the
// permission row's Keys/KeyPatterns fields; user_permissions collapses
// onto the user row's Grants []userGrant slice. The "find every user
// with permission X" reverse direction is no longer needed by the
// service code (only the forward direction "what permissions does this
// user have" is used) so denormalization wins.
type permissionStorage struct {
	store  *Storage
	bucket *bw.Bucket[permissionRow]
	scope  scope
}

func (s *Storage) permissionsAt(sc scope) *permissionStorage {
	return &permissionStorage{store: s, bucket: s.permissions, scope: sc}
}

func (s *Storage) Permissions() service.PermissionStorage   { return s.permissionsAt(s.dbScope()) }
func (t *txStorage) Permissions() service.PermissionStorage { return t.base.permissionsAt(t.scope) }

func (s *permissionStorage) Create(ctx context.Context, perm *service.Permission) error {
	return bucketInsertNew(ctx, s.scope, s.bucket, permissionRowFromService(perm))
}

func (s *permissionStorage) getRow(ctx context.Context, id string) (*permissionRow, error) {
	return bucketGet(ctx, s.scope, s.bucket, id)
}

func (s *permissionStorage) Get(ctx context.Context, id string) (*service.Permission, error) {
	row, err := s.getRow(ctx, id)
	if err != nil {
		return nil, err
	}
	return row.toService(), nil
}

func (s *permissionStorage) listRows(ctx context.Context) ([]*permissionRow, error) {
	return bucketFind(ctx, s.scope, s.bucket, nil)
}

func (s *permissionStorage) List(ctx context.Context) ([]service.Permission, error) {
	rows, err := s.listRows(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	out := make([]service.Permission, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r.toService())
	}
	return out, nil
}

func (s *permissionStorage) Update(ctx context.Context, perm *service.Permission) error {
	return bucketUpdate(ctx, s.scope, s.bucket, permissionRowFromService(perm))
}

// Delete removes the permission row and strips it from every user's
// Grants slice. The SQL backend used FK ON DELETE CASCADE on
// user_permissions; here the cascade is explicit.
func (s *permissionStorage) Delete(ctx context.Context, id string) error {
	// Strip from users first so a partial failure doesn't leave dangling
	// grants pointing at a deleted permission.
	users := s.store.usersAt(s.scope)
	rows, err := users.listRowsAll(ctx)
	if err != nil {
		return err
	}
	for _, ur := range rows {
		filtered := filterGrantsByPermission(ur.Grants, id)
		if len(filtered) == len(ur.Grants) {
			continue
		}
		ur.Grants = filtered
		if err := users.updateRow(ctx, ur); err != nil {
			return err
		}
	}

	return bucketDelete(ctx, s.scope, s.bucket, id)
}

func filterGrantsByPermission(grants []userGrant, permID string) []userGrant {
	out := grants[:0]
	for _, g := range grants {
		if g.PermissionID == permID {
			continue
		}
		out = append(out, g)
	}
	// Re-slice to a fresh backing array so the caller's snapshot doesn't
	// alias the trimmed tail.
	clean := make([]userGrant, len(out))
	copy(clean, out)
	return clean
}

// SetPermissionKeys replaces the Keys slice on a permission row.
func (s *permissionStorage) SetPermissionKeys(ctx context.Context, permissionID string, keys []string) error {
	row, err := s.getRow(ctx, permissionID)
	if err != nil {
		return err
	}
	row.Keys = append([]string(nil), keys...)

	// When a key is removed, drop its patterns too — the SQL backend
	// did this via FK CASCADE on permission_key_patterns(permission_id, key).
	if len(row.KeyPatterns) > 0 {
		granted := make(map[string]struct{}, len(keys))
		for _, k := range keys {
			granted[k] = struct{}{}
		}
		for k := range row.KeyPatterns {
			if _, ok := granted[k]; !ok {
				delete(row.KeyPatterns, k)
			}
		}
		if len(row.KeyPatterns) == 0 {
			row.KeyPatterns = nil
		}
	}

	return bucketUpdate(ctx, s.scope, s.bucket, row)
}

// SetPermissionKeyPatterns replaces the patterns for a single
// (permission, key) grant. The key must already be in row.Keys —
// matches the SQL FK constraint that rejected orphan patterns.
func (s *permissionStorage) SetPermissionKeyPatterns(ctx context.Context, permissionID, key string, patterns []string) error {
	row, err := s.getRow(ctx, permissionID)
	if err != nil {
		return err
	}
	// Skip silently if the key isn't granted (mirror SQLite FK reject
	// behavior — the service layer's applyKeyPatterns also does this).
	granted := false
	for _, k := range row.Keys {
		if k == key {
			granted = true
			break
		}
	}
	if !granted {
		return nil
	}

	if row.KeyPatterns == nil {
		row.KeyPatterns = make(map[string][]string)
	}
	if len(patterns) == 0 {
		delete(row.KeyPatterns, key)
		if len(row.KeyPatterns) == 0 {
			row.KeyPatterns = nil
		}
	} else {
		// Dedupe patterns inline, drop empties.
		seen := make(map[string]struct{}, len(patterns))
		clean := make([]string, 0, len(patterns))
		for _, p := range patterns {
			if p == "" {
				continue
			}
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			clean = append(clean, p)
		}
		if len(clean) == 0 {
			delete(row.KeyPatterns, key)
			if len(row.KeyPatterns) == 0 {
				row.KeyPatterns = nil
			}
		} else {
			row.KeyPatterns[key] = clean
		}
	}

	return bucketUpdate(ctx, s.scope, s.bucket, row)
}

// SetUserPermissions replaces every grant for a user with rows tagged
// 'local'. Used by the admin UI permission editor.
func (s *permissionStorage) SetUserPermissions(ctx context.Context, userID string, permissionIDs []string) error {
	users := s.store.usersAt(s.scope)
	row, err := users.getRow(ctx, userID)
	if err != nil {
		return err
	}
	row.Grants = buildGrants("local", permissionIDs)
	return users.updateRow(ctx, row)
}

// SetUserPermissionsBySource replaces only the grants whose source
// matches; rows from other sources are preserved.
func (s *permissionStorage) SetUserPermissionsBySource(ctx context.Context, userID, source string, permissionIDs []string) error {
	if source == "" {
		source = "local"
	}
	users := s.store.usersAt(s.scope)
	row, err := users.getRow(ctx, userID)
	if err != nil {
		return err
	}

	// Keep grants from other sources verbatim.
	preserved := make([]userGrant, 0, len(row.Grants))
	for _, g := range row.Grants {
		if g.Source != source {
			preserved = append(preserved, g)
		}
	}
	preserved = append(preserved, buildGrants(source, permissionIDs)...)
	row.Grants = preserved
	return users.updateRow(ctx, row)
}

// buildGrants dedupes permission IDs and tags each with the source.
func buildGrants(source string, permissionIDs []string) []userGrant {
	if len(permissionIDs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(permissionIDs))
	out := make([]userGrant, 0, len(permissionIDs))
	for _, pid := range permissionIDs {
		if pid == "" {
			continue
		}
		if _, dup := seen[pid]; dup {
			continue
		}
		seen[pid] = struct{}{}
		out = append(out, userGrant{PermissionID: pid, Source: source})
	}
	return out
}

func (s *permissionStorage) GetUserPermissions(ctx context.Context, userID string) ([]service.Permission, error) {
	users := s.store.usersAt(s.scope)
	row, err := users.getRow(ctx, userID)
	if err != nil {
		return nil, err
	}

	if len(row.Grants) == 0 {
		return []service.Permission{}, nil
	}

	// Dedupe permission IDs across sources before fetching.
	wanted := make(map[string]struct{}, len(row.Grants))
	ids := make([]string, 0, len(row.Grants))
	for _, g := range row.Grants {
		if _, dup := wanted[g.PermissionID]; dup {
			continue
		}
		wanted[g.PermissionID] = struct{}{}
		ids = append(ids, g.PermissionID)
	}

	// Pull the permissions one by one; tiny result sets in practice.
	out := make([]service.Permission, 0, len(ids))
	for _, id := range ids {
		p, err := s.getRow(ctx, id)
		if err != nil {
			if errors.Is(err, service.ErrNotFound) {
				// Stale grant — silently drop. Mirrors how a dangling FK
				// would show up after a manual DELETE.
				continue
			}
			return nil, err
		}
		out = append(out, *p.toService())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ListUserIDsByPermission scans every user row and returns the IDs of
// those whose Grants slice contains permissionID. This is an O(N) walk
// over users; acceptable for admin filtering and matches the existing
// cascade-delete walk style. The returned slice is sorted for stable
// downstream behavior (so query.Cache-style fingerprints don't churn).
func (s *permissionStorage) ListUserIDsByPermission(ctx context.Context, permissionID string) ([]string, error) {
	if permissionID == "" {
		return []string{}, nil
	}
	users := s.store.usersAt(s.scope)
	rows, err := users.listRowsAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0)
	for _, r := range rows {
		for _, g := range r.Grants {
			if g.PermissionID == permissionID {
				out = append(out, r.ID)
				break
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *permissionStorage) GetUserCapabilityKeys(ctx context.Context, userID string) ([]string, error) {
	perms, err := s.GetUserPermissions(ctx, userID)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, p := range perms {
		for _, k := range p.Keys {
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}
	return out, nil
}

func (s *permissionStorage) HasCapabilityKey(ctx context.Context, key string) (bool, error) {
	rows, err := s.listRows(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range rows {
		for _, k := range r.Keys {
			if k == key {
				return true, nil
			}
		}
	}
	return false, nil
}

func (s *permissionStorage) UserHasCapability(ctx context.Context, userID string, key string) (bool, error) {
	keys, err := s.GetUserCapabilityKeys(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, k := range keys {
		if k == key {
			return true, nil
		}
	}
	return false, nil
}

// GetUserDeniedCapabilities returns the per-user deny overlay (capability
// keys subtracted from the resolved set). Empty slice when none.
func (s *permissionStorage) GetUserDeniedCapabilities(ctx context.Context, userID string) ([]string, error) {
	users := s.store.usersAt(s.scope)
	row, err := users.getRow(ctx, userID)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), row.DeniedCaps...), nil
}

// SetUserDeniedCapabilities replaces the per-user deny overlay. Writes the
// user row directly (like SetUserPermissions does for Grants) so it bypasses
// the Update preserve path.
func (s *permissionStorage) SetUserDeniedCapabilities(ctx context.Context, userID string, keys []string) error {
	users := s.store.usersAt(s.scope)
	row, err := users.getRow(ctx, userID)
	if err != nil {
		return err
	}
	// Dedupe, drop empties; nil when empty so the column stays clean.
	if len(keys) == 0 {
		row.DeniedCaps = nil
		return users.updateRow(ctx, row)
	}
	seen := make(map[string]struct{}, len(keys))
	clean := make([]string, 0, len(keys))
	for _, k := range keys {
		if k == "" {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		clean = append(clean, k)
	}
	row.DeniedCaps = clean
	return users.updateRow(ctx, row)
}
