package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/query"
)

// loginRequest is the request body for login.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// login authenticates a user and sets a session cookie.
func (a *api) login(c *ada.Context) error {
	var req loginRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if req.Username == "" || req.Password == "" {
		return errors.Join(fmt.Errorf("username and password are required"), service.ErrBadRequest)
	}

	userInfo, err := a.svc.Authenticate(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		return err
	}

	cookie, err := a.sessionStore.Create(userInfo.ID, userInfo.Username)
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	http.SetCookie(c.Response, cookie)

	return c.SetStatus(http.StatusOK).SendJSON(userInfo)
}

// logout clears the session cookie and deletes the session.
func (a *api) logout(c *ada.Context) error {
	cookie, err := c.Request.Cookie(a.sessionStore.CookieName())
	if err == nil && cookie.Value != "" {
		a.sessionStore.Delete(cookie.Value)
	}

	http.SetCookie(c.Response, a.sessionStore.ClearCookie())

	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "logged out"})
}

// me returns the current authenticated user's info.
func (a *api) me(c *ada.Context) error {
	user := service.UserFromContext(c.Request.Context())

	return c.SetStatus(http.StatusOK).SendJSON(struct {
		User string `json:"user"`
	}{
		User: user,
	})
}

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

// getSetupStatus returns whether initial setup is required (no users exist yet).
func (a *api) getSetupStatus(c *ada.Context) error {
	count, err := a.svc.UserCount(c.Request.Context())
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(struct {
		Required bool `json:"required"`
	}{
		Required: count == 0,
	})
}

// setup creates the first admin user. Only works when zero users exist.
// On success it also creates a session so the user is immediately logged in.
func (a *api) setup(c *ada.Context) error {
	// Check if setup is still available
	count, err := a.svc.UserCount(c.Request.Context())
	if err != nil {
		return err
	}
	if count > 0 {
		return errors.Join(fmt.Errorf("setup already completed"), service.ErrBadRequest)
	}

	var req loginRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if req.Username == "" || req.Password == "" {
		return errors.Join(fmt.Errorf("username and password are required"), service.ErrBadRequest)
	}

	// Create the first user as superadmin
	userInfo, err := a.svc.CreateSetupUser(c.Request.Context(), &service.CreateUserRequest{
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		return err
	}

	// Auto-login: create a session so the user is immediately authenticated
	cookie, err := a.sessionStore.Create(userInfo.ID, userInfo.Username)
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	http.SetCookie(c.Response, cookie)

	return c.SetStatus(http.StatusCreated).SendJSON(userInfo)
}
