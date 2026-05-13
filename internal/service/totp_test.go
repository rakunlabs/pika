package service_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/strategy/totp"

	"github.com/rakunlabs/pika/internal/service"
)

// newTestTOTPService wires a real TOTPService onto an in-memory pika
// service. Mirrors newTestPasskeyService.
func newTestTOTPService(t *testing.T) (*service.Service, *service.TOTPService) {
	t.Helper()
	svc := newTestService(t)
	ts := service.NewTOTPService(svc, "PikaTest")
	svc.SetTOTPService(ts)
	return svc, ts
}

// codeNow generates the live TOTP code for a base32 secret with the
// default config. Used by tests to simulate the authenticator app.
func codeNow(t *testing.T, base32Secret string) string {
	t.Helper()
	sec, err := totp.SecretFromBase32(base32Secret)
	if err != nil {
		t.Fatalf("decode base32 secret: %v", err)
	}
	code, err := totp.Default().Generate(sec, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	return code
}

func TestTOTP_EnrollRoundTrip(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	// Initial status: not enrolled, not pending.
	status, err := ts.Status(ctx, uid)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Enabled || status.PendingEnrollment {
		t.Errorf("fresh user should have no TOTP: %+v", status)
	}

	// Begin: get the secret and URL.
	enroll, err := ts.BeginEnroll(ctx, uid)
	if err != nil {
		t.Fatalf("BeginEnroll: %v", err)
	}
	if enroll.SecretBase32 == "" {
		t.Error("BeginEnroll returned empty secret")
	}
	if !strings.HasPrefix(enroll.OTPAuthURL, "otpauth://totp/PikaTest:alice?") {
		t.Errorf("OTPAuthURL has unexpected shape: %q", enroll.OTPAuthURL)
	}

	// Status mid-enrollment: pending.
	status, err = ts.Status(ctx, uid)
	if err != nil {
		t.Fatalf("Status mid-enroll: %v", err)
	}
	if status.Enabled {
		t.Error("status flipped Enabled before Finish")
	}
	if !status.PendingEnrollment {
		t.Error("status should show PendingEnrollment=true between Begin and Finish")
	}

	// Finish with the live code.
	result, err := ts.FinishEnroll(ctx, uid, codeNow(t, enroll.SecretBase32))
	if err != nil {
		t.Fatalf("FinishEnroll: %v", err)
	}
	if len(result.RecoveryCodes) != 10 {
		t.Errorf("recovery codes count: got %d want 10", len(result.RecoveryCodes))
	}
	for _, c := range result.RecoveryCodes {
		if len(c) != 14 || strings.Count(c, "-") != 2 {
			t.Errorf("recovery code shape unexpected: %q", c)
		}
	}

	// Status after Finish: enabled, 10 codes left.
	status, err = ts.Status(ctx, uid)
	if err != nil {
		t.Fatalf("Status after finish: %v", err)
	}
	if !status.Enabled {
		t.Error("status should be Enabled after Finish")
	}
	if status.RecoveryCodesLeft != 10 {
		t.Errorf("RecoveryCodesLeft: got %d want 10", status.RecoveryCodesLeft)
	}
}

func TestTOTP_FinishWithWrongCodeRejected(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	if _, err := ts.BeginEnroll(ctx, uid); err != nil {
		t.Fatalf("BeginEnroll: %v", err)
	}

	_, err := ts.FinishEnroll(ctx, uid, "000000")
	if !errors.Is(err, service.ErrTOTPInvalidCode) {
		t.Errorf("FinishEnroll with wrong code: got %v want ErrTOTPInvalidCode", err)
	}

	// Should still be pending (not enabled) so a second attempt works.
	status, _ := ts.Status(ctx, uid)
	if status.Enabled {
		t.Error("Enabled flipped to true after a failed Finish")
	}
}

func TestTOTP_VerifyForLoginCode(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	enroll, _ := ts.BeginEnroll(ctx, uid)
	if _, err := ts.FinishEnroll(ctx, uid, codeNow(t, enroll.SecretBase32)); err != nil {
		t.Fatalf("FinishEnroll: %v", err)
	}

	// Login-time verification.
	if err := ts.VerifyForLogin(ctx, uid, codeNow(t, enroll.SecretBase32)); err != nil {
		t.Errorf("VerifyForLogin live code: %v", err)
	}

	// Wrong code rejected.
	if err := ts.VerifyForLogin(ctx, uid, "000000"); !errors.Is(err, service.ErrTOTPInvalidCode) {
		t.Errorf("VerifyForLogin wrong code: got %v want ErrTOTPInvalidCode", err)
	}
}

// TestTOTP_ReplayRejection verifies the replay guard: the same valid
// code can't authenticate twice in the same window. Without this, an
// attacker who shoulder-surfs a code at second 25 has up to 35
// seconds to use it themselves.
func TestTOTP_ReplayRejection(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	enroll, _ := ts.BeginEnroll(ctx, uid)
	if _, err := ts.FinishEnroll(ctx, uid, codeNow(t, enroll.SecretBase32)); err != nil {
		t.Fatalf("FinishEnroll: %v", err)
	}

	code := codeNow(t, enroll.SecretBase32)
	if err := ts.VerifyForLogin(ctx, uid, code); err != nil {
		t.Fatalf("first verify: %v", err)
	}

	// Same code, same user — must be rejected.
	if err := ts.VerifyForLogin(ctx, uid, code); !errors.Is(err, service.ErrTOTPInvalidCode) {
		t.Errorf("replay of just-burned code: got %v want ErrTOTPInvalidCode", err)
	}
}

func TestTOTP_RecoveryCodeBurn(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	enroll, _ := ts.BeginEnroll(ctx, uid)
	result, err := ts.FinishEnroll(ctx, uid, codeNow(t, enroll.SecretBase32))
	if err != nil {
		t.Fatalf("FinishEnroll: %v", err)
	}

	first := result.RecoveryCodes[0]
	// First use: succeeds.
	if err := ts.VerifyForLogin(ctx, uid, first); err != nil {
		t.Fatalf("recovery code first use: %v", err)
	}
	// Second use: rejected (burned).
	if err := ts.VerifyForLogin(ctx, uid, first); !errors.Is(err, service.ErrTOTPInvalidCode) {
		t.Errorf("recovery code replay: got %v want ErrTOTPInvalidCode", err)
	}

	// Remaining codes are still good.
	status, _ := ts.Status(ctx, uid)
	if status.RecoveryCodesLeft != 9 {
		t.Errorf("RecoveryCodesLeft after burn: got %d want 9", status.RecoveryCodesLeft)
	}

	// A different unused recovery code still works.
	if err := ts.VerifyForLogin(ctx, uid, result.RecoveryCodes[1]); err != nil {
		t.Errorf("second recovery code: %v", err)
	}
}

// TestTOTP_RecoveryCodeCaseInsensitive: users typing the code on a
// phone are very likely to hit autocapitalize or capslock. Both forms
// must verify.
func TestTOTP_RecoveryCodeCaseInsensitive(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	enroll, _ := ts.BeginEnroll(ctx, uid)
	result, _ := ts.FinishEnroll(ctx, uid, codeNow(t, enroll.SecretBase32))

	if err := ts.VerifyForLogin(ctx, uid, strings.ToUpper(result.RecoveryCodes[0])); err != nil {
		t.Errorf("uppercase recovery code: %v", err)
	}
}

func TestTOTP_DisableRequiresPassword(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	uid := createUserHelper(t, svc, "alice") // password set inside helper
	ctx := t.Context()

	enroll, _ := ts.BeginEnroll(ctx, uid)
	if _, err := ts.FinishEnroll(ctx, uid, codeNow(t, enroll.SecretBase32)); err != nil {
		t.Fatalf("FinishEnroll: %v", err)
	}

	// Wrong password rejected.
	if err := ts.Disable(ctx, uid, "wrong-password"); !errors.Is(err, service.ErrUnauthorized) {
		t.Errorf("Disable wrong-pw: got %v want ErrUnauthorized", err)
	}

	// Right password (from createUserHelper).
	if err := ts.Disable(ctx, uid, "test-password-1234"); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	status, _ := ts.Status(ctx, uid)
	if status.Enabled || status.PendingEnrollment {
		t.Errorf("status after disable: %+v want fully cleared", status)
	}
}

func TestTOTP_RegenerateRecoveryCodes(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	enroll, _ := ts.BeginEnroll(ctx, uid)
	first, _ := ts.FinishEnroll(ctx, uid, codeNow(t, enroll.SecretBase32))

	// Regenerate.
	second, err := ts.RegenerateRecoveryCodes(ctx, uid, "test-password-1234")
	if err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	if len(second) != 10 {
		t.Errorf("regenerated count: got %d want 10", len(second))
	}

	// First set's codes must no longer verify.
	if err := ts.VerifyForLogin(ctx, uid, first.RecoveryCodes[0]); !errors.Is(err, service.ErrTOTPInvalidCode) {
		t.Errorf("old recovery code after regen: got %v want ErrTOTPInvalidCode", err)
	}

	// New set's codes verify.
	if err := ts.VerifyForLogin(ctx, uid, second[0]); err != nil {
		t.Errorf("new recovery code: %v", err)
	}
}

func TestTOTP_BeginRejectsAlreadyEnrolled(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	enroll, _ := ts.BeginEnroll(ctx, uid)
	if _, err := ts.FinishEnroll(ctx, uid, codeNow(t, enroll.SecretBase32)); err != nil {
		t.Fatalf("FinishEnroll: %v", err)
	}

	if _, err := ts.BeginEnroll(ctx, uid); !errors.Is(err, service.ErrTOTPAlreadyEnrolled) {
		t.Errorf("Begin while enrolled: got %v want ErrTOTPAlreadyEnrolled", err)
	}
}

// TestTOTP_BeginAllowsRestartWhilePending mirrors the UI flow where
// the user opens BeginEnroll, closes the tab without scanning, then
// comes back later. A fresh BeginEnroll must work (generating a new
// secret) so the user isn't stuck with an old QR they can't see.
func TestTOTP_BeginAllowsRestartWhilePending(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	first, err := ts.BeginEnroll(ctx, uid)
	if err != nil {
		t.Fatalf("first Begin: %v", err)
	}
	second, err := ts.BeginEnroll(ctx, uid)
	if err != nil {
		t.Fatalf("second Begin while pending: %v", err)
	}
	if first.SecretBase32 == second.SecretBase32 {
		t.Error("second Begin returned the same secret (should rotate)")
	}

	// First secret no longer enrolls — finish must succeed only
	// with the second.
	if _, err := ts.FinishEnroll(ctx, uid, codeNow(t, first.SecretBase32)); !errors.Is(err, service.ErrTOTPInvalidCode) {
		t.Errorf("finish with stale secret: got %v want ErrTOTPInvalidCode", err)
	}
	if _, err := ts.FinishEnroll(ctx, uid, codeNow(t, second.SecretBase32)); err != nil {
		t.Errorf("finish with fresh secret: %v", err)
	}
}

func TestTOTP_StepUpPendingRoundTrip(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	_ = svc

	payload := []byte(`{"subject":"alice","provider":"local"}`)
	sid := ts.SavePending(payload)
	if sid == "" {
		t.Fatal("SavePending returned empty sid")
	}

	got := ts.ConsumePending(sid)
	if string(got) != string(payload) {
		t.Errorf("pending payload mismatch: got %q want %q", got, payload)
	}

	// One-shot: second consume returns nil.
	if got := ts.ConsumePending(sid); got != nil {
		t.Errorf("pending reused: got %q want nil", got)
	}
}

func TestTOTP_VerifyForLoginNotEnrolled(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	uid := createUserHelper(t, svc, "alice")

	if err := ts.VerifyForLogin(t.Context(), uid, "123456"); !errors.Is(err, service.ErrTOTPNotEnrolled) {
		t.Errorf("VerifyForLogin without enrollment: got %v want ErrTOTPNotEnrolled", err)
	}
}

// TestTOTP_AdminReset is the escape hatch for "user lost everything".
// The admin endpoint sidesteps the password gate (the admin is
// operating on a different account) and atomically clears both the
// secret and the recovery codes.
func TestTOTP_AdminReset(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	adminID := createUserHelper(t, svc, "admin")
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	enroll, _ := ts.BeginEnroll(ctx, uid)
	if _, err := ts.FinishEnroll(ctx, uid, codeNow(t, enroll.SecretBase32)); err != nil {
		t.Fatalf("FinishEnroll: %v", err)
	}

	// Sanity check: user is enrolled.
	status, _ := ts.Status(ctx, uid)
	if !status.Enabled {
		t.Fatal("user not enrolled before reset")
	}

	if err := ts.AdminResetTOTP(ctx, adminID, uid); err != nil {
		t.Fatalf("AdminResetTOTP: %v", err)
	}

	status, _ = ts.Status(ctx, uid)
	if status.Enabled || status.PendingEnrollment {
		t.Errorf("status after reset: %+v want fully cleared", status)
	}

	// Login with any code now returns "not enrolled" (different
	// error class than "invalid code" — the row is gone).
	if err := ts.VerifyForLogin(ctx, uid, "123456"); !errors.Is(err, service.ErrTOTPNotEnrolled) {
		t.Errorf("VerifyForLogin after reset: got %v want ErrTOTPNotEnrolled", err)
	}

	// User can re-enroll from scratch after the reset.
	enroll2, err := ts.BeginEnroll(ctx, uid)
	if err != nil {
		t.Errorf("re-enroll after reset: %v", err)
	}
	if enroll2.SecretBase32 == enroll.SecretBase32 {
		t.Error("re-enroll returned the same secret — should rotate")
	}
}

// TestTOTP_AdminResetIdempotent: calling reset on a user who isn't
// enrolled succeeds silently. This matches the UI's optimistic
// behavior where the admin clicks "Reset 2FA" without first checking
// whether the user actually has TOTP on.
func TestTOTP_AdminResetIdempotent(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	adminID := createUserHelper(t, svc, "admin")
	uid := createUserHelper(t, svc, "alice")

	// No enrollment, reset is no-op.
	if err := ts.AdminResetTOTP(t.Context(), adminID, uid); err != nil {
		t.Errorf("AdminResetTOTP on un-enrolled user: %v", err)
	}
}

// TestTOTP_AdminResetUnknownUser surfaces a missing user as an error.
// Without this the admin would silently succeed on a typo'd user id
// and never know they targeted the wrong account.
func TestTOTP_AdminResetUnknownUser(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	adminID := createUserHelper(t, svc, "admin")
	if err := ts.AdminResetTOTP(t.Context(), adminID, "nonexistent-user-id"); err == nil {
		t.Error("AdminResetTOTP on missing user should error")
	}
}

// TestTOTP_AdminResetSkipsPasswordGate documents the contract:
// AdminResetTOTP does NOT validate any password — it is the
// escape-hatch path. Disable (the self-service equivalent) DOES
// require password. The two methods are deliberately distinct.
func TestTOTP_AdminResetSkipsPasswordGate(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	adminID := createUserHelper(t, svc, "admin")
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	enroll, _ := ts.BeginEnroll(ctx, uid)
	if _, err := ts.FinishEnroll(ctx, uid, codeNow(t, enroll.SecretBase32)); err != nil {
		t.Fatalf("FinishEnroll: %v", err)
	}

	// Self-service Disable with a wrong password fails.
	if err := ts.Disable(ctx, uid, "wrong-password"); !errors.Is(err, service.ErrUnauthorized) {
		t.Errorf("Disable with wrong pw: got %v want ErrUnauthorized", err)
	}

	// Admin reset with no password succeeds.
	if err := ts.AdminResetTOTP(ctx, adminID, uid); err != nil {
		t.Errorf("AdminResetTOTP without password: %v", err)
	}
}

// TestTOTP_AdminResetRejectsSelf is the critical defense-in-depth
// check: even an authenticated admin must not be able to reset
// their OWN 2FA via the admin endpoint. The admin path skips the
// password gate, so allowing it for self-reset would mean a stolen
// session cookie can disable the second factor — defeating the
// point of having one. Self-disable must go through the
// password-gated Disable() method.
func TestTOTP_AdminResetRejectsSelf(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	adminID := createUserHelper(t, svc, "admin")
	ctx := t.Context()

	// Enroll the admin in TOTP.
	enroll, _ := ts.BeginEnroll(ctx, adminID)
	if _, err := ts.FinishEnroll(ctx, adminID, codeNow(t, enroll.SecretBase32)); err != nil {
		t.Fatalf("FinishEnroll: %v", err)
	}

	// Attempt to reset their own 2FA via the admin path.
	err := ts.AdminResetTOTP(ctx, adminID, adminID)
	if !errors.Is(err, service.ErrForbidden) {
		t.Errorf("self-reset: got %v want ErrForbidden", err)
	}

	// And the row must still be there.
	status, _ := ts.Status(ctx, adminID)
	if !status.Enabled {
		t.Error("self-reset attempt cleared the admin's TOTP — defense bypass")
	}
}

// TestTOTP_AdminResetEmptyTargetID guards the API layer: passing
// an empty target id (e.g. the SPA wrote a bug) must fail loudly
// rather than silently no-op.
func TestTOTP_AdminResetEmptyTargetID(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	adminID := createUserHelper(t, svc, "admin")
	err := ts.AdminResetTOTP(t.Context(), adminID, "")
	if !errors.Is(err, service.ErrBadRequest) {
		t.Errorf("empty target id: got %v want ErrBadRequest", err)
	}
}

// TestUserInfo_HasTOTP_PopulatedOnList: the admin user list must
// flag which users have TOTP enabled so the UI can show the badge
// and decide whether to surface the Reset button.
func TestUserInfo_HasTOTP_PopulatedOnList(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	ctx := t.Context()
	aliceID := createUserHelper(t, svc, "alice")
	bobID := createUserHelper(t, svc, "bob")
	_ = bobID

	enroll, _ := ts.BeginEnroll(ctx, aliceID)
	if _, err := ts.FinishEnroll(ctx, aliceID, codeNow(t, enroll.SecretBase32)); err != nil {
		t.Fatalf("FinishEnroll alice: %v", err)
	}

	infos, _, err := svc.ListUsers(ctx, nil)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	var alice, bob *service.UserInfo
	for i := range infos {
		switch infos[i].Username {
		case "alice":
			alice = &infos[i]
		case "bob":
			bob = &infos[i]
		}
	}
	if alice == nil || bob == nil {
		t.Fatalf("missing users in list: %+v", infos)
	}
	if !alice.HasTOTP {
		t.Error("alice should have HasTOTP=true after enrollment")
	}
	if bob.HasTOTP {
		t.Error("bob should have HasTOTP=false (never enrolled)")
	}
}

// TestUserInfo_HasTOTP_PendingNotCounted: a user mid-enrollment
// (Enabled=false row) is NOT reported as HasTOTP=true. The badge
// only flips when the live second factor is in place.
func TestUserInfo_HasTOTP_PendingNotCounted(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	ctx := t.Context()
	uid := createUserHelper(t, svc, "alice")
	if _, err := ts.BeginEnroll(ctx, uid); err != nil {
		t.Fatalf("BeginEnroll: %v", err)
	}
	// Don't call FinishEnroll.

	info, err := svc.GetUser(ctx, uid)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if info.HasTOTP {
		t.Error("pending-enrollment user reported as HasTOTP=true")
	}
}

// TestTOTP_RecoveryCodesUnique guards against a regression where the
// rejection-sampling RNG short-circuits and emits duplicates. A
// duplicate plain code means the bcrypt hashes are different rows but
// the same value verifies twice, which weakens the recovery story.
func TestTOTP_RecoveryCodesUnique(t *testing.T) {
	svc, ts := newTestTOTPService(t)
	uid := createUserHelper(t, svc, "alice")
	ctx := t.Context()

	enroll, _ := ts.BeginEnroll(ctx, uid)
	result, _ := ts.FinishEnroll(ctx, uid, codeNow(t, enroll.SecretBase32))

	seen := make(map[string]struct{}, len(result.RecoveryCodes))
	for _, c := range result.RecoveryCodes {
		if _, dup := seen[c]; dup {
			t.Errorf("duplicate recovery code: %q", c)
		}
		seen[c] = struct{}{}
	}
}
