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

// writeJSONError writes a JSON error response with the given status code.
func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"code":%q,"message":%q}`, code, message)
}
