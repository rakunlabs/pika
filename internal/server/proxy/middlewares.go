package proxy

import (
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/ada/middleware/ratelimit"
	mcors "github.com/rakunlabs/ada/middleware/cors"
	mrequestid "github.com/rakunlabs/ada/middleware/requestid"

	"github.com/rakunlabs/pika/internal/service"
)

// rawMWBuilder is the legacy per-middleware builder signature used
// throughout this file: it does not take a BranchSet (no middleware
// is a composite). adaptMW wraps such a builder into the unified
// NodeBuilder shape that NodeSpec.Build expects, ignoring the
// branches arg.
type rawMWBuilder func(cfg json.RawMessage, svc ServiceDeps) (Middleware, error)

func adaptMW(b rawMWBuilder) NodeBuilder {
	return func(cfg json.RawMessage, svc ServiceDeps, _ BranchSet) (Middleware, error) {
		return b(cfg, svc)
	}
}

// DefaultMiddlewares returns the registered middleware kinds. The
// registry is rebuilt on every call so tests can mutate copies
// without cross-test pollution.
func DefaultMiddlewares() map[string]NodeSpec {
	specs := []NodeSpec{
		{
			Kind:        KindMiddleware,
			Subtype:     "logger",
			Label:       "Request log",
			Description: "Emit a slog Debug line per request (method, path, status, latency).",
			Build:       adaptMW(buildLoggerMW),
		},
		{
			Kind:        KindMiddleware,
			Subtype:     "requestid",
			Label:       "Request ID",
			Description: "Generate or pass through X-Request-Id on every request.",
			Build:       adaptMW(buildRequestIDMW),
		},
		{
			Kind:        KindMiddleware,
			Subtype:     "cors",
			Label:       "CORS",
			Description: "Cross-origin resource sharing. Defaults to allow-all when no origins listed.",
			Build:       adaptMW(buildCorsMW),
		},
		{
			Kind:        KindMiddleware,
			Subtype:     "auth-bearer",
			Label:       "Auth (bearer token)",
			Description: "Require a pika API token in the Authorization header. Token scope defaults to the request path.",
			Build:       adaptMW(buildAuthBearerMW),
		},
		{
			Kind:        KindMiddleware,
			Subtype:     "basic-auth",
			Label:       "Basic auth",
			Description: "Static user/password list checked against HTTP Basic credentials.",
			Build:       adaptMW(buildBasicAuthMW),
		},
		{
			Kind:        KindMiddleware,
			Subtype:     "ip-allowlist",
			Label:       "IP allowlist",
			Description: "Accept requests only from the given CIDRs. Empty list rejects everything.",
			Build:       adaptMW(buildIPAllowMW),
		},
		{
			Kind:        KindMiddleware,
			Subtype:     "ip-denylist",
			Label:       "IP denylist",
			Description: "Reject requests from the given CIDRs.",
			Build:       adaptMW(buildIPDenyMW),
		},
		{
			Kind:        KindMiddleware,
			Subtype:     "header-inject",
			Label:       "Inject headers",
			Description: "Add request and/or response headers.",
			Build:       adaptMW(buildHeaderInjectMW),
		},
		{
			Kind:        KindMiddleware,
			Subtype:     "header-remove",
			Label:       "Remove headers",
			Description: "Strip request and/or response headers.",
			Build:       adaptMW(buildHeaderRemoveMW),
		},
		{
			Kind:        KindMiddleware,
			Subtype:     "ratelimit",
			Label:       "Rate limit",
			Description: "Sliding-window limiter keyed by client IP, configurable thresholds and window.",
			Build:       adaptMW(buildRateLimitMW),
		},
		{
			Kind:        KindMiddleware,
			Subtype:     "compress",
			Label:       "Gzip compression",
			Description: "Compress responses with gzip when the client advertises support.",
			Build:       adaptMW(buildCompressMW),
		},
		{
			Kind:        KindMiddleware,
			Subtype:     "timeout",
			Label:       "Request timeout",
			Description: "Apply a context deadline to the request; the handler sees ctx cancellation when exceeded.",
			Build:       adaptMW(buildTimeoutMW),
		},
		{
			Kind:        KindMiddleware,
			Subtype:     "request-size-limit",
			Label:       "Request size limit",
			Description: "Reject request bodies larger than the configured byte count.",
			Build:       adaptMW(buildSizeLimitMW),
		},
		{
			Kind:        KindMiddleware,
			Subtype:     "strip-prefix",
			Label:       "Strip path prefix",
			Description: "Remove a prefix from r.URL.Path before the next node sees it. Mirrors turna's stripprefix middleware.",
			Build:       adaptMW(buildStripPrefixMW),
		},
		{
			Kind:        KindMiddleware,
			Subtype:     "header-compare",
			Label:       "Header compare",
			Description: "Require (or block) requests where a header matches a value / regex. Useful for cheap auth proxies and feature flags.",
			Build:       adaptMW(buildHeaderCompareMW),
		},
		{
			Kind:        KindMiddleware,
			Subtype:     "response-rewrite",
			Label:       "Rewrite response",
			Description: "Buffer the downstream response and rewrite status, headers, or body before sending. Supports text substitution and template-based body replacement.",
			Build:       adaptMW(buildResponseRewriteMW),
		},
	}

	out := make(map[string]NodeSpec, len(specs))
	for _, s := range specs {
		out[s.Subtype] = s
	}
	return out
}

