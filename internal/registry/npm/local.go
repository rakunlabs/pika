package npm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/service"
)

// Local is the publish-and-serve NPM registry implementation.
//
// Lifecycle of a publish:
//
//   1. Client POSTs `npm publish` JSON to PUT /{pkg}.
//   2. ParsePublish validates the envelope + decodes the tarball.
//   3. If the version already exists, reject with 409. NPM
//      semantics: publish without --force is not idempotent on
//      re-push.
//   4. Rewrite the dist.tarball URL inside the version metadata so
//      consumers fetch from pika, not the original upstream.
//   5. Write the tarball, the per-version meta, and the (optional)
//      README to the store.
//   6. Merge incoming dist-tags into the on-disk map; "latest" is
//      auto-set when the payload omits it AND no latest exists yet.
//   7. Cache file invalidation happens implicitly through the store
//      (WriteVersionMeta deletes packument.json).
//
// Read path (GET) is straightforward — packument is rebuilt lazily
// from the on-disk version-meta files; tarballs stream verbatim.
type Local struct {
	namespace string
	name      string
	store     *Store

	allowPush bool
	maxUpload int64
}

// NewLocalFactory returns the Factory for ("npm", "local") repos.
func NewLocalFactory() registry.Factory {
	return func(_ context.Context, deps registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		fs, err := deps.MountRawFS(r.Mount)
		if err != nil {
			return nil, fmt.Errorf("npm/local %s/%s: %w", ns, r.Name, err)
		}
		return &Local{
			namespace: ns,
			name:      r.Name,
			store:     NewStore(fs, r.BasePath),
			allowPush: r.AllowPush,
			maxUpload: r.MaxUploadSize,
		}, nil
	}
}

func (l *Local) Namespace() string { return l.namespace }
func (l *Local) Name() string      { return l.name }
func (l *Local) Type() string      { return service.RegistryTypeNPM }
func (l *Local) Kind() string      { return service.RegistryKindLocal }
func (l *Local) Store() *Store     { return l.store }
func (l *Local) Close() error      { return nil }

// AllowPush reports whether publish endpoints are enabled.
func (l *Local) AllowPush() bool { return l.allowPush }

// ServeHTTP is the registry interface entry point. Parses the URL,
// classifies the operation, dispatches.
func (l *Local) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req := classify(r.Method, r.URL.Path)
	switch req.Op {
	case "packument":
		servePackumentFromStore(w, r, l.store, req.Pkg)
	case "tarball":
		serveTarballFromStore(w, r, l.store, req.Pkg, req.File)
	case "publish":
		l.servePublish(w, r, req.Pkg)
	case "search":
		l.serveSearch(w, r)
	case "whoami":
		serveWhoami(w, r)
	case "dist-tags":
		l.serveDistTags(w, r, req.Pkg)
	case "dist-tag-set":
		l.serveDistTagSet(w, r, req.Pkg, req.Tag)
	case "dist-tag-del":
		l.serveDistTagDel(w, r, req.Pkg, req.Tag)
	default:
		writeNotFound(w, "unrecognised npm route: "+r.Method+" "+r.URL.Path)
	}
}

