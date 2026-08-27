package service

import (
	"context"
	"time"
)

// AuthSettings is the runtime auth configuration stored in the settings table.
// Replaces the legacy ForwardAuthSettings + ExternalPermissionsSettings.
// See docs/superpowers/specs/2026-04-18-ada-auth-migration-design.md.
type AuthSettings struct {
	UI     AuthUI     `json:"ui"`
	Cookie AuthCookie `json:"cookie"`
	Issuer AuthIssuer `json:"issuer"`

	// Supported strategies. Deliberately absent:
	//   - basic + magic-link: not exposed in the UI or wired into authx
	//   - apikey: pika hardcodes `Authorization: Bearer <key>` at boot
	//     time (authx.BuildAPIKey). There is no configurable header — it
	//     would let two clients silently disagree about where to put the
	//     token. Tokens are managed under Settings → Access Tokens.
	Local   *LocalStrategySettings   `json:"local,omitempty"`
	OAuth2  []OAuth2StrategySettings `json:"oauth2,omitempty"`
	Header  *HeaderStrategySettings  `json:"header,omitempty"`
	Passkey *PasskeyStrategySettings `json:"passkey,omitempty"`

	// RateLimit guards password-bearing endpoints (login, signup) against
	// brute-force attacks. When nil, defaults are applied at boot.
	RateLimit *AuthRateLimitSettings `json:"rate_limit,omitempty"`

	// LinkByVerifiedEmail, when true (default), merges an incoming
	// external identity (OAuth2 or Header) into an existing user row
	// whose email matches the IdP-asserted email — but only if the IdP
	// marks the email as verified (OIDC `email_verified: true`). Without
	// this, two logins by the same person from different providers
	// produce two separate pika users.
	//
	// Set to false if you don't trust your IdPs' email-verification
	// claim (or you have overlapping email domains across tenants).
	// When disabled, every new (provider, subject) creates a brand-new
	// pika user that an admin can later link manually.
	LinkByVerifiedEmail *bool `json:"link_by_verified_email,omitempty"`

	// AccountSecurityAdminOnly restricts passkey and TOTP self-service to
	// superadmins. The default false preserves self-service for every user.
	AccountSecurityAdminOnly bool `json:"account_security_admin_only,omitempty"`

	Capabilities CapabilityMapping `json:"capabilities"`
}

// AccountSecurityAllowed reports whether the current caller may use the
// passkey and TOTP self-service endpoints under this configuration.
func (s *AuthSettings) AccountSecurityAllowed(superadmin bool) bool {
	return s == nil || !s.AccountSecurityAdminOnly || superadmin
}

// LinkByVerifiedEmailEnabled returns the effective value (default true)
// of AuthSettings.LinkByVerifiedEmail, handling both nil settings and nil
// fields uniformly.
func (s *AuthSettings) LinkByVerifiedEmailEnabled() bool {
	if s == nil || s.LinkByVerifiedEmail == nil {
		return true
	}
	return *s.LinkByVerifiedEmail
}

