package external

import "github.com/worldline-go/klient"

type External struct {
	Http  *klient.Config `json:"http,omitempty"`
	Vault *Vault         `json:"vault,omitempty"`
}

type Vault struct {
	BasePath string `json:"base_path"`
	Address  string `json:"address"`

	AppRole *VaultAppRole `json:"app_role,omitempty"`
}

type VaultAppRole struct {
	RoleID          string `json:"role_id"`
	SecretID        string `json:"secret_id"`
	AppRoleBasePath string `json:"app_role_base_path"`
}
