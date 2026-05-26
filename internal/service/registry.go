package service

// Registry settings model — namespace + repository tree configured via UI.
//
// Pika's artifact registry feature is multi-tenant by design: a single
// deployment carries N "namespaces" (logical tenants / teams), and each
// namespace holds M "repositories". A repository has a type
// (go|npm|docker|helm|maven|pypi|cargo) and a kind
// (local|remote|virtual). The triple
// {namespace, repo-name, type} uniquely identifies a serving surface:
//
//   /registries/{namespace}/{repo}/{type-specific-path}
//
// Local repositories store artifacts in a user-selected RawMount under
// a per-repo base path. Remote repositories proxy an upstream URL with
// pull-through caching. Virtual repositories aggregate a list of other
// repos (local + remote) and expose them under one endpoint with a
// configurable lookup order.
//
// Why this lives in the service package (not internal/registry):
//
//   - The Settings row already lives here. Registry config is just
//     another sub-tree of Settings (alongside RawMounts, Hooks,
//     ProxyServers), so keeping the model definition adjacent avoids
//     an import cycle between service and the registry runtime.
//
//   - The HTTP layer and the registry runtime both need to read these
//     structs. Putting them in service keeps the dependency arrow
//     pointing in one direction (registry -> service, not the other
//     way around).
//
//   - Secret values inside RegistrySettings (upstream credentials)
//     piggyback on the existing settings_seal.go sealed-payload
//     mechanism — see internal/secret/settings_seal.go for the
//     extraction/injection pattern.

// RegistrySettings is the root of the registry config. It is referenced
// from Settings (settings.go) as an optional pointer so installations
// that don't use the feature carry zero bytes for it.
type RegistrySettings struct {
	// Disabled is the deployment-wide feature flag for the artifact
	// registry. Same shape and semantics as VaultSettings.Disabled
	// and ProxySettings.Disabled (zero-value = enabled, preserving
	// backward compatibility with pre-flag rows). When true:
	//
	//   - The SPA hides the Registries link from navigation.
	//   - /api/v1/registries/* admin endpoints respond 404.
	//   - The data plane /registries/{ns}/{repo}/... refuses every
	//     request with 404 — package managers see "not configured".
	//   - Existing namespace / repository configuration is preserved;
	//     flipping the flag back makes them serve again.
	//
	// This is a feature-flag, not a destructive action: no on-disk
	// artifacts are touched.
	Disabled bool `json:"disabled,omitempty"`

	// Namespaces is the ordered list of configured tenants. The order
	// is preserved across Save so the UI can present a stable list;
	// it carries no semantic meaning beyond display order.
	//
	// Pika auto-creates a namespace named "default" on first boot
	// (see internal/registry.bootstrap) so a fresh install has
	// somewhere to attach repositories without an extra setup step.
	Namespaces []RegistryNamespace `json:"namespaces,omitempty"`
}

// RegistryNamespace groups repositories. The name is the URL path
// segment ("/registries/{name}/...") and must be unique across the
// installation. Allowed characters: lowercase alphanumerics, hyphen,
// underscore — matches the constraints of every registry protocol
// we support (NPM scope-less names, Docker repo names, Go module
// path segments).
type RegistryNamespace struct {
	Name         string               `json:"name"`
	Description  string               `json:"description,omitempty"`
	Repositories []RegistryRepository `json:"repositories,omitempty"`
}

// RegistryRepository is one repo inside a namespace. The Type field
// determines which protocol handler will serve it; Kind determines
// the backing mode (local storage / remote proxy / virtual
// aggregation).
//
// Field validity by Kind:
//
//	local   -> Mount + BasePath required, AllowPush honored
//	remote  -> URL + cache Mount/BasePath required, Auth optional (raw:// / config:// refs)
//	virtual -> Members required, ordered list of sibling repo names
//
// The validator (Validate, below) rejects rows that mix fields across
// kinds so the runtime can trust the shape without re-checking.
type RegistryRepository struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type"` // "go" | "npm" | "docker" | "helm" | "maven" | "pypi" | "cargo"
	Kind        string `json:"kind"` // "local" | "remote" | "virtual"

	// Local fields
	Mount     string `json:"mount,omitempty"`      // raw mount prefix
	BasePath  string `json:"base_path,omitempty"`  // e.g. "go/" or "npm/"
	AllowPush bool   `json:"allow_push,omitempty"` // publish/push enabled

	// Remote fields
	URL  string                `json:"url,omitempty"`  // upstream base URL
	Auth *RegistryUpstreamAuth `json:"auth,omitempty"` // optional
	// MutableTTL controls how long mutable upstream responses
	// (latest tag, version list) are cached before being refetched.
	// Empty -> default per registry type (e.g. 5m for Go @latest).
	// Format is a Go duration string ("5m", "1h", "0s" to disable).
	MutableTTL string `json:"mutable_ttl,omitempty"`
	// FloatingTags is the operator-provided list of Docker tag
	// names that should be treated as mutable — i.e. the tag →
	// digest pointer is re-resolved through upstream every TTL
	// window. Tags not in this list are cached forever after the
	// first successful resolve.
	//
	// This carves out the common semver convention: tags like
	// "v1.2.3", "1.2", "2024-05-19" are by convention immutable,
	// but pika has no way to know that ahead of time. The operator
	// declares the floaters explicitly.
	//
	// Empty list ⇒ default ("latest", "main", "master", "dev",
	// "develop", "nightly", "edge", "stable", "canary"). Set to a
	// single-element list ["*"] to force every tag to be
	// TTL-bounded (the pre-FloatingTags behaviour, useful for
	// upstreams that overwrite semver tags in flight).
	//
	// Only honored by Docker Remote — Go and NPM have a dedicated
	// @latest / packument endpoint so the floater is already
	// isolated and this list is silently ignored for them.
	FloatingTags []string `json:"floating_tags,omitempty"`
	// InsecureSkipVerify disables TLS cert verification for the
	// upstream. Off by default; only useful for internal mirrors
	// with self-signed certs.
	InsecureSkipVerify bool `json:"insecure_skip_verify,omitempty"`

	// Virtual fields
	// Members is an ordered list of sibling repository names (within
	// the same namespace) to aggregate. Lookup tries members in order
	// on every request; the first match wins. Writes to a virtual
	// repo are rejected — clients must address a concrete local repo.
	Members []string `json:"members,omitempty"`
	// DefaultLocal is an optional hint for the UI / clients about
	// which local member should receive writes. Not enforced by the
	// runtime.
	DefaultLocal string `json:"default_local,omitempty"`

	// Common per-repo overrides
	CORSOrigins   []string        `json:"cors_origins,omitempty"`
	MaxUploadSize int64           `json:"max_upload_size,omitempty"` // bytes; 0 = type default
	Policy        *RegistryPolicy `json:"policy,omitempty"`
}

