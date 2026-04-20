package authx

import (
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
func buildStrategies(d Deps, s *service.AuthSettings) ([]strategy.Authenticator, error) {
	var out []strategy.Authenticator

	if local := BuildLocal(d.Svc, s.Local); local != nil {
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
	return out, nil
}
