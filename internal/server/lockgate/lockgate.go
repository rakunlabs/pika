// Package lockgate provides an HTTP middleware that returns 503 for
// API requests when the server's at-rest encryption key has been
// initialized but not yet unlocked.
//
// Scope: what the gate touches and what it leaves alone
//
// The middleware deliberately ONLY intercepts request paths that
// could reach encrypted at-rest data. In practice that's three
// prefixes:
//
//   - /api/v1/   — admin + data API
//   - /data/     — config resolution endpoint
//   - /raw/      — raw filesystem endpoint
//
// Everything else (the SPA's index.html, JS/CSS/font assets, the
// folder/file delivery handler that backs the static UI, and the
// /login/* + /logout auth flow that lives on its own mux) passes
// through unconditionally. A user pointing their browser at a
// locked pika now sees the normal SPA shell, signs in like usual,
// and only THEN gets the unlock takeover from App.svelte. The
// earlier design served raw JSON for the index request when
// locked, which was unusable from a browser.
//
// # Fresh-install behaviour
//
// Until an administrator has initialized the server key (no
// verifier exists on disk yet), the gate is a no-op even on the
// API prefixes. This preserves the legacy "encryption is opt-in"
// UX — operators can set up users, mounts, and basic config first,
// then turn on at-rest encryption when they're ready. Once a
// verifier is written, every restart enters the locked state and
// the gate engages on the three API prefixes.
//
// What is allowed when locked (initialized but no live key)
//
// API paths that the SPA needs to reach the unlock screen — info,
// me, key status, key unlock — are explicitly allowlisted so a
// logged-in admin can reach the unlock endpoint without hitting a
// 503. Anything else under the gated prefixes returns 503 with
// header X-Pika-Locked: true so the SPA can switch to the unlock
// screen.
//
// # Header signaling
//
// Every locked-response carries `X-Pika-Locked: true`. The SPA's
// axios interceptor watches for this and triggers a status refetch
// + UnlockScreen render, even if the request was kicked off in the
// background (e.g. settings polling). Without the header the SPA
// would have to special-case the JSON body.
package lockgate

import (
	"net/http"
	"strings"

	"github.com/rakunlabs/pika/internal/secret/keymgr"
)

// Middleware returns an HTTP middleware that gates API requests on
// the manager being unlocked. Non-API paths (SPA assets, login
// flow, static handlers) pass through unconditionally so a user
// can always reach the login screen and the unlock takeover that
// follows.
//
// `basePath` is prepended to every path rule so deployments behind
// a sub-path mount (e.g. PIKA_SERVER_BASE_PATH=/admin) still get
// the right matches without per-deployment config.
//
// Matching rules:
//   - "gated prefixes": only requests whose path starts with one of
//     these get any further evaluation. Anything else is a no-op
//     pass-through.
//   - "allow exact" / "allow prefix": even within a gated prefix,
//     these explicit entries (auth bootstrap, key status, unlock)
//     still pass through.
//
// We use literal prefix rules instead of net/http.ServeMux patterns
// because the work happens before routing — there's no Mux state to
// consult — and the rule set is tiny (~10 entries).
func Middleware(mgr *keymgr.Manager, basePath string) func(http.Handler) http.Handler {
	rules := buildRules(basePath)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Fast path #1: no manager wired (test harness, future
			// deployments that disable encryption) → never gate.
			if mgr == nil {
				next.ServeHTTP(w, r)
				return
			}
			// Fast path #2: manager exists but the verifier has not
			// been written yet. This is the fresh-install state:
			// the operator hasn't opted into encryption, so the
			// system should serve every request as plain.
			if !mgr.Initialized() {
				next.ServeHTTP(w, r)
				return
			}
			// Initialized AND unlocked: normal operation.
			if mgr.IsUnlocked() {
				next.ServeHTTP(w, r)
				return
			}
			// Locked. We only 503 requests targeting the API
			// surfaces that could touch encrypted state. The SPA
			// shell itself, the login flow, and static assets must
			// keep working so the user can navigate to login →
			// session → unlock.
			if !rules.shouldGate(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			// Inside a gated prefix: honor the allowlist for the
			// bootstrap endpoints that the SPA needs to drive the
			// unlock flow.
			if rules.allow(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			writeLocked(w)
		})
	}
}

