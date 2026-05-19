package common

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/pika/internal/service"
)

// stubValidator implements TokenValidator with canned responses.
type stubValidator struct {
	expectScope string
	expectOp    string
	err         error
	calls       int
}

func (s *stubValidator) ValidateToken(_ context.Context, raw, scope, op string) error {
	s.calls++
	if raw == "" {
		return service.ErrUnauthorized
	}
	if s.expectScope != "" && scope != s.expectScope {
		return errors.New("wrong scope")
	}
	if s.expectOp != "" && op != s.expectOp {
		return errors.New("wrong op")
	}
	return s.err
}

func TestExtractToken_Bearer(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	r.Header.Set("Authorization", "Bearer abc123")
	if got := ExtractToken(r); got != "abc123" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractToken_Basic(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	r.SetBasicAuth("npm", "pika_token_xyz")
	if got := ExtractToken(r); got != "pika_token_xyz" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractToken_XPikaToken(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	r.Header.Set("X-Pika-Token", "header-token")
	if got := ExtractToken(r); got != "header-token" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractToken_XPikaTokenPriorityOverAuth(t *testing.T) {
	// X-Pika-Token wins when both are set.
	r := httptest.NewRequest("GET", "/x", nil)
	r.Header.Set("X-Pika-Token", "winner")
	r.Header.Set("Authorization", "Bearer loser")
	if got := ExtractToken(r); got != "winner" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractToken_None(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	if got := ExtractToken(r); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestRequireToken_OK(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	r.Header.Set("Authorization", "Bearer good")
	v := &stubValidator{expectScope: "registry/acme", expectOp: OpRead}
	if err := RequireToken(context.Background(), v, r, "registry/acme", OpRead); err != nil {
		t.Fatalf("RequireToken: %v", err)
	}
	if v.calls != 1 {
		t.Fatalf("calls %d", v.calls)
	}
}

func TestRequireToken_Missing(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	v := &stubValidator{}
	err := RequireToken(context.Background(), v, r, "scope", OpRead)
	if !errors.Is(err, service.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	if v.calls != 0 {
		t.Fatalf("validator should not have been called, got %d calls", v.calls)
	}
}

func TestMapAuthError(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"unauthorized", service.ErrUnauthorized, http.StatusUnauthorized},
		{"forbidden", service.ErrForbidden, http.StatusForbidden},
		{"unknown", errors.New("boom"), http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handled := MapAuthError(w, tc.err)
			if !handled {
				t.Fatal("MapAuthError should have written a response")
			}
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d", w.Code, tc.status)
			}
		})
	}
}

func TestMapAuthError_Nil(t *testing.T) {
	w := httptest.NewRecorder()
	if MapAuthError(w, nil) {
		t.Fatal("nil error should produce no response")
	}
	if w.Code != http.StatusOK {
		// httptest defaults to 200 until WriteHeader.
		t.Fatalf("status %d", w.Code)
	}
}

func TestEtagFor(t *testing.T) {
	e1 := EtagFor("payload-A")
	e2 := EtagFor("payload-B")
	e3 := EtagFor("payload-A")
	if e1 == "" || e2 == "" {
		t.Fatal("empty etag")
	}
	if e1 == e2 {
		t.Fatal("distinct inputs produced same etag")
	}
	if e1 != e3 {
		t.Fatal("same input produced different etags")
	}
	if e1[0] != '"' || e1[len(e1)-1] != '"' {
		t.Fatalf("etag not quoted: %q", e1)
	}
	if EtagFor("") != "" {
		t.Fatal("empty input should yield empty etag")
	}
}

func TestMatchIfNoneMatch(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	if MatchIfNoneMatch(r, `"abc"`) {
		t.Fatal("no header should not match")
	}

	r.Header.Set("If-None-Match", `"abc"`)
	if !MatchIfNoneMatch(r, `"abc"`) {
		t.Fatal("exact match expected")
	}
	if MatchIfNoneMatch(r, `"xyz"`) {
		t.Fatal("different etag should not match")
	}

	r.Header.Set("If-None-Match", `"abc", "def"`)
	if !MatchIfNoneMatch(r, `"def"`) {
		t.Fatal("comma-list match expected")
	}

	r.Header.Set("If-None-Match", "*")
	if !MatchIfNoneMatch(r, `"anything"`) {
		t.Fatal("* wildcard should match anything")
	}

	r.Header.Set("If-None-Match", `W/"weak"`)
	if !MatchIfNoneMatch(r, `"weak"`) {
		t.Fatal("weak comparator should match")
	}
}

func TestSetMutableCache(t *testing.T) {
	w := httptest.NewRecorder()
	SetMutableCache(w, `"etag"`, 5*time.Minute)
	if got := w.Header().Get("ETag"); got != `"etag"` {
		t.Fatalf("ETag %q", got)
	}
	cc := w.Header().Get("Cache-Control")
	if cc != "public, max-age=300, must-revalidate" {
		t.Fatalf("Cache-Control %q", cc)
	}
}

func TestSetMutableCache_FloorMinAge(t *testing.T) {
	w := httptest.NewRecorder()
	SetMutableCache(w, "", 0)
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=60, must-revalidate" {
		t.Fatalf("expected 60s floor, got %q", got)
	}
}

func TestSetImmutableCache(t *testing.T) {
	w := httptest.NewRecorder()
	SetImmutableCache(w, `"etag"`)
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control %q", got)
	}
}

