package authx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/tummy"

	"github.com/rakunlabs/pika/internal/service"
)

// withTummy enables tummy + pause for deterministic time, restoring on
// teardown. Required for assertions about Retry-After / window rollover.
func withTummy(t *testing.T) {
	t.Helper()
	tummy.Enable()
	tummy.SetTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	tummy.Pause()
	t.Cleanup(func() {
		tummy.Resume()
		tummy.Disable()
	})
}

// guardWithDefaults builds a LoginGuard with small thresholds suitable for
// unit tests. BackoffBase=0 disables real sleeps so tests don't need to
// wait on the limiter.
func guardWithDefaults() func(http.Handler) http.Handler {
	cfg := &service.AuthRateLimitSettings{
		Enabled:           true,
		Window:            time.Minute,
		IPSoftThreshold:   3,
		IPHardThreshold:   5,
		UserSoftThreshold: 3,
		UserHardThreshold: 4,
		BackoffBase:       0, // skip sleeps in tests
		BackoffMax:        time.Second,
	}
	return LoginGuard(cfg, nil)
}

// fakeLogin is the handler under test: returns 401 by default, 200 if the
// request body says ok=1.
func fakeLogin() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Demonstrate that the body is still readable downstream after
		// the per-username KeyFunc consumed it.
		buf := make([]byte, 256)
		n, _ := r.Body.Read(buf)
		if strings.Contains(string(buf[:n]), `"ok":true`) {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
}

func loginReq(remoteAddr, username string) *http.Request {
	body := strings.NewReader(`{"username":"` + username + `","password":"x"}`)
	r := httptest.NewRequest(http.MethodPost, "/login/pass/local", body)
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = remoteAddr
	return r
}

func TestLoginGuardPassthroughForNonLoginPaths(t *testing.T) {
	withTummy(t)
	guard := guardWithDefaults()
	called := int32(0)
	h := guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&called, 1)
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodGet, "/login/info", nil)
		req.RemoteAddr = "1.1.1.1:1234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("non-login path expected 200, got %d", rec.Code)
		}
	}
	if got := atomic.LoadInt32(&called); got != 100 {
		t.Errorf("handler called %d times, want 100", got)
	}
}

func TestLoginGuardCountsFailedAttempts(t *testing.T) {
	withTummy(t)
	guard := guardWithDefaults()
	h := guard(fakeLogin())

	// User hard threshold is 4. The first 4 failed attempts should all
	// reach the handler (return 401); the 5th must be rejected with 429
	// before the handler runs.
	for i := 0; i < 4; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, loginReq("1.2.3.4:5000", "alice"))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i+1, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginReq("1.2.3.4:5000", "alice"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("5th attempt should be rejected with 429, got %d", rec.Code)
	}
}

func TestLoginGuardUserAxisIndependentOfIP(t *testing.T) {
	withTummy(t)
	guard := guardWithDefaults()
	h := guard(fakeLogin())

	// Hit alice from many IPs to trip the user limit while no single IP
	// reaches its hard threshold.
	for i := 0; i < 4; i++ {
		rec := httptest.NewRecorder()
		ip := "10.0.0." + string(rune('1'+i)) + ":5000"
		h.ServeHTTP(rec, loginReq(ip, "alice"))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("alice attempt %d from %s: expected 401, got %d", i+1, ip, rec.Code)
		}
	}
	// 5th from a 5th IP: user limit (4 hard) is hit, must reject.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginReq("10.0.0.99:5000", "alice"))
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("user-axis hard threshold should reject regardless of IP, got %d", rec.Code)
	}

	// A different username from a clean IP should still work.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, loginReq("172.16.0.1:5000", "bob"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("bob from clean IP should get 401 (handler reached), got %d", rec.Code)
	}
}

func TestLoginGuardIPAxisIndependentOfUser(t *testing.T) {
	withTummy(t)
	guard := guardWithDefaults()
	h := guard(fakeLogin())

	// Hit different usernames from one IP to trip IP limit (5 hard)
	// without any single user reaching theirs (4 hard each).
	users := []string{"u1", "u2", "u3", "u1", "u4"}
	for i, u := range users {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, loginReq("9.9.9.9:5000", u))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d (%s): expected 401, got %d", i+1, u, rec.Code)
		}
	}
	// 6th from same IP: IP hard (5) tripped.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginReq("9.9.9.9:5000", "u5"))
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("IP-axis hard threshold should reject, got %d", rec.Code)
	}
}

