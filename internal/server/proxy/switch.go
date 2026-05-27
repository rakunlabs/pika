package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/rakunlabs/ada"
)

// DefaultSwitchID is the source_handle reserved for the "no rule
// matched" branch every switch node ships with. The frontend uses
// the same constant; renaming it requires a co-ordinated change.
const DefaultSwitchID = "default"

// SwitchConfig is the raw user configuration stored on a switch
// node. Each rule corresponds to ONE output handle on the canvas
// (source_handle == rule.ID). Ordering is significant: rules are
// matched top-to-bottom, the first hit wins, and the "default"
// branch fires only when no rule matched.
//
// Multiple matchers inside the same rule are AND'd: every non-zero
// field must match for the rule to fire.
type SwitchConfig struct {
	Rules []SwitchRule `json:"rules,omitempty"`
}

// SwitchRule is one row in the operator's switch table. Every
// field is optional except ID; an empty rule matches everything,
// which is occasionally what an operator wants for "send the rest
// over here" before the default branch.
type SwitchRule struct {
	// ID is the stable string the frontend embeds in
	// edge.source_handle when the operator wires the rule's
	// output to a downstream node. The frontend generates it
	// (typically "rule-<random>"); we accept whatever it gives.
	ID string `json:"id"`

	// Label is purely cosmetic — shown on the node card and in
	// compile error messages.
	Label string `json:"label,omitempty"`

	// Host is matched against r.Host. An exact match wins; a
	// leading "*." enables suffix matching ("*.example.com"
	// matches "api.example.com" but NOT "example.com"). Empty
	// means "host does not participate".
	Host string `json:"host,omitempty"`

	// CIDRs is the set of allowed source networks (in addition
	// to a Host filter, AND'd). A bare IP is treated as /32 or
	// /128. Empty slice means "source IP does not participate".
	CIDRs []string `json:"cidrs,omitempty"`

	// Path is the ada/mux pattern: "/foo", "/foo/*", "/api/{id}".
	// Empty defaults to "/*" so a host-only rule still has an
	// entry in the mux.
	Path string `json:"path,omitempty"`

	// Methods restricts the rule to specific HTTP methods. Empty
	// means "every method".
	Methods []string `json:"methods,omitempty"`

	// Headers is a name->value map; every entry must match
	// (header present AND value equal) for the rule to fire.
	// Header lookup is case-insensitive (net/http normalises
	// keys); value comparison is case-sensitive.
	Headers map[string]string `json:"headers,omitempty"`

	// Query is a key->value map with the same AND semantics as
	// Headers, against url.Query().
	Query map[string]string `json:"query,omitempty"`
}

// KindSwitch / KindHandler / KindMiddleware are the three values
// NodeSpec.Kind can take. Kept as string constants (rather than a
// typed enum) so they JSON-marshal naturally into the catalog the
// frontend consumes.
const (
	KindMiddleware = "middleware"
	KindHandler    = "handler"
	KindSwitch     = "switch"
)

// DefaultSwitches returns the registered switch kinds. There is
// only one today, but the registry pattern matches DefaultMiddlewares
// / DefaultHandlers so the catalog stays uniform.
func DefaultSwitches() map[string]NodeSpec {
	return map[string]NodeSpec{
		"switch": {
			Kind:        KindSwitch,
			Subtype:     "switch",
			Label:       "Switch",
			Description: "Route by host, source IP, path, method, header or query parameter. Rules are tried top-down; the first match wins; the mandatory 'default' branch handles everything else.",
			Build:       buildSwitch,
		},
		"js-branch": {
			Kind:    KindSwitch,
			Subtype: "js-branch",
			Label:   "JS branch (goja)",
			// Note for readers familiar with chore: chore evaluates ifCase
			// against a stateful input pool and waits for every active
			// upstream input before firing. Pika proxy is stateless and
			// per-request: each branch is its own sub-pipeline, the chosen
			// branch runs in isolation, and there is no fan-in waiting.
			Description: "Run user JavaScript and pick an output handle with ctx.choose(\"id\"). Each handle is an operator-defined branch wired on the canvas; the mandatory 'default' branch fires when the script chooses nothing or an unknown id. Branches do not fan-in — each one runs as its own sub-pipeline.",
			Build:       buildJSBranch,
		},
	}
}

