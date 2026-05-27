package publicendpoint

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	extpkg "github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/service"
)

// externalHandler implements the direct External-resource shim. It
// maps the endpoint path tail to the selected resource's provider-
// specific path and returns the provider entry without going through
// pika's /data inheritance/render pipeline.
type externalHandler struct {
	svc      Service
	basePath string
	resource string
}

func newExternalHandler(ep service.PublicEndpoint, svc Service) http.Handler {
	bp := normalizeBasePath(ep.BasePath)
	resource := ""
	if ep.External != nil {
		resource = ep.External.Resource
	}
	h := &externalHandler{svc: svc, basePath: bp, resource: resource}
	mux := http.NewServeMux()
	if bp == "" {
		mux.HandleFunc("/", h.serve)
	} else {
		mux.HandleFunc(bp+"/", h.serve)
		mux.HandleFunc(bp, h.serve)
	}
	return mux
}

func (h *externalHandler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed: external shim is read-only")
		return
	}
	if strings.TrimSpace(h.resource) == "" {
		writeJSONError(w, http.StatusInternalServerError, "external shim: missing resource")
		return
	}

	path := h.resolvePath(r.URL.Path)
	q := r.URL.Query()
	version := q.Get("version")

	var (
		entry *extpkg.Entry
		err   error
	)
	if version != "" {
		entry, err = h.svc.ReadExternalVersion(r.Context(), h.resource, path, version)
	} else {
		entry, err = h.svc.ReadExternal(r.Context(), h.resource, path)
	}
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		if errors.Is(err, extpkg.ErrNotSupported) {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, service.ErrBadRequest) {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSONError(w, http.StatusBadGateway, "external read failed: "+err.Error())
		return
	}
	writeExternalEntry(w, entry)
}

func (h *externalHandler) resolvePath(path string) string {
	p := path
	if h.basePath != "" {
		p = strings.TrimPrefix(p, h.basePath)
	}
	return strings.TrimPrefix(strings.TrimSuffix(p, "/"), "/")
}

func writeExternalEntry(w http.ResponseWriter, entry *extpkg.Entry) {
	if entry == nil {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	if entry.Raw != nil {
		ct := entry.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(entry.Raw)
		return
	}
	if entry.Data != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(entry.Data)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
