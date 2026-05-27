package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/ok"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// httpRequestConfig is the persisted config for the http-request
// handler — a terminal proxy node that sends one outbound request
// to the templated URL and writes the upstream response back to
// the client. Modelled after chore's `request` node but adapted
// to Pika's stateless middleware shape and the `rakunlabs/ok`
// HTTP client.
type httpRequestConfig struct {
	// URL is the upstream URL, optionally mugo-templated. The
	// rendered string must be a fully-qualified URL (scheme + host).
	URL string `json:"url"`
	// Method is HTTP method; defaults to GET when empty. Templated.
	Method string `json:"method"`
	// Headers are added to the outbound request. Values are
	// mugo-templated against the same context as URL/Method.
	Headers map[string]string `json:"headers,omitempty"`
	// ForwardBody, when true (default), forwards the incoming
	// request body verbatim. When false, the body is dropped
	// unless BodyTemplate is set.
	ForwardBody *bool `json:"forward_body,omitempty"`
	// BodyTemplate replaces the outbound body. When set the
	// rendered template is sent regardless of ForwardBody.
	BodyTemplate string `json:"body_template,omitempty"`
	// MaxRequestBodyBytes caps the incoming body we read into the
	// template context (only when BodyTemplate is set or
	// ParsePayload is true). Default 1 MiB.
	MaxRequestBodyBytes int64 `json:"max_request_body_bytes,omitempty"`
	// ParsePayload, when true, decodes the incoming body as JSON
	// or YAML before exposing it as `.payload` to templates.
	ParsePayload bool `json:"parse_payload,omitempty"`

	TimeoutMs       int    `json:"timeout_ms,omitempty"`
	SkipTLSVerify   bool   `json:"skip_tls_verify,omitempty"`
	UpstreamProxy   string `json:"upstream_proxy,omitempty"`
	FollowRedirects *bool  `json:"follow_redirects,omitempty"`

	Retry httpRequestRetry `json:"retry,omitempty"`
	Auth  httpRequestAuth  `json:"auth,omitempty"`

	Response httpRequestResponseRules `json:"response,omitempty"`
}

type httpRequestRetry struct {
	Enabled      bool          `json:"enabled,omitempty"`
	MaxAttempts  int           `json:"max_attempts,omitempty"`
	BackoffMinMs int           `json:"backoff_min_ms,omitempty"`
	BackoffMaxMs int           `json:"backoff_max_ms,omitempty"`
	RetryCodes   []int         `json:"retry_status_codes,omitempty"`
	NoRetryCodes []int         `json:"no_retry_status_codes,omitempty"`
	_            time.Duration // reserved
}

type httpRequestAuth struct {
	Kind     string                 `json:"kind,omitempty"` // none | basic | bearer | oauth2_cc
	Basic    httpRequestAuthBasic   `json:"basic,omitempty"`
	Bearer   httpRequestAuthBearer  `json:"bearer,omitempty"`
	OAuth2CC httpRequestAuthOAuth2  `json:"oauth2_cc,omitempty"`
	Extras   map[string]interface{} `json:"-"`
}

type httpRequestAuthBasic struct {
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
}

type httpRequestAuthBearer struct {
	Token string `json:"token,omitempty"`
}

