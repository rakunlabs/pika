package npm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/registry"
)

// buildPublishPayloadNoReadme produces a publish body with no
// top-level `readme` field, so the server's tarball-fallback path
// is exercised.
func buildPublishPayloadNoReadme(name, version string, tarball []byte) []byte {
	filename := strings.TrimPrefix(name, "@")
	filename = strings.ReplaceAll(filename, "/", "-") + "-" + version + ".tgz"
	body := map[string]any{
		"name": name,
		"versions": map[string]any{
			version: map[string]any{
				"name":    name,
				"version": version,
				"dist":    map[string]any{"tarball": "https://example.com/" + filename},
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
	}
	out, _ := json.Marshal(body)
	return out
}

func newPublishRequest(_ *testing.T, name string, body []byte) *http.Request {
	r := httptest.NewRequest(http.MethodPut, "/"+name, bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/npm-local")
	return r
}

func newRecorder() *httptest.ResponseRecorder {
	return httptest.NewRecorder()
}

// buildTarballWithReadme constructs a minimal gzipped tar with a
// "package/README.md" entry holding `readme`. Used to verify the
// lazy extractor recovers the README from a real tarball.
func buildTarballWithReadme(t *testing.T, readme string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// Random package.json — content-irrelevant but matches npm
	// convention so the tarball looks plausible.
	pkgJSON := `{"name":"x","version":"1.0.0"}`
	if err := tw.WriteHeader(&tar.Header{
		Name: "package/package.json",
		Mode: 0o644,
		Size: int64(len(pkgJSON)),
	}); err != nil {
		t.Fatalf("write hdr pkg.json: %v", err)
	}
	if _, err := tw.Write([]byte(pkgJSON)); err != nil {
		t.Fatalf("write pkg.json: %v", err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: "package/README.md",
		Mode: 0o644,
		Size: int64(len(readme)),
	}); err != nil {
		t.Fatalf("write hdr readme: %v", err)
	}
	if _, err := tw.Write([]byte(readme)); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gz: %v", err)
	}
	return buf.Bytes()
}

// TestNPMLocal_PackageDetail_FullMetadata verifies the detail
// payload surfaces description, dist-tags, integrity, etc. across
// multiple versions.
func TestNPMLocal_PackageDetail_FullMetadata(t *testing.T) {
	l := newNPMLocal(t, true)
	tarballV1 := []byte("FAKETARBALLV1")
	tarballV2 := []byte("FAKETARBALLV2")
	publishVersion(t, l, "lodash", "1.0.0", tarballV1)
	publishVersion(t, l, "lodash", "2.0.0", tarballV2)
	det, err := l.PackageDetail(context.Background(), "lodash")
	if err != nil {
		t.Fatalf("PackageDetail: %v", err)
	}
	if det.Type != "npm" || det.Name != "lodash" {
		t.Fatalf("envelope: %+v", det)
	}
	if det.NPM == nil {
		t.Fatalf("nil NPM")
	}
	if det.NPM.LatestVersion != "2.0.0" {
		t.Errorf("latest=%q want 2.0.0", det.NPM.LatestVersion)
	}
	if det.NPM.Description != "test package lodash" {
		t.Errorf("description=%q", det.NPM.Description)
	}
	if len(det.NPM.Versions) != 2 {
		t.Fatalf("versions=%d", len(det.NPM.Versions))
	}
	if det.NPM.Versions[0].Version != "2.0.0" {
		t.Errorf("first=%q want 2.0.0", det.NPM.Versions[0].Version)
	}
	// Integrity was synthesised at publish time.
	if det.NPM.Versions[0].Integrity == "" {
		t.Errorf("integrity empty")
	}
	if det.NPM.DistTags["latest"] != "2.0.0" {
		t.Errorf("dist_tags[latest]=%q", det.NPM.DistTags["latest"])
	}
	if det.NPM.Versions[0].PublishedAt == "" {
		t.Errorf("published_at empty")
	}
}

func TestNPMLocal_PackageDetail_NotFound(t *testing.T) {
	l := newNPMLocal(t, true)
	_, err := l.PackageDetail(context.Background(), "ghost")
	if !errors.Is(err, registry.ErrPackageNotFound) {
		t.Errorf("err=%v want ErrPackageNotFound", err)
	}
}

// TestNPMLocal_PackageDetail_EmptyNameIsInvalid verifies that an
// empty caller-supplied name maps to ErrInvalidPackageName (400)
// rather than ErrPackageNotFound (404). The handler dispatches on
// these sentinels to surface a useful HTTP status.
func TestNPMLocal_PackageDetail_EmptyNameIsInvalid(t *testing.T) {
	l := newNPMLocal(t, true)
	_, err := l.PackageDetail(context.Background(), "")
	if !errors.Is(err, registry.ErrInvalidPackageName) {
		t.Errorf("err=%v want ErrInvalidPackageName", err)
	}
	if errors.Is(err, registry.ErrPackageNotFound) {
		t.Errorf("err=%v must not match ErrPackageNotFound (would 404 instead of 400)", err)
	}
}

// TestExtractReadmeFromTarball verifies the lazy extractor pulls
// README.md from the canonical npm tarball layout.
func TestExtractReadmeFromTarball(t *testing.T) {
	body := buildTarballWithReadme(t, "# hello\nworld")
	got, err := extractReadmeFromTarball(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got != "# hello\nworld" {
		t.Errorf("readme=%q", got)
	}
}

// TestExtractReadmeFromTarball_NoReadme verifies the empty-result
// path: a tarball without a README returns "" without error.
func TestExtractReadmeFromTarball_NoReadme(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	pkgJSON := `{"name":"x","version":"1.0.0"}`
	_ = tw.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0o644, Size: int64(len(pkgJSON))})
	_, _ = tw.Write([]byte(pkgJSON))
	_ = tw.Close()
	_ = gz.Close()
	got, err := extractReadmeFromTarball(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty README, got %q", got)
	}
}

// TestPublishExtractsReadmeFromTarballWhenPayloadEmpty verifies
// that a publish whose payload has no `readme` field still ends up
// with a cached README — extracted from the tarball at publish
// time (B2 fallback path).
func TestPublishExtractsReadmeFromTarballWhenPayloadEmpty(t *testing.T) {
	l := newNPMLocal(t, true)
	// Build a tarball with a README inside but a publish payload
	// that omits the top-level `readme` field.
	tarball := buildTarballWithReadme(t, "# extracted-from-tar")
	// Hand-craft the payload (the helper above ALWAYS sets readme,
	// so we duplicate the relevant parts here without that field).
	import_publish := struct{}{}
	_ = import_publish
	// Use a minimal payload with readme=""
	payload := buildPublishPayloadNoReadme("emptypkg", "1.0.0", tarball)
	req := newPublishRequest(t, "emptypkg", payload)
	rec := newRecorder()
	l.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("publish status=%d body=%s", rec.Code, rec.Body.String())
	}
	cached, _ := l.store.ReadReadme("emptypkg")
	if cached != "# extracted-from-tar" {
		t.Errorf("cached readme=%q want '# extracted-from-tar'", cached)
	}
}

// TestStoreLazyExtractReadme_CachesAfterExtract runs the full
// lazy-extract path through the store, then confirms a second
// read hits the cache (ReadReadme returns content without
// extraction).
func TestStoreLazyExtractReadme_CachesAfterExtract(t *testing.T) {
	l := newNPMLocal(t, true)
	// Build a real .tgz with README content, then publish via the
	// store API path (bypass the publish handler so we can pre-load
	// a specific tarball body).
	body := buildTarballWithReadme(t, "# cached")
	publishVersion(t, l, "cachepkg", "1.0.0", body)
	// Clear the cached README (publish wrote a different one from
	// the payload's `readme` field) and re-extract from tarball.
	_ = l.store.WriteReadme("cachepkg", "")
	got, err := l.store.LazyExtractReadme("cachepkg", "cachepkg-1.0.0.tgz")
	if err != nil {
		t.Fatalf("lazy extract: %v", err)
	}
	if got != "# cached" {
		t.Errorf("extracted=%q want '# cached'", got)
	}
	// Cache should now hold the extracted body.
	cached, _ := l.store.ReadReadme("cachepkg")
	if cached != "# cached" {
		t.Errorf("cache=%q want '# cached'", cached)
	}
}
