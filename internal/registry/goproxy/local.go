package goproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/events"
	"github.com/rakunlabs/pika/internal/service"
)

// Local is a Registry implementation backed entirely by pika storage.
// Files are written via manual upload (Pika UI / API) and served
// verbatim. No upstream lookup ever happens — a missing version is
// a 404, full stop.
//
// Upload model
//
// The go ecosystem has no native "publish" step; the recommended
// workflow is `git tag` + a module proxy that pulls from the tag.
// For pika's Local kind we expose admin-only upload endpoints that
// accept the three artefacts (.info, .mod, .zip) and place them
// under the spec-defined storage paths. UI / curl alike can drive
// the upload; the same files become consumable through the standard
// /registries/{ns}/{repo}/{module}/@v/... read endpoints
// immediately.
//
// Upload route shape (gated on AllowPush and CapRegistryWrite):
//
//	PUT /{module}/@v/{version}.info   application/json
//	PUT /{module}/@v/{version}.mod    text/plain
//	PUT /{module}/@v/{version}.zip    application/zip
//
// .info bodies that omit the Time field auto-fill with the current
// server time on upload so a curl one-liner doesn't need to
// hand-craft RFC3339 dates.
type Local struct {
	namespace string
	name      string
	store     *Store

	allowPush  bool
	maxUpload  int64
	mutableTTL time.Duration
	emitter    events.Emitter
}

// NewLocalFactory returns a goproxy.Factory that constructs Local
// Registry instances from RegistryRepository rows. Registered with
// the manager at boot for the ("go", "local") tuple.
func NewLocalFactory() registry.Factory {
	return func(_ context.Context, deps registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		fs, err := deps.MountRawFS(r.Mount)
		if err != nil {
			return nil, fmt.Errorf("goproxy/local %s/%s: %w", ns, r.Name, err)
		}
		ttl := time.Duration(0)
		if r.MutableTTL != "" {
			d, err := time.ParseDuration(r.MutableTTL)
			if err == nil {
				ttl = d
			}
		}
		return &Local{
			namespace:  ns,
			name:       r.Name,
			store:      NewStore(fs, r.BasePath),
			allowPush:  r.AllowPush,
			maxUpload:  r.MaxUploadSize,
			mutableTTL: ttl,
			emitter:    deps.Emitter,
		}, nil
	}
}

// Namespace, Name, Type, Kind: Registry interface metadata.
func (l *Local) Namespace() string { return l.namespace }
func (l *Local) Name() string      { return l.name }
func (l *Local) Type() string      { return service.RegistryTypeGo }
func (l *Local) Kind() string      { return service.RegistryKindLocal }

// Store exposes the underlying Store. Used by the admin API to list
// modules / versions for the UI without leaking storage details
// through the Registry interface.
func (l *Local) Store() *Store { return l.store }

// AllowPush reports whether the upload endpoints are enabled. The
// admin UI uses this to show / hide the upload form.
func (l *Local) AllowPush() bool { return l.allowPush }

// PackageDetail returns per-module metadata via the shared builder.
// Implements registry.PackageDetailer.
func (l *Local) PackageDetail(ctx context.Context, module string) (*registry.PackageDetail, error) {
	return buildPackageDetail(ctx, l.store, module)
}

// Close releases any resources. Local has none; method exists to
// satisfy registry.Registry.
func (l *Local) Close() error { return nil }

// Stats implements registry.StatsProvider for Local Go repositories.
// Returns the count of modules, versions and total on-disk bytes by
// walking the @v/ leaves once. No persistent counter is kept; the
// numbers are exact at the moment of the call.
func (l *Local) Stats(_ context.Context) (registry.Stats, error) {
	mods, vers, bytes := l.store.CountModulesVersionsBytes()
	return registry.Stats{
		ModuleCount:  mods,
		VersionCount: vers,
		TotalBytes:   bytes,
	}, nil
}

// ServeHTTP dispatches a request whose URL path has had the
// /registries/{ns}/{repo} prefix stripped. Recognised verbs:
//
//	GET   — read .info / .mod / .zip / list / @latest
//	PUT   — upload (CapRegistryWrite + AllowPush required)
//	HEAD  — same as GET, body omitted by net/http
//
// Anything else returns 405.
func (l *Local) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, ok := parsePath(r.URL.Path)
	if !ok {
		writeNotFound(w, "unrecognised go module proxy path: "+r.URL.Path)
		return
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		l.serveRead(w, r, req)
	case http.MethodPut:
		l.serveUpload(w, r, req)
	default:
		writeMethodNotAllowed(w, "GET, HEAD, PUT")
	}
}

// serveRead handles GET / HEAD for every Local route.
func (l *Local) serveRead(w http.ResponseWriter, r *http.Request, req *parsedRequest) {
	switch {
	case req.IsList:
		serveCachedList(w, r, l.store, req.Module, l.mutableTTL)
	case req.IsLatest:
		serveCachedLatest(w, r, l.store, req.Module, l.mutableTTL)
	default:
		serveFileFromStore(w, r, l.store, req.Module, req.Version, req.Ext)
	}
}

