package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
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
func (s *Service) GetUserPermissionKeysByUsername(ctx context.Context, username string) ([]string, bool, error) {
	user, err := s.store.Users().GetByUsername(ctx, username)
	if err != nil {
		return nil, false, err
	}

	if user.IsSuperadmin {
		return nil, true, nil
	}

	keys, err := s.store.Permissions().GetUserCapabilityKeys(ctx, user.ID)
	if err != nil {
		return nil, false, err
	}

	return keys, false, nil
}

// CheckPermission checks if a user has a specific permission.
// Returns nil if allowed, ErrForbidden if denied.
//
// Logic:
//  1. If user is superadmin → allow
//  2. If permission key doesn't exist in DB → allow (not restricted)
//  3. If user has the permission → allow
//  4. Otherwise → ErrForbidden
func (s *Service) CheckPermission(ctx context.Context, username string, permKey string) error {
	user, err := s.store.Users().GetByUsername(ctx, username)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}

	// Superadmin bypasses all checks
	if user.IsSuperadmin {
		return nil
	}

	// If the permission key hasn't been created, the feature is unrestricted
	exists, err := s.store.Permissions().HasCapabilityKey(ctx, permKey)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if !exists {
		return nil
	}

	// Check if user has the permission
	has, err := s.store.Permissions().UserHasCapability(ctx, user.ID, permKey)
	if err != nil {
		return fmt.Errorf("permission check failed: %w", err)
	}
	if !has {
		return fmt.Errorf("permission %q required: %w", permKey, ErrForbidden)
	}

	return nil
}
