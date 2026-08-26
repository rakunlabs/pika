package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/strategy/totp"
	"golang.org/x/crypto/bcrypt"
)

// TOTP-specific errors. Returned by every TOTP service method so callers
// can distinguish "wrong code" (retry) from "not enrolled" (different UX
// path) without parsing error strings.
var (
	// ErrTOTPNotEnrolled means the user has no TOTP row. The caller
	// (typically the API handler) should return 404 or treat this as
	// "TOTP feature off for this user".
	ErrTOTPNotEnrolled = errors.New("totp: user is not enrolled")
	// ErrTOTPNotEnabled means the user has a row but never finished
	// enrollment (Enabled=false). Either they abandoned the QR-scan
	// step or the finish handler crashed.
	ErrTOTPNotEnabled = errors.New("totp: enrollment incomplete")
	// ErrTOTPInvalidCode covers every "code didn't verify" case —
	// wrong code, expired code, malformed input, replayed code. The
	// caller surfaces a single uniform message so an attacker can't
	// distinguish "valid code but used twice" from "wrong code".
	ErrTOTPInvalidCode = errors.New("totp: invalid code")
	// ErrTOTPAlreadyEnrolled is returned by BeginEnroll when the user
	// already has Enabled=true. Re-enrollment must go through Disable
	// first so the user can't accidentally clobber a working
	// authenticator by re-scanning a QR.
	ErrTOTPAlreadyEnrolled = errors.New("totp: already enrolled")
)

// TOTPEnrollment is the one-shot payload returned by BeginEnroll. The
// SecretBase32 / OTPAuthURL are the SAME secret rendered two ways —
// the URL is for QR scanning (most users) and the Base32 string is for
// hand entry (when the camera doesn't work or the user is on a desktop
// without a phone-side scanner).
//
// This struct is the *only* time the unencrypted secret leaves the
// service layer; the API handler must NOT log or persist it. Calling
// FinishEnroll consumes the pending row and flips Enabled=true.
type TOTPEnrollment struct {
	SecretBase32 string `json:"secret_base32"`
	OTPAuthURL   string `json:"otpauth_url"`
	// Period/Digits/Algorithm are echoed so the UI can render the
	// authenticator-app expectations alongside the QR ("6 digits,
	// every 30 seconds"). These mirror what's baked into the URL so
	// the UI doesn't have to parse the otpauth URI to display them.
	Period    int    `json:"period"`
	Digits    int    `json:"digits"`
	Algorithm string `json:"algorithm"`
}

// TOTPFinishResult is returned by FinishEnroll. RecoveryCodes are the
// plaintext one-time-use backup codes — they're shown to the user
// exactly here and never again (the service stores only bcrypt
// hashes). The UI MUST present them prominently and the user MUST
// save them; otherwise a lost phone is a locked-out account.
type TOTPFinishResult struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

// TOTPStatus is the safe-to-expose view of UserTOTP — no secret, no
// recovery hashes, just metadata the user can see in the security
// settings page.
type TOTPStatus struct {
	Enabled           bool      `json:"enabled"`
	CreatedAt         time.Time `json:"created_at,omitempty"`
	LastUsedAt        time.Time `json:"last_used_at,omitempty"`
	RecoveryCodesLeft int       `json:"recovery_codes_left"`
	// PendingEnrollment is true when a row exists but Enabled=false —
	// the UI uses this to distinguish "you started, finish your QR
	// scan" from "you haven't started yet".
	PendingEnrollment bool `json:"pending_enrollment"`
}

