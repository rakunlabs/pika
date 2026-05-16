package service

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/hook"
)

// RawMountEntry is a single raw mount configured via the UI.
type RawMountEntry struct {
	Prefix     string                 `json:"prefix"`
	Type       string                 `json:"type,omitempty"` // "local" (default), "s3", "ftp", "sftp", "webdav", "vercel-blob"
	Path       string                 `json:"path,omitempty"` // for type=local
	S3         *S3ConfigEntry         `json:"s3,omitempty"`
	FTP        *FTPConfigEntry        `json:"ftp,omitempty"`
	SFTP       *SFTPConfigEntry       `json:"sftp,omitempty"`
	WebDAV     *WebDAVConfigEntry     `json:"webdav,omitempty"`
	VercelBlob *VercelBlobConfigEntry `json:"vercelBlob,omitempty"`
}

// S3ConfigEntry holds S3 configuration stored in settings.
type S3ConfigEntry struct {
	Bucket    string `json:"bucket"`
	Region    string `json:"region,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"`
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`
	PathStyle bool   `json:"path_style,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	Secure    *bool  `json:"secure,omitempty"`
}

// FTPConfigEntry holds FTP configuration stored in settings.
type FTPConfigEntry struct {
	Host     string `json:"host"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	TLS      bool   `json:"tls,omitempty"`
	BasePath string `json:"base_path,omitempty"`
}

// SFTPConfigEntry holds SFTP (SSH) configuration stored in settings.
type SFTPConfigEntry struct {
	Host       string `json:"host"`
	Username   string `json:"username,omitempty"`
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	BasePath   string `json:"base_path,omitempty"`
}

// WebDAVConfigEntry holds WebDAV configuration stored in settings.
type WebDAVConfigEntry struct {
	URL      string `json:"url"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	BasePath string `json:"base_path,omitempty"`
}

// VercelBlobConfigEntry holds Vercel Blob configuration stored in settings.
type VercelBlobConfigEntry struct {
	Token   string `json:"token"`
	StoreID string `json:"store_id,omitempty"`
	Prefix  string `json:"prefix,omitempty"`
}

// PublicPortSettings configures the public (unauthenticated) HTTP server.
type PublicPortSettings struct {
	Enabled bool   `json:"enabled"`
	Port    string `json:"port,omitempty"` // e.g. "9090"
}

// CompatSettings configures compatibility endpoints on the public server.
type CompatSettings struct {
	ConsulKV *ConsulKVSettings `json:"consul_kv,omitempty"`
}

// ConsulKVSettings configures the Consul KV API compatibility layer.
type ConsulKVSettings struct {
	BasePath string `json:"base_path,omitempty"` // default: "/consul"
}

// ForwardAuthSettings configures forward-auth middleware that delegates
// authentication to an external service. When Enabled, the middleware is
// hot-swapped into the request pipeline via an ada.Slot; when disabled the
// slot becomes a no-op without a restart. Forward-auth can coexist with
// the built-in session auth — the combined middleware tries session first,
// then falls back to forward-auth.
type ForwardAuthSettings struct {
	Enabled                  bool     `json:"enabled"`
	Address                  string   `json:"address"`
	AuthResponseHeaders      []string `json:"auth_response_headers,omitempty"`
	AuthResponseHeadersRegex string   `json:"auth_response_headers_regex,omitempty"`
	AuthRequestHeaders       []string `json:"auth_request_headers,omitempty"`
	TrustForwardHeader       bool     `json:"trust_forward_header,omitempty"`
	InsecureSkipVerify       bool     `json:"insecure_skip_verify,omitempty"`
	Timeout                  string   `json:"timeout,omitempty"` // Go duration string, e.g. "10s"
	RedirectURL              string   `json:"redirect_url,omitempty"`
	RedirectCode             int      `json:"redirect_code,omitempty"`
	RedirectStatusCodes      []int    `json:"redirect_status_codes,omitempty"`
	RequestMethod            string   `json:"request_method,omitempty"`
}

