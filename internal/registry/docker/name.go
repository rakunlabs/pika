package docker

import (
	"fmt"
	"regexp"
	"strings"
)

// Docker / OCI repository name validation.
//
// The OCI distribution spec allows the following grammar:
//
//	name           ::= component ("/" component)*
//	component      ::= alpha-numeric (separator alpha-numeric)*
//	alpha-numeric  ::= [a-z0-9]+
//	separator      ::= [_.] | "__" | [-]+
//
// Up to 255 chars total. We use a regexp that matches that grammar
// while remaining cheap to evaluate per request.

var repoNameRegexp = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|[-]+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|[-]+)[a-z0-9]+)*)*$`)

// ValidateRepoName checks an inbound repository name (the {name}
// path parameter in /v2/{name}/...). Returns an error wrapped with
// ErrNameInvalid on failure.
func ValidateRepoName(name string) error {
	if name == "" {
		return fmt.Errorf("empty name: %w", ErrNameInvalid)
	}
	if len(name) > 255 {
		return fmt.Errorf("name too long: %w", ErrNameInvalid)
	}
	if !repoNameRegexp.MatchString(name) {
		return fmt.Errorf("name %q: invalid characters: %w", name, ErrNameInvalid)
	}
	return nil
}

// IsDigestReference reports whether the given manifest reference is
// a digest (vs. a tag). Tags can't contain ":" — digests always do.
func IsDigestReference(ref string) bool {
	return strings.Contains(ref, ":")
}

// ValidateTag enforces the OCI tag grammar: [A-Za-z0-9_][A-Za-z0-9._-]{0,127}.
func ValidateTag(tag string) error {
	if tag == "" || len(tag) > 128 {
		return fmt.Errorf("invalid tag length: %w", ErrNameInvalid)
	}
	c := tag[0]
	if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
		return fmt.Errorf("tag %q: bad leading character: %w", tag, ErrNameInvalid)
	}
	for i := 1; i < len(tag); i++ {
		c := tag[i]
		ok := (c >= 'A' && c <= 'Z') ||
			(c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') ||
			c == '.' || c == '_' || c == '-'
		if !ok {
			return fmt.Errorf("tag %q: bad character at %d: %w", tag, i, ErrNameInvalid)
		}
	}
	return nil
}
