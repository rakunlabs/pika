package goproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/rawfs/localfs"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/service"
)

// newLocal returns a fresh Local Registry rooted at a temp dir.
func newLocal(t *testing.T, allowPush bool) *Local {
	t.Helper()
	dir := t.TempDir()
	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("localfs.New: %v", err)
	}
	deps := registry.Deps{
		MountRawFS: func(name string) (rawfs.RawFS, error) { return fs, nil },
	}
	repo := &service.RegistryRepository{
		Name:      "go-local",
		Type:      service.RegistryTypeGo,
		Kind:      service.RegistryKindLocal,
		Mount:     "m",
		BasePath:  "go",
		AllowPush: allowPush,
	}
	r, err := NewLocalFactory()(context.Background(), deps, "default", repo)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	return r.(*Local)
}

func uploadInfo(t *testing.T, l *Local, mod, ver string) {
	t.Helper()
	body, _ := json.Marshal(VersionInfo{Version: ver, Time: time.Now().UTC()})
	r := httptest.NewRequest(http.MethodPut, "/"+EncodeModulePath(mod)+"/@v/"+ver+".info", bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("upload info %s: status %d, body %s", ver, w.Code, w.Body.String())
	}
}

func uploadMod(t *testing.T, l *Local, mod, ver, body string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/"+EncodeModulePath(mod)+"/@v/"+ver+".mod", strings.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("upload mod: status %d, body %s", w.Code, w.Body.String())
	}
}

func uploadZip(t *testing.T, l *Local, mod, ver string, body []byte) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/"+EncodeModulePath(mod)+"/@v/"+ver+".zip", bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Type", "application/zip")
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("upload zip: status %d, body %s", w.Code, w.Body.String())
	}
}

func TestLocal_FactoryProducesValidRegistry(t *testing.T) {
	l := newLocal(t, true)
	if l.Namespace() != "default" {
		t.Errorf("namespace=%q", l.Namespace())
	}
	if l.Name() != "go-local" {
		t.Errorf("name=%q", l.Name())
	}
	if l.Type() != "go" {
		t.Errorf("type=%q", l.Type())
	}
	if l.Kind() != "local" {
		t.Errorf("kind=%q", l.Kind())
	}
	if !l.AllowPush() {
		t.Errorf("allowPush=false")
	}
}

func TestLocal_UploadThenGet(t *testing.T) {
	l := newLocal(t, true)
	mod := "github.com/foo/bar"
	uploadInfo(t, l, mod, "v1.0.0")
	uploadMod(t, l, mod, "v1.0.0", "module github.com/foo/bar\n\ngo 1.21\n")
	uploadZip(t, l, mod, "v1.0.0", []byte("FAKEZIPDATA"))

	// GET .info
	{
		r := httptest.NewRequest(http.MethodGet, "/"+EncodeModulePath(mod)+"/@v/v1.0.0.info", nil)
		w := httptest.NewRecorder()
		l.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("get info status %d", w.Code)
		}
		if !strings.Contains(w.Body.String(), `"Version":"v1.0.0"`) {
			t.Fatalf("info body %q", w.Body.String())
		}
		if w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("content-type %q", w.Header().Get("Content-Type"))
		}
		if w.Header().Get("ETag") == "" {
			t.Fatalf("etag missing")
		}
	}

	// GET .mod
	{
		r := httptest.NewRequest(http.MethodGet, "/"+EncodeModulePath(mod)+"/@v/v1.0.0.mod", nil)
		w := httptest.NewRecorder()
		l.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("get mod status %d", w.Code)
		}
		if !strings.Contains(w.Body.String(), "module github.com/foo/bar") {
			t.Fatalf("mod body %q", w.Body.String())
		}
	}

	// GET .zip
	{
		r := httptest.NewRequest(http.MethodGet, "/"+EncodeModulePath(mod)+"/@v/v1.0.0.zip", nil)
		w := httptest.NewRecorder()
		l.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("get zip status %d", w.Code)
		}
		if w.Body.String() != "FAKEZIPDATA" {
			t.Fatalf("zip body %q", w.Body.String())
		}
		if w.Header().Get("Content-Type") != "application/zip" {
			t.Fatalf("zip content-type %q", w.Header().Get("Content-Type"))
		}
	}
}

