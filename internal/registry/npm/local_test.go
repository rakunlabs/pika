package npm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/rawfs/localfs"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/service"
)

func newNPMLocal(t *testing.T, allowPush bool) *Local {
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
		Name: "npm-local", Type: service.RegistryTypeNPM, Kind: service.RegistryKindLocal,
		Mount: "m", BasePath: "npm", AllowPush: allowPush,
	}
	r, err := NewLocalFactory()(context.Background(), deps, "default", repo)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	return r.(*Local)
}

// buildPublishPayload assembles a minimal valid `npm publish` body
// for the given (name, version, tarballBytes).
func buildPublishPayload(name, version string, tarball []byte) []byte {
	filename := strings.TrimPrefix(name, "@")
	filename = strings.ReplaceAll(filename, "/", "-")
	filename = filename + "-" + version + ".tgz"

	body := map[string]any{
		"name": name,
		"versions": map[string]any{
			version: map[string]any{
				"name":        name,
				"version":     version,
				"description": "test package " + name,
				"dist": map[string]any{
					"tarball": "https://example.com/" + filename,
				},
			},
		},
		"_attachments": map[string]any{
			filename: map[string]any{
				"content_type": "application/octet-stream",
				"data":         base64.StdEncoding.EncodeToString(tarball),
				"length":       len(tarball),
			},
		},
		"dist-tags": map[string]string{"latest": version},
		"readme":    "# " + name,
	}
	out, _ := json.Marshal(body)
	return out
}

func publishVersion(t *testing.T, l *Local, name, version string, tarball []byte) {
	t.Helper()
	body := buildPublishPayload(name, version, tarball)
	r := httptest.NewRequest(http.MethodPut, "/"+name, bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/npm-local")
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("publish %s@%s status %d body %s", name, version, w.Code, w.Body.String())
	}
}

func TestNPMLocal_FactoryProducesValidRegistry(t *testing.T) {
	l := newNPMLocal(t, true)
	if l.Type() != "npm" || l.Kind() != "local" {
		t.Errorf("type=%s kind=%s", l.Type(), l.Kind())
	}
	if !l.AllowPush() {
		t.Errorf("push should be enabled")
	}
}

func TestNPMLocal_PublishThenGetPackument(t *testing.T) {
	l := newNPMLocal(t, true)
	publishVersion(t, l, "lodash", "1.0.0", []byte("fake-tarball-bytes"))

	r := httptest.NewRequest(http.MethodGet, "/lodash", nil)
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("packument status %d", w.Code)
	}
	var pkg map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &pkg); err != nil {
		t.Fatalf("decode packument: %v", err)
	}
	if pkg["name"] != "lodash" {
		t.Fatalf("name=%v", pkg["name"])
	}
	versions, ok := pkg["versions"].(map[string]any)
	if !ok || versions["1.0.0"] == nil {
		t.Fatalf("versions=%v", pkg["versions"])
	}
	tags, _ := pkg["dist-tags"].(map[string]any)
	if tags["latest"] != "1.0.0" {
		t.Fatalf("latest=%v", tags["latest"])
	}
}

func TestNPMLocal_PublishThenGetTarball(t *testing.T) {
	l := newNPMLocal(t, true)
	tarball := []byte("THIS-IS-THE-TARBALL")
	publishVersion(t, l, "mypkg", "1.0.0", tarball)

	r := httptest.NewRequest(http.MethodGet, "/mypkg/-/mypkg-1.0.0.tgz", nil)
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("tarball status %d", w.Code)
	}
	got, _ := io.ReadAll(w.Body)
	if !bytes.Equal(got, tarball) {
		t.Fatalf("tarball mismatch: got %q want %q", got, tarball)
	}
}

