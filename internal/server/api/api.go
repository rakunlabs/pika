package api

import (
	"net"

	"github.com/rakunlabs/ada"

	pcluster "github.com/rakunlabs/pika/internal/cluster"
	"github.com/rakunlabs/pika/internal/config"
	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/secret"
	"github.com/rakunlabs/pika/internal/server/authx"
	"github.com/rakunlabs/pika/internal/server/publicendpoint"
	"github.com/rakunlabs/pika/internal/server/servertls"
	"github.com/rakunlabs/pika/internal/service"
)

// Info holds server metadata returned by the info endpoint.
type Info struct {
	Name              string `json:"name"`
	Version           string `json:"version"`
	Commit            string `json:"commit,omitempty"`
	Date              string `json:"date,omitempty"`
	ManagedTLSEnabled bool   `json:"managed_tls_enabled"`

	// EncryptionConfigInvalid is true when the process was started
	// with a non-empty `encryption.password` in config but that
	// passphrase did NOT match the on-disk verifier. The server
	// stays locked in that case (the operator must unlock manually
	// through the UnlockScreen) and the SPA renders a warning in
	// the UnlockScreen pointing at the misconfigured config field.
	//
	// Set once at boot in cmd/pika/main.go; never mutated afterwards.
	// The /api/v1/info endpoint is on the lockgate allowlist so the
	// SPA can read this even while the server is still locked.
	EncryptionConfigInvalid bool `json:"encryption_config_invalid,omitempty"`
}

type api struct {
	svc             *service.Service
	info            Info
	encStore        *secret.Storage         // nil if encryption is disabled
	mgr             *authx.Manager          // auth manager (login/logout/cap resolution)
	dispatcher      *hook.Dispatcher        // hook event bus; nil only in tests
	cluster         *pcluster.Cluster       // nil only in tests or custom embeddings
	publicEndpoints *publicendpoint.Manager // nil only in tests
	tlsMgr          *servertls.Manager      // nil only in tests
	trustedProxies  []*net.IPNet            // proxies whose X-Forwarded-For is honoured
}

// Muxes groups the route trees the API registers on.
type Muxes struct {
	// Protected requires an authenticated session and resolved capabilities.
	Protected *ada.Mux
	// Data serves the consumer-facing /data/* endpoint (token auth).
	Data *ada.Mux
	// Public is reachable without authentication.
	Public *ada.Mux
}

// Deps carries the collaborators the API handlers need. Only Svc and Mgr
// are required; the rest may be nil in tests.
type Deps struct {
	Svc             *service.Service
	Info            Info
	EncStore        *secret.Storage
	Mgr             *authx.Manager
	Dispatcher      *hook.Dispatcher
	Cluster         *pcluster.Cluster
	PublicEndpoints *publicendpoint.Manager
	TLS             *servertls.Manager

	// RateLimit configures the brute-force guard on secret-verifying
	// endpoints (server key unlock). nil disables it.
	RateLimit      *service.AuthRateLimitSettings
	TrustedProxies []*net.IPNet
	// BasePath is stripped from audited paths.
	BasePath string
}

