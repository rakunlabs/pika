// Package proxy implements user-defined HTTP servers built from a
// visual node graph. Each ProxyServer is a standalone ada server
// instance bound to its own port, composed of a chain of
// middlewares, switches and terminal handlers.
//
// The package is layered as:
//
//   - graph.go     — wire-format types persisted in settings; graph
//     compile from {nodes, edges} into a Pipeline.
//   - middlewares  — registry of built-in middleware kinds.
//   - handlers     — registry of built-in terminal handler kinds.
//   - switch.go    — the composite switch node (host/IP/path/method
//     /header/query rules) plus its compiled runtime.
//   - runner.go    — Manager that reconciles []ProxyServer into live
//     ada server instances with per-instance cancel.
//   - catalog.go   — public discovery of registered kinds + config
//     schemas, served to the UI so the form fields and
//     validation are driven by backend metadata.
//
// Unified node model (the bit that drives Compile):
//
//	Every node — listener, middleware, handler, switch — compiles
//	into the SAME shape: a func(next http.Handler) http.Handler.
//
//	- Listener nodes are pass-throughs (return next as-is).
//	- Middleware nodes wrap next.
//	- Handler nodes ignore next; they own the response.
//	- Switch nodes compose their branches (pre-built sub-pipelines,
//	  one per output handle) and dispatch by rule.
//
// Graph contract (enforced in Compile):
//
//   - Exactly one listener node. It is the root of traversal;
//     orphan nodes are an error so a saved graph always matches
//     what runs.
//   - Edges form a DAG. Cycles are rejected.
//   - Every non-switch, non-handler node has exactly one outgoing
//     edge on its single "out" handle. Switch nodes fan out with
//     one outgoing edge per rule.id (+ the "default" handle).
//   - Handlers are terminal: zero outgoing edges.
//
// The compiled Pipeline is what the runner consumes; the graph is
// kept around as an opaque blob so the UI can round-trip it.
// Pipeline.Hash exists so the runner can decide cheaply whether a
// saved server needs to be restarted on settings reload.
package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/rakunlabs/pika/internal/service"
)

// Node type identifiers. Stored verbatim on disk; do not rename
// without a migration.
const (
	NodeTypeListener   = "listener"
	NodeTypeMiddleware = "middleware"
	NodeTypeSwitch     = "switch"
	NodeTypeHandler    = "handler"

	// DefaultOutHandle is the source_handle value used by every
	// non-switch node. Switches use rule.ID (or DefaultSwitchID).
	DefaultOutHandle = "out"
)

// ProxyServer is the wire/storage form of one user-configured
// listener + pipeline. Defined in the service package so the
// settings row can serialize it without importing this package
// (which would create a cycle). Aliased here so existing code in
// this package keeps reading naturally.
type ProxyServer = service.ProxyServer

// ProxyNode mirrors a kaykay flow node.
type ProxyNode = service.ProxyNode

// Point is the kaykay canvas position.
type Point = service.ProxyPoint

// ProxyEdge mirrors a kaykay flow edge.
type ProxyEdge = service.ProxyEdge

// CompiledPipeline is the runner-facing flattened form of a graph.
// Root is the single http.Handler the runner mounts at "/*" on the
// ada server; every routing decision happens INSIDE that handler
// (via the compiled switch tree).
type CompiledPipeline struct {
	// Hash is a stable fingerprint of the input graph (nodes +
	// edges + listener port). The runner uses it to detect "no
	// real change" reloads so a noisy UI save (e.g. a node was
	// dragged 1px) doesn't bounce a healthy listener.
	Hash string `json:"hash"`

	// Protocol is "http" (default) or "tcp". The runner chooses the
	// listener implementation from this value.
	Protocol string `json:"protocol"`

	// ListenHost / ListenPort are pulled out for the runner;
	// duplicates info from ProxyServer.Host/Port intentionally so
	// the runner can rely on Pipeline alone.
	ListenHost string `json:"listen_host"`
	ListenPort string `json:"listen_port"`

	// Root is the fully-composed handler tree. The runner mounts
	// this at "/*" — the path matching that used to live in the
	// runner now lives inside switch nodes.
	Root http.Handler `json:"-"`

	// TCPRoot is the fully-composed TCP connection handler. Only set
	// when Protocol == "tcp".
	TCPRoot TCPHandler `json:"-"`
}

