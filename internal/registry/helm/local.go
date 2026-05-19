package helm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/events"
	"github.com/rakunlabs/pika/internal/service"
)

// Local is the Helm registry implementation backed by pika
// storage. Reads serve the classic Helm protocol (index.yaml +
// chart tarballs); writes accept both the ChartMuseum API and
// plain HTTP PUT of a tarball.
//
// Auth + scopes are handled by the routing layer; ServeHTTP only
// concerns itself with protocol semantics.
type Local struct {
	namespace string
	name      string
	store     *Store

	allowPush bool
	maxUpload int64
	emitter   events.Emitter
}

// NewLocalFactory returns the Factory for ("helm", "local").
func NewLocalFactory() registry.Factory {
	return func(_ context.Context, deps registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		fs, err := deps.MountRawFS(r.Mount)
		if err != nil {
			return nil, fmt.Errorf("helm/local %s/%s: %w", ns, r.Name, err)
		}
		return &Local{
			namespace: ns,
			name:      r.Name,
			store:     NewStore(fs, r.BasePath),
			allowPush: r.AllowPush,
			maxUpload: r.MaxUploadSize,
			emitter:   deps.Emitter,
		}, nil
	}
}

func (l *Local) Namespace() string { return l.namespace }
func (l *Local) Name() string      { return l.name }
func (l *Local) Type() string      { return service.RegistryTypeHelm }
func (l *Local) Kind() string      { return service.RegistryKindLocal }
func (l *Local) Store() *Store     { return l.store }
func (l *Local) Close() error      { return nil }
func (l *Local) AllowPush() bool   { return l.allowPush }

// Stats implements registry.StatsProvider.
func (l *Local) Stats(_ context.Context) (registry.Stats, error) {
	charts, vers, bytes := l.store.CountChartsVersionsBytes()
	return registry.Stats{
		PackageCount: charts, // re-use NPM's slot — UI labels per-type
		VersionCount: vers,
		TotalBytes:   bytes,
	}, nil
}

// PackageDetail implements registry.PackageDetailer.
func (l *Local) PackageDetail(ctx context.Context, name string) (*registry.PackageDetail, error) {
	return buildPackageDetail(ctx, l.store, name)
}

// ServeHTTP dispatches an inbound request. The trimmed path under
// /registries/{ns}/{repo} arrives here as e.g. "/index.yaml",
// "/mychart-1.0.0.tgz", "/api/charts" (POST).
func (l *Local) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case r.Method == http.MethodGet && (p == "/index.yaml" || p == "/index.yaml/"):
		l.serveIndex(w, r)
	case r.Method == http.MethodGet && strings.HasSuffix(p, ".tgz"):
		l.serveTarball(w, r)
	case r.Method == http.MethodGet && (p == "/api/charts" || p == "/api/charts/"):
		l.serveAPIList(w, r)
	case r.Method == http.MethodPost && (p == "/api/charts" || p == "/api/charts/"):
		l.servePublishChartMuseum(w, r)
	case r.Method == http.MethodPut && strings.HasSuffix(p, ".tgz"):
		l.servePublishRawPUT(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(p, "/api/charts/"):
		l.serveDelete(w, r)
	default:
		writeError(w, http.StatusNotFound, "no helm route matches "+r.Method+" "+p)
	}
}

