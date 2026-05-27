package proxy

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/common"
	"github.com/rakunlabs/pika/internal/registry/npm"
	"github.com/rakunlabs/pika/internal/service"
)

// rawHandlerBuilder is the legacy handler builder signature. Every
// handler returns an http.Handler that owns the response (no `next`
// chained). adaptHandler wraps it into the unified NodeBuilder
// shape: the resulting Middleware ignores `next` so the handler
// stays terminal even though Compile composes it like any other
// node.
type rawHandlerBuilder func(cfg json.RawMessage, svc ServiceDeps) (http.Handler, error)

func adaptHandler(b rawHandlerBuilder) NodeBuilder {
	return func(cfg json.RawMessage, svc ServiceDeps, _ BranchSet) (Middleware, error) {
		h, err := b(cfg, svc)
		if err != nil {
			return nil, err
		}
		// Terminal: produce a Middleware that always serves h and
		// drops `next` on the floor. Compile already rejects any
		// downstream edge from a handler, so this discarding is
		// only ever observed when a switch's branch terminates in
		// this handler — exactly the intended behaviour.
		return func(_ http.Handler) http.Handler { return h }, nil
	}
}

// DefaultHandlers returns the registered handler kinds. Handlers do
// NOT carry a "path" or "strip_prefix" field any more — path
// matching moved to the switch node and handlers see r.URL.Path as
// the listener received it. Existing graphs that still embed a
// "path" key in handler config are ignored: the field is left in
// the raw JSON, Compile no longer reads it, and a save round-trip
// preserves it (so an operator can flip back if they need to).
func DefaultHandlers() map[string]NodeSpec {
	specs := []NodeSpec{
		{
			Kind:        KindHandler,
			Subtype:     "data",
			Label:       "Config resource",
			Description: "Serve resolved configuration files. Same engine as the main /data endpoint.",
			Build:       adaptHandler(buildDataHandler),
		},
		{
			Kind:        KindHandler,
			Subtype:     "raw",
			Label:       "Raw mount resource",
			Description: "Serve files from one configured raw mount, with optional PUT/DELETE/MKDIR writes.",
			Build:       adaptHandler(buildRawResourceHandler),
		},
		{
			Kind:        KindHandler,
			Subtype:     "registry",
			Label:       "Registry resource",
			Description: "Expose one configured artifact registry repository through this proxy listener.",
			Build:       adaptHandler(buildRegistryResourceHandler),
		},
		{
			Kind:        KindHandler,
			Subtype:     "cdn",
			Label:       "Package CDN resource",
			Description: "Serve jsDelivr-style files from one configured NPM registry repository.",
			Build:       adaptHandler(buildCDNResourceHandler),
		},
		{
			Kind:        KindHandler,
			Subtype:     "external",
			Label:       "External resource",
			Description: "Read entries from a configured external resource (Vault, Consul, etcd, AWS, ...).",
			Build:       adaptHandler(buildExternalHandler),
		},
		{
			Kind:        KindHandler,
			Subtype:     "consul-kv",
			Label:       "Consul KV compatibility",
			Description: "Expose configurations under the Consul /v1/kv API shape (?raw supported).",
			Build:       adaptHandler(buildConsulKVHandler),
		},
		{
			Kind:        KindHandler,
			Subtype:     "healthz",
			Label:       "Health check",
			Description: "Always responds 200 OK with the configured body.",
			Build:       adaptHandler(buildHealthzHandler),
		},
		{
			Kind:        KindHandler,
			Subtype:     "static-response",
			Label:       "Static / mock response",
			Description: "Return a fixed status, headers and body. Useful for mocks and stubs.",
			Build:       adaptHandler(buildStaticResponseHandler),
		},
		{
			Kind:        KindHandler,
			Subtype:     "redirect",
			Label:       "Redirect",
			Description: "Send a 30x response to the configured URL.",
			Build:       adaptHandler(buildRedirectHandler),
		},
		{
			Kind:        KindHandler,
			Subtype:     "proxy-pass",
			Label:       "Reverse proxy",
			Description: "Forward requests to an upstream URL (httputil.ReverseProxy). WebSocket upgrades are passed through transparently.",
			Build:       adaptHandler(buildProxyPassHandler),
		},
		{
			Kind:        KindHandler,
			Subtype:     "custom-response",
			Label:       "Custom response",
			Description: "Return a Go-template body that can reference the request (path, method, headers, query). The richer cousin of static-response — use when the body needs to vary per call.",
			Build:       adaptHandler(buildCustomResponseHandler),
		},
		{
			Kind:        KindHandler,
			Subtype:     "http-request",
			Label:       "Outbound HTTP request",
			Description: "Send the request to an upstream URL with templated url/method/headers/body, optional basic/bearer/oauth2 auth, retry and TLS overrides. Ported from chore; uses rakunlabs/ok.",
			Build:       adaptHandler(buildHTTPRequestHandler),
		},
	}

	out := make(map[string]NodeSpec, len(specs))
	for _, s := range specs {
		out[s.Subtype] = s
	}
	return out
}

