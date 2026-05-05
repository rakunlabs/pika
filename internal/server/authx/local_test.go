package authx

import (
	"context"
	"errors"
	"testing"

	"github.com/rakunlabs/ada/middleware/auth/strategy/local"

	"github.com/rakunlabs/pika/internal/service"
)

func TestLocalVerifier_BadCredentials(t *testing.T) {
	svc := newTestService(t)
	verifier := LocalVerifier(svc)
	_, err := verifier(context.Background(), "nobody", "nothing")
	if !errors.Is(err, local.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestLocalVerifier_Happy(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.CreateSetupUser(context.Background(),
		&service.CreateUserRequest{Username: "alice", Password: "s3cret"}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	id, err := LocalVerifier(svc)(context.Background(), "alice", "s3cret")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if id.Subject != "alice" || id.Provider != "local" {
		t.Fatalf("unexpected identity: %+v", id)
	}
	if len(id.Roles) != 1 || id.Roles[0] != "superadmin" {
		t.Errorf("expected superadmin role, got %v", id.Roles)
	}
}

func TestLocalRegistrar_FirstUser(t *testing.T) {
	svc := newTestService(t)
	reg := LocalRegistrar(svc, nil)

	id, err := reg(context.Background(), local.RegisterRequest{Username: "root", Password: "hunter2"})
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	if id.Subject != "root" {
		t.Fatalf("subject: %q", id.Subject)
	}

	// Second attempt must fail with ErrUserExists
	_, err = reg(context.Background(), local.RegisterRequest{Username: "root2", Password: "x"})
	if !errors.Is(err, local.ErrUserExists) {
		t.Errorf("second: expected ErrUserExists, got %v", err)
	}
}

func TestLocalBuild_EnabledAndDisabled(t *testing.T) {
	svc := newTestService(t)

	if s := BuildLocal(svc, nil, nil); s != nil {
		t.Error("nil settings: expected nil strategy")
	}

	disabled := &service.LocalStrategySettings{Enabled: false}
	if s := BuildLocal(svc, disabled, nil); s != nil {
		t.Error("disabled: expected nil strategy")
	}

	enabled := &service.LocalStrategySettings{Enabled: true}
	if s := BuildLocal(svc, enabled, nil); s == nil || s.Name() != "local" {
		t.Errorf("enabled: expected local strategy, got %v", s)
	}
}
