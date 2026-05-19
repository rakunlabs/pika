package goproxy

import (
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

// stubResolver is a virtualResolver that returns canned registries
// by name. Tests build the chain explicitly so they don't depend on
// the real manager.
type stubResolver struct {
	regs map[string]registry.Registry
}

func (s *stubResolver) Lookup(_, repo string) (registry.Registry, bool) {
	r, ok := s.regs[repo]
	return r, ok
}

func newVirtual(t *testing.T, members []string, resolver virtualResolver) *Virtual {
	t.Helper()
	repo := &service.RegistryRepository{
		Name:    "go-virt",
		Type:    service.RegistryTypeGo,
		Kind:    service.RegistryKindVirtual,
		Members: members,
	}
	r, err := NewVirtualFactory(resolver)(context.Background(), registry.Deps{}, "default", repo)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	return r.(*Virtual)
}

// localWithVersion returns a Local Registry that already has one
// version pre-loaded under modulePath/version. Useful for building
// member chains quickly.
func localWithVersion(t *testing.T, mod, version, modBody string) *Local {
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
		Name: "local-x", Type: service.RegistryTypeGo, Kind: service.RegistryKindLocal,
		Mount: "m", AllowPush: true,
	}
	r, err := NewLocalFactory()(context.Background(), deps, "default", repo)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	l := r.(*Local)
	uploadInfo(t, l, mod, version)
	uploadMod(t, l, mod, version, modBody)
	return l
}

func TestVirtual_FactoryRequiresMembers(t *testing.T) {
	repo := &service.RegistryRepository{
		Name: "v", Type: service.RegistryTypeGo, Kind: service.RegistryKindVirtual,
	}
	_, err := NewVirtualFactory(&stubResolver{})(context.Background(), registry.Deps{}, "ns", repo)
	if err == nil {
		t.Fatal("expected error when members are empty")
	}
}

func TestVirtual_FirstHitWins(t *testing.T) {
	mod := "github.com/foo/bar"
	encoded := EncodeModulePath(mod)

	a := localWithVersion(t, mod, "v1.0.0", "module github.com/foo/bar (from A)")
	b := localWithVersion(t, mod, "v1.0.0", "module github.com/foo/bar (from B)")

	resolver := &stubResolver{regs: map[string]registry.Registry{
		"a": a, "b": b,
	}}
	v := newVirtual(t, []string{"a", "b"}, resolver)

	r := httptest.NewRequest(http.MethodGet, "/"+encoded+"/@v/v1.0.0.mod", nil)
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body, _ := io.ReadAll(w.Body)
	if !strings.Contains(string(body), "from A") {
		t.Fatalf("expected first member (A) to win, got %q", body)
	}
}

func TestVirtual_FallthroughOnMiss(t *testing.T) {
	mod := "github.com/foo/bar"
	encoded := EncodeModulePath(mod)

	// First member has no versions; second has v1.0.0.
	dir := t.TempDir()
	fs, _ := localfs.New(dir)
	deps := registry.Deps{MountRawFS: func(string) (rawfs.RawFS, error) { return fs, nil }}
	emptyRepo := &service.RegistryRepository{
		Name: "empty", Type: service.RegistryTypeGo, Kind: service.RegistryKindLocal, Mount: "m",
	}
	emptyR, _ := NewLocalFactory()(context.Background(), deps, "default", emptyRepo)
	empty := emptyR.(*Local)

	full := localWithVersion(t, mod, "v1.0.0", "module github.com/foo/bar (from full)")

	resolver := &stubResolver{regs: map[string]registry.Registry{
		"empty": empty, "full": full,
	}}
	v := newVirtual(t, []string{"empty", "full"}, resolver)

	r := httptest.NewRequest(http.MethodGet, "/"+encoded+"/@v/v1.0.0.mod", nil)
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "from full") {
		t.Fatalf("expected fall-through to 'full', got %q", w.Body.String())
	}
}

func TestVirtual_AllMembersMiss(t *testing.T) {
	dir := t.TempDir()
	fs, _ := localfs.New(dir)
	deps := registry.Deps{MountRawFS: func(string) (rawfs.RawFS, error) { return fs, nil }}
	emptyRepo := &service.RegistryRepository{
		Name: "empty", Type: service.RegistryTypeGo, Kind: service.RegistryKindLocal, Mount: "m",
	}
	emptyR, _ := NewLocalFactory()(context.Background(), deps, "default", emptyRepo)
	empty := emptyR.(*Local)

	resolver := &stubResolver{regs: map[string]registry.Registry{"empty": empty}}
	v := newVirtual(t, []string{"empty"}, resolver)

	r := httptest.NewRequest(http.MethodGet, "/"+EncodeModulePath("github.com/missing/mod")+"/@v/v0.0.1.info", nil)
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when all members miss, got %d", w.Code)
	}
}

func TestVirtual_MissingMemberSkipped(t *testing.T) {
	// Resolver returns false for "ghost"; virtual should skip and
	// fall through to "real" without panicking.
	real := localWithVersion(t, "github.com/foo/bar", "v1.0.0", "module github.com/foo/bar (real)")
	resolver := &stubResolver{regs: map[string]registry.Registry{"real": real}}
	v := newVirtual(t, []string{"ghost", "real"}, resolver)

	r := httptest.NewRequest(http.MethodGet, "/"+EncodeModulePath("github.com/foo/bar")+"/@v/v1.0.0.mod", nil)
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "real") {
		t.Fatalf("body %q", w.Body.String())
	}
}

func TestVirtual_UnionList(t *testing.T) {
	mod := "github.com/foo/bar"
	encoded := EncodeModulePath(mod)

	// Member A has v0.1.0, v1.0.0
	a := localWithVersion(t, mod, "v0.1.0", "modA-1")
	uploadInfo(t, a, mod, "v1.0.0")

	// Member B has v1.0.0, v2.0.0 (overlap on v1.0.0, additional v2.0.0)
	b := localWithVersion(t, mod, "v1.0.0", "modB-1")
	uploadInfo(t, b, mod, "v2.0.0")

	resolver := &stubResolver{regs: map[string]registry.Registry{"a": a, "b": b}}
	v := newVirtual(t, []string{"a", "b"}, resolver)

	r := httptest.NewRequest(http.MethodGet, "/"+encoded+"/@v/list", nil)
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	// Expect deduped sorted: v0.1.0, v1.0.0, v2.0.0
	want := "v0.1.0\nv1.0.0\nv2.0.0\n"
	if w.Body.String() != want {
		t.Fatalf("got %q, want %q", w.Body.String(), want)
	}
}

func TestVirtual_RejectsWrite(t *testing.T) {
	resolver := &stubResolver{regs: map[string]registry.Registry{}}
	v := newVirtual(t, []string{"x"}, resolver)
	r := httptest.NewRequest(http.MethodPut, "/"+EncodeModulePath("github.com/foo/bar")+"/@v/v1.0.0.info", strings.NewReader("x"))
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestVirtual_BogusPath(t *testing.T) {
	resolver := &stubResolver{regs: map[string]registry.Registry{}}
	v := newVirtual(t, []string{"x"}, resolver)
	r := httptest.NewRequest(http.MethodGet, "/garbage", nil)
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