// RegistryPolicy carries optional per-repository operational policy.
// The first enforced fields target Docker Local repositories because
// they already have mutable tags and an explicit GC flow. The struct
// is intentionally protocol-neutral so later phases can extend it to
// Go/NPM/Helm retention without changing the settings shape.
type RegistryPolicy struct {
	// ImmutableTags is a list of Docker tag patterns that may be created
	// once but cannot be moved to another digest or deleted. Patterns use
	// shell-style matching (`v*`, `prod`, `release-*`) against one tag.
	ImmutableTags []string                 `json:"immutable_tags,omitempty"`
	Retention     *RegistryRetentionPolicy `json:"retention,omitempty"`
}

// RegistryRetentionPolicy stores default cleanup windows for the
// manual Docker GC estimate/apply buttons. Operators can still override
// these per request; the policy controls the repo's normal defaults.
type RegistryRetentionPolicy struct {
	// GCMinAgeSeconds protects recently-written unreferenced blobs and
	// manifests from GC. Zero means use the server default.
	GCMinAgeSeconds int64 `json:"gc_min_age_seconds,omitempty"`
	// AbandonedUploadMaxAgeSeconds controls stale upload tmp pruning.
	// Zero means use the server default.
	AbandonedUploadMaxAgeSeconds int64 `json:"abandoned_upload_max_age_seconds,omitempty"`
}

// RegistryUpstreamAuth describes how to authenticate to an upstream
// registry when proxying remote repositories. The Username / Password /
// Token / Value fields support direct "raw://mount/path" and "config://key"
// references, plus nested scalar selectors such as
// "config://secrets/app#/registry/token" or
// "raw://vault/secrets.yaml#/registry/token". References are resolved
// at runtime instead of being sent upstream literally. The wrapper
// layer (internal/secret/settings_seal.go) extracts and seals plaintext
// password, token and header value fields automatically.
type RegistryUpstreamAuth struct {
	Type     string `json:"type"` // "basic" | "bearer" | "header"
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"` // basic
	Token    string `json:"token,omitempty"`    // bearer
	Header   string `json:"header,omitempty"`   // header
	Value    string `json:"value,omitempty"`    // header
}

// Registry type / kind constants. Defined as strings (not enum-like
// types) because the JSON shape uses bare strings and re-tagging
// everywhere would be churn for no win.
const (
	RegistryTypeGo     = "go"
	RegistryTypeNPM    = "npm"
	RegistryTypeDocker = "docker"
	// RegistryTypeHelm is the pure-HTTP Helm chart repository
	// (`index.yaml` + `{chart}-{version}.tgz`). Distinct from
	// pushing Helm charts as OCI artifacts into a Docker registry;
	// the type is reserved for the classic Helm protocol so a
	// `helm repo add` against pika just works. The data plane
	// optionally accepts ChartMuseum-style writes (POST /api/charts)
	// for ergonomic publishing.
	RegistryTypeHelm  = "helm"
	RegistryTypeMaven = "maven"
	RegistryTypePyPI  = "pypi"
	RegistryTypeCargo = "cargo"

	RegistryKindLocal   = "local"
	RegistryKindRemote  = "remote"
	RegistryKindVirtual = "virtual"

	RegistryAuthBasic  = "basic"
	RegistryAuthBearer = "bearer"
	RegistryAuthHeader = "header"
)

// KnownRegistryTypes is the canonical allowlist of registry types
// pika ships with. The validator and the factory wiring both read
// this slice — adding a new protocol means a single insertion here
// (plus the factory registration in registry_wire.go).
var KnownRegistryTypes = []string{
	RegistryTypeGo,
	RegistryTypeNPM,
	RegistryTypeDocker,
	RegistryTypeHelm,
	RegistryTypeMaven,
	RegistryTypePyPI,
	RegistryTypeCargo,
}

// IsKnownRegistryType reports whether t is one of the registered
// protocol types. Used by the validator and by any future code path
// that needs to whitelist registry types without hard-coding the
// switch.
func IsKnownRegistryType(t string) bool {
	for _, k := range KnownRegistryTypes {
		if t == k {
			return true
		}
	}
	return false
}

// DefaultRegistryNamespace is the name of the namespace pika auto-
// creates on first boot when RegistrySettings is empty. The bootstrap
// is non-destructive: if any namespace exists already (including one
// named "default" that the operator deleted then recreated), the
// bootstrap step does nothing.
const DefaultRegistryNamespace = "default"
