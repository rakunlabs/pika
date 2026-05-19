package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/blobstore"
	"github.com/rakunlabs/pika/internal/registry/common"
	"github.com/rakunlabs/pika/internal/registry/upstream"
	"github.com/rakunlabs/pika/internal/service"
)

// Remote is a Docker / OCI registry pull-through cache. It serves
// reads from a local Store-backed cache, fetching from upstream on
// miss. Writes are rejected — pushes go to a Local repo, not a
// remote mirror.
//
// Upstream auth handshake
//
// Every modern Docker registry (Docker Hub, GHCR, Quay, ECR) requires
// a bearer token even for public reads. The protocol is:
//
//   1. GET upstream/v2/{path}
//   2. Upstream responds 401 with WWW-Authenticate:
//        Bearer realm=<url>,service=<svc>,scope=repository:name:pull
//   3. We GET <realm>?service=<svc>&scope=<scope> to mint a token
//      (optionally with our configured Auth as Basic credentials).
//   4. Re-issue the original request with Authorization: Bearer <jwt>.
//
// We perform the challenge-response per fetch (with a small in-mem
// token cache keyed by scope) rather than once at boot, because:
//
//   - The upstream picks the scope from our request URL, so we
//     can't pre-fetch tokens without knowing which images will be
//     read.
//   - Tokens are short-lived (5-15min typically); caching all of
//     them indefinitely would invite drift.
//
// Cache strategy
//
//   - Blobs (immutable, content-addressed): cache forever on first
//     fetch. Served from the local Store on subsequent reads.
//   - Manifests by digest: same — immutable.
//   - Manifests by tag: split by tag classification.
//       * Floating tags (operator-declared list — typically
//         "latest", "main", "nightly") are TTL-bounded: on stale,
//         we re-resolve through upstream and update the local
//         tag → digest pointer.
//       * Non-floating tags (semver, dated, etc.) are cached
//         forever. Once we've resolved them once, the on-disk
//         pointer is treated as authoritative — matches the
//         convention that "v1.2.3" doesn't move.
//   - Tag list, catalog, referrers: TTL-bounded.
type Remote struct {
	namespace string
	name      string
	store     *Store
	client    *upstream.Client
	upstreamURL string

	// pathPrefix is prepended to the {name} path component when
	// fetching from upstream. Used for Docker Hub's official
	// "library/" prefix on bare names (e.g. user requests "nginx"
	// → upstream sees "library/nginx").
	pathPrefix string

	mutableTTL time.Duration
	// floatingTags is the resolved set of "treat as mutable" tag
	// names. Membership lookups are case-insensitive so the
	// operator can write "Latest" without surprises. The special
	// value "*" makes every tag floating (pre-FloatingTags
	// behaviour); detected via floatingAll = true.
	floatingTags map[string]struct{}
	floatingAll  bool
	sf           *common.Singleflight

	// tokenCache holds bearer tokens per scope, keyed by the scope
	// string the upstream demanded.
	tokenMu sync.Mutex
	tokens  map[string]cachedToken
}

type cachedToken struct {
	value   string
	expires time.Time
}

// NewRemoteFactory returns the Factory for ("docker", "remote") repos.
func NewRemoteFactory() registry.Factory {
	return func(_ context.Context, deps registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		// Docker needs the CAS blob store in addition to the raw
		// mount handle — BuildRemote covers the raw handle, the
		// upstream client (with the longer 5min timeout blob
		// fetches need), and the TTL; we layer the blobstore on
		// top here.
		b, err := upstream.BuildRemote(deps, "docker/remote", ns, r, 5*time.Minute,
			upstream.RemoteBuildOptions{ClientTimeout: 5 * time.Minute})
		if err != nil {
			return nil, err
		}
		blobs, err := deps.MountFor(r.Mount, r.BasePath)
		if err != nil {
			return nil, fmt.Errorf("docker/remote %s/%s: blobstore: %w", ns, r.Name, err)
		}
		// Detect Docker Hub and inject the "library/" prefix
		// automatically. Heuristic: when the upstream URL is the
		// canonical Docker Hub registry endpoint.
		pathPrefix := ""
		if strings.Contains(r.URL, "registry-1.docker.io") || strings.Contains(r.URL, "registry.docker.io") {
			pathPrefix = "library"
		}
		floating, floatAll := buildFloatingTagSet(r.FloatingTags)
		return &Remote{
			namespace:    ns,
			name:         r.Name,
			store:        NewStore(b.FS, blobs, b.BasePath),
			client:       b.Client,
			upstreamURL:  strings.TrimRight(r.URL, "/"),
			pathPrefix:   pathPrefix,
			mutableTTL:   b.MutableTTL,
			floatingTags: floating,
			floatingAll:  floatAll,
			sf:           common.NewSingleflight(),
			tokens:       make(map[string]cachedToken),
		}, nil
	}
}