func Handle(mux Muxes, deps Deps) error {
	// Set hook service identification from config
	hook.ServiceName = config.ServiceName
	hook.Version = config.Version

	m, mData, mAuth := mux.Protected, mux.Data, mux.Public

	m.Use(auditMiddleware(deps.Svc, deps.BasePath, deps.TrustedProxies))

	api := &api{
		svc:             deps.Svc,
		info:            deps.Info,
		encStore:        deps.EncStore,
		mgr:             deps.Mgr,
		dispatcher:      deps.Dispatcher,
		cluster:         deps.Cluster,
		publicEndpoints: deps.PublicEndpoints,
		tlsMgr:          deps.TLS,
		trustedProxies:  deps.TrustedProxies,
	}

	m.ErrorHandler(api.errorHandler)

	mData.ErrorHandler(api.errorHandler)
	// Data endpoint — consumer-facing, returns resolved config (with token auth)
	mData.GET("/data/*", mData.Wrap(api.getData))

	mAuth.ErrorHandler(api.errorHandler)

	// Per-user (self) endpoints. These live on the authenticated mux but
	// require no capability — every logged-in user owns their own
	// preference document. The /me/* namespace is reserved for additional
	// user-self resources (password change, personal vault, ...).
	m.GET("/api/v1/me/preferences", m.Wrap(api.getMyPreferences))
	m.PUT("/api/v1/me/preferences", m.Wrap(api.updateMyPreferences))
	m.DELETE("/api/v1/me/preferences", m.Wrap(api.resetMyPreferences))

	// Passkey self-service: enroll, list, rename, delete. Scoped to the
	// calling user and optionally restricted to superadmins by auth settings.
	// The service layer verifies ownership on every
	// rename/delete so an attacker who guesses another user's credential
	// id can't act on it. Begin/finish enrollment lives on the same /me
	// namespace because it's a per-user action; the actual login
	// ceremony goes through ada's strategy mux instead (see authx).
	m.POST("/api/v1/me/passkeys/begin", m.Wrap(api.withAccountSecurityAccess(api.beginPasskeyEnroll)))
	m.POST("/api/v1/me/passkeys/finish", m.Wrap(api.withAccountSecurityAccess(api.finishPasskeyEnroll)))
	m.GET("/api/v1/me/passkeys", m.Wrap(api.withAccountSecurityAccess(api.listMyPasskeys)))
	m.PATCH("/api/v1/me/passkeys/*", m.Wrap(api.withAccountSecurityAccess(api.renameMyPasskey)))
	m.DELETE("/api/v1/me/passkeys/*", m.Wrap(api.withAccountSecurityAccess(api.deleteMyPasskey)))

	// TOTP / 2FA self-service: status, enroll (begin/finish),
	// disable, regenerate recovery codes. The login-time step-up
	// verification is owned by ada's strategy mux via the MFA
	// wrapper in authx — not these endpoints. These only manage the
	// enrollment lifecycle. Auth settings may restrict them to superadmins.
	m.GET("/api/v1/me/totp", m.Wrap(api.withAccountSecurityAccess(api.getMyTOTPStatus)))
	m.POST("/api/v1/me/totp/begin", m.Wrap(api.withAccountSecurityAccess(api.beginMyTOTPEnroll)))
	m.POST("/api/v1/me/totp/finish", m.Wrap(api.withAccountSecurityAccess(api.finishMyTOTPEnroll)))
	m.DELETE("/api/v1/me/totp", m.Wrap(api.withAccountSecurityAccess(api.disableMyTOTP)))
	m.POST("/api/v1/me/totp/recovery-codes", m.Wrap(api.withAccountSecurityAccess(api.regenerateMyTOTPRecoveryCodes)))

	// Personal vault self-service. Every endpoint is scoped to the
	// calling user — the server resolves user_id from the session,
	// not from any path param. Returns 503 when the vault feature
	// isn't wired (s.VaultCoord() == nil) so the SPA can hide the
	// /vault route entirely instead of surfacing noisy errors.
	//
	// Account lifecycle: status (always 200), account (404 when
	// not initialized), setup (one-shot, 409 on re-init),
	// unlock-check (rate-limited), rotate-password, recovery-kit,
	// session-lock, reset (destructive).
	m.GET("/api/v1/me/vault/status", m.Wrap(api.getMyVaultStatus))
	m.GET("/api/v1/me/vault/account", m.Wrap(api.getMyVaultAccount))
	m.POST("/api/v1/me/vault/setup", m.Wrap(api.setupMyVault))
	m.POST("/api/v1/me/vault/unlock-check", m.Wrap(api.unlockMyVaultCheck))
	m.POST("/api/v1/me/vault/rotate-password", m.Wrap(api.rotateMyVaultMasterPassword))
	m.POST("/api/v1/me/vault/recovery-kit", m.Wrap(api.regenerateMyVaultRecoveryKit))
	m.PUT("/api/v1/me/vault/session-lock", m.Wrap(api.setMyVaultSessionLock))
	m.DELETE("/api/v1/me/vault", m.Wrap(api.resetMyVault))

	// Items: standard CRUD, soft-delete + purge via DELETE with
	// ?purge=true, restore, touch (last-used-at), versions list.
	// PathValue("*") is the item id; the wildcard convention matches
	// /users/* and the rest of the API so route parsing is uniform.
	m.GET("/api/v1/me/vault/items", m.Wrap(api.listMyVaultItems))
	m.POST("/api/v1/me/vault/items", m.Wrap(api.createMyVaultItem))
	m.GET("/api/v1/me/vault/items/*", m.Wrap(api.getMyVaultItem))
	m.PUT("/api/v1/me/vault/items/*", m.Wrap(api.updateMyVaultItem))
	m.DELETE("/api/v1/me/vault/items/*", m.Wrap(api.softDeleteMyVaultItem))
	m.POST("/api/v1/me/vault/items-restore/*", m.Wrap(api.restoreMyVaultItem))
	m.POST("/api/v1/me/vault/items-use/*", m.Wrap(api.touchMyVaultItem))
	m.GET("/api/v1/me/vault/items-versions/*", m.Wrap(api.listMyVaultItemVersions))

	// User management endpoints.
	m.GET("/api/v1/users", m.Wrap(api.withPerm(service.CapUsersManage, api.listUsers)))
	m.POST("/api/v1/users", m.Wrap(api.withPerm(service.CapUsersManage, api.createUser)))
	m.GET("/api/v1/users/*", m.Wrap(api.withPerm(service.CapUsersManage, api.getUser)))
	m.PATCH("/api/v1/users/*", m.Wrap(api.withPerm(service.CapUsersManage, api.updateUser)))
	m.DELETE("/api/v1/users/*", m.Wrap(api.withPerm(service.CapUsersManage, api.deleteUser)))
	m.POST("/api/v1/users-kick/*", m.Wrap(api.withPerm(service.CapUsersManage, api.kickUser)))
	// Admin TOTP reset: the escape hatch when a user has lost both
	// their authenticator and their recovery codes. Sibling path
	// (not nested under /users/{id}/totp) because the /users/*
	// wildcard catches everything under it — same convention as
	// /users-kick. Idempotent on the server side.
	m.DELETE("/api/v1/users-totp/*", m.Wrap(api.withPerm(service.CapUsersManage, api.adminResetUserTOTP)))

	// Permission bundle management.
	m.GET("/api/v1/permissions", m.Wrap(api.withPerm(service.CapPermissionsManage, api.listPermissions)))
	m.POST("/api/v1/permissions", m.Wrap(api.withPerm(service.CapPermissionsManage, api.createPermission)))
	m.PATCH("/api/v1/permissions/*", m.Wrap(api.withPerm(service.CapPermissionsManage, api.updatePermission)))
	m.DELETE("/api/v1/permissions/*", m.Wrap(api.withPerm(service.CapPermissionsManage, api.deletePermission)))

	// User permission assignment endpoints.
	m.GET("/api/v1/user-permissions/*", m.Wrap(api.withPerm(service.CapPermissionsManage, api.getUserPermissions)))
	m.PUT("/api/v1/user-permissions/*", m.Wrap(api.withPerm(service.CapPermissionsManage, api.setUserPermissions)))

	// Effective-permission introspection + per-user session/deny control.
	// Named-param routes ({user}/{handle}) sit on sibling prefixes so the
	// /users/* wildcard doesn't swallow them (same reasoning as
	// /users-kick). The raw session ID is never exposed — revocation keys
	// off a hash handle resolved server-side.
	m.GET("/api/v1/users-effective/{user}", m.Wrap(api.withPerm(service.CapUsersManage, api.getUserEffectivePermissions)))
	m.GET("/api/v1/users-identities/{user}", m.Wrap(api.withPerm(service.CapUsersManage, api.getUserIdentities)))
	m.GET("/api/v1/users-sessions/{user}", m.Wrap(api.withPerm(service.CapUsersManage, api.listUserSessions)))
	m.DELETE("/api/v1/users-sessions/{user}/{handle}", m.Wrap(api.withPerm(service.CapUsersManage, api.revokeUserSession)))
	m.PUT("/api/v1/users-denied/{user}", m.Wrap(api.withPerm(service.CapPermissionsManage, api.setUserDeniedPermissions)))

	// Folder routes — directory listing reads use the ancestor variant so
	// users granted only a deep pattern (e.g. configs/team-a/**) can still
	// navigate the root and intermediate directories. Writes require the
	// path itself to match a pattern.
	m.GET("/api/v1/folder", m.Wrap(api.withPermPath(service.CapFilesRead, pathFromWildcard, true, api.getFolder)))
	m.GET("/api/v1/folder/*", m.Wrap(api.withPermPath(service.CapFilesRead, pathFromWildcard, true, api.getFolder)))
	m.POST("/api/v1/folder/*", m.Wrap(api.withPermPath(service.CapFilesWrite, pathFromWildcard, false, api.postFolder)))
	m.DELETE("/api/v1/folder/*", m.Wrap(api.withPermPath(service.CapFilesWrite, pathFromWildcard, false, api.deleteFolder)))

	m.GET("/api/v1/file/*", m.Wrap(api.withPermPath(service.CapFilesRead, pathFromWildcard, false, api.getFile)))
	m.POST("/api/v1/file/*", m.Wrap(api.withPermPath(service.CapFilesWrite, pathFromWildcard, false, api.postFile)))
	m.DELETE("/api/v1/file/*", m.Wrap(api.withPermPath(service.CapFilesWrite, pathFromWildcard, false, api.deleteFile)))

	// File versions endpoint
	m.GET("/api/v1/versions/*", m.Wrap(api.withPermPath(service.CapFilesRead, pathFromWildcard, false, api.getFileVersions)))
	m.PATCH("/api/v1/versions/*", m.Wrap(api.withPermPath(service.CapFilesWrite, pathFromWildcard, false, api.patchFileVersion)))

	// Variant endpoints
	m.GET("/api/v1/variants/*", m.Wrap(api.withPermPath(service.CapFilesRead, pathFromWildcard, false, api.listVariants)))

	// Render endpoint — resolves inheritance and variations for preview
	m.POST("/api/v1/render/*", m.Wrap(api.withPermPath(service.CapFilesRead, pathFromWildcard, false, api.renderFile)))

	// Token management endpoints
	m.GET("/api/v1/tokens", m.Wrap(api.withPerm(service.CapTokensManage, api.listTokens)))
	m.POST("/api/v1/tokens", m.Wrap(api.withPerm(service.CapTokensManage, api.createToken)))
	m.DELETE("/api/v1/tokens/*", m.Wrap(api.withPerm(service.CapTokensManage, api.deleteToken)))
	m.PATCH("/api/v1/tokens/*", m.Wrap(api.withPerm(service.CapTokensManage, api.patchToken)))

	// Format conversion endpoint
	m.POST("/api/v1/convert", m.Wrap(api.withPerm(service.CapFilesRead, api.convertFormat)))

	// Search endpoint (SSE streaming) — not wrapped with withPerm since it is a
	// raw http.Handler, not an ada handler. The check is inlined at the top
	// of searchHandler instead.
	m.GET("/api/v1/search", api.searchHandler)

	// MCP is routed by mcpsrv.Endpoint before the mux so its path and
	// authentication policy can change without restarting the server.

	// Server-key lifecycle endpoints.
	//
	// Auth model:
	//   - GET  /api/v1/key/status     — public probe (lives on mAuth
	//       so the SPA can fetch it even before login; also on the
	//       lockgate allowlist so a locked server still answers it).
	//   - POST /api/v1/key/initialize — protected + CapSettingsManage.
	//       Used AFTER the operator has logged in and decided to turn
	//       on at-rest encryption. The fresh-install path is "log in
	//       to the running plaintext server, then opt in to encryption
	//       here". Service-level guard (one-shot verifier) still
	//       refuses re-init.
	//   - POST /api/v1/key/unlock     — protected + CapSettingsManage,
	//       also lockgate-allowlisted so a logged-in admin can reach
	//       it while the rest of the API 503s. Pre-locked-state
	//       restart flow: admin logs in (auth still works) → unlock.
	//   - POST /api/v1/key/lock       — protected + CapSettingsManage.
	//   - POST /api/v1/key/rotate     — protected + CapSettingsManage.
	//
	// API-only automation (no UI session) is still supported: an API
	// token holding settings.manage can call any of these. That
	// covers the curl/post-restart scripted unlock case.
	mAuth.GET("/api/v1/key/status", mAuth.Wrap(api.getKeyStatus))
	m.POST("/api/v1/key/initialize", m.Wrap(api.withPerm(service.CapSettingsManage, api.postKeyInitialize)))
	m.POST("/api/v1/key/unlock", m.Wrap(api.withPerm(service.CapSettingsManage, api.postKeyUnlock)),
		authx.UnlockGuard("server-key", deps.RateLimit, deps.TrustedProxies))
	m.POST("/api/v1/key/lock", m.Wrap(api.withPerm(service.CapSettingsManage, api.postKeyLock)))
	m.POST("/api/v1/key/rotate", m.Wrap(api.withPerm(service.CapSettingsManage, api.postKeyRotate)))

	m.GET("/api/v1/tls/status", m.Wrap(api.withPerm(service.CapSettingsManage, api.getTLSStatus)))
	m.POST("/api/v1/tls/self-signed", m.Wrap(api.withPerm(service.CapSettingsManage, api.generateManagedTLS)))
	m.PUT("/api/v1/tls/manual", m.Wrap(api.withPerm(service.CapSettingsManage, api.uploadManagedTLS)))
	m.POST("/api/v1/tls-generate", m.Wrap(api.withPerm(service.CapSettingsManage, api.generateTLS)))
	m.POST("/api/v1/ssh-keygen", m.Wrap(api.withPerm(service.CapSettingsManage, api.generateSSHKey)))

	// Audit log (persisted record of state-changing requests).
	m.GET("/api/v1/audit", m.Wrap(api.withPerm(service.CapSettingsManage, api.listAudit)))
	m.GET("/api/v1/audit/retention", m.Wrap(api.withPerm(service.CapSettingsManage, api.getAuditRetention)))

	// Settings
	m.GET("/api/v1/settings", m.Wrap(api.withPerm(service.CapSettingsManage, api.getSettings)))
	m.POST("/api/v1/settings", m.Wrap(api.withPerm(service.CapSettingsManage, api.postSettings)))
	m.GET("/api/v1/cluster/status", m.Wrap(api.withPerm(service.CapSettingsManage, api.getClusterStatus)))

	// Public endpoints diagnostics. The endpoint configurations
	// themselves are persisted through the settings round-trip
	// (POST /api/v1/settings carries the public_endpoints list);
	// these routes surface runtime state, a synthetic endpoint probe,
	// and a draft request-rule dry-run without leaving the page.
	m.GET("/api/v1/public-endpoints/status", m.Wrap(api.withPerm(service.CapSettingsManage, api.getPublicEndpointStatus)))
	m.POST("/api/v1/public-endpoints/test-rules", m.Wrap(api.withPerm(service.CapSettingsManage, api.testPublicEndpointRules)))
	m.POST("/api/v1/public-endpoints/{id}/test", m.Wrap(api.withPerm(service.CapSettingsManage, api.testPublicEndpoint)))

	// Backup & Restore. CapSettingsManage is the only gate — anyone
	// authorized to manage settings can already export the entire
	// DB, so no additional admin-secret step is required.
	m.GET("/api/v1/backup", m.Wrap(api.withPerm(service.CapSettingsManage, api.exportBackup)))
	m.GET("/api/v1/backup/info", m.Wrap(api.withPerm(service.CapSettingsManage, api.getBackupInfo)))
	m.POST("/api/v1/backup", m.Wrap(api.withPerm(service.CapSettingsManage, api.importBackup)))

	// External resource browsing — every operation here exposes the
	// shape of configured secret backends (or the secrets themselves
	// when reading), so the whole namespace is gated on settings
	// management. Resource name uses ada's {name} param syntax
	// rather than `*` because middle-segment `*` wildcards in this
	// router don't surface their captured segment via PathValue —
	// the value would silently come back empty. Named params do.
	m.GET("/api/v1/external/resources", m.Wrap(api.withPerm(service.CapExternalRead, api.listExternalResources)))
	m.GET("/api/v1/external/{name}/paths", m.Wrap(api.withPerm(service.CapExternalRead, api.listExternalPaths)))
	m.POST("/api/v1/external/{name}/test", m.Wrap(api.withPerm(service.CapExternalRead, api.testExternalResource)))
	m.POST("/api/v1/external/{name}/read", m.Wrap(api.withPerm(service.CapExternalRead, externalEntryHandler(api.readExternalEntry))))
	m.POST("/api/v1/external/{name}/write", m.Wrap(api.withPerm(service.CapExternalWrite, externalEntryHandler(api.writeExternalEntry))))
	m.POST("/api/v1/external/{name}/delete", m.Wrap(api.withPerm(service.CapExternalWrite, externalEntryHandler(api.deleteExternalEntry))))
	m.POST("/api/v1/external/{name}/versions", m.Wrap(api.withPerm(service.CapExternalRead, externalEntryHandler(api.listExternalVersions))))
	m.POST("/api/v1/external/{name}/version", m.Wrap(api.withPerm(service.CapExternalRead, externalEntryHandler(api.readExternalVersion))))
	m.GET("/api/v1/external/{name}/search", m.Wrap(api.withPerm(service.CapExternalRead, api.searchExternal)))
	// Bulk export is the one external route gated on
	// CapSettingsManage instead of CapExternalRead: a single read
	// leaks one secret, an export leaks the entire backend in one
	// file. It is surfaced only from Settings > External Resources.
	m.GET("/api/v1/external/{name}/export", m.Wrap(api.withPerm(service.CapSettingsManage, api.exportExternalResource)))

	// info and healthz are registered on the unprotected mux so the SPA
	// can always boot (even when forward-auth would redirect API calls).
	// The handler itself checks context for user identity and returns
	// appropriate info — full details when authenticated, minimal when not.
	mAuth.GET("/api/v1/info", mAuth.Wrap(api.infoHandler))
	mAuth.GET("/healthz", mAuth.Wrap(api.healthzHandler))

	return nil
}
