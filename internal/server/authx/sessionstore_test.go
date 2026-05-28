package authx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/sessionstore"

	"github.com/rakunlabs/pika/internal/service"
)

func TestSessionStore_NewSession(t *testing.T) {
	svc := newTestService(t)
	store := NewSessionStore(svc, "pika_session")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	s, err := store.Get(r, "pika_session")
	if err != nil {
		t.Fatalf("Get on empty request: %v", err)
	}
	if !s.IsNew {
		t.Error("expected IsNew=true for fresh session")
	}
}

func TestSessionStore_SaveLoad(t *testing.T) {
	svc := newTestService(t)
	store := NewSessionStore(svc, "pika_session")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	s, _ := store.Get(r, "pika_session")
	s.Values["identity"] = map[string]any{"subject": "alice"}
	if err := store.Save(r, w, s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if s.ID == "" {
		t.Error("Save should assign a session ID")
	}

	// Simulate subsequent request with the cookie.
	cookie := w.Result().Cookies()[0]
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.AddCookie(cookie)

	loaded, err := store.Get(r2, "pika_session")
	if err != nil {
		t.Fatalf("Get loaded: %v", err)
	}
	if loaded.IsNew {
		t.Error("loaded session should not be IsNew")
	}
	if v, _ := loaded.Values["identity"].(map[string]any); v["subject"] != "alice" {
		t.Errorf("identity not preserved: %+v", loaded.Values)
	}
}

func TestSessionStore_DeleteOnMaxAgeNegative(t *testing.T) {
	svc := newTestService(t)
	store := NewSessionStore(svc, "pika_session")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	s, _ := store.Get(r, "pika_session")
	s.Values["x"] = 1
	_ = store.Save(r, w, s)

	// Now request deletion
	s.Options = &sessionstore.Options{MaxAge: -1}
	if err := store.Save(r, w, s); err != nil {
		t.Fatalf("Save(delete): %v", err)
	}

	// Row should be gone — confirm by looking up raw
	if _, err := svc.GetRawSession(context.Background(), s.ID); err == nil {
		t.Error("expected session row to be deleted")
	}
}

// TestSessionStore_PreservesCookieIDOnMissingRow verifies that when a cookie
// is presented for a session that does not yet have a backing DB row, Get
// returns a session whose ID is the cookie value. This is required by ada's
// issuer backend, which synthesizes a request with a chosen SessionID and
// expects Save to persist under that same ID. Regression test for the
// "login succeeds but the next /login/me returns 401" bug.
func TestSessionStore_PreservesCookieIDOnMissingRow(t *testing.T) {
	svc := newTestService(t)
	store := NewSessionStore(svc, "pika_session")

	const wantID = "caller-chosen-session-id"

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "pika_session", Value: wantID})

	sess, err := store.Get(r, "pika_session")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if sess.ID != wantID {
		t.Fatalf("sess.ID: got %q, want %q", sess.ID, wantID)
	}
	if !sess.IsNew {
		t.Error("sess.IsNew should be true when no row exists yet")
	}

	// Save under that ID, then verify a subsequent Get on the same cookie
	// resolves to the same row with IsNew=false.
	sess.Values["marker"] = "hello"
	w := httptest.NewRecorder()
	if err := store.Save(r, w, sess); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if sess.ID != wantID {
		t.Fatalf("Save mutated ID: got %q, want %q", sess.ID, wantID)
	}

	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.AddCookie(&http.Cookie{Name: "pika_session", Value: wantID})
	loaded, err := store.Get(r2, "pika_session")
	if err != nil {
		t.Fatalf("Get (round-trip): %v", err)
	}
	if loaded.IsNew {
		t.Error("round-tripped session should not be IsNew")
	}
	if loaded.Values["marker"] != "hello" {
		t.Errorf("values not preserved: %+v", loaded.Values)
	}
}

