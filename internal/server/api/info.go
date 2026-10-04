package api

import (
	"net/http"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/pika/internal/service"
)

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
	userID := ""
	var resolvedIdentitySubject string

	if a.mgr != nil {
		id, capKeys, resolvedUser, resolvedUserID, _ := a.mgr.ResolveRequest(c.Request)
		if id != nil {
			resolvedIdentitySubject = id.Subject
			userID = resolvedUserID
			username = resolvedUser
			if username == "" {
				username = id.Subject
			}
			caps = capKeys
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
	var subtitle, localLoginName string
	localLoginFormCollapsed := false
	authSettings := a.svc.GetAuthSettings(ctx)
	if as := authSettings; as != nil {
		subtitle = as.UI.Subtitle
		if as.Local != nil && as.Local.Enabled {
			localLoginName = as.Local.Name
			if localLoginName == "" {
				localLoginName = "local"
			}
			localLoginFormCollapsed = as.Local.LoginFormCollapsed
		}
		for _, subject := range as.Capabilities.Superadmins {
			if subject == resolvedIdentitySubject {
				isSuperadmin = true
				break
			}
		}
	}
	if !isSuperadmin && userID != "" {
		if user, err := a.svc.GetUserByID(ctx, userID); err == nil && user != nil {
			isSuperadmin = user.IsSuperadmin
		}
	}
	accountSecurityAvailable := authSettings.AccountSecurityAllowed(isSuperadmin)

	// VaultEnabled mirrors a.svc.VaultEnabled(ctx) so the SPA can
	// gate the /vault link in the navigation. This combines the
	// boot-time gate (cmd/pika wiring) with the admin runtime
	// toggle in Settings → Security → Personal vault, so the link
	// disappears the moment an admin flips the toggle.
	vaultEnabled := a.svc.VaultEnabled(ctx)

	resp := struct {
		Info
		Subtitle                 string                  `json:"subtitle,omitempty"`
		User                     string                  `json:"user,omitempty"`
		AuthEnabled              bool                    `json:"auth_enabled"`
		BuiltinAuth              bool                    `json:"builtin_auth"`
		IsSuperadmin             bool                    `json:"is_superadmin"`
		Permissions              []string                `json:"permissions"`
		Capabilities             []service.Capability    `json:"capabilities"`
		SetupRequired            bool                    `json:"setup_required,omitempty"`
		VaultEnabled             bool                    `json:"vault_enabled"`
		VaultItemTypes           []service.VaultItemType `json:"vault_item_types,omitempty"`
		LocalLoginName           string                  `json:"local_login_name,omitempty"`
		LocalLoginFormCollapsed  bool                    `json:"local_login_form_collapsed"`
		AccountSecurityAvailable bool                    `json:"account_security_available"`
	}{
		Info:                     a.info,
		Subtitle:                 subtitle,
		User:                     username,
		AuthEnabled:              true,
		BuiltinAuth:              true,
		IsSuperadmin:             isSuperadmin,
		Permissions:              caps,
		Capabilities:             service.KnownCapabilities,
		VaultEnabled:             vaultEnabled,
		VaultItemTypes:           service.KnownVaultItemTypes,
		LocalLoginName:           localLoginName,
		LocalLoginFormCollapsed:  localLoginFormCollapsed,
		AccountSecurityAvailable: accountSecurityAvailable,
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
