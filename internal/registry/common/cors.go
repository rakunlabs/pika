package common

import (
	"net/http"
	"strings"
)

// CORS helpers for registry endpoints.
//
// The default pika CORS middleware (mcors.Middleware in
// internal/server/server.go) covers the admin API and the SPA. The
// registry endpoints are public-facing client tools (npm, docker,
// go) that don't make CORS requests in practice — except for the
// browser-based "open this packument" link from the UI, and for
// the few JS-in-browser SDKs that exist.
//
// We expose a per-repo CORS origin allowlist so an operator can
// permit specific browser apps without opening the door for
// everyone. Empty list = no CORS headers emitted (the default).

// ApplyCORS writes Access-Control-Allow-* headers when the request's
// Origin is in the allowlist. "*" in the allowlist means "any
// origin". Returns true when a preflight (OPTIONS) was handled so
// the caller short-circuits the dispatch.
func ApplyCORS(w http.ResponseWriter, r *http.Request, allowlist []string) (handledPreflight bool) {
	if len(allowlist) == 0 {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	allowed := false
	for _, a := range allowlist {
		if a == "*" || a == origin {
			allowed = true
			break
		}
	}
	if !allowed {
		return false
	}

	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, PUT, POST, DELETE, PATCH, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-None-Match, X-Pika-Token, "+r.Header.Get("Access-Control-Request-Headers"))
	w.Header().Set("Access-Control-Expose-Headers", "ETag, Docker-Content-Digest, Content-Range")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.Header().Set("Vary", appendVary(w.Header().Get("Vary"), "Origin"))

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

// appendVary adds a value to an existing Vary header, de-duplicated.
func appendVary(existing, value string) string {
	if existing == "" {
		return value
	}
	for _, part := range strings.Split(existing, ",") {
		if strings.TrimSpace(part) == value {
			return existing
		}
	}
	return existing + ", " + value
}
