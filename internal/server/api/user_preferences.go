package api

import (
	"errors"
	"net/http"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/service"
)

// getMyPreferences returns the authenticated user's UI preferences. When
// no document exists yet, defaults are returned (200 with the canonical
// shape) so the SPA never has to handle 404 here.
func (a *api) getMyPreferences(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}

	prefs, err := a.svc.GetUserPreferences(ctx, userID)
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(prefs)
}

// updateMyPreferences applies a partial patch to the user's preferences
// and returns the merged document. Unknown keys are ignored by the JSON
// decoder; unknown values for typed fields (theme, etc.) are rejected
// with 400 by the service layer.
func (a *api) updateMyPreferences(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}

	var patch service.UserPreferencesPatch
	if err := c.Bind(&patch); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	prefs, err := a.svc.UpdateUserPreferences(ctx, userID, &patch)
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(prefs)
}

// resetMyPreferences deletes the user's stored preference document so
// subsequent reads fall back to the server-side defaults. Idempotent.
func (a *api) resetMyPreferences(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}

	if err := a.svc.ResetUserPreferences(ctx, userID); err != nil {
		return err
	}
	return c.SendNoContent()
}
