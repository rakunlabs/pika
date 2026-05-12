package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/service"
)

// passkeyBeginRequest carries the optional human label the user wants
// to attach to the credential post-enroll. We accept it here at begin
// time so the SPA can collect it on the same form as the device
// prompt and not have to round-trip through a second prompt after
// the authenticator finishes.
type passkeyBeginRequest struct {
	Name string `json:"name,omitempty"`
}

// passkeyBeginResponse is what we hand back to the SPA. session_id is
// echoed verbatim on finish; options is the WebAuthn-conformant
// dictionary the SPA passes straight into navigator.credentials.create.
type passkeyBeginResponse struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name,omitempty"`
	Options   any    `json:"options"`
}

// passkeyFinishRequest carries the session id from begin alongside
// the raw browser response. The response shape is forwarded verbatim
// to the passkey package's FinishEnroll which knows how to parse it.
type passkeyFinishRequest struct {
	SessionID string          `json:"session_id"`
	Name      string          `json:"name,omitempty"`
	Response  json.RawMessage `json:"response"`
}

// beginPasskeyEnroll starts a registration ceremony for the calling
// user. The caller must already be authenticated via some other
// mechanism (password or an existing passkey) — this endpoint adds
// a credential, it does not bootstrap an account.
//
// Returns 503 when the deployment has no passkey engine configured
// (RPID unset, for example), distinct from 401/403 so the SPA can
// hide the "enroll passkey" affordance instead of surfacing a noisy
// auth error.
func (a *api) beginPasskeyEnroll(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.PasskeyCoord()
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "passkey not configured"})
	}

	// Name is optional at begin — we re-collect on finish if missing.
	var req passkeyBeginRequest
	_ = c.Bind(&req) // empty body is fine

	sessionID, opts, err := coord.BeginEnroll(ctx, userID)
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(passkeyBeginResponse{
		SessionID: sessionID,
		Name:      req.Name,
		Options:   opts,
	})
}

// finishPasskeyEnroll verifies the authenticator response and
// persists the credential. The body is the SPA-built finishRequest
// (session_id + name + response).
func (a *api) finishPasskeyEnroll(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.PasskeyCoord()
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "passkey not configured"})
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<17)) // 128 KiB
	if err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	var req passkeyFinishRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	if req.SessionID == "" {
		return errors.Join(errors.New("session_id required"), service.ErrBadRequest)
	}
	if len(req.Response) == 0 {
		return errors.Join(errors.New("response required"), service.ErrBadRequest)
	}

	cred, err := coord.FinishEnroll(ctx, userID, req.SessionID, req.Name, req.Response)
	if err != nil {
		return err
	}
	// Never leak the raw public key over the wire — clone the row to
	// nil it before serializing.
	out := *cred
	out.PublicKey = nil
	return c.SetStatus(http.StatusCreated).SendJSON(out)
}

// listMyPasskeys returns the calling user's enrolled credentials.
// Newest first. Raw public key is stripped.
func (a *api) listMyPasskeys(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.PasskeyCoord()
	if coord == nil {
		// Empty list rather than 503 so the SPA can render the section
		// with a friendly "no passkeys" placeholder; the absence of
		// the begin/finish endpoints is the operator's signal that
		// the feature is off.
		return c.SetStatus(http.StatusOK).SendJSON([]service.PasskeyCredential{})
	}

	rows, err := coord.ListUserPasskeys(ctx, userID)
	if err != nil {
		return err
	}
	if rows == nil {
		rows = []service.PasskeyCredential{}
	}
	return c.SetStatus(http.StatusOK).SendJSON(rows)
}

// renameMyPasskey updates the label on one of the caller's
// credentials. PATCH because we only mutate the name field; a future
// rotation endpoint would use a different verb so a request can't
// accidentally re-enroll the device.
func (a *api) renameMyPasskey(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.PasskeyCoord()
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "passkey not configured"})
	}

	credID := c.Request.PathValue("*")
	if credID == "" {
		return errors.Join(errors.New("credential id required"), service.ErrBadRequest)
	}

	var body struct {
		Name string `json:"name"`
	}
	if err := c.Bind(&body); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	cred, err := coord.RenamePasskey(ctx, userID, credID, body.Name)
	if err != nil {
		return err
	}
	out := *cred
	out.PublicKey = nil
	return c.SetStatus(http.StatusOK).SendJSON(out)
}

// deleteMyPasskey removes one of the caller's credentials. After the
// last one is removed the user falls back to password login (or any
// other strategy the deployment supports).
func (a *api) deleteMyPasskey(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.PasskeyCoord()
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "passkey not configured"})
	}

	credID := c.Request.PathValue("*")
	if credID == "" {
		return errors.Join(errors.New("credential id required"), service.ErrBadRequest)
	}

	if err := coord.DeletePasskey(ctx, userID, credID); err != nil {
		return err
	}
	return c.SendNoContent()
}
