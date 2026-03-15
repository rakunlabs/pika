package external

import "github.com/worldline-go/klient"

type External struct {
	Http  *klient.Config `json:"http,omitempty"`
	Vault *Vault         `json:"vault,omitempty"`
}

type Vault struct {
	// BasePath is the KV mount path in Vault (e.g., "secret/data").
	// Config paths are appended to this when reading secrets.
	BasePath string `json:"base_path"`
	// Address is the Vault server URL (e.g., "https://vault.example.com").
	Address string `json:"address"`

	// AppRole holds AppRole authentication credentials.
	AppRole *VaultAppRole `json:"app_role,omitempty"`
	// Token is a direct Vault token (optional fallback, not recommended for production).
	Token string `json:"token,omitempty"`
}

type VaultAppRole struct {
	RoleID   string `json:"role_id"`
	SecretID string `json:"secret_id"`
	// AppRoleBasePath is the auth mount path for AppRole. Defaults to "approle".
	AppRoleBasePath string `json:"app_role_base_path,omitempty"`
}

// GetAppRoleBasePath returns the AppRole mount path, defaulting to "approle".
func (a *VaultAppRole) GetAppRoleBasePath() string {
	if a.AppRoleBasePath != "" {
		return a.AppRoleBasePath
	}
	return "approle"
}
