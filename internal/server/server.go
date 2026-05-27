package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/rakunlabs/ada"

	mcors "github.com/rakunlabs/ada/middleware/cors"
	mlog "github.com/rakunlabs/ada/middleware/log"
	mrecover "github.com/rakunlabs/ada/middleware/recover"
	mrequestid "github.com/rakunlabs/ada/middleware/requestid"
	mserver "github.com/rakunlabs/ada/middleware/server"
	mtelemetry "github.com/rakunlabs/ada/middleware/telemetry"

	"github.com/rakunlabs/pika/internal/cluster"
	"github.com/rakunlabs/pika/internal/config"
	"github.com/rakunlabs/pika/internal/secret"
	"github.com/rakunlabs/pika/internal/serve/ftpserve"
	"github.com/rakunlabs/pika/internal/serve/sftpserve"
	"github.com/rakunlabs/pika/internal/serve/tftpserve"
	"github.com/rakunlabs/pika/internal/serve/webdavserve"
	"github.com/rakunlabs/pika/internal/server/api"
	"github.com/rakunlabs/pika/internal/server/authx"
	"github.com/rakunlabs/pika/internal/server/lockgate"
	"github.com/rakunlabs/pika/internal/server/proxy"
	"github.com/rakunlabs/pika/internal/service"
)

// proxyMgrWrapper adapts *proxy.Manager to the api.ProxyReconciler
// interface. The api package keeps the interface intentionally weak-
// typed for Validate/Status (they return any) so it doesn't pull in
// the proxy types directly.
type proxyMgrWrapper struct{ m *proxy.Manager }

func (w proxyMgrWrapper) Reconcile(servers []service.ProxyServer) error {
	return w.m.Reconcile(servers)
}

func (w proxyMgrWrapper) ReconcileAll(listeners []service.ProxyListener, graphs []service.ProxyServer) error {
	return w.m.ReconcileAll(listeners, graphs)
}

func (w proxyMgrWrapper) Validate(s service.ProxyServer) (any, error) {
	return w.m.Validate(s)
}

func (w proxyMgrWrapper) ValidateListener(l service.ProxyListener) error {
	return w.m.ValidateListener(l)
}

func (w proxyMgrWrapper) Status() any { return w.m.Status() }

func (w proxyMgrWrapper) ListenersStatus() any { return w.m.ListenersStatus() }

// cookieName returns the session cookie name, preferring the AuthSettings
// value, then the default "pika_session".
func cookieName(s *service.AuthSettings) string {
	if s != nil && s.Cookie.Name != "" {
		return s.Cookie.Name
	}
	return "pika_session"
}

