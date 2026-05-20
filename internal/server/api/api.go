package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"log/slog"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/config"
	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/rawfs/localfs"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/secret"
	"github.com/rakunlabs/pika/internal/serve/ftpserve"
	"github.com/rakunlabs/pika/internal/serve/sftpserve"
	"github.com/rakunlabs/pika/internal/serve/tftpserve"
	"github.com/rakunlabs/pika/internal/serve/webdavserve"
	"github.com/rakunlabs/pika/internal/server/authx"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/pika/internal/usersync"
)

// Info holds server metadata returned by the info endpoint.
type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
}

// ProxyReconciler is the narrow surface api needs from the proxy
// runner. server.go injects an *proxy.Manager value; defining the
// interface here keeps the api package free of an import on proxy
// (which would cycle: api -> proxy -> service -> api consumers).
type ProxyReconciler interface {
	Reconcile(servers []service.ProxyServer) error
	Validate(s service.ProxyServer) (any, error)
	Status() any
}

type api struct {
	svc           *service.Service
	info          Info
	encStore      *secret.Storage     // nil if encryption is disabled
	mgr           *authx.Manager      // auth manager (login/logout/cap resolution)
	rawHandler    *RawHandler         // nil if no raw mounts configured
	proxyMgr      ProxyReconciler     // nil until server.go injects via Handle
	syncScheduler *usersync.Scheduler // nil until set by server.go
	// registryMgr owns the per-(namespace, repo) routing table for
	// the artifact registry feature. nil until server.go calls
	// SetRegistryManager during boot. The data-mux entry handler
	// serveRegistry returns 404 when this is nil, matching pika's
	// existing "feature unconfigured" semantics for raw mounts and
	// proxy servers.
	registryMgr *registry.Manager
}

type response struct {
	Message string `json:"message,omitempty"`
}

