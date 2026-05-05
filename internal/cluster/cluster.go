// Package cluster provides leader-follower clustering for pika by wrapping
// rakunlabs/alan (QUIC peer discovery + distributed lock) and rakunlabs/bw's
// cluster glue.
//
// # Roles
//
//   - Reads (GET / HEAD / OPTIONS) are always served locally on every node.
//     The bw database is replicated, so any node has a fresh-enough copy.
//   - Writes are routed to the current leader. The follower serializes the
//     incoming HTTP request, ships it to the leader over alan/QUIC, and
//     returns the leader's response verbatim to the original client.
//   - After a successful write the leader broadcasts a sync notification so
//     followers pull the incremental backup.
//
// The wrapper is a no-op when [Config.Enabled] is false: [Cluster.IsLeader]
// returns true, [Cluster.Forward] runs the local handler directly, and
// [Cluster.NotifySync] is a no-op. This keeps the call sites in the server
// simple — they don't need to special-case "no clustering configured".
package cluster

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/rakunlabs/alan"
	"github.com/rakunlabs/bw"
	bwcluster "github.com/rakunlabs/bw/cluster"
)

// Config configures clustering. When Enabled is false the rest of the fields
// are ignored and the wrapper behaves as a single-node "always leader".
type Config struct {
	// Enabled gates the entire feature. Default: false (single-node mode).
	Enabled bool `cfg:"enabled" default:"false"`

	// DNSAddr is a DNS name resolved to the cluster's peer addresses (e.g.
	// a Kubernetes headless service). When empty alan still binds locally
	// and waits for peers to discover it via reverse direction.
	DNSAddr string `cfg:"dns_addr"`
	// BindAddr is the local UDP bind address. Default: "0.0.0.0".
	BindAddr string `cfg:"bind_addr"`
	// Port is the UDP/QUIC port. Default: 5000. All peers MUST agree.
	Port int `cfg:"port" default:"5000"`
	// Replicas is the expected cluster size, used by alan for quorum.
	Replicas int `cfg:"replicas"`
	// RefreshInterval controls how often DNSAddr is re-resolved.
	RefreshInterval time.Duration `cfg:"refresh_interval"`
	// HeartbeatInterval is QUIC keep-alive cadence.
	HeartbeatInterval time.Duration `cfg:"heartbeat_interval"`
	// HeartbeatTimeout is QUIC max idle timeout.
	HeartbeatTimeout time.Duration `cfg:"heartbeat_timeout"`

	// SecurityKey is a pre-shared key for cluster membership. When set,
	// only peers carrying the same PSK can join.
	SecurityKey string `cfg:"security_key" log:"-"`

	// LockKey is the alan distributed-lock name used for leader election.
	// Default: "pika-leader".
	LockKey string `cfg:"lock_key"`
	// SyncInterval is how often followers proactively pull from the leader
	// even without a sync notification. Default: 5m.
	SyncInterval time.Duration `cfg:"sync_interval"`
	// Prefix namespaces wire messages so this cluster's traffic doesn't
	// collide with other subsystems on the same alan instance. Default: "pika".
	Prefix string `cfg:"prefix"`

	// ForwardTimeout caps how long a follower waits for the leader to
	// reply to a forwarded request. Default: 30s.
	ForwardTimeout time.Duration `cfg:"forward_timeout"`
}

// ErrNoLeader is returned by Forward when the leader is currently unknown
// (e.g. during election). Surfaces to clients as 503 Service Unavailable.
var ErrNoLeader = errors.New("cluster: no leader known yet")

// Cluster is the pika-facing handle. It wraps the alan node + bw cluster
// and adds an http.Handler slot used by the leader to execute forwarded
// requests through the SAME middleware/route chain it would use for a
// directly-received request.
type Cluster struct {
	enabled bool
	cfg     Config

	alan *alan.Alan
	bw   *bwcluster.Cluster

	// rootHandler is the leader's HTTP handler (server mux). It is set
	// after the server is wired up via SetForwardHandler, so the
	// forwardHandler closure passed to bwcluster can find it later.
	rootHandler atomic.Pointer[http.Handler]

	// forwardTimeout caches Config.ForwardTimeout (resolved with default).
	forwardTimeout time.Duration
}

