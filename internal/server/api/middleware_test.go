package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/pika/internal/storage/sqlite"
)

// newTestAPI boots a minimal *api with a temp SQLite DB so the userMiddleware
// method has a working svc it can read settings from.
func newTestAPI(t *testing.T) *api {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "pika-test.db")
	dsn := "file:" + dbPath + "?cache=shared"

	store, err := sqlite.New(context.Background(), &sqlite.Config{
		DSN: dsn,
		Migration: sqlite.Migration{
			Enabled: true,
			DSN:     dsn,
		},
	})
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	return &api{svc: service.New(store)}
}

// runMiddleware builds the request, runs userMiddleware, and returns the
// context that was passed to the downstream handler.
func runMiddleware(t *testing.T, a *api, headers map[string][]string) context.Context {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/folder", nil)
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	var captured context.Context
	handler := a.userMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Context()
	}))
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if captured == nil {
		t.Fatal("downstream handler was not invoked")
	}
	return captured
}

func TestUserMiddlewareNoHeader(t *testing.T) {
	a := newTestAPI(t)

	ctx := runMiddleware(t, a, nil)
	if u := service.UserFromContext(ctx); u != "system" {
		t.Errorf("no X-User: want system sentinel, got %q", u)
	}
	if g := service.ExternalGroupsFromContext(ctx); g != nil {
		t.Errorf("no X-User: want nil groups, got %v", g)
	}
}

func TestUserMiddlewareUserOnly(t *testing.T) {
	a := newTestAPI(t)

	ctx := runMiddleware(t, a, map[string][]string{
		"X-User": {"alice"},
	})
	if u := service.UserFromContext(ctx); u != "alice" {
		t.Errorf("want alice, got %q", u)
	}
	if g := service.ExternalGroupsFromContext(ctx); g != nil {
		t.Errorf("external permissions disabled: want nil groups, got %v", g)
	}
}

func TestUserMiddlewareGroupsIgnoredWhenDisabled(t *testing.T) {
	a := newTestAPI(t)

	// ExternalPermissions.Enabled is false: userMiddleware must not populate groups
	// even if the header is present.
	err := a.svc.PatchSettings(context.Background(), &service.PatchSettings{
		Action: service.ActionKeySet,
		ExternalPermissions: &service.ExternalPermissionsSettings{
			Enabled: false,
		},
	})
	if err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}

	ctx := runMiddleware(t, a, map[string][]string{
		"X-User":   {"alice"},
		"X-Groups": {"pika-admin"},
	})
	if g := service.ExternalGroupsFromContext(ctx); g != nil {
		t.Errorf("groups should be ignored when disabled, got %v", g)
	}
}

func TestUserMiddlewareSingleValueCommaSeparated(t *testing.T) {
	a := newTestAPI(t)
	err := a.svc.PatchSettings(context.Background(), &service.PatchSettings{
		Action: service.ActionKeySet,
		ExternalPermissions: &service.ExternalPermissionsSettings{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}

	ctx := runMiddleware(t, a, map[string][]string{
		"X-User":   {"alice"},
		"X-Groups": {"a, b , c"},
	})

	want := []string{"a", "b", "c"}
	got := service.ExternalGroupsFromContext(ctx)
	if !slices.Equal(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestUserMiddlewareMultipleHeaderLines(t *testing.T) {
	a := newTestAPI(t)
	err := a.svc.PatchSettings(context.Background(), &service.PatchSettings{
		Action: service.ActionKeySet,
		ExternalPermissions: &service.ExternalPermissionsSettings{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}

	// Two X-Groups header lines AND comma-separated inside.
	ctx := runMiddleware(t, a, map[string][]string{
		"X-User":   {"alice"},
		"X-Groups": {"a,b", "c", " d "},
	})

	want := []string{"a", "b", "c", "d"}
	got := service.ExternalGroupsFromContext(ctx)
	if !slices.Equal(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestUserMiddlewareCustomHeaderAndSeparator(t *testing.T) {
	a := newTestAPI(t)
	err := a.svc.PatchSettings(context.Background(), &service.PatchSettings{
		Action: service.ActionKeySet,
		ExternalPermissions: &service.ExternalPermissionsSettings{
			Enabled:         true,
			GroupsHeader:    "X-Pika-Roles",
			GroupsSeparator: "|",
		},
	})
	if err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}

	ctx := runMiddleware(t, a, map[string][]string{
		"X-User":       {"alice"},
		"X-Pika-Roles": {"admin|editor|viewer"},
		"X-Groups":     {"should-be-ignored"}, // default header, ignored because custom is set
	})

	want := []string{"admin", "editor", "viewer"}
	got := service.ExternalGroupsFromContext(ctx)
	if !slices.Equal(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestUserMiddlewareEmptyGroupsValues(t *testing.T) {
	a := newTestAPI(t)
	err := a.svc.PatchSettings(context.Background(), &service.PatchSettings{
		Action: service.ActionKeySet,
		ExternalPermissions: &service.ExternalPermissionsSettings{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}

	ctx := runMiddleware(t, a, map[string][]string{
		"X-User":   {"alice"},
		"X-Groups": {" , , a , "},
	})

	want := []string{"a"}
	got := service.ExternalGroupsFromContext(ctx)
	if !slices.Equal(got, want) {
		t.Errorf("empty values should be stripped: want %v, got %v", want, got)
	}
}

func TestUserMiddlewareNoXUserIgnoresGroups(t *testing.T) {
	a := newTestAPI(t)
	err := a.svc.PatchSettings(context.Background(), &service.PatchSettings{
		Action: service.ActionKeySet,
		ExternalPermissions: &service.ExternalPermissionsSettings{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}

	// Without X-User, userMiddleware should not populate groups either —
	// the whole auth context hinges on the presence of a user.
	ctx := runMiddleware(t, a, map[string][]string{
		"X-Groups": {"admins"},
	})
	if g := service.ExternalGroupsFromContext(ctx); g != nil {
		t.Errorf("want nil groups when X-User is missing, got %v", g)
	}
}

// TestPermissionsEnforcedDynamic verifies that the permissionsEnforced
// predicate reads settings live so PatchSettings hot-reload takes effect.
func TestPermissionsEnforcedDynamic(t *testing.T) {
	a := newTestAPI(t)
	ctx := context.Background()

	if a.permissionsEnforced(ctx) {
		t.Error("no auth mode configured: should not enforce")
	}

	// Enable external permissions: should enforce now.
	err := a.svc.PatchSettings(ctx, &service.PatchSettings{
		Action: service.ActionKeySet,
		ExternalPermissions: &service.ExternalPermissionsSettings{
			Enabled: true,
		},
	})
	if err != nil {
		t.Fatalf("PatchSettings enable: %v", err)
	}
	if !a.permissionsEnforced(ctx) {
		t.Error("external permissions enabled: should enforce")
	}

	// Disable again: back to no enforcement.
	err = a.svc.PatchSettings(ctx, &service.PatchSettings{
		Action: service.ActionKeySet,
		ExternalPermissions: &service.ExternalPermissionsSettings{
			Enabled: false,
		},
	})
	if err != nil {
		t.Fatalf("PatchSettings disable: %v", err)
	}
	if a.permissionsEnforced(ctx) {
		t.Error("external permissions disabled: should not enforce")
	}
}
