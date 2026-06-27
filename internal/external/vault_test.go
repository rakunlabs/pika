package external

import "testing"

func TestVaultIsKVv2Default(t *testing.T) {
	cases := []struct {
		name    string
		version int
		want    bool
	}{
		{"unset defaults to v2", 0, true},
		{"explicit v2", 2, true},
		{"explicit v1", 1, false},
		{"unknown defaults to v2", 9, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := &Vault{KVVersion: tc.version}
			if got := v.IsKVv2(); got != tc.want {
				t.Fatalf("IsKVv2(version=%d) = %v, want %v", tc.version, got, tc.want)
			}
		})
	}
	var nilVault *Vault
	if !nilVault.IsKVv2() {
		t.Fatal("nil Vault should default to v2")
	}
}

func TestVaultUseListVerb(t *testing.T) {
	cases := []struct {
		name   string
		method string
		want   bool
	}{
		{"unset defaults to GET", "", false},
		{"explicit get", "get", false},
		{"explicit list", "list", true},
		{"list is case-insensitive", "LIST", true},
		{"unknown defaults to GET", "weird", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := &Vault{ListMethod: tc.method}
			if got := v.UseListVerb(); got != tc.want {
				t.Fatalf("UseListVerb(method=%q) = %v, want %v", tc.method, got, tc.want)
			}
		})
	}
	var nilVault *Vault
	if nilVault.UseListVerb() {
		t.Fatal("nil Vault should default to GET (UseListVerb=false)")
	}
}

// TestVaultProviderPathsKVv2 locks in the v2 path layout (the finops/kv2
// case): reads/writes under data/, list/versions under metadata/. This
// is the regression guard against the old "guess v2 then fall back to
// v1" behaviour that produced base-path-less / wrong paths.
func TestVaultProviderPathsKVv2(t *testing.T) {
	p := &VaultProvider{Config: &Vault{Mount: "finops/kv2"}} // KVVersion 0 → v2

	if got, want := p.dataPath("app/db"), "finops/kv2/data/app/db"; got != want {
		t.Fatalf("dataPath = %q, want %q", got, want)
	}
	if got, want := p.dataPath(""), "finops/kv2/data"; got != want {
		t.Fatalf("dataPath(\"\") = %q, want %q", got, want)
	}
	if got, want := p.metadataPath("app/db"), "finops/kv2/metadata/app/db"; got != want {
		t.Fatalf("metadataPath = %q, want %q", got, want)
	}
	if got, want := p.listPath("app"), "finops/kv2/metadata/app"; got != want {
		t.Fatalf("listPath = %q, want %q", got, want)
	}
	if got, want := p.listPath(""), "finops/kv2/metadata/"; got != want {
		t.Fatalf("listPath(\"\") = %q, want %q", got, want)
	}
}

func TestVaultProviderPathsKVv1(t *testing.T) {
	p := &VaultProvider{Config: &Vault{Mount: "secret", KVVersion: 1}}

	if got, want := p.dataPath("app/db"), "secret/app/db"; got != want {
		t.Fatalf("dataPath = %q, want %q", got, want)
	}
	if got, want := p.dataPath(""), "secret"; got != want {
		t.Fatalf("dataPath(\"\") = %q, want %q", got, want)
	}
	if got, want := p.listPath("app"), "secret/app"; got != want {
		t.Fatalf("listPath = %q, want %q", got, want)
	}
	if got, want := p.listPath(""), "secret/"; got != want {
		t.Fatalf("listPath(\"\") = %q, want %q", got, want)
	}
}
