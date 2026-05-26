// Package registry implements pika's artifact registry feature:
// multi-tenant Go module proxy, NPM registry and Docker/OCI image
// registry sharing the same storage backbone, auth model, and admin
// UI.
//
// # Architecture
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
// # Routing
//
// All registry traffic lives under /registries/{namespace}/{repo}.
// The path past that prefix is type-specific (Go: @v/list, NPM:
// /-/v1/search, Docker: /v2/...). The Manager owns route registration
// and hot-reloads the routing table whenever Settings.Registry
// changes (see internal/server/api postSettings reload path).
//
// # Auth
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
	"github.com/rakunlabs/pika/internal/registry/events"
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
	// Type is "go" | "npm" | "docker" | "helm".
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

	// Resolver resolves raw:// and config:// references to plaintext
	// values. Used by remote repository auth (Password/Token fields)
	// so upstream credentials can live outside the registry settings
	// tree.
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

	// Emitter publishes semantic lifecycle events (publish,
	// delete, GC, cache purge) to pika's hook dispatcher. Optional
	// — implementations should funnel through events.EmitSafe so a
	// nil Emitter is a clean no-op.
	Emitter events.Emitter
}

// SecretResolver resolves raw:// and config:// references to their
// underlying plaintext. Defined as an interface (rather than a
// concrete pointer) so the server package can supply the implementation
// without registry having to import server internals.
type SecretResolver interface {
	ResolveSecret(ctx context.Context, value string) (string, error)
}

// PurgeOptions controls the scope of a cache purge. Optional;
// concrete registries decide which fields apply to them.
type PurgeOptions struct {
	// All requests a deep purge: remote-cached blobs, manifests
	// and tarballs are deleted in addition to the mutable
	// pointers (@v/list, packuments, floating tag pointers).
	// Default (All=false) is the conservative "mutable only"
	// scope — operators clear stale tag pointers without
	// triggering a full re-download.
	All bool
}

// PurgeStats is the post-purge accounting the handler surfaces to
// the operator. Zero values mean "nothing matched"; an empty Errors
// slice means "completed cleanly".
type PurgeStats struct {
	PurgedFiles int   `json:"purged_files"`
	PurgedBytes int64 `json:"purged_bytes"`
	// Skipped counts files the purge intentionally left in place
	// (e.g. immutable content under conservative scope).
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors,omitempty"`
}

// CachePurger is the optional interface a Registry implementation
// implements when it supports operator-triggered cache invalidation.
// Local registries typically return ErrNotSupported because their
// content is the source of truth; Remote and Virtual registries
// hold cached upstream data that benefits from a manual refresh.
//
// The handler in internal/server/api/registry.go type-asserts to
// this interface and exposes the POST .../purge endpoint only when
// the assertion succeeds.
type CachePurger interface {
	PurgeCache(ctx context.Context, opts PurgeOptions) (PurgeStats, error)
}

// CDNAssetRequest describes one package file request for CDN-style
// delivery surfaces. Version is the requested version or dist-tag;
// an empty Version means the protocol implementation should resolve
// its normal latest tag. Path is the file path inside the package
// archive, never the backing registry storage path.
type CDNAssetRequest struct {
	Package string
	Version string
	Path    string
}

// CDNAssetProvider is an optional interface implemented by registries
// that can expose package files directly for jsDelivr-like delivery.
// The registry owns protocol-specific version/tag resolution and
// cache headers; callers only parse the public CDN URL and dispatch.
type CDNAssetProvider interface {
	ServeCDNAsset(w http.ResponseWriter, r *http.Request, asset CDNAssetRequest)
}

// Stats is the response shape for GET .../stats. Fields are
// type-specific and zero when the underlying registry doesn't
// produce a value (e.g. a Go repo never has TagCount).
//
// All counts are exact at the time of the call; pika doesn't keep
// running counters between requests — every stats query walks the
// underlying storage. This keeps the design free of persistent
// counter state at the cost of an O(N) walk per request. N is
// typically small (10s of modules / hundreds of blobs), so the
// trade-off is comfortable; large operators can layer their own
// Prometheus collector on top later.
type Stats struct {
	BlobCount  int   `json:"blob_count,omitempty"`
	TotalBytes int64 `json:"total_bytes,omitempty"`
	// Type-specific counters. The handler unpacks them by
	// inspecting the Registry's Type() / Kind() so the UI sees a
	// single shape per protocol.
	ModuleCount     int `json:"module_count,omitempty"`     // Go
	VersionCount    int `json:"version_count,omitempty"`    // Go / NPM
	PackageCount    int `json:"package_count,omitempty"`    // NPM
	RepositoryCount int `json:"repository_count,omitempty"` // Docker
	TagCount        int `json:"tag_count,omitempty"`        // Docker
	ManifestCount   int `json:"manifest_count,omitempty"`   // Docker
}

