package authx

import (
	"context"
	"errors"

	"github.com/rakunlabs/ada/middleware/auth/identity"
	"github.com/rakunlabs/ada/middleware/auth/strategy/local"

	"github.com/rakunlabs/pika/internal/service"
)

// LocalVerifier returns a local.Verifier that validates credentials against
// pika's users table.
func LocalVerifier(svc *service.Service) local.Verifier {
	return func(ctx context.Context, username, password string) (*identity.Identity, error) {
		id, err := svc.VerifyIdentity(ctx, username, password)
		if err != nil {
			if errors.Is(err, service.ErrUnauthorized) || errors.Is(err, service.ErrForbidden) {
				return nil, local.ErrInvalidCredentials
			}
			return nil, err
		}
		return id, nil
	}
}

// LocalRegistrar returns a local.Registrar that creates the FIRST user in the
// system as a superadmin. Subsequent registrations fail with
// local.ErrUserExists — admins create additional users through the admin API.
func LocalRegistrar(svc *service.Service) local.Registrar {
	return func(ctx context.Context, req local.RegisterRequest) (*identity.Identity, error) {
		id, err := svc.BootstrapFirstUser(ctx, req.Username, req.Password)
		if err != nil {
			if errors.Is(err, service.ErrConflict) {
				return nil, local.ErrUserExists
			}
			return nil, err
		}
		return id, nil
	}
}

// BuildLocal constructs the local strategy from settings. Returns nil when
// the strategy is disabled or not configured.
func BuildLocal(svc *service.Service, s *service.LocalStrategySettings) *local.Strategy {
	if s == nil || !s.Enabled {
		return nil
	}
	name := s.Name
	if name == "" {
		name = "local"
	}
	return local.New(name, LocalVerifier(svc),
		local.WithLabel("Username & Password"),
		local.WithRegistrar(LocalRegistrar(svc)),
		local.WithAutoLogin(true),
	)
}
