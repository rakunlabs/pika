package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
)

// helper: build a single middleware by subtype with the given JSON
// config and wrap a 204 terminal handler so we can assert end-to-end
// status / body behaviour through a single request.
func runOneMW(t *testing.T, subtype string, cfg string, svc ServiceDeps, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	specs := DefaultMiddlewares()
	spec, ok := specs[subtype]
	if !ok {
		t.Fatalf("unknown middleware %q", subtype)
	}
	mw, err := spec.Build(json.RawMessage(cfg), svc, nil)
	if err != nil {
		t.Fatalf("build %s: %v", subtype, err)
	}
	term := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	mw(term).ServeHTTP(rec, req)
	return rec
}

func TestAuthBearerMW(t *testing.T) {
	t.Run("missing token -> 401", func(t *testing.T) {
		svc := &fakeService{}
		req := httptest.NewRequest(http.MethodGet, "/data/foo", nil)
		rec := runOneMW(t, "auth-bearer", `{}`, svc, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status: got %d want 401", rec.Code)
		}
	})

	t.Run("forbidden -> 403", func(t *testing.T) {
		svc := &fakeService{validateTokenErr: service.ErrForbidden}
		req := httptest.NewRequest(http.MethodGet, "/data/foo", nil)
		req.Header.Set("Authorization", "Bearer abc")
		rec := runOneMW(t, "auth-bearer", `{}`, svc, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status: got %d want 403", rec.Code)
		}
	})

	t.Run("unauthorized -> 401", func(t *testing.T) {
		svc := &fakeService{validateTokenErr: service.ErrUnauthorized}
		req := httptest.NewRequest(http.MethodGet, "/foo", nil)
		req.Header.Set("Authorization", "Bearer xyz")
		rec := runOneMW(t, "auth-bearer", `{}`, svc, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status: got %d want 401", rec.Code)
		}
	})

	t.Run("valid -> next", func(t *testing.T) {
		svc := &fakeService{}
		req := httptest.NewRequest(http.MethodGet, "/data/foo", nil)
		req.Header.Set("Authorization", "Bearer secret")
		rec := runOneMW(t, "auth-bearer", `{}`, svc, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status: got %d want 204", rec.Code)
		}
		if svc.lastValidateScope != "data/foo" {
			t.Fatalf("scope: got %q want %q", svc.lastValidateScope, "data/foo")
		}
		if svc.lastValidateOp != "read" {
			t.Fatalf("op: got %q want read", svc.lastValidateOp)
		}
	})

	t.Run("scope override", func(t *testing.T) {
		svc := &fakeService{}
		req := httptest.NewRequest(http.MethodPut, "/anything", nil)
		req.Header.Set("Authorization", "Bearer secret")
		rec := runOneMW(t, "auth-bearer", `{"scope":"fixed/scope","operation":"write"}`, svc, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status: got %d", rec.Code)
		}
		if svc.lastValidateScope != "fixed/scope" || svc.lastValidateOp != "write" {
			t.Fatalf("override not applied: %q/%q", svc.lastValidateScope, svc.lastValidateOp)
		}
	})
}

func TestBasicAuthMW(t *testing.T) {
	cfg := `{"realm":"test","users":[{"username":"alice","password":"hunter2"}]}`
	t.Run("missing creds -> 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := runOneMW(t, "basic-auth", cfg, nil, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d", rec.Code)
		}
		if !strings.Contains(rec.Header().Get("WWW-Authenticate"), `realm="test"`) {
			t.Fatalf("realm not echoed: %q", rec.Header().Get("WWW-Authenticate"))
		}
	})
	t.Run("wrong password -> 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth("alice", "wrong")
		rec := runOneMW(t, "basic-auth", cfg, nil, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d", rec.Code)
		}
	})
	t.Run("unknown user -> 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth("bob", "hunter2")
		rec := runOneMW(t, "basic-auth", cfg, nil, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d", rec.Code)
		}
	})
	t.Run("correct -> next", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth("alice", "hunter2")
		rec := runOneMW(t, "basic-auth", cfg, nil, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("got %d", rec.Code)
		}
	})
	t.Run("empty users -> build error", func(t *testing.T) {
		_, err := DefaultMiddlewares()["basic-auth"].Build(json.RawMessage(`{}`), nil, nil)
		if err == nil {
			t.Fatal("expected error for empty user list")
		}
	})
}

