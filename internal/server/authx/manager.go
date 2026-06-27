package authx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth"
	"github.com/rakunlabs/ada/middleware/auth/identity"

	"github.com/rakunlabs/pika/internal/service"
)

// Manager owns the *auth.Auth and the capability resolver. Hot-reloadable via
// Reload; constructed once at boot via Boot.
type Manager struct {
	deps Deps

	mu          sync.Mutex
	authMW      *auth.Auth
	capResolver *CapResolver

	// bootstrapped is set to 1 once a successful registration is observed
	// (even via cluster forward). This prevents a race in clustered mode
	// where the follower's local DB hasn't synced yet but the leader has
	// already created the first user — without this flag the follower
	// would keep reporting signup_first=true until sync completes.
	bootstrapped atomic.Int32
}

// New constructs a Manager from the given Deps.
func New(deps Deps) *Manager { return &Manager{deps: deps} }

// Boot constructs the auth middleware with the initial settings and calls
// Init(). Must be called exactly once before Require()/Mount().
func (m *Manager) Boot(ctx context.Context, s *service.AuthSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Apply effective defaults so that a DB row with partial auth config
	// (e.g. auth.{} with no local block) still boots the default local
	// strategy. Keeps the runtime consistent with what GET /api/v1/settings
	// reports to the UI.
	s = s.WithEffectiveDefaults()

	a := auth.New(buildAuthConfig(s, m.deps.BasePath, m.deps.Version, m.signupFirstFn()))
	a.WithSessionStore(m.deps.SessionStore)

	strats, err := buildStrategies(m.deps, s, m.MarkBootstrapped)
	if err != nil {
		return fmt.Errorf("build strategies: %w", err)
	}
	for _, st := range strats {
		a.Strategy(st)
	}

	if err := a.Init(ctx); err != nil {
		return fmt.Errorf("auth init: %w", err)
	}

	m.authMW = a
	m.capResolver = NewCapResolver(m.deps.Svc, s.Capabilities, s.OAuth2RolesClaims())
	return nil
}

// Reload swaps the strategies and UI config without disturbing in-flight
// requests. Cookie/issuer/session-store changes require a restart.
func (m *Manager) Reload(ctx context.Context, s *service.AuthSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.authMW == nil {
		return fmt.Errorf("manager not booted")
	}
	// Same default-filling as Boot so a Reload with a partial settings
	// object still yields a working local strategy.
	s = s.WithEffectiveDefaults()

	strats, err := buildStrategies(m.deps, s, m.MarkBootstrapped)
	if err != nil {
		return fmt.Errorf("build strategies: %w", err)
	}
	m.authMW.Registry().Replace(strats...)
	m.authMW.SetUI(auth.UIConfig{
		Title:    s.UI.Title,
		Subtitle: s.UI.Subtitle,
		Icon:     s.UI.Icon,
		// Version is sourced from the build (Deps.Version), not from
		// editable settings, so reload cannot change it.
		Version:       m.deps.Version,
		Theme:         s.UI.Theme,
		CustomCSSURL:  s.UI.CustomCSSURL,
		SignupFirstFn: m.signupFirstFn(),
	})

	m.capResolver = NewCapResolver(m.deps.Svc, s.Capabilities, s.OAuth2RolesClaims())
	return nil
}

// Require returns auth.Auth.Require() for wiring into server middleware.
func (m *Manager) Require() func(next http.Handler) http.Handler {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.authMW.Require()
}

// CapMiddleware returns the current capability-resolver middleware.
func (m *Manager) CapMiddleware() ada.MiddlewareFunc {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.capResolver.Middleware()
}

// ResolveRequest attempts to identify the caller without requiring the
// Require()/CapMiddleware() chain. Returns the identity (or nil), the
// capability keys, the resolved pika username, the pika user ID, and any
// per-key path patterns. Used by unprotected endpoints (e.g. /api/v1/info)
// that still need to know who the caller is.
//
// Returns nil identity (and zero-valued fields) when the request carries no
// valid session — callers should treat that as "anonymous".
func (m *Manager) ResolveRequest(r *http.Request) (*identity.Identity, []string, string, string, map[string][]string) {
	m.mu.Lock()
	authMW := m.authMW
	resolver := m.capResolver
	m.mu.Unlock()

	if authMW == nil || resolver == nil {
		return nil, nil, "", "", nil
	}

	// Prefer an identity already attached to the context (set by the
	// CapMiddleware on the protected mux).
	id := identity.FromContext(r.Context())
	if id == nil {
		// Fall back to manual session resolution — same path /login/me uses
		// when Require() isn't in the chain (see ada auth.go:629).
		sid := authMW.Session().CurrentSessionID(r)
		if sid == "" {
			return nil, nil, "", "", nil
		}
		pair, err := authMW.Issuer().Resolve(r.Context(), sid)
		if err != nil || pair == nil || pair.Identity == nil {
			return nil, nil, "", "", nil
		}
		id = pair.Identity
	}

	caps, username, userID, patterns := resolver.resolve(r.Context(), id)
	return id, []string(caps), username, userID, patterns
}

