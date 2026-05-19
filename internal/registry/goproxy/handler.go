package goproxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/pika/internal/registry/common"
)

// HTTP handler primitives shared by the Local, Remote and Virtual
// Registry implementations.
//
// Route shapes (after the manager strips "/registries/{ns}/{repo}"):
//
//	GET  /{module}/@v/list                   text/plain
//	GET  /{module}/@v/{version}.info         application/json
//	GET  /{module}/@v/{version}.mod          text/plain
//	GET  /{module}/@v/{version}.zip          application/zip
//	GET  /{module}/@latest                   application/json
//
// Anything else returns 404. The module path may contain slashes
// ("github.com/foo/bar") so it's everything between the registry
// prefix and the first "@v/" or "@latest" marker.

// parsedRequest captures the result of routing one Go module proxy
// URL. The values are normalised (no leading/trailing whitespace,
// version validated) before the handler dispatches.
type parsedRequest struct {
	Module    string // decoded module path ("github.com/foo/bar")
	Version   string // empty for list/latest
	Ext       string // "info" | "mod" | "zip" | "list" | "latest"
	IsLatest  bool
	IsList    bool
}

// parsePath splits a goproxy URL path into its components. Returns
// (nil, ok=false) when the path doesn't match the protocol shape.
//
// The path is expected to start with "/" and to be everything past
// the registry's /registries/{ns}/{repo} prefix.
func parsePath(p string) (*parsedRequest, bool) {
	if !strings.HasPrefix(p, "/") {
		return nil, false
	}
	p = p[1:]

	// @latest: "{module}/@latest"
	if idx := strings.LastIndex(p, "/@latest"); idx > 0 && p[idx:] == "/@latest" {
		mod, err := DecodeModulePath(p[:idx])
		if err != nil {
			return nil, false
		}
		if err := ValidateModulePath(mod); err != nil {
			return nil, false
		}
		return &parsedRequest{Module: mod, IsLatest: true, Ext: "latest"}, true
	}

	// @v/...: "{module}/@v/{file}"
	idx := strings.LastIndex(p, "/@v/")
	if idx <= 0 {
		return nil, false
	}
	modPart := p[:idx]
	rest := p[idx+len("/@v/"):]

	mod, err := DecodeModulePath(modPart)
	if err != nil {
		return nil, false
	}
	if err := ValidateModulePath(mod); err != nil {
		return nil, false
	}

	if rest == "list" {
		return &parsedRequest{Module: mod, IsList: true, Ext: "list"}, true
	}
	// {version}.{ext}
	dot := strings.LastIndexByte(rest, '.')
	if dot <= 0 || dot == len(rest)-1 {
		return nil, false
	}
	ver := rest[:dot]
	ext := rest[dot+1:]
	if err := ValidateVersion(ver); err != nil {
		return nil, false
	}
	switch ext {
	case "info", "mod", "zip":
	default:
		return nil, false
	}
	return &parsedRequest{Module: mod, Version: ver, Ext: ext}, true
}

// contentTypeFor returns the Content-Type header value for each
// goproxy file extension. The matches are what proxy.golang.org
// emits today; the go client doesn't strictly require any of them
// but cosmetic correctness helps `curl` and browser users.
func contentTypeFor(ext string) string {
	switch ext {
	case "info", "latest":
		return "application/json"
	case "mod":
		return "text/plain; charset=utf-8"
	case "zip":
		return "application/zip"
	case "list":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

// writeNotFound sends the goproxy 404 response. The body shape isn't
// part of the spec — the go client only cares about the status code
// — but emitting a short text message helps `curl` users debug.
func writeNotFound(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = fmt.Fprintln(w, msg)
}

// writeMethodNotAllowed sends a 405. Used for Local repos that
// don't support PUT (upload disabled) and Remote/Virtual repos that
// don't support any write verb.
func writeMethodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	w.WriteHeader(http.StatusMethodNotAllowed)
}

// serveFileFromStore streams a {module, version, ext} file from the
// store with proper headers. The handler reads the file body once
// and forwards it; range requests are not supported because the
// rawfs.ReadSeekCloser layer can't promise universal Range across
// every backend. Go client downloads aren't range-driven so this
// limitation has no practical impact.
func serveFileFromStore(w http.ResponseWriter, r *http.Request, s *Store, mod, ver, ext string) {
	rc, fi, err := s.OpenVersionFile(mod, ver, ext)
	if err != nil {
		writeNotFound(w, fmt.Sprintf("%s@%s.%s not found", mod, ver, ext))
		return
	}
	defer rc.Close()

	// Versioned files are content-addressable by (module, version):
	// the same triple is supposed to always produce the same bytes.
	// Mark them immutable so the go client / any CDN in front of
	// pika cache aggressively.
	etag := common.EtagFor(mod + "@" + ver + "." + ext)
	if common.MatchIfNoneMatch(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	common.SetImmutableCache(w, etag)
	w.Header().Set("Content-Type", contentTypeFor(ext))
	if fi != nil && fi.Size > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fi.Size))
	}
	_, _ = io.Copy(w, rc)
}

// serveCachedList writes the @v/list body with mutable-cache
// headers. ttl is the per-repo MutableTTL (zero falls back to a
// 5-minute default — list output changes whenever a new version is
// published).
func serveCachedList(w http.ResponseWriter, r *http.Request, s *Store, mod string, ttl time.Duration) {
	body, err := s.CachedList(mod, ttl)
	if err != nil {
		writeNotFound(w, fmt.Sprintf("list %s: %v", mod, err))
		return
	}
	etag := EtagForList(body)
	if common.MatchIfNoneMatch(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	cacheTTL := ttl
	if cacheTTL <= 0 {
		cacheTTL = 5 * time.Minute
	}
	common.SetMutableCache(w, etag, cacheTTL)
	w.Header().Set("Content-Type", contentTypeFor("list"))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	_, _ = w.Write(body)
}

// serveCachedLatest writes the @latest body with mutable-cache
// headers. Returns 404 when the module has no versions yet.
func serveCachedLatest(w http.ResponseWriter, r *http.Request, s *Store, mod string, ttl time.Duration) {
	body, err := s.CachedLatest(mod, ttl)
	if err != nil {
		writeNotFound(w, fmt.Sprintf("latest %s: %v", mod, err))
		return
	}
	etag := common.EtagFor(string(body))
	if common.MatchIfNoneMatch(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	cacheTTL := ttl
	if cacheTTL <= 0 {
		cacheTTL = 5 * time.Minute
	}
	common.SetMutableCache(w, etag, cacheTTL)
	w.Header().Set("Content-Type", contentTypeFor("latest"))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	_, _ = w.Write(body)
}

// muxParsedGET is a small router used by Local and Remote handlers
// for the read path. It dispatches one parsed request to either
// version-file, list or latest handlers.
//
// Returning false means the request shape was understood but the
// handler chose not to serve it (e.g. unsupported method); callers
// then fall through to whatever fallback policy they implement.
type readSource interface {
	openVersion(ctx context.Context, mod, ver, ext string) (io.ReadCloser, int64, error)
	listBody(ctx context.Context, mod string) ([]byte, error)
	latestBody(ctx context.Context, mod string) ([]byte, error)
}

// _ keep imports tidy across refactors that may add or remove
// blocks.
var _ = http.MethodGet
