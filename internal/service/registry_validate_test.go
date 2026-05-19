package service

import (
	"errors"
	"strings"
	"testing"
)

// Test the validator across the dimensions the runtime relies on:
// name shape, type/kind enumeration, kind-specific required fields,
// virtual member resolution, and upstream auth shape.

func TestValidate_NilSettings(t *testing.T) {
	var rs *RegistrySettings
	if err := rs.Validate(); err != nil {
		t.Fatalf("nil settings should validate clean, got %v", err)
	}
}

func TestValidate_EmptySettings(t *testing.T) {
	rs := &RegistrySettings{}
	if err := rs.Validate(); err != nil {
		t.Fatalf("empty settings should validate clean, got %v", err)
	}
}

func TestValidate_NamespaceName(t *testing.T) {
	cases := []struct {
		name    string
		ns      string
		wantErr bool
	}{
		{"valid lowercase", "team-a", false},
		{"valid underscore", "team_a", false},
		{"valid digits", "team1", false},
		{"empty", "", true},
		{"uppercase", "TeamA", true},
		{"space", "team a", true},
		{"slash", "team/a", true},
		{"dot", "team.a", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs := &RegistrySettings{Namespaces: []RegistryNamespace{{Name: tc.ns}}}
			err := rs.Validate()
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Fatalf("Validate() err=%v wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, ErrBadRequest) {
				t.Errorf("expected ErrBadRequest in chain, got %v", err)
			}
		})
	}
}

func TestValidate_DuplicateNamespace(t *testing.T) {
	rs := &RegistrySettings{Namespaces: []RegistryNamespace{
		{Name: "a"},
		{Name: "a"},
	}}
	err := rs.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate namespace") {
		t.Fatalf("expected duplicate namespace error, got %v", err)
	}
}

