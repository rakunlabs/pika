package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"github.com/rakunlabs/pika/internal/registry/blobstore"
	"github.com/rakunlabs/pika/internal/service"
)

// fakeDockerUpstream is a minimal Docker registry test server. It
// supports anonymous reads (no challenge); the auth-challenge flow
// is exercised separately because pulling that in requires a more
// elaborate fixture. Many real public registries (GHCR, Quay, even
// Docker Hub for public repos) accept the first GET if the realm
// is reachable; we test the simple path here.
type fakeDockerUpstream struct {
	mux    *http.ServeMux
	hits   *atomic.Int32
	server *httptest.Server
}

func newFakeDockerUpstream() *fakeDockerUpstream {
	fu := &fakeDockerUpstream{mux: http.NewServeMux(), hits: new(atomic.Int32)}
	fu.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fu.hits.Add(1)
		fu.mux.ServeHTTP(w, r)
	}))
	// Default /v2/ probe: 200, no challenge.
	fu.mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
			w.WriteHeader(http.StatusOK)
			return
		}
		// Sub-paths fall through to whatever was registered.
		w.WriteHeader(http.StatusNotFound)
	})
	return fu
}

func (fu *fakeDockerUpstream) Close()         { fu.server.Close() }
func (fu *fakeDockerUpstream) URL() string    { return fu.server.URL }
func (fu *fakeDockerUpstream) Hits() int32    { return fu.hits.Load() }

func (fu *fakeDockerUpstream) ServeManifest(name, ref, mediaType string, body []byte) {
	fu.mux.HandleFunc("/v2/"+name+"/manifests/"+ref, func(w http.ResponseWriter, r *http.Request) {
		dgst := sha256.Sum256(body)
		w.Header().Set("Content-Type", mediaType)
		w.Header().Set("Docker-Content-Digest", "sha256:"+hex.EncodeToString(dgst[:]))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
}

func (fu *fakeDockerUpstream) ServeBlob(name, digest string, body []byte) {
	fu.mux.HandleFunc("/v2/"+name+"/blobs/"+digest, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		_, _ = w.Write(body)
	})
}

func (fu *fakeDockerUpstream) ServeTagsList(name string, tags []string) {
	fu.mux.HandleFunc("/v2/"+name+"/tags/list", func(w http.ResponseWriter, _ *http.Request) {
		body, _ := json.Marshal(map[string]any{"name": name, "tags": tags})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
}

// newDockerRemote constructs a Remote registry pointed at the given
// upstream URL.
func newDockerRemote(t *testing.T, upstreamURL string) *Remote {
	t.Helper()
	dir := t.TempDir()
	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("localfs.New: %v", err)
	}
	deps := registry.Deps{
		MountRawFS: func(string) (rawfs.RawFS, error) { return fs, nil },
		MountFor: func(_, basePath string) (blobstore.BlobStore, error) {
			return blobstore.NewRawFSBlobStore(fs, basePath)
		},
	}
	repo := &service.RegistryRepository{
		Name: "docker-mirror", Type: service.RegistryTypeDocker, Kind: service.RegistryKindRemote,
		Mount: "m", BasePath: "docker", URL: upstreamURL, MutableTTL: "1h",
	}
	r, err := NewRemoteFactory()(context.Background(), deps, "default", repo)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	return r.(*Remote)
}

func TestDockerRemote_Metadata(t *testing.T) {
	fu := newFakeDockerUpstream()
	defer fu.Close()
	rr := newDockerRemote(t, fu.URL())
	if rr.Type() != "docker" || rr.Kind() != "remote" {
		t.Errorf("type=%s kind=%s", rr.Type(), rr.Kind())
	}
}

func TestDockerRemote_VersionProbe(t *testing.T) {
	fu := newFakeDockerUpstream()
	defer fu.Close()
	rr := newDockerRemote(t, fu.URL())

	r := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/docker-mirror")
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("version probe %d", w.Code)
	}
	if w.Header().Get("Docker-Distribution-API-Version") != "registry/2.0" {
		t.Errorf("API version header missing")
	}
}

func TestDockerRemote_RejectsPush(t *testing.T) {
	fu := newFakeDockerUpstream()
	defer fu.Close()
	rr := newDockerRemote(t, fu.URL())
	r := httptest.NewRequest(http.MethodPost, "/v2/lib/foo/blobs/uploads/", nil)
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/docker-mirror")
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestDockerRemote_ManifestPullByTag(t *testing.T) {
	fu := newFakeDockerUpstream()
	defer fu.Close()

	name := "lib/foo"
	manifest := []byte(`{"schemaVersion":2,"layers":[]}`)
	fu.ServeManifest(name, "latest", "application/vnd.oci.image.manifest.v1+json", manifest)

	rr := newDockerRemote(t, fu.URL())

	// First fetch — upstream hit.
	r := httptest.NewRequest(http.MethodGet, "/v2/"+name+"/manifests/latest", nil)
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/docker-mirror")
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), manifest) {
		t.Fatalf("manifest body mismatch")
	}
	hits1 := fu.Hits()

	// Second fetch — cached.
	r2 := httptest.NewRequest(http.MethodGet, "/v2/"+name+"/manifests/latest", nil)
	r2.Header.Set("X-Pika-Registry-Prefix", "/registries/default/docker-mirror")
	w2 := httptest.NewRecorder()
	rr.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("cached status %d", w2.Code)
	}
	if fu.Hits() != hits1 {
		t.Fatalf("upstream re-hit (cache miss): %d → %d", hits1, fu.Hits())
	}
}

