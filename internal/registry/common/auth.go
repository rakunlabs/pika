// Package common holds glue helpers shared across every registry
// protocol head (Go, NPM, Docker). Auth extraction, ETag handling,
// CORS, and the singleflight wrapper used by metadata caches.
//
// Why a shared package
//
// The three protocols look like three different worlds on the wire,
// but at pika's edge they all reduce to the same questions:
//
//   - "Did the caller present a valid pika token?"
//   - "Should I serve a cached metadata document or rebuild it?"
//   - "Should this response be cached by the client?"
//
// Each is one helper here, used by all three. The package
// intentionally has no external dependencies beyond the service
// package so a protocol head can import it without dragging the
// world.
package common

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/rakunlabs/pika/internal/service"
)

// TokenValidator is the narrow surface common needs from
// *service.Service. Defining it as an interface lets tests stub the
// validation without spinning up the full service tree.
type TokenValidator interface {
	// ValidateToken validates a raw token against the named scope
	// and operation. Returns nil on success; wraps
	// service.ErrUnauthorized / ErrForbidden / ErrNotFound on
	// failure.
	ValidateToken(ctx context.Context, raw, scope, op string) error
}

// Operation values passed to ValidateToken's `op` parameter. The
// service package uses these to enforce write/delete grants
// independently from read.
const (
	OpRead   = "read"
	OpWrite  = "write"
	OpDelete = "delete"
)

// ExtractToken pulls a pika token out of an incoming registry
// request. Three header shapes are accepted, in this order:
//
//  1. `Authorization: Bearer <token>` — the canonical pika form, used
//     directly by Go (.netrc Basic translates to this when set to
//     the literal "Bearer" username; npm CLI; cosign; ORAS).
//  2. `Authorization: Basic <base64 user:token>` — npm's `_authToken`
//     in `.npmrc` lands as Basic, and Go's `.netrc` Basic auth too.
//     The token is read from the password slot; the username is
//     ignored (pika tokens are self-identifying).
//  3. `X-Pika-Token: <token>` — escape hatch for clients that
//     can't set Authorization (rare; included for completeness).
//
// Returns the raw token text (without any "Bearer " prefix) or "" if
// none of the headers are present. The function does NOT validate
// the token's bytes — that's ValidateToken's job.
func ExtractToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	if tok := r.Header.Get("X-Pika-Token"); tok != "" {
		return tok
	}
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(auth[len("Bearer "):])
	}
	if strings.HasPrefix(auth, "Basic ") {
		_, password, ok := r.BasicAuth()
		if !ok {
			return ""
		}
		return password
	}
	return ""
}

// RequireToken extracts and validates a pika token in one step.
// The scope is the registry-specific path (e.g.
// "registry/{ns}/{repo}/...") that token scopes are matched against;
// op is OpRead/OpWrite/OpDelete.
//
// Returns nil on success. On failure returns a wrapped
// service.ErrUnauthorized / ErrForbidden so the HTTP layer maps to
// the right status code automatically.
func RequireToken(ctx context.Context, v TokenValidator, r *http.Request, scope, op string) error {
	tok := ExtractToken(r)
	if tok == "" {
		return fmt.Errorf("missing pika token: %w", service.ErrUnauthorized)
	}
	if err := v.ValidateToken(ctx, tok, scope, op); err != nil {
		return err
	}
	return nil
}

// WriteUnauthorized writes a 401 response with a JSON body. Used by
// the entry handler when authentication fails before dispatch.
//
// Docker has its own challenge shape (WWW-Authenticate: Bearer
// realm=...) — the Docker registry head writes that itself rather
// than going through this helper. Other protocols just need a 401
// + message.
func WriteUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = fmt.Fprintf(w, `{"message":%q}`, message)
}

// WriteForbidden writes a 403 response with a JSON body.
func WriteForbidden(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = fmt.Fprintf(w, `{"message":%q}`, message)
}

// MapAuthError translates a TokenValidator error into an HTTP write
// suitable for any registry head's entry path. Returns true when an
// error response was written, false when err was nil.
func MapAuthError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, service.ErrUnauthorized):
		WriteUnauthorized(w, err.Error())
	case errors.Is(err, service.ErrForbidden):
		WriteForbidden(w, err.Error())
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, `{"message":%q}`, err.Error())
	}
	return true
}
