package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/into"

	mcors "github.com/rakunlabs/ada/middleware/cors"
	mforwardauth "github.com/rakunlabs/ada/middleware/forwardauth"
	mlog "github.com/rakunlabs/ada/middleware/log"
	mrecover "github.com/rakunlabs/ada/middleware/recover"
	mrequestid "github.com/rakunlabs/ada/middleware/requestid"
	mserver "github.com/rakunlabs/ada/middleware/server"
	mtelemetry "github.com/rakunlabs/ada/middleware/telemetry"

	"github.com/rakunlabs/pika/internal/config"
	"github.com/rakunlabs/pika/internal/ftpserve"
	"github.com/rakunlabs/pika/internal/secret"
	"github.com/rakunlabs/pika/internal/server/api"
	"github.com/rakunlabs/pika/internal/server/compat"
	"github.com/rakunlabs/pika/internal/server/session"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/pika/internal/sftpserve"
	"github.com/rakunlabs/pika/internal/tftpserve"
)

func Start(ctx context.Context, cfg *config.Config, svc *service.Service, info api.Info, encStore *secret.Storage) error {
	if cfg.Server.ForwardAuth != nil && cfg.Server.Auth != nil {
		return fmt.Errorf("forward_auth and auth are mutually exclusive; configure only one")
	}

	// Build initial raw mount handler from config + DB settings
	rh := api.BuildInitialRawHandler(ctx, cfg.Server.Raw, svc)

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

	var sessionStore *session.Store

	if cfg.Server.ForwardAuth != nil {
		slog.Info("forward auth enabled", "url", cfg.Server.ForwardAuth.Address)
		m.Use(mforwardauth.Middleware(mforwardauth.WithConfig(*cfg.Server.ForwardAuth)))
	} else if cfg.Server.Auth != nil {
		slog.Info("built-in auth enabled")

		cookieOpts := session.CookieOptions{
			Name:     cfg.Server.Auth.Cookie.Name,
			Domain:   cfg.Server.Auth.Cookie.Domain,
			Path:     cfg.Server.Auth.Cookie.Path,
			Secure:   cfg.Server.Auth.Cookie.Secure,
			SameSite: session.ParseSameSite(cfg.Server.Auth.Cookie.SameSite),
		}

		sessionStore = session.NewStore(cfg.Server.Auth.SessionTTL, cookieOpts)
		m.Use(sessionStore.Middleware)
	} else {
		slog.Info("no auth configured — admin API is unprotected")
	}

	if err := api.Handle(m, mData, mAuth, svc, info, encStore, sessionStore, rh); err != nil {
		return err
	}

	// Start FTP server if enabled
	if cfg.Server.FTPServe.Enabled {
		shares := api.BuildFTPShares(ctx, svc, rh)
		users := api.BuildFTPUsers(ctx, svc)
		ftpSrv, err := ftpserve.NewServer(&cfg.Server.FTPServe, shares, users)
		if err != nil {
			return fmt.Errorf("init FTP server: %w", err)
		}
		ftpSrv.Start(ctx)
		api.SetFTPServer(rh, ftpSrv)
	}

	// Start SFTP server if enabled
	if cfg.Server.SFTPServe.Enabled {
		shares := api.BuildFTPShares(ctx, svc, rh)
		users := api.BuildFTPUsers(ctx, svc)
		sftpSrv, err := sftpserve.NewServer(&cfg.Server.SFTPServe, shares, users)
		if err != nil {
			return fmt.Errorf("init SFTP server: %w", err)
		}
		sftpSrv.Start(ctx)
		api.SetSFTPServer(rh, sftpSrv)
	}

	// Start TFTP server if enabled
	if cfg.Server.TFTPServe.Enabled {
		shares := api.BuildFTPShares(ctx, svc, rh)
		tftpSrv, err := tftpserve.NewServer(&cfg.Server.TFTPServe, shares)
		if err != nil {
			return fmt.Errorf("init TFTP server: %w", err)
		}
		tftpSrv.Start(ctx, &cfg.Server.TFTPServe)
		api.SetTFTPServer(rh, tftpSrv)
	}

	if err := folderHandler(mAuth); err != nil {
		return err
	}

	if cfg.Server.Compat != nil && cfg.Server.PublicPort == "" {
		slog.Warn("compat endpoints configured but public_port is not set; compat endpoints will not be available")
	}

	if cfg.Server.PublicPort != "" {
		publicServer := ada.New()
		publicServer.Use(
			mrecover.Middleware(),
			mserver.Middleware(config.Service),
			mcors.Middleware(),
			mrequestid.Middleware(),
			mlog.Middleware(),
			mtelemetry.Middleware(),
		)

		mPublic := publicServer.Group(cfg.Server.BasePath)
		if err := api.HandlePublic(mPublic, svc, rh); err != nil {
			return err
		}

		compat.Register(publicServer.Mux, svc, cfg.Server.Compat)

		publicAddr := net.JoinHostPort(cfg.Server.Host, cfg.Server.PublicPort)
		slog.Info("starting public data server", "address", publicAddr)

		go func() {
			if err := publicServer.StartWithContext(ctx, publicAddr); err != nil {
				slog.Error("public data server failed", "error", err)
				into.CtxCancel()
			}
		}()
	}

	return server.StartWithContext(ctx, net.JoinHostPort(cfg.Server.Host, cfg.Server.Port))
}