// StatsProvider is the optional interface a Registry implements
// when it can report on-disk statistics. Every concrete Local /
// Remote implementation in pika supplies one; Virtual delegates to
// members or returns zeros (each member can be queried directly).
type StatsProvider interface {
	Stats(ctx context.Context) (Stats, error)
}

// PackageDetail is the response shape for per-package detail
// queries (GET /api/v1/registries/{type}/{ns}/{repo}/packages/{name}).
// The polymorphic payload mirrors the registry types pika supports:
// exactly one protocol-specific field is populated per response
// based on Type. This keeps a single endpoint while preserving the
// distinct metadata vocabulary each protocol uses.
//
// Fields are intentionally large here because the detail view is
// the UI's primary surface for discovery — every reasonable
// metadata bit a developer might want is included up-front so the
// UI doesn't need to fan out into multiple secondary requests for
// a single package's overview screen.
type PackageDetail struct {
	Type   string               `json:"type"`             // "npm" | "go" | "docker" | "helm" | "maven" | "pypi" | "cargo"
	Name   string               `json:"name"`             // canonical package / module / image / chart name
	NPM    *NPMPackageDetail    `json:"npm,omitempty"`    // populated when Type == "npm"
	Go     *GoModuleDetail      `json:"go,omitempty"`     // populated when Type == "go"
	Docker *DockerRepoDetail    `json:"docker,omitempty"` // populated when Type == "docker"
	Helm   *HelmChartDetail     `json:"helm,omitempty"`   // populated when Type == "helm"
	Maven  *MavenArtifactDetail `json:"maven,omitempty"`
	PyPI   *PyPIPackageDetail   `json:"pypi,omitempty"`
	Cargo  *CargoCrateDetail    `json:"cargo,omitempty"`
}

// NPMPackageDetail carries the rich metadata surfaced for one NPM
// package. Versions are listed newest-first (semver-sorted when
// possible, lex fallback). Every field except Name is optional —
// a freshly-published package may not have keywords / repository /
// homepage if the publisher omitted them.
type NPMPackageDetail struct {
	// LatestVersion is the version in the "latest" dist-tag, or
	// the highest-sorted version when no dist-tag is set.
	LatestVersion string `json:"latest_version,omitempty"`
	// Description, Homepage, License, Repository, Bugs are pulled
	// from the latest version's package.json. They're top-level
	// here so the UI doesn't have to traverse Versions[latest] to
	// render the package card.
	Description string         `json:"description,omitempty"`
	Homepage    string         `json:"homepage,omitempty"`
	License     string         `json:"license,omitempty"`
	Repository  map[string]any `json:"repository,omitempty"`
	Bugs        map[string]any `json:"bugs,omitempty"`
	Keywords    []string       `json:"keywords,omitempty"`
	Author      map[string]any `json:"author,omitempty"`
	Maintainers []any          `json:"maintainers,omitempty"`
	// DistTags is the full tag→version map (latest, beta, next, …).
	DistTags map[string]string `json:"dist_tags,omitempty"`
	// HasReadme is true when a README is available for at least
	// the latest version. The UI uses this to decide whether to
	// render a "README" tab; the README itself is served from a
	// dedicated sub-endpoint so the package detail payload stays
	// small.
	HasReadme bool `json:"has_readme,omitempty"`
	// Versions is the per-version detail, sorted newest-first.
	Versions []NPMVersionDetail `json:"versions,omitempty"`
}

