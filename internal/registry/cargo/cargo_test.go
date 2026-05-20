package cargo

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
	repo := &service.RegistryRepository{Name: "cargo-local", Type: service.RegistryTypeCargo, Kind: service.RegistryKindLocal, Mount: "m", BasePath: "cargo", AllowPush: true}
	r, err := NewLocalFactory()(context.Background(), deps, "default", repo)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	return r.(*Local)
}

func do(l *Local, method, path string, body io.Reader) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, body)
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/cargo-local")
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	return w
}

func TestCargoLocalPublishIndexDetail(t *testing.T) {
	l := newTestLocal(t)
	w := do(l, http.MethodPut, "/api/v1/crates/demo/1.0.0/download", bytes.NewReader([]byte("crate-bytes")))
	if w.Code != http.StatusCreated {
		t.Fatalf("put status %d body %s", w.Code, w.Body.String())
	}
	w = do(l, http.MethodGet, "/config.json", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "api/v1/crates") {
		t.Fatalf("config status=%d body=%q", w.Code, w.Body.String())
	}
	w = do(l, http.MethodGet, "/"+indexPath("demo"), nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"vers":"1.0.0"`) {
		t.Fatalf("index status=%d body=%q", w.Code, w.Body.String())
	}
	w = do(l, http.MethodGet, "/api/v1/crates/demo/1.0.0/download", nil)
	if w.Code != http.StatusOK || w.Body.String() != "crate-bytes" {
		t.Fatalf("download status=%d body=%q", w.Code, w.Body.String())
	}
	detail, err := l.PackageDetail(context.Background(), "demo")
	if err != nil {
		t.Fatalf("PackageDetail: %v", err)
	}
	if detail.Cargo == nil || detail.Cargo.LatestVersion != "1.0.0" || len(detail.Cargo.Versions) != 1 {
		t.Fatalf("unexpected detail: %+v", detail)
	}
}
