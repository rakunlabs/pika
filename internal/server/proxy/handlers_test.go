package proxy

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/service"
)

func buildHandlerForTest(t *testing.T, subtype, cfg string, svc ServiceDeps) http.Handler {
	t.Helper()
	specs := DefaultHandlers()
	spec, ok := specs[subtype]
	if !ok {
		t.Fatalf("unknown handler %q", subtype)
	}
	// Handlers are now Middleware-shaped (every node compiles into
	// func(next) http.Handler). For tests we want the concrete
	// http.Handler — pass nil as next because handler builders
	// discard their next arg by contract.
	mw, err := spec.Build(json.RawMessage(cfg), svc, nil)
	if err != nil {
		t.Fatalf("build %s: %v", subtype, err)
	}
	return mw(nil)
}

func TestHealthzHandler(t *testing.T) {
	h := buildHandlerForTest(t, "healthz", `{"body":"ready"}`, nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	if rec.Body.String() != "ready" {
		t.Fatalf("body: got %q", rec.Body.String())
	}
}

func TestStaticResponseHandler(t *testing.T) {
	t.Run("plain", func(t *testing.T) {
		h := buildHandlerForTest(t, "static-response",
			`{"status":201,"content_type":"text/plain","body":"hi"}`, nil)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 201 {
			t.Fatalf("status: %d", rec.Code)
		}
		if rec.Body.String() != "hi" {
			t.Fatal("body mismatch")
		}
	})
	t.Run("base64", func(t *testing.T) {
		payload := base64.StdEncoding.EncodeToString([]byte("binary\x00bytes"))
		h := buildHandlerForTest(t, "static-response",
			`{"body_base64":"`+payload+`"}`, nil)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if !strings.Contains(rec.Body.String(), "binary") {
			t.Fatal("base64 body not delivered")
		}
	})
}

func TestRedirectHandler(t *testing.T) {
	t.Run("simple", func(t *testing.T) {
		h := buildHandlerForTest(t, "redirect", `{"target":"https://example.com","status":301}`, nil)
		req := httptest.NewRequest(http.MethodGet, "/old", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 301 {
			t.Fatalf("status: %d", rec.Code)
		}
		if rec.Header().Get("Location") != "https://example.com" {
			t.Fatalf("location: %q", rec.Header().Get("Location"))
		}
	})
	t.Run("preserve_path", func(t *testing.T) {
		h := buildHandlerForTest(t, "redirect",
			`{"target":"https://new.example.com","preserve_path":true,"strip_prefix":"/old","path":"/old/*"}`, nil)
		req := httptest.NewRequest(http.MethodGet, "/old/some/thing?a=1", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		loc := rec.Header().Get("Location")
		if loc != "https://new.example.com/some/thing?a=1" {
			t.Fatalf("location: %q", loc)
		}
	})
	t.Run("missing target", func(t *testing.T) {
		_, err := DefaultHandlers()["redirect"].Build(json.RawMessage(`{}`), nil, nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestProxyPassHandler(t *testing.T) {
	// Spin an upstream test server.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Got-Path", r.URL.Path)
		w.Header().Set("X-Got-Inject", r.Header.Get("X-Inject"))
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "upstream-body")
	}))
	defer upstream.Close()

	cfg := `{
		"target":"` + upstream.URL + `",
		"strip_prefix":"/api",
		"path":"/api/*",
		"set_request_headers":{"X-Inject":"yes"}
	}`
	h := buildHandlerForTest(t, "proxy-pass", cfg, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/items", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if got := rec.Header().Get("X-Got-Path"); got != "/v1/items" {
		t.Fatalf("upstream path: got %q", got)
	}
	if got := rec.Header().Get("X-Got-Inject"); got != "yes" {
		t.Fatalf("header inject: got %q", got)
	}
	if !strings.Contains(rec.Body.String(), "upstream-body") {
		t.Fatalf("body: %q", rec.Body.String())
	}
}

func TestProxyPassHandler_InvalidTarget(t *testing.T) {
	_, err := DefaultHandlers()["proxy-pass"].Build(json.RawMessage(`{"target":"not a url"}`), nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	_, err = DefaultHandlers()["proxy-pass"].Build(json.RawMessage(`{"target":"/no-scheme"}`), nil, nil)
	if err == nil {
		t.Fatal("expected absolute URL error")
	}
}

func TestDataHandler(t *testing.T) {
	svc := &fakeService{
		dataResult: &service.DataResult{Data: []byte(`{"k":"v"}`), Format: "json"},
	}
	h := buildHandlerForTest(t, "data", `{"path":"/conf/*","strip_prefix":"/conf"}`, svc)
	req := httptest.NewRequest(http.MethodGet, "/conf/folder/file.json", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if svc.lastDataKey != "folder/file.json" {
		t.Fatalf("key: got %q", svc.lastDataKey)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type: %q", ct)
	}
}

func TestDataHandler_ServiceError(t *testing.T) {
	svc := &fakeService{dataErr: service.ErrNotFound}
	h := buildHandlerForTest(t, "data", `{}`, svc)
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: %d", rec.Code)
	}
}

func TestExternalHandler_Read(t *testing.T) {
	svc := &fakeService{
		readExternalRes: &external.Entry{Data: map[string]any{"k": "v"}, ContentType: ""},
	}
	h := buildHandlerForTest(t, "external",
		`{"resource":"vault","path":"/v/*","strip_prefix":"/v"}`, svc)
	req := httptest.NewRequest(http.MethodGet, "/v/secret/foo", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if svc.lastExternal != "vault" || svc.lastExternalPath != "secret/foo" {
		t.Fatalf("resource/path: got %q/%q", svc.lastExternal, svc.lastExternalPath)
	}
	// Body should be JSON {"k":"v"}.
	if !strings.Contains(rec.Body.String(), `"k":"v"`) {
		t.Fatalf("body: %q", rec.Body.String())
	}
}

func TestExternalHandler_RawBytes(t *testing.T) {
	svc := &fakeService{
		readExternalRes: &external.Entry{Raw: []byte("rawbytes"), ContentType: "text/plain"},
	}
	h := buildHandlerForTest(t, "external", `{"resource":"r"}`, svc)
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Body.String() != "rawbytes" {
		t.Fatalf("body: %q", rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "text/plain" {
		t.Fatalf("content-type: %q", rec.Header().Get("Content-Type"))
	}
}

func TestExternalHandler_WriteBlockedByDefault(t *testing.T) {
	svc := &fakeService{}
	h := buildHandlerForTest(t, "external", `{"resource":"r"}`, svc)
	req := httptest.NewRequest(http.MethodPut, "/x", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestExternalHandler_WriteAllowed(t *testing.T) {
	svc := &fakeService{}
	h := buildHandlerForTest(t, "external", `{"resource":"r","allow_write":true}`, svc)
	req := httptest.NewRequest(http.MethodPut, "/x", strings.NewReader(`{"k":"v"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestExternalHandler_MissingResource(t *testing.T) {
	_, err := DefaultHandlers()["external"].Build(json.RawMessage(`{}`), &fakeService{}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestConsulKVHandler(t *testing.T) {
	svc := &fakeService{
		dataResult: &service.DataResult{Data: []byte(`{"x":1}`), Format: "json"},
	}
	// Path matching now lives in the switch node; the consul-kv
	// handler only strips its API prefix from r.URL.Path. The
	// "switch in front" pattern is simulated here by feeding the
	// handler a URL that already starts with /v1/kv.
	h := buildHandlerForTest(t, "consul-kv", `{}`, svc)
	req := httptest.NewRequest(http.MethodGet, "/v1/kv/folder/file", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	var arr []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &arr); err != nil {
		t.Fatalf("body not consul-shaped: %v\n%s", err, rec.Body.String())
	}
	if len(arr) != 1 || arr[0]["Key"] != "folder/file" {
		t.Fatalf("unexpected envelope: %+v", arr)
	}
}

func TestConsulKVHandler_RawFlag(t *testing.T) {
	svc := &fakeService{
		dataResult: &service.DataResult{Data: []byte(`raw-data`), Format: "yaml"},
	}
	h := buildHandlerForTest(t, "consul-kv", `{}`, svc)
	req := httptest.NewRequest(http.MethodGet, "/v1/kv/foo?raw", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Body.String() != "raw-data" {
		t.Fatalf("body: %q", rec.Body.String())
	}
}

func TestDefaultHandlers_BuildSmoke(t *testing.T) {
	for name, spec := range DefaultHandlers() {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s panic: %v", name, r)
				}
			}()
			_, _ = spec.Build(nil, &fakeService{}, nil)
		}()
	}
}