func TestDockerRemote_ManifestPullByDigest(t *testing.T) {
	fu := newFakeDockerUpstream()
	defer fu.Close()

	name := "lib/foo"
	manifest := []byte(`{"schemaVersion":2,"layers":[]}`)
	dgst := sha256.Sum256(manifest)
	digestStr := "sha256:" + hex.EncodeToString(dgst[:])
	fu.ServeManifest(name, digestStr, "application/vnd.oci.image.manifest.v1+json", manifest)

	rr := newDockerRemote(t, fu.URL())
	r := httptest.NewRequest(http.MethodGet, "/v2/"+name+"/manifests/"+digestStr, nil)
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/docker-mirror")
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Docker-Content-Digest") != digestStr {
		t.Fatalf("digest header %q want %q", w.Header().Get("Docker-Content-Digest"), digestStr)
	}
}

func TestDockerRemote_BlobPullCachesLocally(t *testing.T) {
	fu := newFakeDockerUpstream()
	defer fu.Close()

	name := "lib/foo"
	body := []byte("UPSTREAM-BLOB-CONTENTS")
	dgst := sha256.Sum256(body)
	digestStr := "sha256:" + hex.EncodeToString(dgst[:])
	fu.ServeBlob(name, digestStr, body)

	rr := newDockerRemote(t, fu.URL())

	r := httptest.NewRequest(http.MethodGet, "/v2/"+name+"/blobs/"+digestStr, nil)
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/docker-mirror")
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatalf("body mismatch")
	}
	hits1 := fu.Hits()

	// Second fetch — cached.
	r2 := httptest.NewRequest(http.MethodGet, "/v2/"+name+"/blobs/"+digestStr, nil)
	r2.Header.Set("X-Pika-Registry-Prefix", "/registries/default/docker-mirror")
	w2 := httptest.NewRecorder()
	rr.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("cached status %d", w2.Code)
	}
	if fu.Hits() != hits1 {
		t.Fatalf("blob cache miss (upstream rehit %d→%d)", hits1, fu.Hits())
	}
}

func TestDockerRemote_TagsListProxy(t *testing.T) {
	fu := newFakeDockerUpstream()
	defer fu.Close()
	fu.ServeTagsList("lib/foo", []string{"v1.0.0", "v2.0.0"})

	rr := newDockerRemote(t, fu.URL())
	r := httptest.NewRequest(http.MethodGet, "/v2/lib/foo/tags/list", nil)
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/docker-mirror")
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "v2.0.0") {
		t.Fatalf("body %q", w.Body.String())
	}
}

func TestDockerRemote_DockerHubLibraryPrefix(t *testing.T) {
	// Use a mock URL that the constructor identifies as Docker Hub
	// and assert the path prefix kicks in.
	rr := newDockerRemoteWith(t, "https://registry-1.docker.io", "1h")
	if rr.pathPrefix != "library" {
		t.Errorf("expected library/ prefix for docker.io URL, got %q", rr.pathPrefix)
	}
	if rr.upstreamName("nginx") != "library/nginx" {
		t.Errorf("upstreamName(nginx) = %q", rr.upstreamName("nginx"))
	}
	if rr.upstreamName("myorg/myimage") != "myorg/myimage" {
		t.Errorf("nested name should pass through")
	}
}