func Handle(m *ada.Mux, mData *ada.Mux, mAuth *ada.Mux, svc *service.Service, info Info, encStore *secret.Storage, mgr *authx.Manager, rh *RawHandler, proxyMgr ProxyReconciler, registryMgr *registry.Manager) error {
	// Set hook service identification from config
	hook.ServiceName = config.ServiceName
	hook.Version = config.Version

	// User-sync scheduler runs LDAP sync sources on a per-source ticker.
	// Constructed here so the api handlers and the postSettings reload
	// path share one instance. Started below after the routes register.
	syncSched := usersync.NewScheduler(svc)

	api := &api{svc: svc, info: info, encStore: encStore, mgr: mgr, rawHandler: rh, proxyMgr: proxyMgr, syncScheduler: syncSched, registryMgr: registryMgr}

	// Perform the initial registry reload so the routing table is
	// hot the first time a client hits /registries/*. If the manager
	// has no factories registered (foundation phase), this is a
	// no-op aside from logging the empty table.
	if registryMgr != nil {
		registryMgr.Reload(context.Background(), svc.GetRegistrySettings(context.Background()))
	}

	m.ErrorHandler(api.errorHandler)

	mData.ErrorHandler(api.errorHandler)
	// Data endpoint — consumer-facing, returns resolved config (with token auth)
	mData.GET("/data/*", mData.Wrap(api.getData))

	// Raw file endpoints (with token auth)
	mData.GET("/raw/*", mData.Wrap(api.getRaw))
	mData.PUT("/raw/*", mData.Wrap(api.putRaw))
	mData.DELETE("/raw/*", mData.Wrap(api.deleteRaw))

	// Artifact registry endpoints. One catch-all per method because
	// every protocol (Go, NPM, Docker) needs both safe and unsafe
	// verbs. The serveRegistry handler parses {namespace}/{repo}
	// out of the path, enforces token+capability, then dispatches
	// to the per-repo Registry.
	mData.GET("/registries/*", mData.Wrap(api.serveRegistry))
	mData.HEAD("/registries/*", mData.Wrap(api.serveRegistry))
	mData.OPTIONS("/registries/*", mData.Wrap(api.serveRegistry))
	mData.POST("/registries/*", mData.Wrap(api.serveRegistry))
	mData.PUT("/registries/*", mData.Wrap(api.serveRegistry))
	mData.PATCH("/registries/*", mData.Wrap(api.serveRegistry))
	mData.DELETE("/registries/*", mData.Wrap(api.serveRegistry))

	mAuth.ErrorHandler(api.errorHandler)

	// Per-user (self) endpoints. These live on the authenticated mux but
	// require no capability — every logged-in user owns their own
	// preference document. The /me/* namespace is reserved for additional
	// user-self resources (password change, personal vault, ...).
	m.GET("/api/v1/me/preferences", m.Wrap(api.getMyPreferences))
	m.PUT("/api/v1/me/preferences", m.Wrap(api.updateMyPreferences))
	m.DELETE("/api/v1/me/preferences", m.Wrap(api.resetMyPreferences))

	// Passkey self-service: enroll, list, rename, delete. All scoped to
	// the calling user — the service layer verifies ownership on every
	// rename/delete so an attacker who guesses another user's credential
	// id can't act on it. Begin/finish enrollment lives on the same /me
	// namespace because it's a per-user action; the actual login
	// ceremony goes through ada's strategy mux instead (see authx).
	m.POST("/api/v1/me/passkeys/begin", m.Wrap(api.beginPasskeyEnroll))
	m.POST("/api/v1/me/passkeys/finish", m.Wrap(api.finishPasskeyEnroll))
	m.GET("/api/v1/me/passkeys", m.Wrap(api.listMyPasskeys))
	m.PATCH("/api/v1/me/passkeys/*", m.Wrap(api.renameMyPasskey))
	m.DELETE("/api/v1/me/passkeys/*", m.Wrap(api.deleteMyPasskey))

	// TOTP / 2FA self-service: status, enroll (begin/finish),
	// disable, regenerate recovery codes. The login-time step-up
	// verification is owned by ada's strategy mux via the MFA
	// wrapper in authx — not these endpoints. These only manage the
	// enrollment lifecycle.
	m.GET("/api/v1/me/totp", m.Wrap(api.getMyTOTPStatus))
	m.POST("/api/v1/me/totp/begin", m.Wrap(api.beginMyTOTPEnroll))
	m.POST("/api/v1/me/totp/finish", m.Wrap(api.finishMyTOTPEnroll))
	m.DELETE("/api/v1/me/totp", m.Wrap(api.disableMyTOTP))
	m.POST("/api/v1/me/totp/recovery-codes", m.Wrap(api.regenerateMyTOTPRecoveryCodes))

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
	m.POST("/api/v1/key/unlock", m.Wrap(api.withPerm(service.CapSettingsManage, api.postKeyUnlock)))
	m.POST("/api/v1/key/lock", m.Wrap(api.withPerm(service.CapSettingsManage, api.postKeyLock)))
	m.POST("/api/v1/key/rotate", m.Wrap(api.withPerm(service.CapSettingsManage, api.postKeyRotate)))

	m.POST("/api/v1/tls-generate", m.Wrap(api.withPerm(service.CapSettingsManage, api.generateTLS)))
	m.POST("/api/v1/ssh-keygen", m.Wrap(api.withPerm(service.CapSettingsManage, api.generateSSHKey)))

	// Settings
	m.GET("/api/v1/settings", m.Wrap(api.withPerm(service.CapSettingsManage, api.getSettings)))
	m.POST("/api/v1/settings", m.Wrap(api.withPerm(service.CapSettingsManage, api.postSettings)))

	// Artifact registry admin endpoints. listRegistryNamespaces and
	// listRegistryRepos are read-only (Registry Read or above);
	// putRegistrySettings replaces the entire registry tree and
	// gates on Registry Admin.
	m.GET("/api/v1/registries", m.Wrap(api.withPerm(service.CapRegistryRead, api.listRegistryNamespaces)))
	m.GET("/api/v1/registries/repos", m.Wrap(api.withPerm(service.CapRegistryRead, api.listRegistryRepos)))
	m.PUT("/api/v1/registries", m.Wrap(api.withPerm(service.CapRegistryAdmin, api.putRegistrySettings)))
	// Per-repo module browser for Go registries. Path params
	// `{ns}` and `{repo}` are extracted via ada's PathValue.
	m.GET("/api/v1/registries/go/{ns}/{repo}/modules", m.Wrap(api.withPerm(service.CapRegistryRead, api.listRegistryGoModules)))
	// Per-repo package browser for NPM registries.
	m.GET("/api/v1/registries/npm/{ns}/{repo}/packages", m.Wrap(api.withPerm(service.CapRegistryRead, api.listRegistryNPMPackages)))
	// Per-repo image/tag browser for Docker registries.
	m.GET("/api/v1/registries/docker/{ns}/{repo}/repos", m.Wrap(api.withPerm(service.CapRegistryRead, api.listRegistryDockerRepos)))
	// Per-repo chart browser for Helm registries.
	m.GET("/api/v1/registries/helm/{ns}/{repo}/charts", m.Wrap(api.withPerm(service.CapRegistryRead, api.listRegistryHelmCharts)))
	// Per-repo artifact browser for Maven registries.
	m.GET("/api/v1/registries/maven/{ns}/{repo}/artifacts", m.Wrap(api.withPerm(service.CapRegistryRead, api.listRegistryMavenArtifacts)))
	// Per-repo package browser for PyPI registries.
	m.GET("/api/v1/registries/pypi/{ns}/{repo}/packages", m.Wrap(api.withPerm(service.CapRegistryRead, api.listRegistryPyPIPackages)))
	// Per-repo crate browser for Cargo registries.
	m.GET("/api/v1/registries/cargo/{ns}/{repo}/crates", m.Wrap(api.withPerm(service.CapRegistryRead, api.listRegistryCargoCrates)))
	// Docker GC trigger (mark-and-sweep). Admin only because it
	// deletes content from the underlying blob store.
	m.POST("/api/v1/registries/docker/{ns}/{repo}/gc", m.Wrap(api.withPerm(service.CapRegistryAdmin, api.runDockerGC)))
	// Docker GC estimate (dry-run). Read-gated because it doesn't
	// delete anything — just reports how much would be reclaimed.
	m.GET("/api/v1/registries/docker/{ns}/{repo}/gc/estimate", m.Wrap(api.withPerm(service.CapRegistryRead, api.estimateDockerGC)))
	// Cache purge for Remote registries (Go / NPM / Docker share
	// one handler — the {type} segment lets the UI hit a clean URL
	// per protocol and lets the handler reject Local registries
	// with a helpful 400). Admin-gated because it forces an
	// upstream re-fetch that may be expensive.
	m.POST("/api/v1/registries/{type}/{ns}/{repo}/purge", m.Wrap(api.withPerm(service.CapRegistryAdmin, api.runRegistryPurge)))
	// Snapshot statistics for a single registry. Read-only,
	// CapRegistryRead is enough — the UI uses this for the
	// "Statistics" card in the repo detail panel.
	m.GET("/api/v1/registries/{type}/{ns}/{repo}/stats", m.Wrap(api.withPerm(service.CapRegistryRead, api.getRegistryStats)))
	// Per-package detail. The {name...} wildcard catches the rest
	// of the path so Go ("example.com/foo/bar"), NPM scoped
	// ("@scope/pkg"), and Docker ("library/nginx") names all
	// route into the same handler.
	m.GET("/api/v1/registries/{type}/{ns}/{repo}/packages/{name...}", m.Wrap(api.withPerm(service.CapRegistryRead, api.getRegistryPackageDetail)))
	// NPM-only: cached README markdown for the package's latest
	// version. Falls back to a lazy tarball extract when the
	// cache is empty.
	m.GET("/api/v1/registries/npm/{ns}/{repo}/packages/{name}/readme", m.Wrap(api.withPerm(service.CapRegistryRead, api.getNPMPackageReadme)))
	// Go-only: raw go.mod bytes for one module version. Used by
	// the detail UI's go.mod viewer.
	m.GET("/api/v1/registries/go/{ns}/{repo}/modules/{path...}", m.Wrap(api.withPerm(service.CapRegistryRead, api.getGoModuleGoMod)))
	// Remote-only: connectivity probe against the configured
	// upstream. Admin-gated because the probe uses the registry's
	// real auth credentials.
	m.POST("/api/v1/registries/{type}/{ns}/{repo}/test-upstream", m.Wrap(api.withPerm(service.CapRegistryAdmin, api.runRegistryUpstreamProbe)))

	// Proxy: split into read vs. manage caps. Read covers the
	// dashboard, status panel, catalog discovery and the in-app
	// test request console; manage adds CRUD and validate. The
	// dedicated caps (rather than reusing settings.manage) let an
	// operator hand a teammate "see proxies, run tests" without
	// also handing them every other settings knob.
	m.GET("/api/v1/proxy", m.Wrap(api.withPerm(service.CapProxyRead, api.listProxyServers)))
	m.GET("/api/v1/proxy/catalog", m.Wrap(api.withPerm(service.CapProxyRead, api.getProxyCatalog)))
	m.GET("/api/v1/proxy/status", m.Wrap(api.withPerm(service.CapProxyRead, api.getProxyStatus)))
	m.GET("/api/v1/proxy/{id}", m.Wrap(api.withPerm(service.CapProxyRead, api.getProxyServer)))
	m.POST("/api/v1/proxy/test", m.Wrap(api.withPerm(service.CapProxyRead, api.proxyTest)))
	m.POST("/api/v1/proxy", m.Wrap(api.withPerm(service.CapProxyManage, api.createProxyServer)))
	m.PUT("/api/v1/proxy/{id}", m.Wrap(api.withPerm(service.CapProxyManage, api.updateProxyServer)))
	m.DELETE("/api/v1/proxy/{id}", m.Wrap(api.withPerm(service.CapProxyManage, api.deleteProxyServer)))
	m.POST("/api/v1/proxy/{id}/validate", m.Wrap(api.withPerm(service.CapProxyManage, api.validateProxyServer)))

	// Backup & Restore. CapSettingsManage is the only gate — anyone
	// authorized to manage settings can already export the entire
	// DB, so no additional admin-secret step is required.
	m.GET("/api/v1/backup", m.Wrap(api.withPerm(service.CapSettingsManage, api.exportBackup)))
	m.GET("/api/v1/backup/info", m.Wrap(api.withPerm(service.CapSettingsManage, api.getBackupInfo)))
	m.POST("/api/v1/backup", m.Wrap(api.withPerm(service.CapSettingsManage, api.importBackup)))

	// Raw filesystem browsing and management (for UI, uses session auth)
	m.GET("/api/v1/raw/*", m.Wrap(api.withPerm(service.CapRawRead, api.rawHandler.serveRaw)))
	m.PUT("/api/v1/raw/*", m.Wrap(api.withPerm(service.CapRawWrite, api.rawHandler.writeFile)))
	m.DELETE("/api/v1/raw/*", m.Wrap(api.withPerm(service.CapRawWrite, api.rawHandler.deleteFile)))
	m.POST("/api/v1/raw-mkdir/*", m.Wrap(api.withPerm(service.CapRawWrite, api.rawHandler.mkDir)))
	m.POST("/api/v1/raw-rename", m.Wrap(api.withPerm(service.CapRawWrite, api.rawHandler.renameFile)))
	m.POST("/api/v1/raw-copy", m.Wrap(api.withPerm(service.CapRawWrite, api.rawHandler.copyFile)))
	m.POST("/api/v1/raw-move", m.Wrap(api.withPerm(service.CapRawWrite, api.rawHandler.moveFile)))

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
	m.POST("/api/v1/external/{name}/read", m.Wrap(api.withPerm(service.CapExternalRead, api.readExternalEntry)))
	m.POST("/api/v1/external/{name}/write", m.Wrap(api.withPerm(service.CapExternalWrite, api.writeExternalEntry)))
	m.POST("/api/v1/external/{name}/delete", m.Wrap(api.withPerm(service.CapExternalWrite, api.deleteExternalEntry)))
	m.POST("/api/v1/external/{name}/versions", m.Wrap(api.withPerm(service.CapExternalRead, api.listExternalVersions)))
	m.POST("/api/v1/external/{name}/version", m.Wrap(api.withPerm(service.CapExternalRead, api.readExternalVersion)))
	m.GET("/api/v1/external/{name}/search", m.Wrap(api.withPerm(service.CapExternalRead, api.searchExternal)))

	// User-sync endpoints (LDAP and future drivers). The endpoints
	// inspect/run the scheduler that's owned by this api struct.
	// Gated by settings management since they touch credentials and
	// reshape user_permissions wholesale.
	m.GET("/api/v1/user-sync/status", m.Wrap(api.withPerm(service.CapSettingsManage, api.listUserSyncStatus)))
	m.POST("/api/v1/user-sync/run/*", m.Wrap(api.withPerm(service.CapSettingsManage, api.runUserSync)))
	m.POST("/api/v1/user-sync/test/*", m.Wrap(api.withPerm(service.CapSettingsManage, api.testUserSync)))

	// info and healthz are registered on the unprotected mux so the SPA
	// can always boot (even when forward-auth would redirect API calls).
	// The handler itself checks context for user identity and returns
	// appropriate info — full details when authenticated, minimal when not.
	mAuth.GET("/api/v1/info", mAuth.Wrap(api.infoHandler))
	mAuth.GET("/healthz", mAuth.Wrap(api.healthzHandler))

	// Boot the user-sync scheduler with current settings. Reload happens
	// inside postSettings whenever PatchSettings.UserSync is set.
	if curSettings, err := svc.Settings(rh.appCtx); err == nil {
		syncSched.Start(rh.appCtx, curSettings.UserSync)
	}

	return nil
}