// commonHandlerCfg used to embed a "path" field shared across every
// handler. With path matching moved to the switch node it now only
// carries fields that survived the refactor — currently empty, kept
// as a named struct so future per-handler shared fields land in one
// place.
type commonHandlerCfg struct{}

// --- data ---

type dataHandlerCfg struct {
	commonHandlerCfg
	// StripPrefix is removed from the URL path before being passed
	// to service.GetData. Defaults to the path of the route minus
	// the trailing "/*", which is what most operators expect ("/data
	// mounted at /foo/* serves bar.json as /foo/bar.json").
	StripPrefix string `json:"strip_prefix,omitempty"`
	// DefaultFormat is the format used when the client does not
	// supply ?format=. Empty means "use the file's stored format"
	// — same behaviour as the main /data endpoint.
	DefaultFormat string `json:"default_format,omitempty"`
}

func buildDataHandler(raw json.RawMessage, svc ServiceDeps) (http.Handler, error) {
	if svc == nil {
		return nil, errors.New("data: service dependency missing")
	}
	cfg := dataHandlerCfg{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("data handler config: %w", err)
		}
	}
	stripPrefix := normaliseStripPrefix(cfg.StripPrefix)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, stripPrefix)
		key = strings.TrimPrefix(key, "/")
		q := r.URL.Query()
		result, err := svc.GetData(r.Context(), key, q.Get("version"), q.Get("variant"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		if result.Error != "" {
			http.Error(w, "configuration has errors: "+result.Error, http.StatusBadRequest)
			return
		}
		requested := q.Get("format")
		if requested == "" {
			requested = cfg.DefaultFormat
		}
		out := result.Data
		format := result.Format
		if requested != "" && requested != result.Format {
			converted, cerr := svc.ConvertFormat(result.Data, result.Format, requested)
			if cerr != nil {
				http.Error(w, fmt.Sprintf("convert %s -> %s: %v", result.Format, requested, cerr), http.StatusBadRequest)
				return
			}
			out = converted
			format = requested
		}
		setFormatContentType(w, format)
		w.WriteHeader(http.StatusOK)
		w.Write(out)
	}), nil
}

func setFormatContentType(w http.ResponseWriter, format string) {
	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
	case "yaml", "yml":
		w.Header().Set("Content-Type", "application/x-yaml")
	case "toml":
		w.Header().Set("Content-Type", "application/toml")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
}

// --- raw ---

type rawResourceCfg struct {
	commonHandlerCfg
	// Mount is the Raw mount prefix from Settings.Raw.
	Mount string `json:"mount"`
	// StripPrefix is removed from URL.Path before looking up the file
	// inside Mount. Empty means the handler sees URL.Path from the root.
	StripPrefix string `json:"strip_prefix,omitempty"`
	// AllowWrite enables PUT, DELETE and POST-as-mkdir against writable mounts.
	AllowWrite bool `json:"allow_write,omitempty"`
	// DirectoryListing enables JSON directory responses for directory paths.
	DirectoryListing bool `json:"directory_listing,omitempty"`
}

