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
// Behavior by identity source:
//
//	local:
//	    username = id.Subject (already the pika username)
//	    caps = DB permissions (is_superadmin shortcut, else users_permissions)
//	         ∪ Superadmins allowlist match
//	         ∪ RoleMapping / ScopeMapping (rarely populated for local)
//
//	external (oauth2, header, ldap, ...):
//	    Resolve via user_identities → users. The session store already did
//	    this at cookie-issue time via FindOrCreateExternalUser, so the
//	    lookup is a simple index hit.
//	    caps = DB permissions for that users row
//	         ∪ Superadmins allowlist match (on id.Subject, i.e. provider's sub)
//	         ∪ RoleMapping[id.Roles] ∪ ScopeMapping[id.Scopes]
//
// All three grant sources are unioned (deduplicated). Superadmin bit on the
// users row short-circuits to the full capability set regardless of other
// inputs.
func (r *CapResolver) resolve(ctx context.Context, id *identity.Identity) (service.Capabilities, string, string, map[string][]string) {
	username := id.Subject
	userID := ""

	// Best-effort: always try to resolve the pika user ID for the
	// identity, even for superadmins. Per-user resources like
	// /api/v1/me/preferences read service.UserIDFromContext to scope
	// their reads/writes, and bouncing them with "no user in context"
	// just because the caller is a superadmin would be surprising. The
	// resolver still returns the full capability set below.
	resolveUserID := func() {
		if r.svc == nil || userID != "" {
			return
		}
		if id.Provider == "" || id.Provider == "local" {
			if user, err := r.svc.GetUserByUsername(ctx, id.Subject); err == nil && user != nil {
				userID = user.ID
			}
			return
		}
		if ui, err := r.svc.GetUserByIdentity(ctx, id.Provider, id.Subject); err == nil && ui != nil {
			username = ui.Username
			userID = ui.ID
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

	// 1. Superadmin allowlist — Subject fast path, bypasses everything.
	// No path patterns: superadmins are unrestricted by definition.
	for _, admin := range r.settings.Superadmins {
		if admin == id.Subject {
			resolveUserID()
			return service.Capabilities(service.KnownCapabilityKeys()), username, userID, nil
		}
	}

	// 2. DB-backed permissions. The path differs by provider because the
	// lookup key differs: local uses username, external uses
	// (provider, subject) → user.
	var patterns map[string][]string
	if r.svc != nil {
		if id.Provider == "" || id.Provider == "local" {
			keys, isSuper, _, err := r.svc.ResolveLocalCapabilityKeys(ctx, id.Subject)
			if err == nil {
				if isSuper {
					resolveUserID()
					return service.Capabilities(service.KnownCapabilityKeys()), username, userID, nil
				}
				add(keys)
				if user, err := r.svc.GetUserByUsername(ctx, id.Subject); err == nil && user != nil {
					userID = user.ID
					if pats, err := r.svc.ResolveUserCapabilityPatterns(ctx, user.ID); err == nil {
						patterns = pats
					}
				}
			}
		} else {
			if ui, err := r.svc.GetUserByIdentity(ctx, id.Provider, id.Subject); err == nil && ui != nil {
				username = ui.Username
				userID = ui.ID
				if ui.IsSuperadmin {
					return service.Capabilities(service.KnownCapabilityKeys()), username, userID, nil
				}
				if keys, _, _, err := r.svc.ResolveUserCapabilityKeysByID(ctx, ui.ID); err == nil {
					add(keys)
				}
				if pats, err := r.svc.ResolveUserCapabilityPatterns(ctx, ui.ID); err == nil {
					patterns = pats
				}
			}
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
