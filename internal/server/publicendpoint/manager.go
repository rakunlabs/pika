// Package publicendpoint owns the runtime side of pika's
// operator-defined public HTTP endpoints. Each endpoint binds its own
// TCP listener and mounts a single mode shim: static direct config
// response, direct External resource read, built-in Consul KV read
// shim, or user-authored Go-template response modifier.
//
// The Manager reconciles a desired list of service.PublicEndpoint
// records against the set of listeners currently running. On every
// settings save the API layer hands the new list to Reload, which
// diffs by ID + bind tuple and starts/stops listeners to match. The
// manager owns no persistent state — it is purely the live view of
// what the settings document declared.
package publicendpoint

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/server/servertls"
	"github.com/rakunlabs/pika/internal/service"
)

// Service is the minimal slice of *service.Service the manager needs.
// Declared as an interface so tests can stub it without standing up
// the entire bw + auth stack. Method names mirror the real service to
// keep the call sites readable.
type Service interface {
	GetData(ctx context.Context, key, version, variant string) (*service.DataResult, error)
	ReadExternal(ctx context.Context, resourceName, path string) (*external.Entry, error)
	ReadExternalVersion(ctx context.Context, resourceName, path, version string) (*external.Entry, error)
	ValidateToken(ctx context.Context, rawKey, configPath, operation string) error
}

