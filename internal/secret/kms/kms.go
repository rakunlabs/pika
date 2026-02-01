package kms

import (
	"context"
	"errors"
)

var (
	ErrNoKMSConfigured = errors.New("no KMS provider configured")
	ErrEncryptFailed   = errors.New("encryption failed")
	ErrDecryptFailed   = errors.New("decryption failed")
)

// Config holds the KMS configuration.
type Config struct {
	// Local configuration for file/env based key storage
	Local LocalConfig `cfg:"local"`

	// External allows mounting an external KMS provider
	// This is set programmatically, not via config
	External Provider `cfg:"-"`
}

// Provider defines the interface for Key Management Services.
// Cloud KMS providers (AWS, GCP, Azure) can implement this interface
// and be mounted via Config.External or passed directly to the crypto package.
type Provider interface {
	// Encrypt encrypts the data encryption key (DEK).
	Encrypt(ctx context.Context, plaintext []byte) (ciphertext []byte, err error)

	// Decrypt decrypts the wrapped DEK.
	Decrypt(ctx context.Context, ciphertext []byte) (plaintext []byte, err error)

	// GenerateDataKey generates a new DEK.
	// Returns both plaintext and ciphertext (wrapped) versions.
	GenerateDataKey(ctx context.Context) (plaintext, ciphertext []byte, err error)

	// Name returns the name of the KMS provider.
	Name() string
}

// New creates a new KMS provider based on configuration.
// Priority: External > Local
func New(ctx context.Context, cfg *Config) (Provider, error) {
	// Check for externally mounted provider first
	if cfg.External != nil {
		return cfg.External, nil
	}

	// Use local provider if configured
	if cfg.Local.Enabled {
		return NewLocal(&cfg.Local)
	}

	return nil, ErrNoKMSConfigured
}

// IsConfigured returns true if any KMS provider is configured.
func IsConfigured(cfg *Config) bool {
	return cfg.External != nil || cfg.Local.Enabled
}

// ProviderType represents the type of KMS provider.
type ProviderType string

const (
	ProviderTypeLocal    ProviderType = "local"
	ProviderTypeExternal ProviderType = "external"
)

// GetEnabledProvider returns the type of the enabled provider.
func GetEnabledProvider(cfg *Config) ProviderType {
	if cfg.External != nil {
		return ProviderTypeExternal
	}
	if cfg.Local.Enabled {
		return ProviderTypeLocal
	}
	return ""
}

// Mount sets an external KMS provider.
// This allows cloud KMS providers to be mounted without importing their SDKs.
//
// Example usage with AWS KMS (in your own code):
//
//	awsProvider := myawskms.New(ctx, keyID, region)
//	cfg.KMS.Mount(awsProvider)
func (c *Config) Mount(provider Provider) {
	c.External = provider
}