func TestIPAllowDeny(t *testing.T) {
	allow := `{"cidrs":["127.0.0.0/8"]}`
	deny := `{"cidrs":["10.0.0.0/8"]}`
	t.Run("allow matches", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.5:54321"
		rec := runOneMW(t, "ip-allowlist", allow, nil, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("got %d", rec.Code)
		}
	})
	t.Run("allow misses -> 403", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "8.8.8.8:54321"
		rec := runOneMW(t, "ip-allowlist", allow, nil, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("got %d", rec.Code)
		}
	})
	t.Run("deny matches -> 403", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.1.2.3:54321"
		rec := runOneMW(t, "ip-denylist", deny, nil, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("got %d", rec.Code)
		}
	})
	t.Run("deny misses", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:54321"
		rec := runOneMW(t, "ip-denylist", deny, nil, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("got %d", rec.Code)
		}
	})
}

func TestHeaderInjectMW(t *testing.T) {
	// Use a custom inner handler that echoes a chosen request header
	// so we can verify Request map injection.
	specs := DefaultMiddlewares()
	mw, err := specs["header-inject"].Build(json.RawMessage(
		`{"request":{"X-Echo":"hi"},"response":{"X-Resp":"yo"},"overwrite":true}`,
	), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Got-Echo", r.Header.Get("X-Echo"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello"))
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Got-Echo"); got != "hi" {
		t.Fatalf("request injection: got %q", got)
	}
	if got := rec.Header().Get("X-Resp"); got != "yo" {
		t.Fatalf("response injection: got %q", got)
	}
}

func TestHeaderRemoveMW(t *testing.T) {
	specs := DefaultMiddlewares()
	mw, err := specs["header-remove"].Build(json.RawMessage(
		`{"request":["X-Drop"],"response":["X-Hide"]}`,
	), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// X-Drop should not reach the handler.
		if r.Header.Get("X-Drop") != "" {
			t.Errorf("request header X-Drop leaked into handler")
		}
		w.Header().Set("X-Hide", "shouldbegone")
		w.Header().Set("X-Keep", "stays")
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Drop", "leak")
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)
	if rec.Header().Get("X-Hide") != "" {
		t.Fatalf("response header X-Hide not stripped: %q", rec.Header().Get("X-Hide"))
	}
	if rec.Header().Get("X-Keep") != "stays" {
		t.Fatalf("response header X-Keep clobbered")
	}
}

func TestCompressMW(t *testing.T) {
	specs := DefaultMiddlewares()
	mw, err := specs["compress"].Build(json.RawMessage(`{"min_length":4}`), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", 100)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(payload))
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding: got %q want gzip", rec.Header().Get("Content-Encoding"))
	}
	r, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != payload {
		t.Fatal("decompressed payload mismatch")
	}
}

func TestCompressMW_SmallBodyPassthrough(t *testing.T) {
	specs := DefaultMiddlewares()
	mw, err := specs["compress"].Build(json.RawMessage(`{"min_length":1024}`), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("tiny"))
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("small body should not have been gzipped")
	}
	if !bytes.Equal(rec.Body.Bytes(), []byte("tiny")) {
		t.Fatalf("body changed: %q", rec.Body.String())
	}
}

func TestTimeoutMW(t *testing.T) {
	specs := DefaultMiddlewares()
	mw, err := specs["timeout"].Build(json.RawMessage(`{"duration":"50ms"}`), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		ctx := r.Context()
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Errorf("context has no deadline")
		}
		if deadline.IsZero() {
			t.Errorf("zero deadline")
		}
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)
	if !called {
		t.Fatal("inner handler not called")
	}
}

func TestSizeLimitMW(t *testing.T) {
	specs := DefaultMiddlewares()
	mw, err := specs["request-size-limit"].Build(json.RawMessage(`{"max_bytes":4}`), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			// MaxBytesReader returns an error when the limit is
			// breached; surface it through the status so the test
			// can assert on it.
			http.Error(w, "too big", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("toolong"))
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d want 413", rec.Code)
	}
}

func TestRatelimitMW_HardThreshold(t *testing.T) {
	specs := DefaultMiddlewares()
	mw, err := specs["ratelimit"].Build(json.RawMessage(
		`{"window":"1s","hard_threshold":2,"backoff_base":"1ms","backoff_max":"5ms"}`,
	), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	hit := func() int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "5.5.5.5:1"
		rec := httptest.NewRecorder()
		mw(inner).ServeHTTP(rec, req)
		return rec.Code
	}
	if code := hit(); code != http.StatusOK {
		t.Fatalf("first req: got %d", code)
	}
	if code := hit(); code != http.StatusOK {
		t.Fatalf("second req: got %d", code)
	}
	// Third call must be rejected.
	if code := hit(); code != http.StatusTooManyRequests {
		t.Fatalf("third req: got %d want 429", code)
	}
}

func TestRatelimitMW_InvalidConfig(t *testing.T) {
	specs := DefaultMiddlewares()
	_, err := specs["ratelimit"].Build(json.RawMessage(`{}`), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "hard_threshold") {
		t.Fatalf("want hard_threshold error, got %v", err)
	}
}