type httpRequestAuthOAuth2 struct {
	ClientID     string   `json:"client_id,omitempty"`
	ClientSecret string   `json:"client_secret,omitempty"`
	TokenURL     string   `json:"token_url,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	// AuthStyle: "" (auto) | "in_params" | "in_header". Pass-through
	// to clientcredentials.
	AuthStyle string `json:"auth_style,omitempty"`
}

type httpRequestResponseRules struct {
	// PassThrough defaults to true: upstream status + headers + body
	// are written verbatim. When false, the response is empty unless
	// StatusOverride / HeaderOverrides are set.
	PassThrough *bool             `json:"pass_through,omitempty"`
	StatusOverride int            `json:"status_override,omitempty"`
	HeaderOverrides map[string]string `json:"header_overrides,omitempty"`
}

// buildHTTPRequestHandler constructs the closure for one configured
// http-request handler node. The closure owns its own ok.Client so
// retry/TLS/proxy/OAuth2 setup happens once at compile time.
func buildHTTPRequestHandler(cfg json.RawMessage, _ ServiceDeps) (http.Handler, error) {
	var c httpRequestConfig
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &c); err != nil {
			return nil, fmt.Errorf("http-request config: %w", err)
		}
	}
	if strings.TrimSpace(c.URL) == "" {
		return nil, fmt.Errorf("http-request: url is required")
	}
	if c.Method == "" {
		c.Method = http.MethodGet
	}
	maxBody := c.MaxRequestBodyBytes
	if maxBody <= 0 {
		maxBody = 1 << 20 // 1 MiB
	}
	forwardBody := true
	if c.ForwardBody != nil {
		forwardBody = *c.ForwardBody
	}
	if c.BodyTemplate != "" {
		// Explicit body override beats forwarding; document by
		// flipping the flag so the runtime path is one branch.
		forwardBody = false
	}

	client, err := buildHTTPRequestClient(&c)
	if err != nil {
		return nil, err
	}

	resp := c.Response
	passThrough := true
	if resp.PassThrough != nil {
		passThrough = *resp.PassThrough
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read upstream payload up-front when we need it for the
		// template context. Otherwise we stream the body through
		// untouched, which is the cheap path for plain proxying.
		var (
			incomingBody []byte
			payload      any
			readErr      error
		)
		needsBody := forwardBody || c.BodyTemplate != "" || c.ParsePayload
		if needsBody && r.Body != nil {
			incomingBody, readErr = io.ReadAll(io.LimitReader(r.Body, maxBody+1))
			_ = r.Body.Close()
			if readErr != nil {
				http.Error(w, "read request body: "+readErr.Error(), http.StatusBadRequest)
				return
			}
			if int64(len(incomingBody)) > maxBody {
				http.Error(w, "request body exceeds max_request_body_bytes", http.StatusRequestEntityTooLarge)
				return
			}
			if c.ParsePayload {
				payload = decodePayload(incomingBody)
			}
		}

		tmplCtx := map[string]any{
			"req": map[string]any{
				"method":  r.Method,
				"path":    r.URL.Path,
				"host":    r.Host,
				"query":   flatQuery(r.URL.Query()),
				"headers": flatHeaders(r.Header),
			},
			"payload": payload,
		}

		renderedURL, err := renderTemplate(c.URL, tmplCtx)
		if err != nil {
			http.Error(w, "render url: "+err.Error(), http.StatusInternalServerError)
			return
		}
		renderedMethod, err := renderTemplate(c.Method, tmplCtx)
		if err != nil {
			http.Error(w, "render method: "+err.Error(), http.StatusInternalServerError)
			return
		}
		renderedMethod = strings.ToUpper(strings.TrimSpace(renderedMethod))
		if renderedMethod == "" {
			renderedMethod = http.MethodGet
		}

		var bodyReader io.Reader
		if c.BodyTemplate != "" {
			rb, err := renderTemplate(c.BodyTemplate, tmplCtx)
			if err != nil {
				http.Error(w, "render body: "+err.Error(), http.StatusInternalServerError)
				return
			}
			bodyReader = bytes.NewBufferString(rb)
		} else if forwardBody && len(incomingBody) > 0 {
			bodyReader = bytes.NewReader(incomingBody)
		}

		outboundCtx := r.Context()
		if c.TimeoutMs > 0 {
			var cancel context.CancelFunc
			outboundCtx, cancel = context.WithTimeout(outboundCtx, time.Duration(c.TimeoutMs)*time.Millisecond)
			defer cancel()
		}

		outReq, err := http.NewRequestWithContext(outboundCtx, renderedMethod, renderedURL, bodyReader)
		if err != nil {
			http.Error(w, "build upstream request: "+err.Error(), http.StatusBadGateway)
			return
		}
		// Render and apply configured headers.
		for k, v := range c.Headers {
			rv, err := renderTemplate(v, tmplCtx)
			if err != nil {
				http.Error(w, "render header "+k+": "+err.Error(), http.StatusInternalServerError)
				return
			}
			outReq.Header.Set(k, rv)
		}
		// Static auth (basic / bearer) is applied here; OAuth2 lives
		// on the client's RoundTripper chain.
		applyStaticAuth(outReq, c.Auth)

		// Forward request-id when present so traces survive the hop.
		if id := r.Header.Get("X-Request-Id"); id != "" && outReq.Header.Get("X-Request-Id") == "" {
			outReq.Header.Set("X-Request-Id", id)
		}

		upstream, err := client.HTTP.Do(outReq)
		if err != nil {
			slog.Warn("http-request upstream failed",
				"url", renderedURL,
				"method", renderedMethod,
				"error", err,
			)
			http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer upstream.Body.Close()

		if passThrough {
			// Copy headers, then status, then body. Drop hop-by-hop
			// headers that http.Client/server already manage so we
			// don't double-write.
			for k, vs := range upstream.Header {
				if isHopByHop(k) {
					continue
				}
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			applyHeaderOverrides(w.Header(), resp.HeaderOverrides)
			status := upstream.StatusCode
			if resp.StatusOverride > 0 {
				status = resp.StatusOverride
			}
			w.WriteHeader(status)
			_, _ = io.Copy(w, upstream.Body)
			return
		}

		// Not pass-through: drain the upstream body (so the conn
		// returns to the pool) but ignore the bytes. Operator can
		// inject status/headers via overrides.
		_, _ = io.Copy(io.Discard, upstream.Body)
		applyHeaderOverrides(w.Header(), resp.HeaderOverrides)
		status := upstream.StatusCode
		if resp.StatusOverride > 0 {
			status = resp.StatusOverride
		}
		w.WriteHeader(status)
	}), nil
}

// buildHTTPRequestClient produces the ok.Client for one node. Setup
// is done once at compile time so per-request work is just `Do`.
func buildHTTPRequestClient(c *httpRequestConfig) (*ok.Client, error) {
	opts := []ok.OptionClientFn{}

	if c.SkipTLSVerify {
		opts = append(opts, ok.WithInsecureSkipVerify(true))
		// Belt-and-braces: ok's InsecureSkipVerify only fires on
		// the default transport. When the operator combines
		// skip_tls_verify with an upstream proxy, we must give
		// ok our own *http.Transport so the flag actually sticks.
		opts = append(opts, ok.WithTLSConfig(&tls.Config{InsecureSkipVerify: true})) //nolint:gosec // operator opt-in
	}
	if c.UpstreamProxy != "" {
		opts = append(opts, ok.WithProxy(c.UpstreamProxy))
	}
	if c.TimeoutMs > 0 {
		// Hard cap on a full attempt incl. retries; the per-request
		// context above adds a tighter per-request timeout.
		opts = append(opts, ok.WithTimeout(time.Duration(c.TimeoutMs*4)*time.Millisecond))
	}

	if c.Retry.Enabled {
		if c.Retry.MaxAttempts > 0 {
			opts = append(opts, ok.WithRetryMax(c.Retry.MaxAttempts))
		}
		if c.Retry.BackoffMinMs > 0 {
			opts = append(opts, ok.WithRetryWaitMin(time.Duration(c.Retry.BackoffMinMs)*time.Millisecond))
		}
		if c.Retry.BackoffMaxMs > 0 {
			opts = append(opts, ok.WithRetryWaitMax(time.Duration(c.Retry.BackoffMaxMs)*time.Millisecond))
		}
		if len(c.Retry.RetryCodes) > 0 || len(c.Retry.NoRetryCodes) > 0 {
			policy := buildRetryPolicy(c.Retry.RetryCodes, c.Retry.NoRetryCodes)
			opts = append(opts, ok.WithRetryPolicy(policy))
		}
	} else {
		opts = append(opts, ok.WithDisableRetry(true))
	}

	// OAuth2 client-credentials: install as a round-tripper wrapper
	// so the token source caches across requests and refreshes
	// transparently when expired.
	if c.Auth.Kind == "oauth2_cc" && c.Auth.OAuth2CC.TokenURL != "" {
		oc := c.Auth.OAuth2CC
		cfg := &clientcredentials.Config{
			ClientID:     oc.ClientID,
			ClientSecret: oc.ClientSecret,
			TokenURL:     oc.TokenURL,
			Scopes:       oc.Scopes,
		}
		switch strings.ToLower(strings.TrimSpace(oc.AuthStyle)) {
		case "in_params":
			cfg.AuthStyle = oauth2.AuthStyleInParams
		case "in_header":
			cfg.AuthStyle = oauth2.AuthStyleInHeader
		}
		ts := cfg.TokenSource(context.Background())
		opts = append(opts, ok.WithRoundTripper(func(_ context.Context, base http.RoundTripper) (http.RoundTripper, error) {
			return &oauth2RoundTripper{base: base, src: ts}, nil
		}))
	}

	cli, err := ok.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("http-request: build client: %w", err)
	}
	return cli, nil
}

// buildRetryPolicy returns a policy that retries when the response
// code is in retryCodes OR (when no whitelist) the code is 5xx, but
// never when it's in noRetryCodes. Mirrors chore's retry semantics.
func buildRetryPolicy(retry, noRetry []int) func(ctx context.Context, resp *http.Response, err error) (bool, error) {
	retrySet := map[int]struct{}{}
	noRetrySet := map[int]struct{}{}
	for _, c := range retry {
		retrySet[c] = struct{}{}
	}
	for _, c := range noRetry {
		noRetrySet[c] = struct{}{}
	}
	return func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if err != nil {
			return true, nil
		}
		if resp == nil {
			return true, nil
		}
		if _, blocked := noRetrySet[resp.StatusCode]; blocked {
			return false, nil
		}
		if len(retrySet) > 0 {
			if _, ok := retrySet[resp.StatusCode]; ok {
				return true, nil
			}
			return false, nil
		}
		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			return true, nil
		}
		return false, nil
	}
}

// applyStaticAuth sets Authorization headers for basic/bearer auth.
// OAuth2 lives on the round-tripper chain so this function is a
// no-op for that kind.
func applyStaticAuth(req *http.Request, a httpRequestAuth) {
	switch a.Kind {
	case "basic":
		user, pass := a.Basic.User, a.Basic.Password
		if user == "" && pass == "" {
			return
		}
		raw := user + ":" + pass
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(raw)))
	case "bearer":
		if a.Bearer.Token == "" {
			return
		}
		req.Header.Set("Authorization", "Bearer "+a.Bearer.Token)
	}
}

func applyHeaderOverrides(h http.Header, overrides map[string]string) {
	for k, v := range overrides {
		if v == "" {
			h.Del(k)
			continue
		}
		h.Set(k, v)
	}
}

func flatQuery(q url.Values) map[string]string {
	out := map[string]string{}
	for k, vs := range q {
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}

func flatHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for k, vs := range h {
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}

// hopByHopHeaders is the canonical list per RFC 7230 §6.1 plus a
// few extras chore proxied through that turn out to be dangerous
// for any reverse-proxy hop.
var hopByHopHeaders = map[string]struct{}{
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailers":            {},
	"transfer-encoding":   {},
	"upgrade":             {},
}

func isHopByHop(name string) bool {
	_, ok := hopByHopHeaders[strings.ToLower(name)]
	return ok
}

// oauth2RoundTripper wraps a base RoundTripper and adds an OAuth2
// Bearer token from the embedded TokenSource. We don't use
// oauth2.NewClient because it builds its own *http.Client; we want
// the token-injection layer to live INSIDE ok's transport chain so
// retries reuse the cached token.
type oauth2RoundTripper struct {
	base http.RoundTripper
	src  oauth2.TokenSource
	mu   sync.Mutex
}

func (rt *oauth2RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	tok, err := rt.src.Token()
	rt.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("oauth2: fetch token: %w", err)
	}
	clone := req.Clone(req.Context())
	scheme := tok.TokenType
	if scheme == "" {
		scheme = "Bearer"
	}
	clone.Header.Set("Authorization", scheme+" "+tok.AccessToken)
	return rt.base.RoundTrip(clone)
}
