package api

import (
	"errors"
	"net/http"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/service"
)

// HTTP wrappers for the server-key lifecycle. The actual logic lives
// in service/keyops.go — handlers here only do request binding and
// status mapping. All four endpoints are exposed under /api/v1/key/*
// (see api.go for registration). Unlock/initialize/status sit on the
// unauthenticated mux because the lockgate allowlist must let them
// through before anything else can succeed; lock/rotate require
// CapSettingsManage just like the legacy /api/v1/rotate.

// getKeyStatus returns whether the server has an at-rest verifier
// (initialized) and whether the live key is loaded (unlocked). This
// endpoint is intentionally cheap and unauthenticated — the SPA
// hits it on every boot to decide between the unlock screen and the
// normal app shell. Returning richer data here (e.g. fingerprints,
// last-unlock timestamps) would let an unauthenticated probe
// fingerprint deployments, so we keep the response narrow.
func (a *api) getKeyStatus(c *ada.Context) error {
	st, err := a.svc.GetKeyStatus(c.Request.Context())
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(st)
}

// postKeyInitialize sets the server's at-rest key for the very first
// time. Fails with 409 Conflict when a verifier already exists.
//
// Authentication: requires an authenticated session AND
// CapSettingsManage. The fresh-install flow is "spin up pika in
// plaintext mode, create the first admin user, log in, then opt in
// to encryption from Settings". This avoids the bootstrap paradox
// the previous design fell into (server locked from byte 0 → can't
// log in → can't unlock). Combined with the one-shot verifier
// check in the service layer, the contract is "only an authenticated
// superadmin can write the verifier, and only once".
func (a *api) postKeyInitialize(c *ada.Context) error {
	var req struct {
		Key string `json:"key"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	if err := a.svc.InitializeServerKey(c.Request.Context(), req.Key); err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "server initialized and unlocked"})
}

// postKeyUnlock loads the supplied key into the manager after
// validating it against the on-disk verifier. Wrong key → 403 (the
// service layer wraps ErrForbidden); the SPA shows a generic "wrong
// key" toast and stays on the unlock form.
//
// Authentication: requires an authenticated session AND
// CapSettingsManage. While the server is locked, the lockgate
// allowlist lets this path through so a logged-in admin can reach
// it. API automation (curl with a settings.manage token) is also
// supported — that's the headless / post-restart unlock path for
// deployments that can't run a browser at restart time.
//
// Wrong-key brute-force is bounded by the same login-guard the
// auth flow uses (sessions are required to reach this route), plus
// the service-side AEAD verify is cheap enough that a per-IP rate
// limiter isn't currently required.
func (a *api) postKeyUnlock(c *ada.Context) error {
	var req struct {
		Key string `json:"key"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	if err := a.svc.UnlockServerKey(c.Request.Context(), req.Key); err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "server unlocked"})
}

// postKeyLock manually clears the live key. Useful for rotation
// drills and "I'm stepping away for a while" scenarios; functionally
// equivalent to a process restart minus the downtime.
//
// Requires CapSettingsManage — only superadmins (and explicit
// settings.manage grantees) can flip the lock bit because the
// resulting 503s break every other user's session.
func (a *api) postKeyLock(c *ada.Context) error {
	if err := a.svc.LockServerKey(); err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "server locked"})
}

// postKeyRotate validates the old key, rewraps the verifier and
// all sealed sensitive settings with the new key, and swaps the
// live encryptor.
//
// Requires CapSettingsManage. The legacy /api/v1/rotate endpoint
// (which used a separate admin_secret) has been retired — capability
// + session auth is the only contract.
func (a *api) postKeyRotate(c *ada.Context) error {
	var req struct {
		CurrentKey string `json:"current_key"`
		NewKey     string `json:"new_key"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	if err := a.svc.RotateServerKey(c.Request.Context(), req.CurrentKey, req.NewKey); err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "server key rotated"})
}
