package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/ada"
	mrecover "github.com/rakunlabs/ada/middleware/recover"
	mserver "github.com/rakunlabs/ada/middleware/server"

	"github.com/rakunlabs/pika/internal/service"
)

// Manager owns the set of live proxy listeners and the graphs
// attached to each. Listeners ARE the actual socket bind (one ada
// HTTP server or one net.TCPListener per entry); graphs are
// compiled pipelines mounted on a listener. The split lets several
// HTTP graphs share one port, routed by HTTP Host header.
//
// A single Manager is created at boot and re-used; every settings
// save calls Reconcile* methods on it. The Manager applies a
// minimum diff against the running set (listeners first, then
// graphs) so a cosmetic edit on one graph never restarts an
// unrelated listener.
type Manager struct {
	parent context.Context
	svc    ServiceDeps
	deps   CompileDeps

	// baseHost is the host inherited from cfg.Server.Host. Any
	// ProxyListener with an empty Host falls back to this so a typical
	// deployment doesn't need to write the host string twice.
	baseHost string

	// reservedPorts are listen ports that other parts of pika own
	// (currently the main HTTP listener). Validate rejects any
	// ProxyListener that tries to bind one of these so saving a
	// listener can never knock the admin UI offline.
	reservedPorts map[string]struct{}

	mu        sync.Mutex
	listeners map[string]*runningListener // key = ProxyListener.ID
	graphs    map[string]*runningGraph    // key = ProxyServer.ID
}

// runningListener owns one accepted socket plus the host-router that
// dispatches incoming HTTP requests to one of the attached graphs.
// TCP listeners use the same struct but accept at most one graph;
// dispatcher is nil and tcpRoot carries the compiled pipeline.
type runningListener struct {
	id       string
	addr     string
	protocol string
	cancel   context.CancelFunc
	done     chan struct{}
	started  time.Time
	lastErr  string

	// hash is a stable fingerprint of the listener config (addr +
	// protocol). When this matches on Reconcile we know we don't
	// have to bounce the socket.
	hash string

	// dispatcher routes one request to the matching graph based on
	// Host header. Set for HTTP listeners; nil for TCP.
	dispatcher *hostDispatcher

	// tcpRoot is the single attached graph's compiled TCP handler.
	// Set for TCP listeners; nil for HTTP.
	tcpRoot     TCPHandler
	tcpGraphID  string
	tcpGraphSum string
}

// runningGraph carries the compiled pipeline for one ProxyServer
// graph and a pointer back to the listener it's mounted on. Stored
// separately from the listener so a graph edit that doesn't change
// its (host_match, listener_id) tuple can swap the handler in-place
// without bouncing the socket.
type runningGraph struct {
	id         string
	listenerID string
	hash       string
	lastErr    string
	// started tracks the moment this graph's handler was last
	// (re)mounted on a listener. Used by the UI status panel to
	// surface a "restarted at" timestamp; the listener may keep
	// running unchanged while a graph swaps in its place.
	started time.Time
}

// NewManager builds an empty Manager. parent is the application
// lifetime context — when it is cancelled all child listeners shut
// down through ada's context.AfterFunc hook.
func NewManager(parent context.Context, svc ServiceDeps, baseHost string, reservedPorts []string) *Manager {
	reserved := make(map[string]struct{}, len(reservedPorts))
	for _, p := range reservedPorts {
		if p != "" {
			reserved[p] = struct{}{}
		}
	}
	return &Manager{
		parent:        parent,
		svc:           svc,
		deps:          Default(svc),
		baseHost:      baseHost,
		reservedPorts: reserved,
		listeners:     map[string]*runningListener{},
		graphs:        map[string]*runningGraph{},
	}
}

// InstanceStatus is the per-graph snapshot used by the UI live-
// status panel. Kept small on purpose; debugging details land in
// server logs rather than the API. The Addr field is the resolved
// listener address so the panel can display "graph foo is running
// on 0.0.0.0:8081" without a second lookup.
type InstanceStatus struct {
	ID         string    `json:"id"`
	ListenerID string    `json:"listener_id,omitempty"`
	Running    bool      `json:"running"`
	Addr       string    `json:"addr,omitempty"`
	Hash       string    `json:"hash,omitempty"`
	Started    time.Time `json:"started,omitempty"`
	LastErr    string    `json:"last_err,omitempty"`
}

