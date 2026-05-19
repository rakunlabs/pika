package npm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/common"
	"github.com/rakunlabs/pika/internal/registry/upstream"
	"github.com/rakunlabs/pika/internal/service"
)

// Remote is a pull-through cache of an upstream NPM registry
// (registry.npmjs.org by default). Read flow:
//
//   1. GET /{pkg}: serve cached packument when fresh per MutableTTL.
//      On stale/miss, fetch from upstream, rewrite tarball URLs to
//      pika, cache the result, serve.
//
//   2. GET /{pkg}/-/{file}.tgz: serve from cache when present (immutable
//      after first fetch). On miss, fetch from upstream, write to
//      store, serve.
//
// Writes are rejected — operators publish to a Local repo, not a
// remote mirror.
type Remote struct {
	namespace string
	name      string
	store     *Store
	client    *upstream.Client

	mutableTTL time.Duration
	sf         *common.Singleflight
}

// NewRemoteFactory returns the Factory for ("npm", "remote") repos.
func NewRemoteFactory() registry.Factory {
	return func(_ context.Context, deps registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		b, err := upstream.BuildRemote(deps, "npm/remote", ns, r, 5*time.Minute)
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
func (rr *Remote) Type() string      { return service.RegistryTypeNPM }
func (rr *Remote) Kind() string      { return service.RegistryKindRemote }
func (rr *Remote) Store() *Store     { return rr.store }

func (rr *Remote) Close() error {
	if rr.client != nil {
		return rr.client.Close()
	}
	return nil
}

// PackageDetail implements registry.PackageDetailer against the
// Remote's cache. Like the goproxy Remote, this is a cache-only
// read — it never triggers an upstream packument fetch. Operators
// who want a warm view should `npm install` once first.
func (rr *Remote) PackageDetail(ctx context.Context, name string) (*registry.PackageDetail, error) {
	return buildPackageDetail(ctx, rr.store, name)
}

// ProbeUpstream implements registry.UpstreamProber. /-/ping is the
// canonical npm health endpoint; registry.npmjs.org returns 200,
// Verdaccio responds the same. Self-hosted Sonatype Nexus does
// not implement /-/ping but the probe surfaces that as a non-2xx
// status code rather than a hard failure.
func (rr *Remote) ProbeUpstream(ctx context.Context) (registry.UpstreamHealth, error) {
	return upstream.Probe(ctx, rr.client, "/-/ping"), nil
}

// Stats implements registry.StatsProvider. Reports cached package
// metadata + tarball footprint for the Remote repo.
func (rr *Remote) Stats(_ context.Context) (registry.Stats, error) {
	pkgs, vers, bytes := rr.store.CountPackagesVersionsBytes()
	return registry.Stats{
		PackageCount: pkgs,
		VersionCount: vers,
		TotalBytes:   bytes,
	}, nil
}

// PurgeCache implements registry.CachePurger. opts.All=false (the
// default) clears every cached packument so the next read re-fetches
// upstream metadata; tarballs are kept (content-addressed by sha512
// integrity). opts.All=true wipes the entire NPM cache including
// tarballs — picked when the operator suspects cache corruption.
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

// ServeHTTP dispatches.
func (rr *Remote) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req := classify(r.Method, r.URL.Path)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "remote registries are read-only")
		return
	}
	switch req.Op {
	case "packument":
		rr.servePackument(w, r, req.Pkg)
	case "tarball":
		rr.serveTarball(w, r, req.Pkg, req.File)
	case "whoami":
		serveWhoami(w, r)
	case "dist-tags":
		rr.serveDistTags(w, r, req.Pkg)
	default:
		writeNotFound(w, "unrecognised npm route on remote repo")
	}
}

// servePackument serves the cached packument or fetches from upstream.
func (rr *Remote) servePackument(w http.ResponseWriter, r *http.Request, name string) {
	if rr.packumentFresh(name) {
		body, ok, _ := rr.store.ReadCachedPackument(name)
		if ok && len(body) > 0 {
			rr.writePackument(w, r, body)
			return
		}
	}
	key := "packument:" + name
	_, _, _ = rr.sf.Do(key, func() (any, error) {
		return nil, rr.refetchPackument(r, name)
	})
	body, ok, _ := rr.store.ReadCachedPackument(name)
	if !ok || len(body) == 0 {
		writeNotFound(w, name+": not found upstream")
		return
	}
	rr.writePackument(w, r, body)
}

