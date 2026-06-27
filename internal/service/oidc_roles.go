package service

import "strings"

// HarvestRoles collects role strings from an identity's raw claims map by
// walking each configured dotted path. It exists because the upstream ada
// OAuth2 strategy only reads a flat top-level claim into Identity.Roles
// (stringsClaim indexes claims[key] directly, with no nested traversal), so
// IdPs that nest roles — notably Keycloak's realm_access.roles and
// resource_access.<client>.roles — need pika-side extraction.
//
// Path syntax is dot-separated segments over the claims map:
//
//	"roles"                      top-level claim
//	"realm_access.roles"         nested map field
//	"resource_access.pika.roles" deeper nesting
//	"resource_access.*.roles"    "*" iterates every value of a map
//
// A leaf value is normalized like ada's stringsClaim: []any of strings,
// []string, or a whitespace-separated string. Results are deduplicated with
// first-seen order preserved. Role strings are returned bare (no client
// namespacing) so they match CapabilityMapping.RoleMapping keys directly.
//
// Missing paths, type mismatches and empty leaves are skipped silently —
// harvesting is best-effort and fail-open to "no extra roles", never an error.
func HarvestRoles(claims map[string]any, paths []string) []string {
	if len(claims) == 0 || len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	add := func(roles []string) {
		for _, r := range roles {
			if r == "" {
				continue
			}
			if _, dup := seen[r]; dup {
				continue
			}
			seen[r] = struct{}{}
			out = append(out, r)
		}
	}
	for _, p := range paths {
		segs := splitPath(p)
		if len(segs) == 0 {
			continue
		}
		walkClaimPath(any(claims), segs, add)
	}
	return out
}

// splitPath breaks a dotted claim path into non-empty segments. A bare or
// dotted-but-empty path yields no segments and is ignored by the caller.
func splitPath(path string) []string {
	raw := strings.Split(path, ".")
	out := raw[:0]
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// walkClaimPath descends `node` following `segs`, invoking emit with the role
// strings found at every leaf reached. A "*" segment fans out across all
// values of a map node.
func walkClaimPath(node any, segs []string, emit func([]string)) {
	if len(segs) == 0 {
		emit(rolesFromLeaf(node))
		return
	}
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	seg, rest := segs[0], segs[1:]
	if seg == "*" {
		for _, v := range m {
			walkClaimPath(v, rest, emit)
		}
		return
	}
	next, ok := m[seg]
	if !ok {
		return
	}
	walkClaimPath(next, rest, emit)
}

// rolesFromLeaf normalizes a claim leaf into role strings, mirroring ada's
// stringsClaim (strategy/oauth2): an array of strings, a []string, or a
// whitespace-separated single string.
func rolesFromLeaf(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	case string:
		return strings.Fields(t)
	default:
		return nil
	}
}
