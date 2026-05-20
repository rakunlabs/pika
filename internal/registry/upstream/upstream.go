// Package upstream is the pull-through proxy substrate every remote
// registry (Go, NPM, Docker) uses to fetch from an external source.
//
// The same Client serves all three protocols because they all share
// the same shape: "given a URL + optional auth, GET it, surface the
// response body and a small set of headers (Content-Type, Content-
// Length, ETag), and let the caller persist the bytes into a
// BlobStore-backed cache".
//
// What upstream is NOT
//
//   - It is not a generic HTTP client. The exposed surface is fetch
//     (GET / HEAD) only; pushes are out of scope because no remote
//     registry contract we support permits pika to push to it.
//   - It does not own the cache. Persistence is the caller's job;
//     upstream returns a Response whose Body the caller streams into
//     its own storage. Splitting the two keeps the cache layout (Go
//     vs. NPM vs. Docker) inside the protocol packages where it
//     belongs.
//   - It does not own TTL bookkeeping. Mutable-endpoint freshness is
//     decided by each protocol head (Go @latest, NPM packument,
//     Docker manifest by tag) using the repo's MutableTTL config.
//     upstream just executes whatever request the caller gives it.
//
// What upstream IS
//
//   - One HTTP client per remote repo (reuses connections).
//   - Auth header injection from RegistryUpstreamAuth (basic /
//     bearer / header), with raw:// / config:// reference resolution.
//   - Sensible defaults (timeouts, redirect policy, retry on
//     transient errors) so every caller doesn't reinvent them.
package upstream

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/service"
)

// Errors. Wrap with sentinel so callers can match without string
// comparisons. ErrNotFound is the cache key for "upstream said 404"
// — callers usually surface that to their own clients as 404 too.
var (
	ErrNotFound     = errors.New("upstream: not found")
	ErrUnauthorized = errors.New("upstream: unauthorized")
	ErrTransient    = errors.New("upstream: transient")
)

// Response is the cooked form of an upstream HTTP response. Body is
// the streaming response body; the caller MUST Close it. The other
// fields are the small subset of headers every protocol head cares
// about; raw headers stay accessible via Header for the rare cases
// (Docker registry's Docker-Content-Digest, NPM's npm-notice) that
// need them.
type Response struct {
	StatusCode    int
	ContentType   string
	ContentLength int64
	ETag          string
	LastModified  string
	Header        http.Header
	Body          io.ReadCloser
}

// Client is the pull-through HTTP client for a single remote repo.
// Construct via NewClient; the zero value is not usable because auth
// resolution depends on the SecretResolver.
//
// One Client per (namespace, repo) pair: the underlying http.Client
// holds its own connection pool, so sharing across distinct remotes
// would force unrelated traffic to compete for sockets.
type Client struct {
	base     string // upstream base URL
	auth     *service.RegistryUpstreamAuth
	resolver registry.SecretResolver
	httpc    *http.Client
}

// Config bundles the inputs every NewClient call needs. Defined as a
// struct (instead of positional args) because Auth + Resolver are
// often nil and named fields document intent at the call site.
type Config struct {
	// BaseURL is the upstream's root URL, e.g. "https://proxy.golang.org"
	// or "https://registry.npmjs.org". Trailing slash optional; the
	// client normalises.
	BaseURL string
	// Auth, if non-nil, applies a per-request authenticator. Password /
	// token / value fields are resolved via Resolver at request time
	// so plaintext never lives in the Client struct.
	Auth *service.RegistryUpstreamAuth
	// Resolver expands raw:// and config:// references in Auth field values.
	// Nil means "no expansion" — values are used verbatim.
	Resolver registry.SecretResolver
	// InsecureSkipVerify disables TLS verification. Off by default;
	// useful only for self-signed internal mirrors.
	InsecureSkipVerify bool
	// Timeout is the per-request timeout. Default is 30s; callers
	// hosting Docker registry mirrors should raise this because layer
	// fetches can be GB-large.
	Timeout time.Duration
}

// NewClient constructs a Client. Returns an error when BaseURL is
// empty (the upstream definition is broken at the source — fail
// fast rather than wait until the first fetch).
func NewClient(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("upstream: BaseURL is empty")
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.InsecureSkipVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	return &Client{
		base:     strings.TrimRight(cfg.BaseURL, "/"),
		auth:     cfg.Auth,
		resolver: cfg.Resolver,
		httpc: &http.Client{
			Transport: tr,
			Timeout:   timeout,
		},
	}, nil
}

// BaseURL returns the normalised upstream root, useful for log lines
// that want to identify which mirror handled a request.
func (c *Client) BaseURL() string { return c.base }

