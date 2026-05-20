package maven

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
	repo := &service.RegistryRepository{Name: "maven-local", Type: service.RegistryTypeMaven, Kind: service.RegistryKindLocal, Mount: "m", BasePath: "maven", AllowPush: true}
	r, err := NewLocalFactory()(context.Background(), deps, "default", repo)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	return r.(*Local)
}

func do(l *Local, method, path string, body io.Reader) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, body)
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/maven-local")
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	return w
}

func TestMavenLocalPublishListDetail(t *testing.T) {
	l := newTestLocal(t)
	jarPath := "/com/example/app/1.0.0/app-1.0.0.jar"
	w := do(l, http.MethodPut, jarPath, bytes.NewReader([]byte("jar-bytes")))
	if w.Code != http.StatusCreated {
		t.Fatalf("put jar status %d body %s", w.Code, w.Body.String())
	}
	w = do(l, http.MethodGet, jarPath, nil)
	if w.Code != http.StatusOK || w.Body.String() != "jar-bytes" {
		t.Fatalf("get jar status=%d body=%q", w.Code, w.Body.String())
	}
	arts, err := l.store.ListArtifacts()
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(arts) != 1 || arts[0].GroupID != "com.example" || arts[0].ArtifactID != "app" || len(arts[0].Versions) != 1 || arts[0].Versions[0] != "1.0.0" {
		t.Fatalf("unexpected artifacts: %+v", arts)
	}
	detail, err := l.PackageDetail(context.Background(), "com.example:app")
	if err != nil {
		t.Fatalf("PackageDetail: %v", err)
	}
	if detail.Maven == nil || detail.Maven.LatestVersion != "1.0.0" || detail.Maven.Versions[0].JarSize != int64(len("jar-bytes")) {
		t.Fatalf("unexpected detail: %+v", detail)
	}
}