// newDockerRemoteWith builds a Remote without spinning up a fake
// upstream — used by tests that only care about constructor logic
// or per-repo configuration knobs. The optional fourth argument is
// the FloatingTags list; pass nil to inherit the default set.
func newDockerRemoteWith(t *testing.T, url, ttl string, floating ...[]string) *Remote {
	t.Helper()
	dir := t.TempDir()
	fs, _ := localfs.New(dir)
	deps := registry.Deps{
		MountRawFS: func(string) (rawfs.RawFS, error) { return fs, nil },
		MountFor: func(_, basePath string) (blobstore.BlobStore, error) {
			return blobstore.NewRawFSBlobStore(fs, basePath)
		},
	}
	repo := &service.RegistryRepository{
		Name: "r", Type: service.RegistryTypeDocker, Kind: service.RegistryKindRemote,
		Mount: "m", BasePath: "docker", URL: url, MutableTTL: ttl,
	}
	if len(floating) > 0 {
		repo.FloatingTags = floating[0]
	}
	r, err := NewRemoteFactory()(context.Background(), deps, "default", repo)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	return r.(*Remote)
}

func TestParseChallenge(t *testing.T) {
	cases := []struct {
		header        string
		wantRealm     string
		wantService   string
	}{
		{
			`Bearer realm="https://auth.docker.io/token",service="registry.docker.io"`,
			"https://auth.docker.io/token", "registry.docker.io",
		},
		{
			`Bearer realm="https://auth.example.com/v2",service="my-reg",scope="repository:foo:pull"`,
			"https://auth.example.com/v2", "my-reg",
		},
		{
			`Basic realm="foo"`,
			"", "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.header, func(t *testing.T) {
			r, s := parseChallenge(tc.header)
			if r != tc.wantRealm || s != tc.wantService {
				t.Errorf("got (%q, %q), want (%q, %q)", r, s, tc.wantRealm, tc.wantService)
			}
		})
	}
}

func TestDockerRemote_UpstreamMissingReturns404(t *testing.T) {
	fu := newFakeDockerUpstream()
	defer fu.Close()
	rr := newDockerRemote(t, fu.URL())

	r := httptest.NewRequest(http.MethodGet, "/v2/missing/img/manifests/v1.0.0", nil)
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/docker-mirror")
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body %s", w.Code, w.Body.String())
	}
}

func TestDockerRemote_BlobDigestMismatchRejected(t *testing.T) {
	fu := newFakeDockerUpstream()
	defer fu.Close()

	// Serve bogus bytes at a requested digest.
	bogus := []byte("not-the-real-bytes")
	want := sha256.Sum256([]byte("actual-bytes"))
	digestStr := "sha256:" + hex.EncodeToString(want[:])
	fu.ServeBlob("lib/foo", digestStr, bogus)

	rr := newDockerRemote(t, fu.URL())
	r := httptest.NewRequest(http.MethodGet, "/v2/lib/foo/blobs/"+digestStr, nil)
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/docker-mirror")
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		// The blob was rejected → cache miss → 404 to client.
		t.Fatalf("expected 404 (rejected mismatched bytes), got %d", w.Code)
	}
}

// TestDockerRemote_NonFloatingTagCachedForever verifies that a tag
// not in the floating-tag set is cached indefinitely: once we've
// resolved it once, subsequent reads never re-hit upstream regardless
// of how aged the cache file is.
//
// Concretely we use a very small MutableTTL ("1ms") so the freshness
// check would fail for any floating tag, then prove that a semver
// tag still serves from cache.
func TestDockerRemote_NonFloatingTagCachedForever(t *testing.T) {
	fu := newFakeDockerUpstream()
	defer fu.Close()

	name := "lib/foo"
	manifest := []byte(`{"schemaVersion":2,"layers":[]}`)
	fu.ServeManifest(name, "v1.2.3", "application/vnd.oci.image.manifest.v1+json", manifest)

	// 1ms TTL — every floating-tag freshness check fails almost
	// immediately. Non-floating tags must NOT consult the TTL.
	rr := newDockerRemoteWith(t, fu.URL(), "1ms", nil) // default floating list
	if rr.isFloatingTag("v1.2.3") {
		t.Fatal("v1.2.3 should not be classified floating")
	}

	// First fetch — cache miss, upstream hit.
	r := httptest.NewRequest(http.MethodGet, "/v2/"+name+"/manifests/v1.2.3", nil)
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/docker-mirror")
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("first fetch %d body %s", w.Code, w.Body.String())
	}
	hits1 := fu.Hits()

	// Sleep beyond TTL — for a floating tag we'd refetch, here we
	// must NOT.
	// (1ms TTL has already elapsed by definition.)

	r2 := httptest.NewRequest(http.MethodGet, "/v2/"+name+"/manifests/v1.2.3", nil)
	r2.Header.Set("X-Pika-Registry-Prefix", "/registries/default/docker-mirror")
	w2 := httptest.NewRecorder()
	rr.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("second fetch %d", w2.Code)
	}
	if fu.Hits() != hits1 {
		t.Fatalf("non-floating tag re-hit upstream after TTL: %d → %d", hits1, fu.Hits())
	}
}

