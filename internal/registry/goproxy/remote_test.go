package goproxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/rawfs/localfs"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/service"
)

// fakeUpstream is a minimal Go module proxy server used to drive
// Remote pull-through tests. The handler is configurable via the
// `routes` map (path → response body) and counts hits so tests can
// verify caching behaviour.
type fakeUpstream struct {
	mu     *http.ServeMux
	hits   *atomic.Int32
	server *httptest.Server
}

func newFakeUpstream() *fakeUpstream {
	fu := &fakeUpstream{
		mu:   http.NewServeMux(),
		hits: new(atomic.Int32),
	}
	fu.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fu.hits.Add(1)
		fu.mu.ServeHTTP(w, r)
	}))
	return fu
}

func (fu *fakeUpstream) URL() string { return fu.server.URL }
func (fu *fakeUpstream) Hits() int32 { return fu.hits.Load() }
func (fu *fakeUpstream) Close()      { fu.server.Close() }

func (fu *fakeUpstream) Serve(path, contentType, body string) {
	fu.mu.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, body)
	})
}

func newRemote(t *testing.T, upstreamURL string) (*Remote, rawfs.RawFS) {
	t.Helper()
	dir := t.TempDir()
	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("localfs.New: %v", err)
	}
	deps := registry.Deps{
		MountRawFS: func(string) (rawfs.RawFS, error) { return fs, nil },
	}
	repo := &service.RegistryRepository{
		Name:       "go-mirror",
		Type:       service.RegistryTypeGo,
		Kind:       service.RegistryKindRemote,
		Mount:      "m",
		BasePath:   "go",
		URL:        upstreamURL,
		MutableTTL: "1h",
	}
	r, err := NewRemoteFactory()(context.Background(), deps, "default", repo)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	return r.(*Remote), fs
}

func TestRemote_FactoryProducesValidRegistry(t *testing.T) {
	fu := newFakeUpstream()
	defer fu.Close()
	rr, _ := newRemote(t, fu.URL())
	if rr.Type() != "go" || rr.Kind() != "remote" {
		t.Errorf("type=%s kind=%s", rr.Type(), rr.Kind())
	}
}

func TestRemote_GetVersionInfoFetchesAndCaches(t *testing.T) {
	fu := newFakeUpstream()
	defer fu.Close()
	mod := "github.com/foo/bar"
	encoded := EncodeModulePath(mod)
	fu.Serve("/"+encoded+"/@v/v1.0.0.info", "application/json",
		`{"Version":"v1.0.0","Time":"2024-01-01T00:00:00Z"}`)
	fu.Serve("/"+encoded+"/@v/v1.0.0.mod", "text/plain", "module github.com/foo/bar\n")
	fu.Serve("/"+encoded+"/@v/v1.0.0.zip", "application/zip", "ZIPDATA")

	rr, _ := newRemote(t, fu.URL())

	// First fetch — hits upstream for the requested file and warms
	// the sibling .mod/.zip so the version is fully cached.
	r := httptest.NewRequest(http.MethodGet, "/"+encoded+"/@v/v1.0.0.info", nil)
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "v1.0.0") {
		t.Fatalf("body %q", w.Body.String())
	}
	hits1 := fu.Hits()
	if hits1 != 3 {
		t.Fatalf("expected 3 upstream hits during whole-version warm-up, got %d", hits1)
	}
	for _, ext := range []string{"info", "mod", "zip"} {
		if _, err := rr.store.StatVersionFile(mod, "v1.0.0", ext); err != nil {
			t.Fatalf("%s was not cached: %v", ext, err)
		}
	}

	// Second fetch — served from cache.
	r2 := httptest.NewRequest(http.MethodGet, "/"+encoded+"/@v/v1.0.0.info", nil)
	w2 := httptest.NewRecorder()
	rr.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("cached status %d", w2.Code)
	}
	if fu.Hits() != hits1 {
		t.Fatalf("cache miss: upstream hit again (%d → %d)", hits1, fu.Hits())
	}

	// The sibling zip was warmed too; reading it must not hit upstream.
	r3 := httptest.NewRequest(http.MethodGet, "/"+encoded+"/@v/v1.0.0.zip", nil)
	w3 := httptest.NewRecorder()
	rr.ServeHTTP(w3, r3)
	if w3.Code != http.StatusOK {
		t.Fatalf("cached zip status %d", w3.Code)
	}
	if fu.Hits() != hits1 {
		t.Fatalf("warmed zip missed cache: upstream hit again (%d → %d)", hits1, fu.Hits())
	}
}

