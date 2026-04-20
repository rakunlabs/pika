package service

import (
	"testing"
)

func TestMigrateLegacyAuthSettings_ForwardAuth(t *testing.T) {
	legacy := &Settings{
		ForwardAuth: &ForwardAuthSettings{
			Enabled: true,
			Address: "http://traefik/auth",
		},
		ExternalPermissions: &ExternalPermissionsSettings{
			Enabled:      true,
			GroupsHeader: "X-Groups",
			Mapping:      map[string][]string{"editors": {"files.read", "files.write"}},
			Superadmins:  []string{"alice"},
		},
	}

	out := MigrateLegacyAuthSettings(legacy)

	if out.Auth == nil {
		t.Fatal("Auth should be populated")
	}
	if out.Auth.Header == nil {
		t.Fatal("Header strategy should be set from ForwardAuth")
	}
	if out.Auth.Header.Groups != "X-Groups" {
		t.Errorf("Groups header: got %q", out.Auth.Header.Groups)
	}
	if len(out.Auth.Capabilities.RoleMapping) != 1 {
		t.Errorf("RoleMapping size: got %d", len(out.Auth.Capabilities.RoleMapping))
	}
	if out.Auth.Capabilities.RoleMapping["editors"][0] != "files.read" {
		t.Errorf("RoleMapping content wrong: %+v", out.Auth.Capabilities.RoleMapping)
	}
	if len(out.Auth.Capabilities.Superadmins) != 1 || out.Auth.Capabilities.Superadmins[0] != "alice" {
		t.Errorf("Superadmins not migrated: %+v", out.Auth.Capabilities.Superadmins)
	}
	if out.ForwardAuth != nil || out.ExternalPermissions != nil {
		t.Error("legacy fields should be cleared after migration")
	}
}

func TestMigrateLegacyAuthSettings_NoOp(t *testing.T) {
	s := &Settings{Auth: &AuthSettings{}}
	out := MigrateLegacyAuthSettings(s)
	if out.Auth != s.Auth {
		t.Error("idempotent migration should return same Auth pointer")
	}
}

func TestMigrateLegacyAuthSettings_Empty(t *testing.T) {
	out := MigrateLegacyAuthSettings(&Settings{})
	if out.Auth != nil {
		t.Error("empty settings should not create Auth")
	}
}