func TestValidate_RepoType(t *testing.T) {
	cases := []struct {
		typ     string
		wantErr bool
	}{
		{"go", false},
		{"npm", false},
		{"docker", false},
		{"maven", true},
		{"", true},
	}
	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			rs := &RegistrySettings{Namespaces: []RegistryNamespace{{
				Name: "ns",
				Repositories: []RegistryRepository{{
					Name:     "r1",
					Type:     tc.typ,
					Kind:     RegistryKindLocal,
					Mount:    "m",
					BasePath: "p/",
				}},
			}}}
			err := rs.Validate()
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Fatalf("Validate() err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestValidate_LocalRequiresMount(t *testing.T) {
	rs := &RegistrySettings{Namespaces: []RegistryNamespace{{
		Name: "ns",
		Repositories: []RegistryRepository{{
			Name: "r1",
			Type: RegistryTypeGo,
			Kind: RegistryKindLocal,
			// Mount intentionally missing
		}},
	}}}
	err := rs.Validate()
	if err == nil || !strings.Contains(err.Error(), "requires mount") {
		t.Fatalf("expected mount required error, got %v", err)
	}
}

func TestValidate_RemoteRequiresURL(t *testing.T) {
	rs := &RegistrySettings{Namespaces: []RegistryNamespace{{
		Name: "ns",
		Repositories: []RegistryRepository{{
			Name: "r1",
			Type: RegistryTypeGo,
			Kind: RegistryKindRemote,
		}},
	}}}
	err := rs.Validate()
	if err == nil || !strings.Contains(err.Error(), "requires url") {
		t.Fatalf("expected url required error, got %v", err)
	}
}

func TestValidate_RemoteURLScheme(t *testing.T) {
	rs := &RegistrySettings{Namespaces: []RegistryNamespace{{
		Name: "ns",
		Repositories: []RegistryRepository{{
			Name: "r1",
			Type: RegistryTypeGo,
			Kind: RegistryKindRemote,
			URL:  "ftp://nope",
		}},
	}}}
	err := rs.Validate()
	if err == nil || !strings.Contains(err.Error(), "url must be http") {
		t.Fatalf("expected url scheme error, got %v", err)
	}
}

func TestValidate_RemoteMutableTTL(t *testing.T) {
	rs := &RegistrySettings{Namespaces: []RegistryNamespace{{
		Name: "ns",
		Repositories: []RegistryRepository{{
			Name:       "r1",
			Type:       RegistryTypeGo,
			Kind:       RegistryKindRemote,
			URL:        "https://proxy.golang.org",
			MutableTTL: "not-a-duration",
		}},
	}}}
	err := rs.Validate()
	if err == nil || !strings.Contains(err.Error(), "invalid mutable_ttl") {
		t.Fatalf("expected mutable_ttl error, got %v", err)
	}
}

func TestValidate_VirtualMembers(t *testing.T) {
	// Members must exist, match type, and not chain.
	t.Run("missing members", func(t *testing.T) {
		rs := &RegistrySettings{Namespaces: []RegistryNamespace{{
			Name: "ns",
			Repositories: []RegistryRepository{{
				Name: "v1",
				Type: RegistryTypeGo,
				Kind: RegistryKindVirtual,
			}},
		}}}
		if err := rs.Validate(); err == nil {
			t.Fatal("expected error on virtual without members")
		}
	})
	t.Run("self-reference", func(t *testing.T) {
		rs := &RegistrySettings{Namespaces: []RegistryNamespace{{
			Name: "ns",
			Repositories: []RegistryRepository{{
				Name:    "v1",
				Type:    RegistryTypeGo,
				Kind:    RegistryKindVirtual,
				Members: []string{"v1"},
			}},
		}}}
		err := rs.Validate()
		if err == nil || !strings.Contains(err.Error(), "cannot reference itself") {
			t.Fatalf("expected self-reference error, got %v", err)
		}
	})
	t.Run("missing member", func(t *testing.T) {
		rs := &RegistrySettings{Namespaces: []RegistryNamespace{{
			Name: "ns",
			Repositories: []RegistryRepository{{
				Name:    "v1",
				Type:    RegistryTypeGo,
				Kind:    RegistryKindVirtual,
				Members: []string{"ghost"},
			}},
		}}}
		err := rs.Validate()
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected missing member error, got %v", err)
		}
	})
	t.Run("type mismatch", func(t *testing.T) {
		rs := &RegistrySettings{Namespaces: []RegistryNamespace{{
			Name: "ns",
			Repositories: []RegistryRepository{
				{Name: "a", Type: RegistryTypeNPM, Kind: RegistryKindLocal, Mount: "m"},
				{Name: "v", Type: RegistryTypeGo, Kind: RegistryKindVirtual, Members: []string{"a"}},
			},
		}}}
		err := rs.Validate()
		if err == nil || !strings.Contains(err.Error(), "expected") {
			t.Fatalf("expected type mismatch error, got %v", err)
		}
	})
	t.Run("virtual chain rejected", func(t *testing.T) {
		rs := &RegistrySettings{Namespaces: []RegistryNamespace{{
			Name: "ns",
			Repositories: []RegistryRepository{
				{Name: "a", Type: RegistryTypeGo, Kind: RegistryKindLocal, Mount: "m"},
				{Name: "v1", Type: RegistryTypeGo, Kind: RegistryKindVirtual, Members: []string{"a"}},
				{Name: "v2", Type: RegistryTypeGo, Kind: RegistryKindVirtual, Members: []string{"v1"}},
			},
		}}}
		err := rs.Validate()
		if err == nil || !strings.Contains(err.Error(), "chains not allowed") {
			t.Fatalf("expected chain rejection, got %v", err)
		}
	})
	t.Run("forward reference works", func(t *testing.T) {
		// Member defined after the virtual that references it should
		// still validate (second-pass resolution).
		rs := &RegistrySettings{Namespaces: []RegistryNamespace{{
			Name: "ns",
			Repositories: []RegistryRepository{
				{Name: "v", Type: RegistryTypeGo, Kind: RegistryKindVirtual, Members: []string{"a"}},
				{Name: "a", Type: RegistryTypeGo, Kind: RegistryKindLocal, Mount: "m"},
			},
		}}}
		if err := rs.Validate(); err != nil {
			t.Fatalf("forward reference should resolve, got %v", err)
		}
	})
}