func TestRemote_GetMissingReturns404(t *testing.T) {
	fu := newFakeUpstream()
	defer fu.Close()
	// No routes registered — every request 404s.

	rr, _ := newRemote(t, fu.URL())
	r := httptest.NewRequest(http.MethodGet, "/"+EncodeModulePath("github.com/missing/mod")+"/@v/v0.0.1.info", nil)
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestRemote_GetZip(t *testing.T) {
	fu := newFakeUpstream()
	defer fu.Close()
	mod := "github.com/foo/bar"
	encoded := EncodeModulePath(mod)
	fu.Serve("/"+encoded+"/@v/v1.0.0.zip", "application/zip", "ZIPDATA")

	rr, _ := newRemote(t, fu.URL())
	r := httptest.NewRequest(http.MethodGet, "/"+encoded+"/@v/v1.0.0.zip", nil)
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if w.Body.String() != "ZIPDATA" {
		t.Fatalf("body %q", w.Body.String())
	}
	if w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("content-type %q", w.Header().Get("Content-Type"))
	}
}

func TestRemote_GetList(t *testing.T) {
	fu := newFakeUpstream()
	defer fu.Close()
	mod := "github.com/foo/bar"
	encoded := EncodeModulePath(mod)
	fu.Serve("/"+encoded+"/@v/list", "text/plain", "v0.1.0\nv1.0.0\nv1.2.0\n")

	rr, _ := newRemote(t, fu.URL())
	r := httptest.NewRequest(http.MethodGet, "/"+encoded+"/@v/list", nil)
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "v1.2.0") {
		t.Fatalf("list body %q", w.Body.String())
	}
}

func TestRemote_GetLatest(t *testing.T) {
	fu := newFakeUpstream()
	defer fu.Close()
	mod := "github.com/foo/bar"
	encoded := EncodeModulePath(mod)
	fu.Serve("/"+encoded+"/@latest", "application/json",
		`{"Version":"v1.2.3","Time":"2024-01-15T00:00:00Z"}`)

	rr, _ := newRemote(t, fu.URL())
	r := httptest.NewRequest(http.MethodGet, "/"+encoded+"/@latest", nil)
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "v1.2.3") {
		t.Fatalf("body %q", w.Body.String())
	}
}

func TestRemote_LatestTTLRevalidates(t *testing.T) {
	fu := newFakeUpstream()
	defer fu.Close()
	mod := "github.com/foo/bar"
	encoded := EncodeModulePath(mod)

	// First response.
	v := "v1.0.0"
	fu.mu.HandleFunc("/"+encoded+"/@latest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"Version":%q,"Time":"2024-01-01T00:00:00Z"}`, v)
	})

	dir := t.TempDir()
	fs, _ := localfs.New(dir)
	deps := registry.Deps{
		MountRawFS: func(string) (rawfs.RawFS, error) { return fs, nil },
	}
	// Very short TTL so revalidation happens on second request.
	repo := &service.RegistryRepository{
		Name: "go-mirror", Type: service.RegistryTypeGo, Kind: service.RegistryKindRemote,
		Mount: "m", BasePath: "go", URL: fu.URL(), MutableTTL: "1ms",
	}
	rr, err := NewRemoteFactory()(context.Background(), deps, "default", repo)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	defer rr.Close()

	r := httptest.NewRequest(http.MethodGet, "/"+encoded+"/@latest", nil)
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("first status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "v1.0.0") {
		t.Fatalf("first body %q", w.Body.String())
	}

	// Mutate upstream and wait past TTL.
	v = "v2.0.0"
	time.Sleep(10 * time.Millisecond)

	r2 := httptest.NewRequest(http.MethodGet, "/"+encoded+"/@latest", nil)
	w2 := httptest.NewRecorder()
	rr.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("second status %d", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "v2.0.0") {
		t.Fatalf("expected revalidated body to show v2.0.0, got %q", w2.Body.String())
	}
}

func TestRemote_RejectsWrite(t *testing.T) {
	fu := newFakeUpstream()
	defer fu.Close()
	rr, _ := newRemote(t, fu.URL())
	r := httptest.NewRequest(http.MethodPut, "/"+EncodeModulePath("github.com/foo/bar")+"/@v/v1.0.0.info", strings.NewReader("x"))
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for PUT on Remote, got %d", w.Code)
	}
}

