package external

import "github.com/rakunlabs/ok"

type External struct {
	Http       *ok.Config  `json:"http,omitempty"`
	Vault      *Vault      `json:"vault,omitempty"`
	Kubernetes *Kubernetes `json:"kubernetes,omitempty"`
	Consul     *Consul     `json:"consul,omitempty"`
	Etcd       *Etcd       `json:"etcd,omitempty"`
	AWS        *AWS        `json:"aws,omitempty"`
	GCP        *GCP        `json:"gcp,omitempty"`
	Azure      *Azure      `json:"azure,omitempty"`
}

// GCP configures a GCP Secret Manager external resource.
type GCP struct {
	// ServiceAccountJSON is the full JSON content of a service account key file.
	ServiceAccountJSON string `json:"service_account_json"`
}

// Kubernetes configures a Kubernetes API external resource.
type Kubernetes struct {
	// Kubeconfig is the path to a kubeconfig file.
	// If empty, in-cluster config is used (service account token at /var/run/secrets/kubernetes.io/serviceaccount/).
	Kubeconfig string `json:"kubeconfig,omitempty"`
}

type Vault struct {
	// Address is the Vault server URL (e.g., "https://vault.example.com").
	Address string `json:"address"`
	// Mount is the KV secrets engine mount path (e.g., "secret").
	Mount string `json:"mount"`

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
