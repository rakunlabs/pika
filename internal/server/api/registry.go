package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/blobstore"
	"github.com/rakunlabs/pika/internal/registry/common"
	"github.com/rakunlabs/pika/internal/registry/docker"
	"github.com/rakunlabs/pika/internal/registry/goproxy"
	"github.com/rakunlabs/pika/internal/registry/npm"
	"github.com/rakunlabs/pika/internal/service"
)

// goStoreFromRegistry extracts the underlying goproxy.Store from a
// Registry instance. Returns ok=false for kinds that don't carry a
// store of their own (Virtual). Local and Remote both expose a
// Store() accessor with the same signature.
func goStoreFromRegistry(r registry.Registry) (*goproxy.Store, bool) {
	type storer interface{ Store() *goproxy.Store }
	if s, ok := r.(storer); ok {
		return s.Store(), true
	}
	return nil, false
}

// npmStoreFromRegistry mirrors goStoreFromRegistry for npm.Store.
func npmStoreFromRegistry(r registry.Registry) (*npm.Store, bool) {
	type storer interface{ Store() *npm.Store }
	if s, ok := r.(storer); ok {
		return s.Store(), true
	}
	return nil, false
}

// dockerStoreFromRegistry mirrors the pattern for docker.Store.
func dockerStoreFromRegistry(r registry.Registry) (*docker.Store, bool) {
	type storer interface{ Store() *docker.Store }
	if s, ok := r.(storer); ok {
		return s.Store(), true
	}
	return nil, false
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

// registrySecretResolver adapts the hook resolver (already wired in
// the api struct) to the registry.SecretResolver interface so the
// upstream client can expand "secret://" auth values. We piggyback on
// the hook package's resolver because it already knows how to read
// from raw mounts and the config store — registry uses the same
// reference vocabulary.
type registrySecretResolver struct {
	svc *service.Service
	rh  *RawHandler
}

// ResolveSecret expands "secret://..." references. For MVP the only
// supported scheme is "raw://<mount>/<path>" carried inside a value
// that begins with "secret://". The wrapping is intentionally double:
//
//   "secret://" tells the upstream client "this is a reference, please
//    resolve before sending on the wire"; the underlying form
//    ("raw://...", "config://...", or a plain inline value) tells the
//    resolver which store to read from.
//
// This matches the hook resolver's contract so the same secret values
// can be used in both hook targets and registry upstream credentials.
func (r *registrySecretResolver) ResolveSecret(ctx context.Context, value string) (string, error) {
	if !strings.HasPrefix(value, "secret://") {
		return value, nil
	}
	inner := strings.TrimPrefix(value, "secret://")
	switch {
	case strings.HasPrefix(inner, "raw://"):
		// raw://mount/path — read via the rawfs mount table.
		path := strings.TrimPrefix(inner, "raw://")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("secret raw ref %q: expected mount/path", inner)
		}
		fs, ok := r.rh.MountFS(parts[0])
		if !ok {
			return "", fmt.Errorf("secret raw ref %q: mount %q not found", inner, parts[0])
		}
		rc, _, err := fs.Open(parts[1])
		if err != nil {
			return "", fmt.Errorf("secret raw ref %q: open: %w", inner, err)
		}
		defer rc.Close()
		buf := make([]byte, 0, 256)
		chunk := make([]byte, 256)
		for {
			n, err := rc.Read(chunk)
			if n > 0 {
				buf = append(buf, chunk[:n]...)
			}
			if err != nil {
				break
			}
			if len(buf) > 64*1024 {
				return "", fmt.Errorf("secret raw ref %q: value too large", inner)
			}
		}
		return strings.TrimSpace(string(buf)), nil
	default:
		// Future: config://, vault://. For MVP, treat unknown inner
		// scheme as plain literal so operators can paste a token
		// after "secret://" if they really want to (cheap fallback,
		// not recommended).
		return inner, nil
	}
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
// permitted on that specific path.
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
	return c.SetStatus(http.StatusOK).SendJSON(rs)
}