// TestRemote_PurgeMutableForcesRefetch verifies that calling
// PurgeCache with the default (mutable-only) scope deletes cached
// @latest pointers so a subsequent request re-hits upstream.
// Immutable artifacts must NOT be re-fetched.
func TestRemote_PurgeMutableForcesRefetch(t *testing.T) {
	fu := newFakeUpstream()
	defer fu.Close()
	mod := "github.com/foo/bar"
	encoded := EncodeModulePath(mod)
	fu.Serve("/"+encoded+"/@latest", "application/json",
		`{"Version":"v1.2.3","Time":"2024-01-15T00:00:00Z"}`)
	fu.Serve("/"+encoded+"/@v/v1.0.0.info", "application/json",
		`{"Version":"v1.0.0","Time":"2024-01-01T00:00:00Z"}`)
	fu.Serve("/"+encoded+"/@v/v1.0.0.mod", "text/plain", "module github.com/foo/bar\n")
	fu.Serve("/"+encoded+"/@v/v1.0.0.zip", "application/zip", "ZIPDATA")

	rr, _ := newRemote(t, fu.URL())

	// Warm caches. .info first so it doesn't get knocked out by the
	// WriteVersionFile-triggered @latest invalidation (a quirk of
	// the Local store: every version write also drops the cached
	// @latest pointer to force a rebuild).
	for _, p := range []string{
		"/" + encoded + "/@v/v1.0.0.info",
		"/" + encoded + "/@latest",
	} {
		r := httptest.NewRequest(http.MethodGet, p, nil)
		w := httptest.NewRecorder()
		rr.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("warm %s: %d", p, w.Code)
		}
	}
	hitsBeforePurge := fu.Hits()

	stats, err := rr.PurgeCache(context.Background(), registry.PurgeOptions{All: false})
	if err != nil {
		t.Fatalf("PurgeCache: %v", err)
	}
	if stats.PurgedFiles == 0 {
		t.Fatalf("expected >0 purged files, got %+v", stats)
	}

	// @latest must re-fetch upstream.
	r := httptest.NewRequest(http.MethodGet, "/"+encoded+"/@latest", nil)
	rr.ServeHTTP(httptest.NewRecorder(), r)
	if fu.Hits() == hitsBeforePurge {
		t.Fatalf("@latest did NOT re-fetch after mutable purge (hits stuck at %d)", hitsBeforePurge)
	}
	hitsAfterLatest := fu.Hits()

	// .info must still be cached (immutable, mutable-only scope).
	r2 := httptest.NewRequest(http.MethodGet, "/"+encoded+"/@v/v1.0.0.info", nil)
	rr.ServeHTTP(httptest.NewRecorder(), r2)
	if fu.Hits() != hitsAfterLatest {
		t.Fatalf("immutable .info was re-fetched after mutable purge (should be kept)")
	}
}

// TestRemote_PurgeAllDropsImmutables checks the wider scope: with
// opts.All=true, the .info we cached previously must also be
// gone and re-fetched on the next read.
func TestRemote_PurgeAllDropsImmutables(t *testing.T) {
	fu := newFakeUpstream()
	defer fu.Close()
	mod := "github.com/foo/bar"
	encoded := EncodeModulePath(mod)
	fu.Serve("/"+encoded+"/@v/v1.0.0.info", "application/json",
		`{"Version":"v1.0.0","Time":"2024-01-01T00:00:00Z"}`)
	fu.Serve("/"+encoded+"/@v/v1.0.0.mod", "text/plain", "module github.com/foo/bar\n")
	fu.Serve("/"+encoded+"/@v/v1.0.0.zip", "application/zip", "ZIPDATA")

	rr, _ := newRemote(t, fu.URL())
	// Warm.
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest(http.MethodGet, "/"+encoded+"/@v/v1.0.0.info", nil)
		rr.ServeHTTP(httptest.NewRecorder(), r)
	}
	hitsBeforePurge := fu.Hits()
	if hitsBeforePurge != 3 {
		t.Fatalf("expected 3 upstream hits during whole-version warm-up, got %d", hitsBeforePurge)
	}

	if _, err := rr.PurgeCache(context.Background(), registry.PurgeOptions{All: true}); err != nil {
		t.Fatalf("PurgeCache: %v", err)
	}

	// Next read must re-fetch.
	r := httptest.NewRequest(http.MethodGet, "/"+encoded+"/@v/v1.0.0.info", nil)
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("post-purge fetch %d", w.Code)
	}
	if fu.Hits() == hitsBeforePurge {
		t.Fatalf(".info was NOT re-fetched after PurgeAll (hits stuck at %d)", hitsBeforePurge)
	}
}

func TestRemote_BogusURLPath(t *testing.T) {
	fu := newFakeUpstream()
	defer fu.Close()
	rr, _ := newRemote(t, fu.URL())
	r := httptest.NewRequest(http.MethodGet, "/no-prefix", nil)
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
