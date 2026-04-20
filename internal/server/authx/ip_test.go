package authx

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseCIDRs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want int // expected count after parse
	}{
		{"empty", nil, 0},
		{"single ipv4", []string{"10.0.0.0/8"}, 1},
		{"single ipv6", []string{"::1/128"}, 1},
		{"mixed valid", []string{"10.0.0.0/8", "192.168.0.0/16"}, 2},
		{"drops invalid", []string{"10.0.0.0/8", "not-a-cidr"}, 1},
		{"trims whitespace", []string{"  10.0.0.0/8  "}, 1},
		{"ignores blanks", []string{"", "  ", "10.0.0.0/8"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseCIDRs(tc.in)
			if len(got) != tc.want {
				t.Errorf("got %d CIDRs, want %d (%v)", len(got), tc.want, got)
			}
		})
	}
}

func TestClientIP(t *testing.T) {
	cidrs := ParseCIDRs([]string{"10.0.0.0/8", "192.168.0.0/16"})

	cases := []struct {
		name           string
		remoteAddr     string
		headers        map[string]string
		trustedProxies []*net.IPNet
		want           string
	}{
		{
			name:       "no trusted proxies, ignores XFF",
			remoteAddr: "1.2.3.4:5678",
			headers:    map[string]string{"X-Forwarded-For": "9.9.9.9"},
			want:       "1.2.3.4",
		},
		{
			name:           "trusted peer, honors XFF first hop",
			remoteAddr:     "10.0.0.5:443",
			headers:        map[string]string{"X-Forwarded-For": "203.0.113.7, 10.0.0.5"},
			trustedProxies: cidrs,
			want:           "203.0.113.7",
		},
		{
			name:           "trusted peer, X-Real-IP takes precedence over XFF",
			remoteAddr:     "10.0.0.5:443",
			headers:        map[string]string{"X-Real-IP": "198.51.100.1", "X-Forwarded-For": "203.0.113.7"},
			trustedProxies: cidrs,
			want:           "198.51.100.1",
		},
		{
			name:           "trusted peer, True-Client-IP wins over X-Real-IP",
			remoteAddr:     "10.0.0.5:443",
			headers:        map[string]string{"True-Client-IP": "100.64.0.1", "X-Real-IP": "198.51.100.1"},
			trustedProxies: cidrs,
			want:           "100.64.0.1",
		},
		{
			name:           "untrusted peer ignores XFF even when CIDRs configured",
			remoteAddr:     "8.8.8.8:443",
			headers:        map[string]string{"X-Forwarded-For": "203.0.113.7"},
			trustedProxies: cidrs,
			want:           "8.8.8.8",
		},
		{
			name:           "trusted peer, no headers — falls back to peer",
			remoteAddr:     "10.0.0.5:443",
			trustedProxies: cidrs,
			want:           "10.0.0.5",
		},
		{
			name:           "trusted peer, malformed XFF falls back to peer",
			remoteAddr:     "10.0.0.5:443",
			headers:        map[string]string{"X-Forwarded-For": "not-an-ip"},
			trustedProxies: cidrs,
			want:           "10.0.0.5",
		},
		{
			name:       "no remote addr returns empty",
			remoteAddr: "",
			want:       "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			got := ClientIP(req, tc.trustedProxies)
			if got != tc.want {
				t.Errorf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}
