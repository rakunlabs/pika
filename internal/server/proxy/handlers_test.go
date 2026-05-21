package proxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/registry"
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

type memoryRawFS struct {
	files map[string][]byte
	dirs  map[string][]rawfs.DirEntry
}

func (m *memoryRawFS) Stat(p string) (*rawfs.FileInfo, error) {
	p = strings.TrimPrefix(p, "/")
	if data, ok := m.files[p]; ok {
		return &rawfs.FileInfo{Name: path.Base(p), Size: int64(len(data))}, nil
	}
	if _, ok := m.dirs[p]; ok {
		name := path.Base(p)
		if name == "." {
			name = ""
		}
		return &rawfs.FileInfo{Name: name, IsDir: true}, nil
	}
	return nil, os.ErrNotExist
}

func (m *memoryRawFS) ReadDir(p string) ([]rawfs.DirEntry, error) {
	p = strings.TrimPrefix(p, "/")
	entries, ok := m.dirs[p]
	if !ok {
		return nil, os.ErrNotExist
	}
	return entries, nil
}

func (m *memoryRawFS) Open(p string) (rawfs.ReadSeekCloser, *rawfs.FileInfo, error) {
	p = strings.TrimPrefix(p, "/")
	data, ok := m.files[p]
	if !ok {
		return nil, nil, os.ErrNotExist
	}
	return nopReadSeekCloser{Reader: bytes.NewReader(data)}, &rawfs.FileInfo{Name: path.Base(p), Size: int64(len(data))}, nil
}

func (m *memoryRawFS) Write(p string, r io.Reader, _ int64) error {
	p = strings.TrimPrefix(p, "/")
	if m.files == nil {
		m.files = map[string][]byte{}
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.files[p] = data
	return nil
}

func (m *memoryRawFS) Delete(p string) error {
	p = strings.TrimPrefix(p, "/")
	if _, ok := m.files[p]; !ok {
		return os.ErrNotExist
	}
	delete(m.files, p)
	return nil
}

func (m *memoryRawFS) MkDir(p string) error {
	p = strings.TrimPrefix(p, "/")
	if m.dirs == nil {
		m.dirs = map[string][]rawfs.DirEntry{}
	}
	m.dirs[p] = nil
	return nil
}

type nopReadSeekCloser struct{ *bytes.Reader }

func (nopReadSeekCloser) Close() error { return nil }

type fakeRegistry struct {
	path   string
	prefix string
	hits   int
}

func (f *fakeRegistry) Namespace() string { return "ns" }
func (f *fakeRegistry) Name() string      { return "repo" }
func (f *fakeRegistry) Type() string      { return "npm" }
func (f *fakeRegistry) Kind() string      { return "local" }
func (f *fakeRegistry) Close() error      { return nil }
func (f *fakeRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.hits++
	f.path = r.URL.Path
	f.prefix = r.Header.Get("X-Pika-Registry-Prefix")
	w.WriteHeader(http.StatusNoContent)
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

func TestRawResourceHandler_ReadFile(t *testing.T) {
	fsys := &memoryRawFS{files: map[string][]byte{"file.txt": []byte("hello")}}
	svc := &fakeService{rawMounts: map[string]rawfs.RawFS{"assets": fsys}}
	h := buildHandlerForTest(t, "raw", `{"mount":"assets","strip_prefix":"/files"}`, svc)
	req := httptest.NewRequest(http.MethodGet, "/files/file.txt", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%q", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "hello" {
		t.Fatalf("body: %q", rec.Body.String())
	}
}

func TestRawResourceHandler_DirectoryListing(t *testing.T) {
	fsys := &memoryRawFS{dirs: map[string][]rawfs.DirEntry{
		"dir": []rawfs.DirEntry{{Name: "child.txt", Size: 3}},
	}}
	svc := &fakeService{rawMounts: map[string]rawfs.RawFS{"assets": fsys}}
	h := buildHandlerForTest(t, "raw", `{"mount":"assets","directory_listing":true}`, svc)
	req := httptest.NewRequest(http.MethodGet, "/dir", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "child.txt") {
		t.Fatalf("listing: %q", rec.Body.String())
	}

	h = buildHandlerForTest(t, "raw", `{"mount":"assets"}`, svc)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disabled listing status: %d", rec.Code)
	}
}

func TestRawResourceHandler_WriteAllowed(t *testing.T) {
	fsys := &memoryRawFS{}
	svc := &fakeService{rawMounts: map[string]rawfs.RawFS{"assets": fsys}}
	h := buildHandlerForTest(t, "raw", `{"mount":"assets","allow_write":true,"strip_prefix":"/files"}`, svc)

	req := httptest.NewRequest(http.MethodPut, "/files/new.txt", strings.NewReader("new body"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("put status: %d", rec.Code)
	}
	if string(fsys.files["new.txt"]) != "new body" {
		t.Fatalf("written body: %q", string(fsys.files["new.txt"]))
	}

	req = httptest.NewRequest(http.MethodPost, "/files/new-dir", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("mkdir status: %d", rec.Code)
	}
	if _, ok := fsys.dirs["new-dir"]; !ok {
		t.Fatal("directory was not created")
	}

	req = httptest.NewRequest(http.MethodDelete, "/files/new.txt", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status: %d", rec.Code)
	}
	if _, ok := fsys.files["new.txt"]; ok {
		t.Fatal("file was not deleted")
	}
}

func TestRawResourceHandler_WriteBlockedByDefault(t *testing.T) {
	fsys := &memoryRawFS{}
	svc := &fakeService{rawMounts: map[string]rawfs.RawFS{"assets": fsys}}
	h := buildHandlerForTest(t, "raw", `{"mount":"assets"}`, svc)
	req := httptest.NewRequest(http.MethodPut, "/new.txt", strings.NewReader("body"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: %d", rec.Code)
	}
}

func TestRegistryResourceHandler(t *testing.T) {
	reg := &fakeRegistry{}
	svc := &fakeService{registries: map[string]registry.Registry{"ns/repo": reg}}
	h := buildHandlerForTest(t, "registry", `{"namespace":"ns","repository":"repo","strip_prefix":"/npm","public_prefix":"/public"}`, svc)
	req := httptest.NewRequest(http.MethodGet, "/npm/pkg", nil)
	req.Header.Set("Authorization", "Bearer pika_token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: %d body=%q", rec.Code, rec.Body.String())
	}
	if reg.path != "/pkg" || reg.prefix != "/public" {
		t.Fatalf("registry path/prefix: %q/%q", reg.path, reg.prefix)
	}
	if svc.lastValidateScope != "registry/ns/repo/pkg" || svc.lastValidateOp != "read" {
		t.Fatalf("token validation: scope=%q op=%q", svc.lastValidateScope, svc.lastValidateOp)
	}
}

func TestRegistryResourceHandler_RequiresTokenByDefault(t *testing.T) {
	reg := &fakeRegistry{}
	svc := &fakeService{registries: map[string]registry.Registry{"ns/repo": reg}}
	h := buildHandlerForTest(t, "registry", `{"namespace":"ns","repository":"repo"}`, svc)
	req := httptest.NewRequest(http.MethodGet, "/pkg", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: %d", rec.Code)
	}
	if reg.hits != 0 {
		t.Fatal("registry should not be called without token")
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
