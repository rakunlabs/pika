package goproxy

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/rakunlabs/pika/internal/registry/common/semver"
)

// Module path encoding and version validation.
//
// The Go module proxy protocol requires "case-encoded" module paths
// in URLs and on disk: every uppercase letter is rewritten as "!" +
// the lowercase letter. The reason is filesystems that case-fold
// (HFS+, NTFS, some FUSE backends) — without the escape,
// "github.com/Azure/foo" and "github.com/azure/foo" would collide.
//
// Spec: https://go.dev/ref/mod#goproxy-protocol
//
// Examples:
//
//	github.com/Azure/azure-sdk-for-go  →  github.com/!azure/azure-sdk-for-go
//	github.com/MakeNowJust/heredoc      →  github.com/!make!now!just/heredoc
//	golang.org/x/sync                   →  golang.org/x/sync           (no change)
//
// We encode at storage / URL boundaries and decode for display.

// EncodeModulePath converts a Go module path into its case-encoded
// form. Every uppercase letter becomes "!<lower>". Lowercase letters
// and other characters pass through.
//
// Empty input returns "" — callers validate elsewhere.
func EncodeModulePath(p string) string {
	var b strings.Builder
	b.Grow(len(p))
	for _, r := range p {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// DecodeModulePath reverses EncodeModulePath. Returns an error when
// "!" is not followed by a lowercase ASCII letter, or when the input
// ends with a stray "!" — both shapes are illegal per spec.
func DecodeModulePath(p string) (string, error) {
	var b strings.Builder
	b.Grow(len(p))
	i := 0
	for i < len(p) {
		c := p[i]
		if c != '!' {
			b.WriteByte(c)
			i++
			continue
		}
		// Expect a lowercase ASCII letter next.
		if i+1 >= len(p) {
			return "", fmt.Errorf("module path %q: trailing '!'", p)
		}
		nx := p[i+1]
		if nx < 'a' || nx > 'z' {
			return "", fmt.Errorf("module path %q: '!' not followed by lowercase letter at %d", p, i+1)
		}
		b.WriteByte(nx - ('a' - 'A'))
		i += 2
	}
	return b.String(), nil
}

// ValidateModulePath is a conservative sanity check for inbound
// module paths. The full Go module path grammar is complex
// (see golang.org/x/mod/module.CheckPath), but for routing safety
// we only need: no path traversal, no empty segments, no leading/
// trailing slash, no whitespace.
//
// Full grammar validation happens at upload-time on the server (when
// it parses the uploaded .mod file) and is enforced by go itself on
// the client side.
func ValidateModulePath(p string) error {
	if p == "" {
		return fmt.Errorf("module path is empty")
	}
	if strings.Contains(p, "..") {
		return fmt.Errorf("module path %q: contains '..'", p)
	}
	if strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") {
		return fmt.Errorf("module path %q: leading/trailing slash", p)
	}
	if strings.ContainsAny(p, " \t\n\r") {
		return fmt.Errorf("module path %q: whitespace", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" {
			return fmt.Errorf("module path %q: empty segment", p)
		}
	}
	return nil
}

// Module version helpers. The proxy protocol accepts any pseudo /
// tagged version that go itself would accept; for routing we only
// need a coarse "is this a plausibly well-formed version string"
// check. The matcher matches:
//
//	v1.2.3
//	v1.2.3-pre.1
//	v1.2.3+build.42
//	v0.0.0-20240101000000-abcdef012345    (pseudo-version)
//	v1.2.3-0.20240101000000-abcdef012345  (pre-pseudo)
//	v2.0.0+incompatible
//
// We reject anything else at the URL boundary so a request like
// `/@v/..%2Fevil.info` never reaches the storage layer with a
// malicious path.
var versionRegexp = regexp.MustCompile(`^v\d+(\.\d+)*([-+][0-9A-Za-z.\-]+)*$`)

// ValidateVersion checks the rough version shape used in URLs.
// Returns an error for empty input or for strings that don't match
// the version regexp.
func ValidateVersion(v string) error {
	if v == "" {
		return fmt.Errorf("version is empty")
	}
	if v == "latest" {
		return fmt.Errorf("'latest' is not a version literal")
	}
	if !versionRegexp.MatchString(v) {
		return fmt.Errorf("version %q: invalid format", v)
	}
	return nil
}

// CompareVersions implements a coarse semver-aware comparator
// returning <0 / 0 / >0 for (a < b), (a == b), (a > b). The
// algorithm mirrors golang.org/x/mod/semver semantics for the
// subset of versions Go modules actually use; pre-release tags
// sort *before* the matching release (1.2.3-alpha < 1.2.3) and
// build metadata is ignored.
//
// Thin wrapper over common/semver.Compare in ModeStrict — Go
// module versions always carry the leading "v". Preserved as a
// package-level export for store_test.go and external callers.
func CompareVersions(a, b string) int {
	return semver.Compare(a, b, semver.ModeStrict)
}

// SortVersionsDesc sorts the slice in place from highest to
// lowest semver. Used by the UI's package detail view which
// wants the newest release at the top.
func SortVersionsDesc(versions []string) {
	semver.SortDesc(versions, semver.ModeStrict)
}

// GoModInfo captures the small subset of go.mod fields pika
// surfaces in the package detail UI. The parser is intentionally
// minimal — full go.mod parsing belongs in golang.org/x/mod/modfile,
// which we avoid here for the same reason as in CompareVersions.
//
// Only `module` and `retract` directives are extracted. Retract
// blocks can carry an optional comment that pika treats as the
// retraction rationale.
type GoModInfo struct {
	// Module is the module path declared at the top of go.mod.
	Module string
	// Retracts lists every retract directive. A retract directive
	// without an explicit version range applies to the version
	// the .mod file describes; with a range it applies to that
	// range. The Version field below is the literal version
	// argument (or range), Rationale is the trailing comment.
	Retracts []GoModRetract
}

// GoModRetract is one row in GoModInfo.Retracts.
type GoModRetract struct {
	// Version is the literal version argument from the retract
	// directive. For ranges ("[v1.0.0, v1.0.4]") this is the raw
	// argument string; the caller can split as needed.
	Version string
	// Rationale is the trailing // comment (the convention for
	// "why was this retracted"). Empty when omitted.
	Rationale string
}

// ParseGoMod extracts the small subset of go.mod fields pika needs
// for the UI detail view. Unknown directives are ignored — this is
// not a validating parser and a malformed go.mod won't cause an
// error, just an empty / partial GoModInfo.
//
// Recognized directives:
//
//	module <path>
//	retract <version> [// rationale]
//	retract [<low>, <high>] [// rationale]
//	retract (
//	    <version> [// rationale]
//	    ...
//	)
func ParseGoMod(body []byte) GoModInfo {
	out := GoModInfo{}
	lines := strings.Split(string(body), "\n")
	inRetractBlock := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		// Strip "// ..." inline comment, but remember it as
		// the rationale for retract lines.
		comment := ""
		if i := strings.Index(line, "//"); i >= 0 {
			comment = strings.TrimSpace(line[i+2:])
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if inRetractBlock {
			if line == ")" {
				inRetractBlock = false
				continue
			}
			out.Retracts = append(out.Retracts, GoModRetract{
				Version:   line,
				Rationale: comment,
			})
			continue
		}
		switch {
		case strings.HasPrefix(line, "module "):
			out.Module = strings.TrimSpace(strings.TrimPrefix(line, "module"))
			out.Module = strings.Trim(out.Module, `"`)
		case line == "retract (":
			inRetractBlock = true
		case strings.HasPrefix(line, "retract "):
			arg := strings.TrimSpace(strings.TrimPrefix(line, "retract"))
			out.Retracts = append(out.Retracts, GoModRetract{
				Version:   arg,
				Rationale: comment,
			})
		}
	}
	return out
}
