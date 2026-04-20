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
			caps, username, userID := r.resolve(req.Context(), id)

			ctx := service.WithCapabilities(req.Context(), caps)
			ctx = service.WithUserInfo(ctx, username, userID)
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
func (r *CapResolver) resolve(ctx context.Context, id *identity.Identity) (service.Capabilities, string, string) {
	username := id.Subject
	userID := ""

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
	for _, admin := range r.settings.Superadmins {
		if admin == id.Subject {
			return service.Capabilities(service.KnownCapabilityKeys()), username, userID
		}
	}

	// 2. DB-backed permissions. The path differs by provider because the
	// lookup key differs: local uses username, external uses
	// (provider, subject) → user.
	if r.svc != nil {
		if id.Provider == "" || id.Provider == "local" {
			keys, isSuper, _, err := r.svc.ResolveLocalCapabilityKeys(ctx, id.Subject)
			if err == nil {
				if isSuper {
					return service.Capabilities(service.KnownCapabilityKeys()), username, userID
				}
				add(keys)
			}
		} else {
			if ui, err := r.svc.GetUserByIdentity(ctx, id.Provider, id.Subject); err == nil && ui != nil {
				username = ui.Username
				userID = ui.ID
				if ui.IsSuperadmin {
					return service.Capabilities(service.KnownCapabilityKeys()), username, userID
				}
				if keys, _, _, err := r.svc.ResolveUserCapabilityKeysByID(ctx, ui.ID); err == nil {
					add(keys)
				}
			}
		}
	}

	// 3. Declarative role / scope mapping — useful for giving external
	// users permissions without creating per-user rows in the admin UI.
	for _, role := range id.Roles {
		add(r.settings.RoleMapping[role])
	}
	for _, sc := range id.Scopes {
		add(r.settings.ScopeMapping[sc])
	}

	return service.Capabilities(out), username, userID
}
