package config

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	mforwardauth "github.com/rakunlabs/ada/middleware/forwardauth"
	"github.com/rakunlabs/chu"
	"github.com/rakunlabs/logi"
	"github.com/rakunlabs/pika/internal/storage"
	"github.com/rakunlabs/tell"
)

var (
	ServiceName = "pika"
	Service     = ServiceName
)

type Config struct {
	LogLevel string `cfg:"log_level" default:"info"`

	Storage storage.Config `cfg:"storage"`
	Server  Server         `cfg:"server"`

	Secret Secret `cfg:"secret"`

	Telemetry tell.Config `cfg:"telemetry"`
}

type Secret struct {
	// Enabled enables encryption of stored values.
	Enabled bool `cfg:"enabled" default:"false"`
	// EncryptionKey is the encryption key (any string, hashed with SHA-256 to derive 32 bytes).
	EncryptionKey string `cfg:"encryption_key" log:"-"`
	// AdminSecret is required to perform key rotation via the API.
	AdminSecret string `cfg:"admin_secret" log:"-"`
}

type Server struct {
	Host string `cfg:"host"`
	Port string `cfg:"port" default:"8080"`

	// PublicPort starts a second HTTP server serving only /data/* and /healthz
	// without token authentication. Leave empty to disable.
	PublicPort string `cfg:"public_port"`

	BasePath string `cfg:"base_path"`

	ForwardAuth *mforwardauth.ForwardAuth `cfg:"forward_auth"`

	// Auth enables built-in user/password authentication.
	// Mutually exclusive with ForwardAuth.
	Auth *Auth `cfg:"auth"`
}

// Auth configures built-in user/password authentication.
type Auth struct {
	// CookieSecret is used to sign session cookies (required).
	CookieSecret string `cfg:"cookie_secret" log:"-"`
	// SessionTTL is the session lifetime (default: 24h).
	SessionTTL time.Duration `cfg:"session_ttl" default:"24h"`
	// Cookie configures session cookie properties.
	Cookie CookieConfig `cfg:"cookie"`
	// SeedUser defines an initial admin user to create on first run if no users exist.
	SeedUser *SeedUser `cfg:"seed_user"`
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
	Secure bool `cfg:"secure" default:"false"`
	// SameSite controls cross-site cookie behavior.
	// Values: "lax" (default), "strict", "none".
	SameSite string `cfg:"same_site" default:"lax"`
}

// SeedUser defines an initial admin user for bootstrapping.
type SeedUser struct {
	Username string `cfg:"username"`
	Password string `cfg:"password" log:"-"`
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
