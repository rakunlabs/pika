package service

import (
	"context"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Capabilities is the frozen capability-key set resolved for a request.
type Capabilities []string

// Has reports whether the set contains the given capability key.
func (c Capabilities) Has(key string) bool {
	for _, k := range c {
		if k == key {
			return true
		}
	}
	return false
}

// CapabilityPatterns maps a capability key to its path-glob restrictions.
// A key absent from the map (or with an empty slice) is unrestricted —
// matches any path. A key with one or more entries is allowed only on
// paths matching at least one of the doublestar patterns.
type CapabilityPatterns map[string][]string

// Allows reports whether the given path is permitted for the given
// capability key under these pattern restrictions.
//
// Rules:
//   - empty path → unscoped check, always allowed (caller is using the
//     pattern-blind variant; the per-key `Has` check already passed)
//   - no patterns for the key → allowed
//   - any pattern matches → allowed
//
// Path matching uses doublestar semantics; the path is normalized by
// trimming leading slashes so pattern authors don't have to think about
// that. Match errors (malformed patterns) are treated as "no match" so a
// bad pattern can never accidentally widen a grant.
func (cp CapabilityPatterns) Allows(key, path string) bool {
	if cp == nil {
		return true
	}
	pats, ok := cp[key]
	if !ok || len(pats) == 0 {
		return true
	}
	if path == "" {
		return true
	}
	clean := strings.TrimLeft(path, "/")
	for _, pat := range pats {
		ok, err := doublestar.Match(pat, clean)
		if err == nil && ok {
			return true
		}
	}
	return false
}

// AllowsAncestor returns true when `path` is either matched by a pattern
// for `key` OR is an ancestor directory of a path that would be matched.
// Used by directory-listing routes (e.g. GET /folder/) so users granted
// `configs/team-a/**` can navigate `""` → `configs` → `configs/team-a`
// without 403s on each parent.
//
// Implementation: walk each pattern segment-by-segment up to the depth of
// `path`. If every segment of `path` matches the corresponding segment of
// the pattern (treating `*`/`**` literally per doublestar), the path could
// still lead to a match — allow it.
func (cp CapabilityPatterns) AllowsAncestor(key, path string) bool {
	if cp.Allows(key, path) {
		return true
	}
	if cp == nil {
		return true
	}
	pats := cp[key]
	if len(pats) == 0 {
		return true
	}
	clean := strings.TrimLeft(path, "/")
	pathSegs := splitSegments(clean)
	for _, pat := range pats {
		if isAncestorOfPattern(pathSegs, pat) {
			return true
		}
	}
	return false
}

func splitSegments(p string) []string {
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// isAncestorOfPattern reports whether `pathSegs` could be a strict-prefix
// ancestor directory of any path matching `pattern`. The Allows fast path
// already covered the equal/match case before this is called.
//
// Algorithm: walk both lists segment-wise. If we hit a `**` in the pattern,
// it can absorb the remaining path segments AND extend further, so any
// remaining path is an ancestor — return true. If we run out of pattern
// segments before consuming all path segments, the path is deeper than
// what the pattern can match: not an ancestor. If we consume the whole
// path with pattern segments still left, the path is a true prefix and
// could be extended into a match.
func isAncestorOfPattern(pathSegs []string, pattern string) bool {
	patSegs := strings.Split(pattern, "/")
	pi := 0
	for _, seg := range pathSegs {
		if pi >= len(patSegs) {
			return false
		}
		ps := patSegs[pi]
		if ps == "**" {
			return true
		}
		ok, err := doublestar.Match(ps, seg)
		if err != nil || !ok {
			return false
		}
		pi++
	}
	// Path fully consumed; pattern has more to match → ancestor.
	return pi < len(patSegs)
}

type capabilitiesCtxKey struct{}
type capabilityPatternsCtxKey struct{}

// WithCapabilities attaches a resolved capability set to ctx.
func WithCapabilities(ctx context.Context, keys []string) context.Context {
	return context.WithValue(ctx, capabilitiesCtxKey{}, Capabilities(keys))
}

// CapabilitiesFromContext returns the capability set attached via WithCapabilities.
// Returns an empty slice when nothing is attached.
func CapabilitiesFromContext(ctx context.Context) Capabilities {
	v, _ := ctx.Value(capabilitiesCtxKey{}).(Capabilities)
	return v
}

// WithCapabilityPatterns attaches the resolved per-key path-pattern map to ctx.
// Pass nil for a superadmin / unrestricted user.
func WithCapabilityPatterns(ctx context.Context, patterns map[string][]string) context.Context {
	if patterns == nil {
		return ctx
	}
	return context.WithValue(ctx, capabilityPatternsCtxKey{}, CapabilityPatterns(patterns))
}

// CapabilityPatternsFromContext returns the per-key patterns attached via
// WithCapabilityPatterns. Returns nil when nothing is attached, which the
// CapabilityPatterns.Allows method correctly treats as "unrestricted".
func CapabilityPatternsFromContext(ctx context.Context) CapabilityPatterns {
	v, _ := ctx.Value(capabilityPatternsCtxKey{}).(CapabilityPatterns)
	return v
}
