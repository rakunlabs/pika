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

// TestSessionStore_ProvisionsExternalUser verifies that when an OAuth2
// login writes a session with provider != "local", the session store
// auto-creates the users row (via FindOrCreateExternalUser) and links
// the session to it. Without this, OAuth2 users would be invisible to
// admin operations like kick and list.
func TestSessionStore_ProvisionsExternalUser(t *testing.T) {
	svc := newTestService(t)
	store := NewSessionStore(svc, "pika_session")

	const sid = "oauth-sid-1"
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "pika_session", Value: sid})

	sess, err := store.Get(r, "pika_session")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Ada issuer shape but with provider="google" (external). This is
	// what the session store sees when an OAuth2 login just completed.
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

	// Verify the users row exists with the external flag set.
	info, err := svc.GetUserByIdentity(context.Background(), "google", "google-sub-777")
	if err != nil {
		t.Fatalf("GetUserByIdentity: %v", err)
	}
	if !info.External {
		t.Error("provisioned user should have External=true")
	}
	if info.Email != "carol@example.com" {
		t.Errorf("Email = %q", info.Email)
	}

	// Verify the session row points at that user.
	row, err := svc.GetRawSession(context.Background(), sid)
	if err != nil {
		t.Fatalf("GetRawSession: %v", err)
	}
	if row.UserID != info.ID {
		t.Errorf("session.UserID = %q, want %q (provisioned)", row.UserID, info.ID)
	}

	// Kick works for OAuth2 users too.
	if err := svc.KickUser(context.Background(), info.ID); err != nil {
		t.Fatalf("KickUser: %v", err)
	}
	if _, err := svc.GetRawSession(context.Background(), sid); err == nil {
		t.Error("OAuth2 session should be gone after kick")
	}
}
