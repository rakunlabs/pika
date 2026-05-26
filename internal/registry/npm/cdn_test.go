package npm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/registry"
)

func buildTarballWithFiles(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatalf("write header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("write body %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func TestParseCDNAssetPath(t *testing.T) {
	tests := []struct {
		path    string
		want    registry.CDNAssetRequest
		wantErr bool
	}{
		{
			path: "lodash@4.17.21/lodash.js",
			want: registry.CDNAssetRequest{Package: "lodash", Version: "4.17.21", Path: "lodash.js"},
		},
		{
			path: "/@scope/pkg@1.2.3/dist/index.js",
			want: registry.CDNAssetRequest{Package: "@scope/pkg", Version: "1.2.3", Path: "dist/index.js"},
		},
		{
			path: "@scope/pkg/dist/index.js",
			want: registry.CDNAssetRequest{Package: "@scope/pkg", Path: "dist/index.js"},
		},
		{path: "lodash@4.17.21/../x.js", wantErr: true},
		{path: "lodash@4.17.21", wantErr: true},
	}
	for _, tt := range tests {
		got, err := ParseCDNAssetPath(tt.path)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("ParseCDNAssetPath(%q) expected error", tt.path)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseCDNAssetPath(%q): %v", tt.path, err)
		}
		if got != tt.want {
			t.Fatalf("ParseCDNAssetPath(%q) = %+v, want %+v", tt.path, got, tt.want)
		}
	}
}

func TestNPMLocal_ServeCDNAsset(t *testing.T) {
	l := newNPMLocal(t, true)
	tarball := buildTarballWithFiles(t, map[string]string{
		"package/package.json":  `{"name":"widget","version":"1.0.0"}`,
		"package/dist/index.js": `export const answer = 42;`,
	})
	publishVersion(t, l, "widget", "1.0.0", tarball)

	asset, err := ParseCDNAssetPath("widget@1.0.0/dist/index.js")
	if err != nil {
		t.Fatalf("parse asset: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/cdn", nil)
	l.ServeCDNAsset(rec, req, asset)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "export const answer = 42;" {
		t.Fatalf("body=%q", rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("Cache-Control=%q", cc)
	}

	asset, err = ParseCDNAssetPath("widget/dist/index.js")
	if err != nil {
		t.Fatalf("parse latest asset: %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodHead, "/cdn", nil)
	l.ServeCDNAsset(rec, req, asset)
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD wrote body %q", rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "must-revalidate") {
		t.Fatalf("latest Cache-Control=%q", cc)
	}
}
