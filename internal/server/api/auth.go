package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/query"
)

// listUsers returns users with optional pagination, sorting and filtering.
//
// Supported query parameters (via github.com/rakunlabs/query):
//
//	_limit=20         pagination page size
//	_offset=0         pagination offset
//	_sort=username    sort field (prefix with - for desc, e.g. -created_at)
//	username[like]=   filter by username (LIKE)
//	disabled=0        filter by status
func (a *api) listUsers(c *ada.Context) error {
	q, err := query.Parse(c.Request.URL.RawQuery)
	if err != nil {
		return errors.Join(fmt.Errorf("invalid query parameters: %w", err), service.ErrBadRequest)
	}

	users, total, err := a.svc.ListUsers(c.Request.Context(), q)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(struct {
		Users []service.UserInfo `json:"users"`
		Total int64              `json:"total"`
	}{
		Users: users,
		Total: total,
	})
}

// createUser creates a new user.
func (a *api) createUser(c *ada.Context) error {
	var req service.CreateUserRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	user, err := a.svc.CreateUser(c.Request.Context(), &req)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusCreated).SendJSON(user)
}

// getUser retrieves a user by ID.
func (a *api) getUser(c *ada.Context) error {
	id := c.Request.PathValue("*")

	user, err := a.svc.GetUser(c.Request.Context(), id)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(user)
}

// updateUser updates a user's properties.
func (a *api) updateUser(c *ada.Context) error {
	id := c.Request.PathValue("*")

	var req service.UpdateUserRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if err := a.svc.UpdateUser(c.Request.Context(), id, &req); err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "user updated"})
}

// deleteUser deletes a user by ID.
func (a *api) deleteUser(c *ada.Context) error {
	id := c.Request.PathValue("*")

	if err := a.svc.DeleteUser(c.Request.Context(), id); err != nil {
		return err
	}

	return c.SendNoContent()
}

// kickUser deletes all active sessions for a user, forcing re-login.
func (a *api) kickUser(c *ada.Context) error {
	id := c.Request.PathValue("*")

	if err := a.svc.KickUser(c.Request.Context(), id); err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "user sessions terminated"})
}
