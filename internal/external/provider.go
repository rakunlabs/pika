package external

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotSupported is returned by Provider operations that a particular
// backend can't honor (e.g. Write on AWS Secrets Manager via our
// minimal client, ListVersions on backends that don't expose versions).
// The browser UI converts this into a disabled action rather than a
// user-facing error. Wrap with %w so callers can errors.Is() it.
var ErrNotSupported = errors.New("operation not supported by this external resource")

// Provider is the unified interface every external-resource backend
// implements. Adding a new backend (e.g. HashiCorp Boundary, 1Password
// Connect, …) is now:
//
//  1. Add the typed config struct to External.
//  2. Add a *XxxProvider type in its own file under internal/external/.
//  3. Implement the five methods below.
//  4. Wire one case into ResourceProvider().
//
// Everything else — the API surface, the SPA editor, the CRUD wiring,
// the connection-test UX — is uniform and needs no changes.
//
// The Service layer holds shared client caches (Vault token renewal,
// Kubernetes REST clients, GCP JWT-signed tokens, …) and passes them
// to providers through Deps so each Provider stays a thin adapter and
// does not duplicate cache state.
type Provider interface {
	// Kind returns the short identifier used in API responses and UI
	// badges ("http", "vault", "kubernetes", …). Must be stable: the
	// SPA branches on it and the docs reference it.
	Kind() string

	// Capabilities reports which browser operations this backend
	// implements. The SPA reads it once per resource selection and
	// hides/disables buttons accordingly. Backends that report
	// CanRead=false for some operations should still return a useful
	// ErrNotSupported (not a panic) if those methods are called
	// directly via the API.
	Capabilities() Capabilities

	// Validate runs a configuration sanity check that does NOT touch
	// the network. Called before persisting a resource so the user
	// gets a synchronous error instead of a confused connection-test
	// failure later. Empty config is rejected here.
	Validate() error

	// Fetch reads configuration data from the backend at the given
	// resource-specific path and returns it as raw bytes. Used by the
	// config inheritance pipeline (the value is JSON-merged into the
	// final file). For the SPA browser, see Read instead.
	Fetch(ctx context.Context, path string) ([]byte, error)

	// Read returns a single entry at the given path as a structured
	// value the SPA can render in a key/value table. For Vault this
	// is the secret data; for K8s Secret/ConfigMap it's the decoded
	// data map; for Consul/etcd it's a single {"value": "..."} pair.
	// HTTP returns the response body as {"body": "..."}. Backends
	// that don't know how to "read a single entry" (none today, but
	// hypothetically a streaming-only source) return ErrNotSupported.
	Read(ctx context.Context, path string) (*Entry, error)

	// List enumerates available paths under the prefix. Backends that
	// have no list semantics (currently only HTTP) MUST return an
	// empty slice and nil error so callers can treat "no list" and
	// "empty list" identically.
	List(ctx context.Context, prefix string) ([]string, error)

	// Write persists a new value at the given path. Map shape is
	// backend-specific: Vault accepts arbitrary {key:value}; Consul
	// and etcd flatten to a single string value (the "value" key is
	// taken). Backends without a writable API return ErrNotSupported.
	Write(ctx context.Context, path string, data map[string]any) error

	// Delete removes the entry at the given path. Idempotent: deleting
	// a non-existent path is not an error. Backends without a deletable
	// API return ErrNotSupported.
	Delete(ctx context.Context, path string) error

	// ListVersions returns the version history for a path, newest
	// first. Today only Vault KV v2 supports this; every other backend
	// returns ErrNotSupported. The browser falls back to "single
	// version" rendering in that case.
	ListVersions(ctx context.Context, path string) ([]Version, error)

	// ReadVersion reads a specific historical version. Only meaningful
	// when ListVersions is supported. The version identifier comes
	// from the corresponding Version.ID.
	ReadVersion(ctx context.Context, path string, version string) (*Entry, error)

	// Test performs a live connectivity check using the stored
	// credentials. The result carries a single-line operator-facing
	// message and an optional sample of discovered paths.
	Test(ctx context.Context) TestResult
}

// Capabilities advertises which browser operations a Provider
// implements. The SPA toggles its action buttons based on this — a
// backend that returns CanWrite=false won't see a Save button at all.
// Capabilities are static for a given Kind, not per-record, so callers
// can cache the lookup per resource type if they ever need to.
type Capabilities struct {
	CanRead     bool `json:"can_read"`
	CanList     bool `json:"can_list"`
	CanWrite    bool `json:"can_write"`
	CanDelete   bool `json:"can_delete"`
	CanVersions bool `json:"can_versions"`
}

