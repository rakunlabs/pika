package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
)

// newTestService is shared with permission_test.go.

func TestFindOrCreateExternalUser_Provisions(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	info, err := svc.FindOrCreateExternalUser(ctx, service.ExternalIdentityInput{
		Provider:    "google",
		Subject:     "108473839483",
		Email:       "alice@example.com",
		DisplayName: "Alice",
		Username:    "alice",
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if info.ID == "" {
		t.Fatal("expected user ID")
	}
	if !info.External {
		t.Error("expected External=true on provisioned external user")
	}
	if info.Email != "alice@example.com" {
		t.Errorf("Email = %q, want %q", info.Email, "alice@example.com")
	}

	// Second call with same (provider, subject) must return the same user.
	info2, err := svc.FindOrCreateExternalUser(ctx, service.ExternalIdentityInput{
		Provider: "google",
		Subject:  "108473839483",
		Email:    "alice.new@example.com", // email changed at IdP
	})
	if err != nil {
		t.Fatalf("re-login: %v", err)
	}
	if info2.ID != info.ID {
		t.Errorf("re-login returned different user: %s vs %s", info2.ID, info.ID)
	}
}

func TestFindOrCreateExternalUser_AutoLinksByVerifiedEmail(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Seed a local user.
	local, err := svc.CreateSetupUser(ctx, &service.CreateUserRequest{
		Username: "alice",
		Password: "s3cret",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Set email on the local user so auto-link has something to match on.
	email := "alice@example.com"
	if err := svc.UpdateUser(ctx, local.ID, &service.UpdateUserRequest{
		Email: &email,
	}); err != nil {
		t.Fatalf("set email: %v", err)
	}

	// OAuth2 login from google with matching verified email.
	info, err := svc.FindOrCreateExternalUser(ctx, service.ExternalIdentityInput{
		Provider:      "google",
		Subject:       "xyz-123",
		Email:         "alice@example.com",
		EmailVerified: true,
		DisplayName:   "Alice via Google",
	})
	if err != nil {
		t.Fatalf("auto-link: %v", err)
	}
	if info.ID != local.ID {
		t.Errorf("auto-link produced new user %s, wanted existing %s", info.ID, local.ID)
	}

	// The same user now has both local password AND google identity.
	identities, err := svc.GetUserIdentities(ctx, local.ID)
	if err != nil {
		t.Fatalf("list identities: %v", err)
	}
	if len(identities) != 1 || identities[0].Provider != "google" {
		t.Errorf("expected 1 google identity, got %+v", identities)
	}
}

func TestFindOrCreateExternalUser_UnverifiedEmailDoesNotLink(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	local, _ := svc.CreateSetupUser(ctx, &service.CreateUserRequest{Username: "alice", Password: "s3cret"})
	email := "alice@example.com"
	if err := svc.UpdateUser(ctx, local.ID, &service.UpdateUserRequest{Email: &email}); err != nil {
		t.Fatalf("set email: %v", err)
	}

	// Unverified email — must NOT link; a new user is provisioned.
	info, err := svc.FindOrCreateExternalUser(ctx, service.ExternalIdentityInput{
		Provider:      "google",
		Subject:       "xyz-123",
		Email:         "alice@example.com",
		EmailVerified: false,
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if info.ID == local.ID {
		t.Error("unverified-email login must not merge into existing user")
	}
}

func TestFindOrCreateExternalUser_DisabledLinkByVerifiedEmail(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Disable auto-linking via settings.
	disabled := false
	settings := &service.Settings{
		Auth: &service.AuthSettings{
			LinkByVerifiedEmail: &disabled,
		},
	}
	if err := svc.SaveSettings(ctx, settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	local, _ := svc.CreateSetupUser(ctx, &service.CreateUserRequest{Username: "alice", Password: "s3cret"})
	email := "alice@example.com"
	if err := svc.UpdateUser(ctx, local.ID, &service.UpdateUserRequest{Email: &email}); err != nil {
		t.Fatalf("set email: %v", err)
	}

	info, err := svc.FindOrCreateExternalUser(ctx, service.ExternalIdentityInput{
		Provider:      "google",
		Subject:       "xyz-123",
		Email:         "alice@example.com",
		EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if info.ID == local.ID {
		t.Error("LinkByVerifiedEmail=false should prevent merging")
	}
}

func TestFindOrCreateExternalUser_UsernameCollision(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Seed a user named "alice" so the external provisioner must pick a
	// different username.
	if _, err := svc.CreateSetupUser(ctx, &service.CreateUserRequest{
		Username: "alice",
		Password: "x",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	info, err := svc.FindOrCreateExternalUser(ctx, service.ExternalIdentityInput{
		Provider: "google",
		Subject:  "external-1",
		Username: "Alice", // sanitizes to "alice" → collision
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if info.Username == "alice" {
		t.Errorf("collision resolver kept the name as %q — must have appended a suffix", info.Username)
	}
	if !strings.HasPrefix(info.Username, "alice") {
		t.Errorf("collision resolver did not keep the prefix: got %q", info.Username)
	}
}

func TestFindOrCreateExternalUser_UsernameFallbacks(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	fromEmail, err := svc.FindOrCreateExternalUser(ctx, service.ExternalIdentityInput{
		Provider:    "google",
		Subject:     "sub-email",
		Email:       "carol.smith@example.com",
		DisplayName: "Display Name Is Not Unique",
	})
	if err != nil {
		t.Fatalf("provision from email: %v", err)
	}
	if fromEmail.Username != "carol_smith" {
		t.Errorf("email fallback username = %q, want carol_smith", fromEmail.Username)
	}

	fromSubject, err := svc.FindOrCreateExternalUser(ctx, service.ExternalIdentityInput{
		Provider:    "gitlab",
		Subject:     "12345",
		DisplayName: "Another Non-Unique Display Name",
	})
	if err != nil {
		t.Fatalf("provision from subject: %v", err)
	}
	if fromSubject.Username != "gitlab_12345" {
		t.Errorf("subject fallback username = %q, want gitlab_12345", fromSubject.Username)
	}
}

func TestGetUserByIdentity(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	info, err := svc.FindOrCreateExternalUser(ctx, service.ExternalIdentityInput{
		Provider: "github",
		Subject:  "gh-999",
		Username: "bob",
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	got, err := svc.GetUserByIdentity(ctx, "github", "gh-999")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.ID != info.ID {
		t.Errorf("GetUserByIdentity returned %s, want %s", got.ID, info.ID)
	}

	if _, err := svc.GetUserByIdentity(ctx, "github", "does-not-exist"); err == nil {
		t.Error("expected ErrNotFound for unknown identity")
	}
}
