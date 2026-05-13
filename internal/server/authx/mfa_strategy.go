package authx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rakunlabs/ada/middleware/auth/identity"
	"github.com/rakunlabs/ada/middleware/auth/strategy"

	"github.com/rakunlabs/pika/internal/service"
)

// MFAStrategy decorates an inner strategy.Authenticator with a TOTP
// step-up gate. The wrapper:
//
//  1. Detects which phase the request is in by inspecting the JSON
//     body. A body carrying `totp_session_id` + `code` is phase 2
//     (verify second factor). Anything else is phase 1 (delegate to
//     the inner strategy).
//  2. Calls inner.Login on phase 1. If the inner strategy fails or
//     defers, the response is already written — return verbatim.
//     If the inner strategy succeeds, look up the resolved identity's
//     user_id and check whether TOTP is enabled.
//  3. If TOTP isn't enabled (or coord is nil), return OutcomeContinue
//     — pass-through, no behavior change. The decorator is a no-op
//     for users who haven't enrolled.
//  4. If TOTP is enabled, marshal the identity, stash it in the
//     coord's pending store, and write a step-up challenge JSON
//     response with the opaque session id. Return OutcomePending so
//     ada doesn't mint a session.
//  5. On phase 2, look up the pending identity by session id, verify
//     the code via coord.VerifyForLogin, and return the identity
//     with OutcomeContinue — ada then mints the real session.
//
// The decorator implements strategy.Registerer when the inner does,
// passing register calls straight through — a brand-new user can't
// have TOTP enrolled yet, so step-up at register time would
// short-circuit the signup flow.
//
// The wrapper is safe for concurrent use. No per-request state lives
// on the struct; all phase coordination is in the coord's pending
// map.
type MFAStrategy struct {
	inner strategy.Authenticator
	coord *service.TOTPService
	svc   *service.Service
}

// NewMFAStrategy returns an MFAStrategy wrapping `inner`. coord may
// be nil — the wrapper degrades to a transparent pass-through, so it
// is always safe to apply (callers don't have to branch on the
// TOTP-enabled flag at the call site).
//
// svc is required to resolve identity.Subject (username) → user_id
// for the IsEnabledForUser lookup. Without it the wrapper can't know
// which user is logging in.
func NewMFAStrategy(inner strategy.Authenticator, svc *service.Service, coord *service.TOTPService) *MFAStrategy {
	return &MFAStrategy{inner: inner, coord: coord, svc: svc}
}

// Name returns the inner strategy's name. The decorator is invisible
// at the URL routing layer — /login/pass/local still works whether
// or not MFA is wrapped around it.
func (m *MFAStrategy) Name() string { return m.inner.Name() }

// Descriptor returns the inner strategy's descriptor. The UI doesn't
// need to know about the step-up phase at info time — it discovers
// the requirement reactively from the phase-1 response.
func (m *MFAStrategy) Descriptor() strategy.Descriptor { return m.inner.Descriptor() }

// Logout forwards to the inner. There's no MFA-specific cleanup —
// the issuer revokes the session, the pending store ages out on its
// own.
func (m *MFAStrategy) Logout(ctx context.Context, id *identity.Identity) error {
	return m.inner.Logout(ctx, id)
}

// Register forwards to the inner's Register when the inner supports
// it. Brand-new users haven't enrolled TOTP, so registration short-
// circuits the step-up path; we still want the inner's
// auto-login behavior to work uninterrupted.
//
// This method is only invoked by ada when the wrapper itself
// satisfies strategy.Registerer — see the MFAStrategyWithRegister
// wrapper below. The split exists because Go interface satisfaction
// is structural, and a wrapper that always implements Register would
// claim signup support for strategies (LDAP) that don't have it.
func (m *MFAStrategy) registerPassthrough(w http.ResponseWriter, r *http.Request) (*identity.Identity, strategy.Outcome, error) {
	reg, ok := m.inner.(strategy.Registerer)
	if !ok {
		writeMFAError(w, http.StatusNotFound, "signup_disabled", "strategy does not support signup")
		return nil, strategy.OutcomeFailed, nil
	}
	return reg.Register(w, r)
}

// mfaFinishRequest is the body shape the SPA POSTs at phase 2. The
// fields are intentionally namespaced (`totp_session_id`, not just
// `session_id`) so the wrapper never collides with a phase-1 body
// that happens to carry a `session_id` field for its own reasons
// (passkey uses a `session_id` key for its own continuation flow,
// and we don't want to break that semantics if a future strategy
// gets wrapped).
type mfaFinishRequest struct {
	TOTPSessionID string `json:"totp_session_id"`
	Code          string `json:"code"`
}