// --- logger ---

// loggerCfg is intentionally empty today; left as a struct so adding
// fields later (e.g. SkipPaths) does not break callers.
type loggerCfg struct{}

func buildLoggerMW(raw json.RawMessage, _ ServiceDeps) (func(http.Handler) http.Handler, error) {
	var cfg loggerCfg
	_ = json.Unmarshal(raw, &cfg)
	// Reuse the same ada log middleware the main server uses so
	// operators see one log format across listeners.
	return mlogMiddleware(), nil
}

// mlogMiddleware is wrapped so we don't import the ada log package
// at the top level (kept the imports tidy and one indirection
// avoids dragging the package into tests that stub their own logger
// later).
func mlogMiddleware() func(http.Handler) http.Handler {
	return adaLogMiddleware()
}

// --- requestid ---

type requestIDCfg struct{}

func buildRequestIDMW(raw json.RawMessage, _ ServiceDeps) (func(http.Handler) http.Handler, error) {
	var cfg requestIDCfg
	_ = json.Unmarshal(raw, &cfg)
	return mrequestid.Middleware(), nil
}

// --- cors ---

type corsCfg struct {
	AllowOrigins     []string `json:"allow_origins,omitempty"`
	AllowMethods     []string `json:"allow_methods,omitempty"`
	AllowHeaders     []string `json:"allow_headers,omitempty"`
	AllowCredentials bool     `json:"allow_credentials,omitempty"`
	ExposeHeaders    []string `json:"expose_headers,omitempty"`
	MaxAge           int      `json:"max_age,omitempty"`
}

func buildCorsMW(raw json.RawMessage, _ ServiceDeps) (func(http.Handler) http.Handler, error) {
	var cfg corsCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("cors config: %w", err)
		}
	}
	// No origins listed = default permissive. The ada/middleware/cors
	// defaults already match this so we just hand-off when the user
	// hasn't asked for anything specific.
	if len(cfg.AllowOrigins) == 0 && len(cfg.AllowMethods) == 0 && len(cfg.AllowHeaders) == 0 {
		return mcors.Middleware(), nil
	}
	return mcors.Middleware(mcors.WithConfig(mcors.Cors{
		AllowOrigins:     cfg.AllowOrigins,
		AllowMethods:     cfg.AllowMethods,
		AllowHeaders:     cfg.AllowHeaders,
		AllowCredentials: cfg.AllowCredentials,
		ExposeHeaders:    cfg.ExposeHeaders,
		MaxAge:           cfg.MaxAge,
	})), nil
}

// --- auth-bearer ---

type authBearerCfg struct {
	// Scope overrides the value passed to service.ValidateToken. When
	// empty the scope defaults to the request URL path so tokens with
	// path-prefix scopes work transparently — same convention /data
	// uses today (see internal/server/api/data.go:18).
	Scope string `json:"scope,omitempty"`
	// Operation overrides the operation string. When empty it is
	// derived from the HTTP method (GET/HEAD -> read, PUT/POST/PATCH
	// -> write, DELETE -> delete).
	Operation string `json:"operation,omitempty"`
}

