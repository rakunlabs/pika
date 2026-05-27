package lockgate_test

import (
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/pika/internal/secret/crypto"
	"github.com/rakunlabs/pika/internal/secret/keymgr"
	"github.com/rakunlabs/pika/internal/server/lockgate"
)

func newUnlockedMgr(t *testing.T) *keymgr.Manager {
	t.Helper()
	key := make([]byte, crypto.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	enc, err := crypto.NewChaCha20(key)
	if err != nil {
		t.Fatalf("new chacha: %v", err)
	}
	m := keymgr.New()
	if err := m.Unlock(enc); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	return m
}

// markerHandler is the downstream handler the gate wraps. It
// records whether it was reached so we can assert on pass-through
// vs. blocked behaviour.
type markerHandler struct {
	called bool
}

func (h *markerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.called = true
	w.WriteHeader(http.StatusOK)
}

// TestUnlockedAllRequestsPassThrough — when the manager is
// unlocked the gate must be a no-op for every path. This is the
// default operating mode and any regression here would be
// catastrophic (every request 503'd in production).
func TestUnlockedAllRequestsPassThrough(t *testing.T) {
	mgr := newUnlockedMgr(t)
	h := &markerHandler{}
	gated := lockgate.Middleware(mgr, "")(h)

	for _, path := range []string{
		"/api/v1/folder",
		"/api/v1/users",
		"/api/v1/random/anything",
		"/",
		"/assets/index-abc.js",
		"/login/password",
	} {
		h.called = false
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		gated.ServeHTTP(w, req)

		if !h.called {
			t.Errorf("path %q: handler not called when unlocked", path)
		}
		if w.Header().Get("X-Pika-Locked") != "" {
			t.Errorf("path %q: X-Pika-Locked set when unlocked", path)
		}
	}
}

// TestNonAPIPathsAlwaysPassThrough — the SPA shell, login flow,
// and static assets must never get the 503 treatment, otherwise a
// user pointing their browser at a locked pika sees raw JSON
// instead of the login screen. This is the property that makes the
// "log in first, then unlock" UX work.
func TestNonAPIPathsAlwaysPassThrough(t *testing.T) {
	mgr := newLockedInitializedMgr(t)
	h := &markerHandler{}
	gated := lockgate.Middleware(mgr, "")(h)

	nonAPI := []string{
		"/", // SPA shell
		"/index.html",
		"/assets/index-abc123.js", // built JS chunks
		"/assets/styles.css",
		"/fonts/inter.woff2",
		"/login",
		"/login/password",
		"/login/me",
		"/login/info",
		"/logout",
		"/folder/foo/bar", // pika's own /folder/* SPA fallback
		"/healthz",        // public probe
	}
	for _, path := range nonAPI {
		h.called = false
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		gated.ServeHTTP(w, req)

		if !h.called {
			t.Errorf("path %q: blocked while locked but should never be gated", path)
		}
		if w.Header().Get("X-Pika-Locked") != "" {
			t.Errorf("path %q: X-Pika-Locked set on non-API path", path)
		}
	}
}

// newLockedInitializedMgr builds a manager in the initialized+locked
// state — the post-initialize, pre-unlock condition that triggers the
// gate. Used by every "while locked" test.
func newLockedInitializedMgr(t *testing.T) *keymgr.Manager {
	t.Helper()
	m := keymgr.New()
	m.MarkInitialized()
	return m
}

// TestFreshInstallSkipsGate — when no verifier exists yet (operator
// hasn't opted in to encryption), the gate must be a no-op. This is
// the legacy "encryption is opt-in" behaviour preserved across the
// new manager wiring; regressing it would block every request on a
// fresh install before the operator can even reach the setup screen.
func TestFreshInstallSkipsGate(t *testing.T) {
	mgr := keymgr.New() // locked AND uninitialized (verifier never written)
	h := &markerHandler{}
	gated := lockgate.Middleware(mgr, "")(h)

	for _, path := range []string{
		"/api/v1/folder",
		"/api/v1/users",
		"/api/v1/settings",
		"/api/v1/key/initialize",
	} {
		h.called = false
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		gated.ServeHTTP(w, req)
		if !h.called {
			t.Errorf("path %q: blocked on fresh install", path)
		}
		if w.Header().Get("X-Pika-Locked") != "" {
			t.Errorf("path %q: X-Pika-Locked set on fresh install", path)
		}
	}
}

// TestLockedAPIAllowlistPassThrough — bootstrap API endpoints
// inside the gated /api/v1/ prefix must still pass through when
// locked. Without these the SPA can't drive the unlock flow.
func TestLockedAPIAllowlistPassThrough(t *testing.T) {
	mgr := newLockedInitializedMgr(t)
	h := &markerHandler{}
	gated := lockgate.Middleware(mgr, "")(h)

	allow := []string{
		"/api/v1/info",       // SPA boot
		"/api/v1/key/status", // key state probe
		"/api/v1/key/unlock", // the unlock action itself
		"/api/v1/me",         // session introspection
		"/api/v1/me/preferences",
		"/api/v1/me/passkeys",
	}
	for _, path := range allow {
		h.called = false
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		gated.ServeHTTP(w, req)

		if !h.called {
			t.Errorf("path %q: blocked while locked but should be allowlisted", path)
		}
		if w.Header().Get("X-Pika-Locked") != "" {
			t.Errorf("path %q: X-Pika-Locked set on allowlisted path", path)
		}
	}
}

// TestLockedGatedPathsBlocked — any API path under the gated
// prefixes that isn't on the allowlist must return 503 with the
// X-Pika-Locked header so the SPA's interceptor can react.
// Initialize is in the blocked set: the verifier is one-shot, and
// the gate enforces it from the other side once the verifier has
// been written.
func TestLockedGatedPathsBlocked(t *testing.T) {
	mgr := newLockedInitializedMgr(t)
	h := &markerHandler{}
	gated := lockgate.Middleware(mgr, "")(h)

	blocked := []string{
		"/api/v1/folder",
		"/api/v1/users",
		"/api/v1/settings",
		"/api/v1/key/initialize",
		"/api/v1/key/rotate",
		"/api/v1/key/lock",
		"/data/some/config",
	}
	for _, path := range blocked {
		h.called = false
		req := httptest.NewRequest(http.MethodPost, path, nil)
		w := httptest.NewRecorder()
		gated.ServeHTTP(w, req)

		if h.called {
			t.Errorf("path %q: passed through while locked", path)
		}
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("path %q: status = %d, want 503", path, w.Code)
		}
		if w.Header().Get("X-Pika-Locked") != "true" {
			t.Errorf("path %q: missing X-Pika-Locked: true header", path)
		}
	}
}

