package service

// Capability keys are the leaf strings that route handlers check via withPerm.
// Each Permission record in the database is a named bundle of these keys,
// and the external-permissions mapping (for forward auth) translates external
// group names into these same keys.
//
// This file is the single source of truth for the capability vocabulary.
// Route registrations in internal/server/api/api.go use these constants and
// the /api/v1/info endpoint ships KnownCapabilities so the UI can render
// self-describing permission editors without duplicating the list.
const (
	CapFilesRead         = "files.read"
	CapFilesWrite        = "files.write"
	CapRawRead           = "raw.read"
	CapRawWrite          = "raw.write"
	CapExternalRead      = "external.read"
	CapExternalWrite     = "external.write"
	CapSettingsManage    = "settings.manage"
	CapTokensManage      = "tokens.manage"
	CapUsersManage       = "users.manage"
	CapPermissionsManage = "permissions.manage"
	// Proxy capabilities are split from settings.manage on purpose:
	// "view proxy + run requests against your own deployment" is a
	// safe operator-level action that doesn't grant the holder the
	// ability to change auth, secrets, mounts etc. — they're two
	// different blast radii.
	CapProxyRead   = "proxy.read"
	CapProxyManage = "proxy.manage"
)

// Capability describes a capability key for UI discovery.
type Capability struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// KnownCapabilities is the canonical list of capability keys the UI renders
// for permission bundles and external-group mapping. The order here is the
// order shown in the UI — keep it logical (read/write pairs together).
var KnownCapabilities = []Capability{
	{CapFilesRead, "Configurations Read", "View folders, files, versions, variants, render and search configurations"},
	{CapFilesWrite, "Configurations Write", "Create, update and delete folders and configuration files"},
	{CapRawRead, "Raw Files Read", "Browse and download raw files from mounted filesystems"},
	{CapRawWrite, "Raw Files Write", "Upload, delete, rename, copy and move raw files"},
	{CapExternalRead, "External Resources Read", "Browse, search and read entries from configured external resources (Vault, Consul, etcd, AWS, Azure, GCP, Kubernetes, HTTP, ...)"},
	{CapExternalWrite, "External Resources Write", "Create, update and delete entries on configured external resources"},
	{CapSettingsManage, "Settings Management", "View and modify server settings, admin secret, backup/restore, key rotation"},
	{CapProxyRead, "Proxy Read", "View configured proxy servers, their pipelines, live status and run test requests against them"},
	{CapProxyManage, "Proxy Manage", "Create, edit and delete proxy server graphs (listeners, middleware, handlers)"},
	{CapTokensManage, "Token Management", "Create, edit and revoke API access tokens"},
	{CapUsersManage, "User Management", "Create, edit, delete and kick users"},
	{CapPermissionsManage, "Permission Management", "Create, edit, delete permissions and assign them to users"},
}

// KnownCapabilityKeys returns just the capability key strings from KnownCapabilities.
// Superadmin grants the entire list; used both for user.is_superadmin and for
// the forward-auth Superadmins allowlist.
func KnownCapabilityKeys() []string {
	keys := make([]string, len(KnownCapabilities))
	for i, c := range KnownCapabilities {
		keys[i] = c.Key
	}
	return keys
}
