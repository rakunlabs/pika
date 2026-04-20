package authx

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rakunlabs/ada/middleware/ratelimit"

	"github.com/rakunlabs/pika/internal/service"
)

// loginGuardStoreCapacity caps the in-memory bucket count per axis. Each
// bucket is small (~100 bytes for typical hold times) so 10k entries × 2
// stores is well under 5 MB even pathologically. LRU-evicted on overflow.
const loginGuardStoreCapacity = 10_000

// LoginGuard returns an http middleware that rate-limits POST /login/pass/*
// and POST /login/register/* per client-IP and per-username. Other paths
// pass through unmodified — the middleware is safe to mount on a group that
// also serves /login/info, /login/me, /logout, etc.
//
// When cfg is nil or cfg.Enabled is false, returns a passthrough.
//
// Failure to construct the in-memory stores (which never happens in
// practice, but the constructor returns an error for completeness) yields
// a passthrough plus a one-shot Error log so the server still boots —
// losing rate limiting is preferable to refusing all logins.
func LoginGuard(cfg *service.AuthRateLimitSettings, trustedProxies []*net.IPNet) func(http.Handler) http.Handler {
	if cfg == nil || !cfg.Enabled {
		return passthrough
	}

	ipStore, err := ratelimit.NewMemoryStore(loginGuardStoreCapacity)
	if err != nil {
		slog.Error("authx: login guard disabled — failed to create IP store", "error", err.Error())
		return passthrough
	}
	userStore, err := ratelimit.NewMemoryStore(loginGuardStoreCapacity)
	if err != nil {
		slog.Error("authx: login guard disabled — failed to create user store", "error", err.Error())
		return passthrough
	}

	ipMW := ratelimit.Middleware(ratelimit.Config{
		Window:        cfg.Window,
		SoftThreshold: cfg.IPSoftThreshold,
		HardThreshold: cfg.IPHardThreshold,
		BackoffBase:   cfg.BackoffBase,
		BackoffMax:    cfg.BackoffMax,
		KeyFunc: func(r *http.Request) []string {
			ip := ClientIP(r, trustedProxies)
			if ip == "" {
				return nil
			}
			return []string{"ip:" + ip}
		},
		ShouldCount: shouldCountStatus,
		OnReject: func(r *http.Request, key string, reason ratelimit.RejectReason, retryAfter time.Duration) {
			slog.Warn("auth: login blocked",
				"axis", "ip",
				"key", key,
				"reason", string(reason),
				"retry_after_s", int(retryAfter.Seconds()),
				"path", r.URL.Path,
				"user_agent", r.UserAgent(),
			)
		},
		OnAttempt: func(r *http.Request, d ratelimit.Decision, status int) {
			slog.Warn("auth: login attempt failed",
				"axis", "ip",
				"key", d.Key,
				"count", d.Count,
				"status", status,
				"path", r.URL.Path,
				"user_agent", r.UserAgent(),
			)
		},
		Store: ipStore,
	})

	userMW := ratelimit.Middleware(ratelimit.Config{
		Window:        cfg.Window,
		SoftThreshold: cfg.UserSoftThreshold,
		HardThreshold: cfg.UserHardThreshold,
		BackoffBase:   cfg.BackoffBase,
		BackoffMax:    cfg.BackoffMax,
		KeyFunc:       userKeyFromBody,
		ShouldCount:   shouldCountStatus,
		OnReject: func(r *http.Request, key string, reason ratelimit.RejectReason, retryAfter time.Duration) {
			slog.Warn("auth: login blocked",
				"axis", "user",
				"key", key,
				"reason", string(reason),
				"retry_after_s", int(retryAfter.Seconds()),
				"path", r.URL.Path,
				"user_agent", r.UserAgent(),
			)
		},
		OnAttempt: func(r *http.Request, d ratelimit.Decision, status int) {
			slog.Warn("auth: login attempt failed",
				"axis", "user",
				"key", d.Key,
				"count", d.Count,
				"status", status,
				"path", r.URL.Path,
				"user_agent", r.UserAgent(),
			)
		},
		Store: userStore,
	})

	return func(next http.Handler) http.Handler {
		// Compose IP→user→handler. Both limiters see the captured status
		// because the inner status recorder propagates upward.
		guarded := ipMW(userMW(next))

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !shouldGuard(r) {
				next.ServeHTTP(w, r)
				return
			}
			guarded.ServeHTTP(w, r)
		})
	}
}

// passthrough is the no-op middleware returned when guarding is disabled.
func passthrough(next http.Handler) http.Handler { return next }

// shouldGuard reports whether the limiter should engage for this request.
// Engagement is restricted to POST on the password-bearing endpoints so
// polling endpoints (/login/info, /login/me, /logout) are never delayed.
func shouldGuard(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	path := r.URL.Path
	return strings.Contains(path, "/login/pass/") || strings.Contains(path, "/login/register/")
}

// shouldCountStatus is the predicate passed to ratelimit.Middleware for both
// axes. Counts only failed credential checks and malformed bodies — not
// successes (which would defeat the purpose) and not server errors (a 5xx
// is the server's problem, not the client's).
func shouldCountStatus(_ *http.Request, status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusBadRequest
}

// userBodyReadLimit caps how many bytes we slurp from a login body when
// extracting the username for rate-limiting. Matches ada's local strategy
// limit so we never read more than the strategy will accept.
const userBodyReadLimit = 1 << 16 // 64 KiB

// userKeyFromBody is the KeyFunc for the per-username limiter. It reads up
// to userBodyReadLimit bytes from r.Body, parses JSON or x-www-form-
// urlencoded, extracts the "username" field, normalizes it (trim + lower),
// and replaces r.Body so the wrapped handler can re-read the same bytes.
//
// Returns nil when:
//   - the content type isn't JSON or form-urlencoded,
//   - the body is unparseable,
//   - the username field is empty.
//
// In all "skip" cases the per-IP limiter still applies, so an attacker
// can't bypass rate limiting just by sending a malformed body.
func userKeyFromBody(r *http.Request) []string {
	if r.Body == nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, userBodyReadLimit))
	if err != nil {
		// Replace the body with what we managed to read so the
		// handler downstream produces a coherent 400.
		r.Body = io.NopCloser(bytes.NewReader(raw))
		return nil
	}
	// Always restore so the strategy can re-read.
	r.Body = io.NopCloser(bytes.NewReader(raw))

	contentType := r.Header.Get("Content-Type")
	username := ""

	switch {
	case strings.HasPrefix(contentType, "application/json"):
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil
		}
		if v, ok := m["username"].(string); ok {
			username = v
		}
	case strings.HasPrefix(contentType, "application/x-www-form-urlencoded"):
		vals, err := url.ParseQuery(string(raw))
		if err != nil {
			return nil
		}
		username = vals.Get("username")
	default:
		return nil
	}

	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return nil
	}
	return []string{"user:" + username}
}
