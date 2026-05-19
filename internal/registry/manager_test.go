package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
)

// stubReg is a Registry implementation that records ServeHTTP and
// Close calls so tests can assert lifecycle.
type stubReg struct {
	ns, name, typ, kind string
	closed              atomic.Bool
	served              atomic.Int32
}

func (s *stubReg) Namespace() string { return s.ns }
func (s *stubReg) Name() string      { return s.name }
func (s *stubReg) Type() string      { return s.typ }
func (s *stubReg) Kind() string      { return s.kind }
func (s *stubReg) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.served.Add(1)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
func (s *stubReg) Close() error {
	s.closed.Store(true)
	return nil
}

func TestManager_RegisterFactory_Errors(t *testing.T) {
	m := NewManager(Deps{})
	f := func(ctx context.Context, deps Deps, ns string, r *service.RegistryRepository) (Registry, error) {
		return &stubReg{ns: ns, name: r.Name}, nil
	}

	if err := m.RegisterFactory("", "local", f); err == nil {
		t.Errorf("expected error for empty type")
	}
	if err := m.RegisterFactory("go", "", f); err == nil {
		t.Errorf("expected error for empty kind")
	}
	if err := m.RegisterFactory("go", "local", nil); err == nil {
		t.Errorf("expected error for nil factory")
	}
	if err := m.RegisterFactory("go", "local", f); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	if err := m.RegisterFactory("go", "local", f); err == nil {
		t.Errorf("expected duplicate registration error")
	}
}

func TestManager_Reload_BuildsRoutingTable(t *testing.T) {
	m := NewManager(Deps{})

	built := []string{}
	f := func(ctx context.Context, deps Deps, ns string, r *service.RegistryRepository) (Registry, error) {
		built = append(built, ns+"/"+r.Name)
		return &stubReg{ns: ns, name: r.Name, typ: r.Type, kind: r.Kind}, nil
	}
	if err := m.RegisterFactory("go", "local", f); err != nil {
		t.Fatal(err)
	}

	rs := &service.RegistrySettings{
		Namespaces: []service.RegistryNamespace{
			{
				Name: "acme",
				Repositories: []service.RegistryRepository{
					{Name: "r1", Type: "go", Kind: "local", Mount: "m"},
					{Name: "r2", Type: "go", Kind: "local", Mount: "m"},
				},
			},
		},
	}
	m.Reload(context.Background(), rs)

	if got := len(built); got != 2 {
		t.Fatalf("expected 2 factories invoked, got %d", got)
	}

	if reg, ok := m.Lookup("acme", "r1"); !ok || reg.Name() != "r1" {
		t.Fatalf("Lookup acme/r1 failed: ok=%v reg=%v", ok, reg)
	}
	if _, ok := m.Lookup("acme", "missing"); ok {
		t.Fatalf("Lookup acme/missing should fail")
	}
}

func TestManager_Reload_SkipsUnknownKind(t *testing.T) {
	m := NewManager(Deps{})

	called := false
	f := func(ctx context.Context, deps Deps, ns string, r *service.RegistryRepository) (Registry, error) {
		called = true
		return &stubReg{ns: ns, name: r.Name}, nil
	}
	if err := m.RegisterFactory("go", "local", f); err != nil {
		t.Fatal(err)
	}

	rs := &service.RegistrySettings{
		Namespaces: []service.RegistryNamespace{{
			Name: "acme",
			Repositories: []service.RegistryRepository{
				// No factory registered for npm/local — should skip silently.
				{Name: "r-unknown", Type: "npm", Kind: "local"},
				{Name: "r-known", Type: "go", Kind: "local"},
			},
		}},
	}
	m.Reload(context.Background(), rs)

	if !called {
		t.Fatal("known factory was not called")
	}
	if _, ok := m.Lookup("acme", "r-unknown"); ok {
		t.Fatal("unknown-kind repo should not be installed")
	}
	if _, ok := m.Lookup("acme", "r-known"); !ok {
		t.Fatal("known-kind repo should be installed")
	}
}

func TestManager_Reload_FactoryErrorIsLoggedAndSkipped(t *testing.T) {
	m := NewManager(Deps{})

	f := func(ctx context.Context, deps Deps, ns string, r *service.RegistryRepository) (Registry, error) {
		if r.Name == "bad" {
			return nil, errors.New("forced failure")
		}
		return &stubReg{ns: ns, name: r.Name}, nil
	}
	if err := m.RegisterFactory("go", "local", f); err != nil {
		t.Fatal(err)
	}

	rs := &service.RegistrySettings{
		Namespaces: []service.RegistryNamespace{{
			Name: "ns",
			Repositories: []service.RegistryRepository{
				{Name: "good", Type: "go", Kind: "local"},
				{Name: "bad", Type: "go", Kind: "local"},
			},
		}},
	}
	m.Reload(context.Background(), rs)

	if _, ok := m.Lookup("ns", "bad"); ok {
		t.Fatal("failed factory should not install repo")
	}
	if _, ok := m.Lookup("ns", "good"); !ok {
		t.Fatal("good repo should still install when a sibling failed")
	}
}

