package authx

import (
	"net"
	"net/http"
	"strings"
)

// Standard request headers for client IP forwarding. The order below is the
// precedence checked when XFF is trusted (most-specific first).
const (
	headerTrueClientIP  = "True-Client-IP"
	headerXRealIP       = "X-Real-IP"
	headerXForwardedFor = "X-Forwarded-For"
)

// ClientIP returns the best-effort client IP for r. When trustedProxies is
// non-empty AND the request's immediate peer (RemoteAddr) is in one of those
// CIDRs, the function honors True-Client-IP, X-Real-IP, and X-Forwarded-For
// (first hop) in that order. Otherwise it returns the immediate peer's IP
// from RemoteAddr — XFF and friends are forgeable when set by an untrusted
// upstream and must not be trusted blindly.
//
// Returns "" only when RemoteAddr is unparseable.
func ClientIP(r *http.Request, trustedProxies []*net.IPNet) string {
	peer := remoteHost(r)
	if peer == "" {
		return ""
	}

	if !isTrustedPeer(peer, trustedProxies) {
		return peer
	}

	if v := strings.TrimSpace(r.Header.Get(headerTrueClientIP)); v != "" {
		if ip := net.ParseIP(v); ip != nil {
			return v
		}
	}
	if v := strings.TrimSpace(r.Header.Get(headerXRealIP)); v != "" {
		if ip := net.ParseIP(v); ip != nil {
			return v
		}
	}
	if xff := r.Header.Get(headerXForwardedFor); xff != "" {
		// First hop is the originating client; subsequent entries are
		// intermediate proxies.
		first, _, _ := strings.Cut(xff, ",")
		first = strings.TrimSpace(first)
		if ip := net.ParseIP(first); ip != nil {
			return first
		}
	}
	return peer
}

// remoteHost extracts the host portion of r.RemoteAddr (stripping the port).
// When RemoteAddr lacks a port (some test setups) the value is returned as
// is. A failed parse yields "".
func remoteHost(r *http.Request) string {
	if r.RemoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// httptest.NewRequest uses "192.0.2.1:1234" which parses fine;
		// some custom callers pass a bare IP. Tolerate both.
		if net.ParseIP(r.RemoteAddr) != nil {
			return r.RemoteAddr
		}
		return ""
	}
	return host
}

// isTrustedPeer reports whether ip falls within any of the trustedProxies
// CIDRs. An empty trustedProxies list disables trust entirely (returns
// false), forcing ClientIP to ignore forwarding headers.
func isTrustedPeer(ip string, trustedProxies []*net.IPNet) bool {
	if len(trustedProxies) == 0 {
		return false
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, n := range trustedProxies {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}

// ParseCIDRs parses a slice of CIDR strings, silently dropping any that fail
// to parse. Returns an empty slice when raw is empty or every entry is
// malformed. Useful for boot-time settings where individual bad entries
// should not prevent the server from starting.
func ParseCIDRs(raw []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		_, n, err := net.ParseCIDR(s)
		if err == nil {
			out = append(out, n)
		}
	}
	return out
}