// Login dispatches phase 1 vs phase 2.
func (m *MFAStrategy) Login(w http.ResponseWriter, r *http.Request) (*identity.Identity, strategy.Outcome, error) {
	if r.Method != http.MethodPost {
		// Forward non-POST to inner; the inner is the source of
		// truth for which methods it accepts (some support GET for
		// OAuth2 callbacks).
		return m.inner.Login(w, r)
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<17)) // 128 KiB
	if err != nil {
		writeMFAError(w, http.StatusBadRequest, "bad_request", "read body")
		return nil, strategy.OutcomeFailed, nil
	}
	// Restore r.Body so the inner can read it during phase 1. The
	// LoginGuard middleware already buffered the body upstream, so
	// double-buffering here is fine.
	r.Body = io.NopCloser(bytes.NewReader(body))

	if isMFAFinishBody(body) {
		return m.handleFinish(w, r, body)
	}
	return m.handleBegin(w, r)
}

// handleBegin delegates to the inner strategy and, on success,
// either steps up to the TOTP phase or passes the identity through.
func (m *MFAStrategy) handleBegin(w http.ResponseWriter, r *http.Request) (*identity.Identity, strategy.Outcome, error) {
	// Buffer the inner's writes so we can discard them if we need to
	// emit a step-up response instead. On phase 1, ada's auth.go has
	// not yet written anything for OutcomeContinue (it writes only
	// after issuer.Issue), so for the common success path no buffer
	// is needed — the inner returns identity without touching w. But
	// the inner DOES write on OutcomeFailed/Pending; we must let
	// those go through, so the buffer only intercepts the no-write
	// path.
	id, outcome, err := m.inner.Login(w, r)
	if outcome != strategy.OutcomeContinue || id == nil {
		// Pass-through for failed / pending / err — the inner has
		// already written the response. We don't even read m.coord
		// here; failing closed is the right default.
		return id, outcome, err
	}

	// Inner succeeded. Decide whether to step up.
	if m.coord == nil {
		return id, strategy.OutcomeContinue, nil
	}

	userID, err := m.resolveUserID(r.Context(), id)
	if err != nil {
		// Fail closed: if we can't resolve the user, we can't tell
		// whether MFA is required. Better to refuse the login than
		// silently let a TOTP-enabled user through without the
		// second factor.
		slog.Warn("mfa: resolve user id", "strategy", m.Name(), "subject", id.Subject, "error", err)
		writeMFAError(w, http.StatusInternalServerError, "mfa_lookup_failed", "could not check MFA status")
		return nil, strategy.OutcomeFailed, nil
	}

	enabled, err := m.coord.IsEnabledForUser(r.Context(), userID)
	if err != nil {
		slog.Warn("mfa: enabled lookup", "user_id", userID, "error", err)
		writeMFAError(w, http.StatusInternalServerError, "mfa_lookup_failed", "could not check MFA status")
		return nil, strategy.OutcomeFailed, nil
	}
	if !enabled {
		return id, strategy.OutcomeContinue, nil
	}

	// MFA required. Stash the identity for phase 2 and emit the
	// challenge. The identity is marshaled so map mutations can't
	// reach inside.
	encoded, err := json.Marshal(id)
	if err != nil {
		writeMFAError(w, http.StatusInternalServerError, "marshal_failed", "could not stash identity")
		return nil, strategy.OutcomeFailed, nil
	}
	sid := m.coord.SavePending(encoded)

	writeMFAResponse(w, http.StatusOK, map[string]any{
		"phase":           "totp_required",
		"totp_session_id": sid,
		"strategy":        m.Name(),
		"expires_in":      int(m.coord.PendingTTL().Seconds()),
	})
	return nil, strategy.OutcomePending, nil
}