func (rr *Remote) Namespace() string { return rr.namespace }
func (rr *Remote) Name() string      { return rr.name }
func (rr *Remote) Type() string      { return service.RegistryTypeDocker }
func (rr *Remote) Kind() string      { return service.RegistryKindRemote }
func (rr *Remote) Store() *Store     { return rr.store }
func (rr *Remote) Close() error {
	if rr.client != nil {
		return rr.client.Close()
	}
	return nil
}

// PackageDetail implements registry.PackageDetailer against the
// cached manifests/tags this Remote has pulled.
func (rr *Remote) PackageDetail(ctx context.Context, name string) (*registry.PackageDetail, error) {
	return buildPackageDetail(ctx, rr.store, name)
}

// ProbeUpstream implements registry.UpstreamProber. /v2/ is the
// OCI Distribution challenge endpoint every spec-compliant
// registry implements; a 200 (or 401 with Bearer challenge)
// confirms reachability.
func (rr *Remote) ProbeUpstream(ctx context.Context) (registry.UpstreamHealth, error) {
	return upstream.Probe(ctx, rr.client, "/v2/"), nil
}

// upstreamName applies the optional path prefix to a logical repo
// name. "nginx" → "library/nginx" for Docker Hub.
func (rr *Remote) upstreamName(name string) string {
	if rr.pathPrefix == "" || strings.Contains(name, "/") {
		return name
	}
	return rr.pathPrefix + "/" + name
}

// ServeHTTP dispatches a request. Writes (PUT/PATCH/POST/DELETE)
// are rejected with 405.
func (rr *Remote) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, ok := classify(r.Method, r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "UNSUPPORTED", "unrecognised docker route")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "DENIED", "remote registry is read-only")
		return
	}

	switch req.Op {
	case opVersionProbe:
		// We don't proxy the upstream's auth challenge; pika's own
		// auth has already been validated. Return the canonical
		// 200 + API-Version header.
		w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
		w.WriteHeader(http.StatusOK)
	case opCatalog:
		// Catalog over a public registry is rarely supported; we
		// surface what's currently cached locally. A future
		// enhancement could proxy the upstream's /v2/_catalog when
		// available.
		rr.serveCachedCatalog(w, r)
	case opTagsList:
		rr.serveTagsList(w, r, req.Name)
	case opManifest:
		rr.serveManifest(w, r, req)
	case opBlob:
		rr.serveBlob(w, r, req)
	case opReferrers:
		rr.serveReferrers(w, r, req)
	default:
		writeError(w, http.StatusNotFound, "UNSUPPORTED", "operation not supported on remote")
	}
}

// serveCachedCatalog reports what's currently in the local cache.
func (rr *Remote) serveCachedCatalog(w http.ResponseWriter, _ *http.Request) {
	repos, err := rr.store.ListRepositories()
	if err != nil {
		mapError(w, err)
		return
	}
	body, _ := json.Marshal(map[string]any{"repositories": repos})
	writeOK(w, "application/json", body)
}