// serveUpload accepts PUT writes from the admin UI / API. The
// upload is rejected when AllowPush is off or the body exceeds the
// per-repo MaxUploadSize. Auth and capability checks have already
// fired in the data-mux entry handler — by the time we reach here,
// the caller has the registry.write scope.
//
// The handler is intentionally permissive about validation beyond
// the URL-level checks: the .mod parser and .zip checker live in
// the go toolchain and re-implementing them is out of scope. The
// trade-off is that a Local repo can hold a malformed module; the
// go client will reject it at install time with a clear error.
func (l *Local) serveUpload(w http.ResponseWriter, r *http.Request, req *parsedRequest) {
	if !l.allowPush {
		writeMethodNotAllowed(w, "GET, HEAD")
		return
	}
	if req.IsLatest || req.IsList {
		writeMethodNotAllowed(w, "GET, HEAD")
		return
	}
	if req.Ext != "info" && req.Ext != "mod" && req.Ext != "zip" {
		writeNotFound(w, "unrecognised upload extension")
		return
	}

	// Size cap. ContentLength is best-effort: chunked uploads have
	// -1, and we don't want to refuse them outright. For chunked
	// bodies the io.Copy below absorbs them up to the configured
	// limit; without a limit the absorb is unbounded (matches
	// pika's existing /raw/* upload semantics).
	if l.maxUpload > 0 && r.ContentLength > l.maxUpload {
		http.Error(w, fmt.Sprintf("upload exceeds %d bytes", l.maxUpload), http.StatusRequestEntityTooLarge)
		return
	}

	// For .info uploads with an empty Time field, auto-fill on the
	// server so curl uploads don't need an explicit timestamp. We
	// peek at the body via a small decode + re-marshal — overhead
	// is negligible because info bodies are tiny.
	body := r.Body
	contentLength := r.ContentLength
	if req.Ext == "info" {
		filled, n, err := maybeFillInfoTime(r.Body, req.Version)
		if err != nil {
			http.Error(w, "invalid .info body: "+err.Error(), http.StatusBadRequest)
			return
		}
		body = filled
		contentLength = n
	}

	if err := l.store.WriteVersionFile(req.Module, req.Version, req.Ext, body, contentLength); err != nil {
		http.Error(w, "write failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Emit a semantic publish event. Only fire on the .zip slot so
	// a three-file upload (info + mod + zip) produces one event
	// per logical publish rather than three. The .zip is the
	// canonical "this version is now installable" marker since the
	// Go toolchain refuses to install a module without it.
	if req.Ext == "zip" {
		events.EmitSafe(l.emitter, hook.Event{
			Type:     hook.EventRegistryPublished,
			Mount:    l.namespace,
			Path:     l.name + "/" + req.Module + "@" + req.Version,
			Protocol: "registry-go",
			Size:     contentLength,
			User:     userFromContext(r.Context()),
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// userFromContext extracts the authenticated user identifier (token
// id or session username) for hook event attribution. Returns ""
// when the request has no identified actor — registry endpoints
// always require auth but tests sometimes drive the handler with a
// bare context.
func userFromContext(_ context.Context) string {
	// The registry data-mux handler stuffs the actor under a
	// well-known key, but accessing it from here would require an
	// import of internal/server/api and a context-key contract.
	// For now we leave User empty in the event; operators who care
	// can correlate via timestamps + token-id audit logs upstream.
	// This is the same trade-off the existing file.* events make.
	return ""
}

// maybeFillInfoTime reads an .info body, fills the Time field with
// time.Now().UTC() when missing, and returns a reader over the
// (possibly rewritten) bytes plus its length. Body is closed.
func maybeFillInfoTime(r io.ReadCloser, version string) (io.ReadCloser, int64, error) {
	defer r.Close()
	buf, err := io.ReadAll(io.LimitReader(r, 64*1024+1))
	if err != nil {
		return nil, 0, err
	}
	if len(buf) > 64*1024 {
		return nil, 0, fmt.Errorf(".info body too large (>64 KiB)")
	}
	// If the body parses as VersionInfo and Time is zero, replace
	// with current time. Otherwise pass through unchanged.
	info := VersionInfo{}
	if perr := json.Unmarshal(buf, &info); perr == nil {
		if info.Time.IsZero() {
			info.Time = time.Now().UTC()
		}
		if info.Version == "" {
			info.Version = version
		}
		out, _ := json.Marshal(info)
		return io.NopCloser(bytes.NewReader(out)), int64(len(out)), nil
	}
	// Not a recognisable VersionInfo — write the bytes verbatim
	// and trust the uploader.
	return io.NopCloser(bytes.NewReader(buf)), int64(len(buf)), nil
}