// EffectiveForUser resolves a target user's effective capabilities for admin
// inspection. It runs the SAME resolver the request hot path uses, sourced
// from the user's most-recent active session (so IdP roles frozen in the
// session participate), and falls back to a DB-only identity when the user is
// offline (showing assigned bundles + superadmin + deny, with no IdP roles).
func (m *Manager) EffectiveForUser(ctx context.Context, userID string) (*EffectiveReport, error) {
	m.mu.Lock()
	resolver := m.capResolver
	m.mu.Unlock()
	if resolver == nil {
		return nil, fmt.Errorf("manager not booted")
	}

	user, err := m.deps.Svc.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	rows, err := m.deps.Svc.ListRawSessionsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	var id *identity.Identity
	online := false
	for _, row := range rows { // newest-first; first reconstructable wins
		var values map[string]any
		if err := json.Unmarshal(row.Payload, &values); err != nil {
			continue
		}
		if rid := reconstructIdentity(values); rid != nil {
			id = rid
			online = true
			break
		}
	}
	if id == nil {
		// Offline: synthesize a DB-only identity. Subject=username so an
		// allowlist superadmin match by username still resolves; the
		// stamped user_id claim lets the resolver load DB bundles,
		// is_superadmin and the deny overlay. IdP roles are unknown.
		id = &identity.Identity{Subject: user.Username}
	}

	// We already know the target user, so pin the resolver's user_id claim
	// to it. This is correct (every listed session belongs to userID) and
	// robust against sessions whose login-time user_id stamping failed.
	if id.Claims == nil {
		id.Claims = map[string]any{}
	}
	id.Claims[PikaUserIDClaim] = userID

	rep := resolver.resolveDetailed(ctx, id)
	rep.Online = online
	return rep, nil
}

// SessionView is the admin-safe projection of one active session. It never
// carries the raw session ID — that value IS the live session cookie secret
// (see SessionStore.Save) — only a stable hash handle for display/revocation.
type SessionView struct {
	Handle    string    `json:"handle"`
	Provider  string    `json:"provider,omitempty"`
	Subject   string    `json:"subject,omitempty"`
	Current   bool      `json:"current"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// sessionHandle derives the public, non-reversible handle for a session ID.
func sessionHandle(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

// ListUserSessions returns admin-safe views of a user's active sessions. The
// caller's own session (when it belongs to userID) is flagged Current so the
// UI can label it and warn before self-revocation.
func (m *Manager) ListUserSessions(ctx context.Context, userID string, r *http.Request) ([]SessionView, error) {
	rows, err := m.deps.Svc.ListRawSessionsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	currentID := m.currentSessionID(r)
	out := make([]SessionView, 0, len(rows))
	for _, row := range rows {
		view := SessionView{
			Handle:    sessionHandle(row.ID),
			Current:   currentID != "" && row.ID == currentID,
			CreatedAt: row.CreatedAt,
			ExpiresAt: row.ExpiresAt,
		}
		var values map[string]any
		if err := json.Unmarshal(row.Payload, &values); err == nil {
			if id := reconstructIdentity(values); id != nil {
				view.Provider = id.Provider
				view.Subject = id.Subject
			}
		}
		out = append(out, view)
	}
	return out, nil
}

// RevokeUserSession deletes a single session of userID identified by its hash
// handle. The raw session ID never leaves the server: we recompute the handle
// over the user's own sessions and match. Returns ErrNotFound when nothing
// matches (already expired/revoked, or a handle for a different user).
func (m *Manager) RevokeUserSession(ctx context.Context, userID, handle string) error {
	rows, err := m.deps.Svc.ListRawSessionsByUser(ctx, userID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if sessionHandle(row.ID) == handle {
			return m.deps.Svc.DeleteRawSession(ctx, row.ID)
		}
	}
	return service.ErrNotFound
}

// currentSessionID returns the calling request's own session ID (raw, never
// exposed) so ListUserSessions can flag the "this is you" row.
func (m *Manager) currentSessionID(r *http.Request) string {
	if r == nil {
		return ""
	}
	m.mu.Lock()
	authMW := m.authMW
	m.mu.Unlock()
	if authMW == nil {
		return ""
	}
	return authMW.Session().CurrentSessionID(r)
}

// Mount registers /login/* and /logout on the given mux.
func (m *Manager) Mount(mux auth.Mux) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.authMW.Mount(mux)
}

func (m *Manager) signupFirstFn() func() bool {
	return func() bool {
		if m.bootstrapped.Load() != 0 {
			return false
		}
		count, err := m.deps.Svc.UserCount(context.Background())
		if err == nil && count > 0 {
			m.bootstrapped.Store(1)
			return false
		}
		return err == nil && count == 0
	}
}

// MarkBootstrapped signals that at least one user has been created (e.g.
// via a forwarded registration that succeeded on the leader). Subsequent
// calls to signupFirstFn will immediately return false even before the
// local DB sync catches up.
func (m *Manager) MarkBootstrapped() {
	m.bootstrapped.Store(1)
}
