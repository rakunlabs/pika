package config

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/rakunlabs/chu"
	"github.com/rakunlabs/logi"
	"github.com/rakunlabs/pika/internal/storage"
	"github.com/rakunlabs/tell"
)

var (
	ServiceName = "pika"
	Service     = ServiceName
	Version     = "v0.0.0"
)

type Config struct {
	LogLevel string `cfg:"log_level" default:"info"`

	Storage storage.Config `cfg:"storage"`
	Server  Server         `cfg:"server"`

	Secret Secret `cfg:"secret"`

	Telemetry tell.Config `cfg:"telemetry"`
}

type Secret struct {
	// EncryptionKey is the encryption key (any string, hashed with SHA-256 to derive 32 bytes).
	// When set (non-empty), encryption is enabled automatically.
	EncryptionKey string `cfg:"encryption_key" log:"-"`
}

type Server struct {
	Host string `cfg:"host"`
	Port string `cfg:"port" default:"8080"`

	BasePath string `cfg:"base_path"`

	// Auth configures built-in user/password authentication.
	Auth Auth `cfg:"auth"`
}

// Auth configures built-in user/password authentication.
// Built-in auth is always enabled. The first visit to the UI will show a
// setup screen to create the initial admin account.
type Auth struct {
	// SessionTTL is the session lifetime (default: 24h).
	SessionTTL time.Duration `cfg:"session_ttl"`
	// Cookie configures session cookie properties.
	Cookie CookieConfig `cfg:"cookie"`
}

// CookieConfig configures the session cookie.
type CookieConfig struct {
	// Name is the cookie name (default: "pika_session").
	Name string `cfg:"name"`
	// Domain sets the cookie domain. Empty means the current host.
	Domain string `cfg:"domain"`
	// Path sets the cookie path (default: "/").
	Path string `cfg:"path"`
	// Secure marks the cookie as HTTPS-only (default: false).
	// Should be true in production behind TLS.
	Secure bool `cfg:"secure"`
	// SameSite controls cross-site cookie behavior.
	// Values: "lax" (default), "strict", "none".
	SameSite string `cfg:"same_site"`
}

func Load(ctx context.Context) (*Config, error) {
	var cfg Config
	if err := chu.Load(ctx, ServiceName, &cfg); err != nil {
		return nil, err
	}

	if err := logi.SetLogLevel(cfg.LogLevel); err != nil {
		return nil, fmt.Errorf("set log level %s: %w", cfg.LogLevel, err)
	}

	slog.Info("loaded configuration", "config", chu.MarshalMap(cfg))

	return &cfg, nil
}
