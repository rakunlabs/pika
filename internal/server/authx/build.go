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

	// TOTP / 2FA coordinator. Built before the strategies that need
	// it so we can pass it into the MFA wrapper. nil-safe: when MFA
	// is off (TOTPCoord returns nil), the wrapper is a transparent
	// pass-through, so the wiring is identical whether or not 2FA is
	// configured for this deployment.
	totpCoord := BuildTOTPService(d.Svc, s)
	d.Svc.SetTOTPService(totpCoord)

	if local := BuildLocal(d.Svc, s.Local, onRegister); local != nil {
		// Local supports signup (first-user bootstrap), so we wrap
		// with the Registerer-aware variant. The wrapper forwards
		// Register straight to the inner — registration paths can't
		// have TOTP enrolled yet.
		out = append(out, NewMFAStrategyWithRegister(local, d.Svc, totpCoord))
	}
	// API Key strategy is always on: tokens created under the Access
	// Tokens settings page must always work, and pika only accepts them
	// via `Authorization: Bearer <key>` (no settings knob to change
	// header name — see BuildAPIKey doc). NOT wrapped with MFA — API
	// keys are programmatic credentials, not interactive logins.
	if apik := BuildAPIKey(d.Svc); apik != nil {
		out = append(out, apik)
	}
	// Header / proxy auth: not wrapped. The upstream proxy is already
	// trusted to assert identity; layering pika-side TOTP on top
	// would be redundant and confusing (the proxy can't render the
	// step-up screen).
	if h := BuildHeader(s.Header); h != nil {
		out = append(out, h)
	}
	// OAuth2: not wrapped. The IdP is responsible for its own MFA;
	// pika doesn't see the user's IdP password, so adding pika-side
	// TOTP on top would just inconvenience users without improving
	// security (the user could just enroll TOTP at the IdP instead).
	oa2, err := BuildOAuth2(s.OAuth2, d.BasePath)
	if err != nil {
		return nil, err
	}
	out = append(out, oa2...)

	l, err := BuildLDAP(s.LDAP)
	if err != nil {
		return nil, err
	}
	if l != nil {
		// LDAP wrapped with MFA. LDAP doesn't support signup (no
		// Registerer interface satisfaction), so we use the plain
		// MFAStrategy — that way ada's handleRegister returns 404
		// for /login/register/<ldap-name>, matching the pre-wrap
		// behavior.
		out = append(out, NewMFAStrategy(l, d.Svc, totpCoord))
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
		strat, err := BuildPasskeyStrategy(engine, ps, d.Svc, name, label, ttl)
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
