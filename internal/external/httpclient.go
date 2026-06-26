package external

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultExternalTimeout is the request timeout shared by every
// hand-rolled external backend client.
const defaultExternalTimeout = 30 * time.Second

// newHTTPClient builds the standard HTTP client used by the external
// backends, wiring an optional outbound proxy and TLS config.
//
// Proxy resolution:
//   - proxy == ""  → http.ProxyFromEnvironment, preserving the
//     historical behaviour where HTTP_PROXY / HTTPS_PROXY / NO_PROXY
//     were honoured implicitly via http.DefaultTransport.
//   - proxy != ""  → the parsed URL via http.ProxyURL. A malformed
//     value falls back to the environment proxy (logged) rather than
//     silently dropping it; validateProxy rejects such values at
//     config-save time, so this fallback is defensive only.
//
// The transport is cloned from http.DefaultTransport so the sensible
// stdlib defaults (dial timeouts, HTTP/2, idle-connection pooling) are
// preserved — the previous per-backend `&http.Client{Timeout: ...}`
// relied on those implicitly via the default transport.
//
// tlsConfig may be nil (most backends); the Kubernetes client passes a
// populated config for CA / mTLS / insecure-skip-verify.
func newHTTPClient(proxy string, tlsConfig *tls.Config) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig
	}
	if p := strings.TrimSpace(proxy); p != "" {
		if proxyURL, err := url.Parse(p); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		} else {
			slog.Warn("external: invalid proxy URL, falling back to environment proxy",
				"proxy", p, "error", err)
		}
	}

	return &http.Client{
		Timeout:   defaultExternalTimeout,
		Transport: transport,
	}
}

// validateProxy reports whether a configured proxy URL is well-formed.
// An empty value is valid and means "use the environment proxy". Each
// Provider.Validate calls this so a bad proxy is rejected when settings
// are saved instead of failing later at request time.
func validateProxy(proxy string) error {
	p := strings.TrimSpace(proxy)
	if p == "" {
		return nil
	}
	u, err := url.Parse(p)
	if err != nil {
		return fmt.Errorf("invalid proxy URL %q: %w", p, err)
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
		// supported by net/http's transport
	case "":
		return fmt.Errorf("proxy URL %q is missing a scheme (e.g. http://host:port)", p)
	default:
		return fmt.Errorf("unsupported proxy scheme %q (use http, https, or socks5)", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("proxy URL %q is missing a host", p)
	}
	return nil
}