// ExternalPermissionsSettings configures permission enforcement for
// forward-auth users. When Enabled, pika reads a groups header from
// each request and maps external group names to pika capability keys
// via Mapping. Superadmins is an allowlist of usernames that bypass
// all checks (equivalent to users.is_superadmin for built-in auth).
//
// When Enabled is false, forward-auth users keep the legacy
// "no permissions applied" behavior — their X-User flows through as
// an audit label but nothing is enforced.
type ExternalPermissionsSettings struct {
	// Enabled toggles permission enforcement under forward auth.
	Enabled bool `json:"enabled"`
	// GroupsHeader is the request header that carries the external group
	// list. Default: "X-Groups". The gateway must include this header in
	// its auth_response_headers allowlist for it to reach pika.
	GroupsHeader string `json:"groups_header,omitempty"`
	// GroupsSeparator is the delimiter inside a single header value.
	// Repeated header lines are also concatenated. Default: ",".
	GroupsSeparator string `json:"groups_separator,omitempty"`
	// Mapping translates external group names to pika capability keys.
	// Example: {"pika-editor": ["files.read", "files.write"]}
	Mapping map[string][]string `json:"mapping,omitempty"`
	// Superadmins is the allowlist of forward-auth usernames that bypass
	// all permission checks.
	Superadmins []string `json:"superadmins,omitempty"`
}

type Settings struct {
	External map[string]external.External `json:"external,omitempty"`
	// EncryptionVerifier is the ciphertext of a known plaintext used
	// to detect a wrong server-encryption key on unlock. Written
	// once at server initialization (POST /api/v1/key/initialize),
	// re-encrypted on rotation. Kept inside Settings rather than its
	// own table so the bootstrap path doesn't grow another schema
	// dependency. The plaintext format is documented in
	// service/keyops.go (verifierPlaintext()).
	EncryptionVerifier  []byte                       `json:"encryption_verifier,omitempty"`
	RawMounts           []RawMountEntry              `json:"raw_mounts,omitempty"`
	FTPShares           []FTPShareEntry              `json:"ftp_shares,omitempty"`
	FTPUsers            []FTPUserEntry               `json:"ftp_users,omitempty"`
	FTPServe            *FTPServeSettings            `json:"ftp_serve,omitempty"`
	SFTPServe           *SFTPServeSettings           `json:"sftp_serve,omitempty"`
	TFTPServe           *TFTPServeSettings           `json:"tftp_serve,omitempty"`
	WebDAVServe         *WebDAVServeSettings         `json:"webdav_serve,omitempty"`
	Hooks               []hook.Hook                  `json:"hooks,omitempty"`
	PublicPort          *PublicPortSettings          `json:"public_port,omitempty"`
	Compat              *CompatSettings              `json:"compat,omitempty"`
	ExternalPermissions *ExternalPermissionsSettings `json:"external_permissions,omitempty"`
	ForwardAuth         *ForwardAuthSettings         `json:"forward_auth,omitempty"`
	Auth                *AuthSettings                `json:"auth,omitempty"`
	UserSync            *UserSyncSettings            `json:"user_sync,omitempty"`
	Vault               *VaultSettings               `json:"vault,omitempty"`

	// SensitivePayload is the at-rest encrypted blob carrying the
	// user-supplied secret values for fields above (S3 access keys,
	// FTP passwords, hook webhook secrets, external-resource creds,
	// SFTP host private keys, etc.). The wrapper layer
	// (internal/secret) inflates this back into the typed fields
	// during Get and re-seals on Set; consumers of *Settings see
	// plaintext as before. The raw byte slot exists here so the
	// row-conversion code in internal/storage/bw can route it to
	// the underlying bucket without dragging the encryption layer
	// into bw. Empty/nil while the server is locked or for fresh
	// installs that haven't written a settings row yet.
	SensitivePayload []byte `json:"sensitive_payload,omitempty"`
}

// VaultSettings configures the personal-vault feature at the
// deployment level. The feature itself is shipped with every Pika
// build; this struct only lets an administrator turn the SPA + API
// surface on or off without redeploying.
//
// Why a "Disabled" flag instead of "Enabled":
//
//   - Backward compatibility. A pre-existing Settings row with no
//     vault config decodes to Vault == nil, and downstream code
//     treats nil as "default state". The default has been "vault
//     available" for the entire history of the feature, so a flag
//     whose zero value preserves that behaviour is "Disabled=false".
//     Flipping to "Enabled=false" would silently disable the vault
//     on every existing install during the upgrade.
//
// Disabling does NOT delete vault data. Every existing
// vault_accounts / vault_items row stays where it is; flipping
// Disabled=true just hides the routes from the API and the link
// from the SPA so no new operations can run. Re-enabling restores
// access to the same data with the same master password.
type VaultSettings struct {
	// Disabled hides the /vault feature from the UI and turns every
	// /api/v1/me/vault/* endpoint into a 404. Existing data is
	// preserved; this is a feature-flag, not a destructive action.
	Disabled bool `json:"disabled"`
}