func (a *api) errorHandler(c *ada.Context, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		c.SetStatus(http.StatusNotFound)
	case errors.Is(err, service.ErrBadRequest):
		c.SetStatus(http.StatusBadRequest)
	case errors.Is(err, service.ErrUnauthorized):
		c.SetStatus(http.StatusUnauthorized)
	case errors.Is(err, service.ErrForbidden):
		c.SetStatus(http.StatusForbidden)
	case errors.Is(err, service.ErrConflict):
		c.SetStatus(http.StatusConflict)
	case errors.Is(err, service.ErrInternal):
		c.SetStatus(http.StatusInternalServerError)
	default:
		c.SetStatus(http.StatusInternalServerError)
	}

	c.SendJSON(response{Message: err.Error()})
}

func (a *api) healthzHandler(c *ada.Context) error {
	return c.SetStatus(http.StatusOK).SendString("OK")
}

func (a *api) infoHandler(c *ada.Context) error {
	ctx := c.Request.Context()

	// This endpoint lives on the unprotected mux (mAuth) so the SPA can
	// always boot. Identify the caller via the auth manager which knows
	// how to resolve a session even when Require()/CapMiddleware() are
	// not in the chain (i.e. on the unprotected mux). This is the only
	// reliable way for the SPA to discover the current user's effective
	// capabilities before any protected route is hit.
	username := ""
	var caps []string
	isSuperadmin := false

	if a.mgr != nil {
		id, capKeys, resolvedUser, _, _ := a.mgr.ResolveRequest(c.Request)
		if id != nil {
			username = resolvedUser
			if username == "" {
				username = id.Subject
			}
			caps = capKeys
			// Superadmin equivalence: the cap resolver returns the full
			// known-key set for both local is_superadmin users and members
			// of the Superadmins allowlist — both should set is_superadmin
			// in the response so the UI can grant unconditional access.
			if len(caps) == len(service.KnownCapabilityKeys()) {
				isSuperadmin = true
			}
		}
	}

	// Fallback: protected-mux requests still have caps/user planted in
	// context by CapMiddleware. Honor those if ResolveRequest didn't
	// produce anything (defensive — shouldn't normally happen).
	if username == "" {
		username = service.UserFromContext(ctx)
	}
	if len(caps) == 0 {
		if c := service.CapabilitiesFromContext(ctx); len(c) > 0 {
			caps = []string(c)
			if len(caps) == len(service.KnownCapabilityKeys()) {
				isSuperadmin = true
			}
		}
	}

	if caps == nil {
		caps = []string{}
	}

	// Surface the editable branding subtitle from auth settings so the
	// authenticated UI (navbar) can render the same value the login
	// screen shows via /login/info. Reading from the same source
	// (settings DB > AuthSettings.UI.Subtitle) keeps the two views in
	// sync automatically when an operator edits the setting. nil is
	// treated as "not configured": Subtitle stays empty and omitempty
	// drops it from the JSON.
	var subtitle string
	if as := a.svc.GetAuthSettings(ctx); as != nil {
		subtitle = as.UI.Subtitle
	}

	// VaultEnabled mirrors a.svc.VaultEnabled(ctx) so the SPA can
	// gate the /vault link in the navigation. This combines the
	// boot-time gate (cmd/pika wiring) with the admin runtime
	// toggle in Settings → Security → Personal vault, so the link
	// disappears the moment an admin flips the toggle.
	vaultEnabled := a.svc.VaultEnabled(ctx)
	proxyEnabled := a.svc.ProxyEnabled(ctx)
	registryEnabled := a.svc.RegistryEnabled(ctx)

	resp := struct {
		Info
		Subtitle        string                  `json:"subtitle,omitempty"`
		User            string                  `json:"user,omitempty"`
		AuthEnabled     bool                    `json:"auth_enabled"`
		BuiltinAuth     bool                    `json:"builtin_auth"`
		IsSuperadmin    bool                    `json:"is_superadmin"`
		Permissions     []string                `json:"permissions"`
		Capabilities    []service.Capability    `json:"capabilities"`
		SetupRequired   bool                    `json:"setup_required,omitempty"`
		RawMounts       []MountInfo             `json:"raw_mounts,omitempty"`
		VaultEnabled    bool                    `json:"vault_enabled"`
		ProxyEnabled    bool                    `json:"proxy_enabled"`
		RegistryEnabled bool                    `json:"registry_enabled"`
		VaultItemTypes  []service.VaultItemType `json:"vault_item_types,omitempty"`
	}{
		Info:            a.info,
		Subtitle:        subtitle,
		User:            username,
		AuthEnabled:     true,
		BuiltinAuth:     true,
		IsSuperadmin:    isSuperadmin,
		Permissions:     caps,
		Capabilities:    service.KnownCapabilities,
		RawMounts:       a.rawHandler.MountsInfo(),
		VaultEnabled:    vaultEnabled,
		ProxyEnabled:    proxyEnabled,
		RegistryEnabled: registryEnabled,
		VaultItemTypes:  service.KnownVaultItemTypes,
	}

	// Fresh-install detection: no users exist yet.
	// The SPA uses this to route to the Setup page instead of Login.
	if username == "" {
		if count, err := a.svc.UserCount(ctx); err == nil && count == 0 {
			resp.SetupRequired = true
		}
	}

	return c.SetStatus(http.StatusOK).SendJSON(resp)
}

