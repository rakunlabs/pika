package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/into"

	mcors "github.com/rakunlabs/ada/middleware/cors"
	mlog "github.com/rakunlabs/ada/middleware/log"
	mrecover "github.com/rakunlabs/ada/middleware/recover"
	mrequestid "github.com/rakunlabs/ada/middleware/requestid"
	mserver "github.com/rakunlabs/ada/middleware/server"
	mtelemetry "github.com/rakunlabs/ada/middleware/telemetry"

	"github.com/rakunlabs/pika/internal/config"
	"github.com/rakunlabs/pika/internal/secret"
	"github.com/rakunlabs/pika/internal/serve/ftpserve"
	"github.com/rakunlabs/pika/internal/serve/sftpserve"
	"github.com/rakunlabs/pika/internal/serve/tftpserve"
	"github.com/rakunlabs/pika/internal/serve/webdavserve"
	"github.com/rakunlabs/pika/internal/server/api"
	"github.com/rakunlabs/pika/internal/server/authx"
	"github.com/rakunlabs/pika/internal/server/compat"
	"github.com/rakunlabs/pika/internal/service"
)

// cookieName returns the session cookie name, preferring the AuthSettings
// value, then the config value, then the default "pika_session".
func cookieName(s *service.AuthSettings, cfg *config.Config) string {
	if s != nil && s.Cookie.Name != "" {
		return s.Cookie.Name
	}
	if cfg.Server.Auth.Cookie.Name != "" {
		return cfg.Server.Auth.Cookie.Name
	}
	return "pika_session"
}

