package npm

import (
	"fmt"
	"net/url"
	"strings"
)

// Package name validation and URL parsing helpers.
//
// NPM package names follow npm/validate-npm-package-name rules. We
// implement a conservative subset that's sufficient for routing:
//
//   - Unscoped: 1..214 chars, lowercase, [a-z0-9._-], cannot start
//     with "." or "_".
//   - Scoped:   "@<scope>/<name>" where both halves obey unscoped
//     rules. Scope and name are separated by exactly one "/".
//
// The full validator (URL-safety, reserved-words, etc.) is left to
// the client's package.json — we only need to refuse names that
// could escape the storage prefix or break URL routing.

// ValidatePackageName checks a package name against the routing-
// safe rule set. Returns an error wrapped with ErrInvalidPackage on
// failure so callers can match with errors.Is.
func ValidatePackageName(name string) error {
	if name == "" {
		return fmt.Errorf("empty: %w", ErrInvalidPackage)
	}
	if len(name) > 214 {
		return fmt.Errorf("%q too long: %w", name, ErrInvalidPackage)
	}
	if strings.HasPrefix(name, "@") {
		// Scoped: "@scope/name"
		slash := strings.IndexByte(name, '/')
		if slash <= 1 || slash == len(name)-1 {
			return fmt.Errorf("%q malformed scope: %w", name, ErrInvalidPackage)
		}
		scope := name[1:slash]
		bare := name[slash+1:]
		if err := validateBareName(scope); err != nil {
			return fmt.Errorf("scope %q: %w: %w", scope, err, ErrInvalidPackage)
		}
		if err := validateBareName(bare); err != nil {
			return fmt.Errorf("name %q: %w: %w", bare, err, ErrInvalidPackage)
		}
		return nil
	}
	if err := validateBareName(name); err != nil {
		return fmt.Errorf("%q: %w: %w", name, err, ErrInvalidPackage)
	}
	return nil
}

// validateBareName checks one unscoped segment (the part after the
// scope separator, or the whole name for unscoped packages).
func validateBareName(s string) error {
	if s == "" {
		return fmt.Errorf("empty segment")
	}
	if s[0] == '.' || s[0] == '_' {
		return fmt.Errorf("must not start with '.' or '_'")
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.'
		if !ok {
			return fmt.Errorf("invalid character %q", r)
		}
	}
	return nil
}

// ParseNameFromPath extracts the package name from the URL path
// component immediately after the registry prefix. Both encoded
// forms are accepted:
//
//	/@scope/name        → "@scope/name"
//	/@scope%2Fname      → "@scope/name"
//	/name               → "name"
//
// rest is the remainder of the path after the name (starting with
// "/" or empty). Returns ok=false when the path doesn't parse as a
// package reference.
func ParseNameFromPath(p string) (name, rest string, ok bool) {
	if !strings.HasPrefix(p, "/") {
		return "", "", false
	}
	p = p[1:]
	if p == "" {
		return "", "", false
	}
	if strings.HasPrefix(p, "@") {
		// Scoped: either "@scope%2Fname..." or "@scope/name..."
		// Try percent-encoded slash first.
		if idx := strings.Index(p, "%2F"); idx > 0 {
			scope := p[:idx]
			afterScope := p[idx+3:]
			// afterScope may carry "/rest" or just the bare name.
			slash := strings.IndexByte(afterScope, '/')
			if slash < 0 {
				return joinScope(scope, afterScope), "", afterScope != ""
			}
			bare := afterScope[:slash]
			if bare == "" {
				return "", "", false
			}
			return joinScope(scope, bare), afterScope[slash:], true
		}
		// Plain "/@scope/name[/rest]"
		slash := strings.IndexByte(p, '/')
		if slash < 0 {
			return "", "", false // "@scope" alone isn't a package name
		}
		scope := p[:slash]
		afterScope := p[slash+1:]
		// afterScope must be at least "name" (no leading slash).
		next := strings.IndexByte(afterScope, '/')
		if next < 0 {
			return joinScope(scope, afterScope), "", afterScope != ""
		}
		bare := afterScope[:next]
		if bare == "" {
			return "", "", false
		}
		return joinScope(scope, bare), afterScope[next:], true
	}
	// Unscoped: "name[/rest]"
	slash := strings.IndexByte(p, '/')
	if slash < 0 {
		return p, "", true
	}
	return p[:slash], p[slash:], true
}

func joinScope(scope, name string) string {
	if !strings.HasPrefix(scope, "@") {
		scope = "@" + scope
	}
	return scope + "/" + name
}

// NormalizeNameFromURL decodes a percent-escaped NPM name (the form
// npm CLIs occasionally send: "@scope%2Fpkg") into its canonical
// "@scope/pkg" shape. Names that don't decode (or that decode to
// invalid forms) are returned verbatim — let ValidatePackageName
// catch them downstream.
func NormalizeNameFromURL(name string) string {
	if dec, err := url.PathUnescape(name); err == nil {
		return dec
	}
	return name
}