// ListenerStatus mirrors InstanceStatus but for the listener side —
// the actual socket binding. The UI's Listeners tab keys off this
// to show whether each configured listener is bound or held up by
// a port conflict / permission error.
type ListenerStatus struct {
	ID       string    `json:"id"`
	Running  bool      `json:"running"`
	Addr     string    `json:"addr,omitempty"`
	Protocol string    `json:"protocol,omitempty"`
	Started  time.Time `json:"started,omitempty"`
	LastErr  string    `json:"last_err,omitempty"`
	// GraphIDs are the IDs of the graphs currently attached to this
	// listener (HTTP can hold many, TCP holds at most one).
	GraphIDs []string `json:"graph_ids,omitempty"`
}

// Status returns a snapshot of every known graph instance,
// including ones that failed to compile or attach. Stable order
// by ID so the UI list doesn't jump around between polls.
func (m *Manager) Status() []InstanceStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]InstanceStatus, 0, len(m.graphs))
	for _, g := range m.graphs {
		var addr string
		var running bool
		if g.listenerID != "" {
			if l := m.listeners[g.listenerID]; l != nil {
				addr = l.addr
				running = l.cancel != nil && g.lastErr == ""
			}
		}
		out = append(out, InstanceStatus{
			ID:         g.id,
			ListenerID: g.listenerID,
			Running:    running,
			Addr:       addr,
			Hash:       g.hash,
			Started:    g.started,
			LastErr:    g.lastErr,
		})
	}
	sortByKey(out, func(s InstanceStatus) string { return s.ID })
	return out
}

// ListenersStatus returns a snapshot of every known listener.
func (m *Manager) ListenersStatus() []ListenerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ListenerStatus, 0, len(m.listeners))
	for _, l := range m.listeners {
		var graphIDs []string
		for _, g := range m.graphs {
			if g.listenerID == l.id {
				graphIDs = append(graphIDs, g.id)
			}
		}
		sortStrings(graphIDs)
		out = append(out, ListenerStatus{
			ID:       l.id,
			Running:  l.cancel != nil,
			Addr:     l.addr,
			Protocol: l.protocol,
			Started:  l.started,
			LastErr:  l.lastErr,
			GraphIDs: graphIDs,
		})
	}
	sortByKey(out, func(s ListenerStatus) string { return s.ID })
	return out
}

// Reconcile is the legacy entry point: it takes ONLY graphs and
// auto-synthesizes a listener per (host, port) tuple for graphs
// that have no ListenerID set. Kept so external call sites (tests
// + the api package that still loads the legacy "ProxyServers"
// list) keep working unchanged after the listener split.
//
// Prefer ReconcileAll for new code that has the persisted
// ProxyListeners slice available.
func (m *Manager) Reconcile(servers []service.ProxyServer) error {
	listeners := synthesizeLegacyListeners(servers, m.baseHost)
	return m.ReconcileAll(listeners, servers)
}