func Start(ctx context.Context, cfg *config.Config, svc *service.Service, info api.Info, encStore *secret.Storage) error {
	// Build initial raw mount handler from DB settings (includes hook dispatcher)
	rh := api.BuildInitialRawHandler(ctx, svc)

	// Wire the hook dispatcher into the service for config-level events
	if d := api.GetDispatcher(rh); d != nil {
		svc.SetHookDispatcher(d)
	}

	server := ada.New()
	server.Use(
		mrecover.Middleware(),
		mserver.Middleware(config.Service),
		mcors.Middleware(),
		mrequestid.Middleware(),
		mlog.Middleware(),
		mtelemetry.Middleware(),
	)

	mData := server.Group(cfg.Server.BasePath)
	m := server.Group(cfg.Server.BasePath)
	mAuth := server.Group(cfg.Server.BasePath)

	// --- Authentication setup via authx.Manager ---

	// Migrate legacy forward-auth / external-permissions settings into AuthSettings on boot.
	boot, err := svc.Settings(ctx)
	if err != nil {
		return fmt.Errorf("read settings for auth boot: %w", err)
	}
	if boot != nil && boot.Auth == nil && (boot.ForwardAuth != nil || boot.ExternalPermissions != nil) {
		service.MigrateLegacyAuthSettings(boot)
		if err := svc.SaveSettings(ctx, boot); err != nil {
			slog.Warn("auth settings migration write-back failed", "error", err)
		}
	}

	authSettings := svc.GetAuthSettings(ctx)

	mgr := authx.New(authx.Deps{
		Svc:          svc,
		SessionStore: authx.NewSessionStore(svc, cookieName(authSettings, cfg)),
		BasePath:     cfg.Server.BasePath + "/",
		CookieName:   cookieName(authSettings, cfg),
		// Version comes from build-time ldflags (cmd/pika/main.go),
		// not from settings — so the login UI shows the real binary
		// version and no operator can spoof it.
		Version: info.Version,
	})
	if err := mgr.Boot(ctx, authSettings); err != nil {
		return fmt.Errorf("auth manager boot: %w", err)
	}

	// Brute-force protection on the unprotected auth group: rate-limit
	// POST /login/pass/* and POST /login/register/* per client-IP and
	// per-username. Other auth routes (info/me/logout) pass through.
	rlSettings := (*service.AuthRateLimitSettings)(nil)
	if authSettings != nil {
		rlSettings = authSettings.RateLimit
	}
	rlSettings = rlSettings.WithDefaults()
	trustedProxies := authx.ParseCIDRs(rlSettings.TrustedProxyCIDRs)
	mAuth.Use(authx.LoginGuard(rlSettings, trustedProxies))

	// Mount /login/* and /logout on the unprotected group.
	mgr.Mount(mAuth)

	// Protected group: require auth + resolve capabilities.
	m.Use(mgr.Require(), mgr.CapMiddleware())

	// publicServerStarter is a callback that creates and starts the public HTTP server.
	// It is passed into the api layer so it can dynamically start/stop the public server
	// when settings change via the UI.
	publicServerStarter := func(settings *service.Settings, rh2 *api.RawHandler) (context.CancelFunc, error) {
		return startPublicServer(ctx, cfg, svc, settings, rh2)
	}

	if err := api.Handle(m, mData, mAuth, svc, info, encStore, mgr, rh, publicServerStarter); err != nil {
		return err
	}

	// Read serve settings from DB
	var settings *service.Settings
	settings, err = svc.Settings(ctx)
	if err != nil {
		return fmt.Errorf("reading settings for server startup: %w", err)
	}

	// Start FTP server if enabled
	if settings.FTPServe != nil && settings.FTPServe.Enabled {
		shares := api.BuildFTPShares(ctx, svc, rh)
		users := api.BuildFTPUsers(ctx, svc)
		ftpSrv, err := ftpserve.NewServer(settings.FTPServe, shares, users)
		if err != nil {
			return fmt.Errorf("init FTP server: %w", err)
		}
		ftpCtx, ftpCancel := context.WithCancel(ctx)
		ftpSrv.Start(ftpCtx)
		api.SetFTPServer(rh, ftpSrv, ftpCancel)
	}

	// Start SFTP server if enabled
	if settings.SFTPServe != nil && settings.SFTPServe.Enabled {
		shares := api.BuildFTPShares(ctx, svc, rh)
		users := api.BuildFTPUsers(ctx, svc)
		sftpSrv, err := sftpserve.NewServer(settings.SFTPServe, shares, users, func(generatedPEM string) {
			settings.SFTPServe.HostKeyPEM = generatedPEM
			if err := svc.PatchSettings(ctx, &service.PatchSettings{
				Action:    service.ActionKeySet,
				SFTPServe: settings.SFTPServe,
			}); err != nil {
				slog.Error("failed to persist auto-generated SFTP host key", "error", err)
			} else {
				slog.Info("auto-generated SFTP host key persisted to database")
			}
		})
		if err != nil {
			return fmt.Errorf("init SFTP server: %w", err)
		}
		sftpCtx, sftpCancel := context.WithCancel(ctx)
		sftpSrv.Start(sftpCtx)
		api.SetSFTPServer(rh, sftpSrv, sftpCancel)
	}

	// Start TFTP server if enabled
	if settings.TFTPServe != nil && settings.TFTPServe.Enabled {
		shares := api.BuildFTPShares(ctx, svc, rh)
		tftpSrv, err := tftpserve.NewServer(settings.TFTPServe, shares)
		if err != nil {
			return fmt.Errorf("init TFTP server: %w", err)
		}
		tftpCtx, tftpCancel := context.WithCancel(ctx)
		tftpSrv.Start(tftpCtx, settings.TFTPServe)
		api.SetTFTPServer(rh, tftpSrv, tftpCancel)
	}

	// Start WebDAV server if enabled
	if settings.WebDAVServe != nil && settings.WebDAVServe.Enabled {
		shares := api.BuildFTPShares(ctx, svc, rh)
		users := api.BuildFTPUsers(ctx, svc)
		webdavSrv, err := webdavserve.NewServer(settings.WebDAVServe, shares, users)
		if err != nil {
			return fmt.Errorf("init WebDAV server: %w", err)
		}
		webdavCtx, webdavCancel := context.WithCancel(ctx)
		webdavSrv.Start(webdavCtx)
		api.SetWebDAVServer(rh, webdavSrv, webdavCancel)
	}

	if err := folderHandler(mAuth); err != nil {
		return err
	}

	// Start public server if enabled in DB settings
	if settings.PublicPort != nil && settings.PublicPort.Enabled && settings.PublicPort.Port != "" {
		cancel, err := startPublicServer(ctx, cfg, svc, settings, rh)
		if err != nil {
			return fmt.Errorf("init public server: %w", err)
		}
		api.SetPublicServer(rh, cancel)
	}

	return server.StartWithContext(ctx, net.JoinHostPort(cfg.Server.Host, cfg.Server.Port))
}

// startPublicServer creates and starts the public (unauthenticated) HTTP server.
// Returns a cancel function to stop it.
func startPublicServer(ctx context.Context, cfg *config.Config, svc *service.Service, settings *service.Settings, rh *api.RawHandler) (context.CancelFunc, error) {
	pubServer := ada.New()
	pubServer.Use(
		mrecover.Middleware(),
		mserver.Middleware(config.Service),
		mcors.Middleware(),
		mrequestid.Middleware(),
		mlog.Middleware(),
		mtelemetry.Middleware(),
	)

	mPublic := pubServer.Group(cfg.Server.BasePath)
	if err := api.HandlePublic(mPublic, svc, rh); err != nil {
		return nil, err
	}

	compat.Register(pubServer.Mux, svc, settings.Compat)

	publicAddr := net.JoinHostPort(cfg.Server.Host, settings.PublicPort.Port)
	slog.Info("starting public data server", "address", publicAddr)

	pubCtx, pubCancel := context.WithCancel(ctx)

	go func() {
		if err := pubServer.StartWithContext(pubCtx, publicAddr); err != nil {
			slog.Error("public data server failed", "error", err)
			into.CtxCancel()
		}
	}()

	return pubCancel, nil
}
