package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/blobstore"
	"github.com/rakunlabs/pika/internal/registry/cargo"
	"github.com/rakunlabs/pika/internal/registry/common"
	"github.com/rakunlabs/pika/internal/registry/docker"
	"github.com/rakunlabs/pika/internal/registry/events"
	"github.com/rakunlabs/pika/internal/registry/goproxy"
	"github.com/rakunlabs/pika/internal/registry/helm"
	"github.com/rakunlabs/pika/internal/registry/maven"
	"github.com/rakunlabs/pika/internal/registry/npm"
	"github.com/rakunlabs/pika/internal/registry/pypi"
	"github.com/rakunlabs/pika/internal/service"
)

// storeFromRegistry extracts the underlying protocol-specific
// store from a Registry instance via structural typing. Each
// protocol's Local / Remote exposes a `Store() *T` accessor with
// the same shape; Virtual kinds don't carry a store of their own
// and return ok=false.
//
// Callers parameterise T with the concrete store type, e.g.:
//
//	store, ok := storeFromRegistry[goproxy.Store](reg)
func storeFromRegistry[T any](r registry.Registry) (*T, bool) {
	type storer interface{ Store() *T }
	if s, ok := r.(storer); ok {
		return s.Store(), true
	}
	return nil, false
}

// resolveRegistry folds the boilerplate every admin/read handler
// shares: feature gate, manager-nil check, path extraction,
// lookup, and an optional registry-type guard.
//
// Pass requiredType="" to skip the type check (e.g. for the
// generic detail/probe/stats endpoints which accept any type).
// Pass a concrete service.RegistryType* constant to enforce it
// (e.g. Docker GC requires RegistryTypeDocker).
//
// The returned error is already shaped for the HTTP layer — it
// wraps service.ErrNotFound / service.ErrBadRequest with a useful
// message; callers should `return err` directly.
func (a *api) resolveRegistry(c *ada.Context, requiredType string) (registry.Registry, string, string, error) {
	if err := a.registryFeatureGate(c); err != nil {
		return nil, "", "", err
	}
	if a.registryMgr == nil {
		return nil, "", "", fmt.Errorf("registry not configured: %w", service.ErrNotFound)
	}
	ns := c.Request.PathValue("ns")
	repo := c.Request.PathValue("repo")
	if ns == "" || repo == "" {
		return nil, "", "", fmt.Errorf("namespace and repo are required: %w", service.ErrBadRequest)
	}
	reg, ok := a.registryMgr.Lookup(ns, repo)
	if !ok {
		return nil, "", "", fmt.Errorf("registry %s/%s not found: %w", ns, repo, service.ErrNotFound)
	}
	// Two flavours of type check:
	//   - If the route carries a {type} segment and the caller
	//     leaves requiredType="", we still cross-check the path
	//     hint against the actual registry to catch URL/state
	//     drift (e.g. an operator pasting an /npm/ URL for a Go
	//     registry).
	//   - If the caller supplied requiredType, that wins and the
	//     path hint is ignored (the route may not even have a
	//     {type} segment for protocol-specific endpoints).
	if requiredType != "" {
		if reg.Type() != requiredType {
			return nil, "", "", fmt.Errorf("registry %s/%s is not a %s registry (got %s): %w",
				ns, repo, requiredType, reg.Type(), service.ErrBadRequest)
		}
	} else if regType := c.Request.PathValue("type"); regType != "" && reg.Type() != regType {
		return nil, "", "", fmt.Errorf("registry type mismatch (path=%s actual=%s): %w",
			regType, reg.Type(), service.ErrBadRequest)
	}
	return reg, ns, repo, nil
}

// registry.go — HTTP wiring for the artifact registry feature.
//
// Two distinct surfaces:
//
//  1. The token-authenticated client traffic mounted on mData under
//     "/registries/{namespace}/{repo}/*". This is where npm/docker/
//     go talk to pika. Authentication uses pika tokens scoped by
//     "registry/{namespace}/{repo}/...". Capability gating runs in
//     this handler (not via withPerm) because the data mux has no
//     CapMiddleware and never will.
//
//  2. The session-authenticated admin endpoints on m under
//     "/api/v1/registries/*" for listing namespaces, editing repos
//     and inspecting cached blobs. These reuse the standard
//     withPerm(CapRegistryAdmin / Read) plumbing.

// registrySecretResolver adapts pika's raw/config stores to the
// registry.SecretResolver interface so upstream clients can expand
// direct "raw://..." and "config://..." auth references.
type registrySecretResolver struct {
	svc *service.Service
	rh  *RawHandler
}

// ResolveSecret expands direct references. Values without a supported
// reference scheme are returned unchanged and treated as inline
// credentials by the upstream client.
func (r *registrySecretResolver) ResolveSecret(ctx context.Context, value string) (string, error) {
	switch {
	case strings.HasPrefix(value, "raw://"):
		return r.resolveRaw(value[len("raw://"):])
	case strings.HasPrefix(value, "config://"):
		return r.resolveConfig(ctx, value[len("config://"):])
	case strings.HasPrefix(value, "secret://"):
		return "", fmt.Errorf("secret:// references are no longer supported; use raw://mount/path or config://key")
	default:
		return value, nil
	}
}

func (r *registrySecretResolver) resolveRaw(path string) (string, error) {
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("raw ref %q: expected mount/path", path)
	}
	if r == nil || r.rh == nil {
		return "", fmt.Errorf("raw ref %q: raw mount resolver not available", path)
	}
	fs, ok := r.rh.MountFS(parts[0])
	if !ok {
		return "", fmt.Errorf("raw ref %q: mount %q not found", path, parts[0])
	}
	rc, _, err := fs.Open(parts[1])
	if err != nil {
		return "", fmt.Errorf("raw ref %q: open: %w", path, err)
	}
	defer rc.Close()
	buf, err := io.ReadAll(io.LimitReader(rc, 64*1024+1))
	if err != nil {
		return "", fmt.Errorf("raw ref %q: read: %w", path, err)
	}
	if len(buf) > 64*1024 {
		return "", fmt.Errorf("raw ref %q: value too large", path)
	}
	return strings.TrimSpace(string(buf)), nil
}

func (r *registrySecretResolver) resolveConfig(ctx context.Context, key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("config ref: key is empty")
	}
	if r == nil || r.svc == nil {
		return "", fmt.Errorf("config ref %q: config resolver not available", key)
	}
	file, err := r.svc.File(ctx, key, 0)
	if err != nil {
		return "", fmt.Errorf("config ref %q: read: %w", key, err)
	}
	return strings.TrimSpace(string(file.Data)), nil
}