func TestApplyCORS_NoOrigin(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/x", nil)
	if ApplyCORS(w, r, []string{"*"}) {
		t.Fatal("no Origin: should not handle preflight")
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("ACAO leaked without Origin")
	}
}

func TestApplyCORS_AllowedOrigin(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/x", nil)
	r.Header.Set("Origin", "https://my.spa")
	if ApplyCORS(w, r, []string{"https://my.spa"}) {
		t.Fatal("GET should not be preflight-handled")
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "https://my.spa" {
		t.Fatalf("ACAO %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestApplyCORS_DeniedOrigin(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/x", nil)
	r.Header.Set("Origin", "https://evil.com")
	if ApplyCORS(w, r, []string{"https://good.com"}) {
		t.Fatal("denied origin: should not handle preflight")
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("ACAO should be empty for denied origin")
	}
}

func TestApplyCORS_PreflightShortCircuit(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("OPTIONS", "/x", nil)
	r.Header.Set("Origin", "https://app")
	if !ApplyCORS(w, r, []string{"*"}) {
		t.Fatal("OPTIONS preflight should be handled")
	}
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d", w.Code)
	}
}

func TestSingleflight_Coalesces(t *testing.T) {
	sf := NewSingleflight()
	var ran atomic.Int32
	start := make(chan struct{})

	const N = 16
	var wg sync.WaitGroup
	results := make([]string, N)
	shareds := make([]bool, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			val, _, shared := sf.Do("key1", func() (any, error) {
				ran.Add(1)
				time.Sleep(20 * time.Millisecond)
				return "result", nil
			})
			results[i] = val.(string)
			shareds[i] = shared
		}(i)
	}
	close(start)
	wg.Wait()

	if ran.Load() != 1 {
		t.Fatalf("expected fn to run exactly once, ran %d times", ran.Load())
	}
	sharedCount := 0
	for i := 0; i < N; i++ {
		if results[i] != "result" {
			t.Errorf("result[%d] = %q", i, results[i])
		}
		if shareds[i] {
			sharedCount++
		}
	}
	// Exactly one caller (the leader) sees shared=false; the rest see true.
	if sharedCount != N-1 {
		t.Fatalf("sharedCount = %d, want %d", sharedCount, N-1)
	}
}

func TestSingleflight_DistinctKeysRunInParallel(t *testing.T) {
	sf := NewSingleflight()
	var ran atomic.Int32
	const N = 8
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			_, _, _ = sf.Do("key-"+string(rune('a'+i)), func() (any, error) {
				ran.Add(1)
				return i, nil
			})
		}(i)
	}
	wg.Wait()
	if ran.Load() != N {
		t.Fatalf("expected %d runs (distinct keys), got %d", N, ran.Load())
	}
}

func TestSingleflight_ForgetAfterDone(t *testing.T) {
	sf := NewSingleflight()
	var ran atomic.Int32
	_, _, _ = sf.Do("key", func() (any, error) {
		ran.Add(1)
		return "a", nil
	})
	// Second call after completion should re-run, not return the cached value.
	_, _, shared := sf.Do("key", func() (any, error) {
		ran.Add(1)
		return "b", nil
	})
	if shared {
		t.Fatal("second call should not be marked shared")
	}
	if ran.Load() != 2 {
		t.Fatalf("expected 2 runs, got %d", ran.Load())
	}
}
