package authx

import (
	"strings"

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
//
// basePath is the server base path (ada's auth.Config.Base, e.g. "/pika/" or
// "/"). We use it to set each strategy's CallbackBasePath explicitly so the
// redirect_uri carries the base_path prefix. ada otherwise only wires this at
// Mount time, but pika rebuilds strategies on every auth-settings save via
// Manager.Reload → Registry.Replace, which does NOT re-run Mount/SetCallback-
// BasePath — so a UI-added/edited provider would emit a base-path-less
// redirect_uri (falling back to ada's "/auth/callback" default) until the next
// restart. Setting it here (an explicit value always wins over SetCallback-
// BasePath) keeps boot and reload consistent and matches the mounted route
// "{base}/login/callback/{strategy}".
func BuildOAuth2(specs []service.OAuth2StrategySettings, basePath string) ([]strategy.Authenticator, error) {
	// Mirror ada's Mount-time formula (auth.go: TrimSuffix(cfg.Base,"/") +
	// "/login/callback") so the redirect_uri path matches the route exactly.
	callbackBasePath := strings.TrimSuffix(basePath, "/") + "/login/callback"

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
		strat := oauth2.New(s.Name, cfg, oauth2.Options{
			Label:            label,
			CallbackBasePath: callbackBasePath,
		})
		out = append(out, strat)
	}
	return out, nil
}
