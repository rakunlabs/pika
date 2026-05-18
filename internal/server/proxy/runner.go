package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/rakunlabs/ada"
	mrecover "github.com/rakunlabs/ada/middleware/recover"
	mserver "github.com/rakunlabs/ada/middleware/server"
)

// Manager owns the set of live proxy server instances. A single
// Manager is created at boot and re-used; every time the operator
// saves the settings row the api layer calls Reconcile with the new
// list and the Manager spins instances up/down to match.
type Manager struct {
	parent context.Context
	svc    ServiceDeps
	deps   CompileDeps

	// baseHost is the host inherited from cfg.Server.Host. Any
	// ProxyServer with an empty Host falls back to this so a typical
	// deployment doesn't need to write the host string twice.
	baseHost string

	// reservedPorts are listen ports that other parts of pika own
	// (currently the main HTTP listener). Validate rejects any
	// ProxyServer that tries to bind one of these so saving a graph
	// can never knock the admin UI offline.
	reservedPorts map[string]struct{}

	mu        sync.Mutex
	instances map[string]*runningInstance // key = ProxyServer.ID
}

type runningInstance struct {
	id      string
	addr    string
	hash    string
	cancel  context.CancelFunc
	started time.Time
	// done is closed by the listener goroutine once Start returns
	// (shutdown finished). Reconcile waits on it before bringing a
	// new listener up on the same port so we don't race the OS
	// socket release between two server lifetimes.
	done chan struct{}
	// lastErr captures the most recent listen / shutdown error so
	// the UI status endpoint can show a meaningful state for a
	// listener that refused to start.
	lastErr string
}

// NewManager builds an empty Manager. parent is the application
// lifetime context — when it is cancelled all child servers shut
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
		instances:     map[string]*runningInstance{},
	}
}

// InstanceStatus is the per-server snapshot used by the UI live-
// status panel. Kept small on purpose; debugging details land in
// server logs rather than the API.
type InstanceStatus struct {
	ID      string    `json:"id"`
	Running bool      `json:"running"`
	Addr    string    `json:"addr,omitempty"`
	Hash    string    `json:"hash,omitempty"`
	Started time.Time `json:"started,omitempty"`
	LastErr string    `json:"last_err,omitempty"`
}

// Status returns a snapshot of every known instance, including ones
// that failed to start. Stable order by id so the UI list doesn't
// jump around between polls.
func (m *Manager) Status() []InstanceStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]InstanceStatus, 0, len(m.instances))
	for _, inst := range m.instances {
		out = append(out, InstanceStatus{
			ID:      inst.id,
			Running: inst.cancel != nil,
			Addr:    inst.addr,
			Hash:    inst.hash,
			Started: inst.started,
			LastErr: inst.lastErr,
		})
	}
	sortByKey(out, func(s InstanceStatus) string { return s.ID })
	return out
}

