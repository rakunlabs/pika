package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/rakunlabs/query"
)

// TokenScope defines what a token can access.
type TokenScope struct {
	// Path is a glob pattern for config paths (e.g., "app/*", "production/**").
	Path string `json:"path"`
	// Operations is a list of allowed operations: "read", "write", "delete".
	Operations []string `json:"operations"`
}

// Token represents an access token stored in the system.
type Token struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	HashedKey string       `json:"hashed_key"` // SHA-256 hash of the raw token
	Scopes    []TokenScope `json:"scopes"`
	CreatedAt time.Time    `json:"created_at"`
	CreatedBy string       `json:"created_by"`
	ExpiresAt *time.Time   `json:"expires_at,omitempty"`
	Active    bool         `json:"active"`
}

// TokenInfo is the public representation of a token (no hash exposed).
type TokenInfo struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Scopes    []TokenScope `json:"scopes"`
	CreatedAt time.Time    `json:"created_at"`
	CreatedBy string       `json:"created_by"`
	ExpiresAt *time.Time   `json:"expires_at,omitempty"`
	Active    bool         `json:"active"`
}

// CreateTokenRequest is the request body for creating a new token.
type CreateTokenRequest struct {
	Name      string       `json:"name"`
	Scopes    []TokenScope `json:"scopes"`
	ExpiresAt *time.Time   `json:"expires_at,omitempty"`
}

// CreateTokenResponse is returned when a token is created.
// The RawKey is only shown once.
type CreateTokenResponse struct {
	TokenInfo
	RawKey string `json:"raw_key"`
}

// PatchTokenRequest is the request body for updating a token.
type PatchTokenRequest struct {
	Name      *string      `json:"name,omitempty"`
	Scopes    []TokenScope `json:"scopes,omitempty"`
	Active    *bool        `json:"active,omitempty"`
	ExpiresAt *time.Time   `json:"expires_at,omitempty"`
}

// generateTokenID generates a short unique ID for a token.
func generateTokenID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// generateRawKey generates a random raw token key.
func generateRawKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "pika_" + hex.EncodeToString(b), nil
}

// hashKey hashes a raw token key using SHA-256.
func hashKey(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// CreateToken creates a new access token.
func (s *Service) CreateToken(ctx context.Context, req *CreateTokenRequest) (*CreateTokenResponse, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("token name is required: %w", ErrBadRequest)
	}
	if len(req.Scopes) == 0 {
		return nil, fmt.Errorf("at least one scope is required: %w", ErrBadRequest)
	}

	id, err := generateTokenID()
	if err != nil {
		return nil, fmt.Errorf("generating token ID: %w", err)
	}

	rawKey, err := generateRawKey()
	if err != nil {
		return nil, fmt.Errorf("generating token key: %w", err)
	}

	token := Token{
		ID:        id,
		Name:      req.Name,
		HashedKey: hashKey(rawKey),
		Scopes:    req.Scopes,
		CreatedAt: time.Now(),
		CreatedBy: AuditUserFromContext(ctx),
		ExpiresAt: req.ExpiresAt,
		Active:    true,
	}

	if err := s.store.Tokens().Create(ctx, &token); err != nil {
		return nil, err
	}

	return &CreateTokenResponse{
		TokenInfo: TokenInfo{
			ID:        token.ID,
			Name:      token.Name,
			Scopes:    token.Scopes,
			CreatedAt: token.CreatedAt,
			CreatedBy: token.CreatedBy,
			ExpiresAt: token.ExpiresAt,
			Active:    token.Active,
		},
		RawKey: rawKey,
	}, nil
}

