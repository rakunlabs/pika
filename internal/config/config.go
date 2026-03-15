package config

import (
	"context"

	mforwardauth "github.com/rakunlabs/ada/middleware/forwardauth"
	"github.com/rakunlabs/chu"
	"github.com/rakunlabs/pika/internal/storage"
	"github.com/rakunlabs/tell"
)

var (
	ServiceName    = "pika"
	ServiceVersion = "v0.0.0"
	Service        = ServiceName + "/" + ServiceVersion
)

type Config struct {
	Storage storage.Config `cfg:"storage"`
	Server  Server         `cfg:"server"`

	Secret Secret `cfg:"secret"`

	Telemetry tell.Config `cfg:"telemetry"`
}

type Secret struct {
	// Enabled enables encryption of stored values.
	Enabled bool `cfg:"enabled" default:"false"`
	// EncryptionKey is the encryption key (any string, hashed with SHA-256 to derive 32 bytes).
	EncryptionKey string `cfg:"encryption_key"`
	// AdminSecret is required to perform key rotation via the API.
	AdminSecret string `cfg:"admin_secret"`
}

type Server struct {
	Host string `cfg:"host"`
	Port string `cfg:"port" default:"8080"`

	BasePath string `cfg:"base_path"`

	ForwardAuth *mforwardauth.ForwardAuth `cfg:"forward_auth"`
}

func Load(ctx context.Context) (*Config, error) {
	var cfg Config
	if err := chu.Load(ctx, ServiceName, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
