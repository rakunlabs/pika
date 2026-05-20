package pypi

import (
	"bytes"
	"context"
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

func newTestLocal(t *testing.T) *Local {
	t.Helper()
	fs, err := localfs.New(t.TempDir())
	if err != nil {
		t.Fatalf("localfs.New: %v", err)
	}
	deps := registry.Deps{MountRawFS: func(string) (rawfs.RawFS, error) { return fs, nil }}
	repo := &service.RegistryRepository{Name: "pypi-local", Type: service.RegistryTypePyPI, Kind: service.RegistryKindLocal, Mount: "m", BasePath: "pypi", AllowPush: true}
	r, err := NewLocalFactory()(context.Background(), deps, "default", repo)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	return r.(*Local)
}

func do(l *Local, method, path string, body io.Reader) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, body)
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/pypi-local")
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	return w
}

func TestPyPILocalPublishSimpleDetail(t *testing.T) {
	l := newTestLocal(t)
	w := do(l, http.MethodPut, "/packages/demo/demo-1.0.0.tar.gz", bytes.NewReader([]byte("sdist")))
	if w.Code != http.StatusCreated {
		t.Fatalf("put status %d body %s", w.Code, w.Body.String())
	}
	w = do(l, http.MethodGet, "/simple/demo/", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "demo-1.0.0.tar.gz") {
		t.Fatalf("simple status=%d body=%q", w.Code, w.Body.String())
	}
	w = do(l, http.MethodGet, "/packages/demo/demo-1.0.0.tar.gz", nil)
	if w.Code != http.StatusOK || w.Body.String() != "sdist" {
		t.Fatalf("file status=%d body=%q", w.Code, w.Body.String())
	}
	detail, err := l.PackageDetail(context.Background(), "demo")
	if err != nil {
		t.Fatalf("PackageDetail: %v", err)
	}
	if detail.PyPI == nil || detail.PyPI.LatestVersion != "1.0.0" || len(detail.PyPI.Versions) != 1 {
		t.Fatalf("unexpected detail: %+v", detail)
	}
}