// AuthRateLimitSettings controls the sliding-window rate limiter applied to
// POST /login/pass/* and POST /login/register/*. Two independent axes run
// in parallel: per-client-IP and per-username. Either axis tripping its hard
// threshold rejects the request with 429 + Retry-After.
//
// Below the soft threshold the request is unaffected. At soft..hard the
// middleware sleeps for an exponentially-growing delay (BackoffBase * 2^n,
// capped at BackoffMax) before invoking the handler. At/above hard, the
// request is rejected without reaching the handler.
//
// Changes take effect at next server start.
type AuthRateLimitSettings struct {
	// Enabled turns the whole limiter on or off. Default true.
	Enabled bool `json:"enabled"`

	// Window is the sliding-window length. Default 15m.
	Window time.Duration `json:"window,omitempty"`

	// IPSoftThreshold is the per-IP count at which backoff engages. Default 3.
	IPSoftThreshold int `json:"ip_soft_threshold,omitempty"`

	// IPHardThreshold is the per-IP count at which requests get 429. Default 30.
	IPHardThreshold int `json:"ip_hard_threshold,omitempty"`

	// UserSoftThreshold is the per-username count at which backoff engages. Default 3.
	UserSoftThreshold int `json:"user_soft_threshold,omitempty"`

	// UserHardThreshold is the per-username count at which requests get 429. Default 15.
	UserHardThreshold int `json:"user_hard_threshold,omitempty"`

	// BackoffBase is the base of the exponential delay. Default 1s.
	BackoffBase time.Duration `json:"backoff_base,omitempty"`

	// BackoffMax caps the per-request delay. Default 15s.
	BackoffMax time.Duration `json:"backoff_max,omitempty"`

	// TrustedProxyCIDRs lists CIDR blocks whose XFF / X-Real-IP /
	// True-Client-IP headers are honored for client-IP extraction. Empty
	// (default) means use RemoteAddr only — required when pika is
	// directly internet-facing. Set to your reverse-proxy's network when
	// running behind nginx/traefik/etc.
	TrustedProxyCIDRs []string `json:"trusted_proxy_cidrs,omitempty"`
}

// DefaultAuthRateLimitSettings returns the out-of-the-box rate-limit config
// applied when a settings row has no rate_limit block.
func DefaultAuthRateLimitSettings() *AuthRateLimitSettings {
	return &AuthRateLimitSettings{
		Enabled:           true,
		Window:            15 * time.Minute,
		IPSoftThreshold:   3,
		IPHardThreshold:   30,
		UserSoftThreshold: 3,
		UserHardThreshold: 15,
		BackoffBase:       time.Second,
		BackoffMax:        15 * time.Second,
	}
}

// WithDefaults fills unset fields with their defaults. Mutates and returns
// the receiver. A nil receiver yields a fresh fully-defaulted struct.
func (s *AuthRateLimitSettings) WithDefaults() *AuthRateLimitSettings {
	d := DefaultAuthRateLimitSettings()
	if s == nil {
		return d
	}
	if s.Window <= 0 {
		s.Window = d.Window
	}
	if s.IPSoftThreshold <= 0 {
		s.IPSoftThreshold = d.IPSoftThreshold
	}
	if s.IPHardThreshold <= 0 {
		s.IPHardThreshold = d.IPHardThreshold
	}
	if s.UserSoftThreshold <= 0 {
		s.UserSoftThreshold = d.UserSoftThreshold
	}
	if s.UserHardThreshold <= 0 {
		s.UserHardThreshold = d.UserHardThreshold
	}
	if s.BackoffBase <= 0 {
		s.BackoffBase = d.BackoffBase
	}
	if s.BackoffMax <= 0 {
		s.BackoffMax = d.BackoffMax
	}
	return s
}

// AuthUI carries the editable branding of the login screen. Note that
// Version is intentionally absent: the login UI displays the server
// version from the build-time ldflags (plumbed via authx.Deps.Version),
// so operators cannot desync the displayed version from the actual
// running binary.
type AuthUI struct {
	Title        string            `json:"title,omitempty"`
	Subtitle     string            `json:"subtitle,omitempty"`
	Icon         string            `json:"icon,omitempty"`
	Theme        map[string]string `json:"theme,omitempty"`
	CustomCSSURL string            `json:"custom_css_url,omitempty"`
}

type AuthCookie struct {
	Name   string `json:"name,omitempty"`
	Domain string `json:"domain,omitempty"`
	Path   string `json:"path,omitempty"`

	// Secure forces the Secure attribute on. Leaving it off does not mean
	// "never": the cookie is marked Secure whenever the request arrived
	// over TLS. Turn this on when TLS terminates upstream and the proxy
	// does not forward a protocol hint, so the automatic detection cannot
	// see it.
	Secure bool `json:"secure,omitempty"`

	// DisableHTTPOnly exposes the session cookie to JavaScript.
	//
	// This is an opt-out, and the reason it is phrased that way: pika
	// used to carry an opt-in `http_only` flag that defaulted to off,
	// which meant every install that never opened the setting shipped a
	// script-readable session cookie. Nothing in the SPA reads it, so
	// there is no reason to turn this on.
	DisableHTTPOnly bool `json:"disable_http_only,omitempty"`

	// SameSite is "lax" (default), "strict" or "none".
	SameSite string `json:"same_site,omitempty"`
}

