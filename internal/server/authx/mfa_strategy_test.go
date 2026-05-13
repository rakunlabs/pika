package authx

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/identity"
	"github.com/rakunlabs/ada/middleware/auth/strategy"
	"github.com/rakunlabs/ada/middleware/auth/strategy/totp"

	"github.com/rakunlabs/pika/internal/service"
)

// fakeAuthenticator is a tiny strategy.Authenticator stub that lets
// tests inject either a successful identity or a failure outcome.
// Mirrors how ada's own tests stub strategies — no HTTP framework
// pulled in.
type fakeAuthenticator struct {
	name    string
	id      *identity.Identity
	outcome strategy.Outcome
	// regCallCount is bumped when Register is invoked. Used by the
	// pass-through test to assert the wrapper routed correctly.
	regCallCount *int
}

func (f *fakeAuthenticator) Name() string                   { return f.name }
func (f *fakeAuthenticator) Descriptor() strategy.Descriptor { return strategy.Descriptor{Name: f.name, Kind: "password"} }
func (f *fakeAuthenticator) Logout(_ context.Context, _ *identity.Identity) error { return nil }

func (f *fakeAuthenticator) Login(w http.ResponseWriter, r *http.Request) (*identity.Identity, strategy.Outcome, error) {
	switch f.outcome {
	case strategy.OutcomeContinue:
		return f.id, strategy.OutcomeContinue, nil
	case strategy.OutcomeFailed:
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_credentials","message":"bad"}`))
		return nil, strategy.OutcomeFailed, nil
	case strategy.OutcomePending:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"phase":"inner_pending"}`))
		return nil, strategy.OutcomePending, nil
	}
	return nil, strategy.OutcomeFailed, nil
}

type fakeRegisterer struct {
	*fakeAuthenticator
}

func (f *fakeRegisterer) Register(w http.ResponseWriter, r *http.Request) (*identity.Identity, strategy.Outcome, error) {
	if f.regCallCount != nil {
		*f.regCallCount++
	}
	return f.id, strategy.OutcomeContinue, nil
}

// TestMFAStrategy_NoCoordPassThrough: when coord is nil, the wrapper
// is a transparent pass-through. The decorator can be applied
// unconditionally (whether or not TOTP is enabled in this
// deployment) without changing the inner's behavior.
func TestMFAStrategy_NoCoordPassThrough(t *testing.T) {
	svc := newTestService(t)
	inner := &fakeAuthenticator{
		name:    "local",
		id:      &identity.Identity{Subject: "alice", Provider: "local"},
		outcome: strategy.OutcomeContinue,
	}
	wrap := NewMFAStrategy(inner, svc, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login/pass/local", strings.NewReader(`{}`))
	id, outcome, err := wrap.Login(rec, req)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if outcome != strategy.OutcomeContinue {
		t.Errorf("outcome: got %v want OutcomeContinue", outcome)
	}
	if id == nil || id.Subject != "alice" {
		t.Errorf("identity not forwarded: %+v", id)
	}
}

// TestMFAStrategy_PassThroughUserWithoutTOTP: when coord exists but
// the resolved user has no TOTP row, the wrapper still passes
// through. This is the resting state of a fresh install — every
// password login should work even though TOTP is wired in.
func TestMFAStrategy_PassThroughUserWithoutTOTP(t *testing.T) {
	svc := newTestService(t)
	uid := createUserHelper(t, svc, "alice")
	_ = uid // user exists; no TOTP row yet
	coord := service.NewTOTPService(svc, "PikaTest")

	inner := &fakeAuthenticator{
		name:    "local",
		id:      &identity.Identity{Subject: "alice", Provider: "local"},
		outcome: strategy.OutcomeContinue,
	}
	wrap := NewMFAStrategy(inner, svc, coord)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login/pass/local", strings.NewReader(`{}`))
	id, outcome, err := wrap.Login(rec, req)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if outcome != strategy.OutcomeContinue || id == nil {
		t.Errorf("non-TOTP user blocked: outcome=%v id=%+v", outcome, id)
	}
}

// TestMFAStrategy_StepUpForEnrolledUser walks the end-to-end MFA
// flow: enroll alice for TOTP, then attempt a login. Phase 1 must
// return a step-up challenge (OutcomePending) rather than handing
// the identity to ada for session minting. Phase 2 (with the right
// code) must hand back the identity.
func TestMFAStrategy_StepUpForEnrolledUser(t *testing.T) {
	svc := newTestService(t)
	uid := createUserHelper(t, svc, "alice")
	coord := service.NewTOTPService(svc, "PikaTest")
	svc.SetTOTPService(coord)

	// Enroll alice via the service so the wrapper picks up the
	// Enabled row.
	enroll, err := coord.BeginEnroll(t.Context(), uid)
	if err != nil {
		t.Fatalf("BeginEnroll: %v", err)
	}
	sec, _ := totp.SecretFromBase32(enroll.SecretBase32)
	code, _ := totp.Default().Generate(sec, time.Now())
	if _, err := coord.FinishEnroll(t.Context(), uid, code); err != nil {
		t.Fatalf("FinishEnroll: %v", err)
	}

	inner := &fakeAuthenticator{
		name:    "local",
		id:      &identity.Identity{Subject: "alice", Provider: "local"},
		outcome: strategy.OutcomeContinue,
	}
	wrap := NewMFAStrategy(inner, svc, coord)

	// Phase 1: empty body simulates a successful inner password
	// verification. The wrapper should see "alice has TOTP",
	// return OutcomePending, and write the step-up challenge.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login/pass/local", strings.NewReader(`{}`))
	id, outcome, err := wrap.Login(rec, req)
	if err != nil {
		t.Fatalf("phase 1 Login: %v", err)
	}
	if outcome != strategy.OutcomePending {
		t.Fatalf("phase 1 outcome: got %v want OutcomePending", outcome)
	}
	if id != nil {
		t.Fatal("phase 1 returned a non-nil identity; ada would mint a session prematurely")
	}

	var challenge struct {
		Phase         string `json:"phase"`
		TOTPSessionID string `json:"totp_session_id"`
		Strategy      string `json:"strategy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &challenge); err != nil {
		t.Fatalf("decode phase-1 response: %v\nbody=%s", err, rec.Body.String())
	}
	if challenge.Phase != "totp_required" {
		t.Errorf("phase: got %q want %q", challenge.Phase, "totp_required")
	}
	if challenge.TOTPSessionID == "" {
		t.Error("phase-1 response missing totp_session_id")
	}
	if challenge.Strategy != "local" {
		t.Errorf("strategy: got %q want %q", challenge.Strategy, "local")
	}

	// Phase 2: post the session id + a fresh code from the same
	// secret. We need a new code because the phase-1 verification
	// path didn't burn any (only the recovery / verify path does);
	// however the time-window replay guard might reject re-using
	// the enrollment-confirm code (we used it for FinishEnroll).
	// Generate a code FROM the same secret AT a slightly later
	// time so the window-key differs from the enrollment code.
	// In practice the test runs within a single TOTP window so we
	// can wait for the next window or use a different time. Wait
	// at most ~30s isn't acceptable in CI — instead use the same
	// code (the replay guard is per (user, code) so a different
	// code is needed; but if the same window happens to produce
	// the same code, the guard rejects it).
	//
	// Workaround: jump 30 seconds into the future via the totp
	// crypto's Generate to ensure we land on a different window
	// before producing the phase-2 code.
	phase2Code, _ := totp.Default().Generate(sec, time.Now().Add(30*time.Second))
	if phase2Code == code {
		// Extremely unlikely but possible — same digits across two
		// windows. Hop one more.
		phase2Code, _ = totp.Default().Generate(sec, time.Now().Add(60*time.Second))
	}

	// The Verify path uses time.Now(); the phase-2 code we produced
	// is for a window 30s in the future. Verify with Skew=1 won't
	// accept it. To exercise phase 2 end-to-end with the wrapper,
	// pop the replay guard for this code by using a freshly minted
	// code at "now" and accepting that the test crosses a window
	// boundary at most rarely. For determinism, just use the same
	// `code` value and tolerate that the replay guard will reject
	// it — instead, assert what we CAN: that the wrapper invokes
	// the verifier and propagates the outcome.
	//
	// Two sub-cases:
	//   (a) bad code → OutcomeFailed with invalid_code body.
	//   (b) good code → OutcomeContinue with identity.
	// We test both.

	// (a) Bad code rejected.
	body, _ := json.Marshal(map[string]string{
		"totp_session_id": challenge.TOTPSessionID,
		"code":            "000000",
	})
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/login/pass/local", bytes.NewReader(body))
	_, outcome2, _ := wrap.Login(rec2, req2)
	if outcome2 != strategy.OutcomeFailed {
		t.Errorf("bad-code outcome: got %v want OutcomeFailed", outcome2)
	}
	if !strings.Contains(rec2.Body.String(), "invalid") {
		t.Errorf("bad-code response should mention invalid: %s", rec2.Body.String())
	}

	// (b) Good code: start a fresh phase-1 to get a new session id
	// (the previous one was consumed by the bad attempt), then
	// finish with a live code. Use a recovery code instead — it
	// bypasses the time-window worry.
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/login/pass/local", strings.NewReader(`{}`))
	_, _, _ = wrap.Login(rec3, req3)
	var ch2 struct{ TOTPSessionID string `json:"totp_session_id"` }
	_ = json.Unmarshal(rec3.Body.Bytes(), &ch2)

	// Mint a fresh recovery code by regenerating.
	regenCodes, err := coord.RegenerateRecoveryCodes(t.Context(), uid, "test-password-1234")
	if err != nil {
		t.Fatalf("RegenerateRecoveryCodes: %v", err)
	}

	body2, _ := json.Marshal(map[string]string{
		"totp_session_id": ch2.TOTPSessionID,
		"code":            regenCodes[0],
	})
	rec4 := httptest.NewRecorder()
	req4 := httptest.NewRequest(http.MethodPost, "/login/pass/local", bytes.NewReader(body2))
	id4, outcome4, err := wrap.Login(rec4, req4)
	if err != nil {
		t.Fatalf("phase 2 good-code: %v", err)
	}
	if outcome4 != strategy.OutcomeContinue {
		t.Errorf("good-code outcome: got %v want OutcomeContinue", outcome4)
	}
	if id4 == nil || id4.Subject != "alice" {
		t.Errorf("good-code identity: %+v", id4)
	}
}

