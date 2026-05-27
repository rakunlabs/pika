package publicendpoint

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/rakunlabs/pika/internal/service"
)

// customHandler implements the user-authored Go-template response
// modifier. The shim resolves the path tail to a config key, fetches
// the data through service.GetData (so variants / versions / format
// conversion all behave the same as the admin API), and renders the
// configured BodyTemplate with a curated input map.
//
// The template engine is the stdlib text/template — NOT mugo — with
// a minimal FuncMap that exposes the helpers most operators need
// (base64, json, time) and nothing else. Filesystem, env, and
// command-exec helpers are deliberately absent: a public-facing
// endpoint should never grow a sandbox-escape surface by accident.
type customHandler struct {
	svc      Service
	basePath string
	cfg      *service.CustomCompat
	tpl      *template.Template
}

func newCustomHandler(ep service.PublicEndpoint, svc Service) (http.Handler, error) {
	if ep.Custom == nil {
		return nil, errors.New("custom shim: missing CustomCompat block")
	}
	tpl, err := template.New("body").Funcs(customFuncMap()).Parse(ep.Custom.BodyTemplate)
	if err != nil {
		return nil, fmt.Errorf("custom shim: parse template: %w", err)
	}
	bp := normalizeBasePath(ep.BasePath)
	h := &customHandler{
		svc:      svc,
		basePath: bp,
		cfg:      ep.Custom,
		tpl:      tpl,
	}
	mux := http.NewServeMux()
	// All requests under the base path land on the shim. When the
	// base path is empty (the operator picked "/" as the prefix)
	// register a single catch-all instead.
	if bp == "" {
		mux.HandleFunc("/", h.serve)
	} else {
		mux.HandleFunc(bp+"/", h.serve)
		// Also accept the bare prefix (no trailing slash) so curl
		// /consul-like-tools don't 404 on the root.
		mux.HandleFunc(bp, h.serve)
	}
	return mux, nil
}

func (h *customHandler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed: custom shim is read-only")
		return
	}

	key := h.resolveKey(r.URL.Path)
	q := r.URL.Query()
	variant := q.Get("variant")
	version := q.Get("version")
	rawMode := q.Has("raw")
	requestedFormat := q.Get("format")
	if !h.cfg.AllowFormatOverride {
		requestedFormat = ""
	}

	missingStatus := h.cfg.StatusOnMissing
	if missingStatus == 0 {
		missingStatus = http.StatusNotFound
	}

	// Resolve. Treat "not found" + "nil result" identically.
	var (
		body           []byte
		resolvedFormat string
		found          bool
	)
	if key != "" {
		result, err := h.svc.GetData(r.Context(), key, version, variant)
		switch {
		case err == nil && result != nil && result.Error == "":
			body = result.Data
			resolvedFormat = result.Format
			found = true
		case err == nil && result != nil && result.Error != "":
			writeJSONError(w, http.StatusBadRequest, result.Error)
			return
		case errors.Is(err, service.ErrNotFound):
			// fall through, found=false.
		case errors.Is(err, service.ErrBadRequest):
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		case err != nil:
			writeJSONError(w, http.StatusBadGateway, "upstream resolve failed: "+err.Error())
			return
		}
	}

	if found && requestedFormat != "" && requestedFormat != resolvedFormat {
		converted, convErr := service.ConvertFormat(body, resolvedFormat, requestedFormat)
		if convErr != nil {
			writeJSONError(w, http.StatusBadRequest, "format conversion: "+convErr.Error())
			return
		}
		body = converted
		resolvedFormat = requestedFormat
	}

	// Template context — kept small and stable so operators can
	// reason about the surface without reading the source.
	data := map[string]any{
		"Key":            key,
		"Variant":        variant,
		"Version":        version,
		"Raw":            rawMode,
		"Format":         requestedFormat,
		"Data":           body,
		"DataString":     string(body),
		"DataB64":        base64.StdEncoding.EncodeToString(body),
		"Found":          found,
		"ResolvedFormat": resolvedFormat,
		"Now":            time.Now().UTC(),
	}

	var rendered bytes.Buffer
	if err := h.tpl.Execute(&rendered, data); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "template execute: "+err.Error())
		return
	}

	ct := h.cfg.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)

	if !found {
		w.WriteHeader(missingStatus)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_, _ = w.Write(rendered.Bytes())
}

// resolveKey strips the base-path prefix and any leading "/" so the
// template's {{ .Key }} matches the keys operators see in the
// configurations tree.
func (h *customHandler) resolveKey(path string) string {
	p := path
	if h.basePath != "" {
		p = strings.TrimPrefix(p, h.basePath)
	}
	return strings.TrimPrefix(strings.TrimSuffix(p, "/"), "/")
}

// customFuncMap is the curated FuncMap exposed to user templates.
// Intentionally minimal: helpers that read the filesystem, process
// environment, or shell out are NOT included.
func customFuncMap() template.FuncMap {
	return template.FuncMap{
		"b64":    func(in any) string { return base64.StdEncoding.EncodeToString(toBytes(in)) },
		"b64dec": b64dec,
		"upper":  strings.ToUpper,
		"lower":  strings.ToLower,
		"trim":   strings.TrimSpace,
		"join":   strings.Join,
		"split":  strings.Split,
		// Common formatters operators reach for.
		"rfc3339": func(t time.Time) string { return t.Format(time.RFC3339) },
		"unix":    func(t time.Time) int64 { return t.Unix() },
	}
}

func toBytes(in any) []byte {
	switch v := in.(type) {
	case []byte:
		return v
	case string:
		return []byte(v)
	default:
		return []byte(fmt.Sprint(in))
	}
}

func b64dec(in string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(in)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