func TestNPMStore_DeleteVersionRemovesMetadataAndTags(t *testing.T) {
	l := newNPMLocal(t, true)
	publishVersion(t, l, "mypkg", "1.0.0", []byte("v1"))
	publishVersion(t, l, "mypkg", "2.0.0", []byte("v2"))

	if err := l.store.DeleteVersion("mypkg", "2.0.0"); err != nil {
		t.Fatalf("DeleteVersion: %v", err)
	}
	versions, err := l.store.ListVersions("mypkg")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 1 || versions[0] != "1.0.0" {
		t.Fatalf("versions after delete = %v", versions)
	}
	tags, err := l.store.ReadDistTags("mypkg")
	if err != nil {
		t.Fatalf("ReadDistTags: %v", err)
	}
	if _, ok := tags["latest"]; ok {
		t.Fatalf("latest tag should be removed after deleting tagged version: %v", tags)
	}

	if err := l.store.DeleteVersion("mypkg", "1.0.0"); err != nil {
		t.Fatalf("DeleteVersion last: %v", err)
	}
	packages, err := l.store.ListPackages()
	if err != nil {
		t.Fatalf("ListPackages: %v", err)
	}
	if len(packages) != 0 {
		t.Fatalf("empty package should not list after last version delete: %v", packages)
	}
}

func TestNPMLocal_RejectsRepublish(t *testing.T) {
	l := newNPMLocal(t, true)
	publishVersion(t, l, "lodash", "1.0.0", []byte("v1"))

	body := buildPublishPayload("lodash", "1.0.0", []byte("v1-again"))
	r := httptest.NewRequest(http.MethodPut, "/lodash", bytes.NewReader(body))
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/npm-local")
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 on republish, got %d", w.Code)
	}
}

func TestNPMLocal_PublishRejectedWhenDisabled(t *testing.T) {
	l := newNPMLocal(t, false)
	body := buildPublishPayload("lodash", "1.0.0", []byte("v1"))
	r := httptest.NewRequest(http.MethodPut, "/lodash", bytes.NewReader(body))
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 when push disabled, got %d", w.Code)
	}
}

func TestNPMLocal_PublishWithMismatchedName(t *testing.T) {
	l := newNPMLocal(t, true)
	body := buildPublishPayload("lodash", "1.0.0", []byte("v1"))
	// URL says "other" but body says "lodash" — server must reject.
	r := httptest.NewRequest(http.MethodPut, "/other", bytes.NewReader(body))
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body %s", w.Code, w.Body.String())
	}
}

func TestNPMLocal_PublishWithIntegrityHeader(t *testing.T) {
	l := newNPMLocal(t, true)
	tarball := []byte("payload-with-integrity")
	integrity, _ := SubresourceIntegrity(tarball, "sha512")

	// Build a payload that includes the correct integrity field.
	name, version := "secure-pkg", "1.0.0"
	filename := name + "-" + version + ".tgz"
	bodyMap := map[string]any{
		"name": name,
		"versions": map[string]any{
			version: map[string]any{
				"name":    name,
				"version": version,
				"dist": map[string]any{
					"tarball":   "https://example.com/" + filename,
					"integrity": integrity,
				},
			},
		},
		"_attachments": map[string]any{
			filename: map[string]any{
				"content_type": "application/octet-stream",
				"data":         base64.StdEncoding.EncodeToString(tarball),
				"length":       len(tarball),
			},
		},
	}
	body, _ := json.Marshal(bodyMap)
	r := httptest.NewRequest(http.MethodPut, "/"+name, bytes.NewReader(body))
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/npm-local")
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestNPMLocal_PublishWithBadIntegrity(t *testing.T) {
	l := newNPMLocal(t, true)
	tarball := []byte("payload")
	name, version := "secure-pkg", "1.0.0"
	filename := name + "-" + version + ".tgz"
	bodyMap := map[string]any{
		"name": name,
		"versions": map[string]any{
			version: map[string]any{
				"name":    name,
				"version": version,
				"dist": map[string]any{
					"tarball":   "https://example.com/" + filename,
					"integrity": "sha512-WRONG",
				},
			},
		},
		"_attachments": map[string]any{
			filename: map[string]any{
				"content_type": "application/octet-stream",
				"data":         base64.StdEncoding.EncodeToString(tarball),
				"length":       len(tarball),
			},
		},
	}
	body, _ := json.Marshal(bodyMap)
	r := httptest.NewRequest(http.MethodPut, "/"+name, bytes.NewReader(body))
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on integrity mismatch, got %d body %s", w.Code, w.Body.String())
	}
}

