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
	LDAP    *LDAPStrategySettings    `json:"ldap,omitempty"`
	Header  *HeaderStrategySettings  `json:"header,omitempty"`
	Passkey *PasskeyStrategySettings `json:"passkey,omitempty"`

	// RateLimit guards password-bearing endpoints (login, signup) against
	// brute-force attacks. When nil, defaults are applied at boot.
	RateLimit *AuthRateLimitSettings `json:"rate_limit,omitempty"`

	// LinkByVerifiedEmail, when true (default), merges an incoming
	// external identity (OAuth2, LDAP, Header) into an existing user row
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

	Capabilities CapabilityMapping `json:"capabilities"`
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
	Name     string `json:"name,omitempty"`
	Domain   string `json:"domain,omitempty"`
	Path     string `json:"path,omitempty"`
	Secure   bool   `json:"secure,omitempty"`
	HttpOnly bool   `json:"http_only,omitempty"`
	SameSite string `json:"same_site,omitempty"`
}

type AuthIssuer struct {
	AccessTTL     time.Duration `json:"access_ttl,omitempty"`
	RefreshTTL    time.Duration `json:"refresh_ttl,omitempty"`
	RotateRefresh bool          `json:"rotate_refresh,omitempty"`
}

type LocalStrategySettings struct {
	Enabled bool   `json:"enabled"`
	Name    string `json:"name,omitempty"`
}