func TestManager_Reload_ClosesOldRegistries(t *testing.T) {
	m := NewManager(Deps{})

	var first, second *stubReg
	f := func(ctx context.Context, deps Deps, ns string, r *service.RegistryRepository) (Registry, error) {
		reg := &stubReg{ns: ns, name: r.Name}
		if first == nil {
			first = reg
		} else {
			second = reg
		}
		return reg, nil
	}
	if err := m.RegisterFactory("go", "local", f); err != nil {
		t.Fatal(err)
	}

	rs := &service.RegistrySettings{Namespaces: []service.RegistryNamespace{{
		Name:         "ns",
		Repositories: []service.RegistryRepository{{Name: "r1", Type: "go", Kind: "local"}},
	}}}

	m.Reload(context.Background(), rs)
	if first == nil {
		t.Fatal("first reload should have produced a registry")
	}
	if first.closed.Load() {
		t.Fatal("first registry should not be closed yet")
	}

	// Second reload — first should be closed.
	m.Reload(context.Background(), rs)
	if !first.closed.Load() {
		t.Fatal("first registry should be closed after second reload")
	}
	if second == nil || second.closed.Load() {
		t.Fatal("second registry should exist and be open")
	}
}

func TestManager_Close(t *testing.T) {
	m := NewManager(Deps{})

	var reg *stubReg
	f := func(ctx context.Context, deps Deps, ns string, r *service.RegistryRepository) (Registry, error) {
		reg = &stubReg{ns: ns, name: r.Name}
		return reg, nil
	}
	_ = m.RegisterFactory("go", "local", f)

	rs := &service.RegistrySettings{Namespaces: []service.RegistryNamespace{{
		Name:         "ns",
		Repositories: []service.RegistryRepository{{Name: "r", Type: "go", Kind: "local"}},
	}}}
	m.Reload(context.Background(), rs)

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !reg.closed.Load() {
		t.Fatal("registry should be closed after manager Close")
	}
	// List should now be empty.
	if got := m.List(); len(got) != 0 {
		t.Fatalf("after Close, List should be empty, got %d", len(got))
	}
}

func TestSplitRequestPath(t *testing.T) {
	cases := []struct {
		in       string
		ns, repo string
		rest     string
		ok       bool
	}{
		{"/acme/r1/@v/list", "acme", "r1", "/@v/list", true},
		{"acme/r1/@v/list", "acme", "r1", "/@v/list", true},
		{"/acme/r1", "acme", "r1", "", true},
		{"/acme/r1/", "acme", "r1", "/", true},
		{"/acme/r1/v2/lib/manifests/latest", "acme", "r1", "/v2/lib/manifests/latest", true},
		{"/acme", "", "", "", false},
		{"/", "", "", "", false},
		{"", "", "", "", false},
		{"//r1/x", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			ns, repo, rest, ok := SplitRequestPath(tc.in)
			if ok != tc.ok || ns != tc.ns || repo != tc.repo || rest != tc.rest {
				t.Errorf("SplitRequestPath(%q) = (%q,%q,%q,%v), want (%q,%q,%q,%v)",
					tc.in, ns, repo, rest, ok, tc.ns, tc.repo, tc.rest, tc.ok)
			}
		})
	}
}

func TestManager_ServeHTTPRoundTrip(t *testing.T) {
	// Sanity end-to-end: register a factory, Reload, dispatch a
	// fabricated HTTP request through the stub registry.
	m := NewManager(Deps{})
	f := func(ctx context.Context, deps Deps, ns string, r *service.RegistryRepository) (Registry, error) {
		return &stubReg{ns: ns, name: r.Name, typ: r.Type, kind: r.Kind}, nil
	}
	_ = m.RegisterFactory("go", "local", f)
	m.Reload(context.Background(), &service.RegistrySettings{
		Namespaces: []service.RegistryNamespace{{
			Name:         "ns",
			Repositories: []service.RegistryRepository{{Name: "r1", Type: "go", Kind: "local"}},
		}},
	})

	reg, ok := m.Lookup("ns", "r1")
	if !ok {
		t.Fatal("Lookup failed")
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/@v/list", nil)
	reg.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ok") {
		t.Fatalf("body %q", w.Body.String())
	}
}