// NPMVersionDetail is one row in NPMPackageDetail.Versions.
type NPMVersionDetail struct {
	Version          string         `json:"version"`
	PublishedAt      string         `json:"published_at,omitempty"` // RFC3339 from publish-time stamp
	Size             int64          `json:"size,omitempty"`         // tarball bytes if known
	Integrity        string         `json:"integrity,omitempty"`    // dist.integrity (sha512-…)
	Shasum           string         `json:"shasum,omitempty"`       // legacy dist.shasum
	TarballURL       string         `json:"tarball_url,omitempty"`  // public tarball URL (rewritten)
	Dependencies     map[string]any `json:"dependencies,omitempty"`
	DevDependencies  map[string]any `json:"dev_dependencies,omitempty"`
	PeerDependencies map[string]any `json:"peer_dependencies,omitempty"`
	Engines          map[string]any `json:"engines,omitempty"`
	Deprecated       string         `json:"deprecated,omitempty"` // non-empty = yanked with reason
}

// GoModuleDetail carries per-module metadata for a Go module.
type GoModuleDetail struct {
	// LatestVersion is the semver-highest version present.
	LatestVersion string `json:"latest_version,omitempty"`
	// Versions sorted newest-first by semver when possible.
	Versions []GoVersionDetail `json:"versions,omitempty"`
}

// GoVersionDetail is one row in GoModuleDetail.Versions.
type GoVersionDetail struct {
	Version             string `json:"version"`
	PublishedAt         string `json:"published_at,omitempty"` // RFC3339 from .info Time
	Retracted           bool   `json:"retracted,omitempty"`    // parsed from go.mod retract directive
	RetractionRationale string `json:"retraction_rationale,omitempty"`
	GoModSize           int64  `json:"gomod_size,omitempty"` // bytes of .mod file
	ZipSize             int64  `json:"zip_size,omitempty"`   // bytes of .zip file
}

// DockerRepoDetail surfaces per-image tag detail with layer-level
// breakdowns. The MVP only resolves layers for tags the operator
// hits via the detail endpoint — listing the whole catalogue does
// not pre-fetch every manifest.
type DockerRepoDetail struct {
	Tags []DockerTagDetail `json:"tags,omitempty"`
}

// DockerTagDetail mirrors dockerTagSummary but with manifest-level
// detail (config digest, layer list, platform array for multi-arch
// manifest lists).
type DockerTagDetail struct {
	Tag          string `json:"tag"`
	Digest       string `json:"digest,omitempty"`
	MediaType    string `json:"media_type,omitempty"`
	ArtifactType string `json:"artifact_type,omitempty"`
	// ManifestSize is the size of the manifest JSON document.
	ManifestSize int64 `json:"manifest_size,omitempty"`
	// ImageSize is the sum of layer sizes (image-on-disk size).
	// Zero when the manifest could not be parsed (e.g. unknown
	// artifact format) or when this entry describes a manifest
	// list (use the per-platform entries instead).
	ImageSize    int64            `json:"image_size,omitempty"`
	ConfigDigest string           `json:"config_digest,omitempty"`
	Layers       []DockerLayer    `json:"layers,omitempty"`
	Platforms    []DockerPlatform `json:"platforms,omitempty"` // populated for manifest lists
}

