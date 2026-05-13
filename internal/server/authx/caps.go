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
}

// NewCapResolver constructs a resolver bound to a service and a (snapshot of)
// the capability mapping from AuthSettings.
func NewCapResolver(svc *service.Service, m service.CapabilityMapping) *CapResolver {
	return &CapResolver{svc: svc, settings: m}
}

// Middleware is an ada.MiddlewareFunc that runs after auth.Require().
// Missing identity → 401 (should never happen past Require, defensive).
func (r *CapResolver) Middleware() ada.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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
		})
	}
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
//  3. Declarative RoleMapping[id.Roles] / ScopeMapping[id.Scopes] —
//     useful for granting external users baseline caps without
//     creating per-user DB rows. These are unrestricted (no path
//     scoping); any key granted here strips its pattern from the DB
//     patterns map so the broader grant wins.
//
// If the Identity carries no PikaUserIDClaim (e.g. a request that
// arrived before SessionStore.Save ran, or a non-session strategy
// that hasn't been wired through resolveSessionUser yet), the
// per-user DB step is skipped — the user still gets Superadmin/Role/
// Scope caps when those apply.
func (r *CapResolver) resolve(ctx context.Context, id *identity.Identity) (service.Capabilities, string, string, map[string][]string) {
	username := id.Subject
	userID := identity.Claim[string](id, PikaUserIDClaim)

	// Look the user up once and reuse for every subsequent decision.
	// Failures (deleted user, DB error) leave `user` nil and we still
	// honor Superadmin/Role/Scope mappings below.
	var user *service.UserInfo
	if r.svc != nil && userID != "" {
		if u, err := r.svc.GetUserByID(ctx, userID); err == nil && u != nil {
			user = u
			username = u.Username
		}
	}

	seen := make(map[string]struct{})
	var out []string
	add := func(keys []string) {
		for _, k := range keys {
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}

	// 1. Superadmin allowlist — operator-controlled escape hatch. We
	// short-circuit to the full known-key set; no patterns because
	// superadmins are unrestricted by definition.
	for _, admin := range r.settings.Superadmins {
		if admin == id.Subject {
			return service.Capabilities(service.KnownCapabilityKeys()), username, userID, nil
		}
	}

	// 2. DB-backed permissions for the resolved user. The is_superadmin
	// shortcut on the users row produces the full known-key set; else
	// we union the per-user permission grants.
	var patterns map[string][]string
	if user != nil {
		if user.IsSuperadmin {
			return service.Capabilities(service.KnownCapabilityKeys()), username, userID, nil
		}
		if keys, _, _, err := r.svc.ResolveUserCapabilityKeysByID(ctx, user.ID); err == nil {
			add(keys)
		}
		if pats, err := r.svc.ResolveUserCapabilityPatterns(ctx, user.ID); err == nil {
			patterns = pats
		}
	}

	// 3. Declarative role / scope mapping — useful for giving external
	// users permissions without creating per-user rows in the admin UI.
	// Caps from this source are unrestricted (we have nowhere to attach
	// patterns in the role/scope mapping config), which means: a user
	// who happens to also be in a mapped role for the same key as a
	// pattern-scoped permission gets unrestricted access. This matches
	// the additive semantics — declared mappings widen, never narrow.
	if len(id.Roles) > 0 || len(id.Scopes) > 0 {
		widened := make(map[string]struct{})
		for _, role := range id.Roles {
			ks := r.settings.RoleMapping[role]
			add(ks)
			for _, k := range ks {
				widened[k] = struct{}{}
			}
		}
		for _, sc := range id.Scopes {
			ks := r.settings.ScopeMapping[sc]
			add(ks)
			for _, k := range ks {
				widened[k] = struct{}{}
			}
		}
		// Strip patterns for any key that role/scope mapping just granted
		// unrestricted — preserves the "broad grant wins" union rule.
		if len(patterns) > 0 && len(widened) > 0 {
			for k := range widened {
				delete(patterns, k)
			}
		}
	}

	return service.Capabilities(out), username, userID, patterns
}
