package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"

	"log/slog"

	"github.com/rakunlabs/ada"
	pcluster "github.com/rakunlabs/pika/internal/cluster"
	"github.com/rakunlabs/pika/internal/config"
	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/secret"
	"github.com/rakunlabs/pika/internal/server/authx"
	"github.com/rakunlabs/pika/internal/server/publicendpoint"
	"github.com/rakunlabs/pika/internal/server/servertls"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/pika/internal/usersync"
)

// Info holds server metadata returned by the info endpoint.
type Info struct {
	Name              string `json:"name"`
	Version           string `json:"version"`
	Commit            string `json:"commit,omitempty"`
	Date              string `json:"date,omitempty"`
	ManagedTLSEnabled bool   `json:"managed_tls_enabled"`
}

type api struct {
	svc             *service.Service
	info            Info
	encStore        *secret.Storage         // nil if encryption is disabled
	mgr             *authx.Manager          // auth manager (login/logout/cap resolution)
	dispatcher      *hook.Dispatcher        // hook event bus; nil only in tests
	syncScheduler   *usersync.Scheduler     // nil until set by server.go
	cluster         *pcluster.Cluster       // nil only in tests or custom embeddings
	publicEndpoints *publicendpoint.Manager // nil only in tests
	tlsMgr          *servertls.Manager      // nil only in tests
	appCtx          context.Context         // root context used for background loops
}

type response struct {
	Message string `json:"message,omitempty"`
}