// buildSwitch is the NodeBuilder for the switch node. It is the only
// builder in the codebase that consumes BranchSet — each rule's
// downstream sub-pipeline arrives pre-compiled under the matching
// rule ID, plus a "default" entry for the fallback branch.
//
// Behaviour at request time:
//
//  1. The rules list is partitioned into host/IP groups in input
//     order. Two consecutive rules with the same (host, cidrs)
//     tuple share an ada.Mux instance so path+method matching is
//     a single trie walk for that group.
//  2. Each group is tried in order. The first whose host/IP
//     predicate matches the request is "the owner" — its mux gets
//     the request. If the mux finds no path/method match the
//     request gets a 404 from that group; we do NOT continue to
//     later groups. This matches the operator's mental model:
//     "if I pick the api.example.com group, that group decides".
//  3. If no group claims the request, the default branch runs.
//
// Header / query matching cannot live in the mux (the mux trie
// keys off path+method only). Rules carrying header/query
// predicates wrap their per-rule branch handler with a "match or
// 404" check that runs after the mux trie has found this rule —
// i.e. path+method already lined up, the extra predicates are the
// final gate.
func buildSwitch(raw json.RawMessage, _ ServiceDeps, branches BranchSet) (Middleware, error) {
	var cfg SwitchConfig
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("switch config: %w", err)
		}
	}
	if branches == nil {
		return nil, errors.New("switch: branches argument is required")
	}

	// Validate ALL rule IDs have a matching branch before we build
	// anything. A missing branch is a config error (operator wired
	// rules without dragging an output edge); a missing default is
	// also fatal because we have no sensible fallback.
	defaultMW, ok := branches[DefaultSwitchID]
	if !ok {
		return nil, errors.New("switch: missing 'default' branch")
	}
	for _, r := range cfg.Rules {
		if r.ID == "" {
			return nil, errors.New("switch: rule with empty id")
		}
		if r.ID == DefaultSwitchID {
			return nil, fmt.Errorf("switch: rule id %q is reserved", DefaultSwitchID)
		}
		if _, ok := branches[r.ID]; !ok {
			return nil, fmt.Errorf("switch: rule %q has no branch wired", r.ID)
		}
	}

	groups, err := buildSwitchGroups(cfg.Rules, branches)
	if err != nil {
		return nil, err
	}

	// The switch's "next" argument is ignored — it is terminal in
	// the sense that every request eventually lands on either a
	// matched branch or the default branch. Each branch was
	// already composed with its own next chain (terminal handler
	// at the end) by Compile.
	return func(_ http.Handler) http.Handler {
		// Default branch terminates at the handler the operator
		// wired to source_handle="default"; calling it with a
		// nil next is fine because every branch ends in a
		// handler which discards next.
		defaultHandler := defaultMW(nil)

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, g := range groups {
				if !g.matchesHostIP(r) {
					continue
				}
				// Claim the request — let this group's mux
				// decide. Mux miss returns 404 from the mux's
				// own NotFound handler; we don't fall through.
				g.mux.ServeHTTP(w, r)
				return
			}
			defaultHandler.ServeHTTP(w, r)
		})
	}, nil
}

// switchGroup is one row in the switch's runtime structure: a host /
// CIDR predicate plus a pre-built mux holding every rule that shares
// the same predicate.
type switchGroup struct {
	host       string // verbatim user input
	hostExact  string // host without leading "*." when hostSuffix
	hostSuffix bool   // true when host began with "*."
	nets       []*net.IPNet
	mux        *ada.Mux
}

// buildSwitchGroups partitions rules by (host, cidrs) in input order
// and constructs one ada.Mux per group. It is the heart of the
// "shared mux for shared predicates" optimisation: an operator with
// 20 rules under api.example.com pays a single trie walk to find
// the matching path, not 20 linear comparisons.
func buildSwitchGroups(rules []SwitchRule, branches BranchSet) ([]*switchGroup, error) {
	var groups []*switchGroup
	var cur *switchGroup
	var curKey string // grouping key derived from (host, cidrs)

	for i, rule := range rules {
		key := groupKey(rule.Host, rule.CIDRs)
		if cur == nil || curKey != key {
			g, err := newSwitchGroup(rule.Host, rule.CIDRs)
			if err != nil {
				return nil, fmt.Errorf("rule %d (%q): %w", i, rule.Label, err)
			}
			groups = append(groups, g)
			cur = g
			curKey = key
		}
		if err := registerRule(cur.mux, rule, branches); err != nil {
			return nil, fmt.Errorf("rule %d (%q): %w", i, rule.Label, err)
		}
	}
	return groups, nil
}

// groupKey collapses (host, cidrs) into a stable string used only
// for partitioning. CIDRs are NOT sorted on purpose: an operator
// who writes [10.0.0.0/8, 192.168/16] in one rule and
// [192.168/16, 10.0.0.0/8] in the next ALMOST CERTAINLY meant
// different groups (different priority semantics). Treating them
// as one group would silently fold two rules into a single mux.
func groupKey(host string, cidrs []string) string {
	return host + "\x00" + strings.Join(cidrs, ",")
}

func newSwitchGroup(host string, cidrs []string) (*switchGroup, error) {
	g := &switchGroup{host: host}
	if strings.HasPrefix(host, "*.") {
		g.hostSuffix = true
		g.hostExact = host[2:]
	} else {
		g.hostExact = host
	}
	for _, c := range cidrs {
		n, err := parseCIDROrIP(c)
		if err != nil {
			return nil, fmt.Errorf("cidr %q: %w", c, err)
		}
		g.nets = append(g.nets, n)
	}
	g.mux = ada.NewMux()
	return g, nil
}

