package config

import (
	"context"

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

	Telemetry tell.Config `cfg:"telemetry"`
}

type Server struct {
	Host string `cfg:"host"`
	Port string `cfg:"port" default:"8080"`

	BasePath string `cfg:"base_path"`
}

func Load(ctx context.Context) (*Config, error) {
	var cfg Config
	if err := chu.Load(ctx, ServiceName, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