// refetchPackument pulls the packument from upstream, rewrites
// tarball URLs to pika, persists, and updates dist-tags.
func (rr *Remote) refetchPackument(r *http.Request, name string) error {
	resp, err := rr.client.Get(r.Context(), "/"+name)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var pkg map[string]any
	if err := json.Unmarshal(body, &pkg); err != nil {
		return fmt.Errorf("parse upstream packument: %w", err)
	}

	// Rewrite each version's dist.tarball URL.
	publicBase := inferPublicBase(r)
	if versions, ok := pkg["versions"].(map[string]any); ok {
		for ver, vm := range versions {
			meta, ok := vm.(map[string]any)
			if !ok {
				continue
			}
			_ = RewriteVersionMetaTarball(meta, name, publicBase)
			versions[ver] = meta
		}
	}
	// Persist dist-tags so they survive a packument cache eviction.
	if tags, ok := pkg["dist-tags"].(map[string]any); ok {
		strTags := make(map[string]string, len(tags))
		for k, v := range tags {
			if s, ok := v.(string); ok {
				strTags[k] = s
			}
		}
		_ = rr.store.WriteDistTags(name, strTags)
	}

	// Re-marshal and cache.
	rewritten, err := json.Marshal(pkg)
	if err != nil {
		return err
	}
	wfs, ok := rr.store.RawFS().(rawfs.WritableRawFS)
	if !ok {
		return errors.New("backend read-only")
	}
	return wfs.Write(rr.store.packumentCachePath(name), bytes.NewReader(rewritten), int64(len(rewritten)))
}

// packumentFresh reports whether the cached packument exists and
// is within mutableTTL.
func (rr *Remote) packumentFresh(name string) bool {
	if rr.mutableTTL <= 0 {
		return false
	}
	fi, err := rr.store.RawFS().Stat(rr.store.packumentCachePath(name))
	if err != nil {
		return false
	}
	return time.Since(fi.ModTime) < rr.mutableTTL
}

// writePackument emits the cached body with proper headers.
func (rr *Remote) writePackument(w http.ResponseWriter, r *http.Request, body []byte) {
	etag := common.EtagFor(string(body))
	if common.MatchIfNoneMatch(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	common.SetMutableCache(w, etag, rr.mutableTTL)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	_, _ = w.Write(body)
}

// serveTarball checks the cache, falls back to upstream fetch.
func (rr *Remote) serveTarball(w http.ResponseWriter, r *http.Request, name, file string) {
	if rc, _, err := rr.store.OpenTarball(name, file); err == nil {
		rc.Close()
		serveTarballFromStore(w, r, rr.store, name, file)
		return
	}
	key := "tarball:" + name + "/" + file
	_, _, _ = rr.sf.Do(key, func() (any, error) {
		return nil, rr.refetchTarball(r.Context(), name, file)
	})
	if rc, _, err := rr.store.OpenTarball(name, file); err == nil {
		rc.Close()
		serveTarballFromStore(w, r, rr.store, name, file)
		return
	}
	writeNotFound(w, name+"/"+file+": not found upstream")
}

// refetchTarball fetches a tarball file and persists it.
func (rr *Remote) refetchTarball(ctx context.Context, name, file string) error {
	urlPath := "/" + name + "/-/" + file
	resp, err := rr.client.Get(ctx, urlPath)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return rr.store.WriteTarball(name, file, bytes.NewReader(body), int64(len(body)))
}

// serveDistTags returns the cached dist-tags map. Remote registries
// don't accept dist-tag writes (forwarded to upstream is out of scope
// for MVP).
func (rr *Remote) serveDistTags(w http.ResponseWriter, _ *http.Request, name string) {
	tags, err := rr.store.ReadDistTags(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(tags) == 0 {
		// Tags only populate after a packument fetch; trigger one
		// on demand so first-time queries succeed.
		_ = rr.refetchPackumentBackground(name)
		tags, _ = rr.store.ReadDistTags(name)
	}
	writeJSON(w, tags)
}

// refetchPackumentBackground is a best-effort refresh that bypasses
// the live request context — used when we want a packument refresh
// triggered by a side endpoint (e.g. dist-tags first-time query).
// Tarball URLs in the refreshed packument carry a placeholder
// public base; that's harmless because dist-tags consumers never
// follow them.
func (rr *Remote) refetchPackumentBackground(name string) error {
	resp, err := rr.client.Get(context.Background(), "/"+name)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var pkg map[string]any
	if err := json.Unmarshal(body, &pkg); err != nil {
		return err
	}
	if tags, ok := pkg["dist-tags"].(map[string]any); ok {
		strTags := make(map[string]string, len(tags))
		for k, v := range tags {
			if s, ok := v.(string); ok {
				strTags[k] = s
			}
		}
		_ = rr.store.WriteDistTags(name, strTags)
	}
	return nil
}

var _ = strings.HasPrefix // kept across optional refactors
