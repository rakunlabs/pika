package api

import (
	"errors"
	"net/http"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/server/authx"
	"github.com/rakunlabs/pika/internal/service"
)

// getUserEffectivePermissions returns a target user's resolved capabilities
// with provenance — what they can actually do right now and where each
// capability comes from (role mapping, scope mapping, db bundle, or
// superadmin), plus the per-user deny overlay. Sourced from the user's
// most-recent active session so IdP-derived roles participate; falls back to a
// DB-only view when the user is offline.
func (a *api) getUserEffectivePermissions(c *ada.Context) error {
	userID := c.Request.PathValue("user")
	rep, err := a.mgr.EffectiveForUser(c.Request.Context(), userID)
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(rep)
}

// getUserIdentities lists a user's linked external identities (OAuth2/LDAP/
// Header). Empty for a pure local-password user. Lets the admin UI show
// "linked accounts" and tell external users apart from local ones.
func (a *api) getUserIdentities(c *ada.Context) error {
	userID := c.Request.PathValue("user")
	idents, err := a.svc.ListUserIdentities(c.Request.Context(), userID)
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(struct {
		Identities []service.UserIdentity `json:"identities"`
	}{Identities: idents})
}

// listUserSessions returns admin-safe views of a user's active sessions. The
// raw session ID (== the live cookie secret) is never exposed; each row
// carries a hash handle used for revocation.
func (a *api) listUserSessions(c *ada.Context) error {
	userID := c.Request.PathValue("user")
	views, err := a.mgr.ListUserSessions(c.Request.Context(), userID, c.Request)
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(struct {
		Sessions []authx.SessionView `json:"sessions"`
	}{Sessions: views})
}

// revokeUserSession terminates a single session by its hash handle.
func (a *api) revokeUserSession(c *ada.Context) error {
	userID := c.Request.PathValue("user")
	handle := c.Request.PathValue("handle")
	if err := a.mgr.RevokeUserSession(c.Request.Context(), userID, handle); err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "session revoked"})
}

// setUserDeniedPermissions replaces a user's deny overlay — capability keys
// that are subtracted from their resolved set regardless of grant source.
// This is how an admin revokes a single IdP-role-derived capability for one
// user without editing the global RoleMapping. Applies on the user's next
// request (no re-login needed).
func (a *api) setUserDeniedPermissions(c *ada.Context) error {
	userID := c.Request.PathValue("user")
	var req struct {
		CapabilityKeys []string `json:"capability_keys"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	if err := a.svc.SetUserDeniedCapabilities(c.Request.Context(), userID, req.CapabilityKeys); err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "denied capabilities updated"})
}
