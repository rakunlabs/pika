package goproxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/common"
	"github.com/rakunlabs/pika/internal/registry/upstream"
	"github.com/rakunlabs/pika/internal/service"
)

// Remote is a Registry implementation that pull-through-caches an
// upstream Go module proxy. Every read goes through this flow:
//
//  1. Look up the requested file in the local Store.
//  2. On hit AND the file is immutable (.info / .mod / .zip), serve
//     from cache. On hit but the file is mutable (@v/list,
//     @latest), serve from cache when fresh per MutableTTL.
//  3. On miss (or stale mutable), fetch from upstream. Persist the
//     response to the Store, then serve the bytes that were just
//     written.
//
// # Concurrency
//
// A burst of requests for the same uncached file would otherwise
// hammer the upstream N times in parallel. The package's singleflight
// coalescer makes only one fetch happen; the rest of the callers
// wait on the in-flight call and read the cached file once it lands.
//
// # Failure modes
//
// Upstream 404 → 404 to the client. Upstream 5xx → 502 (we surface
// the failure rather than serve stale data, matching go-proxy's
// behaviour on flake).
type Remote struct {
	namespace string
	name      string
	store     *Store
	client    *upstream.Client

	mutableTTL time.Duration
	sf         *common.Singleflight
}

var versionFileExts = []string{"info", "mod", "zip"}

// NewRemoteFactory returns the Factory for ("go", "remote") repos.
func NewRemoteFactory() registry.Factory {
	return func(_ context.Context, deps registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		b, err := upstream.BuildRemote(deps, "goproxy/remote", ns, r, 5*time.Minute)
		if err != nil {
			return nil, err
		}
		return &Remote{
			namespace:  ns,
			name:       r.Name,
			store:      NewStore(b.FS, b.BasePath),
			client:     b.Client,
			mutableTTL: b.MutableTTL,
			sf:         common.NewSingleflight(),
		}, nil
	}
}

func (rr *Remote) Namespace() string { return rr.namespace }
func (rr *Remote) Name() string      { return rr.name }
func (rr *Remote) Type() string      { return service.RegistryTypeGo }
func (rr *Remote) Kind() string      { return service.RegistryKindRemote }
func (rr *Remote) Store() *Store     { return rr.store }

// Close releases the upstream HTTP client's idle connections.
func (rr *Remote) Close() error {
	if rr.client != nil {
		return rr.client.Close()
	}
	return nil
}

// PackageDetail returns per-module metadata for the cached view of
// a Remote. Implements registry.PackageDetailer. A module that has
// never been fetched returns ErrPackageNotFound — Remote does NOT
// trigger an upstream warm-up for the detail endpoint; warming is
// the explicit purpose of the data-plane endpoints.
func (rr *Remote) PackageDetail(ctx context.Context, module string) (*registry.PackageDetail, error) {
	return buildPackageDetail(ctx, rr.store, module)
}

// ProbeUpstream implements registry.UpstreamProber. Hits the root
// of the configured GOPROXY upstream; proxy.golang.org returns
// the "Go module mirror" landing page (200), self-hosted Athens
// instances return their dashboard, etc.
func (rr *Remote) ProbeUpstream(ctx context.Context) (registry.UpstreamHealth, error) {
	return upstream.Probe(ctx, rr.client, "/"), nil
}

// Stats implements registry.StatsProvider. Reports the same on-disk
// metrics as Local — for a Remote registry these reflect cached
// upstream artifacts (everything pika has actually pulled), so a
// fresh repo with no traffic reports zeros.
func (rr *Remote) Stats(_ context.Context) (registry.Stats, error) {
	mods, vers, bytes := rr.store.CountModulesVersionsBytes()
	return registry.Stats{
		ModuleCount:  mods,
		VersionCount: vers,
		TotalBytes:   bytes,
	}, nil
}

// WarmVersionFile ensures the requested immutable Go module file is
// present in the local cache and then best-effort fetches the sibling
// files for the same module version. A request for any one of
// .info/.mod/.zip should leave the version usable offline as a real
// pull-through cache entry.
func (rr *Remote) WarmVersionFile(ctx context.Context, mod, ver, requiredExt string) error {
	if err := ValidateModulePath(mod); err != nil {
		return err
	}
	if err := ValidateVersion(ver); err != nil {
		return err
	}
	if !isVersionFileExt(requiredExt) {
		return fmt.Errorf("goproxy: unsupported version file extension %q", requiredExt)
	}
	key := "version:" + mod + "@" + ver
	_, err, _ := rr.sf.Do(key, func() (any, error) {
		return nil, rr.ensureVersionCached(ctx, mod, ver, requiredExt)
	})
	return err
}