func (a *api) getSettings(c *ada.Context) error {
	settings, err := a.svc.Settings(c.Request.Context())
	if err != nil {
		return err
	}

	// Surface the effective auth config, not the raw DB row. When the
	// settings row has no auth.local block, the server's boot path
	// applies defaults (local strategy enabled); returning the bare DB
	// view would make the UI render "disabled" for the strategy the
	// user is literally using right now.
	if settings != nil {
		settings.Auth = settings.Auth.WithEffectiveDefaults()
	}

	return c.SetStatus(http.StatusOK).SendJSON(settings)
}

func (a *api) postSettings(c *ada.Context) error {
	var patchSettings service.PatchSettings
	if err := c.Bind(&patchSettings); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	var prevRegistry *service.RegistrySettings
	if patchSettings.Registry != nil {
		prevRegistry = a.svc.GetRegistrySettings(c.Request.Context())
	}

	if err := a.svc.PatchSettings(c.Request.Context(), &patchSettings); err != nil {
		return err
	}

	// If raw mounts were updated, reload them into the handler
	if patchSettings.RawMounts != nil {
		if err := a.reloadRawMounts(c.Request.Context()); err != nil {
			return err
		}
	}

	// If FTP shares or users were updated, reload them
	if patchSettings.FTPShares != nil {
		a.reloadFTPShares(c.Request.Context())
	}
	if patchSettings.FTPUsers != nil {
		a.reloadFTPUsers(c.Request.Context())
	}

	// If hooks were updated, reload them in the dispatcher
	if patchSettings.Hooks != nil {
		a.reloadHooks(c.Request.Context())
	}

	// If the registry tree was updated, rebuild the routing table.
	// Hot reload semantics match raw mounts: the new tree replaces
	// the old one atomically, in-flight requests against the old
	// handles drain naturally.
	if patchSettings.Registry != nil {
		a.reloadRegistry(c.Request.Context())
		a.emitRegistryDiff(prevRegistry, a.svc.GetRegistrySettings(c.Request.Context()))
	}

	// If serve settings were updated, reload the corresponding servers
	if patchSettings.FTPServe != nil || patchSettings.SFTPServe != nil || patchSettings.TFTPServe != nil || patchSettings.WebDAVServe != nil {
		settings, err := a.svc.Settings(c.Request.Context())
		if err != nil {
			slog.Error("failed to read settings for file server reload", "error", err)
		} else {
			if patchSettings.FTPServe != nil {
				a.reloadFTPServe(settings)
			}
			if patchSettings.SFTPServe != nil {
				a.reloadSFTPServe(settings)
			}
			if patchSettings.TFTPServe != nil {
				a.reloadTFTPServe(settings)
			}
			if patchSettings.WebDAVServe != nil {
				a.reloadWebDAVServe(settings)
			}
		}
	}

	// If proxy servers were updated OR the proxy feature flag was
	// flipped, reconcile the runner. Reading settings fresh covers
	// either trigger: the patch may contain only the Proxy toggle,
	// in which case we need the (unchanged) ProxyServers list, and
	// vice versa. When the deployment-wide proxy flag is off we
	// reconcile with an empty list so the manager stops everything
	// while leaving the graphs persisted for later.
	if a.proxyMgr != nil && (patchSettings.ProxyServers != nil || patchSettings.Proxy != nil) {
		settings, err := a.svc.Settings(c.Request.Context())
		if err != nil {
			slog.Error("read settings for proxy reload", "error", err)
		} else {
			desired := settings.ProxyServers
			if !a.svc.ProxyEnabled(c.Request.Context()) {
				desired = nil
			}
			if err := a.proxyMgr.Reconcile(desired); err != nil {
				slog.Error("proxy reconcile failed", "error", err)
			}
		}
	}

	// If auth settings were updated, reload the auth manager.
	// TODO: detect cookie/issuer changes and set restart_required in response.
	if patchSettings.Auth != nil && a.mgr != nil {
		if err := a.mgr.Reload(c.Request.Context(), patchSettings.Auth); err != nil {
			return fmt.Errorf("auth reload failed: %w", err)
		}
	}

	// If user-sync sources were updated, reload the scheduler so periodic
	// jobs pick up the new config (or get stopped, or fire for the first
	// time on a freshly-enabled source).
	if patchSettings.UserSync != nil && a.syncScheduler != nil {
		a.syncScheduler.Reload(patchSettings.UserSync)
	}

	return c.SetStatus(http.StatusOK).SendJSON(patchSettings)
}

// reloadHooks reads hooks from settings and updates the dispatcher.
func (a *api) reloadHooks(ctx context.Context) {
	settings, err := a.svc.Settings(ctx)
	if err != nil {
		slog.Error("failed to read settings for hook reload", "error", err)
		return
	}

	if a.rawHandler != nil && a.rawHandler.dispatcher != nil {
		a.rawHandler.dispatcher.UpdateHooks(settings.Hooks)
		slog.Info("hooks reloaded", "count", len(settings.Hooks))
	}
}

// reloadRawMounts reads the current settings and rebuilds mount entries.
func (a *api) reloadRawMounts(ctx context.Context) error {
	settings, err := a.svc.Settings(ctx)
	if err != nil {
		return fmt.Errorf("reading settings for raw mount reload: %w", err)
	}

	entries, errs := BuildMountEntries(settings.RawMounts)
	for _, e := range errs {
		slog.Warn("skipping invalid raw mount from settings", "error", e)
	}

	a.rawHandler.UpdateMounts(entries)
	return nil
}

// reloadFTPShares rebuilds shares and updates FTP, SFTP, and TFTP servers.
func (a *api) reloadFTPShares(ctx context.Context) {
	shares := BuildFTPShares(ctx, a.svc, a.rawHandler)

	a.rawHandler.mu.RLock()
	ftpSrv := a.rawHandler.ftpServer
	sftpSrv := a.rawHandler.sftpServer
	tftpSrv := a.rawHandler.tftpServer
	webdavSrv := a.rawHandler.webdavServer
	a.rawHandler.mu.RUnlock()

	if ftpSrv != nil {
		ftpSrv.UpdateShares(shares)
	}
	if sftpSrv != nil {
		sftpSrv.UpdateShares(shares)
	}
	if tftpSrv != nil {
		tftpSrv.UpdateShares(shares)
	}
	if webdavSrv != nil {
		webdavSrv.UpdateShares(shares)
	}
	slog.Info("file shares reloaded", "count", len(shares))
}

// reloadFTPUsers rebuilds users and updates both FTP and SFTP servers.
func (a *api) reloadFTPUsers(ctx context.Context) {
	users := BuildFTPUsers(ctx, a.svc)

	a.rawHandler.mu.RLock()
	ftpSrv := a.rawHandler.ftpServer
	sftpSrv := a.rawHandler.sftpServer
	webdavSrv := a.rawHandler.webdavServer
	a.rawHandler.mu.RUnlock()

	if ftpSrv != nil {
		ftpSrv.UpdateUsers(users)
	}
	if sftpSrv != nil {
		sftpSrv.UpdateUsers(users)
	}
	if webdavSrv != nil {
		webdavSrv.UpdateUsers(users)
	}
	slog.Info("file server users reloaded", "count", len(users))
}

