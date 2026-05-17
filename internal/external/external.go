package external

import "github.com/rakunlabs/ok"

type External struct {
	Http         *ok.Config    `json:"http,omitempty"`
	Vault        *Vault        `json:"vault,omitempty"`
	Kubernetes   *Kubernetes   `json:"kubernetes,omitempty"`
	Consul       *Consul       `json:"consul,omitempty"`
	Etcd         *Etcd         `json:"etcd,omitempty"`
	AWS          *AWS          `json:"aws,omitempty"`
	GCP          *GCP          `json:"gcp,omitempty"`
	GCPParameter *GCPParameter `json:"gcp_parameter,omitempty"`
	Azure        *Azure        `json:"azure,omitempty"`
}

// GCP configures a GCP Secret Manager external resource.
type GCP struct {
	// ServiceAccountJSON is the full JSON content of a service account key file.
	ServiceAccountJSON string `json:"service_account_json"`
}

// GCPParameter configures a GCP Parameter Manager external resource.
//
// Unlike Secret Manager, Parameter Manager is location-scoped: every
// parameter lives under projects/{p}/locations/{loc}/parameters/{name}.
// "global" is the default location and what most operators want when
// they don't care about regional isolation. Pika derives the project
// ID from the service-account JSON (same shape as GCP Secret Manager).
type GCPParameter struct {
	// ServiceAccountJSON is the full JSON content of a service account
	// key file with parametermanager.parameterAccessor (read) and
	// parametermanager.parameterVersionRenderer (render) permissions.
	ServiceAccountJSON string `json:"service_account_json"`
	// Location is the GCP location to query (e.g. "global", "us-central1").
	// Defaults to "global" when empty.
	Location string `json:"location,omitempty"`
}

// GetLocation returns the configured location, defaulting to "global".
func (g *GCPParameter) GetLocation() string {
	if g.Location != "" {
		return g.Location
	}
	return "global"
}

// Kubernetes configures a Kubernetes API external resource.
//
// Authentication selection (in priority order):
//  1. KubeconfigContent — full kubeconfig YAML pasted as a string.
//  2. Kubeconfig        — filesystem path to a kubeconfig file.
//  3. (neither set)     — in-cluster config: service account token mounted
//     at /var/run/secrets/kubernetes.io/serviceaccount/.
type Kubernetes struct {
	// Kubeconfig is the path to a kubeconfig file on the pika server filesystem.
	Kubeconfig string `json:"kubeconfig,omitempty"`
	// KubeconfigContent is the full YAML content of a kubeconfig, pasted directly.
	// Useful for managing the credentials entirely from the UI without writing a file.
	KubeconfigContent string `json:"kubeconfig_content,omitempty"`
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
