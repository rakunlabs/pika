package cluster

import (
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
				wrapped := &writeNotifier{ResponseWriter: w, cluster: c}
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
type writeNotifier struct {
	http.ResponseWriter
	cluster       *Cluster
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
	if n.statusCode >= 200 && n.statusCode < 300 {
		n.cluster.NotifySync()
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
