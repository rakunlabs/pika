// Package netguard restricts which IP addresses outbound requests to
// operator-configured URLs (webhooks, HTTP inheritance) may reach.
//
// The check runs in the dialer's Control hook, i.e. on the resolved IP
// of every connection, so DNS tricks and redirects can't bypass it.
// When a proxy is in use the check applies to the proxy's address.
package netguard

import (
	"fmt"
	"net"
	"net/netip"
	"sync/atomic"
	"syscall"
	"time"
)

// DefaultDenyCIDRs blocks link-local ranges, which include the cloud
// instance metadata endpoint (169.254.169.254).
var DefaultDenyCIDRs = []string{"169.254.0.0/16", "fe80::/10"}

// Policy decides whether an address may be dialed. Allow wins over Deny.
type Policy struct {
	Allow []netip.Prefix
	Deny  []netip.Prefix
}

// ErrBlocked is returned (wrapped) when a dial targets a denied address.
type ErrBlocked struct {
	Addr netip.Addr
}

func (e *ErrBlocked) Error() string {
	return fmt.Sprintf("outbound connection to %s is blocked by server.outbound policy", e.Addr)
}

// NewPolicy parses CIDR (or single IP) strings.
func NewPolicy(allow, deny []string) (*Policy, error) {
	a, err := parsePrefixes(allow)
	if err != nil {
		return nil, fmt.Errorf("allow_cidrs: %w", err)
	}
	d, err := parsePrefixes(deny)
	if err != nil {
		return nil, fmt.Errorf("deny_cidrs: %w", err)
	}
	return &Policy{Allow: a, Deny: d}, nil
}

func parsePrefixes(in []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR or IP %q", s)
		}
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// Allowed reports whether addr may be dialed.
func (p *Policy) Allowed(addr netip.Addr) bool {
	if p == nil {
		return true
	}
	addr = addr.Unmap()
	for _, pr := range p.Allow {
		if pr.Contains(addr) {
			return true
		}
	}
	for _, pr := range p.Deny {
		if pr.Contains(addr) {
			return false
		}
	}
	return true
}

var current atomic.Pointer[Policy]

func init() {
	p, _ := NewPolicy(nil, DefaultDenyCIDRs)
	current.Store(p)
}

// SetPolicy replaces the process-wide policy. nil allows everything.
func SetPolicy(p *Policy) {
	current.Store(p)
}

// Current returns the process-wide policy.
func Current() *Policy {
	return current.Load()
}

// control is a net.Dialer Control hook enforcing the current policy.
func control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("netguard: unexpected dial address %q", address)
	}
	if !Current().Allowed(addr) {
		return &ErrBlocked{Addr: addr}
	}
	return nil
}

// Dialer returns a net.Dialer that enforces the process-wide policy,
// with the same timeouts as http.DefaultTransport.
func Dialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   control,
	}
}