func buildRawResourceHandler(raw json.RawMessage, svc ServiceDeps) (http.Handler, error) {
	if svc == nil {
		return nil, errors.New("raw: service dependency missing")
	}
	var cfg rawResourceCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("raw handler config: %w", err)
		}
	}
	if cfg.Mount == "" {
		return nil, errors.New("raw handler: mount is required")
	}
	stripPrefix := normaliseStripPrefix(cfg.StripPrefix)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fsys, ok := svc.MountRawFS(cfg.Mount)
		if !ok {
			writeServiceError(w, fmt.Errorf("raw mount %q not found: %w", cfg.Mount, service.ErrNotFound))
			return
		}
		path := resourcePath(r.URL.Path, stripPrefix)
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			serveRawResource(w, r, fsys, path, cfg.DirectoryListing)
		case http.MethodPut:
			if !cfg.AllowWrite {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			writeRawResource(w, fsys, path, r.Body, r.ContentLength)
		case http.MethodDelete:
			if !cfg.AllowWrite {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			deleteRawResource(w, fsys, path)
		case http.MethodPost:
			if !cfg.AllowWrite {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			mkdirRawResource(w, fsys, path)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}), nil
}

func serveRawResource(w http.ResponseWriter, r *http.Request, fsys rawfs.RawFS, path string, directoryListing bool) {
	info, err := fsys.Stat(path)
	if err != nil {
		writeServiceError(w, mapRawFSError(err))
		return
	}
	if info.IsDir {
		if !directoryListing {
			http.Error(w, "directory listing disabled", http.StatusForbidden)
			return
		}
		entries, err := fsys.ReadDir(path)
		if err != nil {
			writeServiceError(w, mapRawFSError(err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(entries)
		return
	}
	serveRawFile(w, r, fsys, path)
}

func serveRawFile(w http.ResponseWriter, r *http.Request, fsys rawfs.RawFS, path string) {
	reader, info, err := fsys.Open(path)
	if err != nil {
		writeServiceError(w, mapRawFSError(err))
		return
	}
	defer reader.Close()

	ext := filepath.Ext(info.Name)
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		buf := make([]byte, 512)
		n, _ := reader.Read(buf)
		contentType = http.DetectContentType(buf[:n])
		if _, err := reader.Seek(0, io.SeekStart); err != nil {
			http.Error(w, "seeking file: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", contentType)

	modTime := info.ModTime
	if modTime.IsZero() {
		modTime = time.Now()
	}
	http.ServeContent(w, r, info.Name, modTime, reader)
}

func writeRawResource(w http.ResponseWriter, fsys rawfs.RawFS, path string, body io.Reader, size int64) {
	wfs, ok := fsys.(rawfs.WritableRawFS)
	if !ok {
		writeServiceError(w, fmt.Errorf("raw mount is read-only: %w", service.ErrBadRequest))
		return
	}
	if err := wfs.Write(path, body, size); err != nil {
		writeServiceError(w, mapRawFSError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func deleteRawResource(w http.ResponseWriter, fsys rawfs.RawFS, path string) {
	wfs, ok := fsys.(rawfs.WritableRawFS)
	if !ok {
		writeServiceError(w, fmt.Errorf("raw mount is read-only: %w", service.ErrBadRequest))
		return
	}
	if err := wfs.Delete(path); err != nil {
		writeServiceError(w, mapRawFSError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func mkdirRawResource(w http.ResponseWriter, fsys rawfs.RawFS, path string) {
	wfs, ok := fsys.(rawfs.WritableRawFS)
	if !ok {
		writeServiceError(w, fmt.Errorf("raw mount is read-only: %w", service.ErrBadRequest))
		return
	}
	if err := wfs.MkDir(path); err != nil {
		writeServiceError(w, mapRawFSError(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func mapRawFSError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%v: %w", err, service.ErrNotFound)
	}
	return err
}

// --- registry ---

type registryResourceCfg struct {
	commonHandlerCfg
	Namespace   string `json:"namespace"`
	Repository  string `json:"repository"`
	StripPrefix string `json:"strip_prefix,omitempty"`
	// RequireToken preserves the normal registry token model by default.
	RequireToken *bool `json:"require_token,omitempty"`
}

func buildRegistryResourceHandler(raw json.RawMessage, svc ServiceDeps) (http.Handler, error) {
	if svc == nil {
		return nil, errors.New("registry: service dependency missing")
	}
	var cfg registryResourceCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("registry handler config: %w", err)
		}
	}
	if cfg.Namespace == "" || cfg.Repository == "" {
		return nil, errors.New("registry handler: namespace and repository are required")
	}
	stripPrefix := normaliseStripPrefix(cfg.StripPrefix)
	requireToken := cfg.RequireToken == nil || *cfg.RequireToken

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !svc.RegistryEnabled(r.Context()) {
			writeServiceError(w, fmt.Errorf("registry feature disabled: %w", service.ErrNotFound))
			return
		}
		reg, ok := svc.LookupRegistry(cfg.Namespace, cfg.Repository)
		if !ok {
			writeServiceError(w, fmt.Errorf("registry %s/%s not found: %w", cfg.Namespace, cfg.Repository, service.ErrNotFound))
			return
		}

		if common.ApplyCORS(w, r, svc.RegistryCORSOrigins(r.Context(), cfg.Namespace, cfg.Repository)) {
			return
		}

		rest := registryResourcePath(r.URL.Path, stripPrefix)
		if requireToken {
			tokenRaw := common.ExtractToken(r)
			if tokenRaw == "" {
				writeServiceError(w, fmt.Errorf("missing pika token: %w", service.ErrUnauthorized))
				return
			}
			scope := "registry/" + cfg.Namespace + "/" + cfg.Repository + rest
			if err := svc.ValidateToken(r.Context(), tokenRaw, scope, registryOperationFor(r.Method)); err != nil {
				writeServiceError(w, err)
				return
			}
		}

		r2 := cloneRequestWithResourcePath(r, rest)
		r2.Header.Set("X-Pika-Registry-Prefix", stripPrefix)
		reg.ServeHTTP(w, r2)
	}), nil
}

func registryOperationFor(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return common.OpRead
	case http.MethodDelete:
		return common.OpDelete
	default:
		return common.OpWrite
	}
}

func registryResourcePath(path, stripPrefix string) string {
	rest := resourcePath(path, stripPrefix)
	if rest == "" {
		return "/"
	}
	return "/" + rest
}

// --- package CDN ---

type cdnResourceCfg struct {
	commonHandlerCfg
	Namespace   string `json:"namespace"`
	Repository  string `json:"repository"`
	StripPrefix string `json:"strip_prefix,omitempty"`
	// RequireToken is false by default because CDN proxy resources are
	// commonly published for browser/runtime asset fetches. Operators who
	// want protected CDN paths can either set this true or put an
	// auth-bearer middleware before the handler.
	RequireToken *bool `json:"require_token,omitempty"`
}

func buildCDNResourceHandler(raw json.RawMessage, svc ServiceDeps) (http.Handler, error) {
	if svc == nil {
		return nil, errors.New("cdn: service dependency missing")
	}
	var cfg cdnResourceCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("cdn handler config: %w", err)
		}
	}
	if cfg.Namespace == "" || cfg.Repository == "" {
		return nil, errors.New("cdn handler: namespace and repository are required")
	}
	stripPrefix := normaliseStripPrefix(cfg.StripPrefix)
	requireToken := cfg.RequireToken != nil && *cfg.RequireToken

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !svc.RegistryEnabled(r.Context()) {
			writeServiceError(w, fmt.Errorf("registry feature disabled: %w", service.ErrNotFound))
			return
		}
		reg, ok := svc.LookupRegistry(cfg.Namespace, cfg.Repository)
		if !ok {
			writeServiceError(w, fmt.Errorf("registry %s/%s not found: %w", cfg.Namespace, cfg.Repository, service.ErrNotFound))
			return
		}
		if reg.Type() != service.RegistryTypeNPM {
			writeServiceError(w, fmt.Errorf("registry %s/%s is not an npm registry: %w", cfg.Namespace, cfg.Repository, service.ErrBadRequest))
			return
		}
		provider, ok := reg.(registry.CDNAssetProvider)
		if !ok {
			writeServiceError(w, fmt.Errorf("registry %s/%s does not support CDN assets: %w", cfg.Namespace, cfg.Repository, service.ErrBadRequest))
			return
		}
		if common.ApplyCORS(w, r, svc.RegistryCORSOrigins(r.Context(), cfg.Namespace, cfg.Repository)) {
			return
		}

		rest := registryResourcePath(r.URL.Path, stripPrefix)
		if requireToken {
			tokenRaw := common.ExtractToken(r)
			if tokenRaw == "" {
				writeServiceError(w, fmt.Errorf("missing pika token: %w", service.ErrUnauthorized))
				return
			}
			scope := "registry/" + cfg.Namespace + "/" + cfg.Repository + rest
			if err := svc.ValidateToken(r.Context(), tokenRaw, scope, common.OpRead); err != nil {
				writeServiceError(w, err)
				return
			}
		}

		asset, err := npm.ParseCDNAssetPath(rest)
		if err != nil {
			writeServiceError(w, fmt.Errorf("invalid npm CDN asset path: %w: %w", err, service.ErrBadRequest))
			return
		}
		provider.ServeCDNAsset(w, r, asset)
	}), nil
}

func resourcePath(path, stripPrefix string) string {
	if stripPrefix != "" {
		path = strings.TrimPrefix(path, stripPrefix)
	}
	return strings.TrimPrefix(path, "/")
}

func cloneRequestWithResourcePath(r *http.Request, newPath string) *http.Request {
	r2 := r.Clone(r.Context())
	if r2.URL != nil {
		u := *r2.URL
		u.Path = newPath
		u.RawPath = ""
		r2.URL = &u
	}
	return r2
}

// --- external ---

type externalHandlerCfg struct {
	commonHandlerCfg
	// Resource is the external resource name (Settings.External
	// map key). Required.
	Resource string `json:"resource"`
	// StripPrefix removes a leading portion of URL.Path before it
	// becomes the entry path under the resource.
	StripPrefix string `json:"strip_prefix,omitempty"`
	// AllowWrite enables PUT/DELETE forwarding to WriteExternal /
	// DeleteExternal. Off by default — the most common deployment
	// is read-only exposure.
	AllowWrite bool `json:"allow_write,omitempty"`
}

func buildExternalHandler(raw json.RawMessage, svc ServiceDeps) (http.Handler, error) {
	if svc == nil {
		return nil, errors.New("external: service dependency missing")
	}
	var cfg externalHandlerCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("external handler config: %w", err)
		}
	}
	if cfg.Resource == "" {
		return nil, errors.New("external handler: resource is required")
	}
	stripPrefix := normaliseStripPrefix(cfg.StripPrefix)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entryPath := strings.TrimPrefix(r.URL.Path, stripPrefix)
		entryPath = strings.TrimPrefix(entryPath, "/")
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			entry, err := svc.ReadExternal(r.Context(), cfg.Resource, entryPath)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			writeExternalEntry(w, entry)
		case http.MethodPut, http.MethodPost:
			if !cfg.AllowWrite {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var data map[string]any
			if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
				http.Error(w, "invalid json body: "+err.Error(), http.StatusBadRequest)
				return
			}
			if err := svc.WriteExternal(r.Context(), cfg.Resource, entryPath, data); err != nil {
				writeServiceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			if !cfg.AllowWrite {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if err := svc.DeleteExternal(r.Context(), cfg.Resource, entryPath); err != nil {
				writeServiceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}), nil
}

// writeExternalEntry mirrors the api.readExternalEntry response shape
// while preferring Raw bytes when present.
func writeExternalEntry(w http.ResponseWriter, e interface { /* satisfied by *external.Entry */
}) {
	type entryLike struct {
		Data        map[string]any `json:"data,omitempty"`
		Raw         []byte         `json:"raw,omitempty"`
		ContentType string         `json:"content_type,omitempty"`
		Version     string         `json:"version,omitempty"`
	}
	// Marshal the typed Entry by round-tripping through JSON so we
	// don't need to import the external package here. The cost is a
	// single encode per request and the surface stays decoupled.
	buf, err := json.Marshal(e)
	if err != nil {
		http.Error(w, "encode entry: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var view entryLike
	if err := json.Unmarshal(buf, &view); err != nil {
		http.Error(w, "decode entry: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if len(view.Raw) > 0 {
		if view.ContentType != "" {
			w.Header().Set("Content-Type", view.ContentType)
		} else {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		w.WriteHeader(http.StatusOK)
		w.Write(view.Raw)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(view.Data)
}

// --- consul-kv ---

type consulKVCfg struct {
	commonHandlerCfg
	// StripPrefix is removed before splitting into key. Defaults to
	// "{path}/v1/kv" so a route mounted at /consul serves
	// /consul/v1/kv/<key>.
	StripPrefix string `json:"strip_prefix,omitempty"`
}

func buildConsulKVHandler(raw json.RawMessage, svc ServiceDeps) (http.Handler, error) {
	if svc == nil {
		return nil, errors.New("consul-kv: service dependency missing")
	}
	var cfg consulKVCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("consul-kv config: %w", err)
		}
	}
	// Strip everything before the Consul "/v1/kv/..." key. Default
	// is just "/v1/kv" because path matching now lives in the
	// switch node — the operator chooses where they mount consul-kv
	// and tells us the prefix here if they parked it under
	// something other than the bare /v1/kv.
	stripPrefix := normaliseStripPrefix(cfg.StripPrefix)
	if stripPrefix == "" {
		stripPrefix = "/v1/kv"
	}
	type kvEntry struct {
		CreateIndex int64  `json:"CreateIndex"`
		ModifyIndex int64  `json:"ModifyIndex"`
		LockIndex   int64  `json:"LockIndex"`
		Key         string `json:"Key"`
		Flags       int64  `json:"Flags"`
		Value       string `json:"Value"`
		Session     string `json:"Session"`
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, stripPrefix)
		key = strings.TrimPrefix(key, "/")
		q := r.URL.Query()
		result, err := svc.GetData(r.Context(), key, q.Get("version"), q.Get("variant"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		out := result.Data
		format := result.Format
		if rf := q.Get("format"); rf != "" && rf != result.Format {
			conv, cerr := svc.ConvertFormat(result.Data, result.Format, rf)
			if cerr != nil {
				http.Error(w, fmt.Sprintf("convert: %v", cerr), http.StatusBadRequest)
				return
			}
			out = conv
			format = rf
		}
		if _, raw := q["raw"]; raw {
			setFormatContentType(w, format)
			w.WriteHeader(http.StatusOK)
			w.Write(out)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]kvEntry{{
			Key:   key,
			Value: base64.StdEncoding.EncodeToString(out),
		}})
	}), nil
}

// --- healthz ---

type healthzCfg struct {
	commonHandlerCfg
	Body string `json:"body,omitempty"` // default "OK"
}

func buildHealthzHandler(raw json.RawMessage, _ ServiceDeps) (http.Handler, error) {
	var cfg healthzCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("healthz config: %w", err)
		}
	}
	body := cfg.Body
	if body == "" {
		body = "OK"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	}), nil
}

// --- static-response ---

type staticResponseCfg struct {
	commonHandlerCfg
	Status      int               `json:"status,omitempty"` // default 200
	ContentType string            `json:"content_type,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        string            `json:"body,omitempty"`
	// BodyBase64 lets the UI ship binary bodies through the JSON
	// config without escaping.
	BodyBase64 string `json:"body_base64,omitempty"`
}

func buildStaticResponseHandler(raw json.RawMessage, _ ServiceDeps) (http.Handler, error) {
	var cfg staticResponseCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("static-response config: %w", err)
		}
	}
	if cfg.Status == 0 {
		cfg.Status = http.StatusOK
	}
	var body []byte
	switch {
	case cfg.BodyBase64 != "":
		b, err := base64.StdEncoding.DecodeString(cfg.BodyBase64)
		if err != nil {
			return nil, fmt.Errorf("static-response body_base64: %w", err)
		}
		body = b
	case cfg.Body != "":
		body = []byte(cfg.Body)
	}
	ct := cfg.ContentType
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range cfg.Headers {
			w.Header().Set(k, v)
		}
		if ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.WriteHeader(cfg.Status)
		if len(body) > 0 {
			w.Write(body)
		}
	}), nil
}

// --- redirect ---

type redirectCfg struct {
	commonHandlerCfg
	Target string `json:"target"`
	// Status defaults to 302 (Found). Use 301 for permanent, 307/308
	// to preserve the request method on the follow-up.
	Status int `json:"status,omitempty"`
	// PreservePath, when true, appends the incoming path-after-prefix
	// to the target. Useful for "rewrite /old/* -> /new/*" cases.
	PreservePath bool   `json:"preserve_path,omitempty"`
	StripPrefix  string `json:"strip_prefix,omitempty"`
}

func buildRedirectHandler(raw json.RawMessage, _ ServiceDeps) (http.Handler, error) {
	var cfg redirectCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("redirect config: %w", err)
		}
	}
	if cfg.Target == "" {
		return nil, errors.New("redirect: target is required")
	}
	if cfg.Status == 0 {
		cfg.Status = http.StatusFound
	}
	stripPrefix := normaliseStripPrefix(cfg.StripPrefix)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dest := cfg.Target
		if cfg.PreservePath {
			rest := strings.TrimPrefix(r.URL.Path, stripPrefix)
			dest = strings.TrimRight(cfg.Target, "/") + rest
			if r.URL.RawQuery != "" {
				if strings.Contains(dest, "?") {
					dest += "&" + r.URL.RawQuery
				} else {
					dest += "?" + r.URL.RawQuery
				}
			}
		}
		http.Redirect(w, r, dest, cfg.Status)
	}), nil
}

// --- proxy-pass ---

type proxyPassCfg struct {
	commonHandlerCfg
	Target            string            `json:"target"`
	StripPrefix       string            `json:"strip_prefix,omitempty"`
	PreserveHost      bool              `json:"preserve_host,omitempty"`
	SetRequestHeaders map[string]string `json:"set_request_headers,omitempty"`
	InsecureSkipTLS   bool              `json:"insecure_skip_tls,omitempty"`
}

func buildProxyPassHandler(raw json.RawMessage, _ ServiceDeps) (http.Handler, error) {
	var cfg proxyPassCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("proxy-pass config: %w", err)
		}
	}
	if cfg.Target == "" {
		return nil, errors.New("proxy-pass: target is required")
	}
	target, err := url.Parse(cfg.Target)
	if err != nil {
		return nil, fmt.Errorf("proxy-pass target: %w", err)
	}
	if target.Scheme == "" || target.Host == "" {
		return nil, errors.New("proxy-pass: target must be an absolute URL")
	}
	stripPrefix := normaliseStripPrefix(cfg.StripPrefix)

	rp := httputil.NewSingleHostReverseProxy(target)
	origDirector := rp.Director
	rp.Director = func(r *http.Request) {
		origDirector(r)
		if stripPrefix != "" {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, stripPrefix)
			if !strings.HasPrefix(r.URL.Path, "/") {
				r.URL.Path = "/" + r.URL.Path
			}
		}
		if cfg.PreserveHost {
			r.Host = target.Host
		}
		for k, v := range cfg.SetRequestHeaders {
			r.Header.Set(k, v)
		}
	}
	if cfg.InsecureSkipTLS {
		rp.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	return rp, nil
}

// normaliseStripPrefix sanitises the explicit strip_prefix the user
// typed into a handler config. Returns "" when the field is empty —
// the handler then sees URL.Path unchanged.
//
// (Pre-switch the helper also derived a default from the route's
// own mount path; that fallback is gone because path matching moved
// out of the handler into the switch node. Operators who want the
// pre-switch behaviour must set strip_prefix explicitly.)
func normaliseStripPrefix(explicit string) string {
	p := strings.TrimSpace(explicit)
	if p == "" {
		return ""
	}
	return strings.TrimRight(p, "/")
}

// --- custom-response ---

// customResponseCfg is the richer cousin of static-response. The
// body is a Go text/template that runs once per request with a
// small context exposing the bits of the request a templated
// response usually needs:
//
//	.Method        — HTTP method ("GET").
//	.Path          — r.URL.Path verbatim.
//	.RawQuery      — r.URL.RawQuery.
//	.Query.<key>   — first query value for <key>, empty string if missing.
//	.Headers.<key> — first request header for <key> (case-insensitive
//	                 canonical key, e.g. .Headers.X_Forwarded_For).
//	.Host          — r.Host.
//	.RemoteAddr    — r.RemoteAddr (with port).
//	.Now           — server-side time.Time at render.
//
// We deliberately do NOT expose request bodies — that would
// double-buffer and surprise operators when an upstream sends a
// 100MB payload through.
type customResponseCfg struct {
	commonHandlerCfg
	Status      int               `json:"status,omitempty"` // default 200
	ContentType string            `json:"content_type,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	// Body is the raw template source. We parse it once at build
	// time so bad templates surface as a CompileError instead of
	// a 500 at request time.
	Body string `json:"body,omitempty"`
}

type customResponseCtx struct {
	Method     string
	Path       string
	RawQuery   string
	Host       string
	RemoteAddr string
	Query      map[string]string
	Headers    map[string]string
	Now        time.Time
}

func buildCustomResponseHandler(raw json.RawMessage, _ ServiceDeps) (http.Handler, error) {
	var cfg customResponseCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("custom-response config: %w", err)
		}
	}
	if cfg.Status == 0 {
		cfg.Status = http.StatusOK
	}

	tmpl, err := template.New("custom-response").Parse(cfg.Body)
	if err != nil {
		return nil, fmt.Errorf("custom-response body template: %w", err)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := customResponseCtx{
			Method:     r.Method,
			Path:       r.URL.Path,
			RawQuery:   r.URL.RawQuery,
			Host:       r.Host,
			RemoteAddr: r.RemoteAddr,
			Query:      flattenSingleValue(r.URL.Query()),
			Headers:    canonicalHeaderMap(r.Header),
			Now:        time.Now(),
		}
		var buf strings.Builder
		if err := tmpl.Execute(&buf, ctx); err != nil {
			http.Error(w, "custom-response render: "+err.Error(), http.StatusInternalServerError)
			return
		}

		for k, v := range cfg.Headers {
			w.Header().Set(k, v)
		}
		if cfg.ContentType != "" {
			w.Header().Set("Content-Type", cfg.ContentType)
		}
		w.WriteHeader(cfg.Status)
		_, _ = io.WriteString(w, buf.String())
	}), nil
}

// flattenSingleValue keeps the first value per key. Templates
// almost always want a scalar; .Query.foo reading [""string"" ""
// "another""] would force every template to {{ index .Query.foo 0 }}.
func flattenSingleValue(m url.Values) map[string]string {
	out := make(map[string]string, len(m))
	for k, vs := range m {
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}

// canonicalHeaderMap converts http.Header to a map[string]string,
// keyed by the canonical name with hyphens swapped for underscores
// so the template syntax .Headers.X_Forwarded_For works (Go
// templates choke on hyphens in identifiers).
func canonicalHeaderMap(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		if len(vs) == 0 {
			continue
		}
		out[strings.ReplaceAll(k, "-", "_")] = vs[0]
	}
	return out
}

// writeServiceError maps service sentinel errors to HTTP statuses.
// Centralised so every handler reacts the same way to a missing
// configuration or a forbidden token.
func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, service.ErrBadRequest):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, service.ErrUnauthorized):
		http.Error(w, err.Error(), http.StatusUnauthorized)
	case errors.Is(err, service.ErrForbidden):
		http.Error(w, err.Error(), http.StatusForbidden)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
