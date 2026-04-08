package compat

import (
	"log/slog"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/server/compat/consul"
	"github.com/rakunlabs/pika/internal/service"
)

// Register registers all enabled compatibility adapters on the given mux.
// Each adapter gets its own route group with its configured base path.
func Register(m *ada.Mux, svc *service.Service, cfg *service.CompatSettings) {
	if cfg == nil {
		return
	}

	if cfg.ConsulKV != nil {
		basePath := cfg.ConsulKV.BasePath
		if basePath == "" {
			basePath = "/consul"
		}
		slog.Info("registering consul KV compat endpoint", "base_path", basePath)
		group := m.Group(basePath)
		consul.Register(group, svc)
	}
}
