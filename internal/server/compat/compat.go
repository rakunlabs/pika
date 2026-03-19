package compat

import (
	"log/slog"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/config"
	"github.com/rakunlabs/pika/internal/server/compat/consul"
	"github.com/rakunlabs/pika/internal/service"
)

// Register registers all enabled compatibility adapters on the given mux.
// Each adapter gets its own route group with its configured base path.
func Register(m *ada.Mux, svc *service.Service, cfg *config.Compat) {
	if cfg == nil {
		return
	}

	if cfg.ConsulKV != nil {
		slog.Info("registering consul KV compat endpoint", "base_path", cfg.ConsulKV.BasePath)
		group := m.Group(cfg.ConsulKV.BasePath)
		consul.Register(group, svc)
	}
}