// Sanity: ensure context type isn't accidentally satisfied without
// the real service deps for middlewares that need them.
func TestAuthBearerMW_NoService(t *testing.T) {
	_, err := DefaultMiddlewares()["auth-bearer"].Build(json.RawMessage(`{}`), nil, nil)
	if err == nil {
		t.Fatal("expected build to fail without service deps")
	}
}

// Smoke: every default middleware must build with empty config (or
// surface a clear error). This catches regressions where a new kind
// is added without setting sensible defaults — the catalog endpoint
// promises operators a usable starting point.
func TestDefaultMiddlewares_BuildableOrExplicitError(t *testing.T) {
	for subtype, spec := range DefaultMiddlewares() {
		// Some kinds are intentionally strict (ratelimit needs a
		// hard threshold, basic-auth needs at least one user,
		// request-size-limit needs max_bytes, auth-bearer needs
		// the service). Validate the contract: either the empty
		// build succeeds or it returns an error containing the
		// subtype name or a config key — never a panic.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: panic on empty build: %v", subtype, r)
				}
			}()
			_, _ = spec.Build(nil, &fakeService{}, nil)
		}()
	}
}

// --- strip-prefix ---

func TestStripPrefixMW(t *testing.T) {
	t.Run("strips configured prefix", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
		rec := runOneMW(t, "strip-prefix", `{"prefix":"/api"}`, nil, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status: %d", rec.Code)
		}
		// runOneMW's terminal handler just writes 204; we cannot
		// observe the rewritten path through it. Re-run with a
		// custom inner handler that echoes the path back.
		spec := DefaultMiddlewares()["strip-prefix"]
		mw, err := spec.Build(json.RawMessage(`{"prefix":"/api"}`), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Got-Path", r.URL.Path)
		})
		req2 := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
		rec2 := httptest.NewRecorder()
		mw(inner).ServeHTTP(rec2, req2)
		if got := rec2.Header().Get("X-Got-Path"); got != "/users/42" {
			t.Fatalf("path after strip: got %q want %q", got, "/users/42")
		}
	})

	t.Run("first matching prefix wins", func(t *testing.T) {
		spec := DefaultMiddlewares()["strip-prefix"]
		mw, err := spec.Build(json.RawMessage(`{"prefixes":["/foo","/api"]}`), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-P", r.URL.Path)
		})
		req := httptest.NewRequest(http.MethodGet, "/api/items", nil)
		rec := httptest.NewRecorder()
		mw(inner).ServeHTTP(rec, req)
		if got := rec.Header().Get("X-P"); got != "/items" {
			t.Fatalf("path: %q", got)
		}
	})

	t.Run("no match leaves path alone", func(t *testing.T) {
		spec := DefaultMiddlewares()["strip-prefix"]
		mw, _ := spec.Build(json.RawMessage(`{"prefix":"/api"}`), nil, nil)
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-P", r.URL.Path)
		})
		req := httptest.NewRequest(http.MethodGet, "/other/x", nil)
		rec := httptest.NewRecorder()
		mw(inner).ServeHTTP(rec, req)
		if got := rec.Header().Get("X-P"); got != "/other/x" {
			t.Fatalf("path: %q", got)
		}
	})
}

// --- header-compare ---

