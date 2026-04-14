package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

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
	"github.com/rakunlabs/pika/internal/serve/ftpserve"
	"github.com/rakunlabs/pika/internal/serve/sftpserve"
	"github.com/rakunlabs/pika/internal/serve/tftpserve"
	"github.com/rakunlabs/pika/internal/serve/webdavserve"
	"github.com/rakunlabs/pika/internal/server/api"
	"github.com/rakunlabs/pika/internal/server/compat"
	"github.com/rakunlabs/pika/internal/server/session"
	"github.com/rakunlabs/pika/internal/service"
)

// buildForwardAuthMiddleware converts a ForwardAuthSettings (stored in DB)
// into an ada forward-auth middleware.
func buildForwardAuthMiddleware(fa *service.ForwardAuthSettings) ada.MiddlewareFunc {
	cfg := mforwardauth.ForwardAuth{
		Address:                  fa.Address,
		AuthResponseHeaders:      fa.AuthResponseHeaders,
		AuthResponseHeadersRegex: fa.AuthResponseHeadersRegex,
		AuthRequestHeaders:       fa.AuthRequestHeaders,
		TrustForwardHeader:       fa.TrustForwardHeader,
		InsecureSkipVerify:       fa.InsecureSkipVerify,
		RedirectURL:              fa.RedirectURL,
		RedirectCode:             fa.RedirectCode,
		RedirectStatusCodes:      fa.RedirectStatusCodes,
		RequestMethod:            fa.RequestMethod,
	}

	if fa.Timeout != "" {
		if d, err := time.ParseDuration(fa.Timeout); err == nil {
			cfg.Timeout = d
		}
	}

	return mforwardauth.Middleware(mforwardauth.WithConfig(cfg))
}

// combinedAuthMiddleware returns a middleware that tries session authentication
// first and falls back to forward-auth (via the Slot) when no valid session
// exists. If neither succeeds the request receives 401 Unauthorized.
//
// Auth endpoints (/api/v1/auth/*) are registered on a separate mux that does
// not pass through this middleware, so the local login page is always reachable.
func combinedAuthMiddleware(store *session.Store, slot *ada.Slot) ada.MiddlewareFunc {
	fwdAuth := slot.Middleware()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 1. Session-first: check cookie
			if cookie, err := r.Cookie(store.CookieName()); err == nil && cookie.Value != "" {
				if sess := store.Get(cookie.Value); sess != nil {
					ctx := service.WithUserInfo(r.Context(), sess.Username, sess.UserID)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			// 2. Forward-auth fallback (if the Slot is enabled).
			// The forward-auth middleware handles response on its own:
			//   - success: copies configured response headers, calls next
			//   - failure: returns the auth-service response (401/redirect)
			if slot.Enabled() {
				fwdAuth(next).ServeHTTP(w, r)
				return
			}

			// 3. Neither succeeded.
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
		})
	}
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

	// --- Authentication setup ---
	// Local auth (session-based) is always enabled.
	// Forward-auth can be layered on top via an ada.Slot that is toggled
	// at runtime from the settings UI.

	slog.Info("built-in auth enabled")

	cookieOpts := session.CookieOptions{
		Name:     cfg.Server.Auth.Cookie.Name,
		Domain:   cfg.Server.Auth.Cookie.Domain,
		Path:     cfg.Server.Auth.Cookie.Path,
		Secure:   cfg.Server.Auth.Cookie.Secure,
		SameSite: session.ParseSameSite(cfg.Server.Auth.Cookie.SameSite),
	}

	sessionStore := session.NewStore(svc.SessionStorage(), cfg.Server.Auth.SessionTTL, cookieOpts)

	// Ensure at least one superadmin exists (handles upgrade from older versions)
	if err := svc.EnsureSuperadmin(ctx); err != nil {
		slog.Warn("failed to ensure superadmin", "error", err)
	}

	// Create the forward-auth Slot — starts as NoOp (disabled).
	// The api layer will populate it from DB settings at startup
	// and hot-swap it when settings change.
	forwardAuthSlot := ada.NewSlot(ada.NoOp())
	forwardAuthSlot.Disable()

	// Load forward-auth settings from DB and populate the Slot if enabled.
	if fa := svc.GetForwardAuthSettings(ctx); fa != nil && fa.Enabled && fa.Address != "" {
		forwardAuthSlot.Replace(buildForwardAuthMiddleware(fa))
		forwardAuthSlot.Enable()
		slog.Info("forward-auth enabled from settings", "address", fa.Address)
	}

	// Register the combined auth middleware on the admin API group.
	m.Use(combinedAuthMiddleware(sessionStore, forwardAuthSlot))

	// publicServerStarter is a callback that creates and starts the public HTTP server.
	// It is passed into the api layer so it can dynamically start/stop the public server
	// when settings change via the UI.
	publicServerStarter := func(settings *service.Settings, rh2 *api.RawHandler) (context.CancelFunc, error) {
		return startPublicServer(ctx, cfg, svc, settings, rh2)
	}

	if err := api.Handle(m, mData, mAuth, svc, info, encStore, sessionStore, forwardAuthSlot, rh, publicServerStarter); err != nil {
		return err
	}

	// Read serve settings from DB
	settings, err := svc.Settings(ctx)
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
