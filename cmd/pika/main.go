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
	_ "github.com/rakunlabs/pika/internal/rawfs/ftpfs"  // register FTP backend
	_ "github.com/rakunlabs/pika/internal/rawfs/s3fs"   // register S3 backend
	_ "github.com/rakunlabs/pika/internal/rawfs/sftpfs" // register SFTP backend
	"github.com/rakunlabs/pika/internal/secret"
	"github.com/rakunlabs/pika/internal/secret/crypto"
	"github.com/rakunlabs/pika/internal/server"
	"github.com/rakunlabs/pika/internal/server/api"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/pika/internal/storage"
)

var (
	version = "v0.0.0"
	commit  = "-"
	date    = "-"
)

func main() {
	config.Version = version
	config.Service += "/" + version

	into.Init(run,
		into.WithLogger(logi.InitializeLog(logi.WithCaller(false))),
		into.WithMsgf("%s version:[%s] commit:[%s] date:[%s]", config.ServiceName, version, commit, date),
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

	if cfg.Secret.EncryptionKey != "" {
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
	// Start server
	info := api.Info{
		Name:    config.ServiceName,
		Version: version,
		Commit:  commit,
		Date:    date,
	}

	if err := server.Start(ctx, cfg, svc, info, encStore); err != nil {
		return fmt.Errorf("start server; %w", err)
	}

	return nil
}
