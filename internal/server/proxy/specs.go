package proxy

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/service"
)

// ServiceDeps is the narrow surface the proxy package depends on so
// the runner and the unit tests can talk to the same builders without
// dragging the full *service.Service into the test setup. The real
// production wiring (`ServiceFromService`) just forwards every call
// to the matching method on *service.Service.
//
// Anything a handler or middleware needs from the application core
// goes through this interface. New handler kinds that need additional
// service methods should grow this interface (and the adapter) rather
// than reach into globals.
type ServiceDeps interface {
	// GetData mirrors service.Service.GetData.
	GetData(ctx context.Context, key, versionStr, variant string) (*service.DataResult, error)
	// ConvertFormat exposes the global helper as a method so tests
	// can inject conversions.
	ConvertFormat(in []byte, from, to string) ([]byte, error)
	// ReadExternal / WriteExternal / DeleteExternal mirror the
	// corresponding service methods, used by the "external" handler.
	ReadExternal(ctx context.Context, resource, path string) (*external.Entry, error)
	WriteExternal(ctx context.Context, resource, path string, data map[string]any) error
	DeleteExternal(ctx context.Context, resource, path string) error
	// ValidateToken is consumed by the auth-bearer middleware. It is
	// the same call that /data and /raw use today.
	ValidateToken(ctx context.Context, raw, scope, op string) error
}

// Middleware is the unified node shape. EVERY node in the proxy
// graph is built into a `func(next http.Handler) http.Handler`:
//
//   - Listener nodes pass through (return next as-is).
//   - Middleware nodes wrap next, optionally short-circuiting.
//   - Handler nodes ignore next and always terminate (write the
//     response themselves). They have no successor by contract;
//     a chain that puts another node after a handler is rejected
//     by Compile.
//   - Switch nodes route the request to ONE of their N branches
//     based on host/IP/path/method/header/query rules. Each
//     branch is itself a fully-compiled sub-pipeline starting at
//     a node the operator wired to the corresponding output
//     handle. Branches do not see `next` — once the switch
//     fires a branch, that branch decides the response.
//
// Keeping a single signature means Compile can compose nodes
// with a tiny `mw(next)` call regardless of kind; it also means
// new kinds drop in without touching graph.go or runner.go.
type Middleware = func(http.Handler) http.Handler

// BranchSet is passed to switch (and any future composite) Build
// functions. The keys are output handle IDs the operator wired on
// the canvas (matching ProxyEdge.SourceHandle); the values are the
// already-compiled middlewares for each downstream sub-pipeline.
// A non-composite node receives nil here and ignores the arg.
type BranchSet map[string]Middleware

// NodeBuilder constructs the Middleware closure for a node. cfg is
// the raw user JSON, svc is the production / test service handle,
// branches carries pre-built sub-pipelines for composite nodes
// (nil for leaves).
type NodeBuilder func(cfg json.RawMessage, svc ServiceDeps, branches BranchSet) (Middleware, error)

// NodeSpec describes ONE registered node kind. Every middleware,
// handler and structural node ships through this same struct so the
// catalog, the form panel, and Compile all see one surface.
//
// Kind drives UI grouping (palette categories) and Compile sanity
// checks ("the operator put a handler where a middleware was
// expected") — it does NOT affect how Build is called.
type NodeSpec struct {
	// Kind is one of: "middleware", "handler", "switch". Listener
	// is structural and built into Compile directly (no spec), but
	// in principle nothing stops it from being a NodeSpec too —
	// keeping it out avoids a Build that does nothing.
	Kind string `json:"kind"`

	Subtype      string          `json:"subtype"`
	Label        string          `json:"label"`
	Description  string          `json:"description,omitempty"`
	ConfigSchema json.RawMessage `json:"config_schema,omitempty"`

	// Build constructs the Middleware closure. Returning an error
	// here surfaces as a CompileError with the offending node id
	// attached so the UI can highlight the bad node.
	Build NodeBuilder `json:"-"`
}

// MiddlewareSpec / HandlerSpec aliases keep the existing call sites
// (Default*, tests, the api package) compiling while every site is
// converted to NodeSpec. They are intentionally aliases (not new
// types) so a NodeSpec value works wherever the old name was used.
//
// Once every caller is on NodeSpec these aliases can be deleted; for
// now they make the diff smaller and the rollback story easier.
type MiddlewareSpec = NodeSpec
type HandlerSpec = NodeSpec

// Catalog is the JSON envelope served by GET /api/v1/proxy/catalog.
// The UI keys its palette off Subtype within each Kind bucket and
// renders ConfigSchema with its generic JSON-Schema form renderer.
//
// Switches gets its own bucket so the UI can render it in a
// dedicated palette section (or, equivalently, treat it as a
// structural primitive) without inspecting Kind on each entry.
type Catalog struct {
	Middlewares []NodeSpec `json:"middlewares"`
	Handlers    []NodeSpec `json:"handlers"`
	Switches    []NodeSpec `json:"switches"`
}

// BuildCatalog assembles a Catalog from the package defaults. Server
// startup calls this once and serves the result statically; the list
// only changes when the binary is rebuilt with new kinds.
func BuildCatalog() Catalog {
	mws := DefaultMiddlewares()
	hs := DefaultHandlers()
	sw := DefaultSwitches()

	out := Catalog{}
	for _, m := range mws {
		out.Middlewares = append(out.Middlewares, m)
	}
	for _, h := range hs {
		out.Handlers = append(out.Handlers, h)
	}
	for _, s := range sw {
		out.Switches = append(out.Switches, s)
	}
	// Stable order: subtype ascending so the UI palette doesn't
	// reshuffle on every server boot.
	sortByKey(out.Middlewares, func(m NodeSpec) string { return m.Subtype })
	sortByKey(out.Handlers, func(h NodeSpec) string { return h.Subtype })
	sortByKey(out.Switches, func(s NodeSpec) string { return s.Subtype })
	return out
}

// sortByKey is a small generic helper used only for the catalog.
// Importing sort once at the call site is cheaper than building two
// near-identical sort.Slice closures here.
func sortByKey[T any](in []T, key func(T) string) {
	// Insertion sort is fine — the catalog is tiny and avoiding the
	// "sort" import would be cleaner if not for graph.go already
	// pulling it in.
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && key(in[j]) < key(in[j-1]); j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}
