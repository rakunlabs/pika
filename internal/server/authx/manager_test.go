package authx

import (
	"context"
	"testing"

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