func buildAuthBearerMW(raw json.RawMessage, svc ServiceDeps) (func(http.Handler) http.Handler, error) {
	if svc == nil {
		return nil, errors.New("auth-bearer: service dependency missing")
	}
	var cfg authBearerCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("auth-bearer config: %w", err)
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok := bearerToken(r.Header.Get("Authorization"))
			if tok == "" {
				http.Error(w, "missing bearer token", http.StatusUnauthorized)
				return
			}
			scope := cfg.Scope
			if scope == "" {
				// Path comes in with the leading slash; tokens are
				// stored without it for /data and /raw, so trim to
				// keep parity.
				scope = strings.TrimPrefix(r.URL.Path, "/")
			}
			op := cfg.Operation
			if op == "" {
				op = methodToOp(r.Method)
			}
			if err := svc.ValidateToken(r.Context(), tok, scope, op); err != nil {
				switch {
				case errors.Is(err, service.ErrUnauthorized):
					http.Error(w, "unauthorized", http.StatusUnauthorized)
				default:
					http.Error(w, "forbidden", http.StatusForbidden)
				}
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

func bearerToken(h string) string {
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

func methodToOp(m string) string {
	switch strings.ToUpper(m) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return "read"
	case http.MethodDelete:
		return "delete"
	default:
		return "write"
	}
}

// --- basic-auth ---

type basicAuthCfg struct {
	Realm string                 `json:"realm,omitempty"`
	Users []basicAuthCredentials `json:"users"`
}

type basicAuthCredentials struct {
	Username string `json:"username"`
	// Password is compared in plaintext. Storage is encrypted at rest
	// because the whole Settings row goes through the seal layer, so
	// keeping plaintext on the wire (within the encrypted blob) is
	// the same trust level as e.g. FTP passwords elsewhere in
	// settings. Hashing would still be welcome — see issue tracker.
	Password string `json:"password"`
}

func buildBasicAuthMW(raw json.RawMessage, _ ServiceDeps) (func(http.Handler) http.Handler, error) {
	var cfg basicAuthCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("basic-auth config: %w", err)
		}
	}
	if len(cfg.Users) == 0 {
		return nil, errors.New("basic-auth: at least one user required")
	}
	realm := cfg.Realm
	if realm == "" {
		realm = "pika-proxy"
	}
	users := make(map[string]string, len(cfg.Users))
	for _, u := range cfg.Users {
		if u.Username == "" {
			return nil, errors.New("basic-auth: user with empty username")
		}
		users[u.Username] = u.Password
	}
	wwwAuthHeader := fmt.Sprintf(`Basic realm=%q`, realm)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok {
				w.Header().Set("WWW-Authenticate", wwwAuthHeader)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			want, found := users[user]
			// constantTimeStrEqual to defeat timing oracle. Wrapping
			// the lookup in the same compare ignores whether the
			// username existed.
			if !found || !constantTimeStrEqual(pass, want) {
				w.Header().Set("WWW-Authenticate", wwwAuthHeader)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// constantTimeStrEqual compares two strings in constant time relative
// to the longer length so a username-not-found doesn't return faster
// than a wrong-password case.
func constantTimeStrEqual(a, b string) bool {
	// subtle.ConstantTimeCompare requires equal-length slices; pad
	// the shorter side and OR the mismatch flag with the length
	// comparison so identical-length-but-different and different-length
	// inputs both take the full loop.
	aBytes, bBytes := []byte(a), []byte(b)
	var diff byte
	if len(aBytes) != len(bBytes) {
		diff = 1
	}
	max := len(aBytes)
	if len(bBytes) > max {
		max = len(bBytes)
	}
	for i := 0; i < max; i++ {
		var ai, bi byte
		if i < len(aBytes) {
			ai = aBytes[i]
		}
		if i < len(bBytes) {
			bi = bBytes[i]
		}
		diff |= ai ^ bi
	}
	return diff == 0
}

// --- ip-allowlist / ip-denylist ---

type ipListCfg struct {
	CIDRs []string `json:"cidrs"`
	// TrustForwardedFor, when true, takes the first IP in the
	// X-Forwarded-For header as the source. Off by default because
	// trusting that header on an unproxied listener lets any client
	// spoof their address.
	TrustForwardedFor bool `json:"trust_forwarded_for,omitempty"`
}

func parseIPList(cfg ipListCfg) ([]*net.IPNet, error) {
	nets := make([]*net.IPNet, 0, len(cfg.CIDRs))
	for _, c := range cfg.CIDRs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		// Accept a bare IP by appending /32 or /128.
		if !strings.Contains(c, "/") {
			ip := net.ParseIP(c)
			if ip == nil {
				return nil, fmt.Errorf("invalid IP %q", c)
			}
			if ip.To4() != nil {
				c += "/32"
			} else {
				c += "/128"
			}
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", c, err)
		}
		nets = append(nets, n)
	}
	return nets, nil
}

func clientIPFor(r *http.Request, trustXFF bool) net.IP {
	if trustXFF {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
			if ip := net.ParseIP(first); ip != nil {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

func buildIPAllowMW(raw json.RawMessage, _ ServiceDeps) (func(http.Handler) http.Handler, error) {
	var cfg ipListCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("ip-allowlist config: %w", err)
		}
	}
	nets, err := parseIPList(cfg)
	if err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIPFor(r, cfg.TrustForwardedFor)
			for _, n := range nets {
				if ip != nil && n.Contains(ip) {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, "forbidden", http.StatusForbidden)
		})
	}, nil
}

func buildIPDenyMW(raw json.RawMessage, _ ServiceDeps) (func(http.Handler) http.Handler, error) {
	var cfg ipListCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("ip-denylist config: %w", err)
		}
	}
	nets, err := parseIPList(cfg)
	if err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIPFor(r, cfg.TrustForwardedFor)
			for _, n := range nets {
				if ip != nil && n.Contains(ip) {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// --- header-inject / header-remove ---

type headerInjectCfg struct {
	Request  map[string]string `json:"request,omitempty"`
	Response map[string]string `json:"response,omitempty"`
	// Overwrite, when true, replaces any existing value. When false
	// (default) values are appended via http.Header.Add, matching the
	// usual semantics for multi-value headers.
	Overwrite bool `json:"overwrite,omitempty"`
}

func buildHeaderInjectMW(raw json.RawMessage, _ ServiceDeps) (func(http.Handler) http.Handler, error) {
	var cfg headerInjectCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("header-inject config: %w", err)
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for k, v := range cfg.Request {
				if cfg.Overwrite {
					r.Header.Set(k, v)
				} else {
					r.Header.Add(k, v)
				}
			}
			// Response headers must be set before the handler writes
			// the status. Wrap the writer so we set them just-in-time.
			ww := &headerInjectWriter{ResponseWriter: w, response: cfg.Response, overwrite: cfg.Overwrite}
			next.ServeHTTP(ww, r)
		})
	}, nil
}

