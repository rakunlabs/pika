package authx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/sessionstore"

	"github.com/rakunlabs/pika/internal/service"
)

// TestSessionStoreImplementsDirectStore is a boot guard, not a nicety.
//
// ada's issuer backend rejects a session store that cannot address records
// by raw session ID: it owns its own IDs and has no request/response pair
// to drive Get/Save with. The rejection surfaces from auth.Init, which
// means the whole server refuses to start — a failure mode no unit test
// would otherwise catch, because every other test builds the store
// directly.
func TestSessionStoreImplementsDirectStore(t *testing.T) {
	svc := newTestService(t)

	var store sessionstore.Store = NewSessionStore(svc, "pika_session")

	if _, ok := store.(sessionstore.DirectStore); !ok {
		t.Fatal("SessionStore must implement sessionstore.DirectStore or auth.Init refuses to boot")
	}
}

// TestDirectStoreRoundTrip exercises the path the issuer actually uses.
func TestDirectStoreRoundTrip(t *testing.T) {
	svc := newTestService(t)
	store := NewSessionStore(svc, "pika_session")
	ctx := t.Context()

	if err := store.SaveByID(ctx, "sess-1", map[string]any{"username": "alice"}, time.Hour); err != nil {
		t.Fatalf("SaveByID: %v", err)
	}

	values, err := store.LoadByID(ctx, "sess-1")
	if err != nil {
		t.Fatalf("LoadByID: %v", err)
	}
	if values["username"] != "alice" {
		t.Errorf("round-trip lost values: %+v", values)
	}

	if err := store.DeleteByID(ctx, "sess-1"); err != nil {
		t.Fatalf("DeleteByID: %v", err)
	}
	if _, err := store.LoadByID(ctx, "sess-1"); !errors.Is(err, sessionstore.ErrNoSession) {
		t.Errorf("after delete: expected ErrNoSession, got %v", err)
	}
}

// TestDirectStoreMissingAndExpired pins the two "no session" cases. The
// issuer distinguishes ErrNoSession from a storage failure — reporting a
// missing session as an error would turn an ordinary logged-out request
// into a 500.
func TestDirectStoreMissingAndExpired(t *testing.T) {
	svc := newTestService(t)
	store := NewSessionStore(svc, "pika_session")
	ctx := t.Context()

	if _, err := store.LoadByID(ctx, "never-existed"); !errors.Is(err, sessionstore.ErrNoSession) {
		t.Errorf("unknown id: expected ErrNoSession, got %v", err)
	}

	// Write an already-expired row straight through the service so the
	// read path has to be the thing that notices.
	if err := svc.PutRawSession(ctx, &service.RawSession{
		ID:        "stale",
		Payload:   []byte(`{"username":"bob"}`),
		ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("PutRawSession: %v", err)
	}

	if _, err := store.LoadByID(ctx, "stale"); !errors.Is(err, sessionstore.ErrNoSession) {
		t.Errorf("expired id: expected ErrNoSession, got %v", err)
	}

	// Deleting an unknown id is explicitly not an error.
	if err := store.DeleteByID(ctx, "never-existed"); err != nil {
		t.Errorf("DeleteByID on unknown id: %v", err)
	}
}

// TestSaveByIDResolvesUser is what makes session administration work.
//
// Kick, the disabled-user sweep and the admin session list all key off the
// user_id column, and the capability resolver reads the stamped
// pika_user_id claim on every request. Both are produced during the save.
// SaveByID is now the write path the issuer takes, so a version of it that
// skipped this resolution would leave every session unattributed — and the
// symptom would be "kick silently does nothing", far from the cause.
func TestSaveByIDResolvesUser(t *testing.T) {
	svc := newTestService(t)
	store := NewSessionStore(svc, "pika_session")
	ctx := t.Context()

	user, err := svc.CreateUser(ctx, &service.CreateUserRequest{
		Username: "alice",
		Password: "test-password-1234",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// The shape ada's issuer persists: the Pair as a JSON string.
	values := map[string]any{
		"pair": `{"identity":{"subject":"alice","provider":"local"}}`,
	}
	if err := store.SaveByID(ctx, "sess-1", values, time.Hour); err != nil {
		t.Fatalf("SaveByID: %v", err)
	}

	rows, err := svc.ListRawSessionsByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListRawSessionsByUser: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("session was not attributed to the user: %d rows", len(rows))
	}

	loaded, err := store.LoadByID(ctx, "sess-1")
	if err != nil {
		t.Fatalf("LoadByID: %v", err)
	}

	id := reconstructIdentity(loaded)
	if id == nil {
		t.Fatal("stored payload has no identity")
	}
	if got, _ := id.Claims[PikaUserIDClaim].(string); got != user.ID {
		t.Errorf("pika_user_id claim = %q, want %q", got, user.ID)
	}
}

// TestSaveByIDAppliesDefaultTTL covers the ttl=0 contract: the issuer means
// "no explicit expiry", but pika collects sessions by expiry, so a row
// without one would never be swept.
func TestSaveByIDAppliesDefaultTTL(t *testing.T) {
	svc := newTestService(t)
	store := NewSessionStore(svc, "pika_session")
	ctx := t.Context()

	if err := store.SaveByID(ctx, "sess-1", map[string]any{"k": "v"}, 0); err != nil {
		t.Fatalf("SaveByID: %v", err)
	}

	row, err := svc.GetRawSession(ctx, "sess-1")
	if err != nil {
		t.Fatalf("GetRawSession: %v", err)
	}
	if !row.ExpiresAt.After(time.Now()) {
		t.Fatalf("row has no future expiry: %v", row.ExpiresAt)
	}
}

// TestSaveSetsHardenedCookieAttributes guards the defaults that changed
// with ada's inversion. pika previously shipped a session cookie that was
// neither HttpOnly nor Secure unless an operator went looking for the
// settings.
func TestSaveSetsHardenedCookieAttributes(t *testing.T) {
	svc := newTestService(t)
	store := NewSessionStore(svc, "pika_session")

	tests := []struct {
		name       string
		tls        bool
		wantSecure bool
	}{
		{name: "plain http keeps working", tls: false, wantSecure: false},
		{name: "https marks the cookie Secure", tls: true, wantSecure: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r = r.WithContext(context.Background())
			if tt.tls {
				r.Header.Set("X-Forwarded-Proto", "https")
			}

			sess, err := store.Get(r, "pika_session")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}

			w := httptest.NewRecorder()
			if err := store.Save(r, w, sess); err != nil {
				t.Fatalf("Save: %v", err)
			}

			cookies := w.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("expected one cookie, got %d", len(cookies))
			}

			if !cookies[0].HttpOnly {
				t.Error("session cookie must be HttpOnly")
			}
			if cookies[0].Secure != tt.wantSecure {
				t.Errorf("Secure = %v, want %v", cookies[0].Secure, tt.wantSecure)
			}
		})
	}
}