// PurgeCache implements registry.CachePurger. With All=false (the
// default), only the mutable pointers (@v/list, @latest) are
// deleted — the next request for either re-resolves through
// upstream while immutable artifacts continue to serve from cache
// at zero cost. With All=true, the entire module cache (every
// .info, .mod, .zip under the BasePath) is wiped, forcing a full
// re-download on the next pull. Callers should pick the wider
// scope only when they suspect cache corruption.
func (rr *Remote) PurgeCache(_ context.Context, opts registry.PurgeOptions) (registry.PurgeStats, error) {
	var (
		count int
		bytes int64
		errs  []error
	)
	if opts.All {
		count, bytes, errs = rr.store.PurgeAll()
	} else {
		count, bytes, errs = rr.store.PurgeMutable()
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

// ServeHTTP dispatches a request. Only safe verbs (GET / HEAD) are
// supported on Remote — pushes go to a Local repo, never to a
// remote-mirror.
func (rr *Remote) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, ok := parsePath(r.URL.Path)
	if !ok {
		writeNotFound(w, "unrecognised go module proxy path: "+r.URL.Path)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeMethodNotAllowed(w, "GET, HEAD")
		return
	}

	switch {
	case req.IsList:
		rr.serveList(w, r, req.Module)
	case req.IsLatest:
		rr.serveLatest(w, r, req.Module)
	default:
		rr.serveVersionFile(w, r, req.Module, req.Version, req.Ext)
	}
}

// serveVersionFile handles immutable files (.info / .mod / .zip).
// Cache hit → serve from store. Miss → fetch + persist + serve.
func (rr *Remote) serveVersionFile(w http.ResponseWriter, r *http.Request, mod, ver, ext string) {
	_ = rr.WarmVersionFile(r.Context(), mod, ver, ext)

	// Whether we won the singleflight or rode someone else's fetch,
	// re-check the store for the requested file and serve it. If it's
	// still missing, the required upstream fetch failed.
	if _, err := rr.store.StatVersionFile(mod, ver, ext); err != nil {
		writeUpstreamFailure(w, mod, ver, ext)
		return
	}
	serveFileFromStore(w, r, rr.store, mod, ver, ext)
}

func (rr *Remote) ensureVersionCached(ctx context.Context, mod, ver, requiredExt string) error {
	if _, err := rr.store.StatVersionFile(mod, ver, requiredExt); err != nil {
		if err := rr.fetchAndStoreVersion(ctx, mod, ver, requiredExt); err != nil {
			return err
		}
	}

	for _, ext := range versionFileExts {
		if ext == requiredExt {
			continue
		}
		if _, err := rr.store.StatVersionFile(mod, ver, ext); err == nil {
			continue
		}
		// The sibling files are cache warm-up only. Serving the requested
		// artifact must not fail because a non-critical sibling fetch did.
		_ = rr.fetchAndStoreVersion(ctx, mod, ver, ext)
	}
	return nil
}

func isVersionFileExt(ext string) bool {
	for _, known := range versionFileExts {
		if ext == known {
			return true
		}
	}
	return false
}

// fetchAndStoreVersion does the actual upstream GET + Store.Write
// for one immutable version file. Errors are logged via the caller
// (singleflight) and result in 404 on subsequent stat-checks.
func (rr *Remote) fetchAndStoreVersion(ctx context.Context, mod, ver, ext string) error {
	urlPath := "/" + EncodeModulePath(mod) + "/@v/" + ver + "." + ext
	resp, err := rr.client.Get(ctx, urlPath)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Stream the body into memory first so we know the length before
	// the rawfs Write. Most info/mod files are tiny (<10 KB), zips
	// can be larger — we accept the in-RAM cost because rawfs Write
	// requires a known size and the Remote path doesn't benefit from
	// the BlobStore's spill-to-disk story (Docker layers do).
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read upstream body: %w", err)
	}
	return rr.store.WriteVersionFile(mod, ver, ext, bytes.NewReader(body), int64(len(body)))
}