// reloadFTPServe stops the existing FTP server (if running) and starts a new one if enabled.
func (a *api) reloadFTPServe(settings *service.Settings) {
	shares := BuildFTPShares(context.Background(), a.svc, a.rawHandler)
	users := BuildFTPUsers(context.Background(), a.svc)

	a.rawHandler.mu.Lock()
	oldServer := a.rawHandler.ftpServer
	oldCancel := a.rawHandler.ftpCancel
	a.rawHandler.ftpServer = nil
	a.rawHandler.ftpCancel = nil
	a.rawHandler.mu.Unlock()

	// Cancel context first to trigger clean goroutine shutdown, then stop server to free port.
	if oldCancel != nil {
		oldCancel()
	}
	if oldServer != nil {
		oldServer.Stop()
	}

	if settings.FTPServe != nil && settings.FTPServe.Enabled {
		ftpSrv, err := ftpserve.NewServer(settings.FTPServe, shares, users)
		if err != nil {
			slog.Error("failed to start FTP server", "error", err)
			return
		}
		ctx, cancel := context.WithCancel(a.rawHandler.appCtx)
		ftpSrv.Start(ctx)

		a.rawHandler.mu.Lock()
		a.rawHandler.ftpServer = ftpSrv
		a.rawHandler.ftpCancel = cancel
		a.rawHandler.mu.Unlock()

		slog.Info("FTP server reloaded")
	} else {
		slog.Info("FTP server disabled")
	}
}

// reloadSFTPServe stops the existing SFTP server (if running) and starts a new one if enabled.
func (a *api) reloadSFTPServe(settings *service.Settings) {
	shares := BuildFTPShares(context.Background(), a.svc, a.rawHandler)
	users := BuildFTPUsers(context.Background(), a.svc)

	a.rawHandler.mu.Lock()
	oldServer := a.rawHandler.sftpServer
	oldCancel := a.rawHandler.sftpCancel
	a.rawHandler.sftpServer = nil
	a.rawHandler.sftpCancel = nil
	a.rawHandler.mu.Unlock()

	if oldCancel != nil {
		oldCancel()
	}
	if oldServer != nil {
		oldServer.Stop()
	}

	if settings.SFTPServe != nil && settings.SFTPServe.Enabled {
		sftpSrv, err := sftpserve.NewServer(settings.SFTPServe, shares, users, func(generatedPEM string) {
			settings.SFTPServe.HostKeyPEM = generatedPEM
			if err := a.svc.PatchSettings(context.Background(), &service.PatchSettings{
				Action:    service.ActionKeySet,
				SFTPServe: settings.SFTPServe,
			}); err != nil {
				slog.Error("failed to persist auto-generated SFTP host key", "error", err)
			} else {
				slog.Info("auto-generated SFTP host key persisted to database")
			}
		})
		if err != nil {
			slog.Error("failed to start SFTP server", "error", err)
			return
		}
		ctx, cancel := context.WithCancel(a.rawHandler.appCtx)
		sftpSrv.Start(ctx)

		a.rawHandler.mu.Lock()
		a.rawHandler.sftpServer = sftpSrv
		a.rawHandler.sftpCancel = cancel
		a.rawHandler.mu.Unlock()

		slog.Info("SFTP server reloaded")
	} else {
		slog.Info("SFTP server disabled")
	}
}

// reloadTFTPServe stops the existing TFTP server (if running) and starts a new one if enabled.
func (a *api) reloadTFTPServe(settings *service.Settings) {
	shares := BuildFTPShares(context.Background(), a.svc, a.rawHandler)

	a.rawHandler.mu.Lock()
	oldServer := a.rawHandler.tftpServer
	oldCancel := a.rawHandler.tftpCancel
	a.rawHandler.tftpServer = nil
	a.rawHandler.tftpCancel = nil
	a.rawHandler.mu.Unlock()

	if oldCancel != nil {
		oldCancel()
	}
	if oldServer != nil {
		oldServer.Stop()
	}

	if settings.TFTPServe != nil && settings.TFTPServe.Enabled {
		tftpSrv, err := tftpserve.NewServer(settings.TFTPServe, shares)
		if err != nil {
			slog.Error("failed to start TFTP server", "error", err)
			return
		}
		ctx, cancel := context.WithCancel(a.rawHandler.appCtx)
		tftpSrv.Start(ctx, settings.TFTPServe)

		a.rawHandler.mu.Lock()
		a.rawHandler.tftpServer = tftpSrv
		a.rawHandler.tftpCancel = cancel
		a.rawHandler.mu.Unlock()

		slog.Info("TFTP server reloaded")
	} else {
		slog.Info("TFTP server disabled")
	}
}

// reloadWebDAVServe stops the existing WebDAV server (if running) and starts a new one if enabled.
func (a *api) reloadWebDAVServe(settings *service.Settings) {
	shares := BuildFTPShares(context.Background(), a.svc, a.rawHandler)
	users := BuildFTPUsers(context.Background(), a.svc)

	a.rawHandler.mu.Lock()
	oldServer := a.rawHandler.webdavServer
	oldCancel := a.rawHandler.webdavCancel
	a.rawHandler.webdavServer = nil
	a.rawHandler.webdavCancel = nil
	a.rawHandler.mu.Unlock()

	if oldCancel != nil {
		oldCancel()
	}
	if oldServer != nil {
		oldServer.Stop()
	}

	if settings.WebDAVServe != nil && settings.WebDAVServe.Enabled {
		webdavSrv, err := webdavserve.NewServer(settings.WebDAVServe, shares, users)
		if err != nil {
			slog.Error("failed to start WebDAV server", "error", err)
			return
		}
		ctx, cancel := context.WithCancel(a.rawHandler.appCtx)
		webdavSrv.Start(ctx)

		a.rawHandler.mu.Lock()
		a.rawHandler.webdavServer = webdavSrv
		a.rawHandler.webdavCancel = cancel
		a.rawHandler.mu.Unlock()

		slog.Info("WebDAV server reloaded")
	} else {
		slog.Info("WebDAV server disabled")
	}
}

// (The standalone "public server" was retired with the introduction
// of the user-built Proxy Servers. Reloads now flow through the
// proxy Manager via postSettings; see ProxyReconciler at the top of
// this file.)

// BuildMountEntries creates mountEntry instances from settings entries.
// Returns successfully created entries and any errors for failed ones.
func BuildMountEntries(settingsEntries []service.RawMountEntry) ([]mountEntry, []error) {
	var entries []mountEntry
	var errs []error
	seen := make(map[string]bool)

	for _, m := range settingsEntries {
		if m.Prefix == "" {
			errs = append(errs, fmt.Errorf("raw mount prefix must not be empty"))
			continue
		}
		if seen[m.Prefix] {
			errs = append(errs, fmt.Errorf("duplicate raw mount prefix %q", m.Prefix))
			continue
		}
		seen[m.Prefix] = true

		mountType := m.Type
		if mountType == "" {
			mountType = "local"
		}

		fs, err := newRawFSFromSettings(mountType, m)
		if err != nil {
			errs = append(errs, fmt.Errorf("mount %q: %w", m.Prefix, err))
			continue
		}

		entries = append(entries, mountEntry{
			Prefix:   m.Prefix,
			FS:       fs,
			Type:     mountType,
			Writable: rawfs.IsWritable(fs),
		})
	}

	return entries, errs
}

