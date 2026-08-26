package service_test

import (
	"testing"

	"github.com/rakunlabs/pika/internal/service"
)

// saveAuth is a tiny helper that pushes an AuthSettings block through the same
// PatchSettings entry point the HTTP handler uses.
func saveAuth(t *testing.T, svc *service.Service, auth *service.AuthSettings) {
	t.Helper()
	if err := svc.PatchSettings(t.Context(), &service.PatchSettings{
		Action: service.ActionKeySet,
		Auth:   auth,
	}); err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}
}

func oauth2Secret(t *testing.T, svc *service.Service, name string) string {
	t.Helper()
	got, err := svc.Settings(t.Context())
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.Auth == nil {
		t.Fatal("Auth nil")
	}
	for _, e := range got.Auth.OAuth2 {
		if e.Name == name {
			return e.ClientSecret
		}
	}
	t.Fatalf("oauth2 provider %q not found", name)
	return ""
}

// TestOAuth2SecretBlankKeeps locks in the "leave blank to keep" contract: a
// save that omits the client secret must preserve the stored one instead of
// wiping it (the bug that surfaced as the IdP rejecting an empty secret).
func TestOAuth2SecretBlankKeeps(t *testing.T) {
	svc := newTestService(t)

	saveAuth(t, svc, &service.AuthSettings{
		OAuth2: []service.OAuth2StrategySettings{
			{Name: "google", ClientID: "id", ClientSecret: "s3cr3t"},
		},
	})

	// Second save edits an unrelated field and leaves the secret blank.
	saveAuth(t, svc, &service.AuthSettings{
		OAuth2: []service.OAuth2StrategySettings{
			{Name: "google", ClientID: "id-2", ClientSecret: ""},
		},
	})

	if got := oauth2Secret(t, svc, "google"); got != "s3cr3t" {
		t.Errorf("blank save should keep secret, got %q", got)
	}
}

// TestOAuth2SecretNewValueReplaces verifies a non-empty incoming secret wins.
func TestOAuth2SecretNewValueReplaces(t *testing.T) {
	svc := newTestService(t)

	saveAuth(t, svc, &service.AuthSettings{
		OAuth2: []service.OAuth2StrategySettings{
			{Name: "google", ClientSecret: "old"},
		},
	})
	saveAuth(t, svc, &service.AuthSettings{
		OAuth2: []service.OAuth2StrategySettings{
			{Name: "google", ClientSecret: "new"},
		},
	})

	if got := oauth2Secret(t, svc, "google"); got != "new" {
		t.Errorf("new secret should replace, got %q", got)
	}
}

// TestOAuth2SecretClearFlagWipes verifies the explicit-clear opt-out: with
// ClearClientSecret set, a blank secret is intentionally removed.
func TestOAuth2SecretClearFlagWipes(t *testing.T) {
	svc := newTestService(t)

	saveAuth(t, svc, &service.AuthSettings{
		OAuth2: []service.OAuth2StrategySettings{
			{Name: "google", ClientSecret: "s3cr3t"},
		},
	})
	saveAuth(t, svc, &service.AuthSettings{
		OAuth2: []service.OAuth2StrategySettings{
			{Name: "google", ClientSecret: "", ClearClientSecret: true},
		},
	})

	if got := oauth2Secret(t, svc, "google"); got != "" {
		t.Errorf("clear flag should wipe secret, got %q", got)
	}
}

// TestOAuth2SecretMatchesByName ensures the keep logic pairs entries by
// provider Name, not slice index, so reordering providers between loads
// doesn't cross-wire secrets.
func TestOAuth2SecretMatchesByName(t *testing.T) {
	svc := newTestService(t)

	saveAuth(t, svc, &service.AuthSettings{
		OAuth2: []service.OAuth2StrategySettings{
			{Name: "google", ClientSecret: "g-secret"},
			{Name: "github", ClientSecret: "h-secret"},
		},
	})

	// Re-send in the opposite order, both blank → each must keep its own.
	saveAuth(t, svc, &service.AuthSettings{
		OAuth2: []service.OAuth2StrategySettings{
			{Name: "github", ClientSecret: ""},
			{Name: "google", ClientSecret: ""},
		},
	})

	if got := oauth2Secret(t, svc, "google"); got != "g-secret" {
		t.Errorf("google secret cross-wired or lost: %q", got)
	}
	if got := oauth2Secret(t, svc, "github"); got != "h-secret" {
		t.Errorf("github secret cross-wired or lost: %q", got)
	}
}
