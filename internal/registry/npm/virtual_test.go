package npm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/service"
)

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
		Name: "v", Type: "npm", Kind: "virtual", Members: members,
	}
	r, err := NewVirtualFactory(resolver)(nil, registry.Deps{}, "default", repo)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	return r.(*Virtual)
}

func TestNPMVirtual_PackumentUnion(t *testing.T) {
	a := newNPMLocal(t, true)
	publishVersion(t, a, "lodash", "1.0.0", []byte("from-a"))

	b := newNPMLocal(t, true)
	publishVersion(t, b, "lodash", "2.0.0", []byte("from-b"))

	resolver := &stubResolver{regs: map[string]registry.Registry{"a": a, "b": b}}
	v := newVirtual(t, []string{"a", "b"}, resolver)

	r := httptest.NewRequest(http.MethodGet, "/lodash", nil)
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var pkg map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &pkg)
	versions := pkg["versions"].(map[string]any)
	if versions["1.0.0"] == nil || versions["2.0.0"] == nil {
		t.Fatalf("expected versions from both members, got %v", versions)
	}
}

func TestNPMVirtual_TarballFirstHit(t *testing.T) {
	a := newNPMLocal(t, true)
	publishVersion(t, a, "lodash", "1.0.0", []byte("A-WINS"))
	b := newNPMLocal(t, true)
	publishVersion(t, b, "lodash", "1.0.0", []byte("B-LOSES"))

	resolver := &stubResolver{regs: map[string]registry.Registry{"a": a, "b": b}}
	v := newVirtual(t, []string{"a", "b"}, resolver)

	r := httptest.NewRequest(http.MethodGet, "/lodash/-/lodash-1.0.0.tgz", nil)
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "A-WINS") {
		t.Fatalf("body %q", w.Body.String())
	}
}

func TestNPMVirtual_AllMembersMiss(t *testing.T) {
	a := newNPMLocal(t, true)
	resolver := &stubResolver{regs: map[string]registry.Registry{"a": a}}
	v := newVirtual(t, []string{"a"}, resolver)
	r := httptest.NewRequest(http.MethodGet, "/missing", nil)
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestNPMVirtual_RejectsWrite(t *testing.T) {
	resolver := &stubResolver{regs: map[string]registry.Registry{}}
	v := newVirtual(t, []string{"x"}, resolver)
	r := httptest.NewRequest(http.MethodPut, "/lodash", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestNPMVirtual_FactoryRequiresMembers(t *testing.T) {
	_, err := NewVirtualFactory(&stubResolver{})(nil, registry.Deps{},
		"ns", &service.RegistryRepository{Name: "v", Type: "npm", Kind: "virtual"})
	if err == nil {
		t.Fatal("expected error when members empty")
	}
}

func TestNPMVirtual_UnionSearch(t *testing.T) {
	a := newNPMLocal(t, true)
	publishVersion(t, a, "lodash", "1.0.0", []byte("a"))
	b := newNPMLocal(t, true)
	publishVersion(t, b, "underscore", "1.0.0", []byte("b"))

	resolver := &stubResolver{regs: map[string]registry.Registry{"a": a, "b": b}}
	v := newVirtual(t, []string{"a", "b"}, resolver)

	r := httptest.NewRequest(http.MethodGet, "/-/v1/search?text=", nil)
	w := httptest.NewRecorder()
	v.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var resp struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Total != 2 {
		t.Fatalf("expected 2 unique results, got %d", resp.Total)
	}
}
