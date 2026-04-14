package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// PermissionSource identifies where a user's capability keys were resolved from.
// Used by CheckPermission to apply the right enforcement semantics and by
// infoHandler for diagnostics shipped to the UI.
type PermissionSource string

const (
	// PermissionSourceNone means nothing is configured for this user — neither
	// a built-in users row nor an enabled external-permissions mapping. The
	// progressive-restriction model treats this as "allow everything".
	PermissionSourceNone PermissionSource = "none"
	// PermissionSourceBuiltin means the keys came from the users / permissions
	// / user_permissions tables (built-in auth path).
	PermissionSourceBuiltin PermissionSource = "builtin"
	// PermissionSourceExternal means the keys came from the settings-stored
	// ExternalPermissions mapping (forward-auth path). Enforcement is strict:
	// a missing key denies.
	PermissionSourceExternal PermissionSource = "external"
)

// CreatePermissionRequest is the request body for creating a permission.
type CreatePermissionRequest struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Keys        []string `json:"keys"`
}

// UpdatePermissionRequest is the request body for updating a permission.
type UpdatePermissionRequest struct {
	Key         *string  `json:"key,omitempty"`
	Name        *string  `json:"name,omitempty"`
	Description *string  `json:"description,omitempty"`
	Keys        []string `json:"keys,omitempty"`
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

	return s.store.Permissions().Update(ctx, perm)
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

// GetUserPermissionKeysByUsername returns permission keys for a user by username.
//
// Deprecated: use ResolveUserCapabilityKeys instead. This method remains for
// any out-of-tree callers but delegates to the unified resolver.
func (s *Service) GetUserPermissionKeysByUsername(ctx context.Context, username string) ([]string, bool, error) {
	keys, isSuperadmin, _, err := s.ResolveUserCapabilityKeys(ctx, username)
	return keys, isSuperadmin, err
}

// ResolveUserCapabilityKeys returns the full, deduplicated set of capability
// keys granted to a user. It unifies the two authentication modes:
//
//   - Built-in auth: look up users.id → user_permissions → permission_keys.
//   - Forward auth: look up ExternalPermissions.Superadmins and
//     ExternalPermissions.Mapping against the group list on ctx
//     (set by the API userMiddleware from the configured groups header).
//
// The returned source indicates which branch produced the keys and is used
// by CheckPermission to decide enforcement semantics:
//
//   - "builtin"  → progressive restriction (unknown key → allow)
//   - "external" → strict (missing key → deny)
//   - "none"     → nothing configured (allow, preserves zero-config behavior)
//
// Superadmins from either mode receive KnownCapabilityKeys() and isSuperadmin=true.
func (s *Service) ResolveUserCapabilityKeys(
	ctx context.Context,
	username string,
) (keys []string, isSuperadmin bool, source PermissionSource, err error) {
	if username == "" || username == "system" {
		return nil, false, PermissionSourceNone, nil
	}

	// 1. Built-in auth: try the users table first.
	user, userErr := s.store.Users().GetByUsername(ctx, username)
	if userErr == nil {
		if user.IsSuperadmin {
			return KnownCapabilityKeys(), true, PermissionSourceBuiltin, nil
		}
		dbKeys, err := s.store.Permissions().GetUserCapabilityKeys(ctx, user.ID)
		if err != nil {
			return nil, false, PermissionSourceBuiltin, err
		}
		return dbKeys, false, PermissionSourceBuiltin, nil
	}
	if !errors.Is(userErr, ErrNotFound) {
		return nil, false, "", userErr
	}

	// 2. Forward auth: consult ExternalPermissions settings.
	ext := s.GetExternalPermissionsSettings(ctx)
	if ext == nil || !ext.Enabled {
		return nil, false, PermissionSourceNone, nil
	}

	// 2a. Superadmin allowlist — matched by username.
	for _, name := range ext.Superadmins {
		if name == username {
			return KnownCapabilityKeys(), true, PermissionSourceExternal, nil
		}
	}

	// 2b. Translate external group names to capability keys via Mapping.
	groups := ExternalGroupsFromContext(ctx)
	if len(groups) == 0 || len(ext.Mapping) == 0 {
		return nil, false, PermissionSourceExternal, nil
	}

	seen := make(map[string]struct{})
	var mapped []string
	for _, g := range groups {
		mappedKeys, ok := ext.Mapping[g]
		if !ok {
			continue
		}
		for _, k := range mappedKeys {
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			mapped = append(mapped, k)
		}
	}
	return mapped, false, PermissionSourceExternal, nil
}

// CheckPermission checks if a user has a specific capability key.
// Returns nil if allowed, ErrForbidden if denied.
//
// Enforcement semantics depend on the source of the user's keys (see
// ResolveUserCapabilityKeys):
//
//   - Superadmin → always allow.
//   - Source "none" → allow (nothing configured anywhere).
//   - Source "builtin" → progressive restriction: if no Permission row
//     mentions the key, allow; otherwise require the user to have it.
//   - Source "external" → strict: the user must have the key.
func (s *Service) CheckPermission(ctx context.Context, username, permKey string) error {
	keys, isSuperadmin, source, err := s.ResolveUserCapabilityKeys(ctx, username)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if isSuperadmin {
		return nil
	}

	switch source {
	case PermissionSourceNone:
		return nil
	case PermissionSourceBuiltin:
		// Progressive restriction — preserve the pre-existing zero-config
		// behavior where an unreferenced capability key means "unrestricted".
		exists, err := s.store.Permissions().HasCapabilityKey(ctx, permKey)
		if err != nil {
			return fmt.Errorf("permission check failed: %w", err)
		}
		if !exists {
			return nil
		}
	case PermissionSourceExternal:
		// Strict: once external permissions are enabled, a missing key denies.
	}

	for _, k := range keys {
		if k == permKey {
			return nil
		}
	}
	return fmt.Errorf("permission %q required: %w", permKey, ErrForbidden)
}