// serveTagsList tries upstream first (subject to MutableTTL), falls
// back to the local cache on upstream errors.
func (rr *Remote) serveTagsList(w http.ResponseWriter, r *http.Request, name string) {
	cachePath := rr.store.repoDir(name) + "/_tags_list.json"
	if rr.cachedFresh(cachePath) {
		if body, ok := rr.readCacheFile(cachePath); ok {
			writeOK(w, "application/json", body)
			return
		}
	}
	key := "tags:" + name
	_, _, _ = rr.sf.Do(key, func() (any, error) {
		return nil, rr.refetchTagsList(r.Context(), name, cachePath)
	})
	if body, ok := rr.readCacheFile(cachePath); ok {
		writeOK(w, "application/json", body)
		return
	}
	writeError(w, http.StatusNotFound, "NAME_UNKNOWN", "tags unavailable")
}

func (rr *Remote) refetchTagsList(ctx context.Context, name, cachePath string) error {
	urlPath := "/v2/" + rr.upstreamName(name) + "/tags/list"
	resp, err := rr.upstreamGet(ctx, urlPath, "repository:"+rr.upstreamName(name)+":pull")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return rr.writeCacheFile(cachePath, body)
}

// serveManifest handles GET/HEAD manifests by tag or digest.
// Digest references are immutable so we serve from cache when
// available; tag references go through the TTL-bounded cache.
func (rr *Remote) serveManifest(w http.ResponseWriter, r *http.Request, req parsedRequest) {
	if !req.Digest.IsZero() {
		rr.serveManifestByDigest(w, r, req.Name, req.Digest)
		return
	}
	rr.serveManifestByTag(w, r, req.Name, req.Ref)
}

func (rr *Remote) serveManifestByDigest(w http.ResponseWriter, r *http.Request, name string, dgst blobstore.Digest) {
	// Cache hit.
	if rec, err := rr.store.ReadManifest(name, dgst); err == nil {
		writeManifestResponse(w, r, rec)
		return
	}
	// Cache miss → fetch + store.
	key := "manifest:" + name + ":" + dgst.String()
	_, _, _ = rr.sf.Do(key, func() (any, error) {
		return nil, rr.refetchManifest(r.Context(), name, dgst.String())
	})
	if rec, err := rr.store.ReadManifest(name, dgst); err == nil {
		writeManifestResponse(w, r, rec)
		return
	}
	writeError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest unknown upstream")
}

func (rr *Remote) serveManifestByTag(w http.ResponseWriter, r *http.Request, name, tag string) {
	// Classify the tag. Floating tags (operator-declared "latest",
	// "main", "nightly", …) are TTL-bounded; everything else is
	// served from local cache forever once we've successfully
	// resolved it. The split matches the upstream convention that
	// semver tags don't move while floating tags do.
	floating := rr.isFloatingTag(tag)

	// Cache-hit fast path. For non-floating tags, the mere
	// presence of a tag→digest pointer is enough — no freshness
	// check. For floating tags, honour MutableTTL.
	tagPath := rr.store.tagPath(name, tag)
	cacheUsable := false
	if floating {
		cacheUsable = rr.cachedFresh(tagPath)
	} else {
		cacheUsable = rr.tagPointerExists(tagPath)
	}
	if cacheUsable {
		if dgst, err := rr.store.ReadTag(name, tag); err == nil {
			if rec, err := rr.store.ReadManifest(name, dgst); err == nil {
				writeManifestResponse(w, r, rec)
				return
			}
		}
	}

	// Cache miss or stale floating tag — go to upstream.
	key := "manifest:" + name + ":tag:" + tag
	_, _, _ = rr.sf.Do(key, func() (any, error) {
		return nil, rr.refetchManifest(r.Context(), name, tag)
	})
	if dgst, err := rr.store.ReadTag(name, tag); err == nil {
		if rec, err := rr.store.ReadManifest(name, dgst); err == nil {
			writeManifestResponse(w, r, rec)
			return
		}
	}
	writeError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "tag unresolvable")
}

// tagPointerExists reports whether the on-disk tag pointer file
// is present, regardless of its age. Used by the non-floating-tag
// path which treats any prior successful resolve as authoritative.
func (rr *Remote) tagPointerExists(path string) bool {
	if _, err := rr.store.RawFS().Stat(path); err == nil {
		return true
	}
	return false
}