// Ensure registrySecretResolver implements the registry interface.
var _ registry.SecretResolver = (*registrySecretResolver)(nil)

// buildMountForFunc constructs the Deps.MountFor closure used by
// registry factories. It looks up the requested raw mount via the
// live RawHandler and wraps its rawfs in a BlobStore adapter rooted
// at the per-repo base path.
//
// The closure is rebuilt on every reload because the underlying
// rawfs handle may have changed (e.g. operator switched a mount
// from local to S3). Registry factories should not cache the
// returned BlobStore across reloads.
func buildMountForFunc(rh *RawHandler) func(mount, basePath string) (blobstore.BlobStore, error) {
	return func(mount, basePath string) (blobstore.BlobStore, error) {
		if mount == "" {
			return nil, fmt.Errorf("mount name is empty")
		}
		fs, ok := rh.MountFS(mount)
		if !ok {
			return nil, fmt.Errorf("raw mount %q not found", mount)
		}
		if _, ok := fs.(rawfs.WritableRawFS); !ok {
			return nil, fmt.Errorf("raw mount %q is read-only", mount)
		}
		return blobstore.NewRawFSBlobStore(fs, basePath)
	}
}

// buildMountRawFSFunc constructs the Deps.MountRawFS closure that
// returns the live rawfs.RawFS handle for a named mount. Used by
// the Go module proxy (path-keyed files, no CAS) and any future
// protocol that needs direct rawfs access. The same hot-reload
// note from buildMountForFunc applies here.
func buildMountRawFSFunc(rh *RawHandler) func(mount string) (rawfs.RawFS, error) {
	return func(mount string) (rawfs.RawFS, error) {
		if mount == "" {
			return nil, fmt.Errorf("mount name is empty")
		}
		fs, ok := rh.MountFS(mount)
		if !ok {
			return nil, fmt.Errorf("raw mount %q not found", mount)
		}
		return fs, nil
	}
}

// reloadRegistry rebuilds the registry routing table from current
// settings. Called at boot and from postSettings after a Patch that
// touches the Registry tree.
func (a *api) reloadRegistry(ctx context.Context) {
	if a.registryMgr == nil {
		return
	}
	rs := a.svc.GetRegistrySettings(ctx)
	a.registryMgr.Reload(ctx, rs)
}

// serveRegistry is the entry handler for "/registries/*" on the
// data mux. It parses {namespace}/{repo}/{rest}, enforces token
// auth + capability, and dispatches to the matching Registry.
//
// Auth model: every request requires a pika token. The token's
// scopes are matched against "registry/{namespace}/{repo}/{rest}"
// (with the leading "/registries/" prefix stripped). Read methods
// (GET, HEAD, OPTIONS) check CapRegistryRead; write methods (POST,
// PUT, PATCH, DELETE) check CapRegistryWrite. Per-path scope
// patterns from the token determine whether the operation is
// permitted on that specific path. CORS preflights with an allowed
// repo origin short-circuit before auth so browsers can discover the
// allowed methods/headers.
//
// Public/anonymous registries are intentionally not supported in
// this MVP — per the user's decision.
func (a *api) serveRegistry(c *ada.Context) error {
	if a.registryMgr == nil {
		return fmt.Errorf("registry not configured: %w", service.ErrNotFound)
	}
	// Feature-flag gate: when the operator has disabled the
	// artifact-registry feature, every data-plane request returns
	// 404 (matches the Proxy / Vault feature-flag pattern). Token
	// scope and capability checks below would all be moot once the
	// feature is off, so reject before doing any of that work.
	if !a.svc.RegistryEnabled(c.Request.Context()) {
		return fmt.Errorf("registry feature disabled: %w", service.ErrNotFound)
	}

	// Path is the wildcard tail under "/registries/". ada strips
	// the route prefix, so we get "{namespace}/{repo}/{rest}".
	path := "/" + strings.TrimPrefix(c.Request.PathValue("*"), "/")

	ns, repo, rest, ok := registry.SplitRequestPath(path)
	if !ok {
		return fmt.Errorf("invalid registry path %q: %w", path, service.ErrBadRequest)
	}

	reg, found := a.registryMgr.Lookup(ns, repo)
	if !found {
		return fmt.Errorf("registry %s/%s not found: %w", ns, repo, service.ErrNotFound)
	}
	if common.ApplyCORS(c.Response, c.Request, a.registryCORSOrigins(c.Request.Context(), ns, repo)) {
		return nil
	}

	// Build the scope string for token validation. Mirrors the
	// "raw/{mount}/{path}" convention so token scope globs read
	// uniformly across raw mounts and registries.
	scope := "registry/" + ns + "/" + repo + rest
	op := operationFor(c.Request.Method)

	// Token authentication. Sessions can also authorize via the same
	// capability check the admin API uses; this lets a logged-in UI
	// operator drive `npm pack` against pika from the browser without
	// minting a separate token.
	tokenRaw := common.ExtractToken(c.Request)
	if tokenRaw != "" {
		if err := a.svc.ValidateToken(c.Request.Context(), tokenRaw, scope, op); err != nil {
			return err
		}
	} else {
		// Fall back to UI session + capability check.
		needed := capForOp(op)
		if a.mgr == nil {
			return fmt.Errorf("authentication required: %w", service.ErrUnauthorized)
		}
		id, capKeys, _, _, _ := a.mgr.ResolveRequest(c.Request)
		if id == nil {
			return fmt.Errorf("authentication required: %w", service.ErrUnauthorized)
		}
		if !service.Capabilities(capKeys).Has(needed) {
			return fmt.Errorf("capability %q required: %w", needed, service.ErrForbidden)
		}
	}

	// Strip the prefix that was matched and hand the request to the
	// Registry. ServeHTTP sees a path like "/@v/list" (Go) or
	// "/v2/myrepo/manifests/latest" (Docker).
	r := cloneRequestWithPath(c.Request, rest)

	// Hand the registry a hint about the URL prefix it lives under,
	// so handlers that emit absolute URLs (NPM tarball URLs) can
	// reconstruct the pika-facing public base. The hint is a
	// pika-internal contract, not a wire format; client-supplied
	// headers of the same name are overwritten.
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/"+ns+"/"+repo)

	reg.ServeHTTP(c.Response, r)
	return nil
}

