package external

import (
	"strings"

	"github.com/rakunlabs/ok"
)

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

	// RawValue controls how GCPProvider.Read returns the secret to
	// direct callers (public endpoints, /external/{name}/read).
	//
	//   - nil / false (default): legacy behaviour. Non-JSON payloads
	//     are wrapped as `{"value": "<string>"}` and the response is
	//     served as application/json. This default is intentionally
	//     conservative — the External page editor and the
	//     InheritDialog preview both read `entry.data.value` and would
	//     render an empty form if we removed the wrapper on existing
	//     resources without an opt-in.
	//   - true: GCPProvider.Read returns the secret payload as raw
	//     bytes with Entry.ContentType = GetContentType(). The
	//     "value" wrapper is NOT added — operators who store YAML or
	//     plaintext in Secret Manager get those bytes back verbatim.
	//     Opt in per resource via the "Return raw value" checkbox.
	//
	// Stored as *bool so resources that predate this field stay on
	// the legacy default automatically, and explicit opt-outs after
	// the field is added survive config round-trips.
	//
	// Note: this does NOT affect Fetch() (the inheritance pipeline).
	// That path still emits JSON because the consumer-side
	// `InheritEntry.Format` hint already handles unwrapping per the
	// existing decodeWrappedValue contract.
	RawValue *bool `json:"raw_value,omitempty"`

	// ContentType is the HTTP Content-Type header applied by
	// GCPProvider.Read when RawValue is on. Empty falls back to
	// "application/yaml" — most operators using raw mode store YAML
	// blobs in Secret Manager, and serving them with the matching
	// content type lets browsers and downstream consumers render /
	// parse them correctly without a manual override. Ignored when
	// RawValue is false (the legacy wrapper always serves JSON).
	ContentType string `json:"content_type,omitempty"`

	// Proxy is an optional outbound HTTP/HTTPS/SOCKS5 proxy URL used
	// for all requests to GCP Secret Manager. Empty falls back to the
	// HTTP_PROXY / HTTPS_PROXY / NO_PROXY environment variables.
	Proxy string `json:"proxy,omitempty"`
}

// GetRawValue reports whether GCPProvider.Read should return the raw
// secret bytes. The default is false (legacy wrapper preserved); a
// nil receiver also returns false so callers don't need a separate
// guard. See the field doc for why the default is conservative.
func (g *GCP) GetRawValue() bool {
	if g == nil || g.RawValue == nil {
		return false
	}
	return *g.RawValue
}

// GetContentType returns the Content-Type GCPProvider.Read should use
// when RawValue is on. Empty / whitespace falls back to
// "application/yaml".
func (g *GCP) GetContentType() string {
	if g != nil {
		if ct := strings.TrimSpace(g.ContentType); ct != "" {
			return ct
		}
	}
	return "application/yaml"
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

	// Proxy is an optional outbound HTTP/HTTPS/SOCKS5 proxy URL used
	// for all requests to GCP Parameter Manager. Empty falls back to
	// the HTTP_PROXY / HTTPS_PROXY / NO_PROXY environment variables.
	Proxy string `json:"proxy,omitempty"`
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

	// Proxy is an optional outbound HTTP/HTTPS/SOCKS5 proxy URL used
	// for all requests to the Kubernetes API. Empty falls back to the
	// HTTP_PROXY / HTTPS_PROXY / NO_PROXY environment variables.
	Proxy string `json:"proxy,omitempty"`
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

	// Proxy is an optional outbound HTTP/HTTPS/SOCKS5 proxy URL used
	// for all requests to this Vault. Empty falls back to the
	// HTTP_PROXY / HTTPS_PROXY / NO_PROXY environment variables.
	Proxy string `json:"proxy,omitempty"`
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