// TestDockerRemote_FloatingTagHonoursTTL verifies the opposite: a
// tag in the floating set IS bounded by MutableTTL. A 1ms TTL
// guarantees the second fetch re-hits upstream.
func TestDockerRemote_FloatingTagHonoursTTL(t *testing.T) {
	fu := newFakeDockerUpstream()
	defer fu.Close()

	name := "lib/foo"
	manifest := []byte(`{"schemaVersion":2,"layers":[]}`)
	fu.ServeManifest(name, "latest", "application/vnd.oci.image.manifest.v1+json", manifest)

	rr := newDockerRemoteWith(t, fu.URL(), "1ms", nil)
	if !rr.isFloatingTag("latest") {
		t.Fatal("latest should be classified floating by default")
	}

	r := httptest.NewRequest(http.MethodGet, "/v2/"+name+"/manifests/latest", nil)
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/docker-mirror")
	w := httptest.NewRecorder()
	rr.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("first fetch %d", w.Code)
	}
	hits1 := fu.Hits()

	// Sleep past the 1ms TTL so the cachedFresh check fails.
	time.Sleep(5 * time.Millisecond)

	// TTL elapsed → floating tag must re-hit upstream.
	r2 := httptest.NewRequest(http.MethodGet, "/v2/"+name+"/manifests/latest", nil)
	r2.Header.Set("X-Pika-Registry-Prefix", "/registries/default/docker-mirror")
	w2 := httptest.NewRecorder()
	rr.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("second fetch %d", w2.Code)
	}
	if fu.Hits() == hits1 {
		t.Fatalf("floating tag did NOT re-hit upstream after TTL (hits unchanged at %d)", hits1)
	}
}

// TestDockerRemote_FloatingTagsCustomList verifies that the operator
// can replace the default list. Here we whitelist only "edge" — so
// "latest" (default floater) becomes non-floating in this repo.
func TestDockerRemote_FloatingTagsCustomList(t *testing.T) {
	rr := newDockerRemoteWith(t, "http://example", "5m", []string{"edge"})
	if !rr.isFloatingTag("edge") {
		t.Error("edge should be floating with custom list")
	}
	if !rr.isFloatingTag("Edge") {
		t.Error("classification must be case-insensitive")
	}
	if rr.isFloatingTag("latest") {
		t.Error("latest should NOT be floating when operator overrode the list")
	}
	if rr.isFloatingTag("v1.0.0") {
		t.Error("v1.0.0 should never be floating")
	}
}

// TestDockerRemote_FloatingTagsWildcard verifies that "*" in the
// list flips the all-tags-mutable switch (pre-FloatingTags behaviour).
func TestDockerRemote_FloatingTagsWildcard(t *testing.T) {
	rr := newDockerRemoteWith(t, "http://example", "5m", []string{"*"})
	if !rr.isFloatingTag("latest") {
		t.Error("wildcard should make latest floating")
	}
	if !rr.isFloatingTag("v1.2.3") {
		t.Error("wildcard should make v1.2.3 floating")
	}
	if !rr.isFloatingTag("any-weird-name") {
		t.Error("wildcard should make every tag floating")
	}
}

// TestDockerRemote_FloatingTagsEmptyExplicit verifies that an
// explicitly empty list (operator passed []string{}) is NOT the same
// as "no configuration": currently we treat an empty config-side
// list as nil (defaults applied). This test pins the contract so
// future refactors don't accidentally invert it.
func TestDockerRemote_FloatingTagsEmptyExplicit(t *testing.T) {
	rr := newDockerRemoteWith(t, "http://example", "5m", []string{})
	if !rr.isFloatingTag("latest") {
		t.Error("empty list should fall back to default floaters (latest)")
	}
}

// _ silences unused-import linters across optional refactors.
var _ = io.Copy