// servePublish accepts a publish payload, persists artifacts.
func (l *Local) servePublish(w http.ResponseWriter, r *http.Request, urlName string) {
	if !l.allowPush {
		writeError(w, http.StatusMethodNotAllowed, "publishing disabled for this repo")
		return
	}
	max := l.maxUpload
	if max == 0 {
		// Default cap of 200 MiB — comfortably above the npm
		// tarball-size sanity ceiling while still bounding memory.
		max = 200 * 1024 * 1024
	}

	parsed, err := ParsePublish(r.Body, max)
	if err != nil {
		switch {
		case errorsIs(err, ErrIntegrityFail):
			writeError(w, http.StatusUnprocessableEntity, err.Error())
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	// URL name must match the payload name (npm enforces this
	// client-side, but the server is the authoritative gate).
	if parsed.Name != urlName {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("URL name %q does not match payload name %q", urlName, parsed.Name))
		return
	}

	exists, err := l.store.VersionMetaExists(parsed.Name, parsed.Version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if exists {
		writeError(w, http.StatusConflict,
			fmt.Sprintf("%s@%s already published", parsed.Name, parsed.Version))
		return
	}

	// Rewrite dist.tarball to point at pika before persisting the
	// metadata, so future reads emit URLs that hit us. publicBase
	// is reconstructed from the request — see commentary on
	// inferPublicBase below.
	publicBase := inferPublicBase(r)
	filename := RewriteVersionMetaTarball(parsed.VersionMeta, parsed.Name, publicBase)
	if filename == "" {
		filename = parsed.TarballName
	}

	if err := l.store.WriteTarball(parsed.Name, filename, bytes.NewReader(parsed.Tarball), int64(len(parsed.Tarball))); err != nil {
		writeError(w, http.StatusInternalServerError, "write tarball: "+err.Error())
		return
	}
	if err := l.store.WriteVersionMeta(parsed.Name, parsed.Version, parsed.VersionMeta); err != nil {
		writeError(w, http.StatusInternalServerError, "write meta: "+err.Error())
		return
	}
	if parsed.Readme != "" {
		_ = l.store.WriteReadme(parsed.Name, parsed.Readme)
	}

	// Merge dist-tags. The payload may carry an explicit
	// {"latest": version} or be silent; in the silent case the
	// auto-latest rule kicks in only when no existing latest is set.
	existing, _ := l.store.ReadDistTags(parsed.Name)
	if existing == nil {
		existing = map[string]string{}
	}
	for tag, ver := range parsed.DistTags {
		existing[tag] = ver
	}
	if _, hasLatest := existing["latest"]; !hasLatest {
		existing["latest"] = parsed.Version
	}
	if err := l.store.WriteDistTags(parsed.Name, existing); err != nil {
		writeError(w, http.StatusInternalServerError, "write dist-tags: "+err.Error())
		return
	}

	// npm CLI accepts 200 or 201 on successful publish; 201 is the
	// more correct choice (new resource created).
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = fmt.Fprintf(w, `{"ok":true,"id":%q,"rev":"%d-rev"}`,
		parsed.Name, len(parsed.DistTags)+1)
}

// serveSearch returns a minimal NPM v1 search response shape
// matching name-prefix matches against ListPackages. Real-quality
// search lives in a follow-up phase; this MVP gives the UI
// something to render and lets `npm search` find local packages.
func (l *Local) serveSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	text := strings.ToLower(strings.TrimSpace(q.Get("text")))
	size := 20
	if s := q.Get("size"); s != "" {
		fmt.Sscanf(s, "%d", &size)
		if size <= 0 || size > 250 {
			size = 20
		}
	}
	packages, err := l.store.ListPackages()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type searchObject struct {
		Package map[string]any `json:"package"`
	}
	type searchResponse struct {
		Objects []searchObject `json:"objects"`
		Total   int            `json:"total"`
		Time    string         `json:"time,omitempty"`
	}

	resp := searchResponse{Objects: []searchObject{}}
	for _, p := range packages {
		if text != "" && !strings.Contains(strings.ToLower(p), text) {
			continue
		}
		tags, _ := l.store.ReadDistTags(p)
		latestVer := tags["latest"]
		obj := map[string]any{
			"name":    p,
			"version": latestVer,
			"scope":   scopeOf(p),
		}
		if latestVer != "" {
			if meta, err := l.store.ReadVersionMeta(p, latestVer); err == nil {
				if d, ok := meta["description"].(string); ok {
					obj["description"] = d
				}
			}
		}
		resp.Objects = append(resp.Objects, searchObject{Package: obj})
		if len(resp.Objects) >= size {
			break
		}
	}
	resp.Total = len(resp.Objects)
	writeJSON(w, resp)
}

// scopeOf returns the scope of a package name ("@scope" → "scope")
// or "unscoped" for bare names. npm clients expect one or the other.
func scopeOf(name string) string {
	if strings.HasPrefix(name, "@") {
		if slash := strings.IndexByte(name, '/'); slash > 1 {
			return name[1:slash]
		}
	}
	return "unscoped"
}

// serveDistTags reads the dist-tags map.
func (l *Local) serveDistTags(w http.ResponseWriter, _ *http.Request, name string) {
	tags, err := l.store.ReadDistTags(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, tags)
}

// serveDistTagSet updates one dist-tag. Body is the JSON-quoted
// version string ("\"1.2.3\"") per npm convention.
func (l *Local) serveDistTagSet(w http.ResponseWriter, r *http.Request, name, tag string) {
	if !l.allowPush {
		writeError(w, http.StatusMethodNotAllowed, "writes disabled")
		return
	}
	body, err := readSmall(r.Body, 1024)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	value := strings.Trim(strings.TrimSpace(string(body)), `"`)
	if value == "" {
		writeError(w, http.StatusBadRequest, "empty version value")
		return
	}
	// Confirm the version actually exists; otherwise the dist-tag
	// would point at a non-existent target.
	exists, _ := l.store.VersionMetaExists(name, value)
	if !exists {
		writeError(w, http.StatusNotFound, fmt.Sprintf("%s@%s does not exist", name, value))
		return
	}
	tags, _ := l.store.ReadDistTags(name)
	if tags == nil {
		tags = map[string]string{}
	}
	tags[tag] = value
	if err := l.store.WriteDistTags(name, tags); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// serveDistTagDel removes one dist-tag. Refuses to delete "latest"
// — npm semantics: latest is always required.
func (l *Local) serveDistTagDel(w http.ResponseWriter, _ *http.Request, name, tag string) {
	if !l.allowPush {
		writeError(w, http.StatusMethodNotAllowed, "writes disabled")
		return
	}
	if tag == "latest" {
		writeError(w, http.StatusBadRequest, "cannot delete dist-tag 'latest'")
		return
	}
	tags, _ := l.store.ReadDistTags(name)
	if _, ok := tags[tag]; !ok {
		writeNotFound(w, fmt.Sprintf("dist-tag %s/%s not found", name, tag))
		return
	}
	delete(tags, tag)
	if err := l.store.WriteDistTags(name, tags); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// serveWhoami returns the username from the request context (set
// by the entry handler when token / session auth succeeded). The
// npm CLI uses this to verify `npm whoami`.
func serveWhoami(w http.ResponseWriter, _ *http.Request) {
	// For MVP we don't surface the pika identity through to the
	// registry handler — the auth has already been validated. Emit
	// a placeholder so npm clients don't 404; a follow-up phase
	// will plumb the resolved username through Deps.
	writeJSON(w, map[string]string{"username": "pika-user"})
}

// inferPublicBase reconstructs the pika-facing base URL of this
// registry from the incoming request. Used to rewrite tarball URLs
// in publish payloads so consumers fetch from us, not from the
// (now non-existent) original upstream.
//
// The reconstruction uses the request's Host header + scheme inferred
// from r.TLS / X-Forwarded-Proto. The path is the request URL path
// minus everything past the registry prefix (which has already been
// stripped by the manager's dispatcher), reconstructed from
// r.URL.Path.
//
// Pika is typically deployed behind a reverse proxy that sets
// X-Forwarded-Proto; we honour it for HTTPS detection.
func inferPublicBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = fh
	}
	// The request's URL.Path has had /registries/{ns}/{repo} stripped
	// by the entry handler and only "/{pkg}" remains; we need to
	// re-prepend the registry prefix. r.Header carries the original
	// path via X-Forwarded-Uri on some proxies but pika doesn't
	// require it. Instead, we use a hint header the entry handler
	// sets (X-Pika-Registry-Prefix) that contains the stripped path.
	prefix := r.Header.Get("X-Pika-Registry-Prefix")
	u := &url.URL{Scheme: scheme, Host: host, Path: prefix}
	return u.String()
}

// readSmall reads up to limit bytes from r and returns them. Used
// for small JSON bodies where we don't want to risk unbounded
// memory.
func readSmall(r io.ReadCloser, limit int64) ([]byte, error) {
	defer r.Close()
	buf, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > limit {
		return nil, fmt.Errorf("body exceeds %d bytes", limit)
	}
	return buf, nil
}

// errorsIs is a tiny alias of errors.Is, kept local so we don't
// need to spell it out at every call site.
func errorsIs(err, target error) bool { return errors.Is(err, target) }
