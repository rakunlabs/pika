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
	"github.com/rakunlabs/pika/internal/secret"
	"github.com/rakunlabs/pika/internal/server/api"
	"github.com/rakunlabs/pika/internal/server/session"
	"github.com/rakunlabs/pika/internal/service"
)

func Start(ctx context.Context, cfg *config.Config, svc *service.Service, info api.Info, encStore *secret.Storage) error {
	// Validate mutually exclusive auth options
	if cfg.Server.ForwardAuth != nil && cfg.Server.Auth != nil {
		return fmt.Errorf("forward_auth and auth are mutually exclusive; configure only one")
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
	mAuth := server.Group(cfg.Server.BasePath) // unprotected group for login endpoint

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

	if err := api.Handle(m, mData, mAuth, svc, info, cfg.Secret.AdminSecret, encStore, sessionStore); err != nil {
		return err
	}

	if err := folderHandler(m); err != nil {
		return err
	}

	// Start public data server on a separate port if configured
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
		if err := api.HandlePublic(mPublic, svc); err != nil {
			return err
		}

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