// Stats implements registry.StatsProvider. Returns the same shape
// as Docker Local — for a Remote the numbers reflect what pika has
// cached locally rather than what the upstream catalog reports.
func (rr *Remote) Stats(_ context.Context) (registry.Stats, error) {
	repos, tags, manifests := rr.store.CountRepositoriesTagsManifests()
	var (
		blobCount  int
		totalBytes int64
	)
	_ = rr.store.Blobs().ListBlobs(func(_ blobstore.Digest, info *blobstore.BlobInfo) error {
		blobCount++
		if info != nil {
			totalBytes += info.Size
		}
		return nil
	})
	return registry.Stats{
		RepositoryCount: repos,
		TagCount:        tags,
		ManifestCount:   manifests,
		BlobCount:       blobCount,
		TotalBytes:      totalBytes,
	}, nil
}

// PurgeCache implements registry.CachePurger for Docker Remote.
//
// opts.All=false (default, "mutable scope"): delete the cached
// /v2/_catalog and per-repo /tags/list bodies, plus every floating
// tag → digest pointer. Non-floating tag pointers, manifests by
// digest, blob layers and the referrers index are kept (they're
// content-addressed and rarely benefit from invalidation).
//
// opts.All=true ("nuclear"): also delete every manifest and the
// entire blob store contents. Used when the operator suspects
// cache corruption; forces a full re-download on the next pull.
func (rr *Remote) PurgeCache(_ context.Context, opts registry.PurgeOptions) (registry.PurgeStats, error) {
	wfs, ok := rr.store.RawFS().(rawfs.WritableRawFS)
	if !ok {
		return registry.PurgeStats{}, fmt.Errorf("docker/remote: backend is read-only")
	}
	var (
		count int
		bytes int64
		errs  []error
	)

	// Step 1: per-repo mutable artifacts (tags/list cache + floating
	// tag pointers). Walk the repository directory tree.
	repos, _ := rr.store.ListRepositories()
	for _, name := range repos {
		// Tag list cache.
		tagsListPath := rr.store.repoDir(name) + "/_tags_list.json"
		if fi, err := rr.store.RawFS().Stat(tagsListPath); err == nil {
			if delErr := wfs.Delete(tagsListPath); delErr == nil {
				count++
				bytes += fi.Size
			}
		}
		// Floating tag pointers — read ListTags then filter by
		// classification. With opts.All=true every tag pointer
		// is purged (matches the "force re-resolve everything"
		// intent).
		tags, _ := rr.store.ListTags(name)
		for _, tag := range tags {
			if !opts.All && !rr.isFloatingTag(tag) {
				continue
			}
			tp := rr.store.tagPath(name, tag)
			if fi, err := rr.store.RawFS().Stat(tp); err == nil {
				if delErr := wfs.Delete(tp); delErr == nil {
					count++
					bytes += fi.Size
				} else if !isDockerNotFound(delErr) {
					errs = append(errs, fmt.Errorf("delete %s: %w", tp, delErr))
				}
			}
		}
	}

	// Step 2: with opts.All=true, drop the deeper cache layers too.
	// Manifests + blob store. These are immutable upstream-side so
	// the only reason to wipe them is corruption recovery.
	if opts.All {
		for _, name := range repos {
			manifests := path.Join(rr.store.repoDir(name), "manifests")
			entries, _ := rr.store.RawFS().ReadDir(manifests)
			for _, e := range entries {
				if e.IsDir {
					continue
				}
				p := path.Join(manifests, e.Name)
				if fi, err := rr.store.RawFS().Stat(p); err == nil {
					if delErr := wfs.Delete(p); delErr == nil {
						count++
						bytes += fi.Size
					}
				}
			}
		}
		// Blob store wipe: walk every blob and delete. The
		// BlobStore.ListBlobs callback gives us the size for free
		// so we don't need a separate Stat per entry.
		_ = rr.store.Blobs().ListBlobs(func(d blobstore.Digest, info *blobstore.BlobInfo) error {
			if err := rr.store.Blobs().Delete(d); err == nil {
				count++
				if info != nil {
					bytes += info.Size
				}
			} else if !isDockerNotFound(err) {
				errs = append(errs, fmt.Errorf("delete blob %s: %w", d, err))
			}
			return nil
		})
	}

	out := registry.PurgeStats{
		PurgedFiles: count,
		PurgedBytes: bytes,
	}
	for _, e := range errs {
		out.Errors = append(out.Errors, e.Error())
	}
	return out, nil
}