// FTPServeSettings configures the built-in FTP server (stored in DB).
type FTPServeSettings struct {
	Enabled      bool   `json:"enabled"`
	Port         int    `json:"port,omitempty"`
	Host         string `json:"host,omitempty"`
	PublicIP     string `json:"public_ip,omitempty"`
	PassivePorts string `json:"passive_ports,omitempty"`
	// TLSCertFile is the path to a PEM-encoded TLS certificate file (or certificate chain).
	TLSCertFile string `json:"tls_cert_file,omitempty"`
	// TLSKeyFile is the path to the PEM-encoded TLS private key file.
	TLSKeyFile string `json:"tls_key_file,omitempty"`
	// TLSCertPEM is the PEM-encoded TLS certificate content (used when no file path is given).
	TLSCertPEM string `json:"tls_cert_pem,omitempty"`
	// TLSKeyPEM is the PEM-encoded TLS private key content (used when no file path is given).
	TLSKeyPEM string `json:"tls_key_pem,omitempty"`
	// TLSRequired controls TLS mode: 0 = disabled/optional, 1 = explicit FTPS (AUTH TLS required),
	// 2 = implicit FTPS (entire connection is TLS from the start).
	TLSRequired int `json:"tls_required,omitempty"`
}

// SFTPServeSettings configures the built-in SFTP server (stored in DB).
type SFTPServeSettings struct {
	Enabled     bool   `json:"enabled"`
	Port        int    `json:"port,omitempty"`
	Host        string `json:"host,omitempty"`
	HostKeyPath string `json:"host_key_path,omitempty"`
	// HostKeyPEM is the PEM-encoded SSH private key content (used when no file path is given).
	HostKeyPEM string `json:"host_key_pem,omitempty"`
}

// TFTPServeSettings configures the built-in TFTP server (stored in DB).
type TFTPServeSettings struct {
	Enabled bool   `json:"enabled"`
	Port    int    `json:"port,omitempty"`
	Host    string `json:"host,omitempty"`
}

// WebDAVServeSettings configures the built-in WebDAV server (stored in DB).
type WebDAVServeSettings struct {
	Enabled bool   `json:"enabled"`
	Port    int    `json:"port,omitempty"`
	Host    string `json:"host,omitempty"`
	Prefix  string `json:"prefix,omitempty"` // URL path prefix, default "/"
}

// FTPUserEntry defines an FTP user account stored in settings.
type FTPUserEntry struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	// Shares lists the share names this user can access. Empty = all shares.
	Shares []string `json:"shares,omitempty"`
	// AuthorizedKeys holds SSH public keys (one per line, OpenSSH authorized_keys format)
	// that are allowed to authenticate as this user via the SFTP server.
	AuthorizedKeys string `json:"authorized_keys,omitempty"`
	ReadOnly       bool   `json:"read_only"`
}

// FTPShareEntry defines a folder shared via the built-in FTP server.
// A share can reference one or more mount paths. When multiple paths are
// specified, their contents are merged into a single virtual directory.
type FTPShareEntry struct {
	// Name is the FTP folder name visible to clients.
	Name string `json:"name"`
	// Paths lists the mount paths included in this share.
	// Each path is formatted as "mount_prefix" or "mount_prefix/sub/folder".
	Paths []string `json:"paths"`
	// ReadOnly restricts FTP clients to read-only access on this share.
	ReadOnly bool `json:"read_only"`
	// Root, when true, mounts this share at the FTP root "/" so clients see its
	// contents directly instead of a /sharename/ prefix. Only one share may be root.
	Root bool `json:"root,omitempty"`
}

type PatchSettings struct {
	Action              ActionKey                    `json:"action"`
	External            map[string]external.External `json:"external,omitempty"`
	RawMounts           *[]RawMountEntry             `json:"raw_mounts,omitempty"` // pointer to distinguish nil (not provided) from empty
	FTPShares           *[]FTPShareEntry             `json:"ftp_shares,omitempty"` // pointer to distinguish nil from empty
	FTPUsers            *[]FTPUserEntry              `json:"ftp_users,omitempty"`
	FTPServe            *FTPServeSettings            `json:"ftp_serve,omitempty"`
	SFTPServe           *SFTPServeSettings           `json:"sftp_serve,omitempty"`
	TFTPServe           *TFTPServeSettings           `json:"tftp_serve,omitempty"`
	WebDAVServe         *WebDAVServeSettings         `json:"webdav_serve,omitempty"`
	Hooks               *[]hook.Hook                 `json:"hooks,omitempty"` // pointer to distinguish nil from empty
	PublicPort          *PublicPortSettings          `json:"public_port,omitempty"`
	Compat              *CompatSettings              `json:"compat,omitempty"`
	ExternalPermissions *ExternalPermissionsSettings `json:"external_permissions,omitempty"`
	ForwardAuth         *ForwardAuthSettings         `json:"forward_auth,omitempty"`
	Auth                *AuthSettings                `json:"auth,omitempty"`
	UserSync            *UserSyncSettings            `json:"user_sync,omitempty"`
	Vault               *VaultSettings               `json:"vault,omitempty"`
}

