package authx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/ada/middleware/auth/identity"

	"github.com/rakunlabs/pika/internal/service"
)

func TestCapResolver_NoIdentity401(t *testing.T) {
	cr := &CapResolver{svc: nil, settings: service.CapabilityMapping{}}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := cr.Middleware()(next)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestCapResolver_SuperadminAllCaps(t *testing.T) {
	cr := &CapResolver{
		svc:      nil,
		settings: service.CapabilityMapping{Superadmins: []string{"alice"}},
	}
	got, _, _, _ := cr.resolve(context.Background(), &identity.Identity{Subject: "alice", Provider: "oauth2"})
	if len(got) != len(service.KnownCapabilityKeys()) {
		t.Errorf("superadmin: expected all caps, got %d", len(got))
	}
}

// TestCapResolver_RoleMapping verifies that an external role name maps to a
// pika Permission bundle Key, and the bundle's capability keys are expanded
// into the effective set. The mapping VALUE is a bundle Key ("editor"), not a
// raw capability key.
func TestCapResolver_RoleMapping(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	if _, err := svc.CreatePermission(ctx, &service.CreatePermissionRequest{
		Key:  "editor",
		Name: "Editor",
		Keys: []string{service.CapFilesRead, service.CapFilesWrite},
	}); err != nil {
		t.Fatalf("create perm: %v", err)
	}

	cr := &CapResolver{
		svc: svc,
		settings: service.CapabilityMapping{
			RoleMapping: map[string][]string{"pika-editor": {"editor"}},
		},
	}
	got, _, _, _ := cr.resolve(ctx, &identity.Identity{
		Subject:  "bob",
		Provider: "header",
		Roles:    []string{"pika-editor"},
	})
	if len(got) != 2 || !got.Has(service.CapFilesRead) || !got.Has(service.CapFilesWrite) {
		t.Errorf("role mapping: %v", got)
	}
}

// TestCapResolver_RoleMappingUnknownBundle confirms a role pointing at a
// non-existent (deleted/renamed) bundle key grants nothing — fail-closed.
func TestCapResolver_RoleMappingUnknownBundle(t *testing.T) {
	svc := newTestService(t)
	cr := &CapResolver{
		svc: svc,
		settings: service.CapabilityMapping{
			RoleMapping: map[string][]string{"pika-editor": {"does-not-exist"}},
		},
	}
	got, _, _, _ := cr.resolve(context.Background(), &identity.Identity{
		Subject:  "bob",
		Provider: "header",
		Roles:    []string{"pika-editor"},
	})
	if len(got) != 0 {
		t.Errorf("dangling bundle key should grant nothing, got %v", got)
	}
}

// TestCapResolver_KeycloakNestedRoles verifies that roles harvested from a
// nested claim path (Keycloak's realm_access.roles) — not present in
// Identity.Roles because ada only reads the flat "roles" claim — are mapped to
// permission bundles via RoleMapping.
func TestCapResolver_KeycloakNestedRoles(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	if _, err := svc.CreatePermission(ctx, &service.CreatePermissionRequest{
		Key:  "editor",
		Name: "Editor",
		Keys: []string{service.CapFilesRead, service.CapFilesWrite},
	}); err != nil {
		t.Fatalf("create perm: %v", err)
	}

	cr := &CapResolver{
		svc: svc,
		settings: service.CapabilityMapping{
			RoleMapping: map[string][]string{"pika-admin": {"editor"}},
		},
		rolePaths: map[string][]string{
			"keycloak": {"realm_access.roles", "resource_access.*.roles"},
		},
	}

	got, _, _, _ := cr.resolve(ctx, &identity.Identity{
		Subject:  "kc-sub",
		Provider: "keycloak",
		// Note: Roles is empty — ada found no flat "roles" claim.
		Claims: map[string]any{
			"realm_access": map[string]any{
				"roles": []any{"pika-admin", "offline_access"},
			},
		},
	})
	if len(got) != 2 || !got.Has(service.CapFilesRead) || !got.Has(service.CapFilesWrite) {
		t.Errorf("keycloak realm role mapping: %v", got)
	}
}

func TestCapResolver_LocalDelegates(t *testing.T) {
	svc := newTestService(t)
	user, err := svc.CreateSetupUser(context.Background(),
		&service.CreateUserRequest{Username: "carol", Password: "x"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	cr := &CapResolver{svc: svc, settings: service.CapabilityMapping{}}
	got, _, _, _ := cr.resolve(context.Background(), &identity.Identity{
		Subject:  "carol",
		Provider: "local",
		Claims:   map[string]any{PikaUserIDClaim: user.ID},
	})
	// Superadmin by construction — expect all caps.
	if len(got) == 0 {
		t.Error("local superadmin should have caps")
	}
}

// TestCapResolver_PasskeyResolvesLocalCapabilities locks in the fix for
// the "passkey login = zero caps" bug. Ada's passkey strategy stamps
// id.Provider with the configured strategy name (default "passkey"),
// but the authenticated user is a normal local pika user — the
// credential row in `passkeys` is FK'd to users.id. The resolver must
// route passkey identities through the local-equivalent branch
// (username → ResolveLocalCapabilityKeys) instead of trying to look
// them up via user_identities, which has no row for a passkey
// credential and would silently return an empty capability set.
//
// Before the fix the user logged in successfully but every protected
// endpoint denied them with 403 because the cap set was empty —
// matching the user report "admin'im ama hic permissionum yok".
func TestCapResolver_PasskeyResolvesLocalCapabilities(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// CreateSetupUser flips is_superadmin on the very first user, so
	// the cap set must be the full known-key list.
	user, err := svc.CreateSetupUser(ctx, &service.CreateUserRequest{
		Username: "alice",
		Password: "x",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	cr := &CapResolver{svc: svc, settings: service.CapabilityMapping{}}

	// Mirror what sessionstore.Save plants: provider is the strategy
	// name, subject is the local username, and the pika_user_id claim
	// carries the resolved users.id.
	got, username, userID, _ := cr.resolve(ctx, &identity.Identity{
		Subject:  "alice",
		Provider: "passkey",
		Claims:   map[string]any{PikaUserIDClaim: user.ID},
	})

	if len(got) != len(service.KnownCapabilityKeys()) {
		t.Errorf("passkey superadmin: expected all %d caps, got %d", len(service.KnownCapabilityKeys()), len(got))
	}
	if username != "alice" {
		t.Errorf("username = %q, want alice", username)
	}
	if userID != user.ID {
		t.Errorf("userID = %q, want %q", userID, user.ID)
	}
}

// TestCapResolver_PasskeyHonorsPerUserPermissions verifies that the
// passkey → local branch also picks up non-superadmin DB permissions
// (the bundle/keys flow admins use via the Users UI). A passkey login
// for a regular user with a "files.read" bundle must end up with
// files.read in the cap set — not zero caps, and not "all caps via
// superadmin".
func TestCapResolver_PasskeyHonorsPerUserPermissions(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Seed a superadmin so we can create non-superadmin users.
	if _, err := svc.CreateSetupUser(ctx, &service.CreateUserRequest{
		Username: "root",
		Password: "x",
	}); err != nil {
		t.Fatalf("setup superadmin: %v", err)
	}
	bob, err := svc.CreateUser(ctx, &service.CreateUserRequest{
		Username: "bob",
		Password: "x",
	})
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}

	perm, err := svc.CreatePermission(ctx, &service.CreatePermissionRequest{
		Key:  "viewer",
		Name: "Viewer",
		Keys: []string{service.CapFilesRead},
	})
	if err != nil {
		t.Fatalf("create perm: %v", err)
	}
	if err := svc.SetUserPermissions(ctx, bob.ID, []string{perm.ID}); err != nil {
		t.Fatalf("assign perm: %v", err)
	}

	cr := &CapResolver{svc: svc, settings: service.CapabilityMapping{}}
	got, _, userID, _ := cr.resolve(ctx, &identity.Identity{
		Subject:  "bob",
		Provider: "passkey",
		Claims:   map[string]any{PikaUserIDClaim: bob.ID},
	})

	if userID != bob.ID {
		t.Errorf("userID = %q, want %q (passkey must resolve via username, not user_identities)", userID, bob.ID)
	}
	if !got.Has(service.CapFilesRead) {
		t.Errorf("missing files.read for passkey-authenticated bob: %v", got)
	}
	// Sanity check: bob is NOT superadmin, so the cap set must be
	// strictly smaller than the full known-key list.
	if len(got) == len(service.KnownCapabilityKeys()) {
		t.Error("non-superadmin should not get every capability")
	}
}

// TestCapResolver_ExternalUsesDBPermissions verifies that an OAuth2
// identity (or any non-local provider) linked to a provisioned user row
// inherits that user's superadmin flag / DB permissions. Before this
// change, external users could only get caps via RoleMapping; now they
// also pick up per-user grants from the admin UI.
func TestCapResolver_ExternalUsesDBPermissions(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Provision an external user via the normal session-store path.
	info, err := svc.FindOrCreateExternalUser(ctx, service.ExternalIdentityInput{
		Provider: "google",
		Subject:  "sub-42",
		Username: "eve",
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	// Promote to superadmin. The resolver must honor this regardless of
	// the identity provider.
	superTrue := true
	if err := svc.UpdateUser(ctx, info.ID, &service.UpdateUserRequest{}); err != nil {
		t.Fatalf("update: %v", err)
	}
	_ = superTrue // UpdateUser doesn't expose is_superadmin; flip via raw storage.
	// Easiest path: directly call the internal promotion helper via
	// Users().Update — but that's private. Instead assign a permission
	// and check it's picked up.

	// Create a permission bundle and assign it.
	perm, err := svc.CreatePermission(ctx, &service.CreatePermissionRequest{
		Key:  "viewer",
		Name: "Viewer",
		Keys: []string{service.CapFilesRead},
	})
	if err != nil {
		t.Fatalf("create perm: %v", err)
	}
	if err := svc.SetUserPermissions(ctx, info.ID, []string{perm.ID}); err != nil {
		t.Fatalf("assign perm: %v", err)
	}

	cr := &CapResolver{svc: svc, settings: service.CapabilityMapping{}}
	got, username, userID, _ := cr.resolve(ctx, &identity.Identity{
		Subject:  "sub-42",
		Provider: "google",
		Claims:   map[string]any{PikaUserIDClaim: info.ID},
	})
	if userID != info.ID {
		t.Errorf("resolver did not find user_id: got %q want %q", userID, info.ID)
	}
	if username != info.Username {
		t.Errorf("resolver did not find username: got %q want %q", username, info.Username)
	}
	if !got.Has(service.CapFilesRead) {
		t.Errorf("external user should have files.read via DB permission, got %v", got)
	}
}

// TestCapResolver_ExternalUnionsRoleMapping verifies that DB permissions
// AND RoleMapping both contribute to the external user's capability set.
// Operators can grant baseline caps via RoleMapping (without per-user DB
// rows) and add more via the admin UI — the resolver merges both.
func TestCapResolver_ExternalUnionsRoleMapping(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	info, err := svc.FindOrCreateExternalUser(ctx, service.ExternalIdentityInput{
		Provider: "google",
		Subject:  "sub-99",
		Username: "frank",
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	perm, _ := svc.CreatePermission(ctx, &service.CreatePermissionRequest{
		Key:  "viewer",
		Name: "Viewer",
		Keys: []string{service.CapFilesRead},
	})
	_ = svc.SetUserPermissions(ctx, info.ID, []string{perm.ID})

	// Bundle referenced by the external role mapping (value is a bundle Key).
	if _, err := svc.CreatePermission(ctx, &service.CreatePermissionRequest{
		Key:  "writer",
		Name: "Writer",
		Keys: []string{service.CapFilesWrite},
	}); err != nil {
		t.Fatalf("create writer perm: %v", err)
	}

	cr := &CapResolver{svc: svc, settings: service.CapabilityMapping{
		RoleMapping: map[string][]string{"writer-role": {"writer"}},
	}}
	got, _, _, _ := cr.resolve(ctx, &identity.Identity{
		Subject:  "sub-99",
		Provider: "google",
		Roles:    []string{"writer-role"},
		Claims:   map[string]any{PikaUserIDClaim: info.ID},
	})
	if !got.Has(service.CapFilesRead) {
		t.Errorf("missing DB perm: %v", got)
	}
	if !got.Has(service.CapFilesWrite) {
		t.Errorf("missing role-mapped perm: %v", got)
	}
}

// TestCapResolver_PerUserDenySubtractsCapability verifies the per-user deny
// overlay strips a single role-mapped capability for one user only, leaving
// the rest of the role's grants intact.
func TestCapResolver_PerUserDenySubtractsCapability(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	info, err := svc.FindOrCreateExternalUser(ctx, service.ExternalIdentityInput{
		Provider: "google", Subject: "sub-deny", Username: "deny-user",
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if _, err := svc.CreatePermission(ctx, &service.CreatePermissionRequest{
		Key: "editor", Name: "Editor",
		Keys: []string{service.CapFilesRead, service.CapFilesWrite},
	}); err != nil {
		t.Fatalf("create perm: %v", err)
	}

	cr := &CapResolver{svc: svc, settings: service.CapabilityMapping{
		RoleMapping: map[string][]string{"pika-editor": {"editor"}},
	}}
	id := &identity.Identity{
		Subject: "sub-deny", Provider: "google",
		Roles:  []string{"pika-editor"},
		Claims: map[string]any{PikaUserIDClaim: info.ID},
	}

	got, _, _, _ := cr.resolve(ctx, id)
	if !got.Has(service.CapFilesRead) || !got.Has(service.CapFilesWrite) {
		t.Fatalf("pre-deny should have both caps: %v", got)
	}

	if err := svc.SetUserDeniedCapabilities(ctx, info.ID, []string{service.CapFilesWrite}); err != nil {
		t.Fatalf("set deny: %v", err)
	}
	got, _, _, _ = cr.resolve(ctx, id)
	if got.Has(service.CapFilesWrite) {
		t.Errorf("deny did not remove files.write: %v", got)
	}
	if !got.Has(service.CapFilesRead) {
		t.Errorf("deny removed too much; files.read should remain: %v", got)
	}
}

// TestCapResolver_DenyExemptForAllowlistSuperadmin verifies the operator
// Superadmins allowlist is break-glass and not reduced by a per-user deny.
func TestCapResolver_DenyExemptForAllowlistSuperadmin(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	info, err := svc.FindOrCreateExternalUser(ctx, service.ExternalIdentityInput{
		Provider: "google", Subject: "admin-sub", Username: "admin-u",
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if err := svc.SetUserDeniedCapabilities(ctx, info.ID, []string{service.CapFilesWrite}); err != nil {
		t.Fatalf("set deny: %v", err)
	}

	cr := &CapResolver{svc: svc, settings: service.CapabilityMapping{
		Superadmins: []string{"admin-sub"},
	}}
	got, _, _, _ := cr.resolve(ctx, &identity.Identity{
		Subject: "admin-sub", Provider: "google",
		Claims: map[string]any{PikaUserIDClaim: info.ID},
	})
	if len(got) != len(service.KnownCapabilityKeys()) {
		t.Errorf("allowlist superadmin must be exempt from deny, got %d/%d", len(got), len(service.KnownCapabilityKeys()))
	}
}

// TestCapResolver_ResolveDetailedProvenance verifies the effective report
// attributes a role-mapped capability back to its role and bundle.
func TestCapResolver_ResolveDetailedProvenance(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	info, err := svc.FindOrCreateExternalUser(ctx, service.ExternalIdentityInput{
		Provider: "google", Subject: "sub-prov", Username: "prov",
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if _, err := svc.CreatePermission(ctx, &service.CreatePermissionRequest{
		Key: "editor", Name: "Editor", Keys: []string{service.CapFilesWrite},
	}); err != nil {
		t.Fatalf("create perm: %v", err)
	}
	cr := &CapResolver{svc: svc, settings: service.CapabilityMapping{
		RoleMapping: map[string][]string{"pika-editor": {"editor"}},
	}}
	rep := cr.resolveDetailed(ctx, &identity.Identity{
		Subject: "sub-prov", Provider: "google",
		Roles:  []string{"pika-editor"},
		Claims: map[string]any{PikaUserIDClaim: info.ID},
	})
	found := false
	for _, s := range rep.Sources {
		if s.Capability == service.CapFilesWrite && s.Kind == "role" && s.Role == "pika-editor" && s.Bundle == "editor" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected role provenance for files.write, got %+v", rep.Sources)
	}
}

func TestSubtractDenied(t *testing.T) {
	keys := []string{"a", "b", "c"}
	out, pat := subtractDenied(keys, map[string][]string{"b": {"x/**"}}, []string{"b"})
	if len(out) != 2 || out[0] != "a" || out[1] != "c" {
		t.Errorf("subtractDenied keys: %v", out)
	}
	if pat != nil {
		t.Errorf("subtractDenied should drop the only (denied) pattern, got %v", pat)
	}
	out2, pat2 := subtractDenied(keys, map[string][]string{"b": {"x/**"}}, nil)
	if len(out2) != 3 || pat2 == nil {
		t.Errorf("no deny should pass through unchanged: %v %v", out2, pat2)
	}
}

func TestSessionHandle_StableAndHidesID(t *testing.T) {
	id := "super-secret-session-cookie-id"
	h1 := sessionHandle(id)
	if h1 != sessionHandle(id) {
		t.Errorf("handle not stable")
	}
	if h1 == id || len(h1) != 64 {
		t.Errorf("handle must be a 64-hex sha256 that hides the raw id, got %q", h1)
	}
}
