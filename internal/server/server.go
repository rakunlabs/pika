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
	"github.com/rakunlabs/pika/internal/server/api"
	"github.com/rakunlabs/pika/internal/server/authx"
	"github.com/rakunlabs/pika/internal/server/lockgate"
	"github.com/rakunlabs/pika/internal/service"
)

// cookieName returns the session cookie name, preferring the AuthSettings
// value, then the default "pika_session".
func cookieName(s *service.AuthSettings) string {
	if s != nil && s.Cookie.Name != "" {
		return s.Cookie.Name
	}
	return "pika_session"
}

func Start(ctx context.Context, cfg *config.Config, svc *service.Service, info api.Info, encStore *secret.Storage, cl *cluster.Cluster) error {
	// Build the hook dispatcher. Hooks remain a first-class feature
	// of the configuration server — they emit events on config
	// create/update/delete and on settings changes. The dispatcher
	// is created here so the service layer can hand events into it
	// without dragging an event-bus dependency through the API.
	dispatcher := api.BuildHookDispatcher(ctx, svc)
	svc.SetHookDispatcher(dispatcher)

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

	if err := api.Handle(m, mData, mAuth, svc, info, encStore, mgr, dispatcher, cl); err != nil {
		return err
	}

	if err := folderHandler(mAuth); err != nil {
		return err
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