type headerInjectWriter struct {
	http.ResponseWriter
	response  map[string]string
	overwrite bool
	written   bool
}

func (w *headerInjectWriter) applyOnce() {
	if w.written {
		return
	}
	w.written = true
	for k, v := range w.response {
		if w.overwrite {
			w.ResponseWriter.Header().Set(k, v)
		} else {
			w.ResponseWriter.Header().Add(k, v)
		}
	}
}

func (w *headerInjectWriter) WriteHeader(code int) {
	w.applyOnce()
	w.ResponseWriter.WriteHeader(code)
}

func (w *headerInjectWriter) Write(b []byte) (int, error) {
	w.applyOnce()
	return w.ResponseWriter.Write(b)
}

type headerRemoveCfg struct {
	Request  []string `json:"request,omitempty"`
	Response []string `json:"response,omitempty"`
}

func buildHeaderRemoveMW(raw json.RawMessage, _ ServiceDeps) (func(http.Handler) http.Handler, error) {
	var cfg headerRemoveCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("header-remove config: %w", err)
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, k := range cfg.Request {
				r.Header.Del(k)
			}
			ww := &headerStripWriter{ResponseWriter: w, response: cfg.Response}
			next.ServeHTTP(ww, r)
		})
	}, nil
}

type headerStripWriter struct {
	http.ResponseWriter
	response []string
	written  bool
}

func (w *headerStripWriter) strip() {
	if w.written {
		return
	}
	w.written = true
	for _, k := range w.response {
		w.ResponseWriter.Header().Del(k)
	}
}

func (w *headerStripWriter) WriteHeader(code int) {
	w.strip()
	w.ResponseWriter.WriteHeader(code)
}

func (w *headerStripWriter) Write(b []byte) (int, error) {
	w.strip()
	return w.ResponseWriter.Write(b)
}

// --- ratelimit ---

type rateLimitCfg struct {
	Window         string `json:"window"`                    // Go duration string e.g. "1m"
	SoftThreshold  int    `json:"soft_threshold,omitempty"`  // start backoff (>=)
	HardThreshold  int    `json:"hard_threshold"`            // reject (>=)
	BackoffBase    string `json:"backoff_base,omitempty"`    // Go duration string
	BackoffMax     string `json:"backoff_max,omitempty"`     // Go duration string
	StoreCapacity  int    `json:"store_capacity,omitempty"`  // memory store size
	// KeyBy selects what to bucket by. "ip" (default) uses the
	// remote address; "header" bucket by the header named in
	// KeyHeader; "ip+path" combines RemoteAddr and URL.Path.
	KeyBy             string `json:"key_by,omitempty"`
	KeyHeader         string `json:"key_header,omitempty"`
	TrustForwardedFor bool   `json:"trust_forwarded_for,omitempty"`
}

