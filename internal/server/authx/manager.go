package authx

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth"

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
	m.capResolver = NewCapResolver(m.deps.Svc, s.Capabilities)
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

	m.capResolver = NewCapResolver(m.deps.Svc, s.Capabilities)
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
