// Package mcpsrv exposes pika's configuration store over the Model Context
// Protocol (MCP) so AI agents can discover, read, search and edit
// configurations and external resources the same way the REST API and the
// SPA do.
//
// # Transport
//
// A single streamable-HTTP endpoint (POST /api/v1/mcp). It runs in
// stateless mode: every POST is a self-contained JSON-RPC exchange, so
// there is no cross-request session state that could outlive — or be
// reused across — the credentials that created it.
//
// # Authorization
//
// The endpoint carries no authentication of its own. It is mounted on the
// group that already runs authx Require() + CapMiddleware(), so by the
// time a request arrives the caller has been authenticated — API token via
// `Authorization: Bearer`, or the UI session cookie — and their resolved
// capabilities and path patterns are on the request context.
//
// Two things are layered on top of that:
//
//  1. Tool visibility is filtered per request. A caller who cannot write
//     never sees the write tools in tools/list. Agents plan against the
//     tools they are shown, so hiding what the caller cannot do avoids a
//     whole class of "the model kept retrying a forbidden call" failures.
//
//     Token callers are additionally filtered by scope operation, which
//     capabilities cannot express: pika gates both updates and deletes on
//     files.write, but a token's scopes distinguish "write" from
//     "delete". Showing delete_folder to a write-only token would be a
//     needlessly loaded gun on an LLM-driven surface, so the raw scopes
//     are consulted for the destructive tools.
//
//  2. Path patterns are enforced per tool, mirroring withPermPath on the
//     REST side, and the caller's scope is re-attached to every tool
//     invocation — the MCP SDK runs handlers on a context detached from
//     the HTTP request, so no authorization decision here relies on
//     implicit context propagation.
package mcpsrv

import (
	"context"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rakunlabs/ada/middleware/auth/identity"

	"github.com/rakunlabs/pika/internal/service"
)

// instructions is sent to the client on initialize. It is the only place an
// agent learns pika's domain vocabulary (paths, variants, versions,
// inheritance, external resources) before it starts calling tools, so it is
// worth more than any individual tool description.
const instructions = `pika is a configuration server. Configurations live at slash-separated paths (e.g. "team-a/service/config.yaml") inside a folder tree.

Key concepts:
- Version: every write creates a new immutable version. Reads default to the latest.
- Variant: an environment overlay stored beside a config under the same path (e.g. variant "prod"). Omit the variant to address the base config.
- Inheritance: a config's meta may inherit fields from other configs or from external resources. "get_config" returns the raw stored content; "get_resolved_config" returns the final value after inheritance, templating and format conversion — that is what a consuming application actually receives.
- External resource: a named, pre-configured backend (Vault, Consul, etcd, Kubernetes, AWS, GCP, Azure, HTTP). Entries are addressed by resource name + path.

Guidance:
- Start with "search_configs" or "list_folder" to locate a path; do not guess paths.
- Use "get_resolved_config" when you need the effective value, "get_config" when you need to edit the stored source.
- "set_config" always creates a new version. Pass expected_version (from "get_config") to fail instead of overwriting a concurrent change.
- Tools you cannot see are tools your credentials do not permit.`

// Handler serves the MCP endpoint. Construct once at boot and mount it on
// the authenticated mux; it is safe for concurrent use.
type Handler struct {
	svc     *service.Service
	impl    *mcp.Implementation
	srvOpts *mcp.ServerOptions
	http    http.Handler
}

// New builds the MCP handler. name/version identify the server to clients
// and should come from the build-stamped Info so an agent can tell which
// pika it is talking to.
func New(svc *service.Service, name, version string) *Handler {
	h := &Handler{
		svc:  svc,
		impl: &mcp.Implementation{Name: name, Version: version, Title: "pika configuration server"},
		srvOpts: &mcp.ServerOptions{
			Instructions: instructions,
		},
	}

	h.http = mcp.NewStreamableHTTPHandler(h.serverFor, &mcp.StreamableHTTPOptions{
		// Stateless: no Mcp-Session-Id, no server-side session map. Each
		// POST is authorized on its own credentials by the middleware
		// above us, which is exactly the property we want — an MCP
		// session must never outlive the credential that opened it.
		Stateless: true,
		// Plain JSON responses rather than SSE framing. Every tool here
		// returns a single result; SSE would add framing for no benefit
		// and interacts badly with proxies that buffer.
		JSONResponse: true,
		// Requests reach this handler through pika's own auth middleware
		// and the server is not a localhost-only dev tool, so the SDK's
		// DNS-rebinding guard (which rejects loopback requests carrying a
		// non-loopback Host header) would only produce false positives
		// behind an ingress or a docker port mapping.
		DisableLocalhostProtection: true,
	})

	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.http.ServeHTTP(w, r)
}

// serverFor builds the MCP server for one request, populated only with the
// tools the caller is allowed to use.
func (h *Handler) serverFor(r *http.Request) *mcp.Server {
	scope := snapshotScope(r.Context())

	srv := mcp.NewServer(h.impl, h.srvOpts)
	h.registerConfigTools(srv, scope)
	h.registerExternalTools(srv, scope)

	return srv
}