func buildRateLimitMW(raw json.RawMessage, _ ServiceDeps) (func(http.Handler) http.Handler, error) {
	var cfg rateLimitCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("ratelimit config: %w", err)
		}
	}
	if cfg.HardThreshold <= 0 {
		return nil, errors.New("ratelimit: hard_threshold must be > 0")
	}
	window, err := parseDuration(cfg.Window, time.Minute)
	if err != nil {
		return nil, fmt.Errorf("ratelimit window: %w", err)
	}
	backoffBase, err := parseDuration(cfg.BackoffBase, 100*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("ratelimit backoff_base: %w", err)
	}
	backoffMax, err := parseDuration(cfg.BackoffMax, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("ratelimit backoff_max: %w", err)
	}
	storeCap := cfg.StoreCapacity
	if storeCap <= 0 {
		storeCap = 10_000
	}
	store, err := ratelimit.NewMemoryStore(storeCap)
	if err != nil {
		return nil, fmt.Errorf("ratelimit store: %w", err)
	}
	keyFn := rateLimitKeyFunc(cfg)
	return ratelimit.Middleware(ratelimit.Config{
		Window:        window,
		SoftThreshold: cfg.SoftThreshold,
		HardThreshold: cfg.HardThreshold,
		BackoffBase:   backoffBase,
		BackoffMax:    backoffMax,
		KeyFunc:       keyFn,
		// Count every request regardless of status. Login-guard
		// only counts 4xx because it's brute-force protection; a
		// generic limiter wants to throttle abusive throughput too.
		ShouldCount: func(*http.Request, int) bool { return true },
		Store:       store,
	}), nil
}

func rateLimitKeyFunc(cfg rateLimitCfg) func(*http.Request) []string {
	switch strings.ToLower(cfg.KeyBy) {
	case "header":
		h := cfg.KeyHeader
		if h == "" {
			h = "X-API-Key"
		}
		return func(r *http.Request) []string {
			v := strings.TrimSpace(r.Header.Get(h))
			if v == "" {
				return nil
			}
			return []string{"hdr:" + v}
		}
	case "ip+path":
		return func(r *http.Request) []string {
			ip := clientIPFor(r, cfg.TrustForwardedFor)
			if ip == nil {
				return nil
			}
			return []string{"ip+path:" + ip.String() + "|" + r.URL.Path}
		}
	default:
		return func(r *http.Request) []string {
			ip := clientIPFor(r, cfg.TrustForwardedFor)
			if ip == nil {
				return nil
			}
			return []string{"ip:" + ip.String()}
		}
	}
}

func parseDuration(s string, def time.Duration) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	return time.ParseDuration(s)
}

// --- compress ---

type compressCfg struct {
	// MinLength avoids the gzip overhead on tiny bodies. Default 1
	// KiB. Honoured by deferring compression until the buffered
	// portion exceeds the threshold; smaller responses go through
	// unmodified.
	MinLength int `json:"min_length,omitempty"`
	// Level is the gzip compression level (1-9). Defaults to
	// gzip.DefaultCompression (6).
	Level int `json:"level,omitempty"`
}

func buildCompressMW(raw json.RawMessage, _ ServiceDeps) (func(http.Handler) http.Handler, error) {
	var cfg compressCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("compress config: %w", err)
		}
	}
	if cfg.MinLength <= 0 {
		cfg.MinLength = 1024
	}
	level := cfg.Level
	if level == 0 {
		level = gzip.DefaultCompression
	}
	// Pool writers so allocator pressure stays bounded on hot paths.
	pool := &sync.Pool{New: func() any {
		gz, err := gzip.NewWriterLevel(io.Discard, level)
		if err != nil {
			return gzip.NewWriter(io.Discard)
		}
		return gz
	}}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				next.ServeHTTP(w, r)
				return
			}
			gw := &gzipResponseWriter{
				ResponseWriter: w,
				pool:           pool,
				minLength:      cfg.MinLength,
			}
			defer gw.Close()
			next.ServeHTTP(gw, r)
		})
	}, nil
}

