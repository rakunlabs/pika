package external

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// stubProvider is a Provider that records calls and never touches a network.
// It deliberately does NOT implement ExistenceChecker so the guard's
// create/update fallback can be exercised; stubExisting below adds it.
type stubProvider struct {
	calls []string
	caps  Capabilities
}

func (s *stubProvider) Kind() string { return "stub" }
func (s *stubProvider) Capabilities() Capabilities {
	return s.caps
}
func (s *stubProvider) Validate() error { return nil }
func (s *stubProvider) Fetch(context.Context, string) ([]byte, error) {
	s.calls = append(s.calls, "fetch")
	return []byte("{}"), nil
}
func (s *stubProvider) Read(context.Context, string) (*Entry, error) {
	s.calls = append(s.calls, "read")
	return &Entry{}, nil
}
func (s *stubProvider) List(context.Context, string) ([]string, error) {
	s.calls = append(s.calls, "list")
	return []string{"a"}, nil
}
func (s *stubProvider) Write(context.Context, string, map[string]any) error {
	s.calls = append(s.calls, "write")
	return nil
}
func (s *stubProvider) Delete(context.Context, string) error {
	s.calls = append(s.calls, "delete")
	return nil
}
func (s *stubProvider) ListVersions(context.Context, string) ([]Version, error) {
	s.calls = append(s.calls, "versions")
	return nil, nil
}
func (s *stubProvider) ReadVersion(context.Context, string, string) (*Entry, error) {
	s.calls = append(s.calls, "version")
	return &Entry{}, nil
}
func (s *stubProvider) Test(context.Context) TestResult {
	s.calls = append(s.calls, "test")
	return TestResult{OK: true}
}

type stubExisting struct {
	*stubProvider
	existing map[string]bool
	err      error
}

func (s *stubExisting) Exists(_ context.Context, path string) (bool, error) {
	s.calls = append(s.calls, "exists")
	return s.existing[path], s.err
}

func fullCaps() Capabilities {
	return Capabilities{CanRead: true, CanList: true, CanWrite: true, CanDelete: true, CanVersions: true}
}

