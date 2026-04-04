package service

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/rakunlabs/pika/internal/external"
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

type Settings struct {
	External        map[string]external.External `json:"external,omitempty"`
	AdminSecretHash string                       `json:"admin_secret_hash,omitempty"`
	RawMounts       []RawMountEntry              `json:"raw_mounts,omitempty"`
	FTPShares       []FTPShareEntry              `json:"ftp_shares,omitempty"`
	FTPUsers        []FTPUserEntry               `json:"ftp_users,omitempty"`
}

// FTPUserEntry defines an FTP user account stored in settings.
type FTPUserEntry struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// Shares lists the share names this user can access. Empty = all shares.
	Shares   []string `json:"shares,omitempty"`
	ReadOnly bool     `json:"read_only"`
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
}

type PatchSettings struct {
	Action    ActionKey                    `json:"action"`
	External  map[string]external.External `json:"external,omitempty"`
	RawMounts *[]RawMountEntry             `json:"raw_mounts,omitempty"` // pointer to distinguish nil (not provided) from empty
	FTPShares *[]FTPShareEntry             `json:"ftp_shares,omitempty"` // pointer to distinguish nil from empty
	FTPUsers  *[]FTPUserEntry              `json:"ftp_users,omitempty"`
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
		settings.FTPShares = *patch.FTPShares
	}

	// Handle FTP users update (if provided)
	if patch.FTPUsers != nil {
		settings.FTPUsers = *patch.FTPUsers
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