// pathRules is a tiny path matcher tuned for the boot-time gate:
// constructed once, read many times, never mutated.
type pathRules struct {
	// gatedPrefixes is the set of path prefixes that, when locked,
	// MAY be 503'd. A request whose path doesn't start with any of
	// these is irrelevant to the gate.
	gatedPrefixes []string

	// exempt holds the path matchers (exact + prefix) that pass
	// through even within a gated prefix. These are the bootstrap
	// endpoints the SPA needs to drive the unlock flow itself.
	exemptExact  map[string]struct{}
	exemptPrefix []string
}

// shouldGate reports whether path lives under a gated API surface
// at all. False for SPA assets, login routes, and anything else —
// those never get the 503 treatment.
func (r *pathRules) shouldGate(path string) bool {
	for _, p := range r.gatedPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// allow reports whether path is on the within-gate allowlist.
// Only meaningful when shouldGate(path) is true.
func (r *pathRules) allow(path string) bool {
	if _, ok := r.exemptExact[path]; ok {
		return true
	}
	for _, p := range r.exemptPrefix {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func buildRules(basePath string) *pathRules {
	// Normalize basePath so callers can pass "" or "/foo" or "/foo/"
	// equivalently. Internally we want "/foo" with no trailing slash
	// for the join below.
	bp := strings.TrimRight(basePath, "/")

	// Gated prefixes: every path under one of these is potentially
	// 503'd while locked. Adding a new top-level API namespace
	// requires adding it here AND updating the SPA to handle 503
	// gracefully on the new endpoints.
	gated := []string{
		"/api/v1/", // admin + data API surface
		"/data/",   // resolved-config consumer endpoint
		"/raw/",    // raw filesystem endpoint
	}

	// Allowlisted exact paths — pass through even when gated.
	//
	// Notably absent: /api/v1/key/initialize. Initialize is only
	// callable when no verifier exists yet, which means the gate is
	// already a no-op in that state — adding initialize to the
	// allowlist would let a logged-in admin re-initialize after a
	// restart (when the gate IS engaged), which is exactly the path
	// we want closed.
	exactPaths := []string{
		"/api/v1/info",       // SPA boot + auth-state discovery
		"/api/v1/key/status", // key-state probe (called on every boot)
		"/api/v1/key/unlock", // every-restart unlock (still needed while locked)
	}

	// Allowlisted prefix paths — entire subtree pass-through.
	// /api/v1/me/* lets the user introspect their session while
	// locked (the SPA fetches /me on boot to decide between login
	// and the unlock takeover).
	prefixPaths := []string{
		"/api/v1/me", // self-introspection: /me/preferences, /me/passkeys, etc.
	}

	r := &pathRules{
		gatedPrefixes: make([]string, 0, len(gated)),
		exemptExact:   make(map[string]struct{}, len(exactPaths)),
		exemptPrefix:  make([]string, 0, len(prefixPaths)),
	}
	for _, p := range gated {
		r.gatedPrefixes = append(r.gatedPrefixes, bp+p)
	}
	for _, p := range exactPaths {
		r.exemptExact[bp+p] = struct{}{}
	}
	for _, p := range prefixPaths {
		r.exemptPrefix = append(r.exemptPrefix, bp+p)
	}
	return r
}

// writeLocked emits the canonical "server is locked" response. The
// shape mirrors the api package's response struct so SPA error
// handling has one path to deal with.
func writeLocked(w http.ResponseWriter) {
	w.Header().Set("X-Pika-Locked", "true")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	// Static body — no fmt overhead, no allocation per response.
	// Field names match api.response{Message} so existing client
	// error renderers don't need a new branch.
	_, _ = w.Write([]byte(`{"code":"server_locked","message":"Server is locked. An administrator must unlock it."}`))
}
