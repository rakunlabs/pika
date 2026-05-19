package docker

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/service"
)

// stubResolver — implements virtualbase.Resolver for tests.
type stubResolver struct {
	regs map[string]registry.Registry
}

func (s *stubResolver) Lookup(_, repo string) (registry.Registry, bool) {
	r, ok := s.regs[repo]
	return r, ok
}

func newVirtual(t *testing.T, members []string, resolver *stubResolver) *Virtual {
	t.Helper()
	repo := &service.RegistryRepository{
		Name: "v", Type: service.RegistryTypeDocker, Kind: service.RegistryKindVirtual,
		Members: members,
	}
	r, err := NewVirtualFactory(resolver)(nil, registry.Deps{}, "default", repo)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	return r.(*Virtual)
}

func TestDockerVirtual_FactoryRequiresMembers(t *testing.T) {
	_, err := NewVirtualFactory(&stubResolver{})(nil, registry.Deps{},
		"ns", &service.RegistryRepository{Name: "v", Type: "docker", Kind: "virtual"})
	if err == nil {
		t.Fatal("expected error when members empty")
	}
}

func TestDockerVirtual_VersionProbe(t *testing.T) {
	v := newVirtual(t, []string{"a"}, &stubResolver{regs: map[string]registry.Registry{}})
	r := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
}