// TOTPService coordinates per-user TOTP state on top of the storage.
// It owns:
//   - the TOTP crypto Config (the period/digits/algorithm — single
//     source of truth for both Generate and Verify),
//   - the issuer string baked into otpauth URLs (the brand the user
//     sees in their authenticator app),
//   - the in-memory replay guard (prevents the same code being used
//     twice within its valid window — RFC 6238 §5.2),
//   - the in-memory step-up pending store (a short-lived map from
//     opaque session id → primary-auth identity, populated by the
//     MFA strategy at phase-1 and consumed at phase-2).
//
// Splitting this out of *Service (a god object) keeps the TOTP
// dependencies (ada/totp, golang.org/x/crypto/bcrypt) contained and
// makes "is TOTP enabled in this deployment?" a single nil check.
type TOTPService struct {
	svc    *Service
	cfg    totp.Config
	issuer string

	// replay guards against a single TOTP code being used twice within
	// its valid window. Keyed by "<userID>:<code>"; the value is the
	// expiry time. Map walks happen under replayMu; the GC ticker
	// evicts expired entries opportunistically.
	replayMu sync.Mutex
	replay   map[string]time.Time

	// pending stores phase-1 identities awaiting phase-2 verification.
	// Sized for the typical "user enters password then walks to phone
	// to grab the code" interval — entries expire after pendingTTL.
	pendingMu sync.Mutex
	pending   map[string]*totpPending

	// pendingTTL caps how long the user has between phase 1 and 2.
	// Long enough to fish out a phone (60s feels rushed); short
	// enough that a stolen browser-resident session id can't be
	// replayed days later.
	pendingTTL time.Duration
}

// totpPending is one entry in the step-up pending map. The identity is
// the fully-resolved primary-auth identity from phase 1 — we hand it
// back verbatim at phase 2 so the user's roles and provider don't
// shift between phases.
type totpPending struct {
	identityJSON []byte // marshaled to bytes so map mutations can't reach inside
	expires      time.Time
}

// NewTOTPService wires the TOTP coordinator. The issuer name is what
// authenticator apps display above the code (e.g. "Pika"); leave empty
// and the QR will fall back to just the account.
func NewTOTPService(svc *Service, issuer string) *TOTPService {
	ts := &TOTPService{
		svc:        svc,
		cfg:        totp.Default(),
		issuer:     issuer,
		replay:     make(map[string]time.Time),
		pending:    make(map[string]*totpPending),
		pendingTTL: 5 * time.Minute,
	}
	go ts.gcLoop()
	return ts
}

// SetTOTPService attaches a TOTPService to a parent Service. Mirrors
// the SetPasskeyService pattern; nil disables the feature.
func (s *Service) SetTOTPService(t *TOTPService) {
	s.totp = t
}

// TOTPCoord returns the bound TOTPService, or nil when TOTP is
// disabled for this deployment. Callers MUST nil-check.
func (s *Service) TOTPCoord() *TOTPService {
	return s.totp
}

// Status returns a non-secret view of the user's TOTP state. Returns
// the "not enrolled" status (Enabled=false, PendingEnrollment=false)
// rather than ErrTOTPNotEnrolled — the API page wants to render
// "Enable 2FA" for every user without a per-user 404 dance.
func (ts *TOTPService) Status(ctx context.Context, userID string) (*TOTPStatus, error) {
	row, err := ts.svc.store.UserTOTPs().Get(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return &TOTPStatus{}, nil
		}
		return nil, err
	}
	return &TOTPStatus{
		Enabled:           row.Enabled,
		CreatedAt:         row.CreatedAt,
		LastUsedAt:        row.LastUsedAt,
		RecoveryCodesLeft: len(row.RecoveryCodes),
		PendingEnrollment: !row.Enabled,
	}, nil
}