func (a *api) registryCORSOrigins(ctx context.Context, namespace, repo string) []string {
	rs := a.svc.GetRegistrySettings(ctx)
	ns := rs.FindNamespace(namespace)
	r := ns.FindRepository(repo)
	if r == nil {
		return nil
	}
	return r.CORSOrigins
}

// operationFor returns the ValidateToken operation matching an HTTP
// method. Mirrors the convention used by the proxy auth-bearer
// middleware and by raw mount writes.
func operationFor(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return common.OpRead
	case http.MethodDelete:
		return common.OpDelete
	default:
		return common.OpWrite
	}
}

// capForOp maps the per-method operation to a session capability.
func capForOp(op string) string {
	switch op {
	case common.OpRead:
		return service.CapRegistryRead
	case common.OpDelete:
		return service.CapRegistryDelete
	default:
		return service.CapRegistryWrite
	}
}

// cloneRequestWithPath produces a shallow copy of r whose URL.Path
// has been rewritten. Used to hand a Registry only the path tail
// past the namespace/repo prefix. Returning a copy (rather than
// mutating in place) preserves the original for upstream loggers
// that hold a reference.
func cloneRequestWithPath(r *http.Request, newPath string) *http.Request {
	r2 := r.Clone(r.Context())
	if r2.URL != nil {
		u := *r2.URL
		u.Path = newPath
		// RawPath is invalidated by the rewrite; clearing it forces
		// net/url to re-encode from Path on String() calls.
		u.RawPath = ""
		r2.URL = &u
	}
	return r2
}

// --- Admin (session-auth) endpoints under /api/v1/registries/* ---

// registryFeatureGate rejects requests with 404 when the operator
// has disabled the registry feature via Settings → Features. The
// configuration-change endpoint (putRegistrySettings) is exempt: an
// admin must still be able to re-enable the feature, which means
// writing a Registry block with Disabled=false. Every other admin
// endpoint (list, browse, GC) goes through this gate.
func (a *api) registryFeatureGate(c *ada.Context) error {
	if !a.svc.RegistryEnabled(c.Request.Context()) {
		return fmt.Errorf("registry feature disabled: %w", service.ErrNotFound)
	}
	return nil
}

// listRegistryNamespaces returns the full namespace + repo tree for
// UI rendering. Read-only access only requires CapRegistryRead so a
// non-admin can still browse the catalogue.
func (a *api) listRegistryNamespaces(c *ada.Context) error {
	if err := a.registryFeatureGate(c); err != nil {
		return err
	}
	rs := a.svc.GetRegistrySettings(c.Request.Context())
	if rs == nil {
		rs = &service.RegistrySettings{}
	}
	includeSecrets := service.CapabilitiesFromContext(c.Request.Context()).Has(service.CapRegistryAdmin)
	return c.SetStatus(http.StatusOK).SendJSON(registrySettingsForResponse(rs, includeSecrets))
}

const redactedRegistrySecret = "[redacted]"

func registrySettingsForResponse(rs *service.RegistrySettings, includeSecrets bool) *service.RegistrySettings {
	if rs == nil {
		return &service.RegistrySettings{}
	}
	out := &service.RegistrySettings{Disabled: rs.Disabled}
	if len(rs.Namespaces) == 0 {
		return out
	}
	out.Namespaces = make([]service.RegistryNamespace, len(rs.Namespaces))
	for i, ns := range rs.Namespaces {
		out.Namespaces[i] = service.RegistryNamespace{
			Name:        ns.Name,
			Description: ns.Description,
		}
		if len(ns.Repositories) == 0 {
			continue
		}
		out.Namespaces[i].Repositories = make([]service.RegistryRepository, len(ns.Repositories))
		for j, repo := range ns.Repositories {
			out.Namespaces[i].Repositories[j] = registryRepositoryForResponse(repo, includeSecrets)
		}
	}
	return out
}

func registryRepositoryForResponse(repo service.RegistryRepository, includeSecrets bool) service.RegistryRepository {
	out := repo
	out.Members = cloneStrings(repo.Members)
	out.FloatingTags = cloneStrings(repo.FloatingTags)
	out.CORSOrigins = cloneStrings(repo.CORSOrigins)
	out.Policy = cloneRegistryPolicy(repo.Policy)
	if repo.Auth != nil {
		auth := *repo.Auth
		if !includeSecrets {
			redactRegistryAuth(&auth)
		}
		out.Auth = &auth
	}
	return out
}

