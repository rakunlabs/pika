package authx

import (
	"context"
	"errors"

	"github.com/rakunlabs/ada/middleware/auth/identity"
	"github.com/rakunlabs/ada/middleware/auth/strategy/apikey"

	"github.com/rakunlabs/pika/internal/service"
)

// APIKeyValidator returns an apikey.Validator that checks pika's token table.
// Header-only extraction — no query-string token acceptance.
func APIKeyValidator(svc *service.Service) apikey.Validator {
	return func(ctx context.Context, key string) (*identity.Identity, error) {
		id, err := svc.ValidateTokenIdentity(ctx, key)
		if err != nil {
			if errors.Is(err, service.ErrUnauthorized) || errors.Is(err, service.ErrForbidden) {
				return nil, apikey.ErrInvalidKey
			}
			return nil, err
		}
		return id, nil
	}
}

// BuildAPIKey constructs the apikey strategy. Pika intentionally exposes
// only one way to present a token — as `Authorization: Bearer <key>`.
// Offering a second header gives operators a footgun: clients would
// silently keep working against one header while another tool broke
// against the other. One header, one contract.
//
// Always enabled. Pika builds this strategy unconditionally so tokens
// issued from the Access Tokens page always work — there's no toggle.
func BuildAPIKey(svc *service.Service) *apikey.Strategy {
	return apikey.New(
		"apikey",
		APIKeyValidator(svc),
		apikey.WithLabel("API Key"),
		// Restrict to Authorization only. ada's WithHeaders replaces the
		// default (Authorization, X-API-Key) list, so X-API-Key is not
		// accepted. bearerPrefix defaults to true — clients still send
		// `Authorization: Bearer <key>` and the prefix is stripped
		// before the key hits the validator.
		apikey.WithHeaders("Authorization"),
	)
}
