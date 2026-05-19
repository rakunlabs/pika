package upstream

import (
	"context"
	"errors"
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
//                 challenge here
//   - Helm    → "/index.yaml"
//
// A non-2xx upstream response is NOT considered a failure here —
// we surface the status code and let the operator decide. The
// only outcomes that flip OK=false are: request didn't go out
// (network / DNS / TLS / auth) or status >= 500.
func Probe(ctx context.Context, c *Client, path string) registry.UpstreamHealth {
	out := registry.UpstreamHealth{}
	if c == nil {
		out.Error = "no upstream client configured"
		return out
	}
	out.URL = c.resolveURL(path)
	start := time.Now()
	resp, err := c.Get(ctx, path)
	out.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		out.Error = err.Error()
		// Distinguish transient (server side) vs. client side
		// (probably auth / wrong URL). Either way OK=false; the
		// status code is what tells them apart in the response.
		if errors.Is(err, ErrNotFound) {
			out.StatusCode = 404
			out.OK = true // upstream is reachable, just doesn't have this path
		}
		return out
	}
	defer resp.Body.Close()
	out.StatusCode = resp.StatusCode
	// Read up to 256 bytes for the preview.
	const previewMax = 256
	buf := make([]byte, 0, previewMax)
	chunk := make([]byte, previewMax)
	for len(buf) < previewMax {
		n, err := resp.Body.Read(chunk[:previewMax-len(buf)])
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if err != nil {
			break
		}
	}
	out.BodyPreview = sanitisePreview(string(buf))
	// 2xx & 3xx = upstream reachable, mark OK.
	out.OK = resp.StatusCode >= 200 && resp.StatusCode < 500
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