func cloneRegistryPolicy(in *service.RegistryPolicy) *service.RegistryPolicy {
	if in == nil {
		return nil
	}
	out := &service.RegistryPolicy{
		ImmutableTags: cloneStrings(in.ImmutableTags),
	}
	if in.Retention != nil {
		ret := *in.Retention
		out.Retention = &ret
	}
	return out
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func redactRegistryAuth(auth *service.RegistryUpstreamAuth) {
	if auth.Password != "" {
		auth.Password = redactedRegistrySecret
	}
	if auth.Token != "" {
		auth.Token = redactedRegistrySecret
	}
	if auth.Value != "" {
		auth.Value = redactedRegistrySecret
	}
}

// putRegistrySettings replaces the entire registry tree. The whole-
// tree replacement matches how proxy_servers and raw_mounts are
// patched today — UI sends the new desired state, server validates +
// persists + reloads.
//
// As a side-effect, we diff the incoming tree against the on-disk
// previous tree and emit semantic create / update / delete events
// for each namespace and repository that changed. This gives
// operators a structured audit trail without forcing a separate
// admin sub-API per CRUD verb.
func (a *api) putRegistrySettings(c *ada.Context) error {
	var rs service.RegistrySettings
	if err := json.NewDecoder(c.Request.Body).Decode(&rs); err != nil {
		return fmt.Errorf("decode registry settings: %w: %w", err, service.ErrBadRequest)
	}
	// Snapshot the previous tree before patching so we can diff
	// for hook events.  GetRegistrySettings returns nil for fresh
	// installs; treat that as the empty tree.
	prev := a.svc.GetRegistrySettings(c.Request.Context())
	patch := &service.PatchSettings{
		Action:   service.ActionKeySet,
		Registry: &rs,
	}
	if err := a.svc.PatchSettings(c.Request.Context(), patch); err != nil {
		return err
	}
	a.reloadRegistry(c.Request.Context())
	a.emitRegistryDiff(prev, &rs)
	return c.SetStatus(http.StatusOK).SendJSON(rs)
}

// emitRegistryDiff fires namespace_* and repository_* events for
// every difference between two RegistrySettings trees. Called from
// putRegistrySettings post-save so a UI rename triggers a single
// repository_updated rather than a delete+create pair.
//
// The diff is name-keyed; an operator who renames a namespace ends
// up with one delete (old name) + one create (new name), matching
// what the UI actually does.
func (a *api) emitRegistryDiff(prev, next *service.RegistrySettings) {
	prevNS := map[string]service.RegistryNamespace{}
	if prev != nil {
		for _, n := range prev.Namespaces {
			prevNS[n.Name] = n
		}
	}
	nextNS := map[string]service.RegistryNamespace{}
	if next != nil {
		for _, n := range next.Namespaces {
			nextNS[n.Name] = n
		}
	}
	// Namespace-level events.
	for name := range nextNS {
		if _, existed := prevNS[name]; !existed {
			a.emitRegistryEvent(hook.Event{
				Type:  hook.EventRegistryNamespaceCreated,
				Mount: name,
			})
		}
	}
	for name := range prevNS {
		if _, kept := nextNS[name]; !kept {
			a.emitRegistryEvent(hook.Event{
				Type:  hook.EventRegistryNamespaceDeleted,
				Mount: name,
			})
		}
	}
	// Repository-level events per (still-present) namespace.
	for name, nNS := range nextNS {
		pNS, existed := prevNS[name]
		prevRepos := map[string]service.RegistryRepository{}
		if existed {
			for _, r := range pNS.Repositories {
				prevRepos[r.Name] = r
			}
		}
		nextRepos := map[string]service.RegistryRepository{}
		for _, r := range nNS.Repositories {
			nextRepos[r.Name] = r
		}
		for rn, r := range nextRepos {
			pr, kept := prevRepos[rn]
			switch {
			case !kept:
				a.emitRegistryEvent(hook.Event{
					Type:     hook.EventRegistryRepositoryCreated,
					Mount:    name,
					Path:     rn,
					Protocol: "registry-" + r.Type,
				})
			case repoChanged(pr, r):
				a.emitRegistryEvent(hook.Event{
					Type:     hook.EventRegistryRepositoryUpdated,
					Mount:    name,
					Path:     rn,
					Protocol: "registry-" + r.Type,
				})
			}
		}
		for rn, pr := range prevRepos {
			if _, kept := nextRepos[rn]; !kept {
				a.emitRegistryEvent(hook.Event{
					Type:     hook.EventRegistryRepositoryDeleted,
					Mount:    name,
					Path:     rn,
					Protocol: "registry-" + pr.Type,
				})
			}
		}
	}
}

// repoChanged reports whether two repository rows differ in any
// operator-visible way. Used by emitRegistryDiff to suppress
// updates that are byte-for-byte identical (e.g. the UI just
// re-posted the existing tree to flip a feature flag). A shallow
// JSON-equality compare would also work but is more allocation;
// the explicit shape compare avoids reflect.
func repoChanged(a, b service.RegistryRepository) bool {
	if a.Name != b.Name ||
		a.Description != b.Description ||
		a.Type != b.Type ||
		a.Kind != b.Kind ||
		a.Mount != b.Mount ||
		a.BasePath != b.BasePath ||
		a.AllowPush != b.AllowPush ||
		a.URL != b.URL ||
		a.MutableTTL != b.MutableTTL ||
		a.InsecureSkipVerify != b.InsecureSkipVerify ||
		a.DefaultLocal != b.DefaultLocal ||
		a.MaxUploadSize != b.MaxUploadSize {
		return true
	}
	if len(a.Members) != len(b.Members) {
		return true
	}
	for i := range a.Members {
		if a.Members[i] != b.Members[i] {
			return true
		}
	}
	if len(a.FloatingTags) != len(b.FloatingTags) {
		return true
	}
	for i := range a.FloatingTags {
		if a.FloatingTags[i] != b.FloatingTags[i] {
			return true
		}
	}
	if len(a.CORSOrigins) != len(b.CORSOrigins) {
		return true
	}
	for i := range a.CORSOrigins {
		if a.CORSOrigins[i] != b.CORSOrigins[i] {
			return true
		}
	}
	if !registryPolicyEqual(a.Policy, b.Policy) {
		return true
	}
	// Skip Auth — secret payload is sealed downstream and we don't
	// want to log "updated" every time the seal layer rewrites the
	// sensitive-payload blob with a fresh IV.
	return false
}

func registryPolicyEqual(a, b *service.RegistryPolicy) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if len(a.ImmutableTags) != len(b.ImmutableTags) {
		return false
	}
	for i := range a.ImmutableTags {
		if a.ImmutableTags[i] != b.ImmutableTags[i] {
			return false
		}
	}
	if a.Retention == nil || b.Retention == nil {
		return a.Retention == nil && b.Retention == nil
	}
	return a.Retention.GCMinAgeSeconds == b.Retention.GCMinAgeSeconds &&
		a.Retention.AbandonedUploadMaxAgeSeconds == b.Retention.AbandonedUploadMaxAgeSeconds
}

// listRegistryRepos returns a flat list of every registry the
// manager currently routes — used by the UI overview to render the
// "configured registries" grid without walking the namespace tree
// twice.
type registryListItem struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Kind      string `json:"kind"`
}

func (a *api) listRegistryRepos(c *ada.Context) error {
	if err := a.registryFeatureGate(c); err != nil {
		return err
	}
	if a.registryMgr == nil {
		return c.SetStatus(http.StatusOK).SendJSON([]registryListItem{})
	}
	regs := a.registryMgr.List()
	out := make([]registryListItem, 0, len(regs))
	for _, r := range regs {
		out = append(out, registryListItem{
			Namespace: r.Namespace(),
			Name:      r.Name(),
			Type:      r.Type(),
			Kind:      r.Kind(),
		})
	}
	return c.SetStatus(http.StatusOK).SendJSON(out)
}

// goModuleEntry is the per-module summary returned by
// listRegistryGoModules. Versions are derived from the store's
// @v/list view; for Remote / Virtual registries this reflects
// whatever is in the local cache.
type goModuleEntry struct {
	Module   string   `json:"module"`
	Versions []string `json:"versions"`
}

// npmPackageEntry is the per-package summary returned by
// listRegistryNPMPackages.
type npmPackageEntry struct {
	Name     string            `json:"name"`
	Versions []string          `json:"versions"`
	DistTags map[string]string `json:"dist_tags"`
}

