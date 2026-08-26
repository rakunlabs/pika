package bw

import (
	"context"
	"testing"
	"time"
)

func TestGetUserPermissionsIgnoresLegacyExternalGrants(t *testing.T) {
	ctx := context.Background()
	store, err := New(ctx, &Config{InMemory: true})
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close storage: %v", err)
		}
	})

	now := time.Now().UTC()
	if err := store.permissions.Insert(ctx, &permissionRow{
		ID: "local-permission", Key: "local", Name: "Local", CreatedAt: now,
	}); err != nil {
		t.Fatalf("insert local permission: %v", err)
	}
	if err := store.permissions.Insert(ctx, &permissionRow{
		ID: "external-permission", Key: "external", Name: "External", CreatedAt: now,
	}); err != nil {
		t.Fatalf("insert external permission: %v", err)
	}
	if err := store.users.Insert(ctx, &userRow{
		ID: "user-1", Username: "alice", CreatedAt: now, UpdatedAt: now,
		Grants: []userGrant{
			{PermissionID: "local-permission", Source: "local"},
			{PermissionID: "external-permission", Source: "legacy-sync"},
		},
	}); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	permissions, err := store.Permissions().GetUserPermissions(ctx, "user-1")
	if err != nil {
		t.Fatalf("get user permissions: %v", err)
	}
	if len(permissions) != 1 || permissions[0].ID != "local-permission" {
		t.Fatalf("permissions = %+v; want only local permission", permissions)
	}
}