func TestAccessUnrestrictedIsNotWrapped(t *testing.T) {
	for _, access := range []*Access{nil, {}, {Read: boolPtr(true), Create: boolPtr(true)}} {
		if access.Restricted() {
			t.Fatalf("%+v reported as restricted", access)
		}
	}
	p, err := ResourceProvider(External{Http: nil, Consul: &Consul{Address: "http://consul:8500"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, wrapped := p.(*accessProvider); wrapped {
		t.Fatal("unrestricted resource was wrapped")
	}
	deny := false
	p, err = ResourceProvider(External{Consul: &Consul{Address: "http://consul:8500"}, Access: &Access{Delete: &deny}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, wrapped := p.(*accessProvider); !wrapped {
		t.Fatal("restricted resource was not wrapped")
	}
}

func TestAccessDeniesOperationsBeforeBackend(t *testing.T) {
	stub := &stubProvider{caps: fullCaps()}
	deny := false
	guard := &accessProvider{Provider: stub, access: &Access{
		Read: &deny, List: &deny, Create: &deny, Update: &deny, Delete: &deny,
	}}
	ctx := context.Background()
	for name, err := range map[string]error{
		"fetch":    firstErr(guard.Fetch(ctx, "p")),
		"read":     firstErr2(guard.Read(ctx, "p")),
		"list":     firstErr3(guard.List(ctx, "p")),
		"versions": firstErr4(guard.ListVersions(ctx, "p")),
		"version":  firstErr2(guard.ReadVersion(ctx, "p", "1")),
		"write":    guard.Write(ctx, "p", map[string]any{"value": "v"}),
		"delete":   guard.Delete(ctx, "p"),
	} {
		if !errors.Is(err, ErrAccessDenied) {
			t.Errorf("%s error = %v, want ErrAccessDenied", name, err)
		}
	}
	if len(stub.calls) != 0 {
		t.Fatalf("backend was called: %v", stub.calls)
	}
	caps := guard.Capabilities()
	if caps.CanRead || caps.CanList || caps.CanWrite || caps.CanDelete || caps.CanVersions {
		t.Fatalf("capabilities not masked: %+v", caps)
	}
	if result := guard.Test(ctx); result.OK {
		t.Fatal("test reported OK with listing denied")
	}
}

func TestAccessCreateUpdateSplit(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name        string
		create      bool
		update      bool
		path        string
		wantDenied  bool
		wantBackend bool
	}{
		{name: "create allowed, new path", create: true, update: false, path: "new", wantBackend: true},
		{name: "create allowed, existing path", create: true, update: false, path: "old", wantDenied: true},
		{name: "update allowed, existing path", create: false, update: true, path: "old", wantBackend: true},
		{name: "update allowed, new path", create: false, update: true, path: "new", wantDenied: true},
		{name: "both allowed", create: true, update: true, path: "old", wantBackend: true},
		{name: "neither allowed", create: false, update: false, path: "new", wantDenied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubExisting{stubProvider: &stubProvider{caps: fullCaps()}, existing: map[string]bool{"old": true}}
			guard := &accessProvider{Provider: stub, access: &Access{Create: &tc.create, Update: &tc.update}}
			err := guard.Write(ctx, tc.path, map[string]any{"value": "v"})
			if tc.wantDenied != errors.Is(err, ErrAccessDenied) {
				t.Fatalf("err = %v, wantDenied = %v", err, tc.wantDenied)
			}
			wrote := false
			for _, call := range stub.calls {
				if call == "write" {
					wrote = true
				}
			}
			if wrote != tc.wantBackend {
				t.Fatalf("backend write = %v, want %v (calls: %v)", wrote, tc.wantBackend, stub.calls)
			}
			caps := guard.Capabilities()
			if caps.EffectiveCreate() != tc.create || caps.EffectiveUpdate() != tc.update {
				t.Fatalf("capabilities = %+v", caps)
			}
		})
	}
}

func TestAccessSplitNeedsExistenceChecker(t *testing.T) {
	stub := &stubProvider{caps: fullCaps()}
	create, update := true, false
	guard := &accessProvider{Provider: stub, access: &Access{Create: &create, Update: &update}}
	if err := guard.Write(context.Background(), "p", map[string]any{"value": "v"}); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("err = %v, want ErrAccessDenied", err)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("backend was called: %v", stub.calls)
	}
	// Backends that can't split must not advertise one: the SPA would
	// otherwise render a create form that always fails.
	if caps := guard.Capabilities(); caps.CanCreate != nil || caps.CanUpdate != nil {
		t.Fatalf("published a split the backend can't honour: %+v", caps)
	}
}

func TestAccessExistenceErrorPropagates(t *testing.T) {
	stub := &stubExisting{
		stubProvider: &stubProvider{caps: fullCaps()},
		existing:     map[string]bool{},
		err:          fmt.Errorf("upstream %d", http.StatusBadGateway),
	}
	create, update := true, false
	guard := &accessProvider{Provider: stub, access: &Access{Create: &create, Update: &update}}
	err := guard.Write(context.Background(), "p", map[string]any{"value": "v"})
	if err == nil || errors.Is(err, ErrAccessDenied) {
		t.Fatalf("err = %v, want the upstream failure", err)
	}
}

func TestAccessGitLabResourceSplitsCreateFromUpdate(t *testing.T) {
	existing := map[string]bool{"OLD_KEY": true}
	backend, _ := newKeyServer(t, existing)
	create, update := false, true
	guard := &accessProvider{Provider: backend, access: &Access{Create: &create, Update: &update}}
	ctx := context.Background()
	if err := guard.Write(ctx, "NEW_KEY", map[string]any{"value": "v"}); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("create err = %v, want ErrAccessDenied", err)
	}
	if existing["NEW_KEY"] {
		t.Fatal("variable was created despite create being denied")
	}
	if err := guard.Write(ctx, "OLD_KEY", map[string]any{"value": "v"}); err != nil {
		t.Fatalf("update denied: %v", err)
	}
}

// Small helpers so the table above can hold heterogeneous call results.
func firstErr(_ []byte, err error) error     { return err }
func firstErr2(_ *Entry, err error) error    { return err }
func firstErr3(_ []string, err error) error  { return err }
func firstErr4(_ []Version, err error) error { return err }