type ActionKey string

const (
	ActionKeySet    ActionKey = "set"
	ActionKeyRemove ActionKey = "remove"
)

func (s *Service) Settings(ctx context.Context) (*Settings, error) {
	settings, err := s.store.Settings().Get(ctx)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Return empty settings on first use
			return &Settings{
				External: make(map[string]external.External),
			}, nil
		}
		return nil, err
	}

	return settings, nil
}

func (s *Service) PatchSettings(ctx context.Context, patch *PatchSettings) error {
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}

	switch patch.Action {
	case ActionKeySet:
		if settings.External == nil {
			settings.External = make(map[string]external.External)
		}
		maps.Copy(settings.External, patch.External)
	case ActionKeyRemove:
		if settings.External != nil {
			for k := range patch.External {
				delete(settings.External, k)
			}
		}
	default:
		return ErrBadRequest
	}

	// Handle raw mounts update (if provided)
	if patch.RawMounts != nil {
		settings.RawMounts = *patch.RawMounts
	}

	// Handle FTP shares update (if provided)
	if patch.FTPShares != nil {
		// Validate: at most one share can be root
		rootCount := 0
		for _, s := range *patch.FTPShares {
			if s.Root {
				rootCount++
			}
		}
		if rootCount > 1 {
			return fmt.Errorf("only one FTP share can be mounted at root: %w", ErrBadRequest)
		}
		settings.FTPShares = *patch.FTPShares
	}

	// Handle FTP users update (if provided)
	if patch.FTPUsers != nil {
		settings.FTPUsers = *patch.FTPUsers
	}

	// Handle server config updates (if provided)
	if patch.FTPServe != nil {
		settings.FTPServe = patch.FTPServe
	}
	if patch.SFTPServe != nil {
		settings.SFTPServe = patch.SFTPServe
	}
	if patch.TFTPServe != nil {
		settings.TFTPServe = patch.TFTPServe
	}
	if patch.WebDAVServe != nil {
		settings.WebDAVServe = patch.WebDAVServe
	}

	// Handle hooks update (if provided)
	if patch.Hooks != nil {
		settings.Hooks = *patch.Hooks
	}

	// Handle public port update (if provided)
	if patch.PublicPort != nil {
		settings.PublicPort = patch.PublicPort
	}

	// Handle compat update (if provided)
	if patch.Compat != nil {
		settings.Compat = patch.Compat
	}

	// Handle external-permissions update (if provided)
	if patch.ExternalPermissions != nil {
		settings.ExternalPermissions = patch.ExternalPermissions
	}

	// Handle forward-auth update (if provided)
	if patch.ForwardAuth != nil {
		settings.ForwardAuth = patch.ForwardAuth
	}

	// Handle auth settings update (if provided)
	if patch.Auth != nil {
		settings.Auth = patch.Auth
	}

	// Handle user-sync settings update (if provided)
	if patch.UserSync != nil {
		settings.UserSync = patch.UserSync
	}

	// Handle vault settings update (if provided). The struct only
	// carries a boolean today; we still update the whole pointer so
	// future fields (e.g. per-deployment item-type allowlist) get
	// the same patch-update treatment for free.
	if patch.Vault != nil {
		settings.Vault = patch.Vault
	}

	return s.UpdateSettings(ctx, settings)
}

// GetExternalPermissionsSettings returns the current external-permissions
// configuration or nil if none has been set. Used by the API layer to
// decide whether to enforce permissions under forward auth and by the
// permission resolver to map external groups to capability keys.
func (s *Service) GetExternalPermissionsSettings(ctx context.Context) *ExternalPermissionsSettings {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil
	}
	return settings.ExternalPermissions
}

// GetForwardAuthSettings returns the current forward-auth configuration
// or nil if none has been set. Used by the server layer to configure
// the forward-auth Slot at startup and on settings changes.
func (s *Service) GetForwardAuthSettings(ctx context.Context) *ForwardAuthSettings {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil
	}
	return settings.ForwardAuth
}

func (s *Service) UpdateSettings(ctx context.Context, settings *Settings) error {
	return s.store.Settings().Set(ctx, settings)
}

// SaveSettings persists a full Settings object — used by the auth migration path at boot.
func (s *Service) SaveSettings(ctx context.Context, settings *Settings) error {
	return s.store.Settings().Set(ctx, settings)
}
