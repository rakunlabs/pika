package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/service"
)

// listPermissions returns all defined permissions.
func (a *api) listPermissions(c *ada.Context) error {
	perms, err := a.svc.ListPermissions(c.Request.Context())
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(perms)
}

// createPermission creates a new permission.
func (a *api) createPermission(c *ada.Context) error {
	var req service.CreatePermissionRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	perm, err := a.svc.CreatePermission(c.Request.Context(), &req)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusCreated).SendJSON(perm)
}

// updatePermission updates a permission.
func (a *api) updatePermission(c *ada.Context) error {
	id := c.Request.PathValue("*")

	var req service.UpdatePermissionRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if err := a.svc.UpdatePermission(c.Request.Context(), id, &req); err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "permission updated"})
}

// deletePermission deletes a permission.
func (a *api) deletePermission(c *ada.Context) error {
	id := c.Request.PathValue("*")

	if err := a.svc.DeletePermission(c.Request.Context(), id); err != nil {
		return err
	}

	return c.SendNoContent()
}

// getUserPermissions returns permissions assigned to a user.
func (a *api) getUserPermissions(c *ada.Context) error {
	userID := c.Request.PathValue("*")

	perms, err := a.svc.GetUserPermissions(c.Request.Context(), userID)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(perms)
}

// setUserPermissions replaces all permissions for a user.
func (a *api) setUserPermissions(c *ada.Context) error {
	userID := c.Request.PathValue("*")

	var req struct {
		PermissionIDs []string `json:"permission_ids"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if err := a.svc.SetUserPermissions(c.Request.Context(), userID, req.PermissionIDs); err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "user permissions updated"})
}

// withPerm wraps a handler with a capability check against the resolved
// capability set in the request context (set by authx.CapMiddleware).
func (a *api) withPerm(need string, handler func(*ada.Context) error) func(*ada.Context) error {
	return func(c *ada.Context) error {
		caps := service.CapabilitiesFromContext(c.Request.Context())
		if !caps.Has(need) {
			return fmt.Errorf("capability %q required: %w", need, service.ErrForbidden)
		}
		return handler(c)
	}
}

// PathExtractor pulls the path-to-check out of an incoming request. The
// returned path is matched against the user's per-key glob patterns (if any).
type PathExtractor func(*ada.Context) string

// pathFromWildcard is the common case: take the trailing path segment
// captured by a `*` route pattern (PathValue("*")). Empty path means the
// root listing, which the ancestor variant always allows when patterns
// exist.
func pathFromWildcard(c *ada.Context) string {
	return c.Request.PathValue("*")
}

// withPermPath wraps a handler with both a capability check and a per-key
// path-pattern check. When the user's grant for `need` carries no patterns,
// behavior is identical to withPerm. When patterns exist, the request's
// path (extracted by `extract`) must match at least one of them.
//
// `ancestor` controls whether the path may be a prefix of a matching path
// (used by directory-listing routes so a user with `configs/team-a/**` can
// still navigate the root and intermediate folders).
func (a *api) withPermPath(need string, extract PathExtractor, ancestor bool, handler func(*ada.Context) error) func(*ada.Context) error {
	if extract == nil {
		extract = pathFromWildcard
	}
	return func(c *ada.Context) error {
		ctx := c.Request.Context()
		caps := service.CapabilitiesFromContext(ctx)
		if !caps.Has(need) {
			return fmt.Errorf("capability %q required: %w", need, service.ErrForbidden)
		}
		patterns := service.CapabilityPatternsFromContext(ctx)
		path := extract(c)
		var allowed bool
		if ancestor {
			allowed = patterns.AllowsAncestor(need, path)
		} else {
			allowed = patterns.Allows(need, path)
		}
		if !allowed {
			return fmt.Errorf("path %q not permitted for %q: %w", path, need, service.ErrForbidden)
		}
		return handler(c)
	}
}

// writeJSONError writes a JSON error response with the given status code.
func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"code":%q,"message":%q}`, code, message)
}

// authBearerOrSession authenticates a request to one of the public data
// endpoints (/data/*, /raw/*) which live on the unprotected mux (mData) and
// therefore have no middleware in the chain.
//
// Two equivalent credentials are accepted:
//
//  1. An `Authorization: Bearer <api-token>` header — validated against the
//     tokens table with scope+operation. This is the contract documented for
//     external callers and copy-paste URLs from the UI's SettingsPanel.
//
//  2. The browser session cookie issued by /login/* — resolved through the
//     auth manager, then checked against the user's capability set and the
//     per-key path-pattern restrictions. This lets a logged-in UI user open
//     the same URL they copied without needing to mint an API token first.
//
// `tokenScope` is the path passed to ValidateToken (e.g. the file key, or
// "raw/<key>" for raw mounts). `patternPath` is the path used for session
// capability-pattern matching (use "" to skip path-pattern enforcement,
// mirroring withPerm vs withPermPath semantics on the protected mux).
func (a *api) authBearerOrSession(c *ada.Context, tokenScope, op, capKey, patternPath string) error {
	tokenRaw := c.Request.Header.Get("Authorization")
	if len(tokenRaw) > 7 && tokenRaw[:7] == "Bearer " {
		tokenRaw = tokenRaw[7:]
	}

	if tokenRaw != "" {
		// Bearer path: external API tokens — unchanged behavior.
		return a.svc.ValidateToken(c.Request.Context(), tokenRaw, tokenScope, op)
	}

	// No Bearer header: try to resolve a UI session cookie.
	if a.mgr == nil {
		return errors.Join(errors.New("missing authentication token"), service.ErrUnauthorized)
	}
	id, capKeys, _, _, patternMap := a.mgr.ResolveRequest(c.Request)
	if id == nil {
		// Truly anonymous — surface the original error so external clients
		// keep seeing the familiar message.
		return errors.Join(errors.New("missing authentication token"), service.ErrUnauthorized)
	}

	caps := service.Capabilities(capKeys)
	if !caps.Has(capKey) {
		return fmt.Errorf("capability %q required: %w", capKey, service.ErrForbidden)
	}

	if patternPath != "" {
		if !service.CapabilityPatterns(patternMap).Allows(capKey, patternPath) {
			return fmt.Errorf("path %q not permitted for %q: %w", patternPath, capKey, service.ErrForbidden)
		}
	}

	return nil
}
