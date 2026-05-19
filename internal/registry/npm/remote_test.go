package npm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/rawfs/localfs"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/service"
)

type fakeNPMUpstream struct {
	mux    *http.ServeMux
	hits   *atomic.Int32
	server *httptest.Server
}

func newFakeNPMUpstream() *fakeNPMUpstream {
	fu := &fakeNPMUpstream{mux: http.NewServeMux(), hits: new(atomic.Int32)}
	fu.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fu.hits.Add(1)
		fu.mux.ServeHTTP(w, r)
	}))
	return fu
}

func (fu *fakeNPMUpstream) Close()         { fu.server.Close() }
func (fu *fakeNPMUpstream) URL() string    { return fu.server.URL }
func (fu *fakeNPMUpstream) Hits() int32    { return fu.hits.Load() }

func (fu *fakeNPMUpstream) ServeJSON(path string, body string) {
	fu.mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
}

func (fu *fakeNPMUpstream) ServeBytes(path string, body []byte) {
	fu.mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	})
}

func newRemote(t *testing.T, upstreamURL string) *Remote {
	t.Helper()
	dir := t.TempDir()
	fs, _ := localfs.New(dir)
	deps := registry.Deps{
		MountRawFS: func(string) (rawfs.RawFS, error) { return fs, nil },
	}
	repo := &service.RegistryRepository{
		Name: "npm-mirror", Type: service.RegistryTypeNPM, Kind: service.RegistryKindRemote,
		Mount: "m", BasePath: "npm", URL: upstreamURL, MutableTTL: "1h",
	}
	r, err := NewRemoteFactory()(context.Background(), deps, "default", repo)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	return r.(*Remote)
}

func TestNPMRemote_PackumentFetchAndCache(t *testing.T) {
	fu := newFakeNPMUpstream()
	defer fu.Close()
	fu.ServeJSON("/lodash", `{
		"name":"lodash",
		"dist-tags":{"latest":"1.0.0"},
		"versions":{
			"1.0.0":{
				"name":"lodash","version":"1.0.0",
				"dist":{"tarball":"`+fu.URL()+`/lodash/-/lodash-1.0.0.tgz"}
			}
		}
	}`)

	rr := newRemote(t, fu.URL())

	r := httptest.NewRequest(http.MethodGet, "/lodash", nil)
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/npm-mirror")
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}

	var pkg map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &pkg)
	versions := pkg["versions"].(map[string]any)
	v1 := versions["1.0.0"].(map[string]any)
	dist := v1["dist"].(map[string]any)
	// tarball URL should be rewritten to pika.
	tb, _ := dist["tarball"].(string)
	if !strings.Contains(tb, "/registries/default/npm-mirror/lodash/-/") {
		t.Fatalf("expected rewritten URL, got %q", tb)
	}
	hits1 := fu.Hits()
	if hits1 != 1 {
		t.Fatalf("expected 1 upstream hit, got %d", hits1)
	}

	// Second request — should be cached.
	r2 := httptest.NewRequest(http.MethodGet, "/lodash", nil)
	r2.Header.Set("X-Pika-Registry-Prefix", "/registries/default/npm-mirror")
	w2 := httptest.NewRecorder()
	rr.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("cached status %d", w2.Code)
	}
	if fu.Hits() != hits1 {
		t.Fatalf("cache miss: upstream hit again (%d → %d)", hits1, fu.Hits())
	}
}

func TestNPMRemote_TarballFetchAndCache(t *testing.T) {
	fu := newFakeNPMUpstream()
	defer fu.Close()
	tarball := []byte("UPSTREAM-TARBALL")
	fu.ServeBytes("/lodash/-/lodash-1.0.0.tgz", tarball)

	rr := newRemote(t, fu.URL())

	r := httptest.NewRequest(http.MethodGet, "/lodash/-/lodash-1.0.0.tgz", nil)
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if w.Body.String() != "UPSTREAM-TARBALL" {
		t.Fatalf("body %q", w.Body.String())
	}
	hits1 := fu.Hits()

	// Second request — cached.
	r2 := httptest.NewRequest(http.MethodGet, "/lodash/-/lodash-1.0.0.tgz", nil)
	w2 := httptest.NewRecorder()
	rr.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("cached status %d", w2.Code)
	}
	if fu.Hits() != hits1 {
		t.Fatalf("cache miss after first fetch")
	}
}

func TestNPMRemote_Missing(t *testing.T) {
	fu := newFakeNPMUpstream()
	defer fu.Close()
	rr := newRemote(t, fu.URL())
	r := httptest.NewRequest(http.MethodGet, "/missing-pkg", nil)
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestNPMRemote_RejectsPublish(t *testing.T) {
	fu := newFakeNPMUpstream()
	defer fu.Close()
	rr := newRemote(t, fu.URL())
	r := httptest.NewRequest(http.MethodPut, "/lodash", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// TestNPMRemote_PurgeMutableForcesPackumentRefetch verifies that a
// mutable-only purge drops the cached packument so the next read
// re-fetches from upstream.
func TestNPMRemote_PurgeMutableForcesPackumentRefetch(t *testing.T) {
	fu := newFakeNPMUpstream()
	defer fu.Close()
	fu.ServeJSON("/lodash", `{
		"name":"lodash",
		"dist-tags":{"latest":"1.0.0"},
		"versions":{
			"1.0.0":{
				"name":"lodash","version":"1.0.0",
				"dist":{"tarball":"`+fu.URL()+`/lodash/-/lodash-1.0.0.tgz"}
			}
		}
	}`)

	rr := newRemote(t, fu.URL())
	warm := func() {
		r := httptest.NewRequest(http.MethodGet, "/lodash", nil)
		r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/npm-mirror")
		w := httptest.NewRecorder()
		rr.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("warm %d", w.Code)
		}
	}
	warm()
	hitsBefore := fu.Hits()
	warm() // confirms cache hit
	if fu.Hits() != hitsBefore {
		t.Fatalf("packument should have cached, got %d → %d", hitsBefore, fu.Hits())
	}

	stats, err := rr.PurgeCache(context.Background(), registry.PurgeOptions{All: false})
	if err != nil {
		t.Fatalf("PurgeCache: %v", err)
	}
	if stats.PurgedFiles == 0 {
		t.Fatalf("expected purged files >0, got %+v", stats)
	}

	warm() // must hit upstream again
	if fu.Hits() == hitsBefore {
		t.Fatalf("packument was not re-fetched after purge (stuck at %d)", hitsBefore)
	}
}
