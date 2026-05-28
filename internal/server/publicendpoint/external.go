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
//
// The per-endpoint override fields (rawOverride, ctOverride) come
// from ExternalCompat and let one resource expose different shapes
// on different listeners. They are applied AFTER the provider's
// Read returns; nil/empty means "inherit whatever the provider
// produced", which is the path that keeps existing endpoints
// behaving identically to before the override feature shipped.
type externalHandler struct {
	svc         Service
	basePath    string
	resource    string
	rawOverride *bool
	ctOverride  string
}

func newExternalHandler(ep service.PublicEndpoint, svc Service) http.Handler {
	bp := normalizeBasePath(ep.BasePath)
	resource := ""
	var rawOverride *bool
	ctOverride := ""
	if ep.External != nil {
		resource = ep.External.Resource
		rawOverride = ep.External.RawValue
		ctOverride = ep.External.ContentType
	}
	h := &externalHandler{
		svc:         svc,
		basePath:    bp,
		resource:    resource,
		rawOverride: rawOverride,
		ctOverride:  ctOverride,
	}
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
	writeExternalEntry(w, applyEndpointOverrides(entry, h.rawOverride, h.ctOverride))
}

// applyEndpointOverrides returns a new Entry shaped by the
// per-endpoint overrides. The original entry is not mutated so
// concurrent callers (the same provider may be cached and shared)
// observe a stable upstream view.
//
// Semantics:
//
//   - rawOverride == nil, ctOverride == "": no-op, entry is returned
//     unchanged. This is the steady-state for endpoints that haven't
//     configured an override.
//   - rawOverride == &true: force raw byte serving. If the upstream
//     already provided Raw bytes, those are kept; otherwise we try
//     to extract the inner string from a single-key {"value": "str"}
//     wrapper, then fall back to a JSON-marshal of Data. ctOverride
//     wins when set; otherwise the entry's existing ContentType
//     is kept, defaulting to application/yaml when the entry didn't
//     carry one (matches GCP raw-mode default).
//   - rawOverride == &false: force the legacy JSON wrapper. If only
//     Raw bytes were available upstream, they're re-wrapped as
//     {"value": "<string>"}; otherwise Data is JSON-marshaled as-is.
//     ContentType is ctOverride or "application/json".
//   - rawOverride == nil, ctOverride != "": passthrough on shape but
//     swap the Content-Type. Useful when the operator only wants to
//     re-label the response (e.g. tell consumers it's YAML even when
//     upstream tagged it text/plain).
//
// We deliberately clear out.Data after building the raw-bytes path
// so writeExternalEntry takes the Raw branch and emits the bytes
// verbatim (its Data branch would re-encode through json.Encoder,
// which appends a newline and forces application/json).
func applyEndpointOverrides(entry *extpkg.Entry, rawOverride *bool, ctOverride string) *extpkg.Entry {
	if entry == nil {
		return nil
	}
	out := &extpkg.Entry{
		Data:        entry.Data,
		Raw:         entry.Raw,
		ContentType: entry.ContentType,
		Version:     entry.Version,
	}
	ct := strings.TrimSpace(ctOverride)

	if rawOverride != nil {
		if *rawOverride {
			// Force raw byte output. We always check for a
			// value-wrapper first because upstream providers
			// (GCP/AWS/Vault/HTTP/Consul/etcd in their wrapped
			// modes) populate BOTH Raw (the marshalled wrapper)
			// and Data with the same wrapper map. Skipping the
			// unwrap when Raw is already set would serve the
			// JSON-encoded `{"value":"..."}` bytes instead of
			// the wrapped value the operator actually wants.
			switch {
			case singleValueStringOK(out.Data):
				v, _ := singleValueString(out.Data)
				out.Raw = []byte(v)
			case out.Raw != nil:
				// Truly raw upstream (no wrapper) — keep bytes.
			case out.Data != nil:
				if b, err := json.Marshal(out.Data); err == nil {
					out.Raw = b
				}
			}
			out.Data = nil
			if ct == "" && out.ContentType == "" {
				out.ContentType = "application/yaml"
			}
		} else {
			// Force legacy JSON wrap.
			if out.Data == nil && out.Raw != nil {
				out.Data = map[string]any{"value": string(out.Raw)}
			}
			if out.Data != nil {
				if b, err := json.Marshal(out.Data); err == nil {
					out.Raw = b
				}
			}
			out.Data = nil
			if ct == "" {
				out.ContentType = "application/json"
			}
		}
	}

	if ct != "" {
		out.ContentType = ct
	}

	return out
}

// singleValueString reports whether m has exactly one key "value"
// holding a string, returning that string. Mirrors the upstream
// `{"value": "<str>"}` wrapper convention used by GCP/AWS/Vault/
// HTTP/Consul/etcd providers for non-JSON payloads.
func singleValueString(m map[string]any) (string, bool) {
	if len(m) != 1 {
		return "", false
	}
	v, ok := m["value"]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// singleValueStringOK is the boolean-only variant used in switch
// statements where we want to dispatch on detection without
// shadowing the captured string.
func singleValueStringOK(m map[string]any) bool {
	_, ok := singleValueString(m)
	return ok
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