// Entry is a single record returned by Provider.Read. Data is the
// structured payload (typically map[string]any so the UI can render
// it as a key/value table); Raw carries the marshalled JSON for
// callers that want the verbatim bytes. ContentType is informational
// (e.g. "application/json", "text/plain") and may be empty.
type Entry struct {
	Data        map[string]any `json:"data,omitempty"`
	Raw         []byte         `json:"raw,omitempty"`
	ContentType string         `json:"content_type,omitempty"`
	// Version is the identifier of the version that was returned, if
	// the backend tracks versions. Empty otherwise.
	Version string `json:"version,omitempty"`
}

// Version is a single historical revision returned by
// Provider.ListVersions. ID is the opaque identifier that
// ReadVersion accepts back; CreatedAt is best-effort RFC3339 and
// may be empty if the backend doesn't expose it. Deleted/Destroyed
// mirror Vault KV v2 semantics — most backends will leave them
// false.
type Version struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at,omitempty"`
	Deleted   bool   `json:"deleted,omitempty"`
	Destroyed bool   `json:"destroyed,omitempty"`
}

// TestResult is the outcome of Provider.Test. OK == false means the
// backend rejected the request or was unreachable; Message contains a
// human-readable explanation that the SPA renders verbatim.
type TestResult struct {
	OK      bool     `json:"ok"`
	Message string   `json:"message,omitempty"`
	Sample  []string `json:"sample,omitempty"`
}

// Deps is the bag of shared client caches and helpers Service exposes
// to the providers. We keep it as an interface (not a concrete struct)
// so the providers don't import service, which would create an import
// cycle. service.Service satisfies this interface in service/service.go.
//
// Adding a new backend that needs a cached client only requires
// extending Deps with one more accessor — provider code can otherwise
// fabricate its own client inline (see Consul/etcd today).
type Deps interface {
	VaultClient(ctx context.Context, v *Vault) *VaultClient
	KubeClient(k *Kubernetes) (*KubeClient, error)
	GCPClient(g *GCP) (*GCPSecretManagerClient, error)
	GCPParameterClient(g *GCPParameter) (*GCPParameterManagerClient, error)
	AzureClient(a *Azure) *AzureKeyVaultClient
}

// ResourceProvider returns the concrete provider for a configured
// External record. Exactly one of the typed sub-fields must be set;
// otherwise an error is returned so misconfigured rows surface early.
//
// This is the single dispatch point: callers (service/data.go) hand
// us an External value and we hand them back something they can
// uniformly call Fetch/List/Test on.
func ResourceProvider(ext External, deps Deps) (Provider, error) {
	switch {
	case ext.Http != nil:
		return &HTTPProvider{Config: ext.Http}, nil
	case ext.Vault != nil:
		return &VaultProvider{Config: ext.Vault, Deps: deps}, nil
	case ext.Kubernetes != nil:
		return &KubernetesProvider{Config: ext.Kubernetes, Deps: deps}, nil
	case ext.Consul != nil:
		return &ConsulProvider{Config: ext.Consul}, nil
	case ext.Etcd != nil:
		return &EtcdProvider{Config: ext.Etcd}, nil
	case ext.AWS != nil:
		return &AWSProvider{Config: ext.AWS}, nil
	case ext.GCP != nil:
		return &GCPProvider{Config: ext.GCP, Deps: deps}, nil
	case ext.GCPParameter != nil:
		return &GCPParameterProvider{Config: ext.GCPParameter, Deps: deps}, nil
	case ext.Azure != nil:
		return &AzureProvider{Config: ext.Azure, Deps: deps}, nil
	default:
		return nil, fmt.Errorf("external resource has no configured backend")
	}
}

// Kind returns the same identifier ResourceProvider().Kind() would
// produce, without constructing a Provider. Useful for callers that
// only need to label a record (e.g. list endpoints).
func Kind(ext External) string {
	switch {
	case ext.Http != nil:
		return "http"
	case ext.Vault != nil:
		return "vault"
	case ext.Kubernetes != nil:
		return "kubernetes"
	case ext.Consul != nil:
		return "consul"
	case ext.Etcd != nil:
		return "etcd"
	case ext.AWS != nil:
		return "aws"
	case ext.GCP != nil:
		return "gcp"
	case ext.GCPParameter != nil:
		return "gcp-parameter"
	case ext.Azure != nil:
		return "azure"
	default:
		return "unknown"
	}
}

// capSample trims a slice of discovered paths to a UI-friendly length.
// Providers use this in Test() so the API response stays small.
func capSample(paths []string, n int) []string {
	if len(paths) <= n {
		return paths
	}
	return paths[:n]
}