// isDockerNotFound recognises rawfs / BlobStore not-found errors
// uniformly. Used by purge so a concurrent push that races a delete
// doesn't surface a spurious error.
func isDockerNotFound(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "not found") ||
		strings.Contains(low, "no such file") ||
		strings.Contains(low, "does not exist")
}

// isFloatingTag reports whether the operator has declared the tag
// as mutable. Lookup is case-insensitive and the special "*"
// marker (configured at construction) makes every tag floating.
func (rr *Remote) isFloatingTag(tag string) bool {
	if rr.floatingAll {
		return true
	}
	if len(rr.floatingTags) == 0 {
		return false
	}
	_, ok := rr.floatingTags[strings.ToLower(tag)]
	return ok
}

// buildFloatingTagSet normalises the operator-provided list of
// floating-tag names into a case-insensitive lookup set. The empty
// input falls back to a reasonable default (the names every CI
// pipeline pushes mutably); "*" anywhere in the list flips the
// "all tags float" switch.
//
// Returns (set, floatAll). When floatAll is true the set is
// ignored — the caller short-circuits every lookup.
func buildFloatingTagSet(input []string) (map[string]struct{}, bool) {
	// Default applied only when the operator hasn't configured
	// anything. An explicitly empty list is honoured as "no tags
	// are floating" (every tag cached forever), useful for repos
	// where the operator has audited the upstream and knows tags
	// are write-once.
	const defaultList = "latest,main,master,dev,develop,nightly,edge,stable,canary"
	var source []string
	if len(input) == 0 {
		source = strings.Split(defaultList, ",")
	} else {
		source = input
	}
	out := make(map[string]struct{}, len(source))
	for _, t := range source {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if t == "*" {
			return nil, true
		}
		out[strings.ToLower(t)] = struct{}{}
	}
	return out, false
}

// refetchManifest pulls a manifest from upstream, computes its
// digest, writes it to the store, and updates the tag pointer when
// ref is a tag.
func (rr *Remote) refetchManifest(ctx context.Context, name, ref string) error {
	urlPath := "/v2/" + rr.upstreamName(name) + "/manifests/" + ref
	resp, err := rr.upstreamGetAccept(ctx, urlPath, "repository:"+rr.upstreamName(name)+":pull", manifestAcceptHeader)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	// Compute digest from the body bytes.
	h, _ := blobstore.NewHasher("sha256")
	_, _ = h.Write(body)
	dgst := blobstore.DigestFromHash("sha256", h)

	contentType := resp.ContentType
	if contentType == "" {
		contentType = "application/vnd.oci.image.manifest.v1+json"
	}
	if err := rr.store.WriteManifest(name, dgst, body, contentType); err != nil {
		return err
	}
	// Update tag pointer when ref isn't a digest.
	if !IsDigestReference(ref) {
		_ = rr.store.SetTag(name, ref, dgst)
	}
	// OCI v1.1 referrers index — if this manifest has a subject,
	// register us as a referrer of that subject. Same logic as
	// Local push.
	if insp := inspectManifest(body); insp != nil && insp.Subject != nil && insp.Subject.Digest != "" {
		desc := manifestDescriptor{
			MediaType:    contentType,
			ArtifactType: insp.effectiveArtifactType(),
			Digest:       dgst.String(),
			Size:         int64(len(body)),
			Annotations:  insp.Annotations,
		}
		_ = rr.store.AddReferrer(name, insp.Subject.Digest, desc)
	}
	return nil
}

