package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// templateTransformConfig drives the template-transform middleware.
// Request and response sections are independent; either or both may
// be left empty to skip the transform on that side.
type templateTransformConfig struct {
	Request  templateTransformSide `json:"request,omitempty"`
	Response templateTransformSide `json:"response,omitempty"`
	// ParsePayload decodes the body as JSON/YAML before exposing it
	// to the template under `.payload`. When false (default) the
	// raw body is available as a string at `.body`.
	ParsePayload bool `json:"parse_payload,omitempty"`
	// MaxBodyBytes caps the buffered body length on either side.
	// Default 1 MiB; bodies bigger than this short-circuit with
	// 413 (request) / are passed through unmodified (response).
	MaxBodyBytes int64 `json:"max_body_bytes,omitempty"`
}

type templateTransformSide struct {
	// Body, when non-empty, replaces the body with the render
	// result.
	Body string `json:"body,omitempty"`
	// Headers map header name to a templated value. Empty value
	// deletes the header; otherwise the rendered string is Set().
	Headers map[string]string `json:"headers,omitempty"`
	// Status (response-side only) sets the HTTP status code, also
	// templated, parsed back to int.
	Status string `json:"status,omitempty"`
}

// buildTemplateTransformMW returns the middleware constructor for
// the template-transform node kind.
func buildTemplateTransformMW(cfg json.RawMessage, _ ServiceDeps) (Middleware, error) {
	var c templateTransformConfig
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &c); err != nil {
			return nil, fmt.Errorf("template-transform config: %w", err)
		}
	}
	maxBody := c.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = 1 << 20 // 1 MiB
	}

	// Pre-compute "do we need to touch the request at all" so the
	// hot path is a single bool check.
	transformReq := c.Request.Body != "" || len(c.Request.Headers) > 0
	transformResp := c.Response.Body != "" || len(c.Response.Headers) > 0 || c.Response.Status != ""

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tmplCtx := map[string]any{
				"req": map[string]any{
					"method":  r.Method,
					"path":    r.URL.Path,
					"host":    r.Host,
					"query":   flatQuery(r.URL.Query()),
					"headers": flatHeaders(r.Header),
				},
			}

			if transformReq || c.ParsePayload || c.Request.Body != "" {
				if r.Body != nil {
					body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
					_ = r.Body.Close()
					if err != nil {
						http.Error(w, "read request body: "+err.Error(), http.StatusBadRequest)
						return
					}
					if int64(len(body)) > maxBody {
						http.Error(w, "request body exceeds max_body_bytes", http.StatusRequestEntityTooLarge)
						return
					}
					tmplCtx["body"] = string(body)
					if c.ParsePayload {
						tmplCtx["payload"] = decodePayload(body)
					}
					// Render new body if configured.
					if c.Request.Body != "" {
						rendered, err := renderTemplate(c.Request.Body, tmplCtx)
						if err != nil {
							http.Error(w, "render request body: "+err.Error(), http.StatusInternalServerError)
							return
						}
						body = []byte(rendered)
					}
					r.Body = io.NopCloser(bytes.NewReader(body))
					r.ContentLength = int64(len(body))
					r.Header.Set("Content-Length", strconv.Itoa(len(body)))
				}
			}

			// Render configured request headers.
			for k, v := range c.Request.Headers {
				rv, err := renderTemplate(v, tmplCtx)
				if err != nil {
					http.Error(w, "render request header "+k+": "+err.Error(), http.StatusInternalServerError)
					return
				}
				if rv == "" {
					r.Header.Del(k)
				} else {
					r.Header.Set(k, rv)
				}
			}

			if !transformResp {
				next.ServeHTTP(w, r)
				return
			}

			// Buffer the response so we can rewrite headers + body
			// after `next` returns. We're paying the buffer cost on
			// every response — operators who care about streaming
			// should leave the response side empty.
			rec := newResponseRecorder(maxBody)
			next.ServeHTTP(rec, r)

			respCtx := map[string]any{
				"req":     tmplCtx["req"],
				"status":  rec.status,
				"body":    string(rec.body.Bytes()),
				"headers": flatHeaders(rec.header),
			}
			if c.ParsePayload {
				respCtx["payload"] = decodePayload(rec.body.Bytes())
			}

			outBody := rec.body.Bytes()
			if c.Response.Body != "" {
				rendered, err := renderTemplate(c.Response.Body, respCtx)
				if err != nil {
					http.Error(w, "render response body: "+err.Error(), http.StatusInternalServerError)
					return
				}
				outBody = []byte(rendered)
			}
			// Headers second (so the body render sees the upstream
			// header set, not the overridden one).
			for k, v := range c.Response.Headers {
				rv, err := renderTemplate(v, respCtx)
				if err != nil {
					http.Error(w, "render response header "+k+": "+err.Error(), http.StatusInternalServerError)
					return
				}
				if rv == "" {
					rec.header.Del(k)
				} else {
					rec.header.Set(k, rv)
				}
			}
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			if c.Response.Status != "" {
				rs, err := renderTemplate(c.Response.Status, respCtx)
				if err != nil {
					http.Error(w, "render response status: "+err.Error(), http.StatusInternalServerError)
					return
				}
				rs = strings.TrimSpace(rs)
				if rs != "" {
					if parsed, err := strconv.Atoi(rs); err == nil {
						status = parsed
					}
				}
			}

			// Copy rec headers into the real ResponseWriter, then
			// write status, then body. Content-Length must reflect
			// the rendered body length.
			for k, vs := range rec.header {
				w.Header()[k] = vs
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(outBody)))
			w.WriteHeader(status)
			_, _ = w.Write(outBody)
		})
	}, nil
}

// responseRecorder buffers a handler's response so the wrapping
// middleware can rewrite it. Bounded at maxBytes; beyond that the
// extra bytes are discarded (Write returns the full length so the
// upstream handler doesn't error out).
type responseRecorder struct {
	header   http.Header
	body     *bytes.Buffer
	status   int
	maxBytes int64
	written  int64
}

func newResponseRecorder(maxBytes int64) *responseRecorder {
	return &responseRecorder{
		header:   make(http.Header),
		body:     &bytes.Buffer{},
		maxBytes: maxBytes,
	}
}

func (r *responseRecorder) Header() http.Header { return r.header }
func (r *responseRecorder) WriteHeader(code int) { r.status = code }
func (r *responseRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	remaining := r.maxBytes - r.written
	if remaining <= 0 {
		return len(p), nil
	}
	n := int64(len(p))
	if n > remaining {
		r.body.Write(p[:remaining])
		r.written = r.maxBytes
		return len(p), nil
	}
	r.body.Write(p)
	r.written += n
	return len(p), nil
}