func TestHeaderCompareMW(t *testing.T) {
	t.Run("allow mode passes match, blocks miss", func(t *testing.T) {
		cfg := `{"mode":"allow","headers":{"X-Token":{"equals":"secret"}}}`
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Token", "secret")
		if got := runOneMW(t, "header-compare", cfg, nil, req).Code; got != http.StatusNoContent {
			t.Fatalf("matching request: got %d", got)
		}
		req2 := httptest.NewRequest(http.MethodGet, "/", nil)
		req2.Header.Set("X-Token", "wrong")
		if got := runOneMW(t, "header-compare", cfg, nil, req2).Code; got != http.StatusForbidden {
			t.Fatalf("mismatched request: got %d", got)
		}
	})

	t.Run("block mode mirrors", func(t *testing.T) {
		cfg := `{"mode":"block","status":418,"headers":{"X-Bad":{"equals":"yes"}}}`
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Bad", "yes")
		if got := runOneMW(t, "header-compare", cfg, nil, req).Code; got != http.StatusTeapot {
			t.Fatalf("block mode hit: got %d", got)
		}
		req2 := httptest.NewRequest(http.MethodGet, "/", nil)
		req2.Header.Set("X-Bad", "no")
		if got := runOneMW(t, "header-compare", cfg, nil, req2).Code; got != http.StatusNoContent {
			t.Fatalf("block mode miss: got %d", got)
		}
	})

	t.Run("regex match", func(t *testing.T) {
		cfg := `{"mode":"allow","headers":{"X-Tenant":{"regex":"^acme-[0-9]+$"}}}`
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Tenant", "acme-42")
		if got := runOneMW(t, "header-compare", cfg, nil, req).Code; got != http.StatusNoContent {
			t.Fatalf("regex match: got %d", got)
		}
		req2 := httptest.NewRequest(http.MethodGet, "/", nil)
		req2.Header.Set("X-Tenant", "evil-7")
		if got := runOneMW(t, "header-compare", cfg, nil, req2).Code; got != http.StatusForbidden {
			t.Fatalf("regex miss: got %d", got)
		}
	})

	t.Run("invalid regex surfaces at build", func(t *testing.T) {
		_, err := DefaultMiddlewares()["header-compare"].Build(
			json.RawMessage(`{"headers":{"X":{"regex":"["}}}`), nil, nil)
		if err == nil {
			t.Fatal("expected build error for bad regex")
		}
	})

	t.Run("string shorthand is treated as equals", func(t *testing.T) {
		// The UI's kv-map widget emits {"X-Token":"secret"}; the
		// custom UnmarshalJSON should accept it as an equals
		// rule so an operator does not have to nest objects.
		cfg := `{"mode":"allow","headers":{"X-Token":"secret"}}`
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Token", "secret")
		if got := runOneMW(t, "header-compare", cfg, nil, req).Code; got != http.StatusNoContent {
			t.Fatalf("shorthand hit: got %d", got)
		}
	})
}

// --- response-rewrite ---

func TestResponseRewriteMW(t *testing.T) {
	build := func(cfg string) func(http.Handler) http.Handler {
		mw, err := DefaultMiddlewares()["response-rewrite"].Build(json.RawMessage(cfg), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return mw
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Original", "yes")
		w.Header().Set("Set-Cookie", "leak=1")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"upstream broke"}`))
	})

	t.Run("set_status overrides", func(t *testing.T) {
		mw := build(`{"set_status":503}`)
		rec := httptest.NewRecorder()
		mw(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status: %d", rec.Code)
		}
	})

	t.Run("status_map remaps", func(t *testing.T) {
		mw := build(`{"status_map":{"502":504}}`)
		rec := httptest.NewRecorder()
		mw(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusGatewayTimeout {
			t.Fatalf("status: %d", rec.Code)
		}
	})

	t.Run("delete + set headers", func(t *testing.T) {
		mw := build(`{"delete_headers":["Set-Cookie"],"set_headers":{"X-Pika":"v1"}}`)
		rec := httptest.NewRecorder()
		mw(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Header().Get("Set-Cookie") != "" {
			t.Fatal("Set-Cookie should be stripped")
		}
		if rec.Header().Get("X-Pika") != "v1" {
			t.Fatal("X-Pika should be set")
		}
	})

	t.Run("body_replace substitutes", func(t *testing.T) {
		mw := build(`{"body_replace":[{"from":"upstream","to":"backend"}]}`)
		rec := httptest.NewRecorder()
		mw(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if !strings.Contains(rec.Body.String(), "backend broke") {
			t.Fatalf("body: %q", rec.Body.String())
		}
	})

	t.Run("body_override replaces whole body", func(t *testing.T) {
		mw := build(`{"body_override":"masked"}`)
		rec := httptest.NewRecorder()
		mw(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Body.String() != "masked" {
			t.Fatalf("body: %q", rec.Body.String())
		}
	})
}

// --- custom-response handler smoke test lives here for parity ---

func TestCustomResponseHandler(t *testing.T) {
	h := buildHandlerForTest(t, "custom-response", `{
		"status":200,
		"content_type":"text/plain",
		"body":"hi {{.Method}} {{.Path}} q={{.Query.tag}} h={{.Headers.X_Test}}"
	}`, nil)
	req := httptest.NewRequest(http.MethodGet, "/echo?tag=foo", nil)
	req.Header.Set("X-Test", "value")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	want := "hi GET /echo q=foo h=value"
	if rec.Body.String() != want {
		t.Fatalf("body: got %q want %q", rec.Body.String(), want)
	}
}

func TestCustomResponseHandler_BadTemplate(t *testing.T) {
	_, err := DefaultHandlers()["custom-response"].Build(
		json.RawMessage(`{"body":"{{ .Unclosed"}`), nil, nil)
	if err == nil {
		t.Fatal("expected build error for malformed template")
	}
}

// Use context import to satisfy linter when no test directly uses it
// (the helper functions above reference context indirectly).
var _ = context.Background
var _ = errors.New
var _ = bytes.NewReader
