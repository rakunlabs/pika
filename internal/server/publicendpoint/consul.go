package publicendpoint

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/rakunlabs/pika/internal/service"
)

// consulHandler implements the read-only Consul KV compatibility
// shim. The wire shape matches the previous "/consul/v1/kv/*"
// implementation that lived on the extracted public port:
//
//	GET {basePath}/v1/kv/{key} [?raw] [?variant=] [?version=] [?format=]
//
// 404 returns an empty body (a number of Consul clients depend on
// that exact shape); errors that aren't "not found" map to 502 with
// a JSON body to distinguish them from upstream Consul outages.
//
// Notably absent: PUT, DELETE, /v1/txn, ?wait=, ?index=. The shim is
// read-only by design. Long-poll consumers fall back to immediate
// responses without explicit hostility — ?wait/?index are accepted
// but ignored, matching the documented behaviour.
type consulHandler struct {
	svc      Service
	basePath string
}

func newConsulHandler(ep service.PublicEndpoint, svc Service) http.Handler {
	bp := normalizeBasePath(ep.BasePath)
	h := &consulHandler{svc: svc, basePath: bp}
	mux := http.NewServeMux()
	// Two prefixes so a client can either include or omit the base
	// path. Operators commonly set base_path="/consul" and have
	// clients call "/v1/kv/...", but consul-template under a Pika
	// proxy might preserve the "/consul" prefix. Accept both.
	mux.HandleFunc(bp+"/v1/kv/", h.serve)
	if bp != "" {
		mux.HandleFunc("/v1/kv/", h.serve)
	}
	return mux
}

func (h *consulHandler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed: consul shim is read-only")
		return
	}

	key := extractConsulKey(r.URL.Path)
	if key == "" {
		writeJSONError(w, http.StatusBadRequest, "missing key")
		return
	}

	q := r.URL.Query()
	variant := q.Get("variant")
	version := q.Get("version")
	rawMode := q.Has("raw")
	requestedFormat := q.Get("format")

	result, err := h.svc.GetData(r.Context(), key, version, variant)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			// Consul KV 404 contract: empty body, no JSON.
			w.WriteHeader(http.StatusNotFound)
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
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if result.Error != "" {
		writeJSONError(w, http.StatusBadRequest, result.Error)
		return
	}

	body := result.Data
	format := result.Format

	// Optional format conversion.
	if requestedFormat != "" && requestedFormat != format {
		converted, convErr := service.ConvertFormat(body, format, requestedFormat)
		if convErr != nil {
			writeJSONError(w, http.StatusBadRequest, "format conversion: "+convErr.Error())
			return
		}
		body = converted
		format = requestedFormat
	}

	if rawMode {
		w.Header().Set("Content-Type", contentTypeForFormat(format))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}

	// Default: Consul envelope.
	resp := []consulKVEntry{{
		LockIndex:   0,
		Key:         key,
		Flags:       0,
		Value:       base64.StdEncoding.EncodeToString(body),
		CreateIndex: 0,
		ModifyIndex: 0,
		Session:     "",
	}}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// consulKVEntry is the single-entry shape Consul returns for a leaf
// GET. We expose only the fields a typical client reads — extra
// fields exist in real Consul (e.g. Partition, Namespace) but clients
// tolerate their absence on a successful 200.
type consulKVEntry struct {
	CreateIndex uint64 `json:"CreateIndex"`
	ModifyIndex uint64 `json:"ModifyIndex"`
	LockIndex   uint64 `json:"LockIndex"`
	Key         string `json:"Key"`
	Flags       uint64 `json:"Flags"`
	Value       string `json:"Value"`
	Session     string `json:"Session"`
}

// extractConsulKey pulls the {key} segment out of the request path.
// Accepts both "/v1/kv/<key>" and "/<basePath>/v1/kv/<key>" — the
// mux already filtered down to one of these, so we just slice off
// everything up to and including "/v1/kv/".
func extractConsulKey(path string) string {
	idx := strings.Index(path, "/v1/kv/")
	if idx < 0 {
		return ""
	}
	key := path[idx+len("/v1/kv/"):]
	// Strip trailing slash so "foo/" and "foo" point at the same
	// stored key. Consul itself distinguishes folders from leaves
	// using a trailing slash, but pika's data path uses a single
	// flat key so we normalise on read.
	return strings.TrimSuffix(key, "/")
}

// normalizeBasePath returns the base path with any trailing slash
// removed, and "" coerced to "" (no prefix). The mux uses this value
// raw, so "/consul" + "/v1/kv/" becomes the well-known
// "/consul/v1/kv/" prefix.
func normalizeBasePath(bp string) string {
	if bp == "" || bp == "/" {
		return ""
	}
	return strings.TrimRight(bp, "/")
}