// ListTokens lists all tokens (without exposing the hashed key).
func (s *Service) ListTokens(ctx context.Context, q *query.Query) ([]TokenInfo, int64, error) {
	tokens, total, err := s.store.Tokens().List(ctx, q)
	if err != nil {
		return nil, 0, err
	}

	infos := make([]TokenInfo, 0, len(tokens))
	for _, token := range tokens {
		infos = append(infos, TokenInfo{
			ID:        token.ID,
			Name:      token.Name,
			Scopes:    token.Scopes,
			CreatedAt: token.CreatedAt,
			CreatedBy: token.CreatedBy,
			ExpiresAt: token.ExpiresAt,
			Active:    token.Active,
		})
	}

	return infos, total, nil
}

// DeleteToken deletes a token by ID.
func (s *Service) DeleteToken(ctx context.Context, id string) error {
	return s.store.Tokens().Delete(ctx, id)
}

// PatchToken updates a token's properties.
func (s *Service) PatchToken(ctx context.Context, id string, req *PatchTokenRequest) error {
	token, err := s.store.Tokens().Get(ctx, id)
	if err != nil {
		return err
	}

	if req.Name != nil {
		token.Name = *req.Name
	}
	if req.Scopes != nil {
		token.Scopes = req.Scopes
	}
	if req.Active != nil {
		token.Active = *req.Active
	}
	if req.ExpiresAt != nil {
		token.ExpiresAt = req.ExpiresAt
	}

	return s.store.Tokens().Update(ctx, token)
}

// ValidateToken validates a raw token key and checks if it has permission
// for the given config path and operation.
func (s *Service) ValidateToken(ctx context.Context, rawKey string, configPath string, operation string) error {
	hashed := hashKey(rawKey)

	matchedToken, err := s.store.Tokens().FindByHash(ctx, hashed)
	if err != nil {
		return fmt.Errorf("invalid token: %w", ErrUnauthorized)
	}

	if !matchedToken.Active {
		return fmt.Errorf("token is disabled: %w", ErrForbidden)
	}

	if matchedToken.ExpiresAt != nil && time.Now().After(*matchedToken.ExpiresAt) {
		return fmt.Errorf("token has expired: %w", ErrForbidden)
	}

	// Check scopes
	for _, scope := range matchedToken.Scopes {
		if matchPath(scope.Path, configPath) && containsOperation(scope.Operations, operation) {
			return nil // Authorized
		}
	}

	return fmt.Errorf("token does not have permission for %s on %q: %w", operation, configPath, ErrForbidden)
}

// matchPath checks if a config path matches a scope path pattern.
// Supports:
//   - "*" matches any single path segment
//   - "**" matches any number of path segments
//   - exact matches
func matchPath(pattern, path string) bool {
	if pattern == "*" || pattern == "**" {
		return true
	}

	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	pathParts := strings.Split(strings.Trim(path, "/"), "/")

	return matchPathParts(patternParts, pathParts)
}

func matchPathParts(pattern, path []string) bool {
	pi, pp := 0, 0

	for pi < len(pattern) && pp < len(path) {
		if pattern[pi] == "**" {
			// "**" can match zero or more segments
			if pi == len(pattern)-1 {
				return true // "**" at end matches everything
			}
			// Try matching remaining pattern at each position
			for i := pp; i <= len(path); i++ {
				if matchPathParts(pattern[pi+1:], path[i:]) {
					return true
				}
			}
			return false
		}

		if pattern[pi] == "*" {
			// "*" matches exactly one segment
			pi++
			pp++
			continue
		}

		if pattern[pi] != path[pp] {
			return false
		}

		pi++
		pp++
	}

	// Check remaining pattern parts are all "**"
	for pi < len(pattern) {
		if pattern[pi] != "**" {
			return false
		}
		pi++
	}

	return pp == len(path)
}

func containsOperation(ops []string, op string) bool {
	for _, o := range ops {
		if o == op || o == "*" {
			return true
		}
	}
	return false
}

// findTokenByHash looks up a token by its hashed key.
func (s *Service) findTokenByHash(ctx context.Context, hashed string) (*Token, error) {
	return s.store.Tokens().FindByHash(ctx, hashed)
}