// parseCIDROrIP accepts both "10.0.0.0/8" and the loose "10.0.0.5"
// shorthand, expanding the latter to a /32 (or /128) net so the
// runtime check uses a single ParseCIDR result type.
func parseCIDROrIP(s string) (*net.IPNet, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty cidr")
	}
	if strings.Contains(s, "/") {
		_, n, err := net.ParseCIDR(s)
		return n, err
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, fmt.Errorf("not a valid ip or cidr: %q", s)
	}
	bits := 32
	if ip.To4() == nil {
		bits = 128
	}
	mask := net.CIDRMask(bits, bits)
	return &net.IPNet{IP: ip.Mask(mask), Mask: mask}, nil
}

// registerRule attaches one rule's branch handler to a group's mux.
// Path defaults to "/*" (catch-all) when empty; method "" registers
// under every method (ada/mux convention).
//
// When the rule carries header / query predicates the branch handler
// is wrapped: the wrapper runs AFTER the mux has decided this rule
// matches by path+method, and writes 404 if the extra predicates
// fail. We deliberately do not fall through to other rules — once
// the mux picked us, this branch is responsible for the outcome.
func registerRule(mux *ada.Mux, rule SwitchRule, branches BranchSet) error {
	mw, ok := branches[rule.ID]
	if !ok {
		return fmt.Errorf("branch %q missing", rule.ID)
	}
	// Each rule's compiled chain is rooted at its first downstream
	// node. Pass nil as next because the chain terminates in a
	// handler that discards next anyway.
	handler := mw(nil)
	if needsPostFilter(rule) {
		handler = wrapHeaderQueryFilter(handler, rule)
	}

	path := rule.Path
	if path == "" {
		path = "/*"
	}
	// Convert "/foo/*" into ada's "HandleFuncWildcard" form
	// (anything ending in "*" is a trailing wildcard). For exact
	// matches we go through HandleWithMethod directly.
	isWildcard := strings.HasSuffix(path, "/*")
	if isWildcard {
		path = strings.TrimSuffix(path, "*") // ada appends one
	}

	register := func(method string) {
		if isWildcard {
			mux.HandleFuncWildcard(path, handler.ServeHTTP)
		} else {
			mux.HandleWithMethod(method, path, handler.ServeHTTP)
		}
	}

	if len(rule.Methods) == 0 {
		// "any method" in ada's API is method="" through HandleFunc.
		// For wildcard paths we go through HandleFuncWildcard which
		// internally calls HandleFunc with method "".
		register("")
		return nil
	}
	for _, m := range rule.Methods {
		register(strings.ToUpper(strings.TrimSpace(m)))
	}
	return nil
}

func needsPostFilter(r SwitchRule) bool {
	return len(r.Headers) > 0 || len(r.Query) > 0
}

// wrapHeaderQueryFilter installs a post-mux predicate. The mux
// already confirmed path+method; we now check headers and query
// params. A miss writes 404 — control does NOT fall through to
// another mux group because the design says "the group that
// matched host/IP owns the request".
func wrapHeaderQueryFilter(h http.Handler, rule SwitchRule) http.Handler {
	headers := rule.Headers
	query := rule.Query
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			if r.Header.Get(k) != v {
				http.NotFound(w, r)
				return
			}
		}
		if len(query) > 0 {
			q := r.URL.Query()
			for k, v := range query {
				if q.Get(k) != v {
					http.NotFound(w, r)
					return
				}
			}
		}
		h.ServeHTTP(w, r)
	})
}

// matchesHostIP returns true when the request's Host header and
// source IP satisfy the group's predicate. An empty predicate
// (no host AND no CIDRs) matches every request — useful for the
// "trailing catch-all rule before default" pattern.
func (g *switchGroup) matchesHostIP(r *http.Request) bool {
	if g.host != "" {
		// Host header includes the port for explicit URLs;
		// strip it so the operator can write "example.com"
		// without worrying about :443 / :80.
		h := r.Host
		if idx := strings.IndexByte(h, ':'); idx >= 0 {
			h = h[:idx]
		}
		if g.hostSuffix {
			if !strings.HasSuffix(h, "."+g.hostExact) {
				return false
			}
		} else if h != g.hostExact {
			return false
		}
	}
	if len(g.nets) > 0 {
		ip := remoteIP(r)
		if ip == nil {
			return false
		}
		hit := false
		for _, n := range g.nets {
			if n.Contains(ip) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// remoteIP pulls the request peer address out of RemoteAddr. We
// intentionally do NOT look at X-Forwarded-For here — trusting it
// on a proxy listener would let any client spoof their address.
// Operators who terminate behind a trusted reverse proxy can install
// a trusted-proxy middleware upstream of the switch that rewrites
// RemoteAddr for them.
func remoteIP(r *http.Request) net.IP {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return net.ParseIP(host)
}
