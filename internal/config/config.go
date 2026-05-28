package config

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rakunlabs/chu"
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

	Storage storage.Config `cfg:"storage"`
	Server  Server         `cfg:"server"`
	Cluster cluster.Config `cfg:"cluster"`

	Telemetry tell.Config `cfg:"telemetry"`
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