// BeginEnroll starts a fresh enrollment for the user. Writes a row
// with Enabled=false and returns the secret + otpauth URL the SPA
// renders as a QR. The user proves possession by scanning and POSTing
// the first code to FinishEnroll.
//
// Re-enrolling an already-enabled user is rejected — the caller must
// Disable first (which requires a password re-auth). This prevents an
// attacker who hijacks an active session from silently rotating the
// TOTP secret to an authenticator they control.
//
// Re-entering BeginEnroll while Enabled=false (e.g. user closed the
// QR-scan tab and reopened it) IS allowed: it generates a new secret
// so the user can't accidentally use the stale QR from a previous tab.
func (ts *TOTPService) BeginEnroll(ctx context.Context, userID string) (*TOTPEnrollment, error) {
	if ts == nil {
		return nil, ErrTOTPNotEnrolled
	}

	existing, err := ts.svc.store.UserTOTPs().Get(ctx, userID)
	switch {
	case err == nil && existing.Enabled:
		return nil, ErrTOTPAlreadyEnrolled
	case err != nil && !errors.Is(err, ErrNotFound):
		return nil, err
	}

	user, err := ts.svc.store.Users().Get(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("totp: load user: %w", err)
	}

	secret, err := totp.NewSecret(nil, 20)
	if err != nil {
		return nil, fmt.Errorf("totp: gen secret: %w", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	row := &UserTOTP{
		UserID:    userID,
		Secret:    secret.Base32(),
		Enabled:   false,
		CreatedAt: now,
	}
	if err := ts.svc.store.UserTOTPs().Set(ctx, row); err != nil {
		return nil, fmt.Errorf("totp: persist pending row: %w", err)
	}

	url := secret.URL(totp.KeyURIParams{
		Issuer:  ts.issuer,
		Account: user.Username,
		Config:  ts.cfg,
	})

	return &TOTPEnrollment{
		SecretBase32: secret.Base32(),
		OTPAuthURL:   url,
		Period:       int(ts.cfg.Period.Seconds()),
		Digits:       ts.cfg.Digits,
		Algorithm:    string(ts.cfg.Algorithm),
	}, nil
}

// FinishEnroll confirms a pending enrollment by verifying the user's
// first code. On success it generates plaintext recovery codes
// (returned ONCE, never persisted in plaintext) and flips Enabled=true.
//
// We don't apply the replay guard here — FinishEnroll is rate-limited
// by the API layer and a replay of the enrollment code can't grant
// login (the row isn't Enabled yet, so login would fall through to
// "no second factor").
func (ts *TOTPService) FinishEnroll(ctx context.Context, userID, code string) (*TOTPFinishResult, error) {
	row, err := ts.svc.store.UserTOTPs().Get(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrTOTPNotEnrolled
		}
		return nil, err
	}
	if row.Enabled {
		// Idempotent finish would be tempting but it'd let an
		// attacker confirm "is this user enrolled" by trying any
		// code and seeing whether they get a 200 or 401. Treat it
		// as the "already enrolled" error.
		return nil, ErrTOTPAlreadyEnrolled
	}

	secret, err := totp.SecretFromBase32(row.Secret)
	if err != nil {
		return nil, fmt.Errorf("totp: stored secret corrupt: %w", err)
	}
	if !ts.cfg.Verify(secret, code, time.Now()) {
		return nil, ErrTOTPInvalidCode
	}

	plain, hashed, err := newRecoveryCodes(10)
	if err != nil {
		return nil, fmt.Errorf("totp: gen recovery codes: %w", err)
	}

	row.Enabled = true
	row.RecoveryCodes = hashed
	row.LastUsedAt = time.Now().UTC().Truncate(time.Microsecond)
	if err := ts.svc.store.UserTOTPs().Set(ctx, row); err != nil {
		return nil, fmt.Errorf("totp: persist enrolled row: %w", err)
	}

	return &TOTPFinishResult{RecoveryCodes: plain}, nil
}

// Disable removes the user's TOTP row, requiring a fresh password
// re-authentication first. The re-auth gate prevents an attacker who
// has hijacked the cookie (but not the password) from clearing the
// second factor.
//
// External-only users have no local password — Disable rejects
// them with ErrForbidden so the only way out is the admin force-reset
// path (not in this PR).
func (ts *TOTPService) Disable(ctx context.Context, userID, password string) error {
	user, err := ts.svc.store.Users().Get(ctx, userID)
	if err != nil {
		return err
	}
	if user.PasswordHash == "" {
		// No local password set (e.g. an external-only user). Disabling
		// requires admin help.
		return fmt.Errorf("totp: account has no password to confirm: %w", ErrForbidden)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return fmt.Errorf("totp: invalid password: %w", ErrUnauthorized)
	}
	if err := ts.svc.store.UserTOTPs().Delete(ctx, userID); err != nil {
		return err
	}
	return nil
}

// RegenerateRecoveryCodes mints a fresh set of plaintext codes,
// replacing the stored hashes. Returns the new plaintexts (shown
// once) and invalidates every previously-issued recovery code.
//
// Like Disable, this requires a password re-auth — the recovery codes
// are a parallel backdoor; rotating them silently from a stolen
// session would be just as bad as silently rotating the TOTP secret.
func (ts *TOTPService) RegenerateRecoveryCodes(ctx context.Context, userID, password string) ([]string, error) {
	user, err := ts.svc.store.Users().Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user.PasswordHash == "" {
		return nil, fmt.Errorf("totp: account has no password to confirm: %w", ErrForbidden)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, fmt.Errorf("totp: invalid password: %w", ErrUnauthorized)
	}

	row, err := ts.svc.store.UserTOTPs().Get(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrTOTPNotEnrolled
		}
		return nil, err
	}
	if !row.Enabled {
		return nil, ErrTOTPNotEnabled
	}

	plain, hashed, err := newRecoveryCodes(10)
	if err != nil {
		return nil, err
	}
	row.RecoveryCodes = hashed
	if err := ts.svc.store.UserTOTPs().Set(ctx, row); err != nil {
		return nil, err
	}
	return plain, nil
}

