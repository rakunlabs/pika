package authx

import (
	"strings"

	"github.com/rakunlabs/ada/middleware/auth/identity"

	"github.com/rakunlabs/pika/internal/service"
)

// Token scope operations, as stored on service.TokenScope.
const (
	tokenOpRead   = "read"
	tokenOpWrite  = "write"
	tokenOpDelete = "delete"
	tokenOpAny    = "*"
)

// tokenReport projects an API token's scopes onto pika's capability
// vocabulary so the same withPerm / withPermPath route guards that serve
// browser sessions also serve token callers.
//
// Tokens and users are authorized by different models. A user holds
// capability bundles; a token holds path globs paired with operations.
// This is the one place the two meet, and the mapping is deliberately
// narrow:
//
//   - read  → files.read, restricted to the paths that grant it
//   - write → files.write, likewise
//   - delete → files.write (pika has no separate delete capability; the
//     REST routes gate deletes on files.write)
//
// Nothing else is derivable. A token can never obtain settings.manage,
// users.manage, tokens.manage or the external.* capabilities, because
// its scope vocabulary cannot express them — there is no operation that
// means "administer this server". That keeps the blast radius of a
// leaked token to configuration data, and keeps the token surface
// identical to what the REST routes already enforce.
//
// The returned report is intentionally shallow: no DB lookup, no
// superadmin evaluation, no bundle expansion. A token is not a user.
func tokenReport(id *identity.Identity) *EffectiveReport {
	scopes := service.TokenScopesFromIdentity(id)

	rep := &EffectiveReport{
		Username:     id.Subject,
		Roles:        []string{},
		Scopes:       id.Scopes,
		Capabilities: []string{},
		Sources:      []CapSource{},
		Denied:       []string{},
	}
	if rep.Scopes == nil {
		rep.Scopes = []string{}
	}

	readPaths := scopePathsFor(scopes, tokenOpRead)
	writePaths := scopePathsFor(scopes, tokenOpWrite, tokenOpDelete)

	patterns := map[string][]string{}
	if len(readPaths) > 0 {
		rep.Capabilities = append(rep.Capabilities, service.CapFilesRead)
		rep.Sources = append(rep.Sources, CapSource{Capability: service.CapFilesRead, Kind: "token_scope"})
		patterns[service.CapFilesRead] = readPaths
	}
	if len(writePaths) > 0 {
		rep.Capabilities = append(rep.Capabilities, service.CapFilesWrite)
		rep.Sources = append(rep.Sources, CapSource{Capability: service.CapFilesWrite, Kind: "token_scope"})
		patterns[service.CapFilesWrite] = writePaths
	}

	if len(patterns) > 0 {
		rep.Patterns = patterns
	}

	return rep
}

// scopePathsFor collects the scope paths granting any of the given
// operations, deduplicated and normalized for glob matching.
func scopePathsFor(scopes []service.TokenScope, ops ...string) []string {
	seen := make(map[string]struct{}, len(scopes))
	out := make([]string, 0, len(scopes))

	for _, scope := range scopes {
		if !scopeHasAnyOp(scope, ops) {
			continue
		}

		path := normalizeScopePath(scope.Path)
		if path == "" {
			continue
		}
		if _, dup := seen[path]; dup {
			continue
		}

		seen[path] = struct{}{}
		out = append(out, path)
	}

	return out
}

func scopeHasAnyOp(scope service.TokenScope, want []string) bool {
	for _, have := range scope.Operations {
		if have == tokenOpAny {
			return true
		}
		for _, w := range want {
			if have == w {
				return true
			}
		}
	}

	return false
}

// normalizeScopePath translates a token scope path into an equivalent
// doublestar pattern.
//
// The two dialects are close but not identical, and every difference is a
// place where a token could reach further through the admin API than it
// does through /data/*. There are exactly two:
//
//  1. A bare "*" is match-everything to the token matcher, but stops at a
//     path separator in doublestar — which would narrow such a token to
//     top-level paths only. Rewritten to "**".
//
//  2. The token matcher compares segments literally unless the whole
//     segment is "*" or "**": "my*" matches the segment "my*" and nothing
//     else. doublestar would read it as a prefix glob and match "myapp",
//     silently widening the token. Escaped so it stays literal.
//
// The second case is the one that matters. The scope syntax is documented
// as having no partial-segment wildcards, so a pattern like "my*" is
// already a misunderstanding on the operator's part — but it must fail
// closed on both surfaces, not deny on one and grant on the other.
func normalizeScopePath(path string) string {
	if path == tokenOpAny || path == "**" {
		return "**"
	}

	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if segment == "*" || segment == "**" {
			continue
		}

		segments[i] = escapeGlobMeta(segment)
	}

	return strings.Join(segments, "/")
}

// escapeGlobMeta backslash-escapes every doublestar metacharacter in a
// segment the token matcher would have compared literally.
func escapeGlobMeta(segment string) string {
	if !strings.ContainsAny(segment, `*?[]{}\`) {
		return segment
	}

	var b strings.Builder
	b.Grow(len(segment) * 2)

	for _, r := range segment {
		if strings.ContainsRune(`*?[]{}\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}

	return b.String()
}