// gzipResponseWriter buffers up to MinLength bytes; once the buffer
// overflows it switches the connection to gzip mode (and sets the
// Content-Encoding header). Small bodies skip compression entirely
// for predictable latency.
type gzipResponseWriter struct {
	http.ResponseWriter
	pool      *sync.Pool
	minLength int

	buf       []byte
	gz        *gzip.Writer
	wroteCT   bool
	headerSet bool
	status    int
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	// Defer the actual WriteHeader until we know whether to compress.
	g.status = code
	g.headerSet = true
}

func (g *gzipResponseWriter) Write(p []byte) (int, error) {
	if g.gz != nil {
		return g.gz.Write(p)
	}
	g.buf = append(g.buf, p...)
	if len(g.buf) < g.minLength {
		return len(p), nil
	}
	// Promote to gzip.
	g.ResponseWriter.Header().Set("Content-Encoding", "gzip")
	g.ResponseWriter.Header().Del("Content-Length") // length changes after compression
	if g.headerSet {
		g.ResponseWriter.WriteHeader(g.status)
	}
	gz := g.pool.Get().(*gzip.Writer)
	gz.Reset(g.ResponseWriter)
	g.gz = gz
	if _, err := g.gz.Write(g.buf); err != nil {
		return 0, err
	}
	g.buf = nil
	return len(p), nil
}

func (g *gzipResponseWriter) Close() {
	if g.gz != nil {
		g.gz.Close()
		g.pool.Put(g.gz)
		return
	}
	// Small body — emit unmodified.
	if g.headerSet {
		g.ResponseWriter.WriteHeader(g.status)
	}
	if len(g.buf) > 0 {
		g.ResponseWriter.Write(g.buf)
	}
}

// --- timeout ---

type timeoutCfg struct {
	Duration string `json:"duration"` // Go duration string e.g. "10s"
}

func buildTimeoutMW(raw json.RawMessage, _ ServiceDeps) (func(http.Handler) http.Handler, error) {
	var cfg timeoutCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("timeout config: %w", err)
		}
	}
	d, err := parseDuration(cfg.Duration, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("timeout duration: %w", err)
	}
	if d <= 0 {
		return nil, errors.New("timeout: duration must be > 0")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}, nil
}

// --- request-size-limit ---

type sizeLimitCfg struct {
	MaxBytes int64 `json:"max_bytes"`
}

func buildSizeLimitMW(raw json.RawMessage, _ ServiceDeps) (func(http.Handler) http.Handler, error) {
	var cfg sizeLimitCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("request-size-limit config: %w", err)
		}
	}
	if cfg.MaxBytes <= 0 {
		return nil, errors.New("request-size-limit: max_bytes must be > 0")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxBytes)
			next.ServeHTTP(w, r)
		})
	}, nil
}

// --- strip-prefix ---

// stripPrefixCfg mirrors turna's stripprefix middleware shape so an
// operator coming from a turna config recognises the field names.
// Two ways to specify the prefix(es):
//
//   - Prefix: a single string. First match wins (and it is also the
//     only match).
//   - Prefixes: a list of strings tried in order. The first that
//     matches r.URL.Path is removed; the rest are ignored.
//
// ForceSlash defaults to true, which keeps the trimmed path
// "/"-rooted (matches turna behaviour: stripping "/api" from
// "/api/users" yields "/users"). When false the path keeps
// whatever leading bytes remained, which is occasionally what an
// operator wants when chaining strip-prefix with proxy-pass.
type stripPrefixCfg struct {
	Prefix     string   `json:"prefix,omitempty"`
	Prefixes   []string `json:"prefixes,omitempty"`
	ForceSlash *bool    `json:"force_slash,omitempty"`
}