// ReconcileAll diffs both lists against the running set and applies
// the minimum changes: listeners are reconciled first (so a graph
// attaching to a brand-new listener finds a bound socket), then
// graphs are compiled and mounted.
//
// Returns the first error encountered; the rest of both lists is
// still reconciled so one broken graph doesn't take the others
// down. Per-row errors land in Status() / ListenersStatus().
func (m *Manager) ReconcileAll(listeners []service.ProxyListener, graphs []service.ProxyServer) error {
	// Merge legacy listeners (from graphs that still carry Host/Port
	// directly) onto the operator-managed list. New rows always have
	// ListenerID set, so this is a no-op once every graph has been
	// re-saved through the new UI.
	listeners = mergeLegacy(listeners, graphs, m.baseHost)

	wantL := make(map[string]service.ProxyListener, len(listeners))
	for _, l := range listeners {
		wantL[l.ID] = l
	}
	wantG := make(map[string]service.ProxyServer, len(graphs))
	for _, g := range graphs {
		wantG[g.ID] = g
	}

	var firstErr error

	// 1. Tear down listeners that disappeared or are disabled.
	m.mu.Lock()
	for id, l := range m.listeners {
		w, ok := wantL[id]
		if !ok || !w.Enabled {
			m.stopListenerLocked(l)
			delete(m.listeners, id)
		}
	}
	// 2. Drop graph state for graphs that disappeared.
	for id := range m.graphs {
		if _, ok := wantG[id]; !ok {
			delete(m.graphs, id)
		}
	}
	m.mu.Unlock()

	// 3. Bring listeners up / restart on hash change.
	for _, l := range listeners {
		if !l.Enabled {
			continue
		}
		if err := m.applyListener(l); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// 4. Compile every graph and group by listener.
	byListener := map[string][]compiledLike{}
	for _, g := range graphs {
		if !g.Enabled {
			m.recordGraphError(g.ID, g.ListenerID, "")
			continue
		}
		pipe, err := Compile(g, m.deps)
		if err != nil {
			m.recordGraphError(g.ID, g.ListenerID, err.Error())
			if firstErr == nil {
				firstErr = fmt.Errorf("compile %s: %w", g.ID, err)
			}
			continue
		}
		listenerID := resolveGraphListenerID(g, m.baseHost)
		if _, ok := wantL[listenerID]; !ok {
			msg := "listener " + listenerID + " not found"
			m.recordGraphError(g.ID, listenerID, msg)
			if firstErr == nil {
				firstErr = errors.New(msg)
			}
			continue
		}
		l := wantL[listenerID]
		if !l.Enabled {
			m.recordGraphError(g.ID, listenerID, "listener disabled")
			continue
		}
		if got := protocolOrDefault(g.Protocol); protocolOrDefault(l.Protocol) != got {
			msg := fmt.Sprintf("graph protocol %s does not match listener protocol %s", got, protocolOrDefault(l.Protocol))
			m.recordGraphError(g.ID, listenerID, msg)
			if firstErr == nil {
				firstErr = errors.New(msg)
			}
			continue
		}
		byListener[listenerID] = append(byListener[listenerID], compiledLike{srv: g, pipe: pipe})
	}

	// 5. Mount per-listener handler set.
	for listenerID, items := range byListener {
		if err := m.mountGraphs(listenerID, items); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// 6. For listeners with zero attached graphs, install a clear
	// 404/close handler so an operator who unlinks every graph from
	// a listener still gets a meaningful response instead of stale
	// routing tables.
	m.installEmptyHandlers(byListener)

	return firstErr
}

// applyListener brings one listener up or restarts it on hash change.
func (m *Manager) applyListener(l service.ProxyListener) error {
	host := l.Host
	if host == "" {
		host = m.baseHost
	}
	port := strings.TrimSpace(l.Port)
	if port == "" {
		err := fmt.Errorf("listener %s: port required", l.ID)
		m.recordListenerError(l.ID, err)
		return err
	}
	if err := m.validatePort(port); err != nil {
		m.recordListenerError(l.ID, err)
		return err
	}
	addr := net.JoinHostPort(host, port)
	protocol := protocolOrDefault(l.Protocol)
	hash := addr + "|" + protocol

	m.mu.Lock()
	existing := m.listeners[l.ID]
	if existing != nil && existing.cancel != nil && existing.hash == hash {
		// Same socket; nothing to do at the listener layer.
		existing.lastErr = ""
		m.mu.Unlock()
		return nil
	}
	var prevDone chan struct{}
	if existing != nil {
		m.stopListenerLocked(existing)
		prevDone = existing.done
	}
	m.mu.Unlock()

	if prevDone != nil {
		select {
		case <-prevDone:
		case <-time.After(12 * time.Second):
		}
	}

	switch protocol {
	case ProtocolTCP:
		return m.startTCPListener(l.ID, addr, hash)
	default:
		return m.startHTTPListener(l.ID, addr, hash)
	}
}

// startHTTPListener creates a new ada server bound to addr and
// installs a host-router that the mounted graphs feed into.
func (m *Manager) startHTTPListener(id, addr, hash string) error {
	dispatcher := newHostDispatcher(id)

	s := ada.New()
	s.Use(
		mrecover.Middleware(),
		mserver.Middleware("pika-proxy/"+id),
	)
	s.Mux.HandleWildcard("/", dispatcher)

	pctx, pcancel := context.WithCancel(m.parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		slog.Info("proxy listener starting", "id", id, "addr", addr)
		if err := s.StartWithContext(pctx, addr); err != nil {
			slog.Error("proxy listener failed", "id", id, "addr", addr, "error", err)
			m.recordListenerError(id, err)
		}
	}()

	m.mu.Lock()
	m.listeners[id] = &runningListener{
		id:         id,
		addr:       addr,
		protocol:   ProtocolHTTP,
		cancel:     pcancel,
		done:       done,
		started:    time.Now(),
		hash:       hash,
		dispatcher: dispatcher,
	}
	m.mu.Unlock()
	return nil
}

// startTCPListener opens a raw TCP socket bound to addr. The graph
// pipeline gets attached on the subsequent mountGraphs call.
func (m *Manager) startTCPListener(id, addr, hash string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		m.recordListenerError(id, err)
		return err
	}
	tcpLn, ok := ln.(*net.TCPListener)
	if !ok {
		_ = ln.Close()
		err := fmt.Errorf("listener %s: not a TCP listener", id)
		m.recordListenerError(id, err)
		return err
	}

	rl := &runningListener{
		id:       id,
		addr:     addr,
		protocol: ProtocolTCP,
		started:  time.Now(),
		hash:     hash,
	}

	pctx, pcancel := context.WithCancel(m.parent)
	done := make(chan struct{})
	rl.cancel = pcancel
	rl.done = done

	go func() {
		defer close(done)
		defer ln.Close()
		slog.Info("tcp proxy listener starting", "id", id, "addr", addr)
		go func() {
			<-pctx.Done()
			_ = ln.Close()
		}()

		var wg sync.WaitGroup
		defer wg.Wait()
		for {
			conn, err := tcpLn.AcceptTCP()
			if err != nil {
				if pctx.Err() != nil || errorsIsClosed(err) {
					return
				}
				slog.Warn("tcp proxy accept failed", "id", id, "addr", addr, "error", err)
				continue
			}
			wg.Add(1)
			go func(conn *net.TCPConn) {
				defer wg.Done()
				defer conn.Close()
				// Pick the currently mounted TCP root under lock
				// so a reconcile mid-flight observes the new graph
				// for the next accepted conn without blocking
				// existing ones.
				m.mu.Lock()
				lcur := m.listeners[id]
				m.mu.Unlock()
				if lcur == nil || lcur.tcpRoot == nil {
					return
				}
				connCtx, cancel := context.WithCancel(pctx)
				defer cancel()
				go func() {
					<-connCtx.Done()
					_ = conn.Close()
				}()
				if err := lcur.tcpRoot(connCtx, conn); err != nil && pctx.Err() == nil {
					slog.Warn("tcp proxy connection failed", "id", id, "remote", conn.RemoteAddr(), "error", err)
				}
			}(conn)
		}
	}()

	m.mu.Lock()
	m.listeners[id] = rl
	m.mu.Unlock()
	return nil
}

type mountedGraph struct {
	srv  service.ProxyServer
	pipe CompiledPipeline
}

// mountGraphs installs the per-listener handler set for the supplied
// graphs. For HTTP this means rebuilding the listener's host-router
// table; for TCP it picks the single graph and stores its root.
func (m *Manager) mountGraphs(listenerID string, items []compiledLike) error {
	m.mu.Lock()
	l := m.listeners[listenerID]
	if l == nil || l.cancel == nil {
		m.mu.Unlock()
		err := fmt.Errorf("listener %s not running", listenerID)
		for _, it := range items {
			m.recordGraphErrorLocked(it.srv.ID, listenerID, err.Error())
		}
		return err
	}
	m.mu.Unlock()

	switch l.protocol {
	case ProtocolTCP:
		if len(items) > 1 {
			err := fmt.Errorf("tcp listener %s: only one graph may attach", listenerID)
			for _, it := range items {
				m.recordGraphError(it.srv.ID, listenerID, err.Error())
			}
			return err
		}
		m.mu.Lock()
		l = m.listeners[listenerID]
		if len(items) == 0 {
			l.tcpRoot = nil
			l.tcpGraphID = ""
			l.tcpGraphSum = ""
		} else {
			it := items[0]
			l.tcpRoot = it.pipe.TCPRoot
			l.tcpGraphID = it.srv.ID
			l.tcpGraphSum = it.pipe.Hash
		}
		m.mu.Unlock()
		for _, it := range items {
			m.upsertGraph(it.srv.ID, listenerID, it.pipe.Hash, "")
		}
		return nil
	}

	// HTTP — rebuild the host router.
	routes := make([]hostRoute, 0, len(items))
	var defaultRoute *hostRoute
	var seenCatchAll string
	var firstErr error
	for _, it := range items {
		patterns := normalizePatterns(it.srv.HostMatch)
		if len(patterns) == 0 {
			if seenCatchAll != "" {
				err := fmt.Errorf("listener %s: two graphs claim the catch-all (%s, %s)", listenerID, seenCatchAll, it.srv.ID)
				m.recordGraphError(it.srv.ID, listenerID, err.Error())
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			seenCatchAll = it.srv.ID
			r := hostRoute{graphID: it.srv.ID, root: it.pipe.Root}
			defaultRoute = &r
			m.upsertGraph(it.srv.ID, listenerID, it.pipe.Hash, "")
			continue
		}
		for _, p := range patterns {
			if p == "*" {
				if seenCatchAll != "" {
					err := fmt.Errorf("listener %s: two graphs claim the catch-all (%s, %s)", listenerID, seenCatchAll, it.srv.ID)
					m.recordGraphError(it.srv.ID, listenerID, err.Error())
					if firstErr == nil {
						firstErr = err
					}
					continue
				}
				seenCatchAll = it.srv.ID
				r := hostRoute{graphID: it.srv.ID, root: it.pipe.Root}
				defaultRoute = &r
				continue
			}
			routes = append(routes, hostRoute{
				graphID: it.srv.ID,
				root:    it.pipe.Root,
				pattern: p,
			})
		}
		m.upsertGraph(it.srv.ID, listenerID, it.pipe.Hash, "")
	}

	l.dispatcher.set(routes, defaultRoute)
	return firstErr
}

// compiledLike is a tiny alias so mountGraphs accepts both the
// internal compiled struct and external test fixtures without
// needing a second public type.
type compiledLike = mountedGraph

// installEmptyHandlers clears HTTP host routing tables on listeners
// that ended up with zero attached graphs after a reconcile.
func (m *Manager) installEmptyHandlers(byListener map[string][]compiledLike) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, l := range m.listeners {
		if l == nil || l.cancel == nil {
			continue
		}
		if _, hasGraphs := byListener[id]; hasGraphs {
			continue
		}
		switch l.protocol {
		case ProtocolTCP:
			l.tcpRoot = nil
			l.tcpGraphID = ""
			l.tcpGraphSum = ""
		default:
			if l.dispatcher != nil {
				l.dispatcher.set(nil, nil)
			}
		}
	}
}

// upsertGraph records the per-graph runtime row. The started field
// only advances when the (listenerID, hash) tuple actually changed
// so a no-op reconcile doesn't shake the UI timestamp.
func (m *Manager) upsertGraph(id, listenerID, hash, lastErr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g := m.graphs[id]
	if g == nil {
		g = &runningGraph{id: id}
		m.graphs[id] = g
	}
	changed := g.listenerID != listenerID || g.hash != hash
	g.listenerID = listenerID
	g.hash = hash
	g.lastErr = lastErr
	if changed || g.started.IsZero() {
		g.started = time.Now()
	}
}

func (m *Manager) recordGraphError(id, listenerID, msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recordGraphErrorLocked(id, listenerID, msg)
}

func (m *Manager) recordGraphErrorLocked(id, listenerID, msg string) {
	g := m.graphs[id]
	if g == nil {
		g = &runningGraph{id: id}
		m.graphs[id] = g
	}
	if listenerID != "" {
		g.listenerID = listenerID
	}
	g.lastErr = msg
}

func (m *Manager) recordListenerError(id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.listeners[id]
	if l == nil {
		l = &runningListener{id: id}
		m.listeners[id] = l
	}
	l.lastErr = err.Error()
}

func errorsIsClosed(err error) bool {
	return errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF)
}