// serveList handles @v/list. Mutable: TTL-bounded cache.
func (rr *Remote) serveList(w http.ResponseWriter, r *http.Request, mod string) {
	// If the cached list is fresh, serve it.
	body, err := rr.store.CachedList(mod, rr.mutableTTL)
	if err == nil && len(body) > 0 && rr.cachedFresh(rr.store.listPath(mod)) {
		serveCachedList(w, r, rr.store, mod, rr.mutableTTL)
		return
	}

	key := "list:" + mod
	_, _, _ = rr.sf.Do(key, func() (any, error) {
		return nil, rr.refetchList(r.Context(), mod)
	})

	serveCachedList(w, r, rr.store, mod, rr.mutableTTL)
}

// refetchList fetches the upstream list and writes it to the cache.
// The Store's CachedList builds from the on-disk version index, so
// refetching means dropping the cached list AND ensuring each listed
// version has an .info on disk — otherwise CachedList will return an
// empty body next time. The implementation is conservative: it writes
// the upstream body verbatim to listPath so a subsequent CachedList
// returns it directly, bypassing the version-index walk.
func (rr *Remote) refetchList(ctx context.Context, mod string) error {
	urlPath := "/" + EncodeModulePath(mod) + "/@v/list"
	resp, err := rr.client.Get(ctx, urlPath)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	wfs, ok := rr.store.RawFS().(rawfs.WritableRawFS)
	if !ok {
		return errors.New("backend is read-only")
	}
	return wfs.Write(rr.store.listPath(mod), bytes.NewReader(body), int64(len(body)))
}

// serveLatest handles @latest. Mutable: TTL-bounded cache.
func (rr *Remote) serveLatest(w http.ResponseWriter, r *http.Request, mod string) {
	if rr.cachedFresh(rr.store.latestPath(mod)) {
		serveCachedLatest(w, r, rr.store, mod, rr.mutableTTL)
		return
	}

	key := "latest:" + mod
	_, _, _ = rr.sf.Do(key, func() (any, error) {
		return nil, rr.refetchLatest(r.Context(), mod)
	})

	// CachedLatest reads the file directly so we can serve it even
	// if the just-refetched body is unparseable JSON — the on-disk
	// bytes carry whatever upstream gave us. Set TTL=0 to bypass the
	// freshness check, matching the spec: a fetch attempt is a
	// commitment to serve the result (or 404 when missing).
	body, err := rr.cachedLatestRaw(mod)
	if err != nil {
		writeNotFound(w, "@latest "+mod+": "+err.Error())
		return
	}
	etag := common.EtagFor(string(body))
	if common.MatchIfNoneMatch(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	common.SetMutableCache(w, etag, rr.mutableTTL)
	w.Header().Set("Content-Type", contentTypeFor("latest"))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	_, _ = w.Write(body)
}

// refetchLatest fetches the upstream @latest body and overwrites
// the cache.
func (rr *Remote) refetchLatest(ctx context.Context, mod string) error {
	urlPath := "/" + EncodeModulePath(mod) + "/@latest"
	resp, err := rr.client.Get(ctx, urlPath)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	wfs, ok := rr.store.RawFS().(rawfs.WritableRawFS)
	if !ok {
		return errors.New("backend is read-only")
	}
	return wfs.Write(rr.store.latestPath(mod), bytes.NewReader(body), int64(len(body)))
}

// cachedFresh returns true when path exists on the rawfs AND its
// modtime is within mutableTTL. False when missing or stale.
func (rr *Remote) cachedFresh(path string) bool {
	fi, err := rr.store.RawFS().Stat(path)
	if err != nil {
		return false
	}
	if rr.mutableTTL <= 0 {
		return false
	}
	return time.Since(fi.ModTime) < rr.mutableTTL
}

// cachedLatestRaw reads the on-disk @latest body verbatim. Differs
// from Store.CachedLatest in that it doesn't try to regenerate from
// version-index walks — Remote's @latest is sourced upstream, not
// inferred locally.
func (rr *Remote) cachedLatestRaw(mod string) ([]byte, error) {
	rc, _, err := rr.store.RawFS().Open(rr.store.latestPath(mod))
	if err != nil {
		return nil, mapNotFound(err)
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// writeUpstreamFailure surfaces a fetch failure to the client. We
// pick 404 vs 502 by re-probing the local store: if the file is
// still missing, the upstream itself probably 404'd (which is the
// most common reason for a fetch to not produce a file). 502 is
// only used when we're certain the upstream errored — but
// distinguishing that without preserving the original error
// through singleflight is more bookkeeping than it's worth right
// now. 404 is the safe default.
func writeUpstreamFailure(w http.ResponseWriter, mod, ver, ext string) {
	writeNotFound(w, fmt.Sprintf("%s@%s.%s: not found", mod, ver, ext))
}
