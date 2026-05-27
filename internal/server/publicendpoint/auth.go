package publicendpoint

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/rakunlabs/pika/internal/service"
)

// authMiddleware wraps next with the endpoint's configured auth mode.
// Failed auth gets a uniform JSON 401 — the body shape mirrors what
// the rest of the pika API emits so a client that already understands
// pika's error format keeps working.
func authMiddleware(ep service.PublicEndpoint, svc Service, next http.Handler) http.Handler {
	switch ep.Auth.Mode {
	case "", "none":
		return next
	case "bearer_token":
		return bearerTokenMiddleware(ep, svc, next)
	case "static_token":
		return staticTokenMiddleware(ep, next)
	default:
		// Validation should have caught this. Refuse all traffic
		// rather than silently letting it through.
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSONError(w, http.StatusInternalServerError,
				"public endpoint auth mode misconfigured")
		})
	}
}

// bearerTokenMiddleware looks for an "Authorization: Bearer <token>"
// header, validates it against pika's token store with the
// files.read capability and the resolved key as scope, and forwards
// when authorized.
func bearerTokenMiddleware(ep service.PublicEndpoint, svc Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := r.Header.Get("Authorization")
		token := ""
		if len(hdr) > 7 && strings.EqualFold(hdr[:7], "Bearer ") {
			token = strings.TrimSpace(hdr[7:])
		}
		if token == "" {
			writeJSONError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		// Scope path is the key resolved from the URL; we pass "*"
		// here because the shim itself enforces what counts as a
		// valid key, and ValidateToken is happy to accept "*" with
		// any token scoped to "**" or matching prefix. Operation is
		// always "read" — these endpoints are read-only by design.
		// The shim handler still resolves the actual key path and
		// can be extended to re-call ValidateToken with the precise
		// key if scoping needs to tighten.
		if err := svc.ValidateToken(r.Context(), token, resolveKeyForAuth(r), "read"); err != nil {
			writeJSONError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// staticTokenMiddleware compares the request header in constant time
// against the endpoint's allowed-token list. The header defaults to
// "Authorization" with optional "Bearer " prefix; operators can set
// ep.Auth.HeaderName to "X-Consul-Token" for consul-template
// compatibility.
func staticTokenMiddleware(ep service.PublicEndpoint, next http.Handler) http.Handler {
	headerName := ep.Auth.HeaderName
	if headerName == "" {
		headerName = "Authorization"
	}
	// Pre-compute byte slices so we can use ConstantTimeCompare per
	// request without churning new []byte each time.
	allowed := make([][]byte, 0, len(ep.Auth.StaticTokens))
	for _, t := range ep.Auth.StaticTokens {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		allowed = append(allowed, []byte(t))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get(headerName)
		if raw == "" {
			writeJSONError(w, http.StatusUnauthorized, "missing token")
			return
		}
		// Strip "Bearer " prefix if present so operators can point
		// either header name at the same client.
		if len(raw) > 7 && strings.EqualFold(raw[:7], "Bearer ") {
			raw = strings.TrimSpace(raw[7:])
		}
		got := []byte(raw)
		for _, want := range allowed {
			// Length differences shortcut subtle.ConstantTimeCompare
			// to a 0-return, which is still a constant-time check
			// against any single allowed entry. The loop itself
			// leaks "first match position" but allowed lists are
			// expected to be tiny (1..3 tokens) and the timing of
			// the linear scan reveals only "token is valid" vs
			// "token is invalid", which is the same information the
			// HTTP status already encodes.
			if subtle.ConstantTimeCompare(got, want) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		}
		writeJSONError(w, http.StatusUnauthorized, "invalid token")
	})
}

// resolveKeyForAuth pulls the config-key segment out of the request
// path. The bearer-token middleware uses it as the ValidateToken
// scope so operators can scope an API token to a sub-tree and still
// have it pass at this layer. The shim itself does the same parse
// (with mode-specific rules), so the worst case is a token getting
// rejected here that the shim would have rejected anyway.
//
// We strip the standard Consul prefix "/v1/kv/" when we see it, then
// fall back to the raw URL path. The trailing slash trim normalises
// folder-style keys.
func resolveKeyForAuth(r *http.Request) string {
	p := r.URL.Path
	// Strip the most common consul-template-shaped prefixes so the
	// token scope matches what's written in the pika tokens UI.
	for _, prefix := range []string{"/v1/kv/", "/consul/v1/kv/"} {
		if strings.HasPrefix(p, prefix) {
			return strings.TrimPrefix(p, prefix)
		}
	}
	return strings.TrimPrefix(p, "/")
}

// writeJSONError emits the canonical {message} body the rest of the
// pika API uses, keeping client error parsers unified across the
// admin port and the public ports.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Static-format to avoid an fmt allocation in the hot path.
	_, _ = w.Write([]byte(`{"message":"`))
	_, _ = w.Write([]byte(jsonEscape(message)))
	_, _ = w.Write([]byte(`"}`))
}

// jsonEscape does the minimum JSON-string escaping required for the
// fixed-shape error messages this package emits. We never embed
// user-controlled data here, so a full encoding/json round-trip
// would be overkill — but we still cover the canonical hazards so a
// careless caller can't break the wire format.
func jsonEscape(s string) string {
	if !strings.ContainsAny(s, `"\`) && !strings.ContainsRune(s, '\n') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