// dockerTagSummary surfaces per-tag metadata: digest, artifact
// type (when the manifest is an OCI artifact rather than a plain
// image), content-type, and size breakdown.
//
// B5 breaking change: the historical `size` field was the manifest
// JSON's byte size, mislabelled as image size. It is now split
// into two distinct, correctly-named fields:
//
//   - manifest_size: bytes of the manifest JSON itself
//   - image_size:    sum of layer sizes (zero for OCI artifacts
//     whose layers are not classic image layers)
//
// Old clients that read the `size` field will see it absent —
// they need to choose explicitly which value they want. This was
// agreed at design time as a worthwhile clean break.
type dockerTagSummary struct {
	Tag          string `json:"tag"`
	Digest       string `json:"digest,omitempty"`
	ArtifactType string `json:"artifact_type,omitempty"`
	MediaType    string `json:"media_type,omitempty"`
	ManifestSize int64  `json:"manifest_size,omitempty"`
	ImageSize    int64  `json:"image_size,omitempty"`
}

// dockerRepoEntry is the per-image summary returned by
// listRegistryDockerRepos.
type dockerRepoEntry struct {
	Name string             `json:"name"`
	Tags []dockerTagSummary `json:"tags"`
}

// runRegistryUpstreamProbe runs a connectivity check against a
// Remote registry's upstream. URL: POST /api/v1/registries/{type}/{ns}/{repo}/test-upstream.
// Gated on CapRegistryAdmin because the probe uses the registry's
// real auth credentials.
//
// Local and Virtual registries return 400 ("not a remote") — the
// UI hides the button for them.
func (a *api) runRegistryUpstreamProbe(c *ada.Context) error {
	reg, ns, repo, err := a.resolveRegistry(c, "")
	if err != nil {
		return err
	}
	prober, ok := reg.(registry.UpstreamProber)
	if !ok {
		return fmt.Errorf("upstream probe not supported for %s/%s (kind=%s): %w",
			ns, repo, reg.Kind(), service.ErrBadRequest)
	}
	// Bound the probe by a tight timeout so a hanging upstream
	// doesn't block the admin connection.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	result, err := prober.ProbeUpstream(ctx)
	if err != nil {
		// The Probe helper folds protocol-level errors into the
		// UpstreamHealth struct; a non-nil error here means a
		// genuine internal failure (e.g. context cancellation).
		return fmt.Errorf("probe: %w", err)
	}
	return c.SetStatus(http.StatusOK).SendJSON(result)
}

// runDockerGC triggers a mark-and-sweep garbage collection pass
// against a Docker Local registry. URL:
// POST /api/v1/registries/docker/{ns}/{repo}/gc. Gated on
// CapRegistryAdmin — the operator must hold admin to delete blobs.
//
// Body (optional):
//
//	{"min_age_seconds": 3600, "abandoned_upload_max_age_seconds": 86400}
//
// Omitted fields use the repository policy defaults. Pass 0 to
// disable the corresponding grace/prune window for one request. The
// estimate endpoint (GET .../gc/estimate) accepts the same fields as
// query params.
type gcRunRequest struct {
	MinAgeSeconds                *int64 `json:"min_age_seconds"`
	AbandonedUploadMaxAgeSeconds *int64 `json:"abandoned_upload_max_age_seconds"`
}

// getRegistryStats returns a snapshot of on-disk counts for one
// registry. URL: GET /api/v1/registries/{type}/{ns}/{repo}/stats.
// Gated on CapRegistryRead (browsing is enough; no destructive
// action involved). Walks the underlying storage each call rather
// than maintaining persistent counters — see registry.Stats godoc.
func (a *api) getRegistryStats(c *ada.Context) error {
	reg, _, _, err := a.resolveRegistry(c, "")
	if err != nil {
		return err
	}
	provider, ok := reg.(registry.StatsProvider)
	if !ok {
		// Virtual repos delegate to members; report an empty
		// snapshot rather than erroring so the UI can render
		// "stats not available" gracefully.
		return c.SetStatus(http.StatusOK).SendJSON(registry.Stats{})
	}
	stats, err := provider.Stats(c.Request.Context())
	if err != nil {
		return fmt.Errorf("stats: %w", err)
	}
	return c.SetStatus(http.StatusOK).SendJSON(stats)
}

// runRegistryPurge invalidates the on-disk cache for a Remote
// registry. URL: POST /api/v1/registries/{type}/{ns}/{repo}/purge.
//
// Body (optional):
//
//	{"all": false}   default — mutable pointers only
//	{"all": true}    deep purge — also drops manifests / tarballs / blobs
//
// Gated on CapRegistryAdmin: the operation deletes data and forces
// an upstream re-fetch on the next pull, so it should not be on
// the path of regular CapRegistryWrite tokens.
//
// The handler type-asserts the looked-up Registry to the package
// CachePurger interface — Local registries don't implement it
// (their data is the source of truth) and surface as 400 here.
type purgeRequest struct {
	All bool `json:"all"`
}

func (a *api) runRegistryPurge(c *ada.Context) error {
	reg, ns, repo, err := a.resolveRegistry(c, "")
	if err != nil {
		return err
	}
	purger, ok := reg.(registry.CachePurger)
	if !ok {
		return fmt.Errorf("cache purge not supported for %s/%s (kind=%s): %w", ns, repo, reg.Kind(), service.ErrBadRequest)
	}

	req := purgeRequest{}
	if c.Request.ContentLength > 0 {
		_ = json.NewDecoder(c.Request.Body).Decode(&req)
	}
	stats, err := purger.PurgeCache(c.Request.Context(), registry.PurgeOptions{All: req.All})
	if err != nil {
		return fmt.Errorf("purge: %w", err)
	}
	// Semantic audit hook: surface the purge so operators can log it.
	a.emitRegistryEvent(hook.Event{
		Type:     hook.EventRegistryCachePurged,
		Mount:    ns,
		Path:     repo,
		Protocol: "registry-" + reg.Type(),
		Size:     stats.PurgedBytes,
	})
	return c.SetStatus(http.StatusOK).SendJSON(stats)
}