// Operations in pika's token scope vocabulary. Session callers ignore
// these — their capability alone decides — but a token is scoped per
// operation, so every tool declares which one it performs.
const (
	opRead   = "read"
	opWrite  = "write"
	opDelete = "delete"
)

// authScope is the frozen authorization context of a single HTTP request.
//
// It is captured before handing control to the MCP SDK because the SDK
// runs tool handlers on a context detached from the HTTP request.
// Everything a tool needs in order to be correctly attributed and
// correctly restricted lives here.
type authScope struct {
	username string
	userID   string
	caps     service.Capabilities
	patterns service.CapabilityPatterns

	// tokenScopes is non-nil when the caller is an API token. Capability
	// resolution already folded these into caps and patterns; they are
	// kept for the one decision capabilities cannot express, namely
	// whether the token may delete as well as write.
	tokenScopes []service.TokenScope
}

func snapshotScope(ctx context.Context) authScope {
	return authScope{
		username:    service.UserFromContext(ctx),
		userID:      service.UserIDFromContext(ctx),
		caps:        service.CapabilitiesFromContext(ctx),
		patterns:    service.CapabilityPatternsFromContext(ctx),
		tokenScopes: service.TokenScopesFromIdentity(identity.FromContext(ctx)),
	}
}

// apply re-attaches the snapshotted scope to a tool handler's context so
// the service layer sees the same caller it would have seen on a REST call
// — including the audit author stamped into version history and hook
// events, and the capabilities consulted when resolving inheritance from
// a path the caller may not be allowed to read.
func (a authScope) apply(ctx context.Context) context.Context {
	ctx = service.WithCapabilities(ctx, a.caps)
	ctx = service.WithCapabilityPatterns(ctx, a.patterns)
	ctx = service.WithUserInfo(ctx, a.username, a.userID)

	return ctx
}

// has reports whether the caller may use a tool gated on the given
// capability and operation.
//
// The capability is the gate the REST routes use and is authoritative.
// The operation refines it for token callers only: pika maps both
// "write" and "delete" scopes onto files.write, so without this a
// write-only token would be offered the delete tools.
func (a authScope) has(capability, operation string) bool {
	if !a.caps.Has(capability) {
		return false
	}

	if len(a.tokenScopes) == 0 {
		return true
	}

	for _, scope := range a.tokenScopes {
		for _, op := range scope.Operations {
			if op == operation || op == "*" {
				return true
			}
		}
	}

	return false
}

// allowPath enforces the caller's path restrictions for one concrete
// path. Mirrors api.withPermPath.
func (a authScope) allowPath(capability, operation, path string) error {
	if !a.has(capability, operation) {
		return fmt.Errorf("capability %q is required for this operation", capability)
	}

	if a.patterns.Allows(capability, path) {
		return nil
	}

	return fmt.Errorf("path %q is not permitted for %q", path, capability)
}

// allowAncestorPath is the listing variant of allowPath: a caller scoped
// to `team-a/**` must still be able to enumerate “ and `team-a` to reach
// what they can read, or they have no way to discover their own subtree.
func (a authScope) allowAncestorPath(capability, operation, path string) error {
	if !a.has(capability, operation) {
		return fmt.Errorf("capability %q is required for this operation", capability)
	}

	if a.patterns.AllowsAncestor(capability, path) {
		return nil
	}

	return fmt.Errorf("path %q is not permitted for %q", path, capability)
}

// allowResult is the filtering form used on search results and folder
// listings, where a denied path must be dropped silently rather than
// reported — telling the caller "you may not see team-b/secrets"
// discloses that it exists.
func (a authScope) allowResult(capability, operation, path string) bool {
	return a.allowPath(capability, operation, path) == nil
}

// addTool registers one capability-gated tool.
//
// The tool is skipped entirely when the caller lacks the grant, which is
// what keeps tools/list honest. The handler signature is reduced to
// (ctx, input) -> (output, error) because none of pika's tools need the raw
// CallToolRequest or a hand-built CallToolResult: the SDK derives both the
// structured output and the text content from the returned value, and turns
// a returned error into a tool error the model can read and react to.
func addTool[In, Out any](srv *mcp.Server, scope authScope, capability, operation string, tool *mcp.Tool, fn func(context.Context, In) (Out, error)) {
	if !scope.has(capability, operation) {
		return
	}

	mcp.AddTool(srv, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := fn(scope.apply(ctx), in)
		if err != nil {
			var zero Out

			return nil, zero, err
		}

		return nil, out, nil
	})
}

func ptr[T any](v T) *T { return &v }

var (
	readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)}
	// Writes create a new version rather than overwriting, so they are
	// additive rather than destructive; they are not idempotent because
	// each call produces another version.
	additive    = &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(false)}
	destructive = &mcp.ToolAnnotations{DestructiveHint: ptr(true), IdempotentHint: true, OpenWorldHint: ptr(false)}
)