func TestLoginGuardRetryAfterHeaderSet(t *testing.T) {
	withTummy(t)
	guard := guardWithDefaults()
	h := guard(fakeLogin())

	for i := 0; i < 4; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, loginReq("1.2.3.4:5000", "alice"))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: setup expected 401, got %d", i+1, rec.Code)
		}
	}
	// The 5th hits user hard threshold; expect 429 with Retry-After.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginReq("1.2.3.4:5000", "alice"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Error("Retry-After missing on 429")
	}
}

func TestLoginGuardWindowRollover(t *testing.T) {
	withTummy(t)
	guard := guardWithDefaults()
	h := guard(fakeLogin())

	for i := 0; i < 4; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, loginReq("1.2.3.4:5000", "alice"))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("setup attempt %d: expected 401, got %d", i+1, rec.Code)
		}
	}

	// Advance past Window — entries fall off.
	tummy.AddDuration(2 * time.Minute)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loginReq("1.2.3.4:5000", "alice"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("after window rollover expected 401, got %d", rec.Code)
	}
}

func TestLoginGuardDisabledIsPassthrough(t *testing.T) {
	withTummy(t)
	cfg := &service.AuthRateLimitSettings{Enabled: false}
	guard := LoginGuard(cfg, nil)
	h := guard(fakeLogin())

	for i := 0; i < 100; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, loginReq("1.2.3.4:5000", "alice"))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401 (limiter disabled), got %d", i+1, rec.Code)
		}
	}
}

func TestLoginGuardNilConfigIsPassthrough(t *testing.T) {
	guard := LoginGuard(nil, nil)
	h := guard(fakeLogin())

	for i := 0; i < 100; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, loginReq("1.2.3.4:5000", "alice"))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401 (nil cfg), got %d", i+1, rec.Code)
		}
	}
}

func TestLoginGuardBodyReadableByHandler(t *testing.T) {
	withTummy(t)
	guard := guardWithDefaults()
	bodySeen := ""
	h := guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		bodySeen = string(buf[:n])
		w.WriteHeader(http.StatusUnauthorized)
	}))

	body := strings.NewReader(`{"username":"alice","password":"sekrit"}`)
	req := httptest.NewRequest(http.MethodPost, "/login/pass/local", body)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "1.2.3.4:5000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !strings.Contains(bodySeen, `"alice"`) || !strings.Contains(bodySeen, `"sekrit"`) {
		t.Errorf("handler did not see full body: %q", bodySeen)
	}
}

func TestLoginGuardSuccessNotCounted(t *testing.T) {
	withTummy(t)
	cfg := &service.AuthRateLimitSettings{
		Enabled:           true,
		Window:            time.Minute,
		IPSoftThreshold:   2,
		IPHardThreshold:   3,
		UserSoftThreshold: 2,
		UserHardThreshold: 3,
		BackoffBase:       0,
		BackoffMax:        time.Second,
	}
	guard := LoginGuard(cfg, nil)
	h := guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always succeed — should never count against limits.
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 50; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, loginReq("1.2.3.4:5000", "alice"))
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d: success should never be rate-limited, got %d", i+1, rec.Code)
		}
	}
}

func TestUserKeyFromBodyHandlesFormEncoded(t *testing.T) {
	body := strings.NewReader("username=Bob&password=x")
	req := httptest.NewRequest(http.MethodPost, "/login/pass/local", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	keys := userKeyFromBody(req)
	if len(keys) != 1 || keys[0] != "user:bob" {
		t.Errorf("got %v, want [user:bob]", keys)
	}

	// Body must still be readable.
	buf := make([]byte, 256)
	n, _ := req.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "username=Bob") {
		t.Errorf("body not restored: %q", buf[:n])
	}
}

func TestUserKeyFromBodySkipsOnUnknownContentType(t *testing.T) {
	body := strings.NewReader("not parseable as anything")
	req := httptest.NewRequest(http.MethodPost, "/login/pass/local", body)
	req.Header.Set("Content-Type", "text/plain")

	keys := userKeyFromBody(req)
	if keys != nil {
		t.Errorf("expected nil keys for unknown content type, got %v", keys)
	}
}

func TestUserKeyFromBodySkipsOnMissingUsername(t *testing.T) {
	body := strings.NewReader(`{"password":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/login/pass/local", body)
	req.Header.Set("Content-Type", "application/json")

	keys := userKeyFromBody(req)
	if keys != nil {
		t.Errorf("expected nil keys when username missing, got %v", keys)
	}
}
