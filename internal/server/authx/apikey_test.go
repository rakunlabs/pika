package authx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/ada/middleware/auth/strategy/apikey"

	"github.com/rakunlabs/pika/internal/service"
)

func TestAPIKeyValidator_InvalidKey(t *testing.T) {
	svc := newTestService(t)
	_, err := APIKeyValidator(svc)(context.Background(), "pika_bogus")
	if !errors.Is(err, apikey.ErrInvalidKey) {
		t.Fatalf("expected ErrInvalidKey, got %v", err)
	}
}

func TestAPIKeyValidator_Happy(t *testing.T) {
	svc := newTestService(t)
	// create a superadmin to satisfy CreateToken audit
	_, _ = svc.CreateSetupUser(context.Background(),
		&service.CreateUserRequest{Username: "admin", Password: "x"})

	ctx := service.WithUserInfo(context.Background(), "admin", "")
	resp, err := svc.CreateToken(ctx, &service.CreateTokenRequest{
		Name:   "test",
		Scopes: []service.TokenScope{{Path: "app/*", Operations: []string{"read"}}},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	id, err := APIKeyValidator(svc)(context.Background(), resp.RawKey)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if id.Provider != "apikey" || id.Subject != "test" {
		t.Fatalf("identity: %+v", id)
	}
	if len(id.Scopes) != 1 || id.Scopes[0] != "app/*" {
		t.Errorf("scopes: %v", id.Scopes)
	}
}

// TestBuildAPIKey_AuthorizationOnly verifies that the built strategy
// accepts `Authorization: Bearer <key>` and rejects the legacy `X-API-Key`
// fallback. Pika deliberately pins the header so there's only one
// contract for API-key presentation.
func TestBuildAPIKey_AuthorizationOnly(t *testing.T) {
	svc := newTestService(t)

	// Seed an admin + create a real token so the validator has something
	// to match against.
	_, _ = svc.CreateSetupUser(context.Background(),
		&service.CreateUserRequest{Username: "admin", Password: "x"})
	ctx := service.WithUserInfo(context.Background(), "admin", "")
	resp, err := svc.CreateToken(ctx, &service.CreateTokenRequest{
		Name:   "hdr-test",
		Scopes: []service.TokenScope{{Path: "app/*", Operations: []string{"read"}}},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	s := BuildAPIKey(svc)
	if s == nil {
		t.Fatal("expected non-nil strategy")
	}
	if s.Name() != "apikey" {
		t.Errorf("name: %q", s.Name())
	}

	// Authorization: Bearer <key> → accepted; strategy returns the
	// real Identity derived from the token.
	okReq := httptest.NewRequest(http.MethodGet, "/", nil)
	okReq.Header.Set("Authorization", "Bearer "+resp.RawKey)
	okRec := httptest.NewRecorder()
	id, _, err := s.Login(okRec, okReq)
	if err != nil {
		t.Fatalf("Login(Authorization): %v", err)
	}
	if id == nil {
		t.Errorf("Authorization header: expected non-nil identity, got 401 (status=%d)", okRec.Code)
	}

	// X-API-Key → rejected (legacy fallback is disabled because pika
	// passes WithHeaderName("Authorization") explicitly).
	badReq := httptest.NewRequest(http.MethodGet, "/", nil)
	badReq.Header.Set("X-API-Key", resp.RawKey)
	badRec := httptest.NewRecorder()
	id2, _, _ := s.Login(badRec, badReq)
	if id2 != nil {
		t.Error("X-API-Key should not be accepted — pika pins Authorization-only")
	}
	if badRec.Code != http.StatusUnauthorized {
		t.Errorf("X-API-Key: expected 401, got %d", badRec.Code)
	}
}
