package helm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/rawfs/localfs"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/service"
)

// newLocal builds a Local Helm registry at a fresh temp directory.
func newLocal(t *testing.T, allowPush bool) *Local {
	t.Helper()
	dir := t.TempDir()
	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("localfs: %v", err)
	}
	deps := registry.Deps{
		MountRawFS: func(string) (rawfs.RawFS, error) { return fs, nil },
	}
	repo := &service.RegistryRepository{
		Name: "helm-local", Type: service.RegistryTypeHelm, Kind: service.RegistryKindLocal,
		Mount: "m", BasePath: "helm", AllowPush: allowPush,
	}
	r, err := NewLocalFactory()(context.Background(), deps, "default", repo)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	return r.(*Local)
}

// buildChartTarball assembles a minimal valid Helm chart tarball
// with the given Chart.yaml fields and optional README.
func buildChartTarball(t *testing.T, name, version, description, readme string) []byte {
	t.Helper()
	chartYAML := "apiVersion: v2\nname: " + name + "\nversion: " + version + "\n"
	if description != "" {
		chartYAML += "description: " + description + "\n"
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: name + "/Chart.yaml",
		Mode: 0o644,
		Size: int64(len(chartYAML)),
	}); err != nil {
		t.Fatalf("tar hdr: %v", err)
	}
	_, _ = tw.Write([]byte(chartYAML))
	if readme != "" {
		_ = tw.WriteHeader(&tar.Header{
			Name: name + "/README.md",
			Mode: 0o644,
			Size: int64(len(readme)),
		})
		_, _ = tw.Write([]byte(readme))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestExtractChart_ParsesMetadataAndReadme(t *testing.T) {
	body := buildChartTarball(t, "mychart", "1.0.0", "a test chart", "# hello")
	ext, err := ExtractChart(body)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if ext.Metadata.Name != "mychart" {
		t.Errorf("name=%q", ext.Metadata.Name)
	}
	if ext.Metadata.Version != "1.0.0" {
		t.Errorf("version=%q", ext.Metadata.Version)
	}
	if ext.Metadata.Description != "a test chart" {
		t.Errorf("description=%q", ext.Metadata.Description)
	}
	if ext.Readme != "# hello" {
		t.Errorf("readme=%q", ext.Readme)
	}
	if ext.Digest == "" || len(ext.Digest) != 64 {
		t.Errorf("digest=%q", ext.Digest)
	}
}

func TestExtractChart_RejectsMissingChartYAML(t *testing.T) {
	// A tarball with no Chart.yaml — just a README.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "x/README.md", Mode: 0o644, Size: 5})
	_, _ = tw.Write([]byte("hello"))
	_ = tw.Close()
	_ = gz.Close()
	_, err := ExtractChart(buf.Bytes())
	if !errors.Is(err, ErrInvalidChart) {
		t.Errorf("err=%v want ErrInvalidChart", err)
	}
}

func TestLocal_PublishRawPUT_StoresAndReadsBack(t *testing.T) {
	l := newLocal(t, true)
	body := buildChartTarball(t, "mychart", "1.0.0", "desc", "# readme")
	// PUT /mychart-1.0.0.tgz
	req := httptest.NewRequest(http.MethodPut, "/mychart-1.0.0.tgz", bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	rec := httptest.NewRecorder()
	l.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish status=%d body=%s", rec.Code, rec.Body.String())
	}
	// GET /index.yaml — should contain "mychart" entry.
	req2 := httptest.NewRequest(http.MethodGet, "/index.yaml", nil)
	rec2 := httptest.NewRecorder()
	l.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("index status=%d", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "mychart") {
		t.Errorf("index missing mychart: %s", rec2.Body.String())
	}
	// GET /mychart-1.0.0.tgz — bytes should match what we pushed.
	req3 := httptest.NewRequest(http.MethodGet, "/mychart-1.0.0.tgz", nil)
	rec3 := httptest.NewRecorder()
	l.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("get tarball status=%d", rec3.Code)
	}
	if !bytes.Equal(rec3.Body.Bytes(), body) {
		t.Errorf("tarball body mismatch len=%d want %d", rec3.Body.Len(), len(body))
	}
}

func TestLocal_PackageDetail_HelmMetadata(t *testing.T) {
	l := newLocal(t, true)
	body := buildChartTarball(t, "mychart", "1.0.0", "useful chart", "# hi")
	req := httptest.NewRequest(http.MethodPut, "/mychart-1.0.0.tgz", bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	rec := httptest.NewRecorder()
	l.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish: %d", rec.Code)
	}
	det, err := l.PackageDetail(context.Background(), "mychart")
	if err != nil {
		t.Fatalf("PackageDetail: %v", err)
	}
	if det.Type != "helm" || det.Helm == nil {
		t.Fatalf("envelope: %+v", det)
	}
	if det.Helm.Description != "useful chart" {
		t.Errorf("description=%q", det.Helm.Description)
	}
	if det.Helm.LatestVersion != "1.0.0" {
		t.Errorf("latest=%q", det.Helm.LatestVersion)
	}
	if !det.Helm.HasReadme {
		t.Errorf("HasReadme=false")
	}
	if len(det.Helm.Versions) != 1 {
		t.Errorf("versions=%d", len(det.Helm.Versions))
	}
}

func TestLocal_PackageDetail_NotFound(t *testing.T) {
	l := newLocal(t, true)
	_, err := l.PackageDetail(context.Background(), "ghost")
	if !errors.Is(err, registry.ErrPackageNotFound) {
		t.Errorf("err=%v", err)
	}
}

func TestParseTarballFilename_HyphenInName(t *testing.T) {
	cases := []struct {
		in   string
		name string
		ver  string
	}{
		{"my-chart-1.0.0.tgz", "my-chart", "1.0.0"},
		{"app-2.1.3-beta.tgz", "app", "2.1.3-beta"},
		{"simple-0.0.1.tgz", "simple", "0.0.1"},
	}
	for _, c := range cases {
		name, ver, err := ParseTarballFilename(c.in)
		if err != nil {
			t.Errorf("%s: err=%v", c.in, err)
			continue
		}
		if name != c.name || ver != c.ver {
			t.Errorf("%s: got (%s,%s) want (%s,%s)", c.in, name, ver, c.name, c.ver)
		}
	}
}

func TestLocal_RejectsPublishWhenPushDisabled(t *testing.T) {
	l := newLocal(t, false)
	body := buildChartTarball(t, "x", "1.0.0", "", "")
	req := httptest.NewRequest(http.MethodPut, "/x-1.0.0.tgz", bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	rec := httptest.NewRecorder()
	l.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status=%d want 403", rec.Code)
	}
}

func TestLocal_DeleteVersion(t *testing.T) {
	l := newLocal(t, true)
	body := buildChartTarball(t, "del", "1.0.0", "", "")
	req := httptest.NewRequest(http.MethodPut, "/del-1.0.0.tgz", bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	rec := httptest.NewRecorder()
	l.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish: %d", rec.Code)
	}
	delReq := httptest.NewRequest(http.MethodDelete, "/api/charts/del/1.0.0", nil)
	delRec := httptest.NewRecorder()
	l.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d body %s", delRec.Code, delRec.Body.String())
	}
	if _, err := l.PackageDetail(context.Background(), "del"); !errors.Is(err, registry.ErrPackageNotFound) {
		t.Errorf("post-delete err=%v want NotFound", err)
	}
}

// _ keep base64 used by test setup in other helpers (matches the
// pattern from npm tests).
var _ = base64.StdEncoding.EncodeToString
