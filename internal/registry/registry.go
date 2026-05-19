// Package registry implements pika's artifact registry feature:
// multi-tenant Go module proxy, NPM registry and Docker/OCI image
// registry sharing the same storage backbone, auth model, and admin
// UI.
//
// Architecture
//
// A pika installation carries N "namespaces" (logical tenants). Each
// namespace owns M "repositories"; a repository has a Type (go|npm|
// docker) and a Kind (local|remote|virtual):
//
//   - local   — artifacts stored in a user-selected RawMount under a
//     per-repo base path. Reads and (optionally) writes are served
//     against the BlobStore abstraction (internal/registry/blobstore)
//     so the same code path works on S3, local disk, SFTP, WebDAV,
//     Vercel Blob and FTP without per-backend conditionals.
//
//   - remote  — pull-through proxy of an upstream URL (e.g.
//     proxy.golang.org, registry.npmjs.org, registry-1.docker.io).
//     Fetched artifacts are cached in pika storage; subsequent reads
//     are served from cache. Mutable upstream responses (latest
//     version, dist-tags) honour a per-repo TTL.
//
//   - virtual — ordered aggregation of sibling local + remote repos.
//     A virtual repo exposes one URL; the request is dispatched to
//     the first member that has the artifact. Writes to virtual
//     repos are rejected — clients must address a concrete local
//     repo to publish.
//
// Routing
//
// All registry traffic lives under /registries/{namespace}/{repo}.
// The path past that prefix is type-specific (Go: @v/list, NPM:
// /-/v1/search, Docker: /v2/...). The Manager owns route registration
// and hot-reloads the routing table whenever Settings.Registry
// changes (see internal/server/api postSettings reload path).
//
// Auth
//
// Every protected route gates on a registry capability (read/write/
// delete/admin). Token authentication is handled per-protocol by the
// common token extractor (registry/common/auth.go) so a single pika
// token can drive npm publish, docker push and GOPROXY downloads
// uniformly. Public access is intentionally not exposed in this MVP;
// every read still requires a token.
package registry

import (
	"context"
	"net/http"

	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/registry/blobstore"
	"github.com/rakunlabs/pika/internal/service"
)

// Registry is the per-(namespace, repo) handler surface exposed by
// the manager. Each concrete implementation (go local, go remote,
// go virtual, npm local, ...) implements this interface in its own
// sub-package; the manager treats them uniformly.
//
// Implementations are immutable from the caller's perspective: the
// manager builds a fresh Registry instance whenever Settings.Registry
// changes and atomically swaps the routing table. There is no in-
// place mutation API on this interface; the trade-off is a small
// rebuild cost on every settings save in exchange for lockless reads
// on the hot path.
type Registry interface {
	// Namespace and Name match the corresponding fields on the
	// RegistryRepository row that produced this Registry.
	Namespace() string
	Name() string
	// Type is "go" | "npm" | "docker".
	Type() string
	// Kind is "local" | "remote" | "virtual".
	Kind() string

	// ServeHTTP dispatches a request whose URL path has already had
	// the /registries/{namespace}/{repo} prefix stripped. The handler
	// sees a path like "/@v/list" (Go), "/lodash" (NPM) or
	// "/v2/{name}/manifests/{ref}" (Docker).
	//
	// Implementations may further branch on method / sub-path and
	// must write a response (success or error JSON) before returning.
	// Auth has already been enforced upstream; the handler should
	// only worry about its own protocol semantics.
	ServeHTTP(w http.ResponseWriter, r *http.Request)

	// Close releases any per-Registry resources (open HTTP clients,
	// cached metadata). Called by the manager when this Registry is
	// being replaced in a reload. Safe to call multiple times.
	Close() error
}

// Capability is the per-route auth requirement a handler reports to
// the manager. The manager wires the capability check before
// dispatching to ServeHTTP — handlers never re-check.
//
// Most routes are uniformly "read" or "write" within a registry, but
// a single registry may need both (e.g. NPM: GET packument = read,
// PUT publish = write). The interface lets a registry expose a per-
// method (and per-sub-path) policy without coupling to ada/middleware
// at this layer.
type CapabilityPolicy interface {
	// RequiredCap returns the capability key the manager must
	// confirm before dispatching the request. Returning "" means
	// no check (used for non-mutating probes like /v2/ version
	// challenge).
	RequiredCap(r *http.Request) string
}

// Factory builds a concrete Registry for one RegistryRepository row.
// The manager keeps a Factory per (type, kind) tuple and looks one up
// when (re)building the routing table. Each Factory is responsible
// for resolving any external dependencies it needs from Deps
// (typically: the BlobStore backing this repo's mount, or the
// upstream HTTP client for remote repos).
type Factory func(ctx context.Context, deps Deps, ns string, repo *service.RegistryRepository) (Registry, error)

// Deps is the narrow set of pika services a Registry implementation
// depends on. Defined at this layer (rather than passing *service.
// Service directly) so the package's blast radius stays small and
// the unit tests can stub deps without dragging the whole service
// graph in.
type Deps struct {
	// Svc is pika's main service. Registry implementations should
	// only touch the very small subset (Settings, secret resolver)
	// that is genuinely registry-relevant; everything else has its
	// own narrow accessor below.
	Svc *service.Service

	// Resolver resolves a "secret://..." reference to its plaintext
	// value. Used by remote repository auth (Password/Token fields)
	// so upstream credentials never live in plaintext in Settings.
	// Implementations should treat a nil Resolver as "no references
	// supported"; values without a scheme prefix are returned as-is.
	Resolver SecretResolver

	// MountFor returns a BlobStore rooted at the named raw mount's
	// backend, scoped to basePath. The store is content-addressable;
	// used by Docker (mandatory CAS) and as the dedup substrate for
	// NPM tarballs in a later phase. Errors when the mount does not
	// exist or is not writable.
	MountFor func(mount string, basePath string) (blobstore.BlobStore, error)

	// MountRawFS returns the raw filesystem handle for a named raw
	// mount. Used by protocol heads (Go module proxy, NPM with
	// classic-layout tarballs) that want path-keyed direct file IO
	// rather than CAS — module proxy files are addressed by
	// {module}/@v/{version}.{ext} and never benefit from
	// content-addressing.
	//
	// The returned rawfs is the live, hot-reload-tracked instance;
	// callers must re-fetch on settings change rather than caching
	// the handle across reloads.
	MountRawFS func(mount string) (rawfs.RawFS, error)
}

// SecretResolver resolves a value that may carry a "secret://" prefix
// to its underlying plaintext. Defined as an interface (rather than a
// concrete pointer) so the secret package can supply the
// implementation without registry having to import it.
type SecretResolver interface {
	ResolveSecret(ctx context.Context, value string) (string, error)
}