// EndpointStatus is the JSON view of a single endpoint's runtime
// state for the admin UI. The struct doubles as the live diagnostics
// surface exposed by /api/v1/public-endpoints/status.
type EndpointStatus struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Enabled    bool      `json:"enabled"`
	ListenHost string    `json:"listen_host"`
	ListenPort int       `json:"listen_port"`
	BasePath   string    `json:"base_path"`
	Mode       string    `json:"mode"`
	TLSEnabled bool      `json:"tls_enabled"`
	AllowHTTP  bool      `json:"allow_http"`
	Running    bool      `json:"running"`
	BoundAddr  string    `json:"bound_addr,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
}

// Manager owns the lifecycle of every public endpoint listener.
// All exported methods are safe to call from any goroutine.
type Manager struct {
	svc    Service
	logger *slog.Logger
	tlsMgr *servertls.Manager

	mu      sync.Mutex
	current map[string]*runningEndpoint // ID -> running listener
	last    map[string]EndpointStatus   // ID -> last known status (running OR failed)
	appCtx  context.Context
	closed  bool
}

// runningEndpoint is one live http.Server + listener pair.
type runningEndpoint struct {
	cfg       service.PublicEndpoint
	srv       *http.Server
	ln        net.Listener
	boundAddr string
	startedAt time.Time
}

// New constructs a manager. ctx is the long-lived parent context
// (typically the server's root) — each endpoint goroutine inherits a
// child cancel from it so a top-level Shutdown drops every listener.
func New(ctx context.Context, svc Service, logger *slog.Logger, tlsMgr ...*servertls.Manager) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	var mgr *servertls.Manager
	if len(tlsMgr) > 0 {
		mgr = tlsMgr[0]
	}
	return &Manager{
		svc:     svc,
		logger:  logger,
		tlsMgr:  mgr,
		current: make(map[string]*runningEndpoint),
		last:    make(map[string]EndpointStatus),
		appCtx:  ctx,
	}
}

// Reload applies the desired set of endpoints, starting any that are
// new or whose bind tuple changed, stopping any that disappeared or
// got disabled. Disabled endpoints still appear in Status as
// not-running so the UI can show them.
//
// On bind failures the manager logs the error, records it in the
// status table, and keeps reconciling the rest. The aggregated error
// (or nil) is returned so a UI-driven save can surface a banner.
func (m *Manager) Reload(ctx context.Context, eps []service.PublicEndpoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return errors.New("publicendpoint: manager is shut down")
	}

	desired := make(map[string]service.PublicEndpoint, len(eps))
	for _, ep := range eps {
		if ep.ID == "" {
			// Defensive — the service layer fills IDs in PatchSettings.
			// Skip silently rather than panic.
			continue
		}
		desired[ep.ID] = ep
	}

	// Pass 1 — stop everything that is no longer desired, disabled,
	// or whose bind tuple / mode changed. We deliberately tear down
	// even on body-template edits to keep diffing trivial; a custom-
	// shim restart is cheap.
	for id, run := range m.current {
		want, ok := desired[id]
		if !ok || !want.Enabled || !endpointMatchesRunning(want, run.cfg) {
			m.stopLocked(id, run)
		}
	}

	// Rebuild last-known status: keep entries for endpoints still in
	// the desired list, drop the rest.
	for id := range m.last {
		if _, keep := desired[id]; !keep {
			delete(m.last, id)
		}
	}

	// Pass 2 — start anything new (or just stopped because of an
	// edit). Disabled endpoints record a not-running status row and
	// move on.
	var failures []string
	for _, ep := range stableSort(desired) {
		if !ep.Enabled {
			m.last[ep.ID] = m.statusFromCfg(ep, false, "", "", time.Time{})
			continue
		}
		if _, already := m.current[ep.ID]; already {
			// Bind matches; nothing to do. Refresh the status row
			// for monotonically-updating timestamps.
			run := m.current[ep.ID]
			m.last[ep.ID] = m.statusFromCfg(ep, true, run.boundAddr, "", run.startedAt)
			continue
		}
		if err := m.startLocked(ep); err != nil {
			msg := err.Error()
			m.last[ep.ID] = m.statusFromCfg(ep, false, "", msg, time.Time{})
			m.logger.Error("public endpoint bind failed",
				"id", ep.ID, "name", ep.Name,
				"bind", net.JoinHostPort(ep.ListenHost, strconv.Itoa(ep.ListenPort)),
				"error", err)
			failures = append(failures, fmt.Sprintf("%s: %v", ep.Name, err))
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("%w: %v", service.ErrPublicEndpointBindFailed, failures)
	}
	return nil
}

// Shutdown stops every running listener and prevents any future
// reload. Idempotent.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	for id, run := range m.current {
		m.stopLocked(id, run)
	}
	return nil
}

// Status returns a point-in-time snapshot of every endpoint the
// manager has been told about (running or otherwise). The slice is
// sorted by name for stable UI rendering.
func (m *Manager) Status() []EndpointStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]EndpointStatus, 0, len(m.last))
	for _, s := range m.last {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// FindByID returns the live configuration the manager is currently
// running for the given endpoint ID, or false when nothing is bound.
// Used by the /test endpoint to exercise the actual handler without
// going through the public listener.
func (m *Manager) FindByID(id string) (service.PublicEndpoint, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.current[id]
	if !ok {
		return service.PublicEndpoint{}, false
	}
	return run.cfg, true
}

// HandlerForID returns an http.Handler that serves the endpoint's
// configured shim + auth chain — the same handler bound on the live
// listener. Used by the test endpoint to exercise the pipeline
// without opening a real socket. Returns nil when the endpoint is
// not currently running.
func (m *Manager) HandlerForID(id string) http.Handler {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.current[id]
	if !ok {
		return nil
	}
	return run.srv.Handler
}

// --- internals -----------------------------------------------------

// startLocked binds a fresh listener and spins up the goroutine. The
// caller must hold m.mu.
func (m *Manager) startLocked(ep service.PublicEndpoint) error {
	host := ep.ListenHost
	if host == "" {
		host = "0.0.0.0"
	}
	addr := net.JoinHostPort(host, strconv.Itoa(ep.ListenPort))

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	handler, err := buildHandler(ep, m.svc, m.logger)
	if err != nil {
		// Listener was successfully opened but we cannot build the
		// handler — close the port to avoid a leak.
		_ = ln.Close()
		return fmt.Errorf("build handler: %w", err)
	}

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	serveLn := ln
	if ep.TLS.Enabled {
		if m.tlsMgr == nil {
			_ = ln.Close()
			return fmt.Errorf("tls manager is not configured")
		}
		tlsConfig, err := m.tlsMgr.TLSConfig()
		if err != nil {
			_ = ln.Close()
			return fmt.Errorf("configure tls: %w", err)
		}
		srv.TLSConfig = tlsConfig
		serveLn = servertls.NewOptionalListener(ln, tlsConfig, func() servertls.Policy {
			return servertls.Policy{HTTPS: true, PlainHTTP: ep.TLS.AllowHTTP}
		})
	}

	run := &runningEndpoint{
		cfg:       ep,
		srv:       srv,
		ln:        ln,
		boundAddr: ln.Addr().String(),
		startedAt: time.Now().UTC(),
	}
	m.current[ep.ID] = run
	m.last[ep.ID] = m.statusFromCfg(ep, true, run.boundAddr, "", run.startedAt)

	go func(ep service.PublicEndpoint, run *runningEndpoint) {
		err := run.srv.Serve(serveLn)
		// http.ErrServerClosed is the expected outcome of stopLocked.
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.logger.Error("public endpoint serve exited",
				"id", ep.ID, "name", ep.Name, "error", err)
			m.mu.Lock()
			if _, still := m.current[ep.ID]; still {
				// Surface the death in the status table so the UI
				// can show it.
				prev := m.last[ep.ID]
				prev.Running = false
				prev.LastError = err.Error()
				m.last[ep.ID] = prev
				delete(m.current, ep.ID)
			}
			m.mu.Unlock()
		}
	}(ep, run)

	m.logger.Info("public endpoint started",
		"id", ep.ID, "name", ep.Name, "mode", ep.Mode,
		"bind", run.boundAddr, "base_path", ep.BasePath,
		"tls", ep.TLS.Enabled)
	return nil
}

// stopLocked terminates a running endpoint. Caller holds m.mu.
func (m *Manager) stopLocked(id string, run *runningEndpoint) {
	// Use a short shutdown deadline so a buggy handler can't block
	// the reload loop indefinitely. Background requests get force-
	// closed.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := run.srv.Shutdown(ctx); err != nil {
		// Force-close on shutdown timeout. Listener.Close is idempotent.
		_ = run.srv.Close()
	}
	_ = run.ln.Close()
	delete(m.current, id)

	if prev, ok := m.last[id]; ok {
		prev.Running = false
		prev.BoundAddr = ""
		m.last[id] = prev
	}

	m.logger.Info("public endpoint stopped", "id", id, "name", run.cfg.Name)
}

// statusFromCfg builds an EndpointStatus row from a config + runtime
// snapshot. Centralised so every code path reports the same fields.
func (m *Manager) statusFromCfg(ep service.PublicEndpoint, running bool, boundAddr, lastErr string, startedAt time.Time) EndpointStatus {
	host := ep.ListenHost
	if host == "" {
		host = "0.0.0.0"
	}
	bp := ep.BasePath
	if bp == "" {
		bp = "/"
	}
	return EndpointStatus{
		ID:         ep.ID,
		Name:       ep.Name,
		Enabled:    ep.Enabled,
		ListenHost: host,
		ListenPort: ep.ListenPort,
		BasePath:   bp,
		Mode:       ep.Mode,
		TLSEnabled: ep.TLS.Enabled,
		AllowHTTP:  ep.TLS.AllowHTTP,
		Running:    running,
		BoundAddr:  boundAddr,
		LastError:  lastErr,
		StartedAt:  startedAt,
	}
}

// endpointMatchesRunning reports whether the desired config and the
// currently-running cfg are equivalent for the purposes of "no need
// to restart". Any structural change (host, port, mode, base_path,
// auth, mode-specific body) forces a restart so handlers always see
// the latest config without surgical state copying.
func endpointMatchesRunning(want, have service.PublicEndpoint) bool {
	if want.ListenHost != have.ListenHost ||
		want.ListenPort != have.ListenPort ||
		want.BasePath != have.BasePath ||
		want.Mode != have.Mode ||
		want.TLS.Enabled != have.TLS.Enabled ||
		want.TLS.AllowHTTP != have.TLS.AllowHTTP {
		return false
	}
	if want.Auth.Mode != have.Auth.Mode ||
		want.Auth.HeaderName != have.Auth.HeaderName {
		return false
	}
	if !stringSliceEqual(want.Auth.StaticTokens, have.Auth.StaticTokens) {
		return false
	}
	if !requestCheckEqual(want.RequestCheck, have.RequestCheck) {
		return false
	}
	switch want.Mode {
	case "static":
		// StaticCompat carries no fields today; presence-only.
		return (want.Static == nil) == (have.Static == nil)
	case "consul":
		// ConsulCompat carries no fields today; presence-only.
		return (want.Consul == nil) == (have.Consul == nil)
	case "external":
		if (want.External == nil) != (have.External == nil) {
			return false
		}
		if want.External == nil {
			return true
		}
		return want.External.Resource == have.External.Resource
	case "custom":
		if (want.Custom == nil) != (have.Custom == nil) {
			return false
		}
		if want.Custom == nil {
			return true
		}
		return want.Custom.BodyTemplate == have.Custom.BodyTemplate &&
			want.Custom.ContentType == have.Custom.ContentType &&
			want.Custom.StatusOnMissing == have.Custom.StatusOnMissing &&
			want.Custom.AllowFormatOverride == have.Custom.AllowFormatOverride
	}
	return true
}

func requestCheckEqual(a, b *service.RequestCheck) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	if len(a.Rules) != len(b.Rules) {
		return false
	}
	for i := range a.Rules {
		if !ruleEqual(a.Rules[i], b.Rules[i]) {
			return false
		}
	}
	return true
}

func ruleEqual(a, b service.RequestRule) bool {
	if a.Name != b.Name || a.Enabled != b.Enabled {
		return false
	}
	if !matchEqual(a.When, b.When) {
		return false
	}
	if !actionEqual(a.Then, b.Then) {
		return false
	}
	return actionSliceEqual(a.Actions, b.Actions)
}

func actionSliceEqual(a, b []service.RequestAction) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !actionEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func actionEqual(a, b service.RequestAction) bool {
	if a.Type != b.Type ||
		a.Status != b.Status ||
		a.Body != b.Body ||
		a.ContentType != b.ContentType ||
		a.Name != b.Name ||
		a.Pattern != b.Pattern ||
		a.Value != b.Value {
		return false
	}
	return captureTransformSliceEqual(a.CaptureTransforms, b.CaptureTransforms)
}

func captureTransformSliceEqual(a, b []service.CaptureTransform) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func matchEqual(a, b service.RequestMatch) bool {
	if a.Method != b.Method ||
		a.PathEquals != b.PathEquals ||
		a.PathPrefix != b.PathPrefix ||
		a.HeaderPresent != b.HeaderPresent ||
		a.HeaderAbsent != b.HeaderAbsent ||
		a.QueryPresent != b.QueryPresent ||
		a.QueryAbsent != b.QueryAbsent {
		return false
	}
	if !headerMatchEqual(a.HeaderEquals, b.HeaderEquals) {
		return false
	}
	if !queryMatchEqual(a.QueryEquals, b.QueryEquals) {
		return false
	}
	return true
}

func headerMatchEqual(a, b *service.HeaderMatch) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return a.Name == b.Name && a.Value == b.Value
}

func queryMatchEqual(a, b *service.QueryMatch) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return a.Name == b.Name && a.Value == b.Value
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func stableSort(m map[string]service.PublicEndpoint) []service.PublicEndpoint {
	out := make([]service.PublicEndpoint, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
