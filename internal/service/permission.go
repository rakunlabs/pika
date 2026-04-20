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
// external identities (OAuth2, LDAP, Header) after FindOrCreateExternalUser
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