// serveIndex returns the cached index.yaml, rebuilding it when
// the cache is stale or missing. Helm clients sometimes send
// If-Modified-Since; we serve a fresh body on every call for
// simplicity (the rebuild is O(charts × versions) and indexes
// stay small in practice).
func (l *Local) serveIndex(w http.ResponseWriter, r *http.Request) {
	publicBase := inferPublicBase(r)
	idx, err := BuildIndex(l.store, publicBase)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "build index: "+err.Error())
		return
	}
	body, err := MarshalIndex(idx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "marshal index: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// serveTarball streams a chart tarball. The URL form is
// "/{chart}-{version}.tgz" — we parse the filename to recover the
// chart name and version.
func (l *Local) serveTarball(w http.ResponseWriter, r *http.Request) {
	filename := strings.TrimPrefix(r.URL.Path, "/")
	chart, version, err := ParseTarballFilename(filename)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rc, fi, err := l.store.OpenTarball(chart, version)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/x-gzip")
	if fi != nil {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fi.Size))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// serveAPIList returns a JSON catalogue of every chart + versions.
// Modeled on ChartMuseum's GET /api/charts response so existing
// tooling that targets ChartMuseum keeps working.
func (l *Local) serveAPIList(w http.ResponseWriter, r *http.Request) {
	out := map[string][]ChartMetadata{}
	charts, _ := l.store.ListCharts()
	for _, name := range charts {
		versions, _ := l.store.ListVersions(name)
		metas := make([]ChartMetadata, 0, len(versions))
		for _, v := range versions {
			if m, err := l.store.ReadMetadata(name, v); err == nil {
				metas = append(metas, *m)
			}
		}
		if len(metas) > 0 {
			out[name] = metas
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(out)
}

// servePublishChartMuseum handles the multipart POST /api/charts
// upload. The field name "chart" carries the .tgz body; we accept
// either a multipart envelope or a raw application/octet-stream
// body (some CI clients send the latter).
func (l *Local) servePublishChartMuseum(w http.ResponseWriter, r *http.Request) {
	if !l.allowPush {
		writeError(w, http.StatusForbidden, "push disabled on this repository")
		return
	}
	body, err := l.readUploadBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	l.finishPublish(w, r, body)
}

// servePublishRawPUT handles "PUT /{chart}-{version}.tgz" — a
// minimal upload form that lets `curl -T` publish without a
// multipart envelope.
func (l *Local) servePublishRawPUT(w http.ResponseWriter, r *http.Request) {
	if !l.allowPush {
		writeError(w, http.StatusForbidden, "push disabled on this repository")
		return
	}
	body, err := l.readRawBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	l.finishPublish(w, r, body)
}

// finishPublish does the post-read processing shared by both
// publish flows: extract Chart.yaml, validate, persist, emit
// event.
func (l *Local) finishPublish(w http.ResponseWriter, r *http.Request, body []byte) {
	extraction, err := ExtractChart(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ValidateChartName(extraction.Metadata.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ValidateChartVersion(extraction.Metadata.Version); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := l.store.WriteChart(extraction, body); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	events.EmitSafe(l.emitter, hook.Event{
		Type:     hook.EventRegistryPublished,
		Mount:    l.namespace,
		Path:     l.name + "/" + extraction.Metadata.Name + "@" + extraction.Metadata.Version,
		Protocol: "registry-helm",
		Size:     extraction.Size,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"saved": true,
		"name":  extraction.Metadata.Name,
		"version": extraction.Metadata.Version,
	})
}

// serveDelete removes a chart version. The URL is
// "/api/charts/{chart}/{version}". 204 on success, 404 on miss.
func (l *Local) serveDelete(w http.ResponseWriter, r *http.Request) {
	if !l.allowPush {
		writeError(w, http.StatusForbidden, "delete disabled on this repository")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/charts/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		writeError(w, http.StatusBadRequest, "expected /api/charts/{chart}/{version}")
		return
	}
	chart := parts[0]
	version := strings.TrimSuffix(parts[1], "/")
	if err := ValidateChartName(chart); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := ValidateChartVersion(version); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	size := l.store.TarballSize(chart, version)
	if err := l.store.DeleteVersion(chart, version); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	events.EmitSafe(l.emitter, hook.Event{
		Type:     hook.EventRegistryDeleted,
		Mount:    l.namespace,
		Path:     l.name + "/" + chart + "@" + version,
		Protocol: "registry-helm",
		Size:     size,
	})
	w.WriteHeader(http.StatusNoContent)
}

// readUploadBody reads the chart body from a multipart "chart"
// field, falling back to the raw body when the request isn't
// multipart-encoded. Memory-cap via maxUpload (when set).
func (l *Local) readUploadBody(r *http.Request) ([]byte, error) {
	ct := r.Header.Get("Content-Type")
	maxBytes := l.maxUpload
	if maxBytes == 0 {
		// Helm charts are small (1 MB typical) but include
		// vendored Helm charts (Helmfile bundles) so allow up to
		// 200 MB for parity with NPM.
		maxBytes = 200 * 1024 * 1024
	}
	if strings.HasPrefix(ct, "multipart/") {
		// Parse the multipart envelope.
		if err := r.ParseMultipartForm(maxBytes); err != nil {
			return nil, fmt.Errorf("parse multipart: %w", err)
		}
		fh, _, err := r.FormFile("chart")
		if err != nil {
			return nil, fmt.Errorf("form field 'chart' missing: %w", err)
		}
		defer fh.Close()
		body, err := io.ReadAll(io.LimitReader(fh, maxBytes+1))
		if err != nil {
			return nil, err
		}
		if int64(len(body)) > maxBytes {
			return nil, fmt.Errorf("chart body exceeds %d bytes", maxBytes)
		}
		return body, nil
	}
	return l.readRawBody(r)
}

func (l *Local) readRawBody(r *http.Request) ([]byte, error) {
	maxBytes := l.maxUpload
	if maxBytes == 0 {
		maxBytes = 200 * 1024 * 1024
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("chart body exceeds %d bytes", maxBytes)
	}
	return body, nil
}

// writeError is the local error response helper. Matches the
// shape used by the other registry packages.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// inferPublicBase reconstructs the public URL prefix from the
// X-Pika-Registry-Prefix header pika injects before dispatching
// to the registry handler. Falls back to a relative URL when the
// header is absent (tests; old request paths).
func inferPublicBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	prefix := r.Header.Get("X-Pika-Registry-Prefix")
	if prefix == "" {
		return ""
	}
	return scheme + "://" + host + prefix
}

// _ keep multipart import: used through r.FormFile inside
// readUploadBody. Importing it explicitly above keeps go vet
// happy if a refactor moves the call site.
var _ = multipart.Form{}
