package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/rakunlabs/into"
	"github.com/rakunlabs/logi"
	"github.com/rakunlabs/tell"

	"github.com/rakunlabs/pika/internal/cluster"
	"github.com/rakunlabs/pika/internal/config"
	"github.com/rakunlabs/pika/internal/secret"
	"github.com/rakunlabs/pika/internal/secret/keymgr"
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
	// Initialize cluster (no-op when not enabled). Built here so it can
	// be wrapped around storage and shared with the HTTP server.
	cl, err := cluster.New(cfg.Cluster, store.DB())
	if err != nil {
		return fmt.Errorf("init cluster; %w", err)
	}
	defer cl.Stop()

	// //////////////////////////////////////
	// Initialize the at-rest encryption key manager.
	//
	// Lifecycle:
	//   - Fresh install (no verifier on disk): the manager starts
	//     uninitialized and the lockgate middleware is a no-op.
	//     Pika serves requests in plaintext-at-rest mode until an
	//     operator opts in via Settings → Server encryption key.
	//   - Already-initialized install: the manager starts LOCKED
	//     and the lockgate 503s every non-allowlisted request until
	//     an admin unlocks via POST /api/v1/key/unlock or the UI.
	//
	// This replaces the prior PIKA_SECRET_ENCRYPTION_KEY env: the
	// key is no longer stored on disk or in the process environment;
	// once enabled, the operator types it after every restart.
	mgr := keymgr.New()
	encStore := secret.New(store, mgr)
	var storeWrap service.Storage = encStore

	// //////////////////////////////////////
	// Initialize service
	svc := service.New(storeWrap)
	svc.SetKeyManager(mgr)
	// Hand the service a server-lifetime context so background workers
	// (e.g. Vault AppRole token renewal) survive past the request that
	// first triggers them and are cancelled cleanly on shutdown.
	svc.SetRootContext(ctx)

	// Bootstrap-time hint: if the verifier is already on disk, mark
	// the manager as initialized so the lockgate engages and the SPA
	// renders the unlock screen immediately. Otherwise the system is
	// fresh and serves normally until the operator opts in.
	st, stErr := svc.GetKeyStatus(ctx)
	if stErr == nil && st.Initialized {
		mgr.MarkInitialized()
		slog.Info("server started; encryption enabled, awaiting unlock", "initialized", true)
	} else {
		slog.Info("server started; encryption not yet enabled (opt in via Settings → Server encryption key)", "initialized", false)
	}

	// Optional auto-unlock / auto-initialize from config. When the
	// operator supplies `encryption.password` (config file or
	// PIKA_ENCRYPTION_PASSWORD env) we react in one of three ways:
	//
	//   - Already-initialized + correct passphrase → auto-unlock.
	//     The server transitions to fully online without the operator
	//     touching the UnlockScreen.
	//   - Already-initialized + WRONG passphrase → leave the manager
	//     locked, raise `encryptionConfigInvalid`, and continue boot.
	//     The lockgate still engages and the SPA renders the unlock
	//     screen, but it shows a warning that the config-supplied
	//     value didn't match. The operator can still unlock manually
	//     with the real key.
	//   - Not initialized → auto-initialize with the supplied
	//     passphrase. This is the same flow Settings → Server
	//     encryption key uses; the verifier is written and the
	//     manager is left unlocked.
	//
	// stErr above is treated as "skip auto-handling": if we can't
	// read settings we have no reliable way to tell initialized from
	// not-initialized; the operator will see the unlock screen on
	// the next boot once the underlying read succeeds.
	encryptionConfigInvalid := false
	if cfg.Encryption.Password != "" && stErr == nil {
		switch {
		case st.Initialized:
			if err := svc.UnlockServerKey(ctx, cfg.Encryption.Password); err != nil {
				// Wrong passphrase is the expected failure here
				// (ErrForbidden). We intentionally do NOT abort
				// startup — the operator can still recover by
				// entering the real key through the UnlockScreen,
				// and the warning flag tells the SPA to call out
				// the bad config value.
				slog.Warn("auto-unlock with config password failed; server remains locked", "err", err)
				encryptionConfigInvalid = true
			} else {
				slog.Info("server auto-unlocked using encryption.password from config")
			}
		default:
			// Fresh install: auto-initialize. Failure here is rarer
			// (typically internal errors) but still non-fatal — the
			// operator can opt in later through Settings.
			if err := svc.InitializeServerKey(ctx, cfg.Encryption.Password); err != nil {
				slog.Error("auto-initialize with config password failed; encryption stays disabled", "err", err)
			} else {
				slog.Info("server auto-initialized using encryption.password from config")
			}
		}
	}

	// //////////////////////////////////////
	// Start server
	info := api.Info{
		Name:                    config.ServiceName,
		Version:                 version,
		Commit:                  commit,
		Date:                    date,
		ManagedTLSEnabled:       cfg.Server.TLS.Enabled,
		EncryptionConfigInvalid: encryptionConfigInvalid,
	}

	if err := server.Start(ctx, cfg, svc, info, encStore, cl); err != nil {
		return fmt.Errorf("start server; %w", err)
	}

	return nil
}