func TestNPMLocal_GetTarballRewritesURL(t *testing.T) {
	// The published metadata's dist.tarball URL should point at
	// pika, not the original example.com URL.
	l := newNPMLocal(t, true)
	publishVersion(t, l, "lodash", "1.0.0", []byte("tarball"))

	r := httptest.NewRequest(http.MethodGet, "/lodash", nil)
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var pkg map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &pkg)
	versions := pkg["versions"].(map[string]any)
	v100 := versions["1.0.0"].(map[string]any)
	dist := v100["dist"].(map[string]any)
	url, _ := dist["tarball"].(string)
	if !strings.Contains(url, "/registries/default/npm-local/lodash/-/") {
		t.Fatalf("expected rewritten tarball URL, got %q", url)
	}
}

func TestNPMLocal_DistTags(t *testing.T) {
	l := newNPMLocal(t, true)
	publishVersion(t, l, "lodash", "1.0.0", []byte("v1"))
	publishVersion(t, l, "lodash", "2.0.0-beta.1", []byte("v2"))

	// GET dist-tags
	{
		r := httptest.NewRequest(http.MethodGet, "/-/package/lodash/dist-tags", nil)
		w := httptest.NewRecorder()
		l.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("get dist-tags status %d", w.Code)
		}
		var tags map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &tags)
		// latest was set on first publish to 1.0.0; second publish
		// payload also sends "latest":"2.0.0-beta.1" which merges in.
		if tags["latest"] != "2.0.0-beta.1" {
			t.Fatalf("latest=%q", tags["latest"])
		}
	}

	// PUT next dist-tag.
	{
		body := strings.NewReader(`"1.0.0"`)
		r := httptest.NewRequest(http.MethodPut, "/-/package/lodash/dist-tags/stable", body)
		w := httptest.NewRecorder()
		l.ServeHTTP(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("set dist-tag status %d body %s", w.Code, w.Body.String())
		}
	}

	// DELETE dist-tag.
	{
		r := httptest.NewRequest(http.MethodDelete, "/-/package/lodash/dist-tags/stable", nil)
		w := httptest.NewRecorder()
		l.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("delete dist-tag status %d", w.Code)
		}
	}

	// DELETE latest is forbidden.
	{
		r := httptest.NewRequest(http.MethodDelete, "/-/package/lodash/dist-tags/latest", nil)
		w := httptest.NewRecorder()
		l.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("delete latest: expected 400, got %d", w.Code)
		}
	}
}

func TestNPMLocal_Search(t *testing.T) {
	l := newNPMLocal(t, true)
	publishVersion(t, l, "lodash", "1.0.0", []byte("a"))
	publishVersion(t, l, "underscore", "1.0.0", []byte("b"))

	r := httptest.NewRequest(http.MethodGet, "/-/v1/search?text=lod", nil)
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("search status %d", w.Code)
	}
	var resp struct {
		Objects []struct {
			Package map[string]any `json:"package"`
		} `json:"objects"`
		Total int `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Total != 1 {
		t.Fatalf("expected 1 result, got %d (%v)", resp.Total, resp.Objects)
	}
	if resp.Objects[0].Package["name"] != "lodash" {
		t.Fatalf("wrong hit: %v", resp.Objects[0].Package)
	}
}

func TestNPMLocal_Whoami(t *testing.T) {
	l := newNPMLocal(t, true)
	r := httptest.NewRequest(http.MethodGet, "/-/whoami", nil)
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "username") {
		t.Fatalf("body %q", w.Body.String())
	}
}

func TestNPMLocal_ScopedPackage(t *testing.T) {
	l := newNPMLocal(t, true)
	publishVersion(t, l, "@scope/pkg", "1.0.0", []byte("tarball-bytes"))

	// Packument: /@scope/pkg
	r := httptest.NewRequest(http.MethodGet, "/@scope/pkg", nil)
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var pkg map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &pkg)
	if pkg["name"] != "@scope/pkg" {
		t.Fatalf("name=%v", pkg["name"])
	}

	// Tarball lookup: /@scope/pkg/-/{file}.tgz — the original payload
	// filename used the scope-flattened form.
	r2 := httptest.NewRequest(http.MethodGet, "/@scope/pkg/-/scope-pkg-1.0.0.tgz", nil)
	w2 := httptest.NewRecorder()
	l.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("scoped tarball status %d body %s", w2.Code, w2.Body.String())
	}
}

func TestNPMLocal_UnknownRoute(t *testing.T) {
	l := newNPMLocal(t, true)
	r := httptest.NewRequest(http.MethodGet, "/-/something-unsupported", nil)
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d", w.Code)
	}
}