func Start(ctx context.Context, cfg *config.Config, svc *service.Service, info api.Info, encStore *secret.Storage, cl *cluster.Cluster) error {
	// Build initial raw mount handler from DB settings (includes hook dispatcher)
	rh := api.BuildInitialRawHandler(ctx, svc)

	// Wire the hook dispatcher into the service for config-level events
	if d := api.GetDispatcher(rh); d != nil {
		svc.SetHookDispatcher(d)
	}

	// Personal vault coordinator. Always-on (no AuthSettings dependency),
	// because the feature is per-user opt-in (the SPA hides the route
	// until the user explicitly visits Setup). The service holds an
	// in-memory rate limiter for unlock-check attempts; instantiating
	// it here keeps the wiring single-source.
	svc.SetVaultService(service.NewVaultService(svc))

	server := ada.New()
	server.Use(
		mrecover.Middleware(),
		mserver.Middleware(config.Service),
		mcors.Middleware(),
		mrequestid.Middleware(),
		mlog.Middleware(),
		mtelemetry.Middleware(),
	)

	// Cluster routing wraps everything below: reads stay local on every
	// node, writes are forwarded to the leader. This must be installed
	// BEFORE the groups so it sits inside the top-level chain that they
	// inherit. No-op when cluster is disabled.
	if cl.Enabled() {
		server.Use(cl.Middleware())
	}

	// Lock gate: 503 every non-allowlisted request while the at-rest
	// encryption key is locked. Sits at the top of the chain so the
	// auth + capability middlewares (mounted on the protected group
	// below) never see a request the server can't safely handle.
	// The allowlist explicitly carves out /healthz, /api/v1/info,
	// /api/v1/key/{status,initialize,unlock}, and the auth manager's
	// /login/* + /logout + /api/v1/me/* routes so an admin can
	// always log in and unlock from a fresh start.
	if encStore != nil {
		server.Use(lockgate.Middleware(encStore.KeyManager(), cfg.Server.BasePath))
	}

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
		SessionStore: authx.NewSessionStore(svc, cookieName(authSettings)),
		BasePath:     cfg.Server.BasePath + "/",
		CookieName:   cookieName(authSettings),
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

	// Build the artifact registry manager BEFORE api.Handle so the
	// api struct can attach to it during construction. Protocol
	// factories (registry/goproxy, registry/npm, registry/docker)
	// register themselves into this manager here, before the
	// initial Reload that api.Handle triggers — otherwise
	// Settings.Registry rows of those types would be silently
	// skipped on cold boot.
	registryMgr := api.BootRegistryManager(ctx, svc, rh, rh.Dispatcher())
	if err := registerRegistryFactories(registryMgr); err != nil {
		return fmt.Errorf("register registry factories: %w", err)
	}

	// Proxy manager owns every user-built listener. It is created
	// here so the api layer (which mutates settings) can reach it
	// for the per-save Reconcile call. The main server port is
	// reserved so a misconfigured graph can't knock the admin UI
	// offline by binding the same socket.
	proxyDeps := proxy.ServiceFromService(svc)
	proxyDeps.RawMounts = rh
	proxyDeps.Registries = registryMgr
	proxyMgr := proxy.NewManager(ctx, proxyDeps, cfg.Server.Host, []string{cfg.Server.Port})
	// Deterministic shutdown order: main HTTP server returns first
	// (via StartWithContext below), then the user-facing protocol
	// listeners drain (FTP/SFTP/TFTP/WebDAV via api.StopServeServers),
	// then the user-built proxy graphs (proxyMgr.Stop waits for socket
	// release per B1). Defers execute LIFO so we register them in
	// reverse: proxyMgr first, serve listeners second.
	defer proxyMgr.Stop()
	defer api.StopServeServers(rh)

	if err := api.Handle(m, mData, mAuth, svc, info, encStore, mgr, rh, proxyMgrWrapper{proxyMgr}, registryMgr, cl); err != nil {
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

	// Spin up every enabled proxy server from settings. Reconcile is
	// idempotent and tolerates compile failures (the bad server is
	// recorded in Status() and skipped) so one broken graph doesn't
	// keep the others offline. When the deployment-wide proxy flag
	// is disabled we pass an empty list — the manager stops every
	// running listener but keeps the persisted graphs intact, so
	// flipping the flag back on later restores them verbatim.
	initialServers := settings.ProxyServers
	initialListeners := settings.ProxyListeners
	if !svc.ProxyEnabled(ctx) {
		initialServers = nil
		initialListeners = nil
	}
	if err := proxyMgr.ReconcileAll(initialListeners, initialServers); err != nil {
		slog.Error("proxy initial reconcile reported errors", "error", err)
	}

	// All routes are now registered, so the leader's HTTP handler is
	// known. Wire it into the cluster so forwarded requests can be
	// re-executed locally, then bring alan + bw cluster online.
	if cl.Enabled() {
		cl.SetForwardHandler(server.Mux)
		if err := cl.Start(ctx); err != nil {
			return fmt.Errorf("start cluster: %w", err)
		}
	}

	return server.StartWithContext(ctx, net.JoinHostPort(cfg.Server.Host, cfg.Server.Port))
}

// (The standalone startPublicServer was retired with the Proxy
// builder feature; every user-facing listener now goes through
// proxy.Manager which spawns one ada server per configured graph.)
