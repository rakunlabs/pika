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

// Proxy modes control how a backend selects an outbound proxy. They let
// an operator override whatever the process environment dictates, which
// matters when the platform injects HTTP_PROXY / HTTPS_PROXY globally
// but a specific external resource must reach its target differently.
const (
	// ProxyModeEnvironment honours HTTP_PROXY / HTTPS_PROXY / NO_PROXY.
	// This is the default and matches Go's stdlib behaviour.
	ProxyModeEnvironment = "environment"
	// ProxyModeNone forces a direct connection, ignoring any proxy set
	// in the environment. Use this to stop an externally-injected
	// HTTP_PROXY from capturing a resource's traffic.
	ProxyModeNone = "none"
	// ProxyModeCustom routes through the explicit proxy URL regardless
	// of the environment.
	ProxyModeCustom = "custom"
)

// resolveProxyMode applies backward-compatible defaulting. An unset (or
// unknown) mode means "custom" when a URL is present — the legacy
// single-field behaviour where a bare proxy URL implied custom routing —
// and "environment" otherwise.
func resolveProxyMode(mode, proxyURL string) string {
	switch mode {
	case ProxyModeEnvironment, ProxyModeNone, ProxyModeCustom:
		return mode
	default:
		if strings.TrimSpace(proxyURL) != "" {
			return ProxyModeCustom
		}
		return ProxyModeEnvironment
	}
}

// newHTTPClient builds the standard HTTP client used by the external
// backends, wiring the outbound proxy (per mode) and TLS config.
//
// Proxy resolution by mode:
//   - environment → http.ProxyFromEnvironment (HTTP_PROXY/HTTPS_PROXY/NO_PROXY).
//   - none        → no proxy; a direct connection even if the
//     environment defines a proxy.
//   - custom      → the parsed proxyURL via http.ProxyURL. A malformed
//     value falls back to the environment proxy (logged);
//     validateProxyConfig rejects such values at config-save time, so
//     this fallback is defensive only.
//
// The transport is cloned from http.DefaultTransport so the sensible
// stdlib defaults (dial timeouts, HTTP/2, idle-connection pooling) are
// preserved.
//
// tlsConfig may be nil (most backends); the Kubernetes client passes a
// populated config for CA / mTLS / insecure-skip-verify.
func newHTTPClient(proxyMode, proxyURL string, tlsConfig *tls.Config) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig
	}

	switch resolveProxyMode(proxyMode, proxyURL) {
	case ProxyModeNone:
		transport.Proxy = nil
	case ProxyModeCustom:
		if u, err := url.Parse(strings.TrimSpace(proxyURL)); err == nil && u.Host != "" {
			transport.Proxy = http.ProxyURL(u)
		} else {
			slog.Warn("external: invalid custom proxy URL, falling back to environment proxy",
				"proxy", proxyURL, "error", err)
			// keep the clone's default (http.ProxyFromEnvironment)
		}
	default: // ProxyModeEnvironment — the clone already uses ProxyFromEnvironment
	}

	return &http.Client{
		Timeout:   defaultExternalTimeout,
		Transport: transport,
	}
}

// validateProxy reports whether a proxy URL is well-formed. An empty
// value is valid (no URL supplied).
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

// validateProxyConfig validates a (mode, url) pair as configured on an
// external resource. Each Provider.Validate calls this so bad input is
// rejected when settings are saved rather than failing at request time.
func validateProxyConfig(mode, proxyURL string) error {
	switch mode {
	case "", ProxyModeEnvironment, ProxyModeNone:
		// URL is optional/ignored in these modes.
	case ProxyModeCustom:
		if strings.TrimSpace(proxyURL) == "" {
			return fmt.Errorf("proxy mode %q requires a proxy URL", mode)
		}
	default:
		return fmt.Errorf("unknown proxy mode %q (use %q, %q, or %q)",
			mode, ProxyModeEnvironment, ProxyModeNone, ProxyModeCustom)
	}
	// Whenever a URL is present, make sure it is well-formed.
	return validateProxy(proxyURL)
}