// newRawFSFromSettings creates a RawFS from a settings entry.
func newRawFSFromSettings(mountType string, m service.RawMountEntry) (rawfs.RawFS, error) {
	switch mountType {
	case "local", "":
		if m.Path == "" {
			return nil, fmt.Errorf("path is required for local mount")
		}
		return localfs.New(m.Path)
	case "s3":
		if m.S3 == nil {
			return nil, fmt.Errorf("s3 config is required")
		}
		if rawfs.NewS3FSFunc == nil {
			return nil, fmt.Errorf("s3 backend not available")
		}
		return rawfs.NewS3FSFunc(m.S3.Bucket, m.S3.Region, m.S3.Endpoint, m.S3.AccessKey, m.S3.SecretKey, m.S3.Prefix, m.S3.PathStyle, m.S3.Secure)
	case "ftp":
		if m.FTP == nil {
			return nil, fmt.Errorf("ftp config is required")
		}
		if rawfs.NewFTPFSFunc == nil {
			return nil, fmt.Errorf("ftp backend not available")
		}
		return rawfs.NewFTPFSFunc(m.FTP.Host, m.FTP.Username, m.FTP.Password, m.FTP.BasePath, m.FTP.TLS)
	case "sftp":
		if m.SFTP == nil {
			return nil, fmt.Errorf("sftp config is required")
		}
		if rawfs.NewSFTPFSFunc == nil {
			return nil, fmt.Errorf("sftp backend not available")
		}
		return rawfs.NewSFTPFSFunc(m.SFTP.Host, m.SFTP.Username, m.SFTP.Password, m.SFTP.PrivateKey, m.SFTP.BasePath)
	case "webdav":
		if m.WebDAV == nil {
			return nil, fmt.Errorf("webdav config is required")
		}
		if rawfs.NewWebDAVFSFunc == nil {
			return nil, fmt.Errorf("webdav backend not available")
		}
		return rawfs.NewWebDAVFSFunc(m.WebDAV.URL, m.WebDAV.Username, m.WebDAV.Password, m.WebDAV.BasePath)
	case "vercel-blob":
		if m.VercelBlob == nil {
			return nil, fmt.Errorf("vercelBlob config is required")
		}
		if rawfs.NewVercelBlobFSFunc == nil {
			return nil, fmt.Errorf("vercel-blob backend not available")
		}
		return rawfs.NewVercelBlobFSFunc(m.VercelBlob.Token, m.VercelBlob.StoreID, m.VercelBlob.Prefix)
	default:
		return nil, fmt.Errorf("unknown mount type %q", mountType)
	}
}

// BuildInitialRawHandler creates a rawHandler from DB settings.
// It also creates the hook dispatcher and loads hooks from the database.
func BuildInitialRawHandler(ctx context.Context, svc *service.Service) *RawHandler {
	// Build mount entries from DB settings
	var entries []mountEntry
	settings, err := svc.Settings(ctx)
	if err != nil {
		slog.Warn("could not load settings for raw mounts", "error", err)
	} else if len(settings.RawMounts) > 0 {
		dbEntries, dbErrs := BuildMountEntries(settings.RawMounts)
		for _, err := range dbErrs {
			slog.Warn("skipping invalid raw mount from settings", "error", err)
		}
		entries = dbEntries
	}

	// Create and start the hook dispatcher
	dispatcher := hook.NewDispatcher(256)
	dispatcher.Start(ctx)

	rh := NewRawHandler(entries, ctx, dispatcher)

	// Set up the resolver so PEM references (raw://, config://) can be resolved
	resolver := hook.NewResolver(
		// rawMounts provider: returns a map of mount prefix -> RawFS
		func() map[string]rawfs.RawFS {
			rh.mu.RLock()
			defer rh.mu.RUnlock()
			m := make(map[string]rawfs.RawFS, len(rh.mounts))
			for _, me := range rh.mounts {
				// Use the inner FS (unwrap the hook decorator) to avoid recursive events
				m[me.Prefix] = hook.Inner(me.FS)
			}
			return m
		},
		// configData provider: reads config file data by key
		func(ctx context.Context, key string) ([]byte, error) {
			file, err := svc.File(ctx, key, 0) // version 0 = latest
			if err != nil {
				return nil, err
			}
			return file.Data, nil
		},
	)
	dispatcher.SetResolver(resolver)

	// Load hooks from settings (if any)
	if settings != nil && len(settings.Hooks) > 0 {
		dispatcher.UpdateHooks(settings.Hooks)
	}

	return rh
}

// BuildFTPShares creates FTP share entries from the current settings, resolving
// each path's mount prefix to the corresponding RawFS backend via the rawHandler.
func BuildFTPShares(ctx context.Context, svc *service.Service, rh *RawHandler) []ftpserve.Share {
	settings, err := svc.Settings(ctx)
	if err != nil {
		slog.Warn("could not load settings for FTP shares", "error", err)
		return nil
	}

	rh.mu.RLock()
	defer rh.mu.RUnlock()

	// Build a mount lookup map
	mountMap := make(map[string]rawfs.RawFS, len(rh.mounts))
	for _, m := range rh.mounts {
		mountMap[m.Prefix] = m.FS
	}

	var shares []ftpserve.Share
	for _, s := range settings.FTPShares {
		var sources []ftpserve.ShareSource
		for _, p := range s.Paths {
			// Parse "mount_prefix" or "mount_prefix/sub/path"
			mountPrefix, subPath := parseMountPath(p)
			fs, ok := mountMap[mountPrefix]
			if !ok {
				slog.Warn("FTP share path references unknown mount", "share", s.Name, "path", p, "mount", mountPrefix)
				continue
			}
			sources = append(sources, ftpserve.ShareSource{
				Mount: mountPrefix,
				Path:  subPath,
				FS:    fs,
			})
		}

		if len(sources) == 0 {
			slog.Warn("FTP share has no valid sources, skipping", "share", s.Name)
			continue
		}

		shares = append(shares, ftpserve.Share{
			Name:     s.Name,
			Sources:  sources,
			ReadOnly: s.ReadOnly,
			Root:     s.Root,
		})
	}

	return shares
}

// parseMountPath splits "mount_prefix/sub/path" into ("mount_prefix", "sub/path").
func parseMountPath(p string) (string, string) {
	p = strings.TrimPrefix(p, "/")
	idx := strings.IndexByte(p, '/')
	if idx < 0 {
		return p, ""
	}
	return p[:idx], p[idx+1:]
}

// BuildFTPUsers creates FTP user entries from the current settings.
func BuildFTPUsers(ctx context.Context, svc *service.Service) []ftpserve.User {
	settings, err := svc.Settings(ctx)
	if err != nil {
		slog.Warn("could not load settings for FTP users", "error", err)
		return nil
	}

	var users []ftpserve.User
	for _, u := range settings.FTPUsers {
		users = append(users, ftpserve.User{
			Username:       u.Username,
			Password:       u.Password,
			Shares:         u.Shares,
			AuthorizedKeys: u.AuthorizedKeys,
			ReadOnly:       u.ReadOnly,
		})
	}

	return users
}

// GetDispatcher returns the hook dispatcher from the rawHandler.
func GetDispatcher(rh *RawHandler) *hook.Dispatcher {
	return rh.Dispatcher()
}

// SetFTPServer stores the FTP server reference and its cancel func in the rawHandler.
func SetFTPServer(rh *RawHandler, ftpSrv *ftpserve.Server, cancel context.CancelFunc) {
	rh.mu.Lock()
	rh.ftpServer = ftpSrv
	rh.ftpCancel = cancel
	rh.mu.Unlock()
}

// SetSFTPServer stores the SFTP server reference and its cancel func in the rawHandler.
func SetSFTPServer(rh *RawHandler, sftpSrv *sftpserve.Server, cancel context.CancelFunc) {
	rh.mu.Lock()
	rh.sftpServer = sftpSrv
	rh.sftpCancel = cancel
	rh.mu.Unlock()
}

// SetTFTPServer stores the TFTP server reference and its cancel func in the rawHandler.
func SetTFTPServer(rh *RawHandler, tftpSrv *tftpserve.Server, cancel context.CancelFunc) {
	rh.mu.Lock()
	rh.tftpServer = tftpSrv
	rh.tftpCancel = cancel
	rh.mu.Unlock()
}