// TestSessionStore_SaveCapturesUserIDFromPair verifies that when the ada
// issuer stores an issuer.Pair under sess.Values["pair"], the resulting DB
// row carries the correct user_id so admin-driven kick (DeleteByUserID)
// actually terminates the session. Without this, kicking a user would leave
// their existing cookie valid until its TTL.
func TestSessionStore_SaveCapturesUserIDFromPair(t *testing.T) {
	svc := newTestService(t)

	// Seed a user so username -> user_id lookup succeeds.
	user, err := svc.CreateSetupUser(context.Background(), &service.CreateUserRequest{
		Username: "alice",
		Password: "s3cret!",
	})
	if err != nil {
		t.Fatalf("CreateSetupUser: %v", err)
	}

	store := NewSessionStore(svc, "pika_session")

	const sid = "sid-123"
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "pika_session", Value: sid})

	sess, err := store.Get(r, "pika_session")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Shape matches ada's issuer backend: sess.Values["pair"] is a JSON
	// string of issuer.Pair, whose identity.subject is the username.
	pairJSON, _ := json.Marshal(map[string]any{
		"session_id": sid,
		"identity":   map[string]any{"subject": "alice"},
		"access":     map[string]any{"value": "a", "expires_at": time.Now().Add(time.Hour)},
		"refresh":    map[string]any{"value": "r", "expires_at": time.Now().Add(24 * time.Hour)},
	})
	sess.Values["pair"] = string(pairJSON)

	w := httptest.NewRecorder()
	if err := store.Save(r, w, sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	row, err := svc.GetRawSession(context.Background(), sid)
	if err != nil {
		t.Fatalf("GetRawSession: %v", err)
	}
	if row.Username != "alice" {
		t.Errorf("row.Username: got %q, want alice", row.Username)
	}
	if row.UserID != user.ID {
		t.Errorf("row.UserID: got %q, want %q", row.UserID, user.ID)
	}

	// Kick by user ID: simulates the admin "Kick" button.
	if err := svc.KickUser(context.Background(), user.ID); err != nil {
		t.Fatalf("KickUser: %v", err)
	}

	// After kick, the session must be gone from the DB and a follow-up
	// Get (even with the same cookie) must return IsNew=true so ada
	// treats it as invalid.
	if _, err := svc.GetRawSession(context.Background(), sid); err == nil {
		t.Error("expected session row to be gone after kick")
	}
	r3 := httptest.NewRequest(http.MethodGet, "/", nil)
	r3.AddCookie(&http.Cookie{Name: "pika_session", Value: sid})
	post, err := store.Get(r3, "pika_session")
	if err != nil {
		t.Fatalf("Get after kick: %v", err)
	}
	if !post.IsNew {
		t.Error("session must be IsNew=true after kick so ada's issuer returns ErrNotFound")
	}
}

// TestSessionStore_BindsExistingExternalUser verifies the happy path
// for an OAuth2 session whose (provider, subject) is already linked
// to a pika user — typically created up-front by the user-sync
// engine. The session row must be bound to that existing user so
// admin operations like kick work uniformly across strategies.
//
// Pre-condition: the user + identity link have been provisioned by
// user-sync (simulated here via FindOrCreateExternalUser, which is
// the legitimate caller). The session-save path itself MUST NOT
// create users — that invariant is covered by
// TestSessionStore_DoesNotProvisionUnknownExternal.
func TestSessionStore_BindsExistingExternalUser(t *testing.T) {
	svc := newTestService(t)

	// Pre-provision the external user as user-sync would.
	preInfo, err := svc.FindOrCreateExternalUser(context.Background(), service.ExternalIdentityInput{
		Provider:      "google",
		Subject:       "google-sub-777",
		Email:         "carol@example.com",
		EmailVerified: true,
		DisplayName:   "Carol",
		Username:      "Carol",
	})
	if err != nil {
		t.Fatalf("pre-provision external user: %v", err)
	}

	store := NewSessionStore(svc, "pika_session")
	const sid = "oauth-sid-1"
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "pika_session", Value: sid})

	sess, err := store.Get(r, "pika_session")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	pairJSON, _ := json.Marshal(map[string]any{
		"session_id": sid,
		"identity": map[string]any{
			"subject":        "google-sub-777",
			"email":          "carol@example.com",
			"email_verified": true,
			"name":           "Carol",
			"provider":       "google",
		},
		"access":  map[string]any{"value": "a", "expires_at": time.Now().Add(time.Hour)},
		"refresh": map[string]any{"value": "r", "expires_at": time.Now().Add(24 * time.Hour)},
	})
	sess.Values["pair"] = string(pairJSON)

	w := httptest.NewRecorder()
	if err := store.Save(r, w, sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	row, err := svc.GetRawSession(context.Background(), sid)
	if err != nil {
		t.Fatalf("GetRawSession: %v", err)
	}
	if row.UserID != preInfo.ID {
		t.Errorf("session.UserID = %q, want %q (pre-provisioned)", row.UserID, preInfo.ID)
	}

	if err := svc.KickUser(context.Background(), preInfo.ID); err != nil {
		t.Fatalf("KickUser: %v", err)
	}
	if _, err := svc.GetRawSession(context.Background(), sid); err == nil {
		t.Error("OAuth2 session should be gone after kick")
	}
}