func TestValidate_DefaultLocal(t *testing.T) {
	t.Run("default_local missing", func(t *testing.T) {
		rs := &RegistrySettings{Namespaces: []RegistryNamespace{{
			Name: "ns",
			Repositories: []RegistryRepository{
				{Name: "v", Type: RegistryTypeGo, Kind: RegistryKindVirtual,
					Members: []string{"a"}, DefaultLocal: "ghost"},
				{Name: "a", Type: RegistryTypeGo, Kind: RegistryKindLocal, Mount: "m"},
			},
		}}}
		err := rs.Validate()
		if err == nil || !strings.Contains(err.Error(), "default_local") {
			t.Fatalf("expected default_local error, got %v", err)
		}
	})
	t.Run("default_local must be local kind", func(t *testing.T) {
		rs := &RegistrySettings{Namespaces: []RegistryNamespace{{
			Name: "ns",
			Repositories: []RegistryRepository{
				{Name: "v", Type: RegistryTypeGo, Kind: RegistryKindVirtual,
					Members: []string{"r"}, DefaultLocal: "r"},
				{Name: "r", Type: RegistryTypeGo, Kind: RegistryKindRemote,
					URL: "https://proxy.golang.org"},
			},
		}}}
		err := rs.Validate()
		if err == nil || !strings.Contains(err.Error(), "not local") {
			t.Fatalf("expected default_local-not-local error, got %v", err)
		}
	})
}

func TestValidate_UpstreamAuth(t *testing.T) {
	cases := []struct {
		name    string
		auth    *RegistryUpstreamAuth
		wantErr bool
	}{
		{"basic ok", &RegistryUpstreamAuth{Type: "basic", Username: "u", Password: "p"}, false},
		{"basic no user", &RegistryUpstreamAuth{Type: "basic", Password: "p"}, true},
		{"bearer ok", &RegistryUpstreamAuth{Type: "bearer", Token: "t"}, false},
		{"bearer no token", &RegistryUpstreamAuth{Type: "bearer"}, true},
		{"header ok", &RegistryUpstreamAuth{Type: "header", Header: "X-Token", Value: "v"}, false},
		{"header no name", &RegistryUpstreamAuth{Type: "header", Value: "v"}, true},
		{"unknown type", &RegistryUpstreamAuth{Type: "magic"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs := &RegistrySettings{Namespaces: []RegistryNamespace{{
				Name: "ns",
				Repositories: []RegistryRepository{{
					Name: "r1", Type: RegistryTypeGo, Kind: RegistryKindRemote,
					URL: "https://x", Auth: tc.auth,
				}},
			}}}
			err := rs.Validate()
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Fatalf("Validate() err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestFindNamespaceRepository(t *testing.T) {
	rs := &RegistrySettings{Namespaces: []RegistryNamespace{
		{Name: "alpha", Repositories: []RegistryRepository{{Name: "r1"}}},
		{Name: "beta", Repositories: []RegistryRepository{{Name: "r2"}}},
	}}
	if got := rs.FindNamespace("alpha"); got == nil || got.Name != "alpha" {
		t.Fatalf("FindNamespace alpha: %+v", got)
	}
	if got := rs.FindNamespace("missing"); got != nil {
		t.Fatalf("FindNamespace missing should be nil")
	}
	ns := rs.FindNamespace("alpha")
	if got := ns.FindRepository("r1"); got == nil || got.Name != "r1" {
		t.Fatalf("FindRepository r1: %+v", got)
	}
	if got := ns.FindRepository("r2"); got != nil {
		t.Fatalf("FindRepository wrong-ns should be nil")
	}
	// Nil receivers safe.
	var nilRS *RegistrySettings
	if got := nilRS.FindNamespace("x"); got != nil {
		t.Fatalf("nil RegistrySettings.FindNamespace should be nil, got %v", got)
	}
	var nilNS *RegistryNamespace
	if got := nilNS.FindRepository("x"); got != nil {
		t.Fatalf("nil Namespace.FindRepository should be nil")
	}
}
