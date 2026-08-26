package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PermissionSource identifies where a user's capability keys were resolved from.
type PermissionSource string

const (
	// PermissionSourceNone means nothing is configured for this user. The
	// progressive-restriction model treats this as "allow everything".
	PermissionSourceNone PermissionSource = "none"
	// PermissionSourceBuiltin means the keys came from the users / permissions
	// / user_permissions tables (built-in auth path).
	PermissionSourceBuiltin PermissionSource = "builtin"
)

// CreatePermissionRequest is the request body for creating a permission.
type CreatePermissionRequest struct {
	Key         string              `json:"key"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Keys        []string            `json:"keys"`
	KeyPatterns map[string][]string `json:"key_patterns,omitempty"`
}

// UpdatePermissionRequest is the request body for updating a permission.
//
// KeyPatterns semantics on update:
//   - omitted (nil)  → patterns left untouched
//   - present (even if empty map) → patterns replaced wholesale; keys not
//     in the map become unrestricted
type UpdatePermissionRequest struct {
	Key         *string             `json:"key,omitempty"`
	Name        *string             `json:"name,omitempty"`
	Description *string             `json:"description,omitempty"`
	Keys        []string            `json:"keys,omitempty"`
	KeyPatterns map[string][]string `json:"key_patterns,omitempty"`
}

func generatePermissionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreatePermission creates a new permission.
func (s *Service) CreatePermission(ctx context.Context, req *CreatePermissionRequest) (*Permission, error) {
	if req.Key == "" {
		return nil, fmt.Errorf("key is required: %w", ErrBadRequest)
	}
	if req.Name == "" {
		return nil, fmt.Errorf("name is required: %w", ErrBadRequest)
	}

	id, err := generatePermissionID()
	if err != nil {
		return nil, fmt.Errorf("generating permission ID: %w", err)
	}

	perm := &Permission{
		ID:          id,
		Key:         req.Key,
		Name:        req.Name,
		Description: req.Description,
		Keys:        req.Keys,
		KeyPatterns: sanitizeKeyPatterns(req.KeyPatterns),
		CreatedAt:   time.Now(),
	}

	if err := s.store.Permissions().Create(ctx, perm); err != nil {
		return nil, err
	}

	return perm, nil
}

// ListPermissions returns all permissions.
func (s *Service) ListPermissions(ctx context.Context) ([]Permission, error) {
	return s.store.Permissions().List(ctx)
}

// PermissionsByKeys returns the permission bundles whose Key appears in the
// given list. Lookup is by the human-assignable Key (not the internal ID).
// Unknown keys are skipped — a dangling reference (e.g. an external role
// mapping pointing at a deleted permission) silently grants nothing rather
// than erroring, keeping the request fail-closed. Order and duplicates of the
// input are irrelevant; the result contains each matching bundle once.
func (s *Service) PermissionsByKeys(ctx context.Context, keys []string) ([]Permission, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	want := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if k != "" {
			want[k] = struct{}{}
		}
	}
	if len(want) == 0 {
		return nil, nil
	}
	all, err := s.store.Permissions().List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Permission, 0, len(want))
	for _, p := range all {
		if _, ok := want[p.Key]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// UpdatePermission updates a permission's properties.
func (s *Service) UpdatePermission(ctx context.Context, id string, req *UpdatePermissionRequest) error {
	perm, err := s.store.Permissions().Get(ctx, id)
	if err != nil {
		return err
	}

	if req.Key != nil {
		perm.Key = *req.Key
	}
	if req.Name != nil {
		perm.Name = *req.Name
	}
	if req.Description != nil {
		perm.Description = *req.Description
	}
	if req.Keys != nil {
		perm.Keys = req.Keys
	}
	if req.KeyPatterns != nil {
		perm.KeyPatterns = sanitizeKeyPatterns(req.KeyPatterns)
	}

	return s.store.Permissions().Update(ctx, perm)
}

// sanitizeKeyPatterns trims whitespace, drops empty patterns, deduplicates,
// and returns nil when the result is empty (so JSON omitempty stays clean).
// It also rejects any pattern containing ".." segments to avoid path-traversal
// surprises in glob matching — patterns are intended to scope down, not to
// reach outside their natural namespace.
func sanitizeKeyPatterns(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, ps := range in {
		seen := make(map[string]struct{}, len(ps))
		var clean []string
		for _, p := range ps {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			// Reject obvious traversal attempts. doublestar treats ".." as
			// a literal segment, but allowing it in a permission pattern
			// is almost certainly a mistake.
			if hasTraversalSegment(p) {
				continue
			}
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			clean = append(clean, p)
		}
		if len(clean) > 0 {
			out[k] = clean
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func hasTraversalSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// DeletePermission deletes a permission by ID.
func (s *Service) DeletePermission(ctx context.Context, id string) error {
	return s.store.Permissions().Delete(ctx, id)
}

// SetUserPermissions replaces all permissions for a user.
func (s *Service) SetUserPermissions(ctx context.Context, userID string, permissionIDs []string) error {
	// Verify user exists
	if _, err := s.store.Users().Get(ctx, userID); err != nil {
		return err
	}

	return s.store.Permissions().SetUserPermissions(ctx, userID, permissionIDs)
}

// GetUserPermissions returns all permissions for a user.
func (s *Service) GetUserPermissions(ctx context.Context, userID string) ([]Permission, error) {
	return s.store.Permissions().GetUserPermissions(ctx, userID)
}

// ListUserIDsByPermission returns the IDs of every user that has been granted
// the given permission bundle. Returns an empty slice (not nil) when no users
// hold the permission. Used by the admin UI to filter the user list.
func (s *Service) ListUserIDsByPermission(ctx context.Context, permissionID string) ([]string, error) {
	return s.store.Permissions().ListUserIDsByPermission(ctx, permissionID)
}

// GetUserPermissionKeysByUsername returns permission keys for a user by username.
//
// Deprecated: use ResolveUserCapabilityKeys instead. This method remains for
// any out-of-tree callers but delegates to the unified resolver.
func (s *Service) GetUserPermissionKeysByUsername(ctx context.Context, username string) ([]string, bool, error) {
	keys, isSuperadmin, _, err := s.ResolveUserCapabilityKeys(ctx, username)
	return keys, isSuperadmin, err
}

// ResolveUserCapabilityKeys returns the full, deduplicated set of capability
// keys granted to a user from the built-in users/permissions tables.
//
// Deprecated: new code should use ResolveLocalCapabilityKeys directly. This
// method delegates to it and is kept for backward compatibility.
func (s *Service) ResolveUserCapabilityKeys(
	ctx context.Context,
	username string,
) (keys []string, isSuperadmin bool, source PermissionSource, err error) {
	return s.ResolveLocalCapabilityKeys(ctx, username)
}

// ResolveLocalCapabilityKeys returns (keys, isSuperadmin, source, err) for a
// local user identified by username. If the user is not found in the local DB,
// returns source=PermissionSourceNone with no keys (not an error).
func (s *Service) ResolveLocalCapabilityKeys(
	ctx context.Context,
	username string,
) (keys []string, isSuperadmin bool, source PermissionSource, err error) {
	if username == "" {
		return nil, false, PermissionSourceNone, nil
	}
	user, err := s.store.Users().GetByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, false, PermissionSourceNone, nil
		}
		return nil, false, "", err
	}
	return s.resolveCapabilityKeysForUser(ctx, user)
}

// ResolveUserCapabilityKeysByID returns (keys, isSuperadmin, source, err) for
// a user identified by their stable pika user ID. Used by CapResolver for
// external identities (OAuth2 or Header) after FindOrCreateExternalUser
// has resolved the session to a concrete user row. An empty userID yields
// PermissionSourceNone with no error (absent — not a failure).
func (s *Service) ResolveUserCapabilityKeysByID(
	ctx context.Context,
	userID string,
) (keys []string, isSuperadmin bool, source PermissionSource, err error) {
	if userID == "" {
		return nil, false, PermissionSourceNone, nil
	}
	user, err := s.store.Users().Get(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, false, PermissionSourceNone, nil
		}
		return nil, false, "", err
	}
	return s.resolveCapabilityKeysForUser(ctx, user)
}

// resolveCapabilityKeysForUser is the shared tail of both name-keyed and
// id-keyed resolvers: applies the is_superadmin shortcut, else reads
// permissions.
func (s *Service) resolveCapabilityKeysForUser(
	ctx context.Context,
	user *User,
) (keys []string, isSuperadmin bool, source PermissionSource, err error) {
	if user.IsSuperadmin {
		return KnownCapabilityKeys(), true, PermissionSourceBuiltin, nil
	}
	dbKeys, err := s.store.Permissions().GetUserCapabilityKeys(ctx, user.ID)
	if err != nil {
		return nil, false, PermissionSourceBuiltin, err
	}
	return dbKeys, false, PermissionSourceBuiltin, nil
}

// ResolveUserCapabilityPatterns returns, for each capability key the user
// holds, the union of doublestar path patterns scoping that grant.
//
// Union semantics: if any one of the user's permissions grants a key with
// no patterns, the grant is unrestricted across the union (and that key is
// omitted from the returned map). Only keys whose every granting permission
// declares patterns end up in the map; their values are the union of those
// patterns.
//
// This means a "narrow" permission (e.g. `configs/team-a/**`) is widened
// when combined with a "broad" permission (no patterns) — the broad one
// wins. That matches the additive semantics permissions already use today
// for capability keys.
//
// Returns an empty map (not nil) for users with no scoped grants.
func (s *Service) ResolveUserCapabilityPatterns(ctx context.Context, userID string) (map[string][]string, error) {
	if userID == "" {
		return map[string][]string{}, nil
	}
	perms, err := s.store.Permissions().GetUserPermissions(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Pass 1: figure out which keys are unrestricted (some granting
	// permission carries no patterns for that key).
	unrestricted := make(map[string]bool)
	for _, p := range perms {
		for _, k := range p.Keys {
			pats := p.KeyPatterns[k]
			if len(pats) == 0 {
				unrestricted[k] = true
			}
		}
	}

	// Pass 2: for keys that are *always* scoped, union the patterns.
	out := make(map[string][]string)
	for _, p := range perms {
		for k, pats := range p.KeyPatterns {
			if unrestricted[k] || len(pats) == 0 {
				continue
			}
			seen := make(map[string]struct{}, len(out[k]))
			for _, existing := range out[k] {
				seen[existing] = struct{}{}
			}
			for _, pat := range pats {
				if _, dup := seen[pat]; dup {
					continue
				}
				seen[pat] = struct{}{}
				out[k] = append(out[k], pat)
			}
		}
	}

	return out, nil
}

// CapabilitiesFromBundles flattens a set of permission bundles into the
// effective capability keys and the per-key path patterns scoping them.
//
// Patterns use the same union semantics as ResolveUserCapabilityPatterns: a
// key granted without patterns by ANY bundle is unrestricted (and omitted
// from the returned map); only keys scoped by every granting bundle carry the
// union of their patterns. The returned pattern map is nil when no key is
// scoped — callers (and CapabilityPatterns.Allows) treat nil/empty as
// "unrestricted".
//
// This is the shared union used both for a user's DB-assigned bundles and for
// bundles referenced by external role/scope mappings, so the two sources merge
// consistently when combined into one slice.
func CapabilitiesFromBundles(perms []Permission) ([]string, map[string][]string) {
	seen := make(map[string]struct{})
	keys := make([]string, 0)
	for _, p := range perms {
		for _, k := range p.Keys {
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}

	// Pass 1: a key is unrestricted if some granting bundle lists it with no
	// patterns.
	unrestricted := make(map[string]bool)
	for _, p := range perms {
		for _, k := range p.Keys {
			if len(p.KeyPatterns[k]) == 0 {
				unrestricted[k] = true
			}
		}
	}

	// Pass 2: union the patterns of keys that are always scoped. Patterns for
	// keys not actually granted by any bundle are ignored.
	patterns := make(map[string][]string)
	for _, p := range perms {
		for k, pats := range p.KeyPatterns {
			if _, granted := seen[k]; !granted {
				continue
			}
			if unrestricted[k] || len(pats) == 0 {
				continue
			}
			has := make(map[string]struct{}, len(patterns[k]))
			for _, existing := range patterns[k] {
				has[existing] = struct{}{}
			}
			for _, pat := range pats {
				if _, dup := has[pat]; dup {
					continue
				}
				has[pat] = struct{}{}
				patterns[k] = append(patterns[k], pat)
			}
		}
	}

	if len(patterns) == 0 {
		return keys, nil
	}
	return keys, patterns
}

// CheckPermission checks if a user has a specific capability key.
// Returns nil if allowed, ErrForbidden if denied.
//
// Enforcement uses progressive restriction: if no Permission row mentions the
// key, the check passes (preserving zero-config behavior).
func (s *Service) CheckPermission(ctx context.Context, username, permKey string) error {
	keys, isSuperadmin, source, err := s.ResolveLocalCapabilityKeys(ctx, username)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if isSuperadmin {
		return nil
	}
	if source == PermissionSourceNone {
		return nil
	}

	// Progressive restriction: if the key is not referenced in any Permission
	// row, allow unconditionally (zero-config behavior).
	exists, err := s.store.Permissions().HasCapabilityKey(ctx, permKey)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if !exists {
		return nil
	}

	for _, k := range keys {
		if k == permKey {
			return nil
		}
	}
	return fmt.Errorf("permission %q required: %w", permKey, ErrForbidden)
}