func (a *api) runDockerGC(c *ada.Context) error {
	reg, _, _, err := a.resolveRegistry(c, service.RegistryTypeDocker)
	if err != nil {
		return err
	}
	local, ok := reg.(*docker.Local)
	if !ok {
		return fmt.Errorf("GC requires a local Docker registry (got %s): %w", reg.Kind(), service.ErrBadRequest)
	}

	// Body is optional; omitted fields use repo policy defaults.
	opt := local.DefaultGCOptions()
	req := gcRunRequest{}
	if c.Request.ContentLength > 0 {
		_ = json.NewDecoder(c.Request.Body).Decode(&req)
	}
	if req.MinAgeSeconds != nil {
		opt.MinAge = *req.MinAgeSeconds
	}
	if req.AbandonedUploadMaxAgeSeconds != nil {
		opt.AbandonedUploadMaxAge = *req.AbandonedUploadMaxAgeSeconds
	}

	stats, err := local.GarbageCollect(c.Request.Context(), opt)
	if err != nil {
		return fmt.Errorf("gc: %w", err)
	}
	return c.SetStatus(http.StatusOK).SendJSON(stats)
}

// estimateDockerGC runs a dry-run mark-and-sweep pass and returns
// the GCStats that WOULD have resulted from a real run. URL:
// GET /api/v1/registries/docker/{ns}/{repo}/gc/estimate. Gated on
// CapRegistryRead — pure read pass.
//
// Defaults mirror runDockerGC so the estimate reflects the same
// reclaim window the cleanup button will use. The endpoint accepts
// the same parameters as query string overrides when the operator
// wants to preview a tighter / looser sweep:
//
//	?min_age_seconds=0&abandoned_upload_max_age_seconds=3600
func (a *api) estimateDockerGC(c *ada.Context) error {
	reg, _, _, err := a.resolveRegistry(c, service.RegistryTypeDocker)
	if err != nil {
		return err
	}
	local, ok := reg.(*docker.Local)
	if !ok {
		return fmt.Errorf("GC estimate requires a local Docker registry (got %s): %w", reg.Kind(), service.ErrBadRequest)
	}

	opt := local.DefaultGCOptions()
	opt.DryRun = true
	q := c.Request.URL.Query()
	if v := q.Get("min_age_seconds"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			opt.MinAge = n
		}
	}
	if v := q.Get("abandoned_upload_max_age_seconds"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			opt.AbandonedUploadMaxAge = n
		}
	}

	stats, err := local.GarbageCollect(c.Request.Context(), opt)
	if err != nil {
		return fmt.Errorf("gc estimate: %w", err)
	}
	return c.SetStatus(http.StatusOK).SendJSON(stats)
}

// listRegistryDockerRepos returns image / tag tree for a Docker
// registry repo. URL: /api/v1/registries/docker/{ns}/{repo}/repos.
//
// For each tag we resolve the underlying manifest, peek at its
// artifactType / config.mediaType so the UI can distinguish plain
// images from OCI artifacts (Helm charts, cosign signatures, SBOMs).
func (a *api) listRegistryDockerRepos(c *ada.Context) error {
	reg, _, _, err := a.resolveRegistry(c, service.RegistryTypeDocker)
	if err != nil {
		return err
	}
	store, ok := storeFromRegistry[docker.Store](reg)
	if !ok {
		return c.SetStatus(http.StatusOK).SendJSON([]dockerRepoEntry{})
	}
	names, err := store.ListRepositories()
	if err != nil {
		return fmt.Errorf("list repos: %w", err)
	}
	out := make([]dockerRepoEntry, 0, len(names))
	for _, name := range names {
		tags, _ := store.ListTags(name)
		summaries := make([]dockerTagSummary, 0, len(tags))
		for _, t := range tags {
			summary := dockerTagSummary{Tag: t}
			if dgst, err := store.ReadTag(name, t); err == nil {
				summary.Digest = dgst.String()
				if rec, err := store.ReadManifest(name, dgst); err == nil {
					summary.MediaType = rec.ContentType
					summary.ManifestSize = int64(len(rec.Body))
					if artType := docker.ArtifactTypeOf(rec.Body); artType != "" {
						summary.ArtifactType = artType
					}
					// Image size = sum of layer sizes. Cheap to
					// compute (one JSON parse + a slice scan) and
					// the data the UI actually wants for a "how
					// big is this image" answer.
					if insp := docker.InspectManifestBytes(rec.Body); insp != nil {
						summary.ImageSize = insp.ImageSize
					}
				}
			}
			summaries = append(summaries, summary)
		}
		out = append(out, dockerRepoEntry{Name: name, Tags: summaries})
	}
	return c.SetStatus(http.StatusOK).SendJSON(out)
}

// helmChartEntry is the per-chart summary returned by
// listRegistryHelmCharts.
type helmChartEntry struct {
	Name     string   `json:"name"`
	Versions []string `json:"versions"`
}

type mavenArtifactEntry struct {
	GroupID    string   `json:"group_id"`
	ArtifactID string   `json:"artifact_id"`
	Versions   []string `json:"versions"`
}

type pypiPackageEntry struct {
	Name     string   `json:"name"`
	Versions []string `json:"versions"`
}

type cargoCrateEntry struct {
	Name     string   `json:"name"`
	Versions []string `json:"versions"`
}

// listRegistryHelmCharts returns the chart/version tree for a
// Helm registry repo. URL: GET /api/v1/registries/helm/{ns}/{repo}/charts.
func (a *api) listRegistryHelmCharts(c *ada.Context) error {
	reg, _, _, err := a.resolveRegistry(c, service.RegistryTypeHelm)
	if err != nil {
		return err
	}
	store, ok := storeFromRegistry[helm.Store](reg)
	if !ok {
		return c.SetStatus(http.StatusOK).SendJSON([]helmChartEntry{})
	}
	charts, err := store.ListCharts()
	if err != nil {
		return fmt.Errorf("list charts: %w", err)
	}
	out := make([]helmChartEntry, 0, len(charts))
	for _, name := range charts {
		versions, _ := store.ListVersions(name)
		out = append(out, helmChartEntry{Name: name, Versions: versions})
	}
	return c.SetStatus(http.StatusOK).SendJSON(out)
}

func (a *api) listRegistryMavenArtifacts(c *ada.Context) error {
	reg, _, _, err := a.resolveRegistry(c, service.RegistryTypeMaven)
	if err != nil {
		return err
	}
	store, ok := storeFromRegistry[maven.Store](reg)
	if !ok {
		return c.SetStatus(http.StatusOK).SendJSON([]mavenArtifactEntry{})
	}
	artifacts, err := store.ListArtifacts()
	if err != nil {
		return fmt.Errorf("list maven artifacts: %w", err)
	}
	out := make([]mavenArtifactEntry, 0, len(artifacts))
	for _, a := range artifacts {
		out = append(out, mavenArtifactEntry{GroupID: a.GroupID, ArtifactID: a.ArtifactID, Versions: a.Versions})
	}
	return c.SetStatus(http.StatusOK).SendJSON(out)
}