func TestLocal_GetList(t *testing.T) {
	l := newLocal(t, true)
	mod := "github.com/foo/bar"
	uploadInfo(t, l, mod, "v0.1.0")
	uploadInfo(t, l, mod, "v1.0.0")
	uploadInfo(t, l, mod, "v1.2.0")

	r := httptest.NewRequest(http.MethodGet, "/"+EncodeModulePath(mod)+"/@v/list", nil)
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list status %d", w.Code)
	}
	got := w.Body.String()
	want := "v0.1.0\nv1.0.0\nv1.2.0\n"
	if got != want {
		t.Fatalf("list body %q, want %q", got, want)
	}
}

func TestLocal_GetLatest(t *testing.T) {
	l := newLocal(t, true)
	mod := "github.com/foo/bar"
	uploadInfo(t, l, mod, "v0.1.0")
	uploadInfo(t, l, mod, "v1.0.0")

	r := httptest.NewRequest(http.MethodGet, "/"+EncodeModulePath(mod)+"/@latest", nil)
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("latest status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"Version":"v1.0.0"`) {
		t.Fatalf("latest body %q", w.Body.String())
	}
}

func TestLocal_GetMissing(t *testing.T) {
	l := newLocal(t, true)
	r := httptest.NewRequest(http.MethodGet, "/"+EncodeModulePath("github.com/missing/mod")+"/@v/v1.0.0.info", nil)
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestLocal_UploadRejectedWhenPushDisabled(t *testing.T) {
	l := newLocal(t, false)
	body, _ := json.Marshal(VersionInfo{Version: "v1.0.0", Time: time.Now()})
	r := httptest.NewRequest(http.MethodPut, "/"+EncodeModulePath("github.com/foo/bar")+"/@v/v1.0.0.info", bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 when push disabled, got %d", w.Code)
	}
	if w.Header().Get("Allow") == "" {
		t.Fatalf("Allow header missing on 405")
	}
}

func TestLocal_UploadAutoFillsInfoTime(t *testing.T) {
	l := newLocal(t, true)
	mod := "github.com/foo/bar"
	// Body without Time.
	body := []byte(`{"Version":"v1.0.0"}`)
	r := httptest.NewRequest(http.MethodPut, "/"+EncodeModulePath(mod)+"/@v/v1.0.0.info", bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("upload status %d", w.Code)
	}

	rc, _, err := l.Store().OpenVersionFile(mod, "v1.0.0", "info")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rc.Close()
	out, _ := io.ReadAll(rc)
	var info VersionInfo
	if err := json.Unmarshal(out, &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.Time.IsZero() {
		t.Fatalf("Time should have been auto-filled, got zero")
	}
	if info.Version != "v1.0.0" {
		t.Fatalf("Version=%q", info.Version)
	}
}

func TestLocal_UploadSizeLimit(t *testing.T) {
	l := newLocal(t, true)
	l.maxUpload = 10 // bytes
	body := []byte("this body is more than ten bytes")
	r := httptest.NewRequest(http.MethodPut, "/"+EncodeModulePath("github.com/foo/bar")+"/@v/v1.0.0.zip", bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", w.Code)
	}
}

func TestLocal_GetUnrecognisedPath(t *testing.T) {
	l := newLocal(t, true)
	r := httptest.NewRequest(http.MethodGet, "/garbage", nil)
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("garbage path: status %d", w.Code)
	}
}

func TestLocal_MethodNotAllowed(t *testing.T) {
	l := newLocal(t, true)
	r := httptest.NewRequest(http.MethodPost, "/"+EncodeModulePath("github.com/foo/bar")+"/@v/v1.0.0.info", nil)
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: status %d", w.Code)
	}
}

