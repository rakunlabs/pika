package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"

	"github.com/rakunlabs/into"
	"github.com/rakunlabs/logi"
	"github.com/rakunlabs/tell"

	"github.com/rakunlabs/pika/internal/config"
	"github.com/rakunlabs/pika/internal/secret"
	"github.com/rakunlabs/pika/internal/secret/crypto"
	"github.com/rakunlabs/pika/internal/server"
	"github.com/rakunlabs/pika/internal/server/api"
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
	// Initialize storage
	store, err := storage.New(ctx, &cfg.Storage)
	if err != nil {
		return fmt.Errorf("init storage; %w", err)
	}

	defer store.Close()

	// //////////////////////////////////////
	// Initialize encryption if enabled
	var storeWrap service.Storage = store
	var encStore *secret.Storage

	if cfg.Secret.Enabled {
		if cfg.Secret.EncryptionKey == "" {
			return fmt.Errorf("secret.encryption_key is required when encryption is enabled")
		}

		key := sha256.Sum256([]byte(cfg.Secret.EncryptionKey))

		encryptor, err := crypto.NewChaCha20(key[:])
		if err != nil {
			return fmt.Errorf("init encryptor: %w", err)
		}

		encStore = secret.New(store, encryptor)
		storeWrap = encStore

		slog.Info("encryption enabled")
	}

	// //////////////////////////////////////
	// Initialize service
	svc := service.New(storeWrap)

	// //////////////////////////////////////
	// Seed user if built-in auth is configured
	if cfg.Server.Auth != nil && cfg.Server.Auth.SeedUser != nil {
		seed := cfg.Server.Auth.SeedUser
		if seed.Username != "" && seed.Password != "" {
			if err := svc.SeedUser(ctx, seed.Username, seed.Password); err != nil {
				return fmt.Errorf("seed user: %w", err)
			}
			slog.Info("seed user checked", "username", seed.Username)
		}
	}

	// //////////////////////////////////////
	// Start server
	info := api.Info{
		Name:    config.ServiceName,
		Version: config.ServiceVersion,
		Commit:  commit,
		Date:    date,
	}

	if err := server.Start(ctx, cfg, svc, info, encStore); err != nil {
		return fmt.Errorf("start server; %w", err)
	}

	return nil
}
