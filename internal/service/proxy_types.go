package service

import "encoding/json"

// Proxy* types are the wire/storage forms of the user-built proxy
// servers. They are kept in the service package (rather than imported
// from internal/server/proxy) so the settings row can serialize them
// without dragging the runner into the service package — that
// direction would create an import cycle since the proxy package
// already depends on service for ServiceDeps.
//
// The shape mirrors proxy.ProxyServer 1:1; the proxy package has a
// short helper that round-trips through this struct.

type ProxyServer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// ListenerID is the owning ProxyListener.ID this graph is
	// attached to. Empty for legacy rows persisted before the
	// listener split — those carry Host/Port directly and the
	// manager synthesizes a hidden listener on first load (see
	// internal/server/proxy/runner.go migration path).
	ListenerID string `json:"listener_id,omitempty"`
	// HostMatch is the list of HTTP Host header patterns this graph
	// claims on its listener. An empty list is the catch-all branch
	// (at most one catch-all per HTTP listener). Patterns are matched
	// case-insensitively against r.Host after stripping any :port:
	//
	//   - "example.com" — exact match
	//   - "*.example.com" — suffix glob (one or more labels)
	//   - "*" — explicit catch-all (equivalent to empty list)
	//
	// Ignored for TCP listeners — TCP listeners accept at most one
	// graph at compile time.
	HostMatch []string `json:"host_match,omitempty"`

	// Protocol / Host / Port are kept for backwards compatibility with
	// rows persisted before the listener split. New rows should leave
	// Host/Port empty and inherit from ListenerID; Protocol is still
	// authoritative on the graph so a misattached graph fails compile
	// with a protocol-mismatch error rather than silently coercing.
	Protocol string `json:"protocol,omitempty"`
	// Deprecated: prefer ListenerID. Kept for legacy decode + an
	// auto-migration that runs once at boot.
	Host string `json:"host,omitempty"`
	// Deprecated: prefer ListenerID. See Host.
	Port string `json:"port,omitempty"`

	Nodes    []ProxyNode       `json:"nodes,omitempty"`
	Edges    []ProxyEdge       `json:"edges,omitempty"`
	Pipeline ProxyPipelineMeta `json:"pipeline,omitempty"`
}

// ProxyListener is a first-class persisted bind point — one
// host:port (HTTP or TCP) that one or more ProxyServer graphs can
// attach to. Splitting listeners from graphs lets a single port
// serve several user-built pipelines, routed by HTTP Host header
// for HTTP listeners. TCP listeners are 1:1 with their graph.
//
// Persisted in service.Settings.ProxyListeners. The proxy.Manager
// owns the actual socket; graphs only carry a ListenerID reference.
type ProxyListener struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Protocol string `json:"protocol"` // "http" | "tcp"
	Host     string `json:"host,omitempty"`
	Port     string `json:"port"`
	// TLS is reserved for the cert/key bundle (and ACME hosts in a
	// later iteration). The struct is wired through storage today so
	// the schema doesn't need a second bump when the wiring lands.
	TLS   ProxyListenerTLS `json:"tls,omitempty"`
	Notes string           `json:"notes,omitempty"`
}

// ProxyListenerTLS holds an inline PEM bundle for HTTPS listeners.
// Reserved fields (not yet honoured by the runner); kept here so
// the bw row schema is stable when TLS lands.
type ProxyListenerTLS struct {
	Enabled bool   `json:"enabled,omitempty"`
	CertPEM string `json:"cert_pem,omitempty"`
	KeyPEM  string `json:"key_pem,omitempty"`
}

type ProxyNode struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Protocol string          `json:"protocol,omitempty"`
	Subtype  string          `json:"subtype,omitempty"`
	Position ProxyPoint      `json:"position"`
	Config   json.RawMessage `json:"config,omitempty"`
}

type ProxyPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type ProxyEdge struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	SourceHandle string `json:"source_handle,omitempty"`
	Target       string `json:"target"`
	TargetHandle string `json:"target_handle,omitempty"`
}

// ProxyPipelineMeta is the persisted snapshot of compile metadata.
// The actual function-typed pipeline is rebuilt on every load — this
// struct only carries the bits that are useful for diagnostics and
// for change detection between the row and the live runtime.
type ProxyPipelineMeta struct {
	Hash       string `json:"hash,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
	ListenHost string `json:"listen_host,omitempty"`
	ListenPort string `json:"listen_port,omitempty"`
}
