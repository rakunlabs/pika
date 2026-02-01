package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/rakunlabs/into"
	"github.com/rakunlabs/logi"
	"github.com/rakunlabs/tell"

	"github.com/rakunlabs/pika/internal/config"
	"github.com/rakunlabs/pika/internal/secret"
	"github.com/rakunlabs/pika/internal/secret/crypto"
	"github.com/rakunlabs/pika/internal/secret/kms"
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
	// Initialize storage
	store, err := storage.New(ctx, &cfg.Storage)
	if err != nil {
		return fmt.Errorf("init storage; %w", err)
	}

	defer store.Close()

	// //////////////////////////////////////
	// Initialize encryption if enabled
	var storeWrap service.Storage = store

	if cfg.Secret.Enabled {
		slog.Info("encryption enabled", "algorithm", cfg.Secret.Crypto.Algorithm)

		// Initialize KMS provider if configured
		var kmsProvider kms.Provider
		if kms.IsConfigured(&cfg.Secret.KMS) {
			kmsProvider, err = kms.New(ctx, &cfg.Secret.KMS)
			if err != nil {
				return fmt.Errorf("init kms; %w", err)
			}
			slog.Info("KMS provider initialized", "provider", kmsProvider.Name())
		}

		// Get database connection for key manager
		// Note: This requires the SQLite storage to expose its db connection
		db, err := getDB(store)
		if err != nil {
			return fmt.Errorf("get database connection for key manager; %w", err)
		}

		// Initialize key manager
		keyManager, err := crypto.NewKeyManager(db, kmsProvider)
		if err != nil {
			return fmt.Errorf("init key manager; %w", err)
		}

		// Initialize encryptor
		encryptor, err := crypto.NewChaCha20Poly1305(keyManager)
		if err != nil {
			return fmt.Errorf("init encryptor; %w", err)
		}

		// Initialize rotation scheduler
		var scheduler *crypto.RotationScheduler
		if cfg.Secret.Crypto.Rotation.Enabled {
			scheduler = crypto.NewRotationScheduler(keyManager, encryptor, cfg.Secret.Crypto.Rotation)
		}

		// Wrap storage with encryption
		encStore := secret.New(store, encryptor, scheduler)
		storeWrap = encStore

		// Start rotation scheduler if enabled
		if scheduler != nil {
			if err := encStore.StartRotation(ctx); err != nil {
				return fmt.Errorf("start rotation scheduler; %w", err)
			}
			defer encStore.StopRotation()
			slog.Info("key rotation enabled",
				"interval", cfg.Secret.Crypto.Rotation.Interval.String(),
				"reencrypt_on_start", cfg.Secret.Crypto.Rotation.ReencryptOnStart)
		}
	}

	// //////////////////////////////////////
	// Initialize service
	svc := service.New(storeWrap)

	// //////////////////////////////////////
	// Start server
	if err := server.Start(ctx, cfg, svc); err != nil {
		return fmt.Errorf("start server; %w", err)
	}

	return nil
}

// getDB extracts the database connection from the storage.
// This is a helper to access the underlying DB for the key manager.
func getDB(store storage.Storage) (*sql.DB, error) {
	// Try to get DB from storage that implements DBGetter
	if getter, ok := store.(interface{ DB() *sql.DB }); ok {
		return getter.DB(), nil
	}
	return nil, fmt.Errorf("storage does not expose database connection")
}
