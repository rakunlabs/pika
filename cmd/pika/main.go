package pika

import (
	"context"
	"fmt"

	"github.com/rakunlabs/into"
	"github.com/rakunlabs/logi"
	"github.com/rakunlabs/tell"

	"github.com/rakunlabs/pika/internal/config"
	"github.com/rakunlabs/pika/internal/server"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/pika/internal/storage"
)

var (
	commit = "-"
	date   = "-"
)

func main() {
	into.Init(run,
		into.WithLogger(logi.InitializeLog(logi.WithCaller(false))),
		into.WithMsgf("%s version:[%s] commit:[%s] date:[%s]", config.ServiceName, config.ServiceVersion, commit, date),
	)
}

func run(ctx context.Context) error {
	cfg, err := config.Load(ctx)
	if err != nil {
		return err
	}

	// //////////////////////////////////////
	// Initialize telemetry
	collector, err := tell.New(ctx, cfg.Telemetry)
	if err != nil {
		return fmt.Errorf("init telemetry; %w", err)
	}

	defer collector.Shutdown()

	// //////////////////////////////////////
	// Initialize service
	store, err := storage.New(ctx, &cfg.Storage)
	if err != nil {
		return fmt.Errorf("init storage; %w", err)
	}

	svc := service.New(store)

	// //////////////////////////////////////
	// Start server
	if err := server.Start(ctx, cfg, svc); err != nil {
		return fmt.Errorf("start server; %w", err)
	}

	return nil
}