func (a *api) listRegistryPyPIPackages(c *ada.Context) error {
	reg, _, _, err := a.resolveRegistry(c, service.RegistryTypePyPI)
	if err != nil {
		return err
	}
	store, ok := storeFromRegistry[pypi.Store](reg)
	if !ok {
		return c.SetStatus(http.StatusOK).SendJSON([]pypiPackageEntry{})
	}
	packages, err := store.ListPackages()
	if err != nil {
		return fmt.Errorf("list pypi packages: %w", err)
	}
	out := make([]pypiPackageEntry, 0, len(packages))
	for _, name := range packages {
		versions, _ := store.ListVersions(name)
		out = append(out, pypiPackageEntry{Name: name, Versions: versions})
	}
	return c.SetStatus(http.StatusOK).SendJSON(out)
}

func (a *api) listRegistryCargoCrates(c *ada.Context) error {
	reg, _, _, err := a.resolveRegistry(c, service.RegistryTypeCargo)
	if err != nil {
		return err
	}
	store, ok := storeFromRegistry[cargo.Store](reg)
	if !ok {
		return c.SetStatus(http.StatusOK).SendJSON([]cargoCrateEntry{})
	}
	crates, err := store.ListCrates()
	if err != nil {
		return fmt.Errorf("list cargo crates: %w", err)
	}
	out := make([]cargoCrateEntry, 0, len(crates))
	for _, name := range crates {
		versions, _ := store.ListVersions(name)
		out = append(out, cargoCrateEntry{Name: name, Versions: versions})
	}
	return c.SetStatus(http.StatusOK).SendJSON(out)
}

// listRegistryNPMPackages returns the package/version tree for an
// NPM registry repo. Mirrors listRegistryGoModules in shape and
// gating; URL: /api/v1/registries/npm/{ns}/{repo}/packages.
func (a *api) listRegistryNPMPackages(c *ada.Context) error {
	reg, _, _, err := a.resolveRegistry(c, service.RegistryTypeNPM)
	if err != nil {
		return err
	}
	store, ok := storeFromRegistry[npm.Store](reg)
	if !ok {
		// Virtual NPM repos don't carry their own store; the UI
		// shows a "browse members" hint when this slice is empty.
		return c.SetStatus(http.StatusOK).SendJSON([]npmPackageEntry{})
	}
	packages, err := store.ListPackages()
	if err != nil {
		return fmt.Errorf("list packages: %w", err)
	}
	out := make([]npmPackageEntry, 0, len(packages))
	for _, p := range packages {
		versions, _ := store.ListVersions(p)
		tags, _ := store.ReadDistTags(p)
		if tags == nil {
			tags = map[string]string{}
		}
		out = append(out, npmPackageEntry{
			Name: p, Versions: versions, DistTags: tags,
		})
	}
	return c.SetStatus(http.StatusOK).SendJSON(out)
}

// getRegistryPackageDetail returns the per-package detail document.
// URL: GET /api/v1/registries/{type}/{ns}/{repo}/packages/{name...}.
//
// The `{name...}` wildcard segment accommodates every protocol's
// hierarchical naming: Go modules ("example.com/foo/bar"), NPM
// scoped packages ("@scope/pkg"), Docker image paths
// ("library/nginx"). The handler validates the {type} segment
// matches the resolved registry; mismatches return 400 to surface
// caller bugs cleanly.
//
// Gated on CapRegistryRead — read-only browsing.
func (a *api) getRegistryPackageDetail(c *ada.Context) error {
	reg, ns, repo, err := a.resolveRegistry(c, "")
	if err != nil {
		return err
	}
	// PathValue strips the leading slash by ada convention, but a
	// wildcard segment may carry one through depending on the URL
	// pattern flavour. Normalise so downstream stores see the
	// canonical form.
	name := strings.TrimPrefix(c.Request.PathValue("name"), "/")
	if name == "" {
		return fmt.Errorf("package name is required: %w", service.ErrBadRequest)
	}
	detailer, ok := reg.(registry.PackageDetailer)
	if !ok {
		return fmt.Errorf("package detail not supported for %s/%s (kind=%s): %w",
			ns, repo, reg.Kind(), service.ErrBadRequest)
	}
	out, err := detailer.PackageDetail(c.Request.Context(), name)
	if err != nil {
		// Map registry sentinels to the right HTTP status. Order
		// matters: ErrInvalidPackageName means the caller supplied
		// junk (400); ErrPackageNotFound means the request was
		// well-formed but produced no match (404). Anything else
		// is a genuine 500.
		switch {
		case errors.Is(err, registry.ErrInvalidPackageName):
			return fmt.Errorf("invalid package name %q: %w: %w", name, err, service.ErrBadRequest)
		case errors.Is(err, registry.ErrPackageNotFound):
			return fmt.Errorf("package %q not found in %s/%s: %w", name, ns, repo, service.ErrNotFound)
		}
		return fmt.Errorf("package detail: %w", err)
	}
	return c.SetStatus(http.StatusOK).SendJSON(out)
}

// getNPMPackageReadme returns the cached README markdown for an NPM
// package. URL: GET /api/v1/registries/npm/{ns}/{repo}/packages/{name}/readme.
//
// If the cached file is empty, the handler attempts a lazy extract
// from the latest version's tarball before responding. Returns 404
// when no README is available after the lazy step.
//
// Response: text/markdown; charset=utf-8.
//
// Gated on CapRegistryRead.
func (a *api) getNPMPackageReadme(c *ada.Context) error {
	reg, ns, repo, err := a.resolveRegistry(c, service.RegistryTypeNPM)
	if err != nil {
		return err
	}
	name := strings.TrimPrefix(c.Request.PathValue("name"), "/")
	if name == "" {
		return fmt.Errorf("package name is required: %w", service.ErrBadRequest)
	}
	store, ok := storeFromRegistry[npm.Store](reg)
	if !ok {
		return fmt.Errorf("npm store unavailable for %s/%s: %w", ns, repo, service.ErrBadRequest)
	}
	body, err := store.ReadReadme(name)
	if err != nil {
		return fmt.Errorf("read readme: %w", err)
	}
	if body == "" {
		// Lazy fallback: pick the latest version's tarball, extract
		// the README, cache for next time. The detail builder has
		// already populated HasReadme=false in that case; the UI
		// can retry on demand if it suspects the cache was just
		// missing.
		tarFile := latestTarballFilename(store, name)
		if tarFile != "" {
			extracted, extractErr := store.LazyExtractReadme(name, tarFile)
			if extractErr == nil && extracted != "" {
				body = extracted
			}
		}
	}
	if body == "" {
		return fmt.Errorf("no readme for %s: %w", name, service.ErrNotFound)
	}
	c.Response.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	c.Response.WriteHeader(http.StatusOK)
	_, _ = c.Response.Write([]byte(body))
	return nil
}

