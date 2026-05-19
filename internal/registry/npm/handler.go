package npm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rakunlabs/pika/internal/registry/common"
)

// HTTP handler primitives shared by Local / Remote / Virtual.
//
// Routes recognised at this layer:
//
//   route                              handler responsibility
//   ─────────────────────────────────  ─────────────────────────────
//   GET    /{pkg}                       packument
//   GET    /{pkg}/-/{file}.tgz          tarball
//   PUT    /{pkg}                       publish (Local only)
//   GET    /-/v1/search?text=...        search (basic name match)
//   GET    /-/whoami                    JSON {"username": "..."}
//   GET    /-/package/{pkg}/dist-tags   all dist-tags
//   PUT    /-/package/{pkg}/dist-tags/{tag} set
//   DELETE /-/package/{pkg}/dist-tags/{tag} remove

// classifiedRequest summarises what kind of NPM operation the URL
// describes. Handlers branch on Op and read the relevant fields.
type classifiedRequest struct {
	Op       string // "packument" | "tarball" | "publish" | "search" | "whoami" | "dist-tags" | "dist-tag-set" | "dist-tag-del" | ""
	Pkg      string
	File     string // tarball filename
	Tag      string // dist-tag name (for set/del)
	SearchQ  string
	SearchN  int
}

// classify parses one URL path + HTTP method into a classifiedRequest.
// Returns op=="" for shapes we don't recognise.
func classify(method, p string) classifiedRequest {
	// "-/..." routes (admin / search). Match before package paths
	// so a package named "-" doesn't accidentally collide.
	if strings.HasPrefix(p, "/-/") {
		return classifyDash(method, p)
	}

	name, rest, ok := ParseNameFromPath(p)
	if !ok {
		return classifiedRequest{}
	}
	name = NormalizeNameFromURL(name)
	if err := ValidatePackageName(name); err != nil {
		return classifiedRequest{}
	}

	switch {
	case rest == "":
		// /{pkg}
		switch method {
		case http.MethodGet, http.MethodHead:
			return classifiedRequest{Op: "packument", Pkg: name}
		case http.MethodPut:
			return classifiedRequest{Op: "publish", Pkg: name}
		}
	case strings.HasPrefix(rest, "/-/"):
		// /{pkg}/-/{file}.tgz
		file := strings.TrimPrefix(rest, "/-/")
		if !strings.HasSuffix(file, ".tgz") || strings.ContainsRune(file, '/') {
			return classifiedRequest{}
		}
		return classifiedRequest{Op: "tarball", Pkg: name, File: file}
	}
	return classifiedRequest{}
}

// classifyDash handles the "-/...." admin namespace.
func classifyDash(method, p string) classifiedRequest {
	switch {
	case p == "/-/whoami" && (method == http.MethodGet || method == http.MethodHead):
		return classifiedRequest{Op: "whoami"}
	case strings.HasPrefix(p, "/-/v1/search"):
		// Parse query manually (callers use net/url Values for
		// detail). For classification we accept the path verbatim;
		// the handler reads r.URL.Query() to get text/size.
		return classifiedRequest{Op: "search"}
	case strings.HasPrefix(p, "/-/package/"):
		// /-/package/{name}/dist-tags
		// /-/package/{name}/dist-tags/{tag}
		rest := strings.TrimPrefix(p, "/-/package/")
		// rest = "{name}/dist-tags[/{tag}]"
		dt := strings.Index(rest, "/dist-tags")
		if dt < 0 {
			return classifiedRequest{}
		}
		name := NormalizeNameFromURL(rest[:dt])
		if err := ValidatePackageName(name); err != nil {
			return classifiedRequest{}
		}
		tail := rest[dt+len("/dist-tags"):]
		if tail == "" {
			if method == http.MethodGet || method == http.MethodHead {
				return classifiedRequest{Op: "dist-tags", Pkg: name}
			}
			return classifiedRequest{}
		}
		if !strings.HasPrefix(tail, "/") {
			return classifiedRequest{}
		}
		tag := tail[1:]
		if tag == "" {
			return classifiedRequest{}
		}
		switch method {
		case http.MethodPut:
			return classifiedRequest{Op: "dist-tag-set", Pkg: name, Tag: tag}
		case http.MethodDelete:
			return classifiedRequest{Op: "dist-tag-del", Pkg: name, Tag: tag}
		}
	}
	return classifiedRequest{}
}

// writeNotFound writes the npm "package not found" JSON shape.
func writeNotFound(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = fmt.Fprintf(w, `{"error":"not_found","reason":%q}`, message)
}

// writeError writes a generic npm-style error body with status.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":%q,"reason":%q}`, http.StatusText(status), message)
}

// writeJSON marshals v and writes a 200 response.
func writeJSON(w http.ResponseWriter, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "marshal: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	_, _ = w.Write(body)
}

// servePackumentFromStore reads or rebuilds the packument for one
// package, applies cache headers, and writes the response.
func servePackumentFromStore(w http.ResponseWriter, r *http.Request, s *Store, name string) {
	body, err := loadPackumentBody(s, name)
	if err != nil {
		writeNotFound(w, name+": "+err.Error())
		return
	}
	etag := common.EtagFor(string(body))
	if common.MatchIfNoneMatch(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	common.SetMutableCache(w, etag, 0) // 0 → 60s default floor
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	_, _ = w.Write(body)
}

// loadPackumentBody returns the cached packument body, rebuilding
// when stale or missing.
func loadPackumentBody(s *Store, name string) ([]byte, error) {
	if cached, ok, _ := s.ReadCachedPackument(name); ok && len(cached) > 0 {
		return cached, nil
	}
	pkg, err := BuildPackument(s, name)
	if err != nil {
		return nil, err
	}
	body, err := PackumentJSON(pkg)
	if err != nil {
		return nil, err
	}
	_ = s.WriteCachedPackument(name, body)
	return body, nil
}

// serveTarballFromStore streams a tarball with immutable cache
// headers. NPM tarball filenames are content-addressable in
// practice (publishing the same version reuses the same filename),
// so immutable is the right default.
func serveTarballFromStore(w http.ResponseWriter, r *http.Request, s *Store, name, file string) {
	rc, fi, err := s.OpenTarball(name, file)
	if err != nil {
		writeNotFound(w, name+"/"+file+": "+err.Error())
		return
	}
	defer rc.Close()
	etag := common.EtagFor(name + "/" + file)
	if common.MatchIfNoneMatch(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	common.SetImmutableCache(w, etag)
	w.Header().Set("Content-Type", "application/octet-stream")
	if fi != nil && fi.Size > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fi.Size))
	}
	_, _ = io.Copy(w, rc)
}