// DockerLayer is one row in DockerTagDetail.Layers.
type DockerLayer struct {
	Digest    string `json:"digest"`
	Size      int64  `json:"size,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

// DockerPlatform is one row in DockerTagDetail.Platforms (used for
// manifest lists / multi-arch images).
type DockerPlatform struct {
	OS           string `json:"os,omitempty"`
	Architecture string `json:"architecture,omitempty"`
	Variant      string `json:"variant,omitempty"`
	Digest       string `json:"digest,omitempty"`
	Size         int64  `json:"size,omitempty"`
}

// HelmChartDetail surfaces Chart.yaml-derived metadata for a Helm
// chart. Used by the helm registry type (B3); included in the
// shared shape so the UI can dispatch on Type uniformly.
type HelmChartDetail struct {
	LatestVersion string              `json:"latest_version,omitempty"`
	Description   string              `json:"description,omitempty"`
	Icon          string              `json:"icon,omitempty"`
	AppVersion    string              `json:"app_version,omitempty"`
	Keywords      []string            `json:"keywords,omitempty"`
	Maintainers   []any               `json:"maintainers,omitempty"`
	HasReadme     bool                `json:"has_readme,omitempty"`
	Versions      []HelmVersionDetail `json:"versions,omitempty"`
}

// HelmVersionDetail is one row in HelmChartDetail.Versions.
type HelmVersionDetail struct {
	Version     string   `json:"version"`
	AppVersion  string   `json:"app_version,omitempty"`
	Description string   `json:"description,omitempty"`
	Created     string   `json:"created,omitempty"`
	Digest      string   `json:"digest,omitempty"`
	Size        int64    `json:"size,omitempty"`
	URLs        []string `json:"urls,omitempty"`
}

// MavenArtifactDetail surfaces Maven GAV metadata derived from the
// repository layout and cached maven-metadata.xml files.
type MavenArtifactDetail struct {
	GroupID       string               `json:"group_id,omitempty"`
	ArtifactID    string               `json:"artifact_id,omitempty"`
	LatestVersion string               `json:"latest_version,omitempty"`
	Versions      []MavenVersionDetail `json:"versions,omitempty"`
}

type MavenVersionDetail struct {
	Version string `json:"version"`
	JarSize int64  `json:"jar_size,omitempty"`
	PomSize int64  `json:"pom_size,omitempty"`
}

// PyPIPackageDetail carries package/version rows for a PEP 503
// Simple API repository.
type PyPIPackageDetail struct {
	LatestVersion string              `json:"latest_version,omitempty"`
	Versions      []PyPIVersionDetail `json:"versions,omitempty"`
}

type PyPIVersionDetail struct {
	Version  string   `json:"version"`
	Files    []string `json:"files,omitempty"`
	FileSize int64    `json:"file_size,omitempty"`
}

// CargoCrateDetail carries sparse-index crate versions.
type CargoCrateDetail struct {
	LatestVersion string               `json:"latest_version,omitempty"`
	Versions      []CargoVersionDetail `json:"versions,omitempty"`
}

type CargoVersionDetail struct {
	Version string `json:"version"`
	Yanked  bool   `json:"yanked,omitempty"`
	CKSum   string `json:"cksum,omitempty"`
	Size    int64  `json:"size,omitempty"`
}

// UpstreamHealth is the response shape for the operator-triggered
// "test upstream" probe. Only Remote kinds implement the
// underlying interface; Local / Virtual return 400 from the HTTP
// handler.
type UpstreamHealth struct {
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code,omitempty"`
	LatencyMS  int64  `json:"latency_ms,omitempty"`
	URL        string `json:"url,omitempty"`
	Error      string `json:"error,omitempty"`
	// BodyPreview is the first ~256 bytes of the response (HTML
	// error pages from misconfigured proxies are useful diagnostic
	// signal). Trimmed to keep the payload small.
	BodyPreview string `json:"body_preview,omitempty"`
}

// UpstreamProber is the optional interface implemented by Remote
// kinds that can run a connectivity check against their
// configured upstream. The probe uses whatever auth + TLS config
// the live registry is using, so the result reflects what an
// actual client request would see.
type UpstreamProber interface {
	ProbeUpstream(ctx context.Context) (UpstreamHealth, error)
}

// PackageDetailer is the optional interface a Registry implements
// when it can resolve detailed per-package metadata. Local
// registries always implement it (they own the source bytes);
// Remote registries implement it against their cache; Virtual
// registries delegate to the first member that has the package.
//
// The name argument is the canonical package / module / image
// name (NPM "@scope/pkg", Go "example.com/foo", Docker
// "library/nginx", Helm "my-chart"). Implementations return
// (nil, ErrPackageNotFound) when the name does not exist.
type PackageDetailer interface {
	PackageDetail(ctx context.Context, name string) (*PackageDetail, error)
}

// ErrPackageNotFound is the sentinel returned by PackageDetailer
// implementations when the requested package does not exist in
// this registry. Callers wrap with service.ErrNotFound for 404
// mapping at the HTTP boundary.
var ErrPackageNotFound = errSentinel("package not found")

// ErrInvalidPackageName is the sentinel returned by PackageDetailer
// implementations when the caller-supplied name is malformed for
// the protocol (e.g. an empty NPM package name, a Go module path
// that fails ValidateModulePath). Distinct from ErrPackageNotFound
// because the HTTP layer maps it to 400 instead of 404 — the
// request is malformed, not just unsatisfied.
var ErrInvalidPackageName = errSentinel("invalid package name")

// errSentinel is a string-backed error type used to define package
// sentinels without introducing an errors-package dep at this
// layer.
type errSentinel string

func (e errSentinel) Error() string { return string(e) }