// TestBasePathPrefix — deployments behind a sub-path (e.g.
// PIKA_SERVER_BASE_PATH=/admin) must apply the same gate + allow
// rules prefixed by the base path.
func TestBasePathPrefix(t *testing.T) {
	mgr := newLockedInitializedMgr(t)
	h := &markerHandler{}
	gated := lockgate.Middleware(mgr, "/admin")(h)

	// Gated API path under base path with allowlist exemption.
	h.called = false
	req := httptest.NewRequest(http.MethodGet, "/admin/api/v1/key/status", nil)
	w := httptest.NewRecorder()
	gated.ServeHTTP(w, req)
	if !h.called {
		t.Errorf("base-path-prefixed allowlist entry blocked")
	}

	// Gated API path under base path WITHOUT allowlist match.
	h.called = false
	req = httptest.NewRequest(http.MethodPost, "/admin/api/v1/folder", nil)
	w = httptest.NewRecorder()
	gated.ServeHTTP(w, req)
	if h.called {
		t.Errorf("base-path-prefixed gated path passed through")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("base-path-prefixed gated path: status = %d, want 503", w.Code)
	}

	// Same API path without base path → not under our gated prefix
	// → must pass through (no double-gate when base path is set).
	h.called = false
	req = httptest.NewRequest(http.MethodPost, "/api/v1/folder", nil)
	w = httptest.NewRecorder()
	gated.ServeHTTP(w, req)
	if !h.called {
		t.Errorf("non-base-path-prefixed request unexpectedly blocked")
	}
}

// TestNilManagerSkipsGate — the wiring in cmd/pika is supposed to
// always supply a manager, but defensively the middleware should
// skip rather than panic when nil. Simplest "fail-open if
// misconfigured" choice; documented in the package comment.
func TestNilManagerSkipsGate(t *testing.T) {
	h := &markerHandler{}
	gated := lockgate.Middleware(nil, "")(h)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/folder", nil)
	w := httptest.NewRecorder()
	gated.ServeHTTP(w, req)
	if !h.called {
		t.Errorf("nil manager: request unexpectedly blocked")
	}
}