// SetWebDAVServer stores the WebDAV server reference and its cancel func in the rawHandler.
func SetWebDAVServer(rh *RawHandler, webdavSrv *webdavserve.Server, cancel context.CancelFunc) {
	rh.mu.Lock()
	rh.webdavServer = webdavSrv
	rh.webdavCancel = cancel
	rh.mu.Unlock()
}

// StopServeServers tears down every FTP/SFTP/TFTP/WebDAV listener
// owned by the rawHandler in a deterministic order: cancel context
// first to interrupt accept loops, then call Stop to release the
// socket. Idempotent — calling Stop on a nil server is a no-op via
// the nil guard. Intended for shutdown; the per-protocol reload
// paths handle their own teardown when an operator flips the
// enabled flag at runtime.
func StopServeServers(rh *RawHandler) {
	if rh == nil {
		return
	}
	rh.mu.Lock()
	ftpSrv, ftpCancel := rh.ftpServer, rh.ftpCancel
	sftpSrv, sftpCancel := rh.sftpServer, rh.sftpCancel
	tftpSrv, tftpCancel := rh.tftpServer, rh.tftpCancel
	webdavSrv, webdavCancel := rh.webdavServer, rh.webdavCancel
	rh.ftpServer, rh.ftpCancel = nil, nil
	rh.sftpServer, rh.sftpCancel = nil, nil
	rh.tftpServer, rh.tftpCancel = nil, nil
	rh.webdavServer, rh.webdavCancel = nil, nil
	rh.mu.Unlock()

	if ftpCancel != nil {
		ftpCancel()
	}
	if ftpSrv != nil {
		ftpSrv.Stop()
	}
	if sftpCancel != nil {
		sftpCancel()
	}
	if sftpSrv != nil {
		sftpSrv.Stop()
	}
	if tftpCancel != nil {
		tftpCancel()
	}
	if tftpSrv != nil {
		tftpSrv.Stop()
	}
	if webdavCancel != nil {
		webdavCancel()
	}
	if webdavSrv != nil {
		webdavSrv.Stop()
	}
}

func (a *api) listExternalPaths(c *ada.Context) error {
	resourceName := c.Request.PathValue("name")
	prefix := c.Request.URL.Query().Get("prefix")

	paths, err := a.svc.ListExternalPaths(c.Request.Context(), resourceName, prefix)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(paths)
}

// searchExternal walks the named resource looking for paths/values
// that match the `q` query string. Query params:
//
//	q     — search term (required, non-empty)
//	mode  — "name" (default) or "all" (also greps values)
//	limit — max hits to return (default 200, hard cap inside service)
//
// Returns a JSON array of {path, type, snippet} objects so the SPA
// can render mixed name/content hits in one list. The handler maps
// query strings rather than a JSON body because search responses are
// safe to cache by an intermediary on the q+mode combination, and a
// GET makes that cacheability explicit (whereas POST always says
// "uncachable" to proxies). The query never carries credentials —
// those live in the resource configuration on the server.
func (a *api) searchExternal(c *ada.Context) error {
	resourceName := c.Request.PathValue("name")
	q := c.Request.URL.Query().Get("q")
	if strings.TrimSpace(q) == "" {
		// Empty query is not an error — just nothing to return.
		// Matches the SPA's behaviour where clearing the search box
		// resets the result list without throwing.
		return c.SetStatus(http.StatusOK).SendJSON([]service.ExternalSearchHit{})
	}
	mode := service.ExternalSearchMode(c.Request.URL.Query().Get("mode"))
	if mode != service.ExternalSearchModeAll {
		mode = service.ExternalSearchModeName
	}
	limit := 0
	if raw := c.Request.URL.Query().Get("limit"); raw != "" {
		// fmt.Sscanf swallows errors silently; if parsing fails
		// limit stays 0 and the service applies its default.
		fmt.Sscanf(raw, "%d", &limit)
	}

	hits, err := a.svc.SearchExternal(c.Request.Context(), resourceName, q, mode, limit)
	if err != nil {
		return err
	}
	if hits == nil {
		// Always emit `[]` so SPA destructuring doesn't have to
		// guard against null. Same convention as the rest of the
		// external endpoints.
		hits = []service.ExternalSearchHit{}
	}
	return c.SetStatus(http.StatusOK).SendJSON(hits)
}

// testExternalResource runs a live connectivity check against the named
// external resource. The SPA's External page calls this from its "Test"
// action — the response body always has shape {ok, message, sample} so the
// UI can render the outcome uniformly regardless of backend type.
func (a *api) testExternalResource(c *ada.Context) error {
	resourceName := c.Request.PathValue("name")

	result, err := a.svc.TestExternal(c.Request.Context(), resourceName)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(result)
}

// listExternalResources powers the left pane of the External browser
// page. Returns names, kinds and capability flags for every
// configured resource. Does NOT touch the network.
func (a *api) listExternalResources(c *ada.Context) error {
	resources, err := a.svc.ListExternalResources(c.Request.Context())
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(resources)
}

// externalEntryReq is the shared request body for read/write/delete/
// versions/version endpoints. Path-bearing operations all share this
// shape so the SPA can use one helper. We use POST + JSON body
// (rather than GET + query string) for two reasons: many backend
// paths embed "/" (e.g. Kubernetes "namespace/secret/name", Vault
// "myapp/db") and URL-encoding them through path segments fights the
// router; and POST is uncached by every layer that might sit in
// front, so secret payloads don't end up in proxy logs.
type externalEntryReq struct {
	Path    string         `json:"path"`
	Data    map[string]any `json:"data,omitempty"`
	Version string         `json:"version,omitempty"`
}

// translateNotSupported turns an external.ErrNotSupported into a 405
// so the SPA can branch on it. Anything else is passed through to the
// default error handler.
func translateNotSupported(err error) error {
	if errors.Is(err, external.ErrNotSupported) {
		return errors.Join(err, service.ErrBadRequest)
	}
	return err
}

func (a *api) readExternalEntry(c *ada.Context) error {
	resourceName := c.Request.PathValue("name")
	var req externalEntryReq
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	entry, err := a.svc.ReadExternal(c.Request.Context(), resourceName, req.Path)
	if err != nil {
		return translateNotSupported(err)
	}
	return c.SetStatus(http.StatusOK).SendJSON(entry)
}

func (a *api) writeExternalEntry(c *ada.Context) error {
	resourceName := c.Request.PathValue("name")
	var req externalEntryReq
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	if err := a.svc.WriteExternal(c.Request.Context(), resourceName, req.Path, req.Data); err != nil {
		return translateNotSupported(err)
	}
	return c.SendNoContent()
}

func (a *api) deleteExternalEntry(c *ada.Context) error {
	resourceName := c.Request.PathValue("name")
	var req externalEntryReq
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	if err := a.svc.DeleteExternal(c.Request.Context(), resourceName, req.Path); err != nil {
		return translateNotSupported(err)
	}
	return c.SendNoContent()
}

func (a *api) listExternalVersions(c *ada.Context) error {
	resourceName := c.Request.PathValue("name")
	var req externalEntryReq
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	versions, err := a.svc.ListExternalVersions(c.Request.Context(), resourceName, req.Path)
	if err != nil {
		return translateNotSupported(err)
	}
	if versions == nil {
		// Always emit [] over null so the SPA can iterate without
		// a null-guard at every call site.
		versions = []external.Version{}
	}
	return c.SetStatus(http.StatusOK).SendJSON(versions)
}

