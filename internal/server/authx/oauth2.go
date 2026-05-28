package authx

import (
	"github.com/rakunlabs/ada/middleware/auth/strategy"
	"github.com/rakunlabs/ada/middleware/auth/strategy/oauth2"

	"github.com/rakunlabs/pika/internal/service"
)

// BuildOAuth2 constructs zero or more oauth2 strategies from settings. New
// providers are configured with explicit authorization/token endpoints so boot
// does not depend on OIDC discovery. IssuerURL is only passed through as a
// legacy fallback for existing discovery-based settings. ClientSecret is
// decrypted out of the sealed payload by the storage wrapper before settings
// reach this layer, so the field is already plaintext here.
func BuildOAuth2(specs []service.OAuth2StrategySettings) ([]strategy.Authenticator, error) {
	out := make([]strategy.Authenticator, 0, len(specs))
	for _, s := range specs {
		if s.Name == "" || s.ClientID == "" {
			continue
		}
		manualEndpoints := s.TokenURL != "" && (s.PasswordFlow || s.AuthURL != "")
		if !manualEndpoints && s.IssuerURL == "" {
			continue
		}
		cfg := oauth2.Config{
			AuthURL:      s.AuthURL,
			TokenURL:     s.TokenURL,
			UserInfoURL:  s.UserInfoURL,
			ClientID:     s.ClientID,
			ClientSecret: s.ClientSecret,
			Scopes:       s.Scopes,
			DisablePKCE:  s.DisablePKCE,
			PasswordFlow: s.PasswordFlow,
		}
		if !manualEndpoints {
			cfg.IssuerURL = s.IssuerURL
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