// manifestAcceptHeader lists the manifest media types we ask
// upstream to consider. The order matches what the Docker / OCI
// clients use: prefer image index → docker manifest list → OCI
// image manifest → docker v2 manifest.
var manifestAcceptHeader = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

// serveBlob handles GET/HEAD blob requests. Blob fetches stream
// the body to disk (via BlobStore Upload) and then serve from
// cache. Subsequent reads avoid the upstream entirely.
func (rr *Remote) serveBlob(w http.ResponseWriter, r *http.Request, req parsedRequest) {
	if info, err := rr.store.Blobs().Stat(req.Digest); err == nil {
		writeBlobFromStore(w, r, rr.store.Blobs(), req.Digest, info)
		return
	}
	key := "blob:" + req.Name + ":" + req.Digest.String()
	_, _, _ = rr.sf.Do(key, func() (any, error) {
		return nil, rr.refetchBlob(r.Context(), req.Name, req.Digest)
	})
	if info, err := rr.store.Blobs().Stat(req.Digest); err == nil {
		writeBlobFromStore(w, r, rr.store.Blobs(), req.Digest, info)
		return
	}
	writeError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob unknown upstream")
}

// refetchBlob streams a blob from upstream into the BlobStore.
func (rr *Remote) refetchBlob(ctx context.Context, name string, dgst blobstore.Digest) error {
	urlPath := "/v2/" + rr.upstreamName(name) + "/blobs/" + dgst.String()
	resp, err := rr.upstreamGet(ctx, urlPath, "repository:"+rr.upstreamName(name)+":pull")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sess, err := rr.store.Blobs().StartUpload()
	if err != nil {
		return err
	}
	if _, err := io.Copy(sess, resp.Body); err != nil {
		_ = sess.Cancel()
		return err
	}
	if _, err := sess.Commit(dgst); err != nil {
		if errors.Is(err, blobstore.ErrDigestMismatch) {
			// Upstream returned bytes that don't match the
			// requested digest. Almost certainly a registry bug
			// or MITM — refuse to cache.
			return fmt.Errorf("upstream digest mismatch: %w", err)
		}
		return err
	}
	return nil
}

// serveReferrers proxies the upstream's referrers API when our
// local cache doesn't carry an entry. Most upstream registries
// support the OCI v1.1 referrers endpoint natively; for those that
// don't, we fall back to the (possibly empty) local index.
func (rr *Remote) serveReferrers(w http.ResponseWriter, r *http.Request, req parsedRequest) {
	// Local lookup is cheap; check first.
	idx, _ := rr.store.ReadReferrers(req.Name, req.Digest.String())
	if len(idx.Manifests) > 0 {
		body, _ := json.Marshal(idx)
		writeOK(w, "application/vnd.oci.image.index.v1+json", body)
		return
	}
	// Try upstream. If the registry doesn't support v1.1 referrers,
	// upstream responds 404; we then fall back to the empty local
	// index (200 OK).
	urlPath := "/v2/" + rr.upstreamName(req.Name) + "/referrers/" + req.Digest.String()
	resp, err := rr.upstreamGet(r.Context(), urlPath, "repository:"+rr.upstreamName(req.Name)+":pull")
	if err == nil {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		writeOK(w, "application/vnd.oci.image.index.v1+json", body)
		return
	}
	// Fall back to local (empty) index.
	body, _ := json.Marshal(idx)
	writeOK(w, "application/vnd.oci.image.index.v1+json", body)
}

// ─── Upstream auth + fetch ─────────────────────────────────────────

// upstreamGet performs an authenticated GET against the upstream.
// scope identifies the registry resource being accessed; the
// helper uses it as the cache key and challenge-response scope.
func (rr *Remote) upstreamGet(ctx context.Context, urlPath, scope string) (*upstream.Response, error) {
	return rr.upstreamGetAccept(ctx, urlPath, scope, "")
}

