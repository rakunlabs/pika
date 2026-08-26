package authx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/ada/middleware/auth/identity"

	"github.com/rakunlabs/pika/internal/service"
)

func TestManager_BootWithLocal(t *testing.T) {
	m := newTestManager(t)
	if err := m.Boot(context.Background(), &service.AuthSettings{
		Local: &service.LocalStrategySettings{Enabled: true},
	}); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	// Descriptors() skips hidden strategies (apikey is hidden), so the
	// only visible descriptor is the local one.
	if got := len(m.authMW.Registry().Descriptors()); got != 1 {
		t.Errorf("expected 1 visible strategy descriptor, got %d", got)
	}
}

func TestManager_ReloadSwapsStrategies(t *testing.T) {
	m := newTestManager(t)
	if err := m.Boot(context.Background(), &service.AuthSettings{
		Local: &service.LocalStrategySettings{Enabled: true},
	}); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if err := m.Reload(context.Background(), &service.AuthSettings{
		Header: &service.HeaderStrategySettings{Name: "header"},
	}); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// After reload:
	//   - local is gone (not configured in the new settings)
	//   - header is present (configured)
	//   - apikey is always present (hardcoded in authx.buildStrategies —
	//     pika always validates Authorization: Bearer tokens from the
	//     Access Tokens table)
	all := m.authMW.Registry().List()
	names := make([]string, 0, len(all))
	for _, s := range all {
		names = append(names, s.Name())
	}

	wantPresent := map[string]bool{"header": false, "apikey": false}
	for _, n := range names {
		if _, ok := wantPresent[n]; ok {
			wantPresent[n] = true
		}
	}
	for n, ok := range wantPresent {
		if !ok {
			t.Errorf("expected strategy %q after reload, got %v", n, names)
		}
	}
	for _, n := range names {
		if n == "local" {
			t.Errorf("local strategy should be gone after reload, got %v", names)
		}
	}
}

func TestManager_CapMiddlewareUsesReloadedResolver(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	if err := m.Boot(ctx, &service.AuthSettings{
		Local: &service.LocalStrategySettings{Enabled: true},
	}); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if _, err := m.deps.Svc.CreatePermission(ctx, &service.CreatePermissionRequest{
		Key:  "editor",
		Name: "Editor",
		Keys: []string{service.CapFilesWrite},
	}); err != nil {
		t.Fatalf("create permission: %v", err)
	}

	// The server mounts this handler once at boot, before later settings reloads.
	var got service.Capabilities
	h := m.CapMiddleware()(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		got = service.CapabilitiesFromContext(req.Context())
	}))

	if err := m.Reload(ctx, &service.AuthSettings{
		Local: &service.LocalStrategySettings{Enabled: true},
		Capabilities: service.CapabilityMapping{
			RoleMapping: map[string][]string{"pika-editor": {"editor"}},
		},
	}); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(identity.WithContext(req.Context(), &identity.Identity{
		Subject:  "alice",
		Provider: "oauth2",
		Roles:    []string{"pika-editor"},
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !got.Has(service.CapFilesWrite) {
		t.Fatalf("reloaded role mapping was not applied: %v", got)
	}
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	svc := newTestService(t)
	deps := Deps{
		Svc:          svc,
		SessionStore: NewSessionStore(svc, "pika_session"),
		BasePath:     "/api/v1/",
		CookieName:   "pika_session",
	}
	return New(deps)
}
