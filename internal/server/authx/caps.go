package authx

import (
	"context"
	"net/http"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/identity"

	"github.com/rakunlabs/pika/internal/service"
)

// CapResolver turns a per-request *identity.Identity into a capability set and
// stores it in the context under service.WithCapabilities.
type CapResolver struct {
	svc      *service.Service
	settings service.CapabilityMapping
	// rolePaths maps an identity provider name to the dotted claim paths
	// roles are harvested from (see service.HarvestRoles). It lets OAuth2
	// providers source roles from nested claims — e.g. Keycloak's
	// realm_access.roles / resource_access.*.roles — instead of only the
	// flat "roles" claim ada extracts into Identity.Roles. A provider with
	// no entry (nil map, non-OAuth2 strategies) falls back to Identity.Roles
	// alone.
	rolePaths map[string][]string
}

// NewCapResolver constructs a resolver bound to a service and a (snapshot of)
// the capability mapping from AuthSettings. rolePaths carries the per-provider
// role-claim paths (AuthSettings.OAuth2RolesClaims) so external roles can be
// read from nested claims.
func NewCapResolver(svc *service.Service, m service.CapabilityMapping, rolePaths map[string][]string) *CapResolver {
	return &CapResolver{svc: svc, settings: m, rolePaths: rolePaths}
}

// Middleware is an ada.MiddlewareFunc that runs after auth.Require().
// Missing identity → 401 (should never happen past Require, defensive).
func (r *CapResolver) Middleware() ada.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			r.serveHTTP(next, w, req)
		})
	}
}

func (r *CapResolver) serveHTTP(next http.Handler, w http.ResponseWriter, req *http.Request) {
	id := identity.FromContext(req.Context())
	if id == nil {
		writeJSONErr(w, http.StatusUnauthorized, "no_identity", "not authenticated")
		return
	}
	caps, username, userID, patterns := r.resolve(req.Context(), id)

	ctx := service.WithCapabilities(req.Context(), caps)
	ctx = service.WithUserInfo(ctx, username, userID)
	ctx = service.WithCapabilityPatterns(ctx, patterns)
	next.ServeHTTP(w, req.WithContext(ctx))
}

// resolve computes the capability set for an authenticated identity and
// returns the pika username + user_id that back it (for context-planting).
//
// The resolver is now uniform across every strategy: it reads the
// already-resolved pika users.id from the Identity claim under
// PikaUserIDClaim and looks up the user by that id. SessionStore.Save
// stamps the claim once per session, after running the
// provider-specific (Provider, Subject) → users.id dispatch
// (resolveSessionUser). That stamp eliminates the dispatch from the
// per-request hot path AND keeps every strategy (local, passkey,
// oauth2, header, ...) on the same code path — adding a new strategy
// no longer requires touching this file. See TestCapResolver_*.
//
// Grant sources (all unioned, dedup'd):
//
//  1. Superadmin allowlist match on id.Subject (operator-set; for
//     external IdPs Subject is the OIDC sub, for local it's the
//     username — operators put either form). Returns the full
//     known-key set; superadmins are unrestricted by definition.
//  2. DB-backed permissions for the user_id resolved at login time:
//     is_superadmin column on users (full set), else per-user
//     permission bundle grants.
//  3. Declarative RoleMapping[roles] / ScopeMapping[id.Scopes] —
//     useful for granting external users permissions without creating
//     per-user DB rows. `roles` is Identity.Roles (ada's flat "roles"
//     claim) unioned with roles harvested from the provider's configured
//     nested claim paths (effectiveRoles → service.HarvestRoles), so
//     Keycloak realm_access.roles / resource_access.*.roles participate
//     too. The mapped VALUES are pika Permission bundle Keys (not raw
//     capability keys): each is expanded to the bundle's capability keys
//     and path patterns, then unioned with the user's DB-assigned bundles
//     through the same "unrestricted-wins" rule. An unknown/deleted bundle
//     key grants nothing (fail-closed).
//
// If the Identity carries no PikaUserIDClaim (e.g. a request that
// arrived before SessionStore.Save ran, or a non-session strategy
// that hasn't been wired through resolveSessionUser yet), the
// per-user DB step is skipped — the user still gets Superadmin/Role/
// Scope caps when those apply.
func (r *CapResolver) resolve(ctx context.Context, id *identity.Identity) (service.Capabilities, string, string, map[string][]string) {
	rep := r.resolveDetailed(ctx, id)
	return service.Capabilities(rep.Capabilities), rep.Username, rep.UserID, rep.Patterns
}