// Get issues a GET against the upstream with the given relative
// path (or absolute URL when path begins with http:// or https://).
// The path is appended verbatim — callers are responsible for
// escaping. Returns a Response whose Body is open; caller must Close.
//
// Non-2xx responses are converted into typed errors:
//
//	404 → ErrNotFound
//	401/403 → ErrUnauthorized
//	5xx, network → ErrTransient (wraps original)
//	other 4xx → fmt.Errorf with the status code
func (c *Client) Get(ctx context.Context, pathOrURL string) (*Response, error) {
	return c.do(ctx, http.MethodGet, pathOrURL)
}

// Head issues a HEAD request, useful for cache freshness checks
// (Docker manifest by digest) without pulling the payload.
func (c *Client) Head(ctx context.Context, pathOrURL string) (*Response, error) {
	return c.do(ctx, http.MethodHead, pathOrURL)
}

func (c *Client) do(ctx context.Context, method, pathOrURL string) (*Response, error) {
	url := c.resolveURL(pathOrURL)
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("upstream %s %s: build request: %w", method, url, err)
	}
	if err := c.applyAuth(ctx, req); err != nil {
		return nil, fmt.Errorf("upstream %s %s: auth: %w", method, url, err)
	}

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream %s %s: %w: %w", method, url, ErrTransient, err)
	}

	switch {
	case resp.StatusCode == http.StatusNotFound:
		resp.Body.Close()
		return nil, fmt.Errorf("upstream %s %s: %w", method, url, ErrNotFound)
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		resp.Body.Close()
		return nil, fmt.Errorf("upstream %s %s status %d: %w", method, url, resp.StatusCode, ErrUnauthorized)
	case resp.StatusCode >= 500:
		resp.Body.Close()
		return nil, fmt.Errorf("upstream %s %s status %d: %w", method, url, resp.StatusCode, ErrTransient)
	case resp.StatusCode >= 400:
		resp.Body.Close()
		return nil, fmt.Errorf("upstream %s %s status %d", method, url, resp.StatusCode)
	}

	return &Response{
		StatusCode:    resp.StatusCode,
		ContentType:   resp.Header.Get("Content-Type"),
		ContentLength: resp.ContentLength,
		ETag:          resp.Header.Get("ETag"),
		LastModified:  resp.Header.Get("Last-Modified"),
		Header:        resp.Header,
		Body:          resp.Body,
	}, nil
}

// resolveURL joins the relative path against the configured base.
// Absolute URLs (rare — used by Docker registries that emit
// fully-qualified blob redirects in Location headers) are returned
// verbatim so the cache can chase them across hosts.
func (c *Client) resolveURL(pathOrURL string) string {
	if strings.HasPrefix(pathOrURL, "http://") || strings.HasPrefix(pathOrURL, "https://") {
		return pathOrURL
	}
	if !strings.HasPrefix(pathOrURL, "/") {
		pathOrURL = "/" + pathOrURL
	}
	return c.base + pathOrURL
}

// applyAuth resolves any raw:// or config:// references in the auth config
// and writes the appropriate header onto the request.
func (c *Client) applyAuth(ctx context.Context, req *http.Request) error {
	if c.auth == nil {
		return nil
	}
	switch c.auth.Type {
	case service.RegistryAuthBasic:
		password, err := c.resolveSecret(ctx, c.auth.Password)
		if err != nil {
			return fmt.Errorf("resolve basic password: %w", err)
		}
		req.SetBasicAuth(c.auth.Username, password)
	case service.RegistryAuthBearer:
		token, err := c.resolveSecret(ctx, c.auth.Token)
		if err != nil {
			return fmt.Errorf("resolve bearer token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	case service.RegistryAuthHeader:
		value, err := c.resolveSecret(ctx, c.auth.Value)
		if err != nil {
			return fmt.Errorf("resolve header value: %w", err)
		}
		req.Header.Set(c.auth.Header, value)
	default:
		return fmt.Errorf("unsupported auth type %q", c.auth.Type)
	}
	return nil
}

// resolveSecret expands supported references via the configured
// resolver. Values without a supported scheme are returned verbatim.
// The removed secret:// wrapper is rejected explicitly so old config
// does not silently become an upstream credential literal.
func (c *Client) resolveSecret(ctx context.Context, value string) (string, error) {
	if strings.HasPrefix(value, "secret://") {
		return "", fmt.Errorf("secret:// references are no longer supported; use raw://mount/path or config://key")
	}
	if c.resolver == nil || !isReference(value) {
		return value, nil
	}
	return c.resolver.ResolveSecret(ctx, value)
}

func isReference(value string) bool {
	return strings.HasPrefix(value, "raw://") || strings.HasPrefix(value, "config://")
}

// Close releases the underlying HTTP client's idle connections.
// Called by the registry runtime when a remote repo is being
// reconfigured or removed.
func (c *Client) Close() error {
	if c.httpc != nil {
		c.httpc.CloseIdleConnections()
	}
	return nil
}