// VerifyForLogin is the step-up verifier the MFA strategy calls at
// phase 2. Accepts either a 6-digit TOTP code or a recovery code; on
// a successful recovery-code match the matching hash is removed from
// the row (single-use semantics).
//
// Replay protection: a TOTP code that has already verified once in
// the current window is rejected on the second attempt. Without this
// an attacker who shoulder-surfs a code at second 25 of a 30-second
// window has up to 35 seconds (window + skew) to use it themselves;
// burning the code on first use shrinks that to zero.
func (ts *TOTPService) VerifyForLogin(ctx context.Context, userID, code string) error {
	row, err := ts.svc.store.UserTOTPs().Get(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrTOTPNotEnrolled
		}
		return err
	}
	if !row.Enabled {
		return ErrTOTPNotEnabled
	}

	code = strings.TrimSpace(code)
	if code == "" {
		return ErrTOTPInvalidCode
	}

	// First try TOTP. Recovery codes have a different shape (12 chars
	// with two dashes) than TOTP codes (6 digits), so this won't
	// false-positive on a recovery code.
	secret, err := totp.SecretFromBase32(row.Secret)
	if err != nil {
		return fmt.Errorf("totp: stored secret corrupt: %w", err)
	}
	if ts.cfg.Verify(secret, code, time.Now()) {
		// Check replay before declaring success. If the code has been
		// burned already in this window, treat as invalid (uniform
		// error so the attacker can't tell "right code but burned"
		// from "wrong code").
		if !ts.markReplay(userID, code) {
			return ErrTOTPInvalidCode
		}
		row.LastUsedAt = time.Now().UTC().Truncate(time.Microsecond)
		if err := ts.svc.store.UserTOTPs().Set(ctx, row); err != nil {
			// Don't fail the login on a counter-update hiccup —
			// LastUsedAt is audit metadata, not a security check.
			_ = err
		}
		return nil
	}

	// Fall through to recovery-code path. Recovery codes are the
	// "phone in the toilet" escape hatch; matching one burns it.
	for i, h := range row.RecoveryCodes {
		if err := bcrypt.CompareHashAndPassword([]byte(h), []byte(strings.ToLower(code))); err == nil {
			// Match — splice this hash out and persist. Use a
			// fresh slice (don't mutate the read-back row's
			// backing array — defensive against future aliasing).
			updated := make([]string, 0, len(row.RecoveryCodes)-1)
			updated = append(updated, row.RecoveryCodes[:i]...)
			updated = append(updated, row.RecoveryCodes[i+1:]...)
			row.RecoveryCodes = updated
			row.LastUsedAt = time.Now().UTC().Truncate(time.Microsecond)
			if err := ts.svc.store.UserTOTPs().Set(ctx, row); err != nil {
				return fmt.Errorf("totp: burn recovery code: %w", err)
			}
			return nil
		}
	}

	return ErrTOTPInvalidCode
}

// markReplay records that a TOTP code was used for the given user
// and reports whether the burn was successful (true) or whether the
// code was already burned in this window (false).
//
// TTL is period * (2*Skew + 1) — long enough to cover every window
// that VerifyForLogin would have accepted the code for. Once the code
// can no longer verify, the entry is harmless and the GC ticker
// evicts it.
func (ts *TOTPService) markReplay(userID, code string) bool {
	key := userID + ":" + code
	ttl := ts.cfg.Period * time.Duration(2*ts.cfg.Skew+1)
	now := time.Now()

	ts.replayMu.Lock()
	defer ts.replayMu.Unlock()
	if expires, present := ts.replay[key]; present && expires.After(now) {
		return false
	}
	ts.replay[key] = now.Add(ttl)
	return true
}

