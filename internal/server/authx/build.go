package authx

import (
	"fmt"
	"time"

	"github.com/rakunlabs/ada/middleware/auth"
	"github.com/rakunlabs/ada/middleware/auth/issuer"
	"github.com/rakunlabs/ada/middleware/auth/session"
	"github.com/rakunlabs/ada/middleware/auth/sessionstore"
	"github.com/rakunlabs/ada/middleware/auth/strategy"

	"github.com/rakunlabs/pika/internal/service"
)

// Deps is the set of collaborators the manager needs.
type Deps struct {
	Svc          *service.Service
	SessionStore sessionstore.Store
	BasePath     string // e.g. "/api/v1/"
	CookieName   string // must match AuthSettings.Cookie.Name

	// Version is the build-time server version displayed on the login UI.
	// Sourced from the -ldflags-stamped binary (cmd/pika/main.go), not
	// from the settings DB, so operators can't desync what the login
	// screen shows from the actual binary they're running.
	Version string
}

// buildAuthConfig converts AuthSettings into an auth.Config.
func buildAuthConfig(s *service.AuthSettings, base, version string, signupFirstFn func() bool) auth.Config {
	cookieName := s.Cookie.Name
	if cookieName == "" {
		cookieName = "pika_session"
	}
	cfg := auth.Config{
		Base: base,
		UI: auth.UIConfig{
			Title:         s.UI.Title,
			Subtitle:      s.UI.Subtitle,
			Icon:          s.UI.Icon,
			Version:       version,
			Theme:         s.UI.Theme,
			CustomCSSURL:  s.UI.CustomCSSURL,
			SignupFirstFn: signupFirstFn,
			// Pika ships its own login UI in _ui (Login.svelte) served by
			// the SPA folder handler at /*. Setting ExternalFolder=true
			// tells ada to register only the JSON endpoints under /login/*
			// and skip mounting its embedded static UI, so the SPA owns
			// the /login route and the experience stays consistent with
			// the rest of the app.
			ExternalFolder: true,
		},
		CookieName: cookieName,
		Cookie: session.CookieOptions{
			Path:     s.Cookie.Path,
			Domain:   s.Cookie.Domain,
			Secure:   s.Cookie.Secure,
			HttpOnly: s.Cookie.HttpOnly,
		},
		IssuerConfig: issuer.Config{
			AccessTTL:     s.Issuer.AccessTTL,
			RefreshTTL:    s.Issuer.RefreshTTL,
			RotateRefresh: s.Issuer.RotateRefresh,
		},
	}
	return cfg
}

// buildStrategies translates AuthSettings → concrete strategies.
//
// The passkey strategy is special: it depends on a long-lived
// *service.PasskeyService which holds the in-process challenge store.
// We instantiate (or replace) that service here at every Boot/Reload
// so a settings change (RPID, origins, TTL) takes effect on the next
// request without invalidating in-flight enrollments unnecessarily —
// the previous service is GC'd when the *auth.Auth registry replaces
// the strategy.
func buildStrategies(d Deps, s *service.AuthSettings, onRegister func()) ([]strategy.Authenticator, error) {
	var out []strategy.Authenticator

	if local := BuildLocal(d.Svc, s.Local, onRegister); local != nil {
		out = append(out, local)
	}
	// API Key strategy is always on: tokens created under the Access
	// Tokens settings page must always work, and pika only accepts them
	// via `Authorization: Bearer <key>` (no settings knob to change
	// header name — see BuildAPIKey doc).
	if apik := BuildAPIKey(d.Svc); apik != nil {
		out = append(out, apik)
	}
	if h := BuildHeader(s.Header); h != nil {
		out = append(out, h)
	}
	oa2, err := BuildOAuth2(s.OAuth2)
	if err != nil {
		return nil, err
	}
	out = append(out, oa2...)

	l, err := BuildLDAP(s.LDAP)
	if err != nil {
		return nil, err
	}
	if l != nil {
		out = append(out, l)
	}

	// Passkey: wire the engine + service if the settings have it on.
	// Disabled (nil engine) means we skip registration AND tear down
	// any previously-bound PasskeyService so the /api/v1/me/passkeys
	// endpoints return 503 immediately.
	engine, err := BuildPasskeyEngine(s)
	if err != nil {
		return nil, fmt.Errorf("passkey engine: %w", err)
	}
	if engine != nil {
		ttl := time.Duration(0)
		if s.Passkey != nil {
			ttl = s.Passkey.ChallengeTTL
		}
		ps := service.NewPasskeyService(d.Svc, engine, ttl)
		d.Svc.SetPasskeyService(ps)

		name, label := "passkey", "Passkey"
		if s.Passkey != nil {
			if s.Passkey.Name != "" {
				name = s.Passkey.Name
			}
			if s.Passkey.Label != "" {
				label = s.Passkey.Label
			}
		}
		strat, err := BuildPasskeyStrategy(engine, ps, name, label)
		if err != nil {
			return nil, fmt.Errorf("passkey strategy: %w", err)
		}
		if strat != nil {
			out = append(out, strat)
		}
	} else {
		// Tear down any prior coordinator so the API endpoints stop
		// accepting enroll requests as soon as an operator turns the
		// feature off.
		d.Svc.SetPasskeyService(nil)
	}

	return out, nil
}