func Handle(m *ada.Mux, mData *ada.Mux, mAuth *ada.Mux, svc *service.Service, info Info, encStore *secret.Storage, mgr *authx.Manager, dispatcher *hook.Dispatcher, cl *pcluster.Cluster, peMgr *publicendpoint.Manager, tlsMgr *servertls.Manager) error {
	// Set hook service identification from config
	hook.ServiceName = config.ServiceName
	hook.Version = config.Version

	// User-sync scheduler runs LDAP sync sources on a per-source ticker.
	// Constructed here so the api handlers and the postSettings reload
	// path share one instance. Started below after the routes register.
	syncSched := usersync.NewScheduler(svc)

	api := &api{svc: svc, info: info, encStore: encStore, mgr: mgr, dispatcher: dispatcher, syncScheduler: syncSched, cluster: cl, publicEndpoints: peMgr, tlsMgr: tlsMgr, appCtx: context.Background()}

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

	m.GET("/api/v1/tls/status", m.Wrap(api.withPerm(service.CapSettingsManage, api.getTLSStatus)))
	m.POST("/api/v1/tls/self-signed", m.Wrap(api.withPerm(service.CapSettingsManage, api.generateManagedTLS)))
	m.PUT("/api/v1/tls/manual", m.Wrap(api.withPerm(service.CapSettingsManage, api.uploadManagedTLS)))
	m.POST("/api/v1/tls-generate", m.Wrap(api.withPerm(service.CapSettingsManage, api.generateTLS)))
	m.POST("/api/v1/ssh-keygen", m.Wrap(api.withPerm(service.CapSettingsManage, api.generateSSHKey)))

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
	if curSettings, err := svc.Settings(api.appCtx); err == nil {
		syncSched.Start(api.appCtx, curSettings.UserSync)
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

	resp := struct {
		Info
		Subtitle       string                  `json:"subtitle,omitempty"`
		User           string                  `json:"user,omitempty"`
		AuthEnabled    bool                    `json:"auth_enabled"`
		BuiltinAuth    bool                    `json:"builtin_auth"`
		IsSuperadmin   bool                    `json:"is_superadmin"`
		Permissions    []string                `json:"permissions"`
		Capabilities   []service.Capability    `json:"capabilities"`
		SetupRequired  bool                    `json:"setup_required,omitempty"`
		VaultEnabled   bool                    `json:"vault_enabled"`
		VaultItemTypes []service.VaultItemType `json:"vault_item_types,omitempty"`
	}{
		Info:           a.info,
		Subtitle:       subtitle,
		User:           username,
		AuthEnabled:    true,
		BuiltinAuth:    true,
		IsSuperadmin:   isSuperadmin,
		Permissions:    caps,
		Capabilities:   service.KnownCapabilities,
		VaultEnabled:   vaultEnabled,
		VaultItemTypes: service.KnownVaultItemTypes,
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

func (a *api) getClusterStatus(c *ada.Context) error {
	return c.SetStatus(http.StatusOK).SendJSON(a.cluster.Status())
}

func (a *api) postSettings(c *ada.Context) error {
	var patchSettings service.PatchSettings
	if err := c.Bind(&patchSettings); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if err := a.svc.PatchSettings(c.Request.Context(), &patchSettings); err != nil {
		return err
	}

	// If hooks were updated, reload them in the dispatcher
	if patchSettings.Hooks != nil {
		a.reloadHooks(c.Request.Context())
	}
	if patchSettings.EventLog != nil {
		a.reloadEventLog(c.Request.Context())
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

	// If public endpoints were updated, reconcile the live listener
	// set. Bind failures don't fail the save — settings already
	// persisted, the operator can fix the port and try again. We
	// surface bind errors through GET /public-endpoints/status so
	// the UI can show a banner.
	if patchSettings.PublicEndpoints != nil && a.publicEndpoints != nil {
		// Re-read the persisted list so we apply the post-service
		// (validated, IDs filled, timestamps set) shape rather than
		// the raw client payload.
		settings, err := a.svc.Settings(c.Request.Context())
		if err == nil && settings != nil {
			if rerr := a.publicEndpoints.Reload(c.Request.Context(), settings.PublicEndpoints); rerr != nil {
				slog.Warn("public endpoints reload reported issues", "error", rerr)
			}
		}
	}

	return c.SetStatus(http.StatusOK).SendJSON(patchSettings)
}

// getPublicEndpointStatus returns the live diagnostic view of every
// configured public endpoint (running, disabled, bind-failed).
func (a *api) getPublicEndpointStatus(c *ada.Context) error {
	if a.publicEndpoints == nil {
		return c.SetStatus(http.StatusOK).SendJSON([]publicendpoint.EndpointStatus{})
	}
	return c.SetStatus(http.StatusOK).SendJSON(a.publicEndpoints.Status())
}

// testPublicEndpoint runs a synthetic GET through the endpoint's
// live handler chain — the same chain the public listener serves —
// and returns the status, headers and body. Two reasons we do this
// through the real handler instead of hitting the bound port:
//   - Operators can probe disabled endpoints (manager refuses to
//     bind a port for them but the handler is still constructed).
//   - It works without the admin SPA having direct network access to
//     the public bind, which is common in proxy-fronted deployments.
//
// Request body:
//
//	{ "key": "...", "variant": "...", "version": "...",
//	  "raw": bool, "format": "...",
//	  "headers": {"X-Tenant": "acme", ...} }
//
// The "headers" map is forwarded verbatim onto the synthetic
// request so an operator can exercise both the auth chain and
// any request-check rules they configured. Both Authorization-
// style auth tokens and policy-relevant headers (e.g. X-Tenant
// matched by a rule) go through this same map.
//
// Response:
//
//	{ "status": 200, "headers": {...}, "body": "..." }
func (a *api) testPublicEndpoint(c *ada.Context) error {
	if a.publicEndpoints == nil {
		return errors.Join(errors.New("public endpoints manager not wired"), service.ErrInternal)
	}
	id := c.Request.PathValue("id")
	if id == "" {
		return errors.Join(errors.New("id is required"), service.ErrBadRequest)
	}

	var req struct {
		Key     string            `json:"key"`
		Variant string            `json:"variant,omitempty"`
		Version string            `json:"version,omitempty"`
		Raw     bool              `json:"raw,omitempty"`
		Format  string            `json:"format,omitempty"`
		Headers map[string]string `json:"headers,omitempty"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	// Resolve the endpoint so we know its mode and base path.
	settings, err := a.svc.Settings(c.Request.Context())
	if err != nil {
		return err
	}
	var ep *service.PublicEndpoint
	for i := range settings.PublicEndpoints {
		if settings.PublicEndpoints[i].ID == id {
			ep = &settings.PublicEndpoints[i]
			break
		}
	}
	if ep == nil {
		return errors.Join(fmt.Errorf("public endpoint %q not found", id), service.ErrNotFound)
	}

	handler := a.publicEndpoints.HandlerForID(id)
	if handler == nil {
		// Endpoint exists but isn't running (disabled or bind
		// failure). Build a transient handler for the probe so
		// operators can still validate their template / shim.
		built, berr := publicendpoint.BuildHandlerForProbe(*ep, a.svc, slog.Default())
		if berr != nil {
			return errors.Join(berr, service.ErrBadRequest)
		}
		handler = built
	}

	// Construct the synthetic URL. For consul mode we emit the
	// well-known "{basePath}/v1/kv/<key>" shape; for custom mode the
	// key is appended directly to the base path. The auth chain on
	// the handler will still apply — operators wanting to bypass
	// auth for a one-off probe should toggle the auth mode to
	// "none" first.
	probePath := buildProbePath(*ep, req.Key)
	q := buildProbeQuery(req.Variant, req.Version, req.Raw, req.Format)
	if q != "" {
		probePath += "?" + q
	}

	rec := httptest.NewRecorder()
	probe := httptest.NewRequest(http.MethodGet, probePath, nil)
	// Custom headers from the probe form. Operators use this to
	// supply auth tokens, tenant headers checked by request rules,
	// or anything else the live handler chain inspects.
	for k, v := range req.Headers {
		if k == "" {
			continue
		}
		probe.Header.Set(k, v)
	}
	// Backward-compat shortcut: a single Authorization header may
	// also arrive via X-PublicEndpoint-Auth (used by an earlier
	// UI revision). Honour it only when the operator did not
	// already set Authorization through the new headers map.
	if probe.Header.Get("Authorization") == "" {
		if ah := c.Request.Header.Get("X-PublicEndpoint-Auth"); ah != "" {
			probe.Header.Set("Authorization", ah)
		}
	}
	handler.ServeHTTP(rec, probe)

	headers := make(map[string]string, len(rec.Result().Header))
	for k, v := range rec.Result().Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	return c.SetStatus(http.StatusOK).SendJSON(struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	}{
		Status:  rec.Result().StatusCode,
		Headers: headers,
		Body:    rec.Body.String(),
	})
}

// testPublicEndpointRules dry-runs a draft request_check rule list
// without requiring the operator to save the endpoint. It runs the
// same Go evaluator used by live Endpoints and returns a trace so the
// UI can show matched rules, applied actions and the final request
// shape that would reach the mode shim.
func (a *api) testPublicEndpointRules(c *ada.Context) error {
	var req struct {
		RequestCheck service.RequestCheck `json:"request_check"`
		Method       string               `json:"method,omitempty"`
		Path         string               `json:"path"`
		Headers      map[string]string    `json:"headers,omitempty"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	ep := service.PublicEndpoint{
		Name:         "request-rule-test",
		ListenHost:   "127.0.0.1",
		ListenPort:   1,
		BasePath:     "/",
		Mode:         "static",
		Static:       &service.StaticCompat{},
		Auth:         service.EndpointAuth{Mode: "none"},
		RequestCheck: &req.RequestCheck,
	}
	if err := ep.Validate(); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	result, err := publicendpoint.TestRequestRules(&req.RequestCheck, req.Method, req.Path, req.Headers)
	if err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	return c.SendJSON(result)
}

// buildProbePath assembles the URL the test handler walks. For
// consul-mode endpoints we use the well-known /v1/kv/<key> shape
// so the shim's path parsing matches a real call. For custom- and
// static-mode endpoints we simply join the base path and the key;
// static resolves that path tail exactly like /data/<key>.
func buildProbePath(ep service.PublicEndpoint, key string) string {
	bp := ep.BasePath
	if bp == "" || bp == "/" {
		bp = ""
	}
	key = strings.TrimPrefix(key, "/")
	switch ep.Mode {
	case "consul":
		if key == "" {
			return bp + "/v1/kv/"
		}
		return bp + "/v1/kv/" + key
	default:
		if bp == "" {
			return "/" + key
		}
		return bp + "/" + key
	}
}

func buildProbeQuery(variant, version string, raw bool, format string) string {
	parts := []string{}
	if variant != "" {
		parts = append(parts, "variant="+variant)
	}
	if version != "" {
		parts = append(parts, "version="+version)
	}
	if raw {
		parts = append(parts, "raw")
	}
	if format != "" {
		parts = append(parts, "format="+format)
	}
	return strings.Join(parts, "&")
}

// reloadEventLog applies the built-in event logging toggle without touching
// hook delivery targets.
func (a *api) reloadEventLog(ctx context.Context) {
	settings, err := a.svc.Settings(ctx)
	if err != nil {
		slog.Error("failed to read settings for event log reload", "error", err)
		return
	}

	if a.dispatcher != nil {
		enabled := settings.EventLogEnabled()
		a.dispatcher.SetEventLogEnabled(enabled)
		slog.Info("event log setting reloaded", "enabled", enabled)
	}
}

// reloadHooks reads hooks from settings and updates the dispatcher.
func (a *api) reloadHooks(ctx context.Context) {
	settings, err := a.svc.Settings(ctx)
	if err != nil {
		slog.Error("failed to read settings for hook reload", "error", err)
		return
	}

	if a.dispatcher != nil {
		a.dispatcher.UpdateHooks(settings.Hooks)
		slog.Info("hooks reloaded", "count", len(settings.Hooks))
	}
}

// BuildHookDispatcher creates the hook dispatcher and wires up the
// config-data resolver. Hooks operate purely on configuration events
// in this build — the rawfs/raw-mount references that used to feed
// the resolver were extracted out of pika; PEM references that
// pointed at raw mounts are no longer supported here.
func BuildHookDispatcher(ctx context.Context, svc *service.Service) *hook.Dispatcher {
	settings, err := svc.Settings(ctx)
	if err != nil {
		slog.Warn("could not load settings for hook dispatcher", "error", err)
	}

	dispatcher := hook.NewDispatcher(256)
	if settings != nil {
		dispatcher.SetEventLogEnabled(settings.EventLogEnabled())
	}
	dispatcher.Start(ctx)

	// Resolver: only `config://` references are resolvable now that
	// raw mounts live in a different repo. Leftover `raw://...`
	// references in existing hook configs pass through as inline
	// PEM text and fail naturally if not valid.
	resolver := hook.NewResolver(
		func(ctx context.Context, key string) ([]byte, error) {
			file, err := svc.File(ctx, key, 0)
			if err != nil {
				return nil, err
			}
			return file.Data, nil
		},
	)
	dispatcher.SetResolver(resolver)

	if settings != nil && len(settings.Hooks) > 0 {
		dispatcher.UpdateHooks(settings.Hooks)
	}
	return dispatcher
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