// stopListenerLocked tears down a running listener. Caller holds m.mu.
func (m *Manager) stopListenerLocked(l *runningListener) {
	if l == nil || l.cancel == nil {
		return
	}
	l.cancel()
	l.cancel = nil
}

// Stop cancels every running listener and waits for each goroutine
// to release its socket before returning. Without the wait a restart
// immediately after Stop could race the OS socket release and fail
// with "address already in use" on the same port.
func (m *Manager) Stop() {
	m.mu.Lock()
	dones := make([]chan struct{}, 0, len(m.listeners))
	ids := make([]string, 0, len(m.listeners))
	for id, l := range m.listeners {
		if l == nil {
			continue
		}
		if l.cancel != nil {
			l.cancel()
			l.cancel = nil
		}
		if l.done != nil {
			dones = append(dones, l.done)
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()

	if len(dones) == 0 {
		return
	}

	deadline := time.NewTimer(12 * time.Second)
	defer deadline.Stop()
	for i, d := range dones {
		select {
		case <-d:
		case <-deadline.C:
			slog.Warn("proxy: listener did not stop within timeout; abandoning wait",
				"pending", len(dones)-i, "first_pending_id", ids[i])
			return
		}
	}
}

// validatePort rejects ports owned by the rest of pika.
func (m *Manager) validatePort(port string) error {
	if _, blocked := m.reservedPorts[port]; blocked {
		return fmt.Errorf("proxy: port %s is reserved by another pika listener", port)
	}
	return nil
}

// Validate compiles a single graph without starting it. Used by the
// /api/v1/proxy/{id}/validate endpoint so the UI can pre-flight a
// graph before persisting it. Legacy behaviour: if the graph still
// carries Host/Port directly we honour those for the port-reservation
// check; otherwise the check is skipped here and surfaced on the
// listener instead.
func (m *Manager) Validate(s service.ProxyServer) (CompiledPipeline, error) {
	pipe, err := Compile(s, m.deps)
	if err != nil {
		return CompiledPipeline{}, err
	}
	if s.Port != "" {
		if err := m.validatePort(s.Port); err != nil {
			return CompiledPipeline{}, err
		}
	}
	return pipe, nil
}

// ValidateListener checks a ProxyListener against the reserved-port
// allowlist without binding the socket. Used by the API layer to
// pre-flight a save.
func (m *Manager) ValidateListener(l service.ProxyListener) error {
	port := strings.TrimSpace(l.Port)
	if port == "" {
		return fmt.Errorf("listener %s: port required", l.ID)
	}
	if err := m.validatePort(port); err != nil {
		return err
	}
	switch protocolOrDefault(l.Protocol) {
	case ProtocolHTTP, ProtocolTCP:
	default:
		return fmt.Errorf("listener %s: unsupported protocol %q", l.ID, l.Protocol)
	}
	return nil
}

// resolveGraphListenerID returns the ID of the listener a graph
// should attach to. New rows carry ListenerID directly. Legacy rows
// (Host/Port only) map onto the synthesized listener whose ID is
// "legacy-<port>" (matching synthesizeLegacyListeners).
func resolveGraphListenerID(g service.ProxyServer, baseHost string) string {
	if id := strings.TrimSpace(g.ListenerID); id != "" {
		return id
	}
	return legacyListenerIDFor(g, baseHost)
}

func legacyListenerIDFor(g service.ProxyServer, baseHost string) string {
	port := strings.TrimSpace(g.Port)
	host := strings.TrimSpace(g.Host)
	if host == "" {
		host = baseHost
	}
	return "legacy-" + protocolOrDefault(g.Protocol) + "-" + host + "-" + port
}

// synthesizeLegacyListeners builds a ProxyListener slice for any
// graph that lacks a ListenerID. Used by the legacy Reconcile entry
// point so tests and the existing api callers see no behaviour
// change after the split.
func synthesizeLegacyListeners(graphs []service.ProxyServer, baseHost string) []service.ProxyListener {
	seen := map[string]bool{}
	out := []service.ProxyListener{}
	for _, g := range graphs {
		if strings.TrimSpace(g.ListenerID) != "" {
			continue
		}
		id := legacyListenerIDFor(g, baseHost)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, service.ProxyListener{
			ID:       id,
			Name:     "Legacy " + g.Port,
			Enabled:  true,
			Protocol: protocolOrDefault(g.Protocol),
			Host:     g.Host,
			Port:     g.Port,
		})
	}
	return out
}

// mergeLegacy appends synthesized legacy listeners to the operator-
// managed slice so a partial migration (some graphs already moved to
// new listeners, some still on Host/Port) works.
func mergeLegacy(listeners []service.ProxyListener, graphs []service.ProxyServer, baseHost string) []service.ProxyListener {
	have := map[string]bool{}
	for _, l := range listeners {
		have[l.ID] = true
	}
	for _, l := range synthesizeLegacyListeners(graphs, baseHost) {
		if !have[l.ID] {
			listeners = append(listeners, l)
			have[l.ID] = true
		}
	}
	return listeners
}

func protocolOrDefault(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "" {
		return ProtocolHTTP
	}
	return p
}

func normalizePatterns(in []string) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// hostRoute is one entry in a host-router table.
type hostRoute struct {
	graphID string
	pattern string // empty when this is the default entry
	root    http.Handler
}

// hostDispatcher routes incoming HTTP requests to the matching
// graph root by Host header. Reads happen on the hot path so the
// table swap is published behind an atomic-ish pointer pattern
// (mu-guarded swap, request handlers take a quick local copy).
type hostDispatcher struct {
	listenerID string

	mu       sync.RWMutex
	routes   []hostRoute
	fallback *hostRoute
}

func newHostDispatcher(listenerID string) *hostDispatcher {
	return &hostDispatcher{listenerID: listenerID}
}

func (d *hostDispatcher) set(routes []hostRoute, fallback *hostRoute) {
	// Two passes so suffix matches (*.example.com) come AFTER exact
	// matches for the same domain — the dispatcher checks exact
	// first by iterating in this stable order.
	exact := []hostRoute{}
	suffix := []hostRoute{}
	for _, r := range routes {
		if strings.HasPrefix(r.pattern, "*.") {
			suffix = append(suffix, r)
		} else {
			exact = append(exact, r)
		}
	}
	merged := append(exact, suffix...)
	d.mu.Lock()
	d.routes = merged
	d.fallback = fallback
	d.mu.Unlock()
}

func (d *hostDispatcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.RLock()
	routes := d.routes
	fallback := d.fallback
	d.mu.RUnlock()

	host := strings.ToLower(r.Host)
	// Strip port if present so "example.com:8080" matches
	// "example.com".
	if i := strings.LastIndex(host, ":"); i >= 0 {
		// IPv6 raw literals would confuse this; the proxy is
		// for user-facing names so this is acceptable.
		if !strings.Contains(host[:i], "]") {
			host = host[:i]
		}
	}

	for _, route := range routes {
		if matchHost(route.pattern, host) {
			route.root.ServeHTTP(w, r)
			return
		}
	}
	if fallback != nil {
		fallback.root.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

// matchHost evaluates a host pattern. Supports:
//   - exact match
//   - "*.example.com" suffix glob (matches "a.example.com" and
//     "a.b.example.com" but NOT "example.com")
func matchHost(pattern, host string) bool {
	pattern = strings.ToLower(pattern)
	if pattern == host {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		return len(host) > len(suffix) && strings.HasSuffix(host, suffix)
	}
	return false
}