// New constructs a cluster wrapper. When cfg.Enabled is false it returns a
// no-op cluster (single-node mode). When enabled it builds an alan node and
// a bw cluster around the supplied bw.DB but does NOT start them — call
// Start after SetForwardHandler has wired the leader-side request executor.
func New(cfg Config, db *bw.DB) (*Cluster, error) {
	c := &Cluster{cfg: cfg, enabled: cfg.Enabled}
	if !cfg.Enabled {
		return c, nil
	}
	if db == nil {
		return nil, errors.New("cluster: bw.DB is required when enabled")
	}

	// Alan config — fall through to alan defaults for anything left zero.
	ac := alan.Config{
		DNSAddr:           cfg.DNSAddr,
		BindAddr:          cfg.BindAddr,
		Port:              cfg.Port,
		Replicas:          cfg.Replicas,
		RefreshInterval:   cfg.RefreshInterval,
		HeartbeatInterval: cfg.HeartbeatInterval,
		HeartbeatTimeout:  cfg.HeartbeatTimeout,
	}
	if cfg.SecurityKey != "" {
		ac.Security = alan.SecurityConfig{
			Enabled: true,
			Key:     []byte(cfg.SecurityKey),
		}
	}

	a, err := alan.New(ac)
	if err != nil {
		return nil, err
	}

	lockKey := cfg.LockKey
	if lockKey == "" {
		lockKey = "pika-leader"
	}
	prefix := cfg.Prefix
	if prefix == "" {
		prefix = "pika"
	}
	syncInterval := cfg.SyncInterval
	if syncInterval == 0 {
		syncInterval = 5 * time.Minute
	}

	bwOpts := []bwcluster.Option{
		bwcluster.WithLockKey(lockKey),
		bwcluster.WithPrefix(prefix),
		bwcluster.WithSyncInterval(syncInterval),
		bwcluster.WithOnLeaderChange(func(isLeader bool) {
			if isLeader {
				slog.Info("cluster: this node became leader")
			} else {
				slog.Info("cluster: this node stepped down")
			}
		}),
		// The forward handler dispatches each forwarded request through the
		// leader's own HTTP handler (set later via SetForwardHandler).
		bwcluster.WithForwardHandler(func(ctx context.Context, data []byte) []byte {
			hp := c.rootHandler.Load()
			if hp == nil {
				return encodeForwardError(http.StatusServiceUnavailable, "leader: handler not ready")
			}
			return runForwardedRequest(ctx, *hp, data)
		}),
	}

	c.alan = a
	c.bw = bwcluster.New(db, a, bwOpts...)

	c.forwardTimeout = cfg.ForwardTimeout
	if c.forwardTimeout <= 0 {
		c.forwardTimeout = 30 * time.Second
	}
	return c, nil
}

// Enabled reports whether clustering is on for this node.
func (c *Cluster) Enabled() bool { return c != nil && c.enabled }

// IsLeader reports whether this node currently holds the leader lock. When
// clustering is disabled this is always true (single-node mode).
func (c *Cluster) IsLeader() bool {
	if c == nil || !c.enabled {
		return true
	}
	return c.bw.IsLeader()
}

// SetForwardHandler installs the http.Handler used by the leader to execute
// forwarded requests. Must be called before Start, otherwise forwarded
// requests will fail with 503 until it's set.
func (c *Cluster) SetForwardHandler(h http.Handler) {
	if c == nil || !c.enabled {
		return
	}
	c.rootHandler.Store(&h)
}

// Forward sends the serialized HTTP request to the current leader and
// returns the serialized HTTP response. Single-node and on-leader paths
// invoke the local handler directly with no network round-trip.
func (c *Cluster) Forward(ctx context.Context, payload []byte) ([]byte, error) {
	if c == nil || !c.enabled {
		return nil, ErrNoLeader
	}
	fctx, cancel := context.WithTimeout(ctx, c.forwardTimeout)
	defer cancel()
	resp, err := c.bw.Forward(fctx, payload)
	if err != nil {
		if errors.Is(err, bwcluster.ErrNoLeader) {
			return nil, ErrNoLeader
		}
		return nil, err
	}
	return resp, nil
}

// NotifySync wakes followers to pull the latest backup. Call after every
// successful write on the leader. No-op on followers and in single-node mode.
func (c *Cluster) NotifySync() {
	if c == nil || !c.enabled {
		return
	}
	c.bw.NotifySync()
}

// Sync performs a synchronous pull from the leader. It blocks until the
// incremental backup is applied or the context expires. No-op on the leader
// and in single-node mode. Use this after forwarding a write to ensure the
// local database reflects the leader's state before responding to the client.
func (c *Cluster) Sync(ctx context.Context) error {
	if c == nil || !c.enabled {
		return nil
	}
	return c.bw.Sync(ctx)
}

// Start launches alan + bw cluster background goroutines. Returns once
// alan is ready to send/receive. Bound to ctx — cancellation triggers a
// graceful Stop.
func (c *Cluster) Start(ctx context.Context) error {
	if c == nil || !c.enabled {
		return nil
	}
	return c.bw.Start(ctx)
}

// Stop gracefully tears down the cluster.
func (c *Cluster) Stop() {
	if c == nil || !c.enabled {
		return
	}
	c.bw.Stop()
}