func (a *api) readExternalVersion(c *ada.Context) error {
	resourceName := c.Request.PathValue("name")
	var req externalEntryReq
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	entry, err := a.svc.ReadExternalVersion(c.Request.Context(), resourceName, req.Path, req.Version)
	if err != nil {
		return translateNotSupported(err)
	}
	return c.SetStatus(http.StatusOK).SendJSON(entry)
}

func (a *api) postFolder(c *ada.Context) error {
	key := c.Request.PathValue("*")

	if err := a.svc.SetFolder(c.Request.Context(), key); err != nil {
		return err
	}

	return c.SendNoContent()
}

func (a *api) getFolder(c *ada.Context) error {
	key := c.Request.PathValue("*")

	data, err := a.svc.Folder(c.Request.Context(), key)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(data)
}

func (a *api) deleteFolder(c *ada.Context) error {
	key := c.Request.PathValue("*")

	if err := a.svc.DeleteFolder(c.Request.Context(), key); err != nil {
		return err
	}

	return c.SendNoContent()
}

func (a *api) getFile(c *ada.Context) error {
	key := c.Request.PathValue("*")
	variant := c.Request.URL.Query().Get("variant")

	version := int64(0)
	if versionStr := c.Request.URL.Query().Get("version"); versionStr != "" {
		var err error
		version, err = strconv.ParseInt(versionStr, 10, 64)
		if err != nil {
			return errors.Join(err, service.ErrBadRequest)
		}
	}

	var data *service.File
	var err error
	if variant != "" {
		data, err = a.svc.Variant(c.Request.Context(), key, variant, version)
	} else {
		data, err = a.svc.File(c.Request.Context(), key, version)
	}
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(data)
}

func (a *api) postFile(c *ada.Context) error {
	key := c.Request.PathValue("*")
	variant := c.Request.URL.Query().Get("variant")

	var req struct {
		service.File
		ExpectedVersion *int64 `json:"expected_version,omitempty"`
		Constraint      string `json:"constraint,omitempty"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	var version int64
	var err error
	if variant != "" {
		version, err = a.svc.SetVariant(c.Request.Context(), key, variant, &req.File, req.ExpectedVersion, req.Constraint)
	} else {
		version, err = a.svc.SetFile(c.Request.Context(), key, &req.File, req.ExpectedVersion, req.Constraint)
	}
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusCreated).SendJSON(struct {
		service.File
		Version int64 `json:"version"`
	}{
		File:    req.File,
		Version: version,
	})
}

func (a *api) deleteFile(c *ada.Context) error {
	key := c.Request.PathValue("*")
	variant := c.Request.URL.Query().Get("variant")

	version := int64(0)
	if versionStr := c.Request.URL.Query().Get("version"); versionStr != "" {
		var err error
		version, err = strconv.ParseInt(versionStr, 10, 64)
		if err != nil {
			return errors.Join(err, service.ErrBadRequest)
		}
	}

	var err error
	if variant != "" {
		err = a.svc.DeleteVariant(c.Request.Context(), key, variant, version)
	} else {
		err = a.svc.DeleteFile(c.Request.Context(), key, version)
	}
	if err != nil {
		return err
	}

	return c.SendNoContent()
}

func (a *api) getFileVersions(c *ada.Context) error {
	key := c.Request.PathValue("*")
	variant := c.Request.URL.Query().Get("variant")

	var versions service.FileVersions
	var err error
	if variant != "" {
		versions, err = a.svc.VariantVersions(c.Request.Context(), key, variant)
	} else {
		versions, err = a.svc.FileVersionsList(c.Request.Context(), key)
	}
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(versions)
}

func (a *api) patchFileVersion(c *ada.Context) error {
	key := c.Request.PathValue("*")
	variant := c.Request.URL.Query().Get("variant")

	var req struct {
		Version    int64  `json:"version"`
		Constraint string `json:"constraint"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if req.Version <= 0 {
		return errors.Join(fmt.Errorf("version is required and must be > 0"), service.ErrBadRequest)
	}

	filePath := key
	if variant != "" {
		filePath = key + "/@" + variant
	}

	if err := a.svc.UpdateConstraint(c.Request.Context(), filePath, req.Version, req.Constraint); err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "constraint updated"})
}

func (a *api) listVariants(c *ada.Context) error {
	key := c.Request.PathValue("*")

	variants, err := a.svc.ListVariants(c.Request.Context(), key)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(variants)
}

func (a *api) renderFile(c *ada.Context) error {
	key := c.Request.PathValue("*")
	variant := c.Request.URL.Query().Get("variant")

	var req struct {
		Content string           `json:"content"`
		Meta    service.FileMeta `json:"meta"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	result, err := a.svc.RenderFile(c.Request.Context(), key, variant, req.Content, &req.Meta)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(result)
}

func (a *api) listTokens(c *ada.Context) error {
	tokens, _, err := a.svc.ListTokens(c.Request.Context(), nil)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(tokens)
}

func (a *api) createToken(c *ada.Context) error {
	var req service.CreateTokenRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	result, err := a.svc.CreateToken(c.Request.Context(), &req)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusCreated).SendJSON(result)
}

func (a *api) deleteToken(c *ada.Context) error {
	id := c.Request.PathValue("*")

	if err := a.svc.DeleteToken(c.Request.Context(), id); err != nil {
		return err
	}

	return c.SendNoContent()
}

func (a *api) patchToken(c *ada.Context) error {
	id := c.Request.PathValue("*")

	var req service.PatchTokenRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if err := a.svc.PatchToken(c.Request.Context(), id, &req); err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "token updated"})
}

func (a *api) convertFormat(c *ada.Context) error {
	var req struct {
		Content string `json:"content"`
		From    string `json:"from"`
		To      string `json:"to"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if req.From == "" || req.To == "" {
		return errors.Join(fmt.Errorf("'from' and 'to' formats are required"), service.ErrBadRequest)
	}

	converted, err := service.ConvertFormat([]byte(req.Content), req.From, req.To)
	if err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	return c.SetStatus(http.StatusOK).SendJSON(struct {
		Content string `json:"content"`
		Format  string `json:"format"`
	}{
		Content: string(converted),
		Format:  req.To,
	})
}

// searchHandler uses SSE to stream search results as they are found.
// The client can abort the connection to cancel the search.
func (a *api) searchHandler(w http.ResponseWriter, r *http.Request) {
	// Inline permission check — this is a raw http.Handler, not an ada
	// handler, so it can't use the withPerm wrapper.
	caps := service.CapabilitiesFromContext(r.Context())
	if !caps.Has(service.CapFilesRead) {
		http.Error(w, `{"message":"forbidden"}`, http.StatusForbidden)
		return
	}
	patterns := service.CapabilityPatternsFromContext(r.Context())

	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, `{"message":"query parameter 'q' is required"}`, http.StatusBadRequest)
		return
	}

	// mode=name skips reading file contents entirely (faster + safer,
	// no content scanning). Default (omitted or any other value) keeps
	// the existing path + content behaviour for backward compatibility.
	nameOnly := r.URL.Query().Get("mode") == "name"

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Use a cancellable context — cancelled when client disconnects
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	results := make(chan service.SearchResult, 10)

	// Run search in background
	go func() {
		_ = a.svc.Search(ctx, service.SearchOptions{Query: query, NameOnly: nameOnly}, results)
	}()

	// Stream results as SSE events. When the user's files.read grant is
	// path-scoped, each result's Path must match before being forwarded.
	// Empty patterns (the common case) are a no-op: Allows returns true.
	for result := range results {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !patterns.Allows(service.CapFilesRead, result.Path) {
			continue
		}

		data, err := json.Marshal(result)
		if err != nil {
			continue
		}

		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	// Send done event
	fmt.Fprintf(w, "event: done\ndata: {}\n\n")
	flusher.Flush()
}