// TestMFAStrategy_RegistererPassThrough ensures the wrapper forwards
// Register calls to an inner that supports signup (the local
// strategy with WithRegistrar). The decorator must not block
// registration because brand-new users can't have TOTP enrolled.
func TestMFAStrategy_RegistererPassThrough(t *testing.T) {
	svc := newTestService(t)
	regCount := 0
	inner := &fakeRegisterer{
		fakeAuthenticator: &fakeAuthenticator{
			name:         "local",
			id:           &identity.Identity{Subject: "bob", Provider: "local"},
			outcome:      strategy.OutcomeContinue,
			regCallCount: &regCount,
		},
	}
	wrap := NewMFAStrategyWithRegister(inner, svc, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login/register/local", strings.NewReader(`{}`))
	id, outcome, err := wrap.Register(rec, req)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if outcome != strategy.OutcomeContinue || id == nil {
		t.Errorf("register: outcome=%v id=%+v", outcome, id)
	}
	if regCount != 1 {
		t.Errorf("inner Register call count: got %d want 1", regCount)
	}
}

// TestMFAStrategy_InnerFailNotMasked: when the inner strategy
// rejects credentials, the wrapper must pass that through verbatim
// (OutcomeFailed + inner's body). We must not accidentally swallow
// the inner's error and present a step-up screen.
func TestMFAStrategy_InnerFailNotMasked(t *testing.T) {
	svc := newTestService(t)
	coord := service.NewTOTPService(svc, "PikaTest")

	inner := &fakeAuthenticator{
		name:    "local",
		outcome: strategy.OutcomeFailed,
	}
	wrap := NewMFAStrategy(inner, svc, coord)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login/pass/local", strings.NewReader(`{}`))
	id, outcome, err := wrap.Login(rec, req)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if outcome != strategy.OutcomeFailed {
		t.Errorf("outcome: got %v want OutcomeFailed", outcome)
	}
	if id != nil {
		t.Errorf("identity should be nil on inner fail: %+v", id)
	}
	if !strings.Contains(rec.Body.String(), "invalid_credentials") {
		t.Errorf("inner's response body lost: %s", rec.Body.String())
	}
}

// TestIsMFAFinishBody covers the dispatch oracle that decides phase
// 1 vs phase 2. Important: a phase-1 body must NOT accidentally
// match because it carries one of the two keys.
func TestIsMFAFinishBody(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`{}`, false},
		{``, false},
		{`{"username":"alice","password":"hunter2"}`, false},
		{`{"code":"123456"}`, false},                                          // missing session id
		{`{"totp_session_id":"abc"}`, false},                                  // missing code
		{`{"totp_session_id":"abc","code":"123456"}`, true},                   // happy path
		{`{"totp_session_id":"abc","code":"123456","extra":"ignored"}`, true}, // tolerates extras
		{`malformed`, false},
		{`{"phase":"begin"}`, false},
	}
	for _, c := range cases {
		got := isMFAFinishBody([]byte(c.body))
		if got != c.want {
			t.Errorf("isMFAFinishBody(%q): got %v want %v", c.body, got, c.want)
		}
	}
}

// ── small helper local to this file ──

// createUserHelper inserts a user via the public CreateUser API so
// password hash / timestamps are populated correctly. Duplicates the
// helper from service tests because authx and service are different
// packages and the helper isn't exported.
func createUserHelper(t *testing.T, svc *service.Service, username string) string {
	t.Helper()
	info, err := svc.CreateUser(t.Context(), &service.CreateUserRequest{
		Username: username,
		Password: "test-password-1234",
	})
	if err != nil {
		t.Fatalf("CreateUser(%q): %v", username, err)
	}
	return info.ID
}
