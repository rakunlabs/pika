package publicendpoint

import (
	"errors"
	"net/http"
	"strings"

	"github.com/rakunlabs/pika/internal/service"
)

// staticHandler implements the plain config-data shim. It is the
// simplest endpoint mode: resolve the path tail as a pika config key
// and write the bytes exactly like /data/{key}, with optional
// variant/version/format query params. No Go template and no Consul
// envelope are involved.
type staticHandler struct {
	svc      Service
	basePath string
}

func newStaticHandler(ep service.PublicEndpoint, svc Service) http.Handler {
	bp := normalizeBasePath(ep.BasePath)
	h := &staticHandler{svc: svc, basePath: bp}
	mux := http.NewServeMux()
	if bp == "" {
		mux.HandleFunc("/", h.serve)
	} else {
		mux.HandleFunc(bp+"/", h.serve)
		mux.HandleFunc(bp, h.serve)
	}
	return mux
}

func (h *staticHandler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed: static shim is read-only")
		return
	}

	key := h.resolveKey(r.URL.Path)
	if key == "" {
		writeJSONError(w, http.StatusBadRequest, "missing key")
		return
	}

	q := r.URL.Query()
	variant := q.Get("variant")
	version := q.Get("version")
	requestedFormat := q.Get("format")

	result, err := h.svc.GetData(r.Context(), key, version, variant)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		if errors.Is(err, service.ErrBadRequest) {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSONError(w, http.StatusBadGateway, "upstream resolve failed: "+err.Error())
		return
	}
	if result == nil {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	if result.Error != "" {
		writeJSONError(w, http.StatusBadRequest, "configuration has errors: "+result.Error)
		return
	}

	body := result.Data
	format := result.Format
	if requestedFormat != "" && requestedFormat != format {
		converted, convErr := service.ConvertFormat(body, format, requestedFormat)
		if convErr != nil {
			writeJSONError(w, http.StatusBadRequest, "format conversion: "+convErr.Error())
			return
		}
		body = converted
		format = requestedFormat
	}

	w.Header().Set("Content-Type", contentTypeForFormat(format))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *staticHandler) resolveKey(path string) string {
	p := path
	if h.basePath != "" {
		p = strings.TrimPrefix(p, h.basePath)
	}
	return strings.TrimPrefix(strings.TrimSuffix(p, "/"), "/")
}

func contentTypeForFormat(format string) string {
	switch strings.ToLower(format) {
	case "json":
		return "application/json"
	case "yaml", "yml":
		return "application/x-yaml"
	case "toml":
		return "application/toml"
	default:
		return "application/octet-stream"
	}
}