type OAuth2StrategySettings struct {
	Name         string   `json:"name"`
	DisplayName  string   `json:"display_name,omitempty"`
	IssuerURL    string   `json:"issuer_url,omitempty"`
	ClientID     string   `json:"client_id,omitempty"`
	ClientSecret string   `json:"client_secret,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	DisablePKCE  bool     `json:"disable_pkce,omitempty"`
	PasswordFlow bool     `json:"password_flow,omitempty"`
}

type LDAPStrategySettings struct {
	Name         string `json:"name,omitempty"`
	Addr         string `json:"addr,omitempty"`
	BindDN       string `json:"bind_dn,omitempty"`
	BindPassword string `json:"bind_password,omitempty"`
	UserBaseDN   string `json:"user_base_dn,omitempty"`
	UserFilter   string `json:"user_filter,omitempty"`
	GroupBaseDN  string `json:"group_base_dn,omitempty"`
	GroupFilter  string `json:"group_filter,omitempty"`
	TLS          bool   `json:"tls,omitempty"`
	InsecureSkip bool   `json:"insecure_skip,omitempty"`
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

type CapabilityMapping struct {
	Superadmins  []string            `json:"superadmins,omitempty"`
	RoleMapping  map[string][]string `json:"role_mapping,omitempty"`
	ScopeMapping map[string][]string `json:"scope_mapping,omitempty"`
}

// UserSyncSettings holds every configured user-sync source. Top-level on
// purpose (not nested under a single LDAP strategy) so the same install
// can pull from multiple LDAP servers, and so future sources (SCIM,
// CSV upload, etc.) plug in at the same level.
type UserSyncSettings struct {
	Sources []SyncSource `json:"sources,omitempty"`
}

// SyncSource is one user provider that pika reconciles its local user
// table against. Each source's (provider, subject) tuples form a
// namespace inside user_identities — changing a source's ID after rows
// exist will orphan those rows, which is intentional: it's how an admin
// "removes" an LDAP source without losing audit history.
type SyncSource struct {
	// ID is the stable handle used as user_identities.provider for users
	// provisioned by this source. Pattern: lowercase, no spaces.
	// Example: "ldap-prod".
	ID string `json:"id"`
	// Name is the human label shown in the UI.
	Name string `json:"name"`
	// Type is the source kind. Currently only "ldap"; reserved for future
	// drivers (e.g. "scim").
	Type string `json:"type"`
	// Enabled gates the periodic schedule and JIT path. When false, the
	// source still exists in settings but neither the cron nor login
	// auto-provisioning will use it.
	Enabled bool `json:"enabled"`
	// LDAP is the source-specific config when Type="ldap". Other Type
	// values would carry their own pointer here in the future.
	LDAP *LDAPSyncSpec `json:"ldap,omitempty"`
	// Schedule controls the periodic batch sync. Manual ("Sync now") and
	// JIT (first-login) paths run regardless of schedule.
	Schedule SyncSchedule `json:"schedule"`
	// OnMissing decides what happens to local users provisioned by this
	// source who are no longer returned by the search filter.
	//   "disable" — set users.disabled=true (default; reversible)
	//   "ignore"  — leave the user row alone
	OnMissing string `json:"on_missing,omitempty"`
}

// SyncSchedule controls when the periodic sync goroutine fires.
type SyncSchedule struct {
	// Mode: "manual" (no ticker) or "interval" (every IntervalMinutes).
	Mode string `json:"mode"`
	// IntervalMinutes is the cadence when Mode="interval". Minimum 1.
	IntervalMinutes int `json:"interval_minutes,omitempty"`
}

// LDAPSyncSpec holds the LDAP-specific fields of a SyncSource: how to
// connect, what to search for, and how to map LDAP attributes onto
// pika's user fields.
//
// Keeping connection params here (rather than referencing the login-side
// AuthSettings.LDAP) lets sync run against a different bind/base than
// the login flow if an operator wants e.g. a service account with broader
// search rights.
type LDAPSyncSpec struct {
	Address      string `json:"address"`
	TLS          bool   `json:"tls,omitempty"`
	InsecureSkip bool   `json:"insecure_skip,omitempty"`
	BindDN       string `json:"bind_dn"`
	BindPassword string `json:"bind_password,omitempty"`

	// UserBaseDN is the search root, e.g. "ou=people,dc=example,dc=com".
	UserBaseDN string `json:"user_base_dn"`
	// UserFilter is an LDAP search filter, e.g. "(objectClass=person)".
	// Defaults to "(objectClass=*)" when empty.
	UserFilter string `json:"user_filter,omitempty"`
	// PageSize controls paged search; 0 = library default (500).
	PageSize uint32 `json:"page_size,omitempty"`

	// Attributes maps LDAP attribute names onto pika user fields. Every
	// field is optional; an empty value means "don't read this".
	Attributes LDAPAttributeMap `json:"attributes"`

	// GroupPermissions maps an LDAP group value (verbatim, as it appears
	// in the user's Attributes.Groups attribute — typically a full DN for
	// memberOf) onto a list of pika permission IDs to grant.
	//
	// At sync time, each user's groups are looked up here; the union of
	// matched permission IDs is written to user_permissions with
	// source=<SyncSource.ID>. Permissions assigned via the admin UI
	// (source='local') are untouched.
	GroupPermissions map[string][]string `json:"group_permissions,omitempty"`
}

// LDAPAttributeMap binds LDAP attribute names to pika user fields. Each
// non-empty field tells the sync engine "read this LDAP attribute and
// store its (first) value in the matching pika field". Multi-valued
// attributes (groups) take the full list.
type LDAPAttributeMap struct {
	// Username is the LDAP attribute used as the pika username.
	// Common: "uid" (OpenLDAP), "sAMAccountName" (AD), "cn".
	Username string `json:"username"`
	// Subject is the stable provider-side identifier stored in
	// user_identities.subject. Defaults to the value of Username when
	// empty. For directories that expose a stable opaque ID, prefer
	// "entryUUID" (OpenLDAP) or "objectGUID" (AD).
	Subject string `json:"subject,omitempty"`
	// Email is the LDAP attribute used as users.email.
	// Common: "mail".
	Email string `json:"email,omitempty"`
	// DisplayName is the LDAP attribute used as users.display_name.
	// Common: "displayName", "cn", "gecos".
	DisplayName string `json:"display_name,omitempty"`
	// GivenName / Surname are read for use as fallbacks when DisplayName
	// is missing — DisplayName takes precedence; if absent and these
	// are set, sync uses "GivenName Surname".
	GivenName string `json:"given_name,omitempty"`
	Surname   string `json:"surname,omitempty"`
	// Groups is a multi-valued attribute (typically "memberOf") whose
	// values feed GroupPermissions.
	Groups string `json:"groups,omitempty"`
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
	if s.LDAP != nil {
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
