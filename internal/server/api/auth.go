package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/service"
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

// listUsers returns all users.
func (a *api) listUsers(c *ada.Context) error {
	users, _, err := a.svc.ListUsers(c.Request.Context(), nil)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(users)
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