// CapSource records where one granted capability key came from, so admin
// tooling can answer "why does this user have files.write?".
type CapSource struct {
	Capability string `json:"capability"`
	// Kind is one of: "superadmin", "db_bundle", "role", "scope".
	Kind   string `json:"kind"`
	Bundle string `json:"bundle,omitempty"` // permission bundle key
	Role   string `json:"role,omitempty"`   // source role (Kind=="role")
	Scope  string `json:"scope,omitempty"`  // source scope (Kind=="scope")
}

// EffectiveReport is the full, introspectable resolution of an identity's
// capabilities — the same computation the request hot-path uses, plus the
// provenance and the deny overlay the admin UI needs to display and edit.
type EffectiveReport struct {
	Username         string              `json:"username"`
	UserID           string              `json:"user_id"`
	Online           bool                `json:"online"`
	Superadmin       bool                `json:"superadmin"`
	SuperadminReason string              `json:"superadmin_reason,omitempty"` // "allowlist" | "column"
	Roles            []string            `json:"roles"`
	Scopes           []string            `json:"scopes"`
	Capabilities     []string            `json:"capabilities"` // effective (post-deny)
	Patterns         map[string][]string `json:"patterns,omitempty"`
	Sources          []CapSource         `json:"sources"` // provenance (pre-deny)
	Denied           []string            `json:"denied"`  // configured deny overlay
}

// resolveDetailed is the single source of truth for capability resolution.
// resolve() wraps it for the hot path; the admin "effective permissions"
// endpoint surfaces the whole report. Grant sources mirror the documentation
// on resolve()'s former body: superadmin (allowlist/column), DB-assigned
// bundles, and RoleMapping/ScopeMapping. A per-user deny overlay
// (user.DeniedCapabilities) is then subtracted from the final set — except
// for the operator Superadmins allowlist, which is an explicit break-glass
// escape hatch and is never reduced by deny.
func (r *CapResolver) resolveDetailed(ctx context.Context, id *identity.Identity) *EffectiveReport {
	// API tokens are resolved from their own scope model and share none
	// of the machinery below. This branch must come first: a token is
	// named by its operator, and the superadmin allowlist matches on
	// id.Subject — so a token merely named after a superadmin would
	// otherwise be handed the full capability set.
	if id.Provider == service.TokenProvider {
		return tokenReport(id)
	}

	rep := &EffectiveReport{
		Username:     id.Subject,
		Roles:        []string{},
		Scopes:       []string{},
		Capabilities: []string{},
		Sources:      []CapSource{},
		Denied:       []string{},
	}
	userID := identity.Claim[string](id, PikaUserIDClaim)
	rep.UserID = userID

	// Look the user up once and reuse for every subsequent decision.
	var user *service.UserInfo
	if r.svc != nil && userID != "" {
		if u, err := r.svc.GetUserByID(ctx, userID); err == nil && u != nil {
			user = u
			rep.Username = u.Username
		}
	}

	// Per-user deny overlay travels on the loaded user (no extra DB read).
	var denied []string
	if user != nil && len(user.DeniedCapabilities) > 0 {
		denied = user.DeniedCapabilities
		rep.Denied = append([]string(nil), denied...)
	}

	roles := r.effectiveRoles(id)
	if len(roles) > 0 {
		rep.Roles = roles
	}
	if len(id.Scopes) > 0 {
		rep.Scopes = id.Scopes
	}

	// 1. Superadmin allowlist — break-glass escape hatch, exempt from deny.
	for _, admin := range r.settings.Superadmins {
		if admin == id.Subject {
			rep.Superadmin = true
			rep.SuperadminReason = "allowlist"
			rep.Capabilities = service.KnownCapabilityKeys()
			rep.Sources = superadminSources(rep.Capabilities)
			return rep
		}
	}

	// 2. The is_superadmin column — full set, but deny still applies.
	if user != nil && user.IsSuperadmin {
		rep.Superadmin = true
		rep.SuperadminReason = "column"
		keys := service.KnownCapabilityKeys()
		rep.Sources = superadminSources(keys)
		keys, _ = subtractDenied(keys, nil, denied)
		rep.Capabilities = keys
		return rep
	}

	// 3. Union DB-assigned bundles + role/scope-mapped bundles, tracking
	// provenance for every capability key as we go.
	var bundles []service.Permission
	var sources []CapSource

	if r.svc != nil && user != nil {
		if dbB, err := r.svc.GetUserPermissions(ctx, user.ID); err == nil {
			bundles = append(bundles, dbB...)
			for _, p := range dbB {
				for _, k := range p.Keys {
					sources = append(sources, CapSource{Capability: k, Kind: "db_bundle", Bundle: p.Key})
				}
			}
		}
	}

	if r.svc != nil && (len(roles) > 0 || len(id.Scopes) > 0) {
		if mb, err := r.svc.PermissionsByKeys(ctx, r.mappedPermissionKeys(roles, id.Scopes)); err == nil {
			bundles = append(bundles, mb...)
			byKey := make(map[string]service.Permission, len(mb))
			for _, p := range mb {
				byKey[p.Key] = p
			}
			for _, role := range roles {
				for _, bk := range r.settings.RoleMapping[role] {
					if p, ok := byKey[bk]; ok {
						for _, k := range p.Keys {
							sources = append(sources, CapSource{Capability: k, Kind: "role", Role: role, Bundle: p.Key})
						}
					}
				}
			}
			for _, sc := range id.Scopes {
				for _, bk := range r.settings.ScopeMapping[sc] {
					if p, ok := byKey[bk]; ok {
						for _, k := range p.Keys {
							sources = append(sources, CapSource{Capability: k, Kind: "scope", Scope: sc, Bundle: p.Key})
						}
					}
				}
			}
		}
	}

	keys, patterns := service.CapabilitiesFromBundles(bundles)
	keys, patterns = subtractDenied(keys, patterns, denied)
	if len(keys) > 0 {
		rep.Capabilities = keys
	}
	rep.Patterns = patterns
	if len(sources) > 0 {
		rep.Sources = sources
	}
	return rep
}