// TestSessionStore_DoesNotProvisionUnknownExternal is the regression
// test for the default rule: the live auth path MUST NOT create users unless
// the OAuth2 provider explicitly opts into AutoCreateUser. A login from an
// unrecognized external identity must persist the session unbound (no user_id)
// and emit a warning, never silently invent a duplicate users row.
//
// Without this guarantee, an OAuth2 sign-in for an unknown subject
// would create a brand-new "google_2"-style user even when the human
// already exists in pika under a different username — leaking
// permissions and confusing admins.
func TestSessionStore_DoesNotProvisionUnknownExternal(t *testing.T) {
	svc := newTestService(t)
	store := NewSessionStore(svc, "pika_session")

	const sid = "oauth-sid-unknown"
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "pika_session", Value: sid})

	sess, err := store.Get(r, "pika_session")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	pairJSON, _ := json.Marshal(map[string]any{
		"session_id": sid,
		"identity": map[string]any{
			"subject":  "unknown-sub-999",
			"name":     "Stranger",
			"provider": "google",
		},
		"access":  map[string]any{"value": "a", "expires_at": time.Now().Add(time.Hour)},
		"refresh": map[string]any{"value": "r", "expires_at": time.Now().Add(24 * time.Hour)},
	})
	sess.Values["pair"] = string(pairJSON)

	w := httptest.NewRecorder()
	if err := store.Save(r, w, sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Identity must NOT resolve — no user was provisioned.
	if _, err := svc.GetUserByIdentity(context.Background(), "google", "unknown-sub-999"); err == nil {
		t.Error("expected no users row for unknown external identity; auth path provisioned one")
	}

	// Session row still exists (login isn't blocked) but is unbound
	// from any user_id, which is the fail-closed behavior we want.
	row, err := svc.GetRawSession(context.Background(), sid)
	if err != nil {
		t.Fatalf("GetRawSession: %v", err)
	}
	if row.UserID != "" {
		t.Errorf("session.UserID = %q, want empty (unbound, no auto-provision)", row.UserID)
	}
}