// latestTarballFilename returns the dist.tarball filename for the
// latest version of `name`, or "" if the package has no versions /
// no usable dist block. Used by the lazy README extractor to know
// which tarball to pop open.
func latestTarballFilename(store *npm.Store, name string) string {
	versions, _ := store.ListVersions(name)
	if len(versions) == 0 {
		return ""
	}
	tags, _ := store.ReadDistTags(name)
	latest := tags["latest"]
	if latest == "" {
		latest = versions[len(versions)-1]
	}
	meta, err := store.ReadVersionMeta(name, latest)
	if err != nil {
		return ""
	}
	dist, ok := meta["dist"].(map[string]any)
	if !ok {
		return ""
	}
	t, _ := dist["tarball"].(string)
	if t == "" {
		return ""
	}
	if i := strings.LastIndex(t, "/"); i >= 0 {
		return t[i+1:]
	}
	return t
}

// getGoModuleGoMod returns the raw go.mod bytes for a single Go
// module version. URL: GET /api/v1/registries/go/{ns}/{repo}/modules/{name...}/versions/{version}/gomod.
//
// Gated on CapRegistryRead.
func (a *api) getGoModuleGoMod(c *ada.Context) error {
	reg, ns, repo, err := a.resolveRegistry(c, service.RegistryTypeGo)
	if err != nil {
		return err
	}
	name := strings.TrimPrefix(c.Request.PathValue("name"), "/")
	version := c.Request.PathValue("version")
	if name == "" || version == "" {
		return fmt.Errorf("name and version are required: %w", service.ErrBadRequest)
	}
	store, ok := storeFromRegistry[goproxy.Store](reg)
	if !ok {
		return fmt.Errorf("go store unavailable for %s/%s: %w", ns, repo, service.ErrBadRequest)
	}
	body, err := store.ReadGoMod(name, version)
	if err != nil {
		return fmt.Errorf("go.mod for %s@%s: %w: %w", name, version, err, service.ErrNotFound)
	}
	c.Response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	c.Response.WriteHeader(http.StatusOK)
	_, _ = c.Response.Write(body)
	return nil
}

// listRegistryGoModules returns the module/version tree for a Go
// registry repo. URL shape: /api/v1/registries/{ns}/{repo}/go-modules.
// The handler downcasts the resolved Registry to a type that
// exposes a *goproxy.Store; registries without a Store (Virtual)
// fall back to walking every member.
//
// Used by the UI to render the module browser inside the repo
// detail view. Read-only — gated on CapRegistryRead.
func (a *api) listRegistryGoModules(c *ada.Context) error {
	reg, _, _, err := a.resolveRegistry(c, service.RegistryTypeGo)
	if err != nil {
		return err
	}
	store, ok := storeFromRegistry[goproxy.Store](reg)
	if !ok {
		// Virtual registries don't carry a store of their own;
		// they aggregate over members. For now we just return an
		// empty list — the UI can show a "browse members" hint.
		// A future enhancement could walk members and merge their
		// module lists here.
		return c.SetStatus(http.StatusOK).SendJSON([]goModuleEntry{})
	}

	modules, err := store.ListModules()
	if err != nil {
		return fmt.Errorf("list modules: %w", err)
	}
	out := make([]goModuleEntry, 0, len(modules))
	for _, m := range modules {
		versions, _ := store.ListVersions(m)
		out = append(out, goModuleEntry{Module: m, Versions: versions})
	}
	return c.SetStatus(http.StatusOK).SendJSON(out)
}

// hookEmitter adapts a *hook.Dispatcher to the registry events.Emitter
// interface. Thin wrapper so the registry package never imports
// hook.Dispatcher directly — only the narrow Emit method that the
// registry impls actually call.
type hookEmitter struct {
	d *hook.Dispatcher
}

func (h *hookEmitter) Emit(event hook.Event) {
	if h == nil || h.d == nil {
		return
	}
	h.d.Emit(event)
}

// emitRegistryEvent is a small dispatcher-side helper for the admin
// HTTP layer. Used by the purge endpoint and the namespace / repo
// CRUD flow inside putRegistrySettings — those operations don't go
// through a registry impl's emitter, so we publish straight against
// the dispatcher pika already has wired.
func (a *api) emitRegistryEvent(event hook.Event) {
	if a.rawHandler == nil {
		return
	}
	d := a.rawHandler.Dispatcher()
	if d == nil {
		return
	}
	d.Emit(event)
}

// BootRegistryManager constructs the registry.Manager with all
// dependencies wired, performs the default-namespace bootstrap, and
// loads the initial routing table. Called once from server.Start.
//
// The returned manager has no factories registered yet — they are
// added by their respective protocol packages (registry/goproxy,
// registry/npm, registry/docker) at boot, before the initial
// Reload. The wiring code in server.go registers them right after
// this function returns.
//
// The dispatcher argument is optional: pass nil to disable semantic
// event emission (no registry.* hooks fire). Tests typically pass
// nil; the live server passes its main hook.Dispatcher so operators
// can wire push / GC events to webhooks.
func BootRegistryManager(ctx context.Context, svc *service.Service, rh *RawHandler, dispatcher *hook.Dispatcher) *registry.Manager {
	// Non-fatal: log and continue if the bootstrap can't write.
	// A locked encryption store, for example, will block writes
	// until unlock; the bootstrap can run again on the next save.
	if err := svc.EnsureDefaultRegistryNamespace(ctx); err != nil {
		slog.Warn("registry: default namespace bootstrap failed", "error", err)
	}

	var emitter events.Emitter
	if dispatcher != nil {
		emitter = &hookEmitter{d: dispatcher}
	}
	deps := registry.Deps{
		Svc:        svc,
		Resolver:   &registrySecretResolver{svc: svc, rh: rh},
		MountFor:   buildMountForFunc(rh),
		MountRawFS: buildMountRawFSFunc(rh),
		Emitter:    emitter,
	}
	return registry.NewManager(deps)
}
