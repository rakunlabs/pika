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
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Enabled  bool               `json:"enabled"`
	Host     string             `json:"host,omitempty"`
	Port     string             `json:"port"`
	Nodes    []ProxyNode        `json:"nodes,omitempty"`
	Edges    []ProxyEdge        `json:"edges,omitempty"`
	Pipeline ProxyPipelineMeta  `json:"pipeline,omitempty"`
}

type ProxyNode struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
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
	ListenHost string `json:"listen_host,omitempty"`
	ListenPort string `json:"listen_port,omitempty"`
}