func buildStripPrefixMW(raw json.RawMessage, _ ServiceDeps) (func(http.Handler) http.Handler, error) {
	var cfg stripPrefixCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("strip-prefix config: %w", err)
		}
	}
	prefixes := cfg.Prefixes
	if len(prefixes) == 0 && cfg.Prefix != "" {
		prefixes = []string{cfg.Prefix}
	}
	if len(prefixes) == 0 {
		// Nothing to strip — installing a no-op middleware is
		// preferable to an error because an operator who just
		// dropped the node onto the canvas should not have to
		// fill in a field before save works.
		return func(next http.Handler) http.Handler { return next }, nil
	}
	forceSlash := true
	if cfg.ForceSlash != nil {
		forceSlash = *cfg.ForceSlash
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, p := range prefixes {
				if strings.HasPrefix(r.URL.Path, p) {
					r.URL.Path = strings.TrimPrefix(r.URL.Path, p)
					break
				}
			}
			if forceSlash && !strings.HasPrefix(r.URL.Path, "/") {
				r.URL.Path = "/" + r.URL.Path
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// --- header-compare ---

// headerCompareCfg checks one or more request headers against
// expected values. The match policy is configurable:
//
//   - Mode "allow" (default): a match passes through; a miss
//     responds with the configured status (default 403).
//   - Mode "block": a match blocks (configured status); a miss
//     passes through.
//
// Per-header comparison: when both Equals and Regex are empty the
// rule just requires the header to be present (non-empty). Equals
// is exact (case-sensitive value). Regex is the Go RE2 syntax.
// Multiple headers are AND'd — every entry must satisfy its rule
// for the overall match to fire.
type headerCompareRule struct {
	Equals string `json:"equals,omitempty"`
	Regex  string `json:"regex,omitempty"`
}

// UnmarshalJSON lets the operator write either the rich form
// (`{"X":{"equals":"v"}}`) or the shorthand string form
// (`{"X":"v"}`, treated as equals). The UI's kv-map widget emits
// the shorthand so the operator does not have to nest objects
// just to express the common case.
func (r *headerCompareRule) UnmarshalJSON(data []byte) error {
	// Try the rich form first.
	type richAlias headerCompareRule
	var rich richAlias
	if err := json.Unmarshal(data, &rich); err == nil {
		*r = headerCompareRule(rich)
		return nil
	}
	// Fall back to a bare string: treat as equals.
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("header-compare rule: expected object or string, got %s", string(data))
	}
	r.Equals = s
	return nil
}

type headerCompareCfg struct {
	Mode    string                       `json:"mode,omitempty"`    // "allow" (default) or "block"
	Status  int                          `json:"status,omitempty"`  // response code on the failing branch; default 403
	Headers map[string]headerCompareRule `json:"headers,omitempty"` // name -> rule (AND'd)
}

func buildHeaderCompareMW(raw json.RawMessage, _ ServiceDeps) (func(http.Handler) http.Handler, error) {
	var cfg headerCompareCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("header-compare config: %w", err)
		}
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = "allow"
	}
	if mode != "allow" && mode != "block" {
		return nil, fmt.Errorf("header-compare: mode must be 'allow' or 'block', got %q", mode)
	}
	status := cfg.Status
	if status == 0 {
		status = http.StatusForbidden
	}

	// Pre-compile regex rules once; surface bad patterns at build
	// time so the UI can flag the broken node rather than 500'ing
	// the request later.
	type compiled struct {
		equals string
		regex  *regexp.Regexp
	}
	compiledRules := make(map[string]compiled, len(cfg.Headers))
	for name, r := range cfg.Headers {
		c := compiled{equals: r.Equals}
		if r.Regex != "" {
			rx, err := regexp.Compile(r.Regex)
			if err != nil {
				return nil, fmt.Errorf("header-compare: header %q regex: %w", name, err)
			}
			c.regex = rx
		}
		compiledRules[name] = c
	}

	matchAll := func(r *http.Request) bool {
		for name, rule := range compiledRules {
			v := r.Header.Get(name)
			switch {
			case rule.regex != nil:
				if !rule.regex.MatchString(v) {
					return false
				}
			case rule.equals != "":
				if v != rule.equals {
					return false
				}
			default:
				if v == "" {
					return false
				}
			}
		}
		return true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			matched := matchAll(r)
			pass := (mode == "allow" && matched) || (mode == "block" && !matched)
			if !pass {
				http.Error(w, http.StatusText(status), status)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// --- response-rewrite ---

// responseRewriteCfg buffers the downstream response and applies
// transforms before flushing it to the real ResponseWriter. The
// transforms are intentionally narrow — pika is not a templating
// engine; for richer transforms an operator should write a custom
// handler. The supported knobs cover the 80% of "I need to mask
// an upstream error message" / "force JSON content type" cases:
//
//   - SetStatus: replace the downstream status code wholesale.
//   - StatusMap: per-status replacement (502→503, etc.).
//   - SetHeaders: add or overwrite response headers.
//   - DeleteHeaders: strip headers entirely (set-cookie leaks etc.).
//   - BodyReplace: list of {from, to} string substitutions on the
//     body. Plain literal; no regex (regex versions tend to get
//     misused on JSON and break it).
//   - BodyOverride: complete replacement of the body. Status and
//     content-type can be set alongside for a clean overwrite.
//
// Body buffering is bounded by MaxBodyBytes (default 1 MiB). Bodies
// larger than the cap pass through untouched but the rewrite knobs
// silently skip — we never truncate.
type bodySubst struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type responseRewriteCfg struct {
	SetStatus     int               `json:"set_status,omitempty"`
	StatusMap     map[string]int    `json:"status_map,omitempty"`
	SetHeaders    map[string]string `json:"set_headers,omitempty"`
	DeleteHeaders []string          `json:"delete_headers,omitempty"`
	BodyReplace   []bodySubst       `json:"body_replace,omitempty"`
	BodyOverride  string            `json:"body_override,omitempty"`
	MaxBodyBytes  int               `json:"max_body_bytes,omitempty"` // default 1 MiB
}

// bufferingWriter captures status, headers and body until Flush()
// is called by the middleware. It implements just enough of
// http.ResponseWriter for the downstream handlers we ship; if a
// future handler needs hijacker / flusher / pusher we'll grow it
// behind a build-tag-style assertion.
type bufferingWriter struct {
	header  http.Header
	status  int
	body    []byte
	maxBody int
	tooBig  bool
}

func (b *bufferingWriter) Header() http.Header {
	if b.header == nil {
		b.header = http.Header{}
	}
	return b.header
}

func (b *bufferingWriter) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}

func (b *bufferingWriter) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	if b.tooBig {
		// Already over the cap — stop buffering further bytes;
		// we'll fall through to a pass-through path on flush.
		return len(p), nil
	}
	if len(b.body)+len(p) > b.maxBody {
		b.tooBig = true
		// Keep the bytes we already have; the flush path knows
		// what to do (no rewrite, send the partial buffer plus
		// the new bytes through as-is).
		b.body = append(b.body, p...)
		return len(p), nil
	}
	b.body = append(b.body, p...)
	return len(p), nil
}