func TestDockerVirtual_RejectsWrite(t *testing.T) {
	v := newVirtual(t, []string{"a"}, &stubResolver{regs: map[string]registry.Registry{}})
	r := httptest.NewRequest(http.MethodPut, "/v2/lib/foo/manifests/latest", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestDockerVirtual_ManifestFirstHit(t *testing.T) {
	// Two Local members, both with image "lib/foo:latest". First
	// member's response wins.
	a := newDockerLocal(t, true)
	b := newDockerLocal(t, true)

	manifestA := []byte(`{"schemaVersion":2,"layers":[],"_from":"A"}`)
	manifestB := []byte(`{"schemaVersion":2,"layers":[],"_from":"B"}`)
	pushManifestBody(t, a, "lib/foo", "latest", manifestA, "application/vnd.oci.image.manifest.v1+json")
	pushManifestBody(t, b, "lib/foo", "latest", manifestB, "application/vnd.oci.image.manifest.v1+json")

	resolver := &stubResolver{regs: map[string]registry.Registry{"a": a, "b": b}}
	v := newVirtual(t, []string{"a", "b"}, resolver)

	r := httptest.NewRequest(http.MethodGet, "/v2/lib/foo/manifests/latest", nil)
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/v")
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"_from":"A"`)) {
		t.Fatalf("first member should win, got %q", w.Body.String())
	}
}

func TestDockerVirtual_CatalogUnion(t *testing.T) {
	a := newDockerLocal(t, true)
	b := newDockerLocal(t, true)

	pushManifestBody(t, a, "lib/alpha", "v1", []byte(`{"schemaVersion":2}`), "application/vnd.oci.image.manifest.v1+json")
	pushManifestBody(t, b, "lib/beta", "v1", []byte(`{"schemaVersion":2}`), "application/vnd.oci.image.manifest.v1+json")
	// Overlap: both have lib/shared, which should dedupe.
	pushManifestBody(t, a, "lib/shared", "v1", []byte(`{"schemaVersion":2}`), "application/vnd.oci.image.manifest.v1+json")
	pushManifestBody(t, b, "lib/shared", "v1", []byte(`{"schemaVersion":2}`), "application/vnd.oci.image.manifest.v1+json")

	resolver := &stubResolver{regs: map[string]registry.Registry{"a": a, "b": b}}
	v := newVirtual(t, []string{"a", "b"}, resolver)

	r := httptest.NewRequest(http.MethodGet, "/v2/_catalog", nil)
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var resp struct {
		Repositories []string `json:"repositories"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	want := map[string]bool{"lib/alpha": true, "lib/beta": true, "lib/shared": true}
	if len(resp.Repositories) != 3 {
		t.Fatalf("expected 3 unique repos, got %d (%v)", len(resp.Repositories), resp.Repositories)
	}
	for _, r := range resp.Repositories {
		if !want[r] {
			t.Errorf("unexpected repo %q", r)
		}
	}
}

func TestDockerVirtual_TagsUnion(t *testing.T) {
	a := newDockerLocal(t, true)
	b := newDockerLocal(t, true)

	pushManifestBody(t, a, "lib/foo", "v1.0", []byte(`{"schemaVersion":2}`), "application/vnd.oci.image.manifest.v1+json")
	pushManifestBody(t, b, "lib/foo", "v2.0", []byte(`{"schemaVersion":2}`), "application/vnd.oci.image.manifest.v1+json")
	pushManifestBody(t, b, "lib/foo", "v1.0", []byte(`{"schemaVersion":2}`), "application/vnd.oci.image.manifest.v1+json") // overlap

	resolver := &stubResolver{regs: map[string]registry.Registry{"a": a, "b": b}}
	v := newVirtual(t, []string{"a", "b"}, resolver)

	r := httptest.NewRequest(http.MethodGet, "/v2/lib/foo/tags/list", nil)
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var resp struct {
		Tags []string `json:"tags"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	want := map[string]bool{"v1.0": true, "v2.0": true}
	if len(resp.Tags) != 2 {
		t.Fatalf("expected 2 unique tags, got %d (%v)", len(resp.Tags), resp.Tags)
	}
	for _, ttag := range resp.Tags {
		if !want[ttag] {
			t.Errorf("unexpected tag %q", ttag)
		}
	}
}

func TestDockerVirtual_AllMembersMiss(t *testing.T) {
	a := newDockerLocal(t, true)
	resolver := &stubResolver{regs: map[string]registry.Registry{"a": a}}
	v := newVirtual(t, []string{"a"}, resolver)
	r := httptest.NewRequest(http.MethodGet, "/v2/missing/img/manifests/v1", nil)
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestDockerVirtual_ReferrersUnion(t *testing.T) {
	a := newDockerLocal(t, true)
	b := newDockerLocal(t, true)
	name := "lib/foo"

	subjectBody := []byte(`{"schemaVersion":2}`)
	dgst := sha256.Sum256(subjectBody)
	subjectDigest := "sha256:" + hex.EncodeToString(dgst[:])

	// A holds a cosign signature for the subject.
	cosignBody := []byte(`{
		"schemaVersion":2,
		"artifactType":"application/vnd.dev.cosign.simplesigning.v1+json",
		"subject":{"digest":"` + subjectDigest + `","size":2}
	}`)
	pushManifestBody(t, a, name, "v1.0.0", subjectBody, "application/vnd.oci.image.manifest.v1+json")
	pushManifestBody(t, a, name, digestOf(cosignBody), cosignBody, "application/vnd.oci.image.manifest.v1+json")

	// B holds an SBOM for the same subject.
	sbomBody := []byte(`{
		"schemaVersion":2,
		"artifactType":"application/spdx+json",
		"subject":{"digest":"` + subjectDigest + `","size":2}
	}`)
	pushManifestBody(t, b, name, "v1.0.0", subjectBody, "application/vnd.oci.image.manifest.v1+json")
	pushManifestBody(t, b, name, digestOf(sbomBody), sbomBody, "application/vnd.oci.image.manifest.v1+json")

	resolver := &stubResolver{regs: map[string]registry.Registry{"a": a, "b": b}}
	v := newVirtual(t, []string{"a", "b"}, resolver)

	r := httptest.NewRequest(http.MethodGet, "/v2/"+name+"/referrers/"+subjectDigest, nil)
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var idx ociImageIndex
	_ = json.Unmarshal(w.Body.Bytes(), &idx)
	if len(idx.Manifests) != 2 {
		t.Fatalf("expected 2 unioned referrers, got %d", len(idx.Manifests))
	}
}