// SavePending stores a phase-1 identity awaiting phase-2 TOTP
// verification and returns the opaque session id the SPA echoes back.
// The identity is held in marshaled form so the map's value doesn't
// alias into the caller's identity pointer.
//
// Uses the package-local newSessionID (panic-on-RNG-failure) — the
// passkey package established that any failure of crypto/rand is
// catastrophic and not worth threading through every call site.
func (ts *TOTPService) SavePending(identJSON []byte) string {
	sid := newSessionID()
	ts.pendingMu.Lock()
	defer ts.pendingMu.Unlock()
	ts.pending[sid] = &totpPending{
		identityJSON: identJSON,
		expires:      time.Now().Add(ts.pendingTTL),
	}
	return sid
}

// ConsumePending atomically loads and deletes a pending entry. One-
// shot semantics: a session id can only redeem a phase-2 attempt
// once. If the entry is missing or expired, returns nil.
func (ts *TOTPService) ConsumePending(sessionID string) []byte {
	ts.pendingMu.Lock()
	defer ts.pendingMu.Unlock()
	entry, ok := ts.pending[sessionID]
	if !ok {
		return nil
	}
	delete(ts.pending, sessionID)
	if time.Now().After(entry.expires) {
		return nil
	}
	return entry.identityJSON
}

// PendingTTL exposes the configured per-pending lifetime so the
// caller (API layer) can mirror it in a response field — the UI
// shows "expires in N seconds" to nudge the user to enter the code
// promptly.
func (ts *TOTPService) PendingTTL() time.Duration {
	return ts.pendingTTL
}

// AdminResetTOTP removes a target user's TOTP enrollment without
// requiring the user's password — the escape hatch for "user lost
// their authenticator AND their recovery codes". Caller MUST be a
// user with the CapUsersManage capability (the API handler enforces).
//
// callerID is the user_id of the admin performing the reset.
// targetID is the user_id being reset. The two MUST differ:
//
//   - The admin path skips password re-auth (an admin doesn't know
//     their target's password). That trade-off only makes sense
//     when the admin is operating on someone else.
//   - If an admin's own session cookie is stolen, the attacker would
//     otherwise be able to wipe the admin's own 2FA from a hijacked
//     browser without the password — defeating the whole point of
//     enrolling a second factor. Forcing self-disable through the
//     password-gated Disable() preserves that protection.
//
// The check is also a usability nudge: admins clearing their own
// 2FA almost always mean to use the self-service Disable; an
// accidental click on the admin "Reset" button on their own row
// should fail loudly rather than silently destroy their factor.
//
// Returns ErrForbidden when callerID == targetID. Returns the
// underlying storage error (typically ErrNotFound) when targetID
// doesn't resolve to a real user.
//
// The operation is otherwise idempotent: deleting a non-existent
// TOTP row (target user exists but has no enrollment) succeeds
// silently. The UI calls this without first checking has_totp; we
// don't want a 404 to mask a successful clear in the presence of a
// race.
//
// After reset the user can log in with just their password (or any
// other enrolled strategy). They are encouraged to re-enroll from
// the Security settings page — the previous secret and recovery
// codes are unrecoverable, so the user effectively starts fresh.
func (ts *TOTPService) AdminResetTOTP(ctx context.Context, callerID, targetID string) error {
	if targetID == "" {
		return fmt.Errorf("totp: target user id required: %w", ErrBadRequest)
	}
	if callerID == targetID {
		return fmt.Errorf("totp: admins cannot reset their own 2FA via the admin path — use the self-service Disable endpoint, which requires your password: %w", ErrForbidden)
	}
	// Defensive: make sure the target user actually exists. Without
	// this, a malformed user id from the UI would silently succeed
	// and leave the admin with no signal that they typed the wrong
	// id.
	if _, err := ts.svc.store.Users().Get(ctx, targetID); err != nil {
		return err
	}
	if err := ts.svc.store.UserTOTPs().Delete(ctx, targetID); err != nil {
		if errors.Is(err, ErrNotFound) {
			// Idempotent: row already absent, treat as success.
			return nil
		}
		return err
	}
	return nil
}