// superadminSources tags every known capability key as superadmin-granted.
func superadminSources(keys []string) []CapSource {
	out := make([]CapSource, 0, len(keys))
	for _, k := range keys {
		out = append(out, CapSource{Capability: k, Kind: "superadmin"})
	}
	return out
}

// subtractDenied removes the denied capability keys from the resolved set and
// drops their path patterns. Returns the inputs untouched when nothing is
// denied (the common case).
func subtractDenied(keys []string, patterns map[string][]string, denied []string) ([]string, map[string][]string) {
	if len(denied) == 0 {
		return keys, patterns
	}
	deny := make(map[string]struct{}, len(denied))
	for _, d := range denied {
		deny[d] = struct{}{}
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if _, blocked := deny[k]; blocked {
			continue
		}
		out = append(out, k)
	}
	if patterns != nil {
		for k := range deny {
			delete(patterns, k)
		}
		if len(patterns) == 0 {
			patterns = nil
		}
	}
	return out, patterns
}

// effectiveRoles returns the role strings used for RoleMapping lookups: the
// roles ada already parsed into Identity.Roles (the flat "roles" claim)
// unioned with any roles harvested from the provider's configured nested claim
// paths (Keycloak realm/client roles, etc.). When the provider has no path
// config, this is just Identity.Roles.
func (r *CapResolver) effectiveRoles(id *identity.Identity) []string {
	paths := r.rolePaths[id.Provider]
	if len(paths) == 0 {
		return id.Roles
	}
	harvested := service.HarvestRoles(id.Claims, paths)
	if len(harvested) == 0 {
		return id.Roles
	}
	if len(id.Roles) == 0 {
		return harvested
	}
	seen := make(map[string]struct{}, len(id.Roles)+len(harvested))
	out := make([]string, 0, len(id.Roles)+len(harvested))
	for _, src := range [][]string{id.Roles, harvested} {
		for _, role := range src {
			if role == "" {
				continue
			}
			if _, dup := seen[role]; dup {
				continue
			}
			seen[role] = struct{}{}
			out = append(out, role)
		}
	}
	return out
}

// mappedPermissionKeys collects the pika Permission bundle Keys an identity
// is entitled to through its roles and scopes, deduplicated. The values stored
// in RoleMapping/ScopeMapping are bundle Keys; resolve() expands them.
func (r *CapResolver) mappedPermissionKeys(roles, scopes []string) []string {
	seen := make(map[string]struct{})
	var out []string
	collect := func(ks []string) {
		for _, k := range ks {
			if k == "" {
				continue
			}
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}
	for _, role := range roles {
		collect(r.settings.RoleMapping[role])
	}
	for _, sc := range scopes {
		collect(r.settings.ScopeMapping[sc])
	}
	return out
}