// captureEmitter is a minimal events.Emitter used by tests to
// inspect emitted hook events. Thread-safe via the slice append
// happening on the same goroutine as ServeHTTP.
type captureEmitter struct {
	events []hook.Event
}

func (c *captureEmitter) Emit(e hook.Event) {
	c.events = append(c.events, e)
}

// TestLocal_PublishEmitsEvent confirms a successful module publish
// (info + mod + zip) fires exactly one registry.published event,
// keyed on the .zip slot (the canonical "this version is now
// installable" marker).
func TestLocal_PublishEmitsEvent(t *testing.T) {
	dir := t.TempDir()
	fs, _ := localfs.New(dir)
	cap := &captureEmitter{}
	deps := registry.Deps{
		MountRawFS: func(string) (rawfs.RawFS, error) { return fs, nil },
		Emitter:    cap,
	}
	repo := &service.RegistryRepository{
		Name: "go-local", Type: service.RegistryTypeGo, Kind: service.RegistryKindLocal,
		Mount: "m", BasePath: "go", AllowPush: true,
	}
	r, err := NewLocalFactory()(context.Background(), deps, "default", repo)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	l := r.(*Local)

	uploadInfo(t, l, "github.com/foo/bar", "v1.0.0")
	uploadMod(t, l, "github.com/foo/bar", "v1.0.0", "module github.com/foo/bar")
	uploadZip(t, l, "github.com/foo/bar", "v1.0.0", []byte("ZIPDATA"))

	if len(cap.events) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(cap.events), cap.events)
	}
	e := cap.events[0]
	if e.Type != hook.EventRegistryPublished {
		t.Errorf("type = %s, want %s", e.Type, hook.EventRegistryPublished)
	}
	if e.Mount != "default" {
		t.Errorf("Mount = %s, want default", e.Mount)
	}
	if e.Path != "go-local/github.com/foo/bar@v1.0.0" {
		t.Errorf("Path = %s, want go-local/github.com/foo/bar@v1.0.0", e.Path)
	}
	if e.Protocol != "registry-go" {
		t.Errorf("Protocol = %s, want registry-go", e.Protocol)
	}
}

// TestLocal_StatsCountsModulesAndVersions confirms the Stats
// provider returns accurate counts after a few uploads. Two
// modules, three total versions; bytes > 0.
func TestLocal_StatsCountsModulesAndVersions(t *testing.T) {
	l := newLocal(t, true)

	uploadInfo(t, l, "github.com/foo/a", "v0.1.0")
	uploadInfo(t, l, "github.com/foo/a", "v0.2.0")
	uploadInfo(t, l, "github.com/foo/b", "v1.0.0")

	stats, err := l.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.ModuleCount != 2 {
		t.Errorf("ModuleCount = %d, want 2", stats.ModuleCount)
	}
	if stats.VersionCount != 3 {
		t.Errorf("VersionCount = %d, want 3", stats.VersionCount)
	}
	if stats.TotalBytes <= 0 {
		t.Errorf("TotalBytes = %d, want > 0", stats.TotalBytes)
	}
}

func TestLocal_IfNoneMatchReturns304(t *testing.T) {
	l := newLocal(t, true)
	mod := "github.com/foo/bar"
	uploadInfo(t, l, mod, "v1.0.0")

	// First fetch — capture etag.
	r1 := httptest.NewRequest(http.MethodGet, "/"+EncodeModulePath(mod)+"/@v/v1.0.0.info", nil)
	w1 := httptest.NewRecorder()
	l.ServeHTTP(w1, r1)
	etag := w1.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("etag missing")
	}

	// Second fetch with If-None-Match — expect 304.
	r2 := httptest.NewRequest(http.MethodGet, "/"+EncodeModulePath(mod)+"/@v/v1.0.0.info", nil)
	r2.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	l.ServeHTTP(w2, r2)
	if w2.Code != http.StatusNotModified {
		t.Fatalf("expected 304, got %d", w2.Code)
	}
}
