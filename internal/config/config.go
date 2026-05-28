package config

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rakunlabs/chu"
	_ "github.com/rakunlabs/chu/loader/external/loaderconsul"
	_ "github.com/rakunlabs/chu/loader/external/loadergcpparameter"
	_ "github.com/rakunlabs/chu/loader/external/loadergcpsecret"
	_ "github.com/rakunlabs/chu/loader/external/loadervault"
	"github.com/rakunlabs/chu/loader/loaderenv"
	"github.com/rakunlabs/logi"
	"github.com/rakunlabs/pika/internal/cluster"
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

	Storage    storage.Config `cfg:"storage"`
	Server     Server         `cfg:"server"`
	Cluster    cluster.Config `cfg:"cluster"`
	Encryption Encryption     `cfg:"encryption"`

	Telemetry tell.Config `cfg:"telemetry"`
}

// Encryption carries optional at-rest passphrase that the operator
// can supply through the config file (or PIKA_ENCRYPTION_PASSWORD).
//
// Semantics applied at boot in cmd/pika/main.go:
//
//   - Empty: legacy behaviour. The server starts locked if a verifier
//     exists on disk and the operator must enter the key through the
//     UnlockScreen on every restart.
//   - Set + already-initialized: the server attempts auto-unlock with
//     the supplied passphrase. Success → fully online, no manual
//     step. Failure (wrong passphrase) → server stays LOCKED, the
//     UnlockScreen still asks for a key, and /api/v1/info carries
//     a warning flag so the SPA can tell the operator their config
//     value is bad.
//   - Set + not yet initialized: the server auto-initializes using
//     this passphrase. The supplied value becomes the at-rest key.
//
// SECURITY NOTE: when populated this field defeats the original
// "key never lives on disk" design. We mask it from `chu.MarshalMap`
// via `log:"-"` so it doesn't leak into the loaded-configuration
// log line, but it IS readable to anyone who can read the config
// file or process env. Operators trading manual-unlock UX for
// at-rest-key-on-disk should understand that trade-off.
type Encryption struct {
	Password string `cfg:"password" log:"-"`
}

type Server struct {
	Host string `cfg:"host"`
	Port string `cfg:"port" default:"8080"`

	BasePath string `cfg:"base_path"`
	TLS      TLS    `cfg:"tls"`
}

type TLS struct {
	// Enabled controls whether the admin listener can serve HTTPS.
	// Runtime settings can still allow plaintext HTTP on the same port.
	Enabled bool `cfg:"enabled" default:"true"`
	// CertFile and KeyFile are PEM paths. When either is empty Pika
	// stores an auto-managed pair under the storage directory.
	CertFile string `cfg:"cert_file"`
	KeyFile  string `cfg:"key_file"`
}

func Load(ctx context.Context) (*Config, error) {
	var cfg Config
	if err := chu.Load(ctx, ServiceName, &cfg,
		chu.WithLoaderOption(loaderenv.New(
			loaderenv.WithPrefix("PIKA_"),
		)),
		chu.WithVersion(Version),
	); err != nil {
		return nil, err
	}

	if err := logi.SetLogLevel(cfg.LogLevel); err != nil {
		return nil, fmt.Errorf("set log level %s: %w", cfg.LogLevel, err)
	}

	basePath, err := normalizeBasePath(cfg.Server.BasePath)
	if err != nil {
		return nil, fmt.Errorf("server.base_path: %w", err)
	}
	cfg.Server.BasePath = basePath

	slog.Info("loaded configuration", "config", chu.MarshalMap(cfg))

	return &cfg, nil
}

func normalizeBasePath(raw string) (string, error) {
	bp := strings.TrimSpace(raw)
	if bp == "" || bp == "/" {
		return "", nil
	}
	if !strings.HasPrefix(bp, "/") {
		return "", fmt.Errorf("must start with /")
	}
	if strings.ContainsAny(bp, "?#") {
		return "", fmt.Errorf("must be a path only, without query or fragment")
	}
	if strings.ContainsAny(bp, " \t\r\n") {
		return "", fmt.Errorf("must not contain whitespace")
	}
	bp = strings.TrimRight(bp, "/")
	if strings.Contains(bp, "//") {
		return "", fmt.Errorf("must not contain empty path segments")
	}
	return bp, nil
}
