package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/rakunlabs/query"
	"golang.org/x/crypto/bcrypt"
)

// User represents an application user stored in the database.
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	Disabled     bool      `json:"disabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// UserInfo is the public representation of a user (no password hash).
type UserInfo struct {
	ID             string    `json:"id"`
	Username       string    `json:"username"`
	Disabled       bool      `json:"disabled"`
	ActiveSessions int64     `json:"active_sessions"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// CreateUserRequest is the request body for creating a new user.
type CreateUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// UpdateUserRequest is the request body for updating a user.
type UpdateUserRequest struct {
	Username *string `json:"username,omitempty"`
	Password *string `json:"password,omitempty"`
	Disabled *bool   `json:"disabled,omitempty"`
}

func (u *User) toInfo() UserInfo {
	return UserInfo{
		ID:        u.ID,
		Username:  u.Username,
		Disabled:  u.Disabled,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

// generateUserID generates a unique user ID.
func generateUserID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreateUser creates a new user with a hashed password.
func (s *Service) CreateUser(ctx context.Context, req *CreateUserRequest) (*UserInfo, error) {
	if req.Username == "" {
		return nil, fmt.Errorf("username is required: %w", ErrBadRequest)
	}
	if req.Password == "" {
		return nil, fmt.Errorf("password is required: %w", ErrBadRequest)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hashing password: %w", err)
	}

	id, err := generateUserID()
	if err != nil {
		return nil, fmt.Errorf("generating user ID: %w", err)
	}

	now := time.Now()
	user := User{
		ID:           id,
		Username:     req.Username,
		PasswordHash: string(hash),
		Disabled:     false,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.store.Users().Create(ctx, &user); err != nil {
		if errors.Is(err, ErrConflict) {
			return nil, fmt.Errorf("username %q already exists: %w", req.Username, ErrConflict)
		}
		return nil, err
	}

	info := user.toInfo()
	return &info, nil
}

// Authenticate verifies a username/password combination.
// Returns the user info on success, or ErrUnauthorized on failure.
func (s *Service) Authenticate(ctx context.Context, username, password string) (*UserInfo, error) {
	user, err := s.store.Users().GetByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("invalid credentials: %w", ErrUnauthorized)
		}
		return nil, err
	}

	if user.Disabled {
		return nil, fmt.Errorf("user is disabled: %w", ErrForbidden)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, fmt.Errorf("invalid credentials: %w", ErrUnauthorized)
	}

	info := user.toInfo()
	return &info, nil
}

// ListUsers returns all users (without password hashes).
// If includeSessions is true, each UserInfo includes the count of active sessions.
func (s *Service) ListUsers(ctx context.Context, q *query.Query) ([]UserInfo, int64, error) {
	users, total, err := s.store.Users().List(ctx, q)
	if err != nil {
		return nil, 0, err
	}

	infos := make([]UserInfo, 0, len(users))
	for _, user := range users {
		info := user.toInfo()
		count, err := s.store.Sessions().CountByUserID(ctx, user.ID)
		if err == nil {
			info.ActiveSessions = count
		}
		infos = append(infos, info)
	}

	return infos, total, nil
}

// GetUser retrieves a user by ID.
func (s *Service) GetUser(ctx context.Context, id string) (*UserInfo, error) {
	user, err := s.store.Users().Get(ctx, id)
	if err != nil {
		return nil, err
	}

	info := user.toInfo()
	return &info, nil
}

// UpdateUser updates a user's properties.
// If the user is being disabled, all their active sessions are deleted.
func (s *Service) UpdateUser(ctx context.Context, id string, req *UpdateUserRequest) error {
	user, err := s.store.Users().Get(ctx, id)
	if err != nil {
		return err
	}

	if req.Username != nil {
		user.Username = *req.Username
	}

	if req.Password != nil {
		hash, err := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hashing password: %w", err)
		}
		user.PasswordHash = string(hash)
	}

	wasDisabled := user.Disabled
	if req.Disabled != nil {
		user.Disabled = *req.Disabled
	}

	user.UpdatedAt = time.Now()

	if err := s.store.Users().Update(ctx, user); err != nil {
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("username %q already exists: %w", user.Username, ErrConflict)
		}
		return err
	}

	// If user was just disabled, delete all their sessions
	if !wasDisabled && user.Disabled {
		_ = s.store.Sessions().DeleteByUserID(ctx, id)
	}

	return nil
}

// KickUser deletes all active sessions for a user, forcing them to re-login.
func (s *Service) KickUser(ctx context.Context, id string) error {
	// Verify user exists
	if _, err := s.store.Users().Get(ctx, id); err != nil {
		return err
	}

	return s.store.Sessions().DeleteByUserID(ctx, id)
}

// DeleteUser deletes a user by ID.
func (s *Service) DeleteUser(ctx context.Context, id string) error {
	return s.store.Users().Delete(ctx, id)
}

// UserCount returns the number of users in the system.
func (s *Service) UserCount(ctx context.Context) (int64, error) {
	return s.store.Users().Count(ctx)
}