// IsEnabledForUser is the fast path the MFA strategy calls between
// phase-1 password verification and minting a session. Returns true
// when the user has a row with Enabled=true.
//
// A missing row or an Enabled=false row both yield (false, nil) — the
// strategy treats both as "no MFA, mint session normally". An error
// (storage failure) is propagated so the strategy can fail closed.
func (ts *TOTPService) IsEnabledForUser(ctx context.Context, userID string) (bool, error) {
	row, err := ts.svc.store.UserTOTPs().Get(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return row.Enabled, nil
}

// gcLoop evicts stale entries from the replay and pending maps. Both
// have a natural TTL, so the GC is opportunistic — late eviction just
// means slightly more memory until the next pass.
func (ts *TOTPService) gcLoop() {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		ts.replayMu.Lock()
		for k, exp := range ts.replay {
			if !exp.After(now) {
				delete(ts.replay, k)
			}
		}
		ts.replayMu.Unlock()
		ts.pendingMu.Lock()
		for k, p := range ts.pending {
			if !p.expires.After(now) {
				delete(ts.pending, k)
			}
		}
		ts.pendingMu.Unlock()
	}
}

// ── helpers ────────────────────────────────────────────────────────

// recoveryCodeAlphabet is the character set used in recovery codes.
// Crockford-style base32: digits + uppercase letters minus the visually-
// confusable I, L, O, U. Lowercase versions are accepted on input via
// strings.ToLower in VerifyForLogin.
//
// Length and grouping (12 chars in 3 groups of 4, dash-separated) give
// ~62 bits of entropy per code at 32 alphabet size, well above what's
// needed for a one-time bypass; the format reads as "xxxx-xxxx-xxxx"
// which is the de-facto convention users recognize from GitHub/Google.
const recoveryCodeAlphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ"

// newRecoveryCodes returns n plaintext recovery codes and the matching
// bcrypt hashes. The plaintext is shown to the user once; only the
// hashes are persisted. bcrypt cost is the bcrypt.DefaultCost so the
// verify-on-login is fast enough to not block the request thread for
// users with many codes (default 10).
func newRecoveryCodes(n int) (plain []string, hashed []string, err error) {
	if n <= 0 {
		n = 10
	}
	plain = make([]string, n)
	hashed = make([]string, n)

	for i := 0; i < n; i++ {
		raw, err := randomFromAlphabet(12, recoveryCodeAlphabet)
		if err != nil {
			return nil, nil, err
		}
		// Insert dashes after positions 4 and 8 for readability.
		formatted := raw[:4] + "-" + raw[4:8] + "-" + raw[8:]
		plain[i] = formatted
		// Hash the lowercased form — VerifyForLogin lowercases input
		// so the user can type either case without surprise.
		h, err := bcrypt.GenerateFromPassword([]byte(strings.ToLower(formatted)), bcrypt.DefaultCost)
		if err != nil {
			return nil, nil, err
		}
		hashed[i] = string(h)
	}
	return plain, hashed, nil
}

// randomFromAlphabet returns a string of `length` characters drawn
// uniformly from the alphabet using crypto/rand. The rejection-
// sampling pattern avoids the modulo bias that a naive `rand[i] %
// len(alphabet)` would introduce when len(alphabet) doesn't divide
// 256 evenly.
func randomFromAlphabet(length int, alphabet string) (string, error) {
	if length <= 0 {
		return "", nil
	}
	out := make([]byte, 0, length)
	buf := make([]byte, length*2) // overdraw to amortize the syscall

	// Inclusive max in [0, 256) that we accept without bias.
	limit := byte(256 - (256 % len(alphabet)))

	for len(out) < length {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if b >= limit {
				continue
			}
			out = append(out, alphabet[int(b)%len(alphabet)])
			if len(out) == length {
				break
			}
		}
	}
	return string(out), nil
}

// equalConstantTime is a thin wrapper so VerifyForLogin-adjacent code
// is uniformly constant-time. Not currently exported; bcrypt's
// CompareHashAndPassword already runs constant-time internally, but
// future code paths might benefit.
//
//nolint:unused // retained for future hot-path verifiers.
func equalConstantTime(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
