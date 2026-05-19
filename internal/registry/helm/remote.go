package helm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/upstream"
	"github.com/rakunlabs/pika/internal/service"
)

// Remote is a pull-through Helm registry: fetches index.yaml and
// chart tarballs from an upstream Helm repo (Bitnami, Artifact Hub
// reflectors, internal mirrors), caches the responses in the
// configured raw mount and serves cached bytes on subsequent
// reads.
//
// index.yaml is a mutable document (a `helm repo update` re-pulls
// it); the per-repo MutableTTL controls how long a cached copy
// is served before a refetch. Tarballs are content-addressed by
// (name, version) per Helm convention, so they're cached forever
// once pulled.
//
// Writes are rejected with 405 — clients must address a Local
// repo to publish.
type Remote struct {
	namespace string
	name      string
	store     *Store
	client    *upstream.Client

	mutableTTL time.Duration
}

// NewRemoteFactory returns the Factory for ("helm", "remote").
func NewRemoteFactory() registry.Factory {
	return func(_ context.Context, deps registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		b, err := upstream.BuildRemote(deps, "helm/remote", ns, r, 5*time.Minute)
		if err != nil {
			return nil, err
		}
		return &Remote{
			namespace:  ns,
			name:       r.Name,
			store:      NewStore(b.FS, b.BasePath),
			client:     b.Client,
			mutableTTL: b.MutableTTL,
		}, nil
	}
}

func (rr *Remote) Namespace() string { return rr.namespace }
func (rr *Remote) Name() string      { return rr.name }
func (rr *Remote) Type() string      { return service.RegistryTypeHelm }
func (rr *Remote) Kind() string      { return service.RegistryKindRemote }
func (rr *Remote) Store() *Store     { return rr.store }
func (rr *Remote) Close() error {
	if rr.client != nil {
		return rr.client.Close()
	}
	return nil
}

// Stats reports the cached chart footprint.
func (rr *Remote) Stats(_ context.Context) (registry.Stats, error) {
	charts, vers, bytes := rr.store.CountChartsVersionsBytes()
	return registry.Stats{
		PackageCount: charts,
		VersionCount: vers,
		TotalBytes:   bytes,
	}, nil
}

// PurgeCache implements registry.CachePurger.
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

// PackageDetail returns the cached view of a chart.
func (rr *Remote) PackageDetail(ctx context.Context, name string) (*registry.PackageDetail, error) {
	return buildPackageDetail(ctx, rr.store, name)
}

// ProbeUpstream implements registry.UpstreamProber. /index.yaml
// is the only universally-available Helm endpoint; a successful
// fetch confirms both reachability and that the upstream is a
// Helm repo (rather than e.g. a generic file server).
func (rr *Remote) ProbeUpstream(ctx context.Context) (registry.UpstreamHealth, error) {
	return upstream.Probe(ctx, rr.client, "/index.yaml"), nil
}

// ServeHTTP dispatches. Only safe verbs (GET / HEAD) are accepted;
// writes return 405.
func (rr *Remote) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "remote registry is read-only")
		return
	}
	p := r.URL.Path
	switch {
	case p == "/index.yaml" || p == "/index.yaml/":
		rr.serveIndex(w, r)
	case strings.HasSuffix(p, ".tgz"):
		rr.serveTarball(w, r)
	default:
		writeError(w, http.StatusNotFound, "no helm route matches "+r.Method+" "+p)
	}
}

// serveIndex serves index.yaml. Honour the per-repo MutableTTL —
// when the cache is fresh, return it directly; otherwise re-fetch.
func (rr *Remote) serveIndex(w http.ResponseWriter, r *http.Request) {
	cachePath := rr.store.indexPath()
	if rr.mutableTTL > 0 {
		if rc, fi, err := rr.store.fs.Open(cachePath); err == nil {
			if time.Since(fi.ModTime) < rr.mutableTTL {
				defer rc.Close()
				w.Header().Set("Content-Type", "application/yaml")
				_, _ = io.Copy(w, rc)
				return
			}
			rc.Close()
		}
	}
	resp, err := rr.client.Get(r.Context(), "/index.yaml")
	if err != nil {
		if errors.Is(err, upstream.ErrNotFound) {
			writeError(w, http.StatusNotFound, "upstream has no index.yaml")
			return
		}
		writeError(w, http.StatusBadGateway, "upstream: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "read upstream: "+err.Error())
		return
	}
	if wfs, ok := rr.store.fs.(rawfs.WritableRawFS); ok {
		_ = wfs.Write(cachePath, strings.NewReader(string(body)), int64(len(body)))
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// serveTarball serves a chart tarball, fetching + caching on miss.
func (rr *Remote) serveTarball(w http.ResponseWriter, r *http.Request) {
	filename := strings.TrimPrefix(r.URL.Path, "/")
	chart, version, err := ParseTarballFilename(filename)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Cache hit?
	if rc, fi, err := rr.store.OpenTarball(chart, version); err == nil {
		defer rc.Close()
		w.Header().Set("Content-Type", "application/x-gzip")
		if fi != nil {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", fi.Size))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, rc)
		return
	}
	// Cache miss — fetch from upstream and persist.
	resp, err := rr.client.Get(r.Context(), "/"+filename)
	if err != nil {
		if errors.Is(err, upstream.ErrNotFound) {
			writeError(w, http.StatusNotFound, "upstream has no "+filename)
			return
		}
		writeError(w, http.StatusBadGateway, "upstream: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "read upstream: "+err.Error())
		return
	}
	// Extract Chart.yaml so the detail endpoint works for cached
	// remote charts. Failure is non-fatal — we still serve the
	// tarball; the detail endpoint will skip the chart silently.
	if extraction, err := ExtractChart(body); err == nil {
		_ = rr.store.WriteChart(extraction, body)
	} else {
		// Without Chart.yaml, just persist the tarball directly so
		// the next request hits the cache. We won't be able to
		// surface it in /api/charts though.
		if wfs, ok := rr.store.fs.(rawfs.WritableRawFS); ok {
			_ = wfs.Write(rr.store.tarballPath(chart, version),
				strings.NewReader(string(body)), int64(len(body)))
		}
	}
	w.Header().Set("Content-Type", "application/x-gzip")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