// putRegistrySettings replaces the entire registry tree. The whole-
// tree replacement matches how proxy_servers and raw_mounts are
// patched today — UI sends the new desired state, server validates +
// persists + reloads.
func (a *api) putRegistrySettings(c *ada.Context) error {
	var rs service.RegistrySettings
	if err := json.NewDecoder(c.Request.Body).Decode(&rs); err != nil {
		return fmt.Errorf("decode registry settings: %w: %w", err, service.ErrBadRequest)
	}
	patch := &service.PatchSettings{
		Action:   service.ActionKeySet,
		Registry: &rs,
	}
	if err := a.svc.PatchSettings(c.Request.Context(), patch); err != nil {
		return err
	}
	a.reloadRegistry(c.Request.Context())
	return c.SetStatus(http.StatusOK).SendJSON(rs)
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
// image), and content-type. Populated best-effort — manifest read
// failures fall back to a name-only entry.
type dockerTagSummary struct {
	Tag          string `json:"tag"`
	Digest       string `json:"digest,omitempty"`
	ArtifactType string `json:"artifact_type,omitempty"`
	MediaType    string `json:"media_type,omitempty"`
	Size         int64  `json:"size,omitempty"`
}

// dockerRepoEntry is the per-image summary returned by
// listRegistryDockerRepos.
type dockerRepoEntry struct {
	Name string             `json:"name"`
	Tags []dockerTagSummary `json:"tags"`
}

// runDockerGC triggers a mark-and-sweep garbage collection pass
// against a Docker Local registry. URL:
// POST /api/v1/registries/docker/{ns}/{repo}/gc. Gated on
// CapRegistryAdmin — the operator must hold admin to delete blobs.
//
// Body (optional):
//
//	{"min_age_seconds": 3600}
//
// MinAge of 3600 (1h) is recommended in production; tests use 0.
type gcRunRequest struct {
	MinAgeSeconds int64 `json:"min_age_seconds"`
}

func (a *api) runDockerGC(c *ada.Context) error {
	if err := a.registryFeatureGate(c); err != nil {
		return err
	}
	if a.registryMgr == nil {
		return fmt.Errorf("registry not configured: %w", service.ErrNotFound)
	}
	ns := c.Request.PathValue("ns")
	repo := c.Request.PathValue("repo")
	if ns == "" || repo == "" {
		return fmt.Errorf("namespace and repo are required: %w", service.ErrBadRequest)
	}
	reg, ok := a.registryMgr.Lookup(ns, repo)
	if !ok {
		return fmt.Errorf("registry %s/%s not found: %w", ns, repo, service.ErrNotFound)
	}
	if reg.Type() != service.RegistryTypeDocker {
		return fmt.Errorf("GC is only available for Docker registries: %w", service.ErrBadRequest)
	}
	local, ok := reg.(*docker.Local)
	if !ok {
		return fmt.Errorf("GC requires a local Docker registry (got %s): %w", reg.Kind(), service.ErrBadRequest)
	}

	// Body is optional; default MinAge=3600.
	req := gcRunRequest{MinAgeSeconds: 3600}
	if c.Request.ContentLength > 0 {
		_ = json.NewDecoder(c.Request.Body).Decode(&req)
	}

	stats, err := local.GarbageCollect(c.Request.Context(), docker.GCOptions{
		MinAge: req.MinAgeSeconds,
	})
	if err != nil {
		return fmt.Errorf("gc: %w", err)
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
	if err := a.registryFeatureGate(c); err != nil {
		return err
	}
	if a.registryMgr == nil {
		return fmt.Errorf("registry not configured: %w", service.ErrNotFound)
	}
	ns := c.Request.PathValue("ns")
	repo := c.Request.PathValue("repo")
	if ns == "" || repo == "" {
		return fmt.Errorf("namespace and repo are required: %w", service.ErrBadRequest)
	}
	reg, ok := a.registryMgr.Lookup(ns, repo)
	if !ok {
		return fmt.Errorf("registry %s/%s not found: %w", ns, repo, service.ErrNotFound)
	}
	if reg.Type() != service.RegistryTypeDocker {
		return fmt.Errorf("registry %s/%s is not a Docker registry: %w", ns, repo, service.ErrBadRequest)
	}
	store, ok := dockerStoreFromRegistry(reg)
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
					summary.Size = int64(len(rec.Body))
					if artType := docker.ArtifactTypeOf(rec.Body); artType != "" {
						summary.ArtifactType = artType
					}
				}
			}
			summaries = append(summaries, summary)
		}
		out = append(out, dockerRepoEntry{Name: name, Tags: summaries})
	}
	return c.SetStatus(http.StatusOK).SendJSON(out)
}