func TestSessionStore_AutoCreatesUnknownOAuth2UserWhenEnabled(t *testing.T) {
	svc := newTestService(t)
	if err := svc.SaveSettings(context.Background(), &service.Settings{
		Auth: &service.AuthSettings{
			OAuth2: []service.OAuth2StrategySettings{{Name: "google", AutoCreateUser: true}},
		},
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	store := NewSessionStore(svc, "pika_session")
	const sid = "oauth-sid-autocreate"
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "pika_session", Value: sid})

	sess, err := store.Get(r, "pika_session")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	pairJSON, _ := json.Marshal(map[string]any{
		"session_id": sid,
		"identity": map[string]any{
			"subject":        "new-google-sub",
			"email":          "email-local@example.com",
			"email_verified": true,
			"name":           "Display Name Is Not Unique",
			"claims":         map[string]any{"preferred_username": "preferred_user"},
			"provider":       "google",
		},
		"access":  map[string]any{"value": "a", "expires_at": time.Now().Add(time.Hour)},
		"refresh": map[string]any{"value": "r", "expires_at": time.Now().Add(24 * time.Hour)},
	})
	sess.Values["pair"] = string(pairJSON)

	w := httptest.NewRecorder()
	if err := store.Save(r, w, sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	row, err := svc.GetRawSession(context.Background(), sid)
	if err != nil {
		t.Fatalf("GetRawSession: %v", err)
	}
	if row.UserID == "" {
		t.Fatal("session.UserID is empty; auto-created user was not bound")
	}

	info, err := svc.GetUserByIdentity(context.Background(), "google", "new-google-sub")
	if err != nil {
		t.Fatalf("GetUserByIdentity: %v", err)
	}
	if info.ID != row.UserID {
		t.Errorf("identity user ID = %q, want session user ID %q", info.ID, row.UserID)
	}
	if !info.External {
		t.Error("auto-created OAuth2 user should be external-only")
	}
	if info.Username != "preferred_user" {
		t.Errorf("username = %q, want preferred_username claim", info.Username)
	}
}

func TestSessionStore_AutoCreateUsesExistingVerifiedEmailUser(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	local, err := svc.CreateSetupUser(ctx, &service.CreateUserRequest{Username: "alice", Password: "s3cret"})
	if err != nil {
		t.Fatalf("CreateSetupUser: %v", err)
	}
	email := "alice@example.com"
	if err := svc.UpdateUser(ctx, local.ID, &service.UpdateUserRequest{Email: &email}); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if err := svc.SaveSettings(ctx, &service.Settings{
		Auth: &service.AuthSettings{
			OAuth2: []service.OAuth2StrategySettings{{Name: "google", AutoCreateUser: true}},
		},
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	store := NewSessionStore(svc, "pika_session")
	const sid = "oauth-sid-autolink-existing"
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "pika_session", Value: sid})
	sess, err := store.Get(r, "pika_session")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	pairJSON, _ := json.Marshal(map[string]any{
		"session_id": sid,
		"identity": map[string]any{
			"subject":        "alice-google-sub",
			"email":          "alice@example.com",
			"email_verified": true,
			"name":           "Alice via Google",
			"provider":       "google",
		},
		"access":  map[string]any{"value": "a", "expires_at": time.Now().Add(time.Hour)},
		"refresh": map[string]any{"value": "r", "expires_at": time.Now().Add(24 * time.Hour)},
	})
	sess.Values["pair"] = string(pairJSON)

	w := httptest.NewRecorder()
	if err := store.Save(r, w, sess); err != nil {
		t.Fatalf("Save: %v", err)
	}
	row, err := svc.GetRawSession(ctx, sid)
	if err != nil {
		t.Fatalf("GetRawSession: %v", err)
	}
	if row.UserID != local.ID {
		t.Errorf("session.UserID = %q, want existing local user %q", row.UserID, local.ID)
	}
	if count, err := svc.UserCount(ctx); err != nil || count != 1 {
		t.Fatalf("UserCount = %d, %v; want one reused user", count, err)
	}
}

// TestSessionStore_StampsUserIDClaim locks in the claim-decoration
// contract: after Save resolves (Provider, Subject) → users.id, it
// MUST embed that id into the serialized identity claims under
// PikaUserIDClaim so the per-request CapResolver can skip the
// provider-based dispatch. Without this, every authenticated request
// pays the cost of re-running the dispatch, and worse, every code
// path that re-runs the dispatch is another place the same bug class
// ("passkey treated as external → zero caps") can regress.
func TestSessionStore_StampsUserIDClaim(t *testing.T) {
	svc := newTestService(t)
	user, err := svc.CreateSetupUser(context.Background(), &service.CreateUserRequest{
		Username: "alice",
		Password: "x",
	})
	if err != nil {
		t.Fatalf("CreateSetupUser: %v", err)
	}

	store := NewSessionStore(svc, "pika_session")
	const sid = "claim-sid"
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "pika_session", Value: sid})

	sess, err := store.Get(r, "pika_session")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	pairJSON, _ := json.Marshal(map[string]any{
		"session_id": sid,
		"identity": map[string]any{
			"subject":  "alice",
			"provider": "passkey",
		},
		"access":  map[string]any{"value": "a", "expires_at": time.Now().Add(time.Hour)},
		"refresh": map[string]any{"value": "r", "expires_at": time.Now().Add(24 * time.Hour)},
	})
	sess.Values["pair"] = string(pairJSON)

	w := httptest.NewRecorder()
	if err := store.Save(r, w, sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Re-read the persisted payload and inspect the embedded identity
	// claims. The pair is round-tripped as JSON, so claims surface as
	// a generic map[string]any.
	row, err := svc.GetRawSession(context.Background(), sid)
	if err != nil {
		t.Fatalf("GetRawSession: %v", err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(row.Payload, &persisted); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	pairStr, _ := persisted["pair"].(string)
	if pairStr == "" {
		t.Fatal("payload missing pair")
	}
	var pair map[string]any
	if err := json.Unmarshal([]byte(pairStr), &pair); err != nil {
		t.Fatalf("pair unmarshal: %v", err)
	}
	ident, _ := pair["identity"].(map[string]any)
	claims, _ := ident["claims"].(map[string]any)
	got, _ := claims[PikaUserIDClaim].(string)
	if got != user.ID {
		t.Errorf("pika_user_id claim = %q, want %q", got, user.ID)
	}
}

// TestSessionStore_PasskeyLoginBindsExistingLocalUser locks in the
// passkey ↔ local-user resolution: ada stamps id.Provider with the
// strategy name ("passkey" by default), but a passkey assertion has
// already authenticated against an existing pika user (the credential
// is FK'd to users.id in the passkeys table). The session store must
// recognize "passkey" as local-equivalent and bind to the existing
// user via username lookup — NEVER fall into the external branch and
// auto-create a "<username>_2" duplicate. This was the original bug:
// after enrolling a passkey, the first passkey login spawned a second
// users row.
func TestSessionStore_PasskeyLoginBindsExistingLocalUser(t *testing.T) {
	svc := newTestService(t)

	user, err := svc.CreateSetupUser(context.Background(), &service.CreateUserRequest{
		Username: "alice",
		Password: "s3cret!",
	})
	if err != nil {
		t.Fatalf("CreateSetupUser: %v", err)
	}

	store := NewSessionStore(svc, "pika_session")
	const sid = "passkey-sid-1"
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "pika_session", Value: sid})

	sess, err := store.Get(r, "pika_session")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Mirror what ada's passkey strategy writes after a successful
	// FinishLogin: provider="passkey", subject=<username>.
	pairJSON, _ := json.Marshal(map[string]any{
		"session_id": sid,
		"identity": map[string]any{
			"subject":  "alice",
			"name":     "Alice",
			"provider": "passkey",
		},
		"access":  map[string]any{"value": "a", "expires_at": time.Now().Add(time.Hour)},
		"refresh": map[string]any{"value": "r", "expires_at": time.Now().Add(24 * time.Hour)},
	})
	sess.Values["pair"] = string(pairJSON)

	w := httptest.NewRecorder()
	if err := store.Save(r, w, sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Session must be bound to the EXISTING alice — not a fresh
	// "alice_2" provisioned via the external path.
	row, err := svc.GetRawSession(context.Background(), sid)
	if err != nil {
		t.Fatalf("GetRawSession: %v", err)
	}
	if row.UserID != user.ID {
		t.Errorf("session.UserID = %q, want %q (existing alice)", row.UserID, user.ID)
	}
	if row.Username != "alice" {
		t.Errorf("session.Username = %q, want alice", row.Username)
	}

	// And no spurious external identity link should exist for
	// (provider="passkey", subject="alice").
	if _, err := svc.GetUserByIdentity(context.Background(), "passkey", "alice"); err == nil {
		t.Error("passkey login must not create a user_identities row; passkey is local-equivalent")
	}
}
