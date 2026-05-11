package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/query"
)

// usernameSearchTransform wraps a raw search term with SQL LIKE wildcards
// so the UI can send a plain string (e.g. ?username=admin) instead of
// having to know about %-wildcards. Empty values pass through untouched
// so the cap layer's "missing filter" semantics are preserved.
//
// Backward compatibility: if the caller already provided their own
// wildcard (a literal `%` in the value), we treat the value as a hand-
// crafted pattern and leave it alone. This keeps older curl invocations
// like `?username[like]=%admin%` working unchanged.
func usernameSearchTransform(v string) string {
	if v == "" {
		return v
	}
	if strings.Contains(v, "%") {
		return v
	}
	return "%" + v + "%"
}

// listUsers returns users with optional pagination, sorting and filtering.
//
// Supported query parameters (via github.com/rakunlabs/query):
//
//	_limit=20         pagination page size (default 50, capped server-side)
//	_offset=0         pagination offset
//	_sort=username    sort field (prefix with - for desc, e.g. -created_at)
//	username=admin    case-insensitive substring search on username. The
//	                  server wraps the value with % wildcards and uses
//	                  ILIKE, so `?username=admin` matches "Admin",
//	                  "admin01", "superadmin", etc.
//	disabled=0        filter by status
//	permission_id=ID  filter by users that have the given permission bundle
//	                  granted. Pre-resolved to id[in]=... server-side because
//	                  grants live on the user row (no junction table) and
//	                  the bw query layer cannot join.
func (a *api) listUsers(c *ada.Context) error {
	// Configure the parser declaratively instead of hand-rewriting the
	// URL values. WithSkipExpressionCmp keeps permission_id parseable
	// (so q.GetValue can read it) but stops it from leaking into the
	// storage WHERE clause as an unknown column. WithKey makes the
	// `username` filter ergonomic from the UI side: plain string in,
	// case-insensitive substring match out.
	q, err := query.Parse(
		c.Request.URL.RawQuery,
		query.WithSkipExpressionCmp("permission_id"),
		query.WithKey("username",
			query.KeyOperator(query.OperatorILike),
			query.KeyValueTransform(usernameSearchTransform),
		),
		// Safety umbrella for direct API callers (curl, scripts): never
		// dump the entire user table in one shot. The UI always sends
		// _limit=20 so this is invisible there.
		query.WithDefaultLimit(50),
	)
	if err != nil {
		return errors.Join(fmt.Errorf("invalid query parameters: %w", err), service.ErrBadRequest)
	}

	if permissionID := q.GetValue("permission_id"); permissionID != "" {
		userIDs, err := a.svc.ListUserIDsByPermission(c.Request.Context(), permissionID)
		if err != nil {
			return err
		}
		if len(userIDs) == 0 {
			// Short-circuit: no user holds this permission. Return an
			// empty page without touching the store.
			return c.SetStatus(http.StatusOK).SendJSON(struct {
				Users []service.UserInfo `json:"users"`
				Total int64              `json:"total"`
			}{
				Users: []service.UserInfo{},
				Total: 0,
			})
		}
		// Inject id IN (...). AddWhere also indexes the expression on
		// q.Values, so any storage layer that consults that map sees it.
		q.AddWhere(query.NewExpressionCmp(query.OperatorIn, "id", userIDs))
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