// listRegistryNPMPackages returns the package/version tree for an
// NPM registry repo. Mirrors listRegistryGoModules in shape and
// gating; URL: /api/v1/registries/npm/{ns}/{repo}/packages.
func (a *api) listRegistryNPMPackages(c *ada.Context) error {
	if err := a.registryFeatureGate(c); err != nil {
		return err
	}
	if a.registryMgr == nil {
		return fmt.Errorf("registry not configured: %w", service.ErrNotFound)
	}
	ns := c.Request.PathValue("ns")
	repo := c.Request.PathValue("repo")
	if ns == "" || repo == "" {
		return fmt.Errorf("namespace and repo are required: %w", service.ErrBadRequest)
	}
	reg, ok := a.registryMgr.Lookup(ns, repo)
	if !ok {
		return fmt.Errorf("registry %s/%s not found: %w", ns, repo, service.ErrNotFound)
	}
	if reg.Type() != service.RegistryTypeNPM {
		return fmt.Errorf("registry %s/%s is not an NPM registry: %w", ns, repo, service.ErrBadRequest)
	}
	store, ok := npmStoreFromRegistry(reg)
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

// listRegistryGoModules returns the module/version tree for a Go
// registry repo. URL shape: /api/v1/registries/{ns}/{repo}/go-modules.
// The handler downcasts the resolved Registry to a type that
// exposes a *goproxy.Store; registries without a Store (Virtual)
// fall back to walking every member.
//
// Used by the UI to render the module browser inside the repo
// detail view. Read-only — gated on CapRegistryRead.
func (a *api) listRegistryGoModules(c *ada.Context) error {
	if err := a.registryFeatureGate(c); err != nil {
		return err
	}
	if a.registryMgr == nil {
		return fmt.Errorf("registry not configured: %w", service.ErrNotFound)
	}
	ns := c.Request.PathValue("ns")
	repo := c.Request.PathValue("repo")
	if ns == "" || repo == "" {
		return fmt.Errorf("namespace and repo are required: %w", service.ErrBadRequest)
	}
	reg, ok := a.registryMgr.Lookup(ns, repo)
	if !ok {
		return fmt.Errorf("registry %s/%s not found: %w", ns, repo, service.ErrNotFound)
	}
	if reg.Type() != service.RegistryTypeGo {
		return fmt.Errorf("registry %s/%s is not a Go registry: %w", ns, repo, service.ErrBadRequest)
	}

	store, ok := goStoreFromRegistry(reg)
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

// BootRegistryManager constructs the registry.Manager with all
// dependencies wired, performs the default-namespace bootstrap, and
// loads the initial routing table. Called once from server.Start.
//
// The returned manager has no factories registered yet — they are
// added by their respective protocol packages (registry/goproxy,
// registry/npm, registry/docker) at boot, before the initial
// Reload. The wiring code in server.go registers them right after
// this function returns.
func BootRegistryManager(ctx context.Context, svc *service.Service, rh *RawHandler) *registry.Manager {
	// Non-fatal: log and continue if the bootstrap can't write.
	// A locked encryption store, for example, will block writes
	// until unlock; the bootstrap can run again on the next save.
	if err := svc.EnsureDefaultRegistryNamespace(ctx); err != nil {
		slog.Warn("registry: default namespace bootstrap failed", "error", err)
	}

	deps := registry.Deps{
		Svc:        svc,
		Resolver:   &registrySecretResolver{svc: svc, rh: rh},
		MountFor:   buildMountForFunc(rh),
		MountRawFS: buildMountRawFSFunc(rh),
	}
	return registry.NewManager(deps)
}