// CompileError carries a structured failure so the UI can highlight
// the offending node directly instead of parsing a free-form string.
type CompileError struct {
	NodeID  string `json:"node_id,omitempty"`
	EdgeID  string `json:"edge_id,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *CompileError) Error() string {
	switch {
	case e.NodeID != "":
		return fmt.Sprintf("proxy compile: node %s: %s", e.NodeID, e.Message)
	case e.EdgeID != "":
		return fmt.Sprintf("proxy compile: edge %s: %s", e.EdgeID, e.Message)
	default:
		return "proxy compile: " + e.Message
	}
}

// ErrEmptyGraph is returned when a server is saved with no nodes —
// the UI uses it to distinguish "draft, not ready to run" from a
// real compile failure.
var ErrEmptyGraph = errors.New("proxy: empty graph")

// CompileDeps lets Compile resolve the per-node builders. Splitting
// the dependencies out (rather than reading globals) keeps the
// package testable: tests can pass a registry with stub builders.
//
// All three NodeSpec maps are merged for lookup; we keep them
// separate on the input side so callers can swap one bucket
// (typically Switches for "no switches in this test"). Empty maps
// are treated as "none registered".
type CompileDeps struct {
	Middlewares    map[string]NodeSpec
	Handlers       map[string]NodeSpec
	Switches       map[string]NodeSpec
	TCPMiddlewares map[string]NodeSpec
	TCPHandlers    map[string]NodeSpec
	Service        ServiceDeps
}

// Default builds a CompileDeps that uses the package-level registries
// and the supplied service handle. The runner uses this; tests build
// their own.
func Default(svc ServiceDeps) CompileDeps {
	return CompileDeps{
		Middlewares:    DefaultMiddlewares(),
		Handlers:       DefaultHandlers(),
		Switches:       DefaultSwitches(),
		TCPMiddlewares: DefaultTCPMiddlewares(),
		TCPHandlers:    DefaultTCPHandlers(),
		Service:        svc,
	}
}

// specFor resolves the NodeSpec for a node by (type, subtype). Kept
// as a method on CompileDeps so it can pick the right registry
// based on Type without duplicating the lookup logic at every
// call site.
func (d CompileDeps) specFor(protocol string, n *ProxyNode) (NodeSpec, *CompileError) {
	switch n.Type {
	case NodeTypeMiddleware:
		registry := d.Middlewares
		unknownCode := "unknown_middleware"
		if protocol == ProtocolTCP {
			registry = d.TCPMiddlewares
			unknownCode = "unknown_tcp_middleware"
		}
		s, ok := registry[n.Subtype]
		if !ok {
			return NodeSpec{}, &CompileError{NodeID: n.ID, Code: unknownCode, Message: "unknown " + protocol + " middleware subtype: " + n.Subtype}
		}
		if sp := specProtocol(s); sp != protocol {
			return NodeSpec{}, &CompileError{NodeID: n.ID, Code: "protocol_mismatch", Message: "node is " + sp + " but server protocol is " + protocol}
		}
		return s, nil
	case NodeTypeHandler:
		registry := d.Handlers
		unknownCode := "unknown_handler"
		if protocol == ProtocolTCP {
			registry = d.TCPHandlers
			unknownCode = "unknown_tcp_handler"
		}
		s, ok := registry[n.Subtype]
		if !ok {
			return NodeSpec{}, &CompileError{NodeID: n.ID, Code: unknownCode, Message: "unknown " + protocol + " handler subtype: " + n.Subtype}
		}
		if sp := specProtocol(s); sp != protocol {
			return NodeSpec{}, &CompileError{NodeID: n.ID, Code: "protocol_mismatch", Message: "node is " + sp + " but server protocol is " + protocol}
		}
		return s, nil
	case NodeTypeSwitch:
		if protocol != ProtocolHTTP {
			return NodeSpec{}, &CompileError{NodeID: n.ID, Code: "unsupported_tcp_switch", Message: "switch nodes are only supported for HTTP proxy servers"}
		}
		// Switch nodes carry a subtype too (currently always
		// "switch") so future variants (e.g. weighted routing)
		// share the registry pattern.
		sub := n.Subtype
		if sub == "" {
			sub = "switch"
		}
		s, ok := d.Switches[sub]
		if !ok {
			return NodeSpec{}, &CompileError{NodeID: n.ID, Code: "unknown_switch", Message: "unknown switch subtype: " + sub}
		}
		if sp := specProtocol(s); sp != ProtocolHTTP {
			return NodeSpec{}, &CompileError{NodeID: n.ID, Code: "protocol_mismatch", Message: "switch is " + sp + " but server protocol is " + protocol}
		}
		return s, nil
	}
	return NodeSpec{}, &CompileError{NodeID: n.ID, Code: "unknown_type", Message: "node type does not have a builder: " + n.Type}
}

// Compile turns a graph into a Pipeline. It does not start anything;
// the runner calls it on every saved ProxyServer and only spawns when
// it returns nil.
//
// Algorithm:
//  1. Index nodes by ID; reject duplicates and unknown types.
//  2. Find the unique listener.
//  3. Build an adjacency map keyed by (sourceID, sourceHandle).
//     Reject dangling edge endpoints.
//  4. DFS for cycles.
//  5. Recursively compose: build(nodeID) returns a Middleware that
//     wraps its single downstream chain. Switch nodes consume their
//     branch sub-chains through the BranchSet arg.
//  6. Orphan check (every node must be visited during build).
//  7. Materialise Root = build(listener)(terminal404) and compute
//     the deterministic Hash.
func Compile(srv ProxyServer, deps CompileDeps) (CompiledPipeline, error) {
	if len(srv.Nodes) == 0 {
		return CompiledPipeline{}, ErrEmptyGraph
	}

	protocol, perr := normaliseProtocol(srv.Protocol)
	if perr != nil {
		return CompiledPipeline{}, &CompileError{Code: "bad_protocol", Message: perr.Error()}
	}
	host := trimSpace(srv.Host)
	port := trimSpace(srv.Port)
	if port == "" {
		return CompiledPipeline{}, &CompileError{Code: "missing_port", Message: "listen port is required"}
	}
	if protocol == ProtocolTCP {
		return compileTCP(srv, deps, protocol, host, port)
	}

	// 1. Index + per-node validation.
	byID := make(map[string]*ProxyNode, len(srv.Nodes))
	for i := range srv.Nodes {
		n := &srv.Nodes[i]
		if n.ID == "" {
			return CompiledPipeline{}, &CompileError{Code: "missing_id", Message: "node has empty id"}
		}
		if _, dup := byID[n.ID]; dup {
			return CompiledPipeline{}, &CompileError{NodeID: n.ID, Code: "duplicate_id", Message: "duplicate node id"}
		}
		if err := validateNodeProtocol(protocol, n); err != nil {
			return CompiledPipeline{}, err
		}
		byID[n.ID] = n
		switch n.Type {
		case NodeTypeListener, NodeTypeMiddleware, NodeTypeSwitch, NodeTypeHandler:
		default:
			return CompiledPipeline{}, &CompileError{NodeID: n.ID, Code: "unknown_type", Message: "unknown node type " + n.Type}
		}
	}

	// 2. Find exactly one listener.
	var listener *ProxyNode
	for _, n := range byID {
		if n.Type == NodeTypeListener {
			if listener != nil {
				return CompiledPipeline{}, &CompileError{NodeID: n.ID, Code: "multi_listener", Message: "graph has more than one listener"}
			}
			listener = n
		}
	}
	if listener == nil {
		return CompiledPipeline{}, &CompileError{Code: "no_listener", Message: "graph has no listener node"}
	}

	// 3. Build adjacency keyed by (source, handle). DefaultOutHandle
	// ("out") is the implicit handle for non-switch nodes; empty
	// SourceHandle on an edge is treated as the default.
	outgoing := make(map[string]map[string][]ProxyEdge, len(srv.Nodes))
	for _, e := range srv.Edges {
		if _, ok := byID[e.Source]; !ok {
			return CompiledPipeline{}, &CompileError{EdgeID: e.ID, Code: "edge_dangling_source", Message: "edge source node not found: " + e.Source}
		}
		if _, ok := byID[e.Target]; !ok {
			return CompiledPipeline{}, &CompileError{EdgeID: e.ID, Code: "edge_dangling_target", Message: "edge target node not found: " + e.Target}
		}
		handle := e.SourceHandle
		if handle == "" {
			handle = DefaultOutHandle
		}
		if outgoing[e.Source] == nil {
			outgoing[e.Source] = map[string][]ProxyEdge{}
		}
		outgoing[e.Source][handle] = append(outgoing[e.Source][handle], e)
	}

	// 4. Cycle detection via DFS coloring (white=0, gray=1, black=2).
	color := make(map[string]int, len(srv.Nodes))
	var dfs func(id string) error
	dfs = func(id string) error {
		switch color[id] {
		case 1:
			return &CompileError{NodeID: id, Code: "cycle", Message: "graph contains a cycle through this node"}
		case 2:
			return nil
		}
		color[id] = 1
		for _, group := range outgoing[id] {
			for _, e := range group {
				if err := dfs(e.Target); err != nil {
					return err
				}
			}
		}
		color[id] = 2
		return nil
	}
	if err := dfs(listener.ID); err != nil {
		return CompiledPipeline{}, err
	}

	// 5. Recursive compose. visited is BOTH our orphan guard and a
	// memoisation hint (a node reachable from two switch branches
	// would otherwise be built twice; we forbid that explicitly
	// because nodes carry side-effecting builders that should not
	// be invoked twice for the same logical node).
	visited := map[string]bool{}
	var build func(id string) (Middleware, error)
	build = func(id string) (Middleware, error) {
		if visited[id] {
			return nil, &CompileError{NodeID: id, Code: "revisit", Message: "node reachable through multiple paths"}
		}
		visited[id] = true
		node := byID[id]

		switch node.Type {
		case NodeTypeListener:
			// Listener has exactly one outgoing edge on "out".
			// (Zero outgoing edges = dead-end; the listener
			// MUST forward somewhere or there is nothing to
			// run.)
			next, err := walkSingle(node, outgoing, byID, build)
			if err != nil {
				return nil, err
			}
			// Pass-through middleware: the listener's role is
			// purely structural at the graph layer; the actual
			// "I am a listener" wiring lives in the runner.
			return func(_ http.Handler) http.Handler {
				return next(nil)
			}, nil

		case NodeTypeMiddleware:
			spec, cerr := deps.specFor(protocol, node)
			if cerr != nil {
				return nil, cerr
			}
			mw, err := spec.Build(node.Config, deps.Service, nil)
			if err != nil {
				return nil, &CompileError{NodeID: node.ID, Code: "middleware_build", Message: err.Error()}
			}
			next, err := walkSingle(node, outgoing, byID, build)
			if err != nil {
				return nil, err
			}
			// Compose: the middleware wraps the rest of the chain.
			return func(_ http.Handler) http.Handler {
				return mw(next(nil))
			}, nil

		case NodeTypeHandler:
			// Handlers must not have outgoing edges. Anything
			// downstream would be unreachable AND would imply
			// the operator misunderstood the model.
			if hasAnyOutgoing(outgoing[node.ID]) {
				return nil, &CompileError{NodeID: node.ID, Code: "handler_with_outgoing", Message: "handler nodes cannot have outgoing edges"}
			}
			spec, cerr := deps.specFor(protocol, node)
			if cerr != nil {
				return nil, cerr
			}
			mw, err := spec.Build(node.Config, deps.Service, nil)
			if err != nil {
				return nil, &CompileError{NodeID: node.ID, Code: "handler_build", Message: err.Error()}
			}
			return mw, nil

		case NodeTypeSwitch:
			spec, cerr := deps.specFor(protocol, node)
			if cerr != nil {
				return nil, cerr
			}
			// Parse rules so we know which output handles are
			// required. Branches map MUST contain every rule.id
			// plus DefaultSwitchID; missing entries are flagged
			// with a node-level error so the UI can highlight
			// the offending switch.
			var cfg SwitchConfig
			if len(node.Config) > 0 {
				if err := json.Unmarshal(node.Config, &cfg); err != nil {
					return nil, &CompileError{NodeID: node.ID, Code: "switch_config", Message: err.Error()}
				}
			}
			branches := BranchSet{}
			for _, rule := range cfg.Rules {
				targets := outgoing[node.ID][rule.ID]
				if len(targets) == 0 {
					return nil, &CompileError{NodeID: node.ID, Code: "switch_branch_missing", Message: "rule " + rule.ID + " has no wired branch"}
				}
				if len(targets) > 1 {
					return nil, &CompileError{NodeID: node.ID, Code: "switch_branch_fanout", Message: "rule " + rule.ID + " has multiple wired branches"}
				}
				sub, err := build(targets[0].Target)
				if err != nil {
					return nil, err
				}
				branches[rule.ID] = sub
			}
			// Default branch.
			defaultTargets := outgoing[node.ID][DefaultSwitchID]
			if len(defaultTargets) == 0 {
				return nil, &CompileError{NodeID: node.ID, Code: "switch_default_missing", Message: "switch has no 'default' branch wired"}
			}
			if len(defaultTargets) > 1 {
				return nil, &CompileError{NodeID: node.ID, Code: "switch_default_fanout", Message: "switch has multiple 'default' branches"}
			}
			defaultSub, err := build(defaultTargets[0].Target)
			if err != nil {
				return nil, err
			}
			branches[DefaultSwitchID] = defaultSub

			mw, err := spec.Build(node.Config, deps.Service, branches)
			if err != nil {
				return nil, &CompileError{NodeID: node.ID, Code: "switch_build", Message: err.Error()}
			}
			return mw, nil
		}

		return nil, &CompileError{NodeID: node.ID, Code: "unknown_type", Message: "no builder for type: " + node.Type}
	}

	rootMW, err := build(listener.ID)
	if err != nil {
		return CompiledPipeline{}, err
	}

	// 6. Orphan check.
	for id := range byID {
		if !visited[id] {
			return CompiledPipeline{}, &CompileError{NodeID: id, Code: "orphan", Message: "node is not reachable from the listener"}
		}
	}

	// 7. Wrap the root chain with a terminal 404 so any middleware
	// that calls next() without a downstream handler still produces
	// a sensible response.
	root := rootMW(http.HandlerFunc(http.NotFound))

	pipe := CompiledPipeline{
		Protocol:   protocol,
		ListenHost: host,
		ListenPort: port,
		Root:       root,
	}
	pipe.Hash = hashPipeline(srv)
	return pipe, nil
}

func compileTCP(srv ProxyServer, deps CompileDeps, protocol, host, port string) (CompiledPipeline, error) {
	byID := make(map[string]*ProxyNode, len(srv.Nodes))
	for i := range srv.Nodes {
		n := &srv.Nodes[i]
		if n.ID == "" {
			return CompiledPipeline{}, &CompileError{Code: "missing_id", Message: "node has empty id"}
		}
		if _, dup := byID[n.ID]; dup {
			return CompiledPipeline{}, &CompileError{NodeID: n.ID, Code: "duplicate_id", Message: "duplicate node id"}
		}
		if err := validateNodeProtocol(protocol, n); err != nil {
			return CompiledPipeline{}, err
		}
		byID[n.ID] = n
		switch n.Type {
		case NodeTypeListener, NodeTypeMiddleware, NodeTypeHandler:
		case NodeTypeSwitch:
			return CompiledPipeline{}, &CompileError{NodeID: n.ID, Code: "unsupported_tcp_switch", Message: "switch nodes are only supported for HTTP proxy servers"}
		default:
			return CompiledPipeline{}, &CompileError{NodeID: n.ID, Code: "unknown_type", Message: "unknown node type " + n.Type}
		}
	}

	var listener *ProxyNode
	for _, n := range byID {
		if n.Type == NodeTypeListener {
			if listener != nil {
				return CompiledPipeline{}, &CompileError{NodeID: n.ID, Code: "multi_listener", Message: "graph has more than one listener"}
			}
			listener = n
		}
	}
	if listener == nil {
		return CompiledPipeline{}, &CompileError{Code: "no_listener", Message: "graph has no listener node"}
	}

	outgoing := make(map[string]map[string][]ProxyEdge, len(srv.Nodes))
	for _, e := range srv.Edges {
		if _, ok := byID[e.Source]; !ok {
			return CompiledPipeline{}, &CompileError{EdgeID: e.ID, Code: "edge_dangling_source", Message: "edge source node not found: " + e.Source}
		}
		if _, ok := byID[e.Target]; !ok {
			return CompiledPipeline{}, &CompileError{EdgeID: e.ID, Code: "edge_dangling_target", Message: "edge target node not found: " + e.Target}
		}
		handle := e.SourceHandle
		if handle == "" {
			handle = DefaultOutHandle
		}
		if outgoing[e.Source] == nil {
			outgoing[e.Source] = map[string][]ProxyEdge{}
		}
		outgoing[e.Source][handle] = append(outgoing[e.Source][handle], e)
	}

	color := make(map[string]int, len(srv.Nodes))
	var dfs func(id string) error
	dfs = func(id string) error {
		switch color[id] {
		case 1:
			return &CompileError{NodeID: id, Code: "cycle", Message: "graph contains a cycle through this node"}
		case 2:
			return nil
		}
		color[id] = 1
		for _, group := range outgoing[id] {
			for _, e := range group {
				if err := dfs(e.Target); err != nil {
					return err
				}
			}
		}
		color[id] = 2
		return nil
	}
	if err := dfs(listener.ID); err != nil {
		return CompiledPipeline{}, err
	}

	visited := map[string]bool{}
	var build func(id string) (TCPMiddleware, error)
	build = func(id string) (TCPMiddleware, error) {
		if visited[id] {
			return nil, &CompileError{NodeID: id, Code: "revisit", Message: "node reachable through multiple paths"}
		}
		visited[id] = true
		node := byID[id]

		switch node.Type {
		case NodeTypeListener:
			next, err := walkSingleTCP(node, outgoing, build)
			if err != nil {
				return nil, err
			}
			return func(_ TCPHandler) TCPHandler {
				return next(nil)
			}, nil

		case NodeTypeMiddleware:
			spec, cerr := deps.specFor(protocol, node)
			if cerr != nil {
				return nil, cerr
			}
			if spec.BuildTCP == nil {
				return nil, &CompileError{NodeID: node.ID, Code: "tcp_builder_missing", Message: "TCP middleware has no TCP builder"}
			}
			mw, err := spec.BuildTCP(node.Config, deps.Service, nil)
			if err != nil {
				return nil, &CompileError{NodeID: node.ID, Code: "middleware_build", Message: err.Error()}
			}
			next, err := walkSingleTCP(node, outgoing, build)
			if err != nil {
				return nil, err
			}
			return func(_ TCPHandler) TCPHandler {
				return mw(next(nil))
			}, nil

		case NodeTypeHandler:
			if hasAnyOutgoing(outgoing[node.ID]) {
				return nil, &CompileError{NodeID: node.ID, Code: "handler_with_outgoing", Message: "handler nodes cannot have outgoing edges"}
			}
			spec, cerr := deps.specFor(protocol, node)
			if cerr != nil {
				return nil, cerr
			}
			if spec.BuildTCP == nil {
				return nil, &CompileError{NodeID: node.ID, Code: "tcp_builder_missing", Message: "TCP handler has no TCP builder"}
			}
			mw, err := spec.BuildTCP(node.Config, deps.Service, nil)
			if err != nil {
				return nil, &CompileError{NodeID: node.ID, Code: "handler_build", Message: err.Error()}
			}
			return mw, nil
		}

		return nil, &CompileError{NodeID: node.ID, Code: "unknown_type", Message: "no TCP builder for type: " + node.Type}
	}

	rootMW, err := build(listener.ID)
	if err != nil {
		return CompiledPipeline{}, err
	}
	for id := range byID {
		if !visited[id] {
			return CompiledPipeline{}, &CompileError{NodeID: id, Code: "orphan", Message: "node is not reachable from the listener"}
		}
	}

	pipe := CompiledPipeline{
		Protocol:   protocol,
		ListenHost: host,
		ListenPort: port,
		TCPRoot:    rootMW(tcpTerminalNotFound),
	}
	pipe.Hash = hashPipeline(srv)
	return pipe, nil
}

func tcpTerminalNotFound(_ context.Context, _ *net.TCPConn) error {
	return errors.New("tcp proxy: no terminal handler")
}

// walkSingle resolves the lone "out" edge of a listener / middleware
// node and recurses. Surfacing "exactly one outgoing edge" as a
// helper keeps the recursive build readable.
func walkSingle(
	from *ProxyNode,
	outgoing map[string]map[string][]ProxyEdge,
	byID map[string]*ProxyNode,
	build func(id string) (Middleware, error),
) (Middleware, error) {
	edges := outgoing[from.ID][DefaultOutHandle]
	switch len(edges) {
	case 0:
		return nil, &CompileError{NodeID: from.ID, Code: "dead_end", Message: from.Type + " node has no outgoing edge"}
	case 1:
		_ = byID // kept in the signature so future helpers can read node metadata
		return build(edges[0].Target)
	default:
		return nil, &CompileError{NodeID: from.ID, Code: "fanout_not_allowed", Message: from.Type + " node may have only one outgoing edge"}
	}
}

func walkSingleTCP(
	from *ProxyNode,
	outgoing map[string]map[string][]ProxyEdge,
	build func(id string) (TCPMiddleware, error),
) (TCPMiddleware, error) {
	edges := outgoing[from.ID][DefaultOutHandle]
	switch len(edges) {
	case 0:
		return nil, &CompileError{NodeID: from.ID, Code: "dead_end", Message: from.Type + " node has no outgoing edge"}
	case 1:
		return build(edges[0].Target)
	default:
		return nil, &CompileError{NodeID: from.ID, Code: "fanout_not_allowed", Message: from.Type + " node may have only one outgoing edge"}
	}
}

func hasAnyOutgoing(handles map[string][]ProxyEdge) bool {
	for _, group := range handles {
		if len(group) > 0 {
			return true
		}
	}
	return false
}

// hashPipeline is a stable fingerprint of the inputs that materially
// affect a running server: the host, port, ordered node ids/types/
// subtypes/configs and ordered edges. The goal is to make Reconcile
// a cheap no-op for cosmetic edits (a node was nudged on the canvas
// but nothing functional changed).
//
// Position fields are deliberately NOT included; configs ARE.
func hashPipeline(srv ProxyServer) string {
	h := sha256.New()
	protocol, _ := normaliseProtocol(srv.Protocol)
	fmt.Fprintf(h, "protocol=%s\nhost=%s\nport=%s\n", protocol, srv.Host, srv.Port)

	// Sort nodes by ID so the same graph produces the same hash
	// regardless of disk ordering.
	nodes := make([]*ProxyNode, 0, len(srv.Nodes))
	for i := range srv.Nodes {
		nodes = append(nodes, &srv.Nodes[i])
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	for _, n := range nodes {
		fmt.Fprintf(h, "node=%s|protocol=%s|type=%s|sub=%s|cfg=%s\n", n.ID, n.Protocol, n.Type, n.Subtype, string(n.Config))
	}

	edges := make([]ProxyEdge, len(srv.Edges))
	copy(edges, srv.Edges)
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Source != edges[j].Source {
			return edges[i].Source < edges[j].Source
		}
		if edges[i].SourceHandle != edges[j].SourceHandle {
			return edges[i].SourceHandle < edges[j].SourceHandle
		}
		return edges[i].Target < edges[j].Target
	})
	for _, e := range edges {
		fmt.Fprintf(h, "edge=%s>%s:%s>%s\n", e.Source, e.SourceHandle, e.Target, e.TargetHandle)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func normaliseProtocol(raw string) (string, error) {
	p := strings.ToLower(trimSpace(raw))
	if p == "" {
		return ProtocolHTTP, nil
	}
	switch p {
	case ProtocolHTTP, ProtocolTCP:
		return p, nil
	default:
		return "", fmt.Errorf("unsupported proxy protocol %q", raw)
	}
}

func validateNodeProtocol(serverProtocol string, n *ProxyNode) *CompileError {
	if trimSpace(n.Protocol) == "" {
		return nil
	}
	nodeProtocol, err := normaliseProtocol(n.Protocol)
	if err != nil {
		return &CompileError{NodeID: n.ID, Code: "bad_node_protocol", Message: err.Error()}
	}
	if nodeProtocol != serverProtocol {
		return &CompileError{NodeID: n.ID, Code: "protocol_mismatch", Message: "node is " + nodeProtocol + " but server protocol is " + serverProtocol}
	}
	n.Protocol = nodeProtocol
	return nil
}

func trimSpace(s string) string {
	// Tiny helper to avoid importing strings just for one call
	// — graph.go used to depend on strings via the old route-sort
	// path; the package surface stayed in this file for the next
	// reader to find easily.
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\n' || s[j-1] == '\r') {
		j--
	}
	return s[i:j]
}
