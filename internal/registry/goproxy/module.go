package goproxy

import (
	"fmt"
	"regexp"
	"strings"
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
