package service_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
	bwstore "github.com/rakunlabs/pika/internal/storage/bw"
)

// newTestService boots an in-memory bw-backed Service. Each test
// gets a fresh, isolated database (Badger in-memory mode skips the
// on-disk directory entirely).
func newTestService(t *testing.T) *service.Service {
	t.Helper()

	store, err := bwstore.New(t.Context(), &bwstore.Config{InMemory: true})
	if err != nil {
		t.Fatalf("bw.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	return service.New(store)
}

// createUserHelper inserts a user via the public CreateUser API so the
// password hash + timestamps are populated correctly. Returns the created id.
func createUserHelper(t *testing.T, svc *service.Service, username string) string {
	t.Helper()
	info, err := svc.CreateUser(t.Context(), &service.CreateUserRequest{
		Username: username,
		Password: "test-password-1234",
	})
	if err != nil {
		t.Fatalf("CreateUser(%q): %v", username, err)
	}
	return info.ID
}

// createSuperadminHelper inserts a superadmin via CreateSetupUser.
func createSuperadminHelper(t *testing.T, svc *service.Service, username string) string {
	t.Helper()
	info, err := svc.CreateSetupUser(t.Context(), &service.CreateUserRequest{
		Username: username,
		Password: "test-password-1234",
	})
	if err != nil {
		t.Fatalf("CreateSetupUser(%q): %v", username, err)
	}
	return info.ID
}

// createPermHelper creates a Permission bundle and assigns it to the given user.
func createPermAndAssign(t *testing.T, svc *service.Service, userID, key, name string, caps []string) {
	t.Helper()
	ctx := t.Context()
	perm, err := svc.CreatePermission(ctx, &service.CreatePermissionRequest{
		Key:  key,
		Name: name,
		Keys: caps,
	})
	if err != nil {
		t.Fatalf("CreatePermission: %v", err)
	}
	if err := svc.SetUserPermissions(ctx, userID, []string{perm.ID}); err != nil {
		t.Fatalf("SetUserPermissions: %v", err)
	}
}

// ---- ResolveUserCapabilityKeys ----

func TestResolveEmptyUsername(t *testing.T) {
	svc := newTestService(t)

	keys, isSuper, source, err := svc.ResolveUserCapabilityKeys(t.Context(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isSuper || len(keys) != 0 || source != service.PermissionSourceNone {
		t.Errorf("got keys=%v isSuper=%v source=%q, want empty/none", keys, isSuper, source)
	}
}

func TestResolveSystemUsername(t *testing.T) {
	svc := newTestService(t)

	_, _, source, err := svc.ResolveUserCapabilityKeys(t.Context(), "system")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if source != service.PermissionSourceNone {
		t.Errorf("system sentinel should resolve to none, got %q", source)
	}
}

func TestResolveBuiltinUserWithNoPermissions(t *testing.T) {
	svc := newTestService(t)
	createUserHelper(t, svc, "alice")

	keys, isSuper, source, err := svc.ResolveUserCapabilityKeys(t.Context(), "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isSuper {
		t.Error("alice should not be superadmin")
	}
	if len(keys) != 0 {
		t.Errorf("want empty keys, got %v", keys)
	}
	if source != service.PermissionSourceBuiltin {
		t.Errorf("want source=builtin, got %q", source)
	}
}

func TestResolveBuiltinUserWithPermissions(t *testing.T) {
	svc := newTestService(t)
	userID := createUserHelper(t, svc, "alice")
	createPermAndAssign(t, svc, userID, "editor", "Editor",
		[]string{service.CapFilesRead, service.CapFilesWrite})

	keys, isSuper, source, err := svc.ResolveUserCapabilityKeys(t.Context(), "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isSuper {
		t.Error("alice should not be superadmin")
	}
	if source != service.PermissionSourceBuiltin {
		t.Errorf("want source=builtin, got %q", source)
	}
	if !slices.Contains(keys, service.CapFilesRead) || !slices.Contains(keys, service.CapFilesWrite) {
		t.Errorf("want files.read + files.write, got %v", keys)
	}
}

func TestResolveBuiltinSuperadminGetsAllKnownKeys(t *testing.T) {
	svc := newTestService(t)
	createSuperadminHelper(t, svc, "root")

	keys, isSuper, source, err := svc.ResolveUserCapabilityKeys(t.Context(), "root")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isSuper {
		t.Error("root should be superadmin")
	}
	if source != service.PermissionSourceBuiltin {
		t.Errorf("want source=builtin, got %q", source)
	}
	if len(keys) != len(service.KnownCapabilityKeys()) {
		t.Errorf("superadmin should receive all %d known keys, got %d",
			len(service.KnownCapabilityKeys()), len(keys))
	}
	for _, want := range service.KnownCapabilityKeys() {
		if !slices.Contains(keys, want) {
			t.Errorf("missing capability %q for superadmin", want)
		}
	}
}

func TestResolveUnknownUserReturnsNone(t *testing.T) {
	svc := newTestService(t)

	// No users table entry.
	keys, isSuper, source, err := svc.ResolveUserCapabilityKeys(t.Context(), "bob")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isSuper || len(keys) != 0 {
		t.Errorf("want empty, got keys=%v isSuper=%v", keys, isSuper)
	}
	if source != service.PermissionSourceNone {
		t.Errorf("want source=none, got %q", source)
	}
}

// ---- CheckPermission ----

func TestCheckPermissionBuiltinProgressiveUnrestricted(t *testing.T) {
	svc := newTestService(t)
	createUserHelper(t, svc, "alice")

	// No Permission rows mention files.read at all, so progressive restriction
	// should treat it as unrestricted and allow alice through.
	err := svc.CheckPermission(t.Context(), "alice", service.CapFilesRead)
	if err != nil {
		t.Errorf("want nil (progressive allow), got %v", err)
	}
}

func TestCheckPermissionBuiltinRestrictedUserMissing(t *testing.T) {
	svc := newTestService(t)
	aliceID := createUserHelper(t, svc, "alice")
	createUserHelper(t, svc, "bob") // bob exists but has nothing

	// Create a Permission that mentions files.read but only assign it to alice.
	createPermAndAssign(t, svc, aliceID, "reader", "Reader", []string{service.CapFilesRead})

	// alice: allowed.
	if err := svc.CheckPermission(t.Context(), "alice", service.CapFilesRead); err != nil {
		t.Errorf("alice should be allowed, got %v", err)
	}

	// bob: the key is "known" to the system now, so progressive restriction
	// kicks in and denies since bob doesn't have it.
	err := svc.CheckPermission(t.Context(), "bob", service.CapFilesRead)
	if !errors.Is(err, service.ErrForbidden) {
		t.Errorf("bob should be forbidden, got %v", err)
	}
}

func TestCheckPermissionBuiltinSuperadminAllows(t *testing.T) {
	svc := newTestService(t)
	createSuperadminHelper(t, svc, "root")

	// Superadmin passes every check even when no permissions are configured.
	for _, cap := range service.KnownCapabilityKeys() {
		if err := svc.CheckPermission(t.Context(), "root", cap); err != nil {
			t.Errorf("superadmin denied for %q: %v", cap, err)
		}
	}
}

func TestCheckPermissionNoConfigAllowAll(t *testing.T) {
	svc := newTestService(t)
	// No users, no permissions. Current "unrestricted" behavior.
	for _, cap := range service.KnownCapabilityKeys() {
		if err := svc.CheckPermission(t.Context(), "random", cap); err != nil {
			t.Errorf("no-config: want allow for %q, got %v", cap, err)
		}
	}
}

// ---- Capabilities constants ----

func TestKnownCapabilityKeysCount(t *testing.T) {
	keys := service.KnownCapabilityKeys()
	if len(keys) != len(service.KnownCapabilities) {
		t.Errorf("KnownCapabilityKeys() returned %d, want %d",
			len(keys), len(service.KnownCapabilities))
	}
	expected := []string{
		service.CapFilesRead,
		service.CapFilesWrite,
		service.CapSettingsManage,
		service.CapTokensManage,
		service.CapUsersManage,
		service.CapPermissionsManage,
	}
	for _, e := range expected {
		if !slices.Contains(keys, e) {
			t.Errorf("missing known capability %q", e)
		}
	}
}