type AuthIssuer struct {
	AccessTTL  time.Duration `json:"access_ttl,omitempty"`
	RefreshTTL time.Duration `json:"refresh_ttl,omitempty"`

	// DisableRefreshRotation turns off refresh-token rotation.
	//
	// Rotation is on by default: each refresh mints a new token and
	// invalidates the old one, so a stolen refresh token is usable at
	// most once. Turn it off only if several clients share one session
	// and would race each other out of it — not a shape pika's browser
	// sessions take.
	//
	// Like DisableHTTPOnly, this replaces an opt-in flag (`rotate_refresh`)
	// that defaulted to the weaker behaviour. Settings persisted with
	// either value of the old field land on rotation-enabled after the
	// rename, which is the safe direction.
	DisableRefreshRotation bool `json:"disable_refresh_rotation,omitempty"`
}

type LocalStrategySettings struct {
	Enabled            bool   `json:"enabled"`
	Name               string `json:"name,omitempty"`
	LoginFormCollapsed bool   `json:"login_form_collapsed,omitempty"`
}

type OAuth2StrategySettings struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	AuthURL     string `json:"auth_url,omitempty"`
	TokenURL    string `json:"token_url,omitempty"`
	UserInfoURL string `json:"userinfo_url,omitempty"`
	// IssuerURL is retained for existing OIDC-discovery based settings.
	// New providers should set AuthURL and TokenURL explicitly instead.
	IssuerURL    string `json:"issuer_url,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	// ClientSecretSet is a read-only indicator the API emits on GET so the
	// SPA can tell "a secret is stored" apart from "no secret" without ever
	// receiving the secret value itself. It is never persisted: the API sets
	// it on the response copy, and PatchSettings forces it false before store.
	ClientSecretSet bool `json:"client_secret_set,omitempty"`
	// ClearClientSecret is a write-only request flag. Because the SPA can't
	// read a stored secret back, an empty ClientSecret on save means "keep the
	// existing one" (see PatchSettings). Setting this flag opts that single
	// provider out so the operator can deliberately blank the secret. It is
	// never persisted.
	ClearClientSecret bool     `json:"clear_client_secret,omitempty"`
	Scopes            []string `json:"scopes,omitempty"`
	DisablePKCE       bool     `json:"disable_pkce,omitempty"`
	PasswordFlow      bool     `json:"password_flow,omitempty"`
	// TokenAuthMethod selects how the client credentials are presented to
	// the token endpoint during the code (or password) exchange. Most
	// providers accept the default HTTP Basic header, but some reject it
	// and require the secret transmitted differently — surfacing as an
	// "invalid_client" / "client_secret does not match" error even when the
	// secret itself is correct.
	//
	//	"" / "basic" -> HTTP Basic auth header (client_secret_basic) [default]
	//	"post"       -> client_id & client_secret sent as request parameters
	//	"bearer"     -> Authorization: Bearer <client_secret>
	//
	// See authx.BuildOAuth2 for the mapping onto ada's AuthHeaderStyle.
	TokenAuthMethod string `json:"token_auth_method,omitempty"`
	// AutoCreateUser allows this OAuth2 provider to materialize unknown
	// external identities into local external-only users during login. When
	// false (default), login only binds to an existing user/link.
	AutoCreateUser bool `json:"auto_create_user,omitempty"`
	// RolesClaims lists dotted claim paths to read role strings from when
	// resolving capabilities for this provider. Empty means the default
	// ["roles"] (the flat top-level claim ada already extracts into
	// Identity.Roles).
	//
	// Paths support nesting and a "*" wildcard segment that iterates every
	// value of a map. This covers Keycloak's role shapes:
	//
	//	"realm_access.roles"          -> realm roles
	//	"resource_access.*.roles"     -> every client's roles
	//	"resource_access.pika.roles"  -> one client's roles
	//
	// The harvested role strings are unioned with Identity.Roles and routed
	// through CapabilityMapping.RoleMapping like any other role name (bare,
	// not client-namespaced). See HarvestRoles and CapResolver.resolve.
	RolesClaims []string `json:"roles_claims,omitempty"`
}

// OAuth2RolesClaims returns the EXTRA role-claim paths configured per OAuth2
// provider, keyed by provider Name. Only providers with an explicit
// RolesClaims are included — these are additive sources harvested on top of
// the default flat "roles" claim, which ada always extracts into
// Identity.Roles and CapResolver.effectiveRoles always unions in. A provider
// that sets no RolesClaims is therefore absent from the map and keeps the
// default "roles" behavior with zero extra work. Used by authx.Manager to bind
// the CapResolver to the live settings at boot/reload.
func (s *AuthSettings) OAuth2RolesClaims() map[string][]string {
	if s == nil || len(s.OAuth2) == 0 {
		return nil
	}
	var out map[string][]string
	for _, spec := range s.OAuth2 {
		if spec.Name == "" || len(spec.RolesClaims) == 0 {
			continue
		}
		if out == nil {
			out = make(map[string][]string)
		}
		out[spec.Name] = spec.RolesClaims
	}
	return out
}

// OAuth2AutoCreateUserEnabled reports whether the named OAuth2 provider is
// allowed to provision missing users during login. Missing settings, unknown
// providers and non-OAuth2 strategies all default to fail-closed.
func (s *AuthSettings) OAuth2AutoCreateUserEnabled(provider string) bool {
	if s == nil || provider == "" {
		return false
	}
	for _, spec := range s.OAuth2 {
		if spec.Name == provider {
			return spec.AutoCreateUser
		}
	}
	return false
}

type HeaderStrategySettings struct {
	Name           string   `json:"name,omitempty"`
	User           string   `json:"user,omitempty"`
	Email          string   `json:"email,omitempty"`
	DisplayName    string   `json:"display_name_header,omitempty"`
	Roles          string   `json:"roles,omitempty"`
	Groups         string   `json:"groups,omitempty"`
	TrustedProxies []string `json:"trusted_proxies,omitempty"`
}

// PasskeyStrategySettings configures the WebAuthn login strategy.
//
// RPID is the WebAuthn relying-party identifier — the effective
// domain the credentials are bound to (e.g. "example.com" or
// "localhost"). MUST be set for passkey to work; an empty RPID
// disables the feature.
//
// RPOrigins is the list of full origins (scheme + host + optional
// port) the user agent may report in clientDataJSON. Multiple
// entries allow the same pika instance to serve from both
// "https://example.com" and "https://admin.example.com" without
// re-enrolling passkeys per origin.
//
// Name / Label are the UI knobs: Name is the URL key the SPA POSTs
// to (/login/pass/<name>); Label is the human string shown on the
// login screen.
//
// UserVerification mirrors the WebAuthn enum. "preferred" (default)
// asks for biometric/PIN when the authenticator supports it but
// falls back to UP-only. "required" forces verification — set this
// when passkey is being used as the sole authentication factor.
//
// ChallengeTTL caps how long a registration or login challenge stays
// valid. 5 minutes is the typical sweet spot.
type PasskeyStrategySettings struct {
	Enabled          bool          `json:"enabled"`
	Name             string        `json:"name,omitempty"`
	Label            string        `json:"label,omitempty"`
	RPID             string        `json:"rp_id,omitempty"`
	RPDisplayName    string        `json:"rp_display_name,omitempty"`
	RPOrigins        []string      `json:"rp_origins,omitempty"`
	UserVerification string        `json:"user_verification,omitempty"`
	ChallengeTTL     time.Duration `json:"challenge_ttl,omitempty"`
}

// CapabilityMapping declares how external identities (OAuth2 or Header)
// gain pika capabilities without a per-user DB row.
//
// RoleMapping/ScopeMapping keys are the external role/scope names as they
// appear on the identity (e.g. an OAuth2 `roles` claim value). Their VALUES
// are pika Permission bundle Keys — each is expanded at request time to the
// bundle's capability keys and path patterns (see CapResolver.resolve and
// Service.CapabilitiesFromBundles). A value that doesn't match any existing
// bundle Key grants nothing (fail-closed), so renaming/deleting a bundle
// quietly revokes the external grant rather than erroring.
//
// Note: prior versions stored raw capability keys here. After the switch to
// bundle Keys, legacy capability-key values no longer match a bundle and must
// be re-pointed at a Permission via the settings UI.
type CapabilityMapping struct {
	Superadmins  []string            `json:"superadmins,omitempty"`
	RoleMapping  map[string][]string `json:"role_mapping,omitempty"`
	ScopeMapping map[string][]string `json:"scope_mapping,omitempty"`
}

// GetAuthSettings returns the current AuthSettings from DB settings, or nil
// when nothing has been configured yet.
func (s *Service) GetAuthSettings(ctx context.Context) *AuthSettings {
	settings, err := s.Settings(ctx)
	if err != nil || settings == nil {
		return nil
	}
	return settings.Auth
}

// DefaultAuthSettings returns the baseline AuthSettings applied when the
// DB has no stored auth config. Mirrors what the authx manager uses at
// boot when settings.Auth is nil — exposed here so API responses can show
// the effective config instead of a blank form.
func DefaultAuthSettings() *AuthSettings {
	return &AuthSettings{
		UI:    AuthUI{Title: "Pika"},
		Local: &LocalStrategySettings{Enabled: true, Name: "local"},
	}
}

// WithEffectiveDefaults fills unset fields on a sparse settings object so
// callers see the live runtime config. Only applied when the whole auth
// block has never been configured (s is nil or the receiver has no
// strategy at all) — an operator who explicitly saved auth with no Local
// strategy retains that intent. Call sites:
//
//   - GET /api/v1/settings (read path): surfaces the boot defaults so
//     "Local strategy disabled" isn't shown for a fresh install where
//     login actually works via the implicit default.
//   - authx.Manager.Boot: applies the same defaults so runtime and API
//     view agree.
//
// Rules when filling:
//   - Nil receiver → fresh struct with Local enabled and RateLimit defaults.
//   - Non-nil receiver with at least one strategy configured → no Local
//     auto-fill; only RateLimit zero-fields are filled.
//   - Non-nil receiver with no strategy at all → Local auto-fill (treat
//     as "equivalent to nil" since otherwise the server has no way to
//     authenticate anyone).
//
// Mutates and returns the receiver.
func (s *AuthSettings) WithEffectiveDefaults() *AuthSettings {
	if s == nil {
		d := DefaultAuthSettings()
		d.RateLimit = DefaultAuthRateLimitSettings()
		return d
	}
	if !s.hasAnyStrategy() {
		d := DefaultAuthSettings()
		s.Local = d.Local
	}
	s.RateLimit = s.RateLimit.WithDefaults()
	return s
}

// hasAnyStrategy reports whether any authentication strategy has been
// configured. A settings object with zero strategies leaves the server
// unable to authenticate anyone, so the default local strategy is
// applied in that case — matching what the authx manager does at boot
// from a fresh DB.
func (s *AuthSettings) hasAnyStrategy() bool {
	if s == nil {
		return false
	}
	if s.Local != nil {
		return true
	}
	if len(s.OAuth2) > 0 {
		return true
	}
	if s.Header != nil {
		return true
	}
	if s.Passkey != nil && s.Passkey.Enabled {
		return true
	}
	// APIKey is always built (see AuthSettings doc), so it never
	// contributes to this check — hasAnyStrategy is about login-form
	// strategies that the operator has opted into, not about boot-time
	// fixtures.
	return false
}