// handleFinish verifies the TOTP code against the pending identity.
func (m *MFAStrategy) handleFinish(w http.ResponseWriter, r *http.Request, body []byte) (*identity.Identity, strategy.Outcome, error) {
	if m.coord == nil {
		// We received a phase-2 body but MFA isn't configured.
		// Treat as a malformed phase-1 request — the SPA shouldn't
		// be sending this shape if /auth/info advertised plain
		// password auth.
		writeMFAError(w, http.StatusBadRequest, "bad_request", "MFA not enabled")
		return nil, strategy.OutcomeFailed, nil
	}

	var req mfaFinishRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeMFAError(w, http.StatusBadRequest, "bad_request", "parse finish body")
		return nil, strategy.OutcomeFailed, nil
	}
	if req.TOTPSessionID == "" || req.Code == "" {
		writeMFAError(w, http.StatusBadRequest, "bad_request", "missing totp_session_id or code")
		return nil, strategy.OutcomeFailed, nil
	}

	identJSON := m.coord.ConsumePending(req.TOTPSessionID)
	if identJSON == nil {
		// One-shot: either the session id is wrong, expired, or
		// already redeemed. Uniform 401 to avoid revealing which.
		writeMFAError(w, http.StatusUnauthorized, "invalid_session", "step-up session is invalid or expired")
		return nil, strategy.OutcomeFailed, nil
	}

	var id identity.Identity
	if err := json.Unmarshal(identJSON, &id); err != nil {
		// Defensive — we wrote this ourselves, so the only way it
		// fails is storage corruption. Don't let the user through.
		writeMFAError(w, http.StatusInternalServerError, "session_corrupt", "could not restore step-up identity")
		return nil, strategy.OutcomeFailed, nil
	}

	userID, err := m.resolveUserID(r.Context(), &id)
	if err != nil {
		writeMFAError(w, http.StatusInternalServerError, "mfa_lookup_failed", "could not resolve user")
		return nil, strategy.OutcomeFailed, nil
	}

	if err := m.coord.VerifyForLogin(r.Context(), userID, req.Code); err != nil {
		switch {
		case errors.Is(err, service.ErrTOTPInvalidCode):
			writeMFAError(w, http.StatusUnauthorized, "invalid_code", "invalid TOTP code")
		case errors.Is(err, service.ErrTOTPNotEnrolled), errors.Is(err, service.ErrTOTPNotEnabled):
			// User was enabled at phase 1, then disabled between
			// phases — race. Treat as invalid_code (same UX).
			writeMFAError(w, http.StatusUnauthorized, "invalid_code", "invalid TOTP code")
		default:
			slog.Error("mfa: verify failed", "user_id", userID, "error", err)
			writeMFAError(w, http.StatusInternalServerError, "verify_failed", "MFA verification error")
		}
		return nil, strategy.OutcomeFailed, nil
	}

	return &id, strategy.OutcomeContinue, nil
}

// resolveUserID converts an identity (which carries the username as
// Subject for local/LDAP) into the stable user_id. Returns an error
// when the user has been deleted between phases.
//
// For external strategies that haven't been linked yet to a pika
// user row (e.g. first-time OAuth2 callback), the user_id lookup
// would fail — but those strategies don't get wrapped (the wiring
// at build.go only wraps Local and LDAP, which always resolve to a
// pika user before returning OutcomeContinue).
func (m *MFAStrategy) resolveUserID(ctx context.Context, id *identity.Identity) (string, error) {
	if id == nil || id.Subject == "" {
		return "", errors.New("empty identity subject")
	}
	user, err := m.svc.GetUserByUsername(ctx, id.Subject)
	if err != nil {
		return "", err
	}
	return user.ID, nil
}

// isMFAFinishBody peeks at the JSON body to decide whether this is a
// phase-2 request. We use json.RawMessage probing rather than full
// decoding so we don't have to commit to a schema before we know the
// phase.
//
// Treats a body containing BOTH `totp_session_id` and `code` keys as
// finish; anything else is begin. This intentionally tolerates a
// phase-1 body that happens to carry one of those keys for its own
// reasons (defense against future inner strategies whose fields
// collide).
func isMFAFinishBody(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		return false
	}
	_, hasSID := probe["totp_session_id"]
	_, hasCode := probe["code"]
	return hasSID && hasCode
}

// MFAStrategyWithRegister is the strategy.Registerer-implementing
// variant of MFAStrategy. We expose two types so a wrapper around a
// strategy that doesn't support signup (LDAP) doesn't accidentally
// claim it does — interface satisfaction is structural in Go, and
// ada checks for it with a type assertion at register-handler time.
//
// Use NewMFAStrategy when the inner is signup-less (LDAP, OAuth2);
// use NewMFAStrategyWithRegister when the inner has WithRegistrar
// (local). The latter forwards Register straight to the inner so
// the first-user bootstrap continues to work.
type MFAStrategyWithRegister struct {
	*MFAStrategy
}

// NewMFAStrategyWithRegister wraps an inner strategy that supports
// signup. Panics in development builds when the inner doesn't
// actually implement Registerer — better a loud startup failure
// than silently dropping signup at runtime.
func NewMFAStrategyWithRegister(inner strategy.Authenticator, svc *service.Service, coord *service.TOTPService) *MFAStrategyWithRegister {
	if _, ok := inner.(strategy.Registerer); !ok {
		panic("authx: NewMFAStrategyWithRegister requires inner to implement strategy.Registerer")
	}
	return &MFAStrategyWithRegister{MFAStrategy: NewMFAStrategy(inner, svc, coord)}
}

// Register satisfies strategy.Registerer.
func (m *MFAStrategyWithRegister) Register(w http.ResponseWriter, r *http.Request) (*identity.Identity, strategy.Outcome, error) {
	return m.registerPassthrough(w, r)
}

// ── tiny JSON helpers (kept local to avoid spreading writeError forks) ──

func writeMFAResponse(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Debug("mfa: write response", "error", err)
	}
}

func writeMFAError(w http.ResponseWriter, status int, code, msg string) {
	writeMFAResponse(w, status, map[string]string{
		"error":   code,
		"message": msg,
	})
}

// suppress unused import warnings on platforms that don't pull in
// strings for the body trimming above.
var _ = strings.TrimSpace
