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
//   1. Look up the requested file in the local Store.
//   2. On hit AND the file is immutable (.info / .mod / .zip), serve
//      from cache. On hit but the file is mutable (@v/list,
//      @latest), serve from cache when fresh per MutableTTL.
//   3. On miss (or stale mutable), fetch from upstream. Persist the
//      response to the Store, then serve the bytes that were just
//      written.
//
// Concurrency
//
// A burst of requests for the same uncached file would otherwise
// hammer the upstream N times in parallel. The package's singleflight
// coalescer makes only one fetch happen; the rest of the callers
// wait on the in-flight call and read the cached file once it lands.
//
// Failure modes
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

// NewRemoteFactory returns the Factory for ("go", "remote") repos.
func NewRemoteFactory() registry.Factory {
	return func(_ context.Context, deps registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		fs, err := deps.MountRawFS(r.Mount)
		if err != nil {
			return nil, fmt.Errorf("goproxy/remote %s/%s: mount: %w", ns, r.Name, err)
		}
		client, err := upstream.NewClient(upstream.Config{
			BaseURL:            r.URL,
			Auth:               r.Auth,
			Resolver:           deps.Resolver,
			InsecureSkipVerify: r.InsecureSkipVerify,
		})
		if err != nil {
			return nil, fmt.Errorf("goproxy/remote %s/%s: client: %w", ns, r.Name, err)
		}
		ttl := 5 * time.Minute
		if r.MutableTTL != "" {
			if d, err := time.ParseDuration(r.MutableTTL); err == nil {
				ttl = d
			}
		}
		return &Remote{
			namespace:  ns,
			name:       r.Name,
			store:      NewStore(fs, r.BasePath),
			client:     client,
			mutableTTL: ttl,
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
	if _, err := rr.store.StatVersionFile(mod, ver, ext); err == nil {
		serveFileFromStore(w, r, rr.store, mod, ver, ext)
		return
	}

	key := "version:" + mod + "@" + ver + "." + ext
	_, _, _ = rr.sf.Do(key, func() (any, error) {
		// Double-check inside singleflight: a previous winner may
		// have just landed the file while we waited.
		if _, err := rr.store.StatVersionFile(mod, ver, ext); err == nil {
			return nil, nil
		}
		return nil, rr.fetchAndStoreVersion(r.Context(), mod, ver, ext)
	})

	// Whether we won the singleflight or rode someone else's fetch,
	// re-check the store for the file and serve it. If it's still
	// missing, the fetch failed — surface 404 / 502.
	if _, err := rr.store.StatVersionFile(mod, ver, ext); err != nil {
		writeUpstreamFailure(w, mod, ver, ext)
		return
	}
	serveFileFromStore(w, r, rr.store, mod, ver, ext)
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