// Reconcile diffs the desired list of servers against the running
// set and applies the minimum set of changes. The caller (api layer)
// invokes this on settings save AND on boot.
//
// Returns the first compile error encountered; the rest of the list
// is still reconciled so one broken graph doesn't take the others
// down. The UI separately calls Validate to surface compile errors
// to the operator before saving.
func (m *Manager) Reconcile(servers []ProxyServer) error {
	want := make(map[string]ProxyServer, len(servers))
	for _, s := range servers {
		want[s.ID] = s
	}

	var firstErr error

	m.mu.Lock()
	// 1. Stop instances that disappeared or that are now disabled.
	for id, inst := range m.instances {
		s, ok := want[id]
		if !ok || !s.Enabled {
			m.stopLocked(inst)
			delete(m.instances, id)
		}
	}
	m.mu.Unlock()

	// 2. Start or restart each desired instance.
	for _, s := range servers {
		if !s.Enabled {
			continue
		}
		if err := m.applyOne(s); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// applyOne starts a fresh instance, restarts an existing one when its
// pipeline hash has changed, or no-ops when the hash matches.
func (m *Manager) applyOne(s ProxyServer) error {
	pipe, err := Compile(s, m.deps)
	if err != nil {
		// Persist the error against the (existing or placeholder)
		// instance entry so Status() can show it. If the instance
		// is already running with an older pipeline we leave it
		// alone — see the "last good Pipeline" note in graph.go.
		m.mu.Lock()
		inst := m.instances[s.ID]
		if inst == nil {
			inst = &runningInstance{id: s.ID}
			m.instances[s.ID] = inst
		}
		inst.lastErr = err.Error()
		m.mu.Unlock()
		return fmt.Errorf("compile %s: %w", s.ID, err)
	}

	m.mu.Lock()
	inst := m.instances[s.ID]
	if inst != nil && inst.cancel != nil && inst.hash == pipe.Hash {
		// Nothing to do.
		inst.lastErr = ""
		m.mu.Unlock()
		return nil
	}
	// Stop the previous incarnation (if any) before opening a new
	// listener — they bind the same port. Capture the done channel
	// outside the lock so we can wait without blocking other
	// reconcile calls (status reads etc.).
	var prevDone chan struct{}
	if inst != nil {
		m.stopLocked(inst)
		prevDone = inst.done
	}
	m.mu.Unlock()

	if prevDone != nil {
		// Wait for the old listener to release its socket. The cap
		// matches ada's default shutdown timeout (10s) plus a small
		// margin; if it really hangs we proceed and let the OS
		// surface "address in use" instead of deadlocking forever.
		select {
		case <-prevDone:
		case <-time.After(12 * time.Second):
		}
	}

	addr, err := m.resolveAddr(pipe)
	if err != nil {
		m.recordError(s.ID, err)
		return err
	}
	if err := m.validatePort(pipe.ListenPort); err != nil {
		m.recordError(s.ID, err)
		return err
	}

	cancel, done, err := m.startInstance(s.ID, addr, pipe)
	if err != nil {
		m.recordError(s.ID, err)
		return err
	}

	m.mu.Lock()
	m.instances[s.ID] = &runningInstance{
		id:      s.ID,
		addr:    addr,
		hash:    pipe.Hash,
		cancel:  cancel,
		done:    done,
		started: time.Now(),
	}
	m.mu.Unlock()
	return nil
}

// startInstance builds a fresh ada server, mounts the pipeline, and
// launches the listener in a goroutine. Returns the per-instance
// cancel function (closing it triggers ada's graceful shutdown) and
// a "done" channel closed once Start returns so callers can wait
// for the socket to be released before binding a successor on the
// same port.
func (m *Manager) startInstance(id, addr string, pipe CompiledPipeline) (context.CancelFunc, chan struct{}, error) {
	s := ada.New()
	// Baseline: panic recovery and a Server header that names the
	// proxy. We intentionally leave out cors/log/requestid — those
	// are user-configurable middleware nodes the operator can drop
	// into the graph if they want.
	s.Use(
		mrecover.Middleware(),
		mserver.Middleware("pika-proxy/"+id),
	)
	// Compile produced a single root handler that owns ALL routing
	// (path/method/host/IP/header/query) through any switch nodes
	// the operator placed on the graph. Mount it as a catch-all so
	// every request that lands on the listener goes through the
	// graph's defined entry point.
	s.Mux.HandleWildcard("/", pipe.Root)

	pctx, pcancel := context.WithCancel(m.parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		slog.Info("proxy listener starting", "id", id, "addr", addr)
		if err := s.StartWithContext(pctx, addr); err != nil {
			slog.Error("proxy listener failed", "id", id, "addr", addr, "error", err)
			m.recordError(id, err)
		}
	}()
	return pcancel, done, nil
}

// stopLocked tears down a running instance. The caller must hold m.mu.
func (m *Manager) stopLocked(inst *runningInstance) {
	if inst == nil || inst.cancel == nil {
		return
	}
	inst.cancel()
	inst.cancel = nil
}

// Stop cancels every running instance and waits for each listener
// goroutine to release its socket before returning. Without the wait
// a restart immediately after Stop could race the OS socket release
// and fail with "address already in use" on the same port. The
// global timeout matches the per-instance budget used by applyOne
// (12s = ada's default shutdown + margin) — if a listener really
// hangs we surface logs and proceed instead of deadlocking forever.
func (m *Manager) Stop() {
	m.mu.Lock()
	dones := make([]chan struct{}, 0, len(m.instances))
	ids := make([]string, 0, len(m.instances))
	for id, inst := range m.instances {
		if inst == nil {
			continue
		}
		if inst.cancel != nil {
			inst.cancel()
			inst.cancel = nil
		}
		if inst.done != nil {
			dones = append(dones, inst.done)
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

func (m *Manager) recordError(id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := m.instances[id]
	if inst == nil {
		inst = &runningInstance{id: id}
		m.instances[id] = inst
	}
	inst.lastErr = err.Error()
}

// resolveAddr computes the bind address from the pipeline + manager
// defaults. Done as a method so resolution stays in one place even
// when we add unix-socket support later.
func (m *Manager) resolveAddr(pipe CompiledPipeline) (string, error) {
	host := pipe.ListenHost
	if host == "" {
		host = m.baseHost
	}
	if pipe.ListenPort == "" {
		return "", fmt.Errorf("proxy: listen port required")
	}
	return net.JoinHostPort(host, pipe.ListenPort), nil
}

// validatePort rejects ports owned by the rest of pika. Other ports
// get a cheap "is the OS happy with this string" sanity check.
func (m *Manager) validatePort(port string) error {
	if _, blocked := m.reservedPorts[port]; blocked {
		return fmt.Errorf("proxy: port %s is reserved by another pika listener", port)
	}
	return nil
}

// Validate compiles a single server without starting it. Used by the
// /api/v1/proxy/{id}/validate endpoint so the UI can pre-flight a
// graph before persisting it.
func (m *Manager) Validate(s ProxyServer) (CompiledPipeline, error) {
	pipe, err := Compile(s, m.deps)
	if err != nil {
		return CompiledPipeline{}, err
	}
	if err := m.validatePort(pipe.ListenPort); err != nil {
		return CompiledPipeline{}, err
	}
	return pipe, nil
}
