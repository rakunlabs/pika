package authx

import (
	"context"
	"encoding/json"
	"net"
	"net/http"

	"github.com/rakunlabs/ada/middleware/auth/identity"
	"github.com/rakunlabs/ada/middleware/auth/strategy"
	authheader "github.com/rakunlabs/ada/middleware/auth/strategy/header"

	"github.com/rakunlabs/pika/internal/service"
)

// BuildHeader constructs the header strategy from settings. Returns nil when
// settings are absent. When TrustedProxies is non-empty, wraps the strategy
// with a CIDR guard that rejects requests from untrusted source IPs.
func BuildHeader(s *service.HeaderStrategySettings) strategy.Authenticator {
	if s == nil {
		return nil
	}
	name := s.Name
	if name == "" {
		name = "header"
	}

	m := authheader.HeaderMap{
		User:   s.User,
		Email:  s.Email,
		Name:   s.DisplayName,
		Roles:  s.Roles,
		Groups: s.Groups,
	}

	base := authheader.New(name, authheader.WithHeaderMap(m))
	if len(s.TrustedProxies) == 0 {
		return base
	}
	return &trustedProxiesStrategy{inner: base, cidrs: parseCIDRs(s.TrustedProxies)}
}

type trustedProxiesStrategy struct {
	inner strategy.Authenticator
	cidrs []*net.IPNet
}

func (t *trustedProxiesStrategy) Name() string { return t.inner.Name() }
func (t *trustedProxiesStrategy) Descriptor() strategy.Descriptor {
	return t.inner.Descriptor()
}

func (t *trustedProxiesStrategy) Login(w http.ResponseWriter, r *http.Request) (*identity.Identity, strategy.Outcome, error) {
	if !t.trusted(r) {
		writeJSONErr(w, http.StatusForbidden, "untrusted_proxy", "request from untrusted source")
		return nil, strategy.OutcomeFailed, nil
	}
	return t.inner.Login(w, r)
}

func (t *trustedProxiesStrategy) Logout(ctx context.Context, id *identity.Identity) error {
	return t.inner.Logout(ctx, id)
}

func (t *trustedProxiesStrategy) trusted(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, c := range t.cidrs {
		if c.Contains(ip) {
			return true
		}
	}
	return false
}

// wrapTrustedProxies is a test helper — exposes the CIDR check as an
// http.Handler wrapper for unit-test isolation.
func wrapTrustedProxies(next http.Handler, cidrs []string) http.Handler {
	parsed := parseCIDRs(cidrs)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(parsed) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if host == "" {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip != nil {
			for _, c := range parsed {
				if c.Contains(ip) {
					next.ServeHTTP(w, r)
					return
				}
			}
		}
		w.WriteHeader(http.StatusForbidden)
	})
}

func parseCIDRs(raw []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(raw))
	for _, s := range raw {
		_, n, err := net.ParseCIDR(s)
		if err == nil {
			out = append(out, n)
		}
	}
	return out
}

func writeJSONErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": msg})
}
