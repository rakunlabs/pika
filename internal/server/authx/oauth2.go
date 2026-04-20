package authx

import (
	"github.com/rakunlabs/ada/middleware/auth/strategy"
	"github.com/rakunlabs/ada/middleware/auth/strategy/oauth2"

	"github.com/rakunlabs/pika/internal/service"
)

// BuildOAuth2 constructs zero or more oauth2 strategies from settings. Any
// strategy whose IssuerURL or ClientID is empty is skipped.
// NOTE: ClientSecret is treated as plaintext — TODO: re-encrypt via secret store (Task 20).
func BuildOAuth2(specs []service.OAuth2StrategySettings) ([]strategy.Authenticator, error) {
	out := make([]strategy.Authenticator, 0, len(specs))
	for _, s := range specs {
		if s.Name == "" || s.IssuerURL == "" || s.ClientID == "" {
			continue
		}
		// TODO: decrypt s.ClientSecret via secret store when available.
		secretClear := s.ClientSecret

		cfg := oauth2.Config{
			IssuerURL:    s.IssuerURL,
			ClientID:     s.ClientID,
			ClientSecret: secretClear,
			Scopes:       s.Scopes,
			DisablePKCE:  s.DisablePKCE,
			PasswordFlow: s.PasswordFlow,
		}
		label := s.DisplayName
		if label == "" {
			label = s.Name
		}
		strat := oauth2.New(s.Name, cfg, oauth2.Options{Label: label})
		out = append(out, strat)
	}
	return out, nil
}
