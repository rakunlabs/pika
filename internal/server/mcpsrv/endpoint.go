package mcpsrv

import (
	"net/http"
	"strings"

	"github.com/rakunlabs/ada/middleware/auth/identity"
	"github.com/rakunlabs/pika/internal/server/authx"
	"github.com/rakunlabs/pika/internal/service"
)

// Endpoint routes the configured path before the SPA fallback. Read settings
// per request so changes (including replicated changes) take effect immediately.
// locked is checked explicitly because a custom path may be outside /api/v1.
func Endpoint(svc *service.Service, mgr *authx.Manager, basePath, name, version string, locked func() bool) func(http.Handler) http.Handler {
	h := New(svc, name, version)
	protected := mgr.Require()(mgr.CapMiddleware()(h))
	scoped := mgr.CapMiddleware()(h)
	bp := strings.TrimRight(basePath, "/")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := strings.TrimPrefix(r.URL.Path, bp)
			// Reserved application routes cannot be selected as MCP endpoints.
			if !strings.HasPrefix(r.URL.Path, bp+"/") || mcpPathReserved(p) {
				next.ServeHTTP(w, r)
				return
			}
			settings, err := svc.Settings(r.Context())
			if err != nil {
				http.Error(w, "MCP settings unavailable", http.StatusServiceUnavailable)
				return
			}
			cfg := service.EffectiveMCPSettings(settings.MCP)
			if p != cfg.Endpoint {
				if p == service.DefaultMCPEndpoint {
					http.NotFound(w, r)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			if locked != nil && locked() {
				http.Error(w, "server key is locked", http.StatusServiceUnavailable)
				return
			}
			if err := cfg.Validate(); err != nil {
				http.Error(w, "invalid MCP settings", http.StatusServiceUnavailable)
				return
			}
			if !cfg.AuthDisabled {
				protected.ServeHTTP(w, r)
				return
			}
			// X-User is a proxy-supplied audit label only. The apikey provider
			// deliberately bypasses user lookup and superadmin name matching.
			username := strings.TrimSpace(r.Header.Get("X-User"))
			if username == "" {
				username = "mcp-proxy"
			}
			id := &identity.Identity{
				Provider: service.TokenProvider, Subject: username, Name: username,
				Claims: map[string]any{service.TokenScopesClaim: cfg.Scopes},
			}
			scoped.ServeHTTP(w, r.WithContext(identity.WithContext(r.Context(), id)))
		})
	}
}

func mcpPathReserved(p string) bool {
	return (service.MCPSettings{Endpoint: p}).Validate() != nil
}
