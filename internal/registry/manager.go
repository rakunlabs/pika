package registry

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/rakunlabs/pika/internal/service"
)

// Manager owns the in-memory routing table for /registries/* traffic.
// It rebuilds the table on every Settings.Registry change so the
// hot path stays lockless after the swap.
//
// Lifecycle:
//
//	mgr := registry.NewManager(deps)
//	mgr.RegisterFactory("go", "local", goproxy.NewLocal)
//	mgr.RegisterFactory("go", "remote", goproxy.NewRemote)
//	... (npm, docker)
//	mgr.Reload(ctx, settings.Registry)   // initial build
//
// On postSettings: caller invokes mgr.Reload again with the new tree.
// In-flight requests against the old routing table complete against
// their (now-released) Registry handles; the snapshot read of
// `current` is atomic-pointer-style under the read lock.
type Manager struct {
	deps Deps

	mu        sync.RWMutex
	factories map[factoryKey]Factory
	current   *snapshot
}

// snapshot is one immutable build of the routing table. The manager
// publishes a new snapshot on every Reload and discards (Close) the
// previous one after the swap.
type snapshot struct {
	// regs indexes registries by their composite key "{ns}/{repo}".
	regs map[string]Registry
}

type factoryKey struct {
	Type string // "go" | "npm" | "docker"
	Kind string // "local" | "remote" | "virtual"
}

// NewManager constructs an empty manager. Callers must register every
// (type, kind) Factory they need (via RegisterFactory) before the
// first Reload, otherwise rows of unknown shape will be skipped with
// a warning log.
func NewManager(deps Deps) *Manager {
	return &Manager{
		deps:      deps,
		factories: make(map[factoryKey]Factory),
		current:   &snapshot{regs: map[string]Registry{}},
	}
}

// RegisterFactory associates a (type, kind) tuple with the builder
// that the manager will call when a matching repo row shows up in
// settings. Returns an error if (type, kind) is already registered —
// double registration is almost always a programming mistake at boot
// wiring and should surface loudly.
func (m *Manager) RegisterFactory(typ, kind string, f Factory) error {
	if typ == "" || kind == "" || f == nil {
		return fmt.Errorf("registry: invalid factory registration (type=%q kind=%q nil=%v)", typ, kind, f == nil)
	}
	k := factoryKey{Type: typ, Kind: kind}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.factories[k]; dup {
		return fmt.Errorf("registry: factory %s/%s already registered", typ, kind)
	}
	m.factories[k] = f
	return nil
}

// Reload (re)builds the routing table from a RegistrySettings tree.
// Safe to call multiple times. A nil tree (or an empty Namespaces
// slice) collapses the routing table to empty without freeing
// existing factories — the next non-empty Reload picks up where the
// previous one left off.
//
// Reload tolerates per-row errors: a factory that fails to build is
// logged and skipped; the remaining rows are still installed. This
// matches the behaviour of every other settings-driven subsystem in
// pika (proxy reconcile, raw mount build) so one bad row never
// disables the rest.
func (m *Manager) Reload(ctx context.Context, rs *service.RegistrySettings) {
	next := &snapshot{regs: map[string]Registry{}}

	if rs != nil {
		for i := range rs.Namespaces {
			ns := &rs.Namespaces[i]
			for j := range ns.Repositories {
				r := &ns.Repositories[j]
				m.buildRow(ctx, ns.Name, r, next)
			}
		}
	}

	m.mu.Lock()
	old := m.current
	m.current = next
	m.mu.Unlock()

	if old != nil {
		for _, reg := range old.regs {
			if err := reg.Close(); err != nil {
				slog.Warn("registry: error closing old registry on reload",
					"namespace", reg.Namespace(), "repo", reg.Name(), "error", err)
			}
		}
	}
	slog.Info("registry: routing table reloaded", "count", len(next.regs))
}

// buildRow looks up the factory for one repo row and installs the
// resulting Registry into the next snapshot. Errors are logged and
// the row is skipped.
func (m *Manager) buildRow(ctx context.Context, ns string, r *service.RegistryRepository, next *snapshot) {
	m.mu.RLock()
	f, ok := m.factories[factoryKey{Type: r.Type, Kind: r.Kind}]
	m.mu.RUnlock()
	if !ok {
		slog.Warn("registry: no factory registered for repo",
			"namespace", ns, "repo", r.Name, "type", r.Type, "kind", r.Kind)
		return
	}
	reg, err := f(ctx, m.deps, ns, r)
	if err != nil {
		slog.Warn("registry: failed to build repo",
			"namespace", ns, "repo", r.Name, "type", r.Type, "kind", r.Kind, "error", err)
		return
	}
	next.regs[regKey(ns, r.Name)] = reg
}

// Lookup returns the Registry for the given (namespace, repo) pair
// or (nil, false). Read path: no locks beyond the snapshot pointer
// load. Virtual repos resolve through their member chain at request
// time; the manager only exposes them by their own name.
func (m *Manager) Lookup(namespace, repo string) (Registry, bool) {
	m.mu.RLock()
	snap := m.current
	m.mu.RUnlock()
	if snap == nil {
		return nil, false
	}
	reg, ok := snap.regs[regKey(namespace, repo)]
	return reg, ok
}

// List returns every (namespace, repo) currently routable. Order is
// unspecified (map iteration); callers that need stable ordering must
// sort the result themselves.
func (m *Manager) List() []Registry {
	m.mu.RLock()
	snap := m.current
	m.mu.RUnlock()
	if snap == nil {
		return nil
	}
	out := make([]Registry, 0, len(snap.regs))
	for _, reg := range snap.regs {
		out = append(out, reg)
	}
	return out
}

// Close releases every Registry in the current snapshot. Intended
// for graceful shutdown — does not clear factories.
func (m *Manager) Close() error {
	m.mu.Lock()
	old := m.current
	m.current = &snapshot{regs: map[string]Registry{}}
	m.mu.Unlock()

	if old == nil {
		return nil
	}
	for _, reg := range old.regs {
		if err := reg.Close(); err != nil {
			slog.Warn("registry: error closing registry on shutdown",
				"namespace", reg.Namespace(), "repo", reg.Name(), "error", err)
		}
	}
	return nil
}

func regKey(ns, repo string) string {
	return ns + "/" + repo
}

// SplitRequestPath separates "/registries/{namespace}/{repo}/{rest}"
// into the three components. The leading slash and trailing rest
// (which may start with `/` or be empty) are preserved as the
// implementation expects. Returns ok=false when the URL does not
// have at least namespace + repo segments.
//
// Used by the HTTP entry handler in internal/server/api before
// dispatching to a Registry.
func SplitRequestPath(p string) (namespace, repo, rest string, ok bool) {
	// Strip leading slash for splitting.
	q := strings.TrimPrefix(p, "/")
	// Expecting "{namespace}/{repo}[/{rest}]"
	first := strings.IndexByte(q, '/')
	if first <= 0 {
		return "", "", "", false
	}
	namespace = q[:first]
	q = q[first+1:]
	second := strings.IndexByte(q, '/')
	if second < 0 {
		// No remainder past the repo segment.
		return namespace, q, "", q != ""
	}
	repo = q[:second]
	rest = q[second:] // keep leading slash so handlers see "/@v/list"
	if repo == "" {
		return "", "", "", false
	}
	return namespace, repo, rest, true
}