func buildResponseRewriteMW(raw json.RawMessage, _ ServiceDeps) (func(http.Handler) http.Handler, error) {
	var cfg responseRewriteCfg
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("response-rewrite config: %w", err)
		}
	}
	maxBody := cfg.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = 1 << 20 // 1 MiB
	}
	// statusMap keys arrive as JSON strings ("502") — preconvert
	// to int once so the per-request hot path is a single lookup.
	mappedStatus := make(map[int]int, len(cfg.StatusMap))
	for k, v := range cfg.StatusMap {
		var src int
		if _, err := fmt.Sscanf(k, "%d", &src); err != nil {
			return nil, fmt.Errorf("response-rewrite: status_map key %q is not an integer", k)
		}
		mappedStatus[src] = v
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			buf := &bufferingWriter{maxBody: maxBody}
			next.ServeHTTP(buf, r)

			// Status resolution (priority): SetStatus > status_map > original.
			status := buf.status
			if status == 0 {
				status = http.StatusOK
			}
			if cfg.SetStatus != 0 {
				status = cfg.SetStatus
			} else if mapped, ok := mappedStatus[status]; ok {
				status = mapped
			}

			// Body transformation. BodyOverride wins outright;
			// otherwise BodyReplace runs in order. Both paths
			// no-op when the body overflowed the cap so we
			// never corrupt a half-buffered response.
			body := buf.body
			if !buf.tooBig {
				if cfg.BodyOverride != "" {
					body = []byte(cfg.BodyOverride)
				} else if len(cfg.BodyReplace) > 0 {
					body = applyBodyReplace(body, cfg.BodyReplace)
				}
			}

			// Header pipeline: copy upstream headers verbatim,
			// then strip the ones the operator told us to drop,
			// then overwrite with the SetHeaders map.
			out := w.Header()
			for k, vs := range buf.Header() {
				for _, v := range vs {
					out.Add(k, v)
				}
			}
			for _, k := range cfg.DeleteHeaders {
				out.Del(k)
			}
			for k, v := range cfg.SetHeaders {
				out.Set(k, v)
			}
			// Content-Length is invalidated by any body rewrite.
			// Let the http package recompute it on flush.
			if cfg.BodyOverride != "" || len(cfg.BodyReplace) > 0 {
				out.Del("Content-Length")
			}

			w.WriteHeader(status)
			_, _ = w.Write(body)
		})
	}, nil
}

func applyBodyReplace(body []byte, subs []bodySubst) []byte {
	s := string(body)
	for _, sub := range subs {
		if sub.From == "" {
			continue
		}
		s = strings.ReplaceAll(s, sub.From, sub.To)
	}
	return []byte(s)
}

// --- decoding helper used by tests for round-trip checks ---

// decodeBase64 is exported only to make round-trip tests for the
// header injectors a little less verbose; the runtime middlewares
// above never base64-anything.
func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
