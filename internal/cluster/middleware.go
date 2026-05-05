package cluster

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

// internalForwardHeader marks a request that arrived via the cluster forward
// path. The leader uses it to skip a second round of forwarding (just in case)
// and to suppress the post-write notify (the leader's *own* handler chain
// already calls NotifySync via the writeNotifier wrapper).
const internalForwardHeader = "X-Pika-Cluster-Forwarded"

// Middleware returns the http middleware that implements the read-local /
// write-forward routing. When the cluster is disabled or this node IS the
// leader, write requests are wrapped in a writeNotifier so a successful
// response triggers a sync broadcast. When this node is a follower, write
// requests are serialized and shipped to the leader; the leader's response
// is then streamed back to the original client.
//
// Reads pass through unchanged on every node.
func (c *Cluster) Middleware() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isReadMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			// Single-node mode: pass through, no notify needed.
			if c == nil || !c.enabled {
				next.ServeHTTP(w, r)
				return
			}

			// Leader path — including requests that arrived via forward.
			if c.IsLeader() {
				forwarded := r.Header.Get(internalForwardHeader) == "1"
				if forwarded {
					slog.Debug("cluster: leader executing forwarded write",
						"method", r.Method,
						"path", r.URL.Path,
						"remote_addr", r.RemoteAddr,
						"version", c.localVersion(),
					)
				}
				wrapped := &writeNotifier{
					ResponseWriter: w,
					cluster:        c,
					ctx:            r.Context(),
					method:         r.Method,
					path:           r.URL.Path,
					forwarded:      forwarded,
					beforeVersion:  c.localVersion(),
				}
				next.ServeHTTP(wrapped, r)
				wrapped.maybeNotify()
				return
			}

			// Defence in depth: a forwarded request reaching a follower
			// means leadership flipped mid-flight. Bail out rather than
			// loop the request back through Forward again.
			if r.Header.Get(internalForwardHeader) == "1" {
				http.Error(w, "cluster: forwarded request reached non-leader", http.StatusServiceUnavailable)
				return
			}

			// Mark and forward.
			r.Header.Set(internalForwardHeader, "1")
			payload, err := SerializeRequest(r)
			if err != nil {
				slog.Error("cluster: failed to serialize forward request", "error", err, "path", r.URL.Path)
				http.Error(w, "cluster: failed to serialize request", http.StatusInternalServerError)
				return
			}

			respBytes, err := c.Forward(r.Context(), payload)
			if err != nil {
				if errors.Is(err, ErrNoLeader) {
					http.Error(w, "cluster: no leader available, retry shortly", http.StatusServiceUnavailable)
					return
				}
				slog.Error("cluster: forward to leader failed", "error", err, "path", r.URL.Path)
				http.Error(w, "cluster: leader forward failed: "+err.Error(), http.StatusBadGateway)
				return
			}

			// Note: in bw v0.1.4 the leader's NotifySync (invoked inside the
			// forward handler on the leader side) blocks until every behind
			// follower has applied the diff, so by the time Forward returns
			// this node's own DB is already caught up. No explicit follower
			// pull is needed before responding to the client.

			if err := WriteForwardedResponse(w, respBytes); err != nil {
				slog.Warn("cluster: writing leader response back to client failed", "error", err)
			}
		})
	}
}

// writeNotifier captures the response status code so we can fire NotifySync
// on the leader after a successful (2xx) write. It also implements
// http.Flusher / http.Hijacker pass-through where the underlying writer
// supports them, so streaming endpoints (SSE) keep working.
//
// The captured ctx is the original request context. NotifySync needs a ctx
// to bound how long it waits for followers to apply the diff (bw v0.1.4+
// blocks until every behind follower acks). We reuse the request ctx so a
// client disconnect cancels the waiting.
type writeNotifier struct {
	http.ResponseWriter
	cluster *Cluster
	ctx     context.Context

	// Captured at request entry for the post-write log line.
	method        string
	path          string
	forwarded     bool
	beforeVersion uint64

	statusCode    int
	headerWritten bool
}

func (n *writeNotifier) WriteHeader(code int) {
	if n.headerWritten {
		return
	}
	n.statusCode = code
	n.headerWritten = true
	n.ResponseWriter.WriteHeader(code)
}

func (n *writeNotifier) Write(b []byte) (int, error) {
	if !n.headerWritten {
		n.statusCode = http.StatusOK
		n.headerWritten = true
	}
	return n.ResponseWriter.Write(b)
}

func (n *writeNotifier) Flush() {
	if f, ok := n.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (n *writeNotifier) maybeNotify() {
	if !n.headerWritten {
		return
	}
	if n.statusCode < 200 || n.statusCode >= 300 {
		return
	}
	afterVersion := n.cluster.localVersion()
	if afterVersion != n.beforeVersion {
		slog.Info("cluster: leader applied write",
			"method", n.method,
			"path", n.path,
			"forwarded", n.forwarded,
			"status", n.statusCode,
			"from_version", n.beforeVersion,
			"to_version", afterVersion,
		)
	}

	ctx := n.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	// NotifySync blocks until followers apply the diff. Errors are
	// logged but not surfaced to the client — the write itself
	// already succeeded locally on the leader.
	if err := n.cluster.NotifySync(ctx); err != nil {
		slog.Warn("cluster: notify sync after write failed",
			"method", n.method,
			"path", n.path,
			"error", err,
		)
	}
}

// isReadMethod reports whether the request method is safe for local serving.
// Anything else is considered a write and routed through the leader.
func isReadMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}
