package external

import (
	"net/http"
	"testing"
)

func TestResolveProxyMode(t *testing.T) {
	cases := []struct {
		name   string
		mode   string
		url    string
		expect string
	}{
		{"explicit environment", ProxyModeEnvironment, "", ProxyModeEnvironment},
		{"explicit none", ProxyModeNone, "", ProxyModeNone},
		{"explicit custom", ProxyModeCustom, "http://p:8080", ProxyModeCustom},
		{"empty mode no url defaults to environment", "", "", ProxyModeEnvironment},
		{"empty mode with url defaults to custom", "", "http://p:8080", ProxyModeCustom},
		{"unknown mode no url defaults to environment", "bogus", "", ProxyModeEnvironment},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveProxyMode(tc.mode, tc.url); got != tc.expect {
				t.Fatalf("resolveProxyMode(%q,%q) = %q, want %q", tc.mode, tc.url, got, tc.expect)
			}
		})
	}
}

func TestNewHTTPClientProxySelection(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)

	t.Run("none forces direct", func(t *testing.T) {
		// Even with an environment proxy set, "none" must not proxy.
		t.Setenv("HTTP_PROXY", "http://env-proxy:3128")
		t.Setenv("HTTPS_PROXY", "http://env-proxy:3128")
		c := newHTTPClient(ProxyModeNone, "", nil)
		tr := c.Transport.(*http.Transport)
		if tr.Proxy != nil {
			t.Fatalf("expected nil Proxy for mode none, got non-nil")
		}
	})

	t.Run("custom uses the configured url", func(t *testing.T) {
		c := newHTTPClient(ProxyModeCustom, "http://custom-proxy:8080", nil)
		tr := c.Transport.(*http.Transport)
		if tr.Proxy == nil {
			t.Fatal("expected non-nil Proxy for mode custom")
		}
		u, err := tr.Proxy(req)
		if err != nil {
			t.Fatalf("Proxy func error: %v", err)
		}
		if u == nil || u.Host != "custom-proxy:8080" {
			t.Fatalf("expected proxy host custom-proxy:8080, got %v", u)
		}
	})

	t.Run("environment honours env proxy", func(t *testing.T) {
		t.Setenv("HTTP_PROXY", "http://env-proxy:3128")
		c := newHTTPClient(ProxyModeEnvironment, "", nil)
		tr := c.Transport.(*http.Transport)
		if tr.Proxy == nil {
			t.Fatal("expected non-nil Proxy for mode environment")
		}
		// Use a plain http request so HTTP_PROXY applies.
		hreq, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
		u, err := tr.Proxy(hreq)
		if err != nil {
			t.Fatalf("Proxy func error: %v", err)
		}
		if u == nil || u.Host != "env-proxy:3128" {
			t.Fatalf("expected env proxy host env-proxy:3128, got %v", u)
		}
	})

	t.Run("invalid custom url falls back to environment", func(t *testing.T) {
		// A malformed custom URL must not panic; it falls back to env.
		c := newHTTPClient(ProxyModeCustom, "://bad", nil)
		tr := c.Transport.(*http.Transport)
		if tr.Proxy == nil {
			t.Fatal("expected non-nil Proxy (env fallback) for invalid custom url")
		}
	})
}

func TestValidateProxyConfig(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		url     string
		wantErr bool
	}{
		{"environment empty ok", ProxyModeEnvironment, "", false},
		{"none empty ok", ProxyModeNone, "", false},
		{"custom requires url", ProxyModeCustom, "", true},
		{"custom valid url ok", ProxyModeCustom, "http://p:8080", false},
		{"custom socks5 ok", ProxyModeCustom, "socks5://p:1080", false},
		{"custom bad scheme", ProxyModeCustom, "ftp://p:21", true},
		{"unknown mode", "weird", "", true},
		{"empty mode bad url", "", "http://[::1", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateProxyConfig(tc.mode, tc.url)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for (%q,%q), got nil", tc.mode, tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for (%q,%q): %v", tc.mode, tc.url, err)
			}
		})
	}
}
