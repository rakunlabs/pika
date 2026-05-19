// Package semver is the minimal semver comparator pika uses across
// the goproxy / npm / helm registries.
//
// We deliberately do not pull in golang.org/x/mod/semver or
// Masterminds/semver here. Three reasons:
//
//   - Input space is bounded: every caller has already validated
//     the version through the protocol-specific path (Go's
//     ValidateVersion, npm's tarball publish, Helm chart Chart.yaml).
//   - The comparator only needs to answer "newest first" for the
//     UI's version dropdowns and the cached-latest pointer; it is
//     not a constraint solver.
//   - Three near-clones of this code existed across goproxy/, npm/,
//     and helm/ — collapsing them removes ~150 lines of drift risk.
//
// The single semantic toggle is the leading `v`:
//
//   - ModeStrict   requires "v1.2.3" (Go module versions).
//   - ModeLenient  accepts both "1.2.3" and "v1.2.3" (npm, Helm).
//
// Build metadata (`+sha.abc`) is dropped on both sides. Pre-release
// tags (`-rc.1`) sort below the matching release.
package semver

import (
	"sort"
	"strings"
)

// Mode selects how strictly the leading "v" prefix is enforced.
type Mode int

const (
	// ModeStrict requires every version to start with "v" — this
	// is Go's module rule. Versions missing the prefix fall back
	// to lex compare.
	ModeStrict Mode = iota
	// ModeLenient accepts both "v1.2.3" and "1.2.3" — this is
	// the npm / Helm convention.
	ModeLenient
)

// Compare returns -1 if a < b, 0 if equal, +1 if a > b under the
// configured Mode. For unparseable inputs the comparator falls
// back to lexicographic order so the result is at least stable.
func Compare(a, b string, m Mode) int {
	if a == b {
		return 0
	}
	an, apre, aok := split(a, m)
	bn, bpre, bok := split(b, m)
	if !aok || !bok {
		switch {
		case a < b:
			return -1
		case a > b:
			return 1
		}
		return 0
	}
	// Numeric components, left-to-right. Missing components are
	// treated as zero ("v1.2" < "v1.2.1").
	for i := 0; i < len(an) || i < len(bn); i++ {
		var ai, bi int
		if i < len(an) {
			ai = an[i]
		}
		if i < len(bn) {
			bi = bn[i]
		}
		if ai != bi {
			if ai < bi {
				return -1
			}
			return 1
		}
	}
	// Numeric tied; pre-release tag breaks. A version without a
	// pre-release tag is greater than one with.
	switch {
	case apre == "" && bpre == "":
		return 0
	case apre == "":
		return 1
	case bpre == "":
		return -1
	}
	// Both pre-release — lex compare. Close enough for the
	// common alpha/beta/rc patterns; deliberately not full SemVer
	// 2.0 identifier compare.
	switch {
	case apre < bpre:
		return -1
	case apre > bpre:
		return 1
	}
	return 0
}

// SortDesc orders the slice newest-first in place. Uses a stable
// sort so equal-precedence versions keep their input order.
func SortDesc(versions []string, m Mode) {
	sort.SliceStable(versions, func(i, j int) bool {
		return Compare(versions[i], versions[j], m) > 0
	})
}

// split parses "v1.2.3-rc.1+meta" into numeric components and the
// pre-release tag. Build metadata is dropped (per the SemVer rule
// that it is ignored for precedence). Returns ok=false for any
// shape that doesn't fit (non-numeric component, missing required
// prefix under ModeStrict, empty body after prefix strip).
func split(v string, m Mode) ([]int, string, bool) {
	switch m {
	case ModeStrict:
		if !strings.HasPrefix(v, "v") {
			return nil, "", false
		}
		v = v[1:]
	case ModeLenient:
		v = strings.TrimPrefix(v, "v")
	}
	if v == "" {
		return nil, "", false
	}
	if i := strings.Index(v, "+"); i >= 0 {
		v = v[:i]
	}
	pre := ""
	if i := strings.Index(v, "-"); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			return nil, "", false
		}
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				return nil, "", false
			}
			n = n*10 + int(c-'0')
		}
		out = append(out, n)
	}
	return out, pre, true
}