// upstreamGetAccept extends upstreamGet with an explicit Accept
// header. Used by manifest fetches to negotiate the manifest media
// type with the upstream.
func (rr *Remote) upstreamGetAccept(ctx context.Context, urlPath, scope, accept string) (*upstream.Response, error) {
	// First try without a token (upstream may permit anonymous
	// reads for public repos).
	resp, err := rr.client.Get(ctx, urlPath)
	if err == nil {
		return resp, nil
	}
	if !errors.Is(err, upstream.ErrUnauthorized) {
		return nil, err
	}

	// Mint or reuse a bearer token for the requested scope.
	token, err := rr.getBearerToken(ctx, scope)
	if err != nil {
		return nil, err
	}
	return rr.fetchWithBearer(ctx, urlPath, token, accept)
}

// getBearerToken returns a valid bearer for the scope, minting a
// fresh one when the cache misses or the cached token is near
// expiry.
func (rr *Remote) getBearerToken(ctx context.Context, scope string) (string, error) {
	rr.tokenMu.Lock()
	cached, ok := rr.tokens[scope]
	rr.tokenMu.Unlock()
	if ok && time.Until(cached.expires) > 30*time.Second {
		return cached.value, nil
	}

	// Probe upstream's /v2/ endpoint to harvest the realm + service
	// from the WWW-Authenticate header. We cache the probe result
	// in a single shared "" scope entry, but most upstreams emit
	// the same realm+service across scopes so we don't actually
	// need to re-probe per scope.
	realm, service, err := rr.probeChallenge(ctx)
	if err != nil {
		return "", err
	}

	// Fetch a token from <realm>?service=<svc>&scope=<scope>.
	tokenURL := realm + "?service=" + service + "&scope=" + scope
	resp, err := rr.client.Get(ctx, tokenURL)
	if err != nil {
		return "", fmt.Errorf("token fetch: %w", err)
	}
	defer resp.Body.Close()
	var tokenResp struct {
		Token        string `json:"token"`
		AccessToken  string `json:"access_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("token decode: %w", err)
	}
	value := tokenResp.Token
	if value == "" {
		value = tokenResp.AccessToken
	}
	if value == "" {
		return "", fmt.Errorf("upstream returned empty token")
	}
	lifetime := time.Duration(tokenResp.ExpiresIn) * time.Second
	if lifetime <= 0 {
		lifetime = 5 * time.Minute
	}
	rr.tokenMu.Lock()
	rr.tokens[scope] = cachedToken{value: value, expires: time.Now().Add(lifetime)}
	rr.tokenMu.Unlock()
	return value, nil
}

// probeChallenge does GET upstream/v2/ and parses the
// WWW-Authenticate header to discover the realm + service. Cached
// so we don't probe on every token mint.
func (rr *Remote) probeChallenge(ctx context.Context) (realm, service string, err error) {
	rr.tokenMu.Lock()
	if cached, ok := rr.tokens["__challenge__"]; ok {
		rr.tokenMu.Unlock()
		// Split "realm|service" off the cached value.
		parts := strings.SplitN(cached.value, "|", 2)
		if len(parts) == 2 {
			return parts[0], parts[1], nil
		}
	}
	rr.tokenMu.Unlock()

	// Build a one-shot http.Request via the underlying http client
	// (we need access to the response headers on a 401, which the
	// upstream.Client wrapper hides). Use net/http directly.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rr.upstreamURL+"/v2/", nil)
	if err != nil {
		return "", "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("probe: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return "", "", fmt.Errorf("probe expected 401, got %d", resp.StatusCode)
	}
	auth := resp.Header.Get("WWW-Authenticate")
	realm, service = parseChallenge(auth)
	if realm == "" {
		return "", "", fmt.Errorf("missing realm in WWW-Authenticate")
	}
	rr.tokenMu.Lock()
	rr.tokens["__challenge__"] = cachedToken{
		value:   realm + "|" + service,
		expires: time.Now().Add(24 * time.Hour),
	}
	rr.tokenMu.Unlock()
	return realm, service, nil
}

// parseChallenge extracts realm and service from a WWW-Authenticate
// Bearer header. Format:
//
//	Bearer realm="https://auth.docker.io/token",service="registry.docker.io"
func parseChallenge(header string) (realm, service string) {
	if !strings.HasPrefix(header, "Bearer ") {
		return "", ""
	}
	body := header[len("Bearer "):]
	for _, kv := range splitChallenge(body) {
		k, v, _ := strings.Cut(kv, "=")
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch strings.TrimSpace(k) {
		case "realm":
			realm = v
		case "service":
			service = v
		}
	}
	return realm, service
}

// splitChallenge splits the body of a Bearer challenge into
// comma-separated key=value pairs without breaking quoted commas.
func splitChallenge(s string) []string {
	var out []string
	inQuote := false
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			inQuote = !inQuote
			continue
		}
		if c == ',' && !inQuote {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// fetchWithBearer reissues a GET against the upstream with the
// given bearer token + optional Accept header.
func (rr *Remote) fetchWithBearer(ctx context.Context, urlPath, token, accept string) (*upstream.Response, error) {
	// Use net/http directly because upstream.Client doesn't expose
	// a per-call header injection point. Mirrors what upstream.Get
	// does internally.
	url := rr.upstreamURL + urlPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", upstream.ErrTransient, err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		resp.Body.Close()
		return nil, upstream.ErrNotFound
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		resp.Body.Close()
		return nil, upstream.ErrUnauthorized
	case resp.StatusCode >= 500:
		resp.Body.Close()
		return nil, fmt.Errorf("upstream %d: %w", resp.StatusCode, upstream.ErrTransient)
	case resp.StatusCode >= 400:
		resp.Body.Close()
		return nil, fmt.Errorf("upstream %d", resp.StatusCode)
	}
	return &upstream.Response{
		StatusCode:    resp.StatusCode,
		ContentType:   resp.Header.Get("Content-Type"),
		ContentLength: resp.ContentLength,
		ETag:          resp.Header.Get("ETag"),
		Header:        resp.Header,
		Body:          resp.Body,
	}, nil
}

// ─── Cache helpers ─────────────────────────────────────────────────

// cachedFresh reports whether a rawfs file exists and is within
// mutableTTL.
func (rr *Remote) cachedFresh(path string) bool {
	if rr.mutableTTL <= 0 {
		return false
	}
	fi, err := rr.store.RawFS().Stat(path)
	if err != nil {
		return false
	}
	return time.Since(fi.ModTime) < rr.mutableTTL
}

// readCacheFile reads a small cache file via rawfs.
func (rr *Remote) readCacheFile(path string) ([]byte, bool) {
	rc, _, err := rr.store.RawFS().Open(path)
	if err != nil {
		return nil, false
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return nil, false
	}
	return body, true
}

func (rr *Remote) writeCacheFile(path string, body []byte) error {
	wfs, ok := rr.store.RawFS().(interface {
		Write(string, io.Reader, int64) error
	})
	if !ok {
		return errors.New("backend read-only")
	}
	return wfs.Write(path, bytes.NewReader(body), int64(len(body)))
}

// ─── Response writers ──────────────────────────────────────────────

// writeManifestResponse emits a manifest record to the client.
func writeManifestResponse(w http.ResponseWriter, r *http.Request, rec *ManifestRecord) {
	w.Header().Set("Content-Type", rec.ContentType)
	w.Header().Set("Docker-Content-Digest", rec.Digest.String())
	w.Header().Set("Content-Length", strconv.Itoa(len(rec.Body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(rec.Body)
}

// writeBlobFromStore streams a cached blob.
func writeBlobFromStore(w http.ResponseWriter, r *http.Request, bs blobstore.BlobStore, dgst blobstore.Digest, info *blobstore.BlobInfo) {
	rc, _, err := bs.Get(dgst)
	if err != nil {
		writeError(w, http.StatusNotFound, "BLOB_UNKNOWN", err.Error())
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Docker-Content-Digest", dgst.String())
	if info != nil && info.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, rc)
}
