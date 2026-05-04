// Package ldapclient is pika's concrete implementation of the
// ada/middleware/auth/strategy/ldap.Connector interface, plus the
// equivalent primitives used by the user-sync engine. One package
// serves both the per-request login path and the periodic batch
// path so credentials, TLS settings, and connection logic live in
// exactly one place.
package ldapclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	goldap "github.com/go-ldap/ldap/v3"
	adaldap "github.com/rakunlabs/ada/middleware/auth/strategy/ldap"
)

// Config carries everything needed to connect to and bind against an
// LDAP server. Mirrors the relevant fields of service.LDAPStrategySettings
// so the two are interchangeable but the package itself has no service-
// layer dependency (avoids an import cycle: service -> authx -> ldapclient,
// usersync -> ldapclient, both fine).
type Config struct {
	// Address is "host:port" or a full ldap://host:port / ldaps://host:port URL.
	Address string
	// TLS, when true, dials with ldaps:// (or upgrades a plain dial via StartTLS
	// if the address is an ldap:// URL).
	TLS bool
	// InsecureSkip skips certificate verification when TLS is on. Only for
	// dev/lab installs.
	InsecureSkip bool
	// Timeout bounds the dial and any single LDAP operation. Zero means 10s.
	Timeout time.Duration
}

// Connector implements adaldap.Connector. The same instance is also used
// directly by the sync engine via NewConn.
type Connector struct {
	cfg Config
}

// New returns a Connector with the given config.
func New(cfg Config) *Connector {
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	return &Connector{cfg: cfg}
}

// Connect opens a fresh LDAP connection. The returned Conn satisfies both
// adaldap.Conn (used by the login strategy) and the SearchPaged method
// used by the sync engine.
func (c *Connector) Connect(ctx context.Context) (adaldap.Conn, error) {
	conn, err := c.NewConn(ctx)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// NewConn is the concrete-typed accessor used by sync code that needs more
// than the adaldap.Conn surface (e.g. paged search).
func (c *Connector) NewConn(ctx context.Context) (*Conn, error) {
	addr := c.cfg.Address
	if addr == "" {
		return nil, errors.New("ldapclient: empty address")
	}

	// Normalize address: accept "host:port", "ldap://...", "ldaps://..."
	scheme := ""
	if strings.HasPrefix(addr, "ldap://") {
		scheme = "ldap"
	} else if strings.HasPrefix(addr, "ldaps://") {
		scheme = "ldaps"
	}

	dialURL := addr
	if scheme == "" {
		// Bare host:port — pick scheme from TLS flag.
		if c.cfg.TLS {
			dialURL = "ldaps://" + addr
		} else {
			dialURL = "ldap://" + addr
		}
	}

	var opts []goldap.DialOpt
	opts = append(opts, goldap.DialWithDialer(&net.Dialer{Timeout: c.cfg.Timeout}))
	if c.cfg.TLS || scheme == "ldaps" {
		opts = append(opts, goldap.DialWithTLSConfig(&tls.Config{
			InsecureSkipVerify: c.cfg.InsecureSkip, //nolint:gosec // documented opt-in
		}))
	}

	l, err := goldap.DialURL(dialURL, opts...)
	if err != nil {
		return nil, fmt.Errorf("ldap dial %q: %w", dialURL, err)
	}
	l.SetTimeout(c.cfg.Timeout)

	// StartTLS path: explicit TLS=true with an ldap:// URL upgrades the
	// plaintext connection rather than reaching for ldaps://.
	if scheme == "ldap" && c.cfg.TLS {
		if err := l.StartTLS(&tls.Config{InsecureSkipVerify: c.cfg.InsecureSkip}); err != nil { //nolint:gosec
			_ = l.Close()
			return nil, fmt.Errorf("starttls: %w", err)
		}
	}

	return &Conn{l: l}, nil
}

// Conn wraps a single go-ldap connection and adapts it to the adaldap.Conn
// interface (Bind/Search/Close).
type Conn struct {
	l *goldap.Conn
}

// Bind authenticates against the directory.
func (c *Conn) Bind(dn, password string) error {
	if dn == "" {
		// Anonymous bind. go-ldap rejects empty-DN bind by default; use
		// UnauthenticatedBind to make intent explicit.
		return c.l.UnauthenticatedBind("")
	}
	return c.l.Bind(dn, password)
}

// Search performs a one-shot subtree search and returns ada-shaped Entry
// values. Used by the login flow.
func (c *Conn) Search(baseDN, filter string, attributes []string) ([]adaldap.Entry, error) {
	req := goldap.NewSearchRequest(
		baseDN,
		goldap.ScopeWholeSubtree,
		goldap.NeverDerefAliases,
		0, 0, false, // sizeLimit, timeLimit, typesOnly
		filter,
		attributes,
		nil,
	)
	res, err := c.l.Search(req)
	if err != nil {
		return nil, err
	}
	return toAdaEntries(res.Entries), nil
}

// SearchAll performs a paged subtree search, accumulating every result.
// Used by the sync engine (which often needs thousands of entries that
// trip server-side size limits without paging).
func (c *Conn) SearchAll(baseDN, filter string, attributes []string, pageSize uint32) ([]adaldap.Entry, error) {
	if pageSize == 0 {
		pageSize = 500
	}
	req := goldap.NewSearchRequest(
		baseDN,
		goldap.ScopeWholeSubtree,
		goldap.NeverDerefAliases,
		0, 0, false,
		filter,
		attributes,
		nil,
	)
	res, err := c.l.SearchWithPaging(req, pageSize)
	if err != nil {
		return nil, err
	}
	return toAdaEntries(res.Entries), nil
}

// Close terminates the connection.
func (c *Conn) Close() error { return c.l.Close() }

func toAdaEntries(in []*goldap.Entry) []adaldap.Entry {
	out := make([]adaldap.Entry, len(in))
	for i, e := range in {
		attrs := make(map[string][]string, len(e.Attributes))
		for _, a := range e.Attributes {
			attrs[a.Name] = a.Values
		}
		out[i] = adaldap.Entry{DN: e.DN, Attributes: attrs}
	}
	return out
}

// Ensure context is referenced — go-ldap doesn't accept a ctx-aware dial,
// so the timeout is the only deadline mechanism we have.
var _ = context.Background
