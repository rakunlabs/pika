package service

import (
	"reflect"
	"sort"
	"testing"
)

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func TestHarvestRoles_FlatClaim(t *testing.T) {
	claims := map[string]any{"roles": []any{"admin", "viewer"}}
	got := HarvestRoles(claims, []string{"roles"})
	if want := []string{"admin", "viewer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestHarvestRoles_KeycloakRealmRoles(t *testing.T) {
	claims := map[string]any{
		"realm_access": map[string]any{
			"roles": []any{"offline_access", "pika-admin"},
		},
	}
	got := HarvestRoles(claims, []string{"realm_access.roles"})
	if want := []string{"offline_access", "pika-admin"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestHarvestRoles_KeycloakClientRolesWildcard(t *testing.T) {
	claims := map[string]any{
		"resource_access": map[string]any{
			"pika": map[string]any{"roles": []any{"editor"}},
			"grafana": map[string]any{
				"roles": []any{"viewer"},
			},
		},
	}
	got := sortedCopy(HarvestRoles(claims, []string{"resource_access.*.roles"}))
	if want := []string{"editor", "viewer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestHarvestRoles_SpecificClient(t *testing.T) {
	claims := map[string]any{
		"resource_access": map[string]any{
			"pika":    map[string]any{"roles": []any{"editor"}},
			"grafana": map[string]any{"roles": []any{"viewer"}},
		},
	}
	got := HarvestRoles(claims, []string{"resource_access.pika.roles"})
	if want := []string{"editor"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestHarvestRoles_MultiplePathsDedup(t *testing.T) {
	claims := map[string]any{
		"realm_access": map[string]any{"roles": []any{"admin", "shared"}},
		"resource_access": map[string]any{
			"pika": map[string]any{"roles": []any{"shared", "editor"}},
		},
	}
	got := HarvestRoles(claims, []string{
		"realm_access.roles",
		"resource_access.*.roles",
	})
	// first-seen order preserved, "shared" not duplicated.
	if want := []string{"admin", "shared", "editor"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestHarvestRoles_LeafShapes(t *testing.T) {
	// space-separated string leaf
	if got := HarvestRoles(map[string]any{"roles": "a b c"}, []string{"roles"}); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("string leaf: got %v", got)
	}
	// []string leaf
	if got := HarvestRoles(map[string]any{"roles": []string{"x", "y"}}, []string{"roles"}); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Fatalf("[]string leaf: got %v", got)
	}
}

func TestHarvestRoles_MissingAndEmpty(t *testing.T) {
	claims := map[string]any{"realm_access": map[string]any{"roles": []any{"a"}}}
	if got := HarvestRoles(claims, []string{"does.not.exist"}); got != nil {
		t.Fatalf("missing path should yield nil, got %v", got)
	}
	if got := HarvestRoles(nil, []string{"roles"}); got != nil {
		t.Fatalf("nil claims should yield nil, got %v", got)
	}
	if got := HarvestRoles(claims, nil); got != nil {
		t.Fatalf("nil paths should yield nil, got %v", got)
	}
	// "*" over a non-map node is a no-op, not a panic.
	if got := HarvestRoles(map[string]any{"realm_access": "oops"}, []string{"realm_access.*.roles"}); got != nil {
		t.Fatalf("wildcard over non-map should yield nil, got %v", got)
	}
}

func TestOAuth2RolesClaims_OnlyExplicitOverrides(t *testing.T) {
	s := &AuthSettings{
		OAuth2: []OAuth2StrategySettings{
			{Name: "google"}, // no override → default "roles" via id.Roles, absent here
			{Name: "keycloak", RolesClaims: []string{"realm_access.roles"}},
			{Name: "", RolesClaims: []string{"roles"}}, // empty name skipped
		},
	}
	got := s.OAuth2RolesClaims()
	if _, ok := got["google"]; ok {
		t.Errorf("provider without RolesClaims must not appear: %v", got["google"])
	}
	if !reflect.DeepEqual(got["keycloak"], []string{"realm_access.roles"}) {
		t.Errorf("keycloak = %v", got["keycloak"])
	}
	if _, ok := got[""]; ok {
		t.Errorf("empty-name provider should be skipped")
	}
}
