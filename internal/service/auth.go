package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/identity"
	"github.com/rakunlabs/query"
	"golang.org/x/crypto/bcrypt"
)

// dummyHash is a precomputed bcrypt hash used to equalize the time taken by
// Authenticate when a username does not exist. Without this, an attacker
// can probe valid usernames by measuring response latency: a real user
// triggers a bcrypt compare (~100ms), a non-existent user returns
// immediately. Running CompareHashAndPassword against this throwaway hash
// in the not-found path closes that timing channel.
//
// Initialized lazily on first use to avoid paying the bcrypt cost during
// package init when callers may never authenticate (e.g. CLI tools).
var dummyHash []byte

func ensureDummyHash() {
	if dummyHash != nil {
		return
	}
	h, err := bcrypt.GenerateFromPassword([]byte("not-a-real-password"), bcrypt.DefaultCost)
	if err != nil {
		// Should never happen; fall back to a fixed bcrypt-shaped value
		// so CompareHashAndPassword still consumes time on each call.
		dummyHash = []byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinvalidinvali")
		return
	}
	dummyHash = h
}

// User represents an application user stored in the database. A user may
// have zero or many linked external identities (OAuth2, LDAP, Header) and
// optionally a local password. External-only users have External=true and
// an empty PasswordHash — they can never be authenticated via local.
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	Email        string    `json:"email"`
	DisplayName  string    `json:"display_name"`
	External     bool      `json:"external"`
	Disabled     bool      `json:"disabled"`
	IsSuperadmin bool      `json:"is_superadmin"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// UserInfo is the public representation of a user (no password hash).
type UserInfo struct {
	ID             string    `json:"id"`
	Username       string    `json:"username"`
	Email          string    `json:"email,omitempty"`
	DisplayName    string    `json:"display_name,omitempty"`
	External       bool      `json:"external,omitempty"`
	Disabled       bool      `json:"disabled"`
	IsSuperadmin   bool      `json:"is_superadmin"`
	ActiveSessions int64     `json:"active_sessions"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// CreateUserRequest is the request body for creating a new user.
type CreateUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// UpdateUserRequest is the request body for updating a user. Only
// non-nil fields are applied; omitted fields keep their existing value.
type UpdateUserRequest struct {
	Username    *string `json:"username,omitempty"`
	Password    *string `json:"password,omitempty"`
	Email       *string `json:"email,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Disabled    *bool   `json:"disabled,omitempty"`
}

func (u *User) toInfo() UserInfo {
	return UserInfo{
		ID:           u.ID,
		Username:     u.Username,
		Email:        u.Email,
		DisplayName:  u.DisplayName,
		External:     u.External,
		Disabled:     u.Disabled,
		IsSuperadmin: u.IsSuperadmin,
		CreatedAt:    u.CreatedAt,
		UpdatedAt:    u.UpdatedAt,
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
	return s.createUser(ctx, req, false)
}

// CreateSetupUser creates the first admin user with superadmin privileges.
func (s *Service) CreateSetupUser(ctx context.Context, req *CreateUserRequest) (*UserInfo, error) {
	return s.createUser(ctx, req, true)
}

func (s *Service) createUser(ctx context.Context, req *CreateUserRequest, superadmin bool) (*UserInfo, error) {
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
		IsSuperadmin: superadmin,
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
//
// On a username-not-found, this still runs bcrypt against a throwaway hash
// so the response time is comparable to a real user with a wrong password.
// Without this, an attacker can probe for valid usernames by measuring
// latency.
func (s *Service) Authenticate(ctx context.Context, username, password string) (*UserInfo, error) {
	user, err := s.store.Users().GetByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			ensureDummyHash()
			// Result intentionally ignored — we just want the
			// CPU work to happen so timings match.
			_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
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

	slog.Info("auth: login success", "username", user.Username)

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

// GetUserByUsername retrieves a user by username. Used by the auth layer to
// resolve an identity's subject (username) to its stable user_id so
// session rows can be targeted by admin flows (kick, disable).
func (s *Service) GetUserByUsername(ctx context.Context, username string) (*UserInfo, error) {
	user, err := s.store.Users().GetByUsername(ctx, username)
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

	if req.Email != nil {
		// Normalize with the same rules the provisioner uses so
		// auto-linking against this row remains consistent.
		user.Email = normalizeEmail(*req.Email)
	}
	if req.DisplayName != nil {
		user.DisplayName = *req.DisplayName
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

// EnsureSuperadmin checks if at least one superadmin exists.
// If none do (e.g. after a migration from an older version), the earliest
// created user is promoted. This prevents lockout on upgrade.
func (s *Service) EnsureSuperadmin(ctx context.Context) error {
	users, _, err := s.store.Users().List(ctx, nil)
	if err != nil {
		return err
	}

	if len(users) == 0 {
		return nil // no users yet, setup will handle it
	}

	// Check if any superadmin exists
	for _, u := range users {
		if u.IsSuperadmin {
			return nil // at least one exists
		}
	}

	// Promote the earliest user
	earliest := users[0]
	for _, u := range users[1:] {
		if u.CreatedAt.Before(earliest.CreatedAt) {
			earliest = u
		}
	}

	earliest.IsSuperadmin = true
	earliest.UpdatedAt = time.Now()

	return s.store.Users().Update(ctx, &earliest)
}

// VerifyIdentity validates username/password and returns an *identity.Identity
// populated from the DB user. Returns ErrUnauthorized on bad credentials.
func (s *Service) VerifyIdentity(ctx context.Context, username, password string) (*identity.Identity, error) {
	info, err := s.Authenticate(ctx, username, password)
	if err != nil {
		return nil, err
	}
	id := &identity.Identity{
		Subject:  info.Username,
		Name:     info.Username,
		Provider: "local",
		IssuedAt: time.Now(),
	}
	if info.IsSuperadmin {
		id.Roles = []string{"superadmin"}
	}
	return id, nil
}

// BootstrapFirstUser creates the initial superadmin user. Returns ErrConflict
// when a user already exists (serves as the concurrent-setup race guard).
func (s *Service) BootstrapFirstUser(ctx context.Context, username, password string) (*identity.Identity, error) {
	count, err := s.UserCount(ctx)
	if err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, ErrConflict
	}
	info, err := s.CreateSetupUser(ctx, &CreateUserRequest{Username: username, Password: password})
	if err != nil {
		return nil, err
	}
	return &identity.Identity{
		Subject:  info.Username,
		Name:     info.Username,
		Provider: "local",
		Roles:    []string{"superadmin"},
		IssuedAt: time.Now(),
	}, nil
}
