package api

import (
	"errors"
	"net/http"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/pika/internal/service"
)

// TOTP / 2FA self-service handlers. The /api/v1/me/totp namespace
// is owned by the calling user — every handler resolves user_id
// from the request context (set by mgr.Require) and never accepts a
// path-param user id, so an admin cannot accidentally enroll/disable
// someone else's TOTP from these endpoints.
//
// The handler set mirrors passkey.go in shape: get-status / begin
// (returns one-shot secret) / finish (returns one-shot recovery
// codes) / delete (with password re-auth) / regenerate-recovery
// (with password re-auth).
//
// Endpoints return 503 when the deployment hasn't wired the TOTP
// coordinator (s.TOTPCoord() == nil). The wiring is built whenever
// AuthSettings is non-nil so 503 should be rare in practice; it
// only surfaces during the bootstrap window before Boot() runs.

// totpFinishRequest is the body shape for POST /finish: a single
// 6-digit code the user just read from their authenticator. The
// caller's identity is taken from the session, so no username field.
type totpFinishRequest struct {
	Code string `json:"code"`
}

// totpPasswordRequest is the body shape for password-gated mutations
// (Disable, RegenerateRecoveryCodes). Re-auth via password
// confirms the user is at the keyboard, not an attacker holding a
// stolen cookie. External-only users have no password — the service
// layer rejects them with ErrForbidden so they can't accidentally
// disable their own TOTP and then not know how to re-enable.
type totpPasswordRequest struct {
	Password string `json:"password"`
}

// getMyTOTPStatus returns the safe-to-expose view of the user's
// TOTP state. Always 200 — a "no row" user gets a status with
// Enabled=false/PendingEnrollment=false rather than 404, so the
// settings page can render uniformly.
func (a *api) getMyTOTPStatus(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.TOTPCoord()
	if coord == nil {
		// Feature off: present as "not enrolled" rather than 503 so
		// the UI doesn't have to special-case the deployment state.
		return c.SetStatus(http.StatusOK).SendJSON(&service.TOTPStatus{})
	}
	status, err := coord.Status(ctx, userID)
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(status)
}

// beginMyTOTPEnroll generates a fresh secret + otpauth URL.
//
// The response is the one and only time the unencrypted secret leaves
// the server — the SPA must render the QR (or fall back to the
// base32 string) and the user must scan / type into their
// authenticator app before posting back to finish.
//
// Rejected with 409 when the user is already enrolled — they must
// Disable first (which re-authenticates with the password) before
// rotating to a new authenticator. This prevents a stolen-cookie
// attacker from silently rebinding the second factor.
func (a *api) beginMyTOTPEnroll(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.TOTPCoord()
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "TOTP not configured"})
	}

	enroll, err := coord.BeginEnroll(ctx, userID)
	if err != nil {
		if errors.Is(err, service.ErrTOTPAlreadyEnrolled) {
			return c.SetStatus(http.StatusConflict).SendJSON(response{
				Message: "TOTP is already enabled; disable it first to re-enroll",
			})
		}
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(enroll)
}

// finishMyTOTPEnroll confirms a pending enrollment by verifying the
// user's first code. On success returns the plaintext recovery
// codes — these are shown to the user once, never again. The SPA
// is responsible for nudging the user to save them.
func (a *api) finishMyTOTPEnroll(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.TOTPCoord()
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "TOTP not configured"})
	}

	var req totpFinishRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	if req.Code == "" {
		return errors.Join(errors.New("code required"), service.ErrBadRequest)
	}

	result, err := coord.FinishEnroll(ctx, userID, req.Code)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrTOTPInvalidCode):
			return c.SetStatus(http.StatusUnauthorized).SendJSON(response{
				Message: "Invalid TOTP code",
			})
		case errors.Is(err, service.ErrTOTPNotEnrolled):
			return c.SetStatus(http.StatusBadRequest).SendJSON(response{
				Message: "Start enrollment via POST /me/totp/begin first",
			})
		case errors.Is(err, service.ErrTOTPAlreadyEnrolled):
			return c.SetStatus(http.StatusConflict).SendJSON(response{
				Message: "TOTP is already enabled",
			})
		}
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(result)
}

// disableMyTOTP removes the user's TOTP enrollment after verifying
// the password. Returns 204 on success.
func (a *api) disableMyTOTP(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.TOTPCoord()
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "TOTP not configured"})
	}

	var req totpPasswordRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	if req.Password == "" {
		return errors.Join(errors.New("password required"), service.ErrBadRequest)
	}

	if err := coord.Disable(ctx, userID, req.Password); err != nil {
		// ErrUnauthorized (wrong password) and ErrForbidden (no
		// local password / external-only user) bubble through the
		// standard error mapping.
		return err
	}
	return c.SendNoContent()
}

// regenerateMyTOTPRecoveryCodes mints a fresh set of recovery codes
// after password re-auth. Returns the new plaintext codes; the
// previous set is invalidated.
func (a *api) regenerateMyTOTPRecoveryCodes(c *ada.Context) error {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	coord := a.svc.TOTPCoord()
	if coord == nil {
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: "TOTP not configured"})
	}

	var req totpPasswordRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	if req.Password == "" {
		return errors.Join(errors.New("password required"), service.ErrBadRequest)
	}

	codes, err := coord.RegenerateRecoveryCodes(ctx, userID, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrTOTPNotEnrolled), errors.Is(err, service.ErrTOTPNotEnabled):
			return c.SetStatus(http.StatusBadRequest).SendJSON(response{
				Message: "Enable TOTP before regenerating recovery codes",
			})
		}
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(map[string]any{
		"recovery_codes": codes,
	})
}

// adminResetUserTOTP wipes a target user's TOTP enrollment. Lives
// on the /users-totp/{id} path (not /users/{id}/totp) because the
// existing /users/* wildcard catches everything under it — using a
// flat sibling path matches the convention already in use for
// kickUser (/users-kick/{id}).
//
// Capability-gated by CapUsersManage at the router. Self-reset
// is rejected at the service layer (ErrForbidden) — that path
// would skip the password gate that self-Disable requires, which
// is exactly the protection a stolen session cookie tries to
// bypass.
//
// The operation is otherwise idempotent: resetting a user who
// isn't enrolled returns 204 just the same. The audit story (who
// reset whom, when) lives in the existing access logs — we don't
// emit a dedicated hook event here so the change set stays
// minimal; that can be a follow-up.
func (a *api) adminResetUserTOTP(c *ada.Context) error {
	ctx := c.Request.Context()
	targetID := c.Request.PathValue("*")
	if targetID == "" {
		return errors.Join(errors.New("target user id required"), service.ErrBadRequest)
	}

	callerID := service.UserIDFromContext(ctx)
	if callerID == "" {
		return errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}

	coord := a.svc.TOTPCoord()
	if coord == nil {
		// Same shape as the self-service endpoints: 503 when the
		// deployment hasn't wired TOTP. Note that decorateHasTOTP
		// returns false for every user in this case so the admin
		// UI shouldn't have shown the button — defensive.
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{
			Message: "TOTP not configured",
		})
	}

	if err := coord.AdminResetTOTP(ctx, callerID, targetID); err != nil {
		return err
	}
	return c.SendNoContent()
}
