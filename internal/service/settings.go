package service

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/hook"
	"golang.org/x/crypto/bcrypt"
)

// RawMountEntry is a single raw mount configured via the UI.
type RawMountEntry struct {
	Prefix string           `json:"prefix"`
	Type   string           `json:"type,omitempty"` // "local" (default), "s3", "ftp", "sftp"
	Path   string           `json:"path,omitempty"` // for type=local
	S3     *S3ConfigEntry   `json:"s3,omitempty"`
	FTP    *FTPConfigEntry  `json:"ftp,omitempty"`
	SFTP   *SFTPConfigEntry `json:"sftp,omitempty"`
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

type Settings struct {
	External        map[string]external.External `json:"external,omitempty"`
	AdminSecretHash string                       `json:"admin_secret_hash,omitempty"`
	RawMounts       []RawMountEntry              `json:"raw_mounts,omitempty"`
	FTPShares       []FTPShareEntry              `json:"ftp_shares,omitempty"`
	FTPUsers        []FTPUserEntry               `json:"ftp_users,omitempty"`
	FTPServe        *FTPServeSettings            `json:"ftp_serve,omitempty"`
	SFTPServe       *SFTPServeSettings           `json:"sftp_serve,omitempty"`
	TFTPServe       *TFTPServeSettings           `json:"tftp_serve,omitempty"`
	Hooks           []hook.Hook                  `json:"hooks,omitempty"`
	PublicPort      *PublicPortSettings          `json:"public_port,omitempty"`
	Compat          *CompatSettings              `json:"compat,omitempty"`
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
	Action     ActionKey                    `json:"action"`
	External   map[string]external.External `json:"external,omitempty"`
	RawMounts  *[]RawMountEntry             `json:"raw_mounts,omitempty"` // pointer to distinguish nil (not provided) from empty
	FTPShares  *[]FTPShareEntry             `json:"ftp_shares,omitempty"` // pointer to distinguish nil from empty
	FTPUsers   *[]FTPUserEntry              `json:"ftp_users,omitempty"`
	FTPServe   *FTPServeSettings            `json:"ftp_serve,omitempty"`
	SFTPServe  *SFTPServeSettings           `json:"sftp_serve,omitempty"`
	TFTPServe  *TFTPServeSettings           `json:"tftp_serve,omitempty"`
	Hooks      *[]hook.Hook                 `json:"hooks,omitempty"` // pointer to distinguish nil from empty
	PublicPort *PublicPortSettings          `json:"public_port,omitempty"`
	Compat     *CompatSettings              `json:"compat,omitempty"`
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

	return s.UpdateSettings(ctx, settings)
}

func (s *Service) UpdateSettings(ctx context.Context, settings *Settings) error {
	return s.store.Settings().Set(ctx, settings)
}

// SetAdminSecret hashes the provided plaintext secret with bcrypt and stores it in settings.
// If a current secret is already set, currentSecret must match it.
func (s *Service) SetAdminSecret(ctx context.Context, currentSecret, newSecret string) error {
	if newSecret == "" {
		return fmt.Errorf("new secret is required: %w", ErrBadRequest)
	}

	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}

	// If an admin secret is already configured, validate the current one.
	if settings.AdminSecretHash != "" {
		if currentSecret == "" {
			return fmt.Errorf("current secret is required: %w", ErrBadRequest)
		}
		if err := bcrypt.CompareHashAndPassword([]byte(settings.AdminSecretHash), []byte(currentSecret)); err != nil {
			return fmt.Errorf("invalid current secret: %w", ErrForbidden)
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newSecret), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing admin secret: %w", err)
	}

	settings.AdminSecretHash = string(hash)

	return s.UpdateSettings(ctx, settings)
}

// VerifyAdminSecret checks the provided plaintext against the stored bcrypt hash.
// Returns ErrForbidden if the secret does not match, or ErrBadRequest if no secret is configured.
func (s *Service) VerifyAdminSecret(ctx context.Context, secret string) error {
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}

	if settings.AdminSecretHash == "" {
		return fmt.Errorf("admin secret is not configured: %w", ErrBadRequest)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(settings.AdminSecretHash), []byte(secret)); err != nil {
		return fmt.Errorf("invalid admin secret: %w", ErrForbidden)
	}

	return nil
}

// HasAdminSecret returns true if an admin secret has been configured.
func (s *Service) HasAdminSecret(ctx context.Context) (bool, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return false, err
	}

	return settings.AdminSecretHash != "", nil
}
