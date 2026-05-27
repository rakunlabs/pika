package upstream

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/pika/internal/registry"
)

// Probe runs a connectivity check against the client's configured
// upstream by issuing a GET against `path`. The result captures
// status code, end-to-end latency, and a small body preview for
// diagnostic display. Errors that prevent the request from being
// issued at all (auth resolver failure, bad URL) end up in the
// Error field with OK=false.
//
// The caller (a per-protocol Remote) picks the path that's most
// likely to be a cheap health-probe surface for its upstream:
//
//   - Go      → "" (root) or "/" — proxy.golang.org returns 200
//   - NPM     → "/-/ping" — npmjs returns 200, others may 404
//   - Docker  → "/v2/" — every Docker registry advertises the
//     challenge here
//   - Helm    → "/index.yaml"
//
// A non-2xx upstream response is NOT considered a request failure here —
// we surface the status code and let the operator decide. The only
// outcomes that flip OK=false are: request didn't go out (network / DNS /
// TLS / auth) or status >= 500.
func Probe(ctx context.Context, c *Client, path string) (out registry.UpstreamHealth) {
	if c == nil {
		out.Error = "no upstream client configured"
		return out
	}
	out.URL = c.resolveURL(path)
	start := time.Now()
	defer func() { out.LatencyMS = time.Since(start).Milliseconds() }()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, out.URL, nil)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if err := c.applyAuth(ctx, req); err != nil {
		out.Error = "auth: " + err.Error()
		return out
	}

	// Probe intentionally bypasses Client.Get: normal registry reads turn
	// 401/403/5xx responses into typed errors and close the body, while the
	// admin probe needs the raw status code and a small diagnostic preview.
	resp, err := c.httpc.Do(req)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer resp.Body.Close()
	out.StatusCode = resp.StatusCode

	const previewMax = 256
	buf, _ := io.ReadAll(io.LimitReader(resp.Body, previewMax))
	out.BodyPreview = sanitisePreview(string(buf))
	out.OK = resp.StatusCode >= 200 && resp.StatusCode < 500
	if !out.OK {
		out.Error = http.StatusText(resp.StatusCode)
		if out.Error == "" {
			out.Error = "upstream returned status"
		}
	}
	return out
}

// sanitisePreview trims non-printable noise and collapses
// excessive whitespace so the preview renders cleanly in the
// admin UI.
func sanitisePreview(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 256 {
		s = s[:256]
	}
	// Drop control characters except newline / tab.
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' || (r >= 0x20 && r < 0x7f) || r > 0x7f {
			b.WriteRune(r)
		}
	}
	return b.String()
}
