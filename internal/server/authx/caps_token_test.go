package authx

import (
	"context"
	"slices"
	"testing"

	"github.com/rakunlabs/ada/middleware/auth/identity"

	"github.com/rakunlabs/pika/internal/service"
)

// tokenIdentity builds the identity service.ValidateTokenIdentity would
// produce for a token with the given name and scopes.
func tokenIdentity(name string, scopes ...service.TokenScope) *identity.Identity {
	paths := make([]string, 0, len(scopes))
	for _, sc := range scopes {
		paths = append(paths, sc.Path)
	}

	return &identity.Identity{
		Subject:  name,
		Name:     name,
		Provider: service.TokenProvider,
		Scopes:   paths,
		Claims: map[string]any{
			service.TokenScopesClaim: scopes,
		},
	}
}

func tokenScope(path string, ops ...string) service.TokenScope {
	return service.TokenScope{Path: path, Operations: ops}
}

// TestCapResolver_TokenScopesMapToCapabilities covers the projection of
// pika's token model onto its capability model — the step that makes an
// API token usable on the admin API rather than only on /data/*.
func TestCapResolver_TokenScopesMapToCapabilities(t *testing.T) {
	cr := &CapResolver{settings: service.CapabilityMapping{}}

	tests := []struct {
		name         string
		scopes       []service.TokenScope
		wantCaps     []string
		wantPatterns map[string][]string
	}{
		{
			name:         "read only",
			scopes:       []service.TokenScope{tokenScope("team-a/**", "read")},
			wantCaps:     []string{service.CapFilesRead},
			wantPatterns: map[string][]string{service.CapFilesRead: {"team-a/**"}},
		},
		{
			name:     "read and write on different prefixes stay separate",
			scopes:   []service.TokenScope{tokenScope("shared/**", "read"), tokenScope("team-a/**", "read", "write")},
			wantCaps: []string{service.CapFilesRead, service.CapFilesWrite},
			wantPatterns: map[string][]string{
				service.CapFilesRead:  {"shared/**", "team-a/**"},
				service.CapFilesWrite: {"team-a/**"},
			},
		},
		{
			name:   "delete implies the write capability",
			scopes: []service.TokenScope{tokenScope("team-a/**", "delete")},
			// pika has no separate delete capability; the REST delete
			// routes are gated on files.write.
			wantCaps:     []string{service.CapFilesWrite},
			wantPatterns: map[string][]string{service.CapFilesWrite: {"team-a/**"}},
		},
		{
			name:     "wildcard operation grants both",
			scopes:   []service.TokenScope{tokenScope("**", "*")},
			wantCaps: []string{service.CapFilesRead, service.CapFilesWrite},
			wantPatterns: map[string][]string{
				service.CapFilesRead:  {"**"},
				service.CapFilesWrite: {"**"},
			},
		},
		{
			name:   "bare star scope is normalized to doublestar",
			scopes: []service.TokenScope{tokenScope("*", "read")},
			// TokenScopesAllow treats "*" as match-everything, doublestar
			// does not. Without normalization the same token would reach
			// less through the admin API than through /data/*.
			wantCaps:     []string{service.CapFilesRead},
			wantPatterns: map[string][]string{service.CapFilesRead: {"**"}},
		},
		{
			name:     "no scopes grants nothing",
			scopes:   nil,
			wantCaps: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps, username, userID, patterns := cr.resolve(context.Background(), tokenIdentity("tok", tt.scopes...))

			gotCaps := []string(caps)
			slices.Sort(gotCaps)
			want := slices.Clone(tt.wantCaps)
			slices.Sort(want)
			if !slices.Equal(gotCaps, want) {
				t.Errorf("capabilities = %v, want %v", gotCaps, want)
			}

			if username != "tok" {
				t.Errorf("username = %q, want %q", username, "tok")
			}
			if userID != "" {
				t.Errorf("a token must not resolve to a pika user id, got %q", userID)
			}

			for key, wantPaths := range tt.wantPatterns {
				gotPaths := slices.Clone(patterns[key])
				slices.Sort(gotPaths)
				slices.Sort(wantPaths)
				if !slices.Equal(gotPaths, wantPaths) {
					t.Errorf("patterns[%s] = %v, want %v", key, gotPaths, wantPaths)
				}
			}
			for key := range patterns {
				if _, expected := tt.wantPatterns[key]; !expected {
					t.Errorf("unexpected pattern key %q", key)
				}
			}
		})
	}
}

// TestTokenScopeTranslationMatchesTokenMatcher is the safety property for
// the whole scope→pattern projection: for every scope shape, the paths a
// token can reach through the derived capability patterns (doublestar,
// used by the admin API) must be exactly the paths it can reach through
// service.TokenScopesAllow (used by /data/*).
//
// Any disagreement is a bug in one of two directions, and the widening
// direction is a privilege escalation — a token reaching further through
// the admin API than the surface it was minted for.
func TestTokenScopeTranslationMatchesTokenMatcher(t *testing.T) {
	scopePaths := []string{
		"**",
		"*",
		"myapp",
		"myapp/**",
		"myapp/*",
		"myapp/config",
		"tenants/*/config",
		// Not valid scope syntax, but an operator can type it. The token
		// matcher reads it literally; doublestar would read it as a
		// prefix glob unless the translation escapes it.
		"my*",
		"my*/**",
		"myapp/con?ig",
		"myapp/[abc]",
	}

	paths := []string{
		"",
		"myapp",
		"myapp/config",
		"myapp/config/db",
		"myapp/a/b/c",
		"myotherapp",
		"myotherapp/config",
		"my*",
		"my*/x",
		"tenants/acme/config",
		"tenants/acme/other",
		"other",
		"myapp/conFig",
		"myapp/a",
	}

	for _, scopePath := range scopePaths {
		t.Run(scopePath, func(t *testing.T) {
			scopes := []service.TokenScope{tokenScope(scopePath, "read")}
			_, _, _, patterns := (&CapResolver{settings: service.CapabilityMapping{}}).
				resolve(context.Background(), tokenIdentity("tok", scopes...))

			cp := service.CapabilityPatterns(patterns)

			for _, path := range paths {
				want := service.TokenScopesAllow(scopes, path, "read")
				got := cp.Allows(service.CapFilesRead, path)

				// The empty path is the unscoped check, which
				// CapabilityPatterns.Allows short-circuits to true by
				// contract; it is never a real config path.
				if path == "" {
					continue
				}

				if got != want {
					t.Errorf("scope %q, path %q: token matcher = %v, derived pattern = %v",
						scopePath, path, want, got)
				}
			}
		})
	}
}

// TestCapResolver_TokenCannotReachAdminCapabilities pins the ceiling. A
// token's vocabulary cannot express "administer this server", so no scope
// combination may produce an administrative capability — otherwise a
// leaked config token would also be a leaked admin credential.
func TestCapResolver_TokenCannotReachAdminCapabilities(t *testing.T) {
	cr := &CapResolver{settings: service.CapabilityMapping{}}

	// Every operation on everything, plus scope strings that happen to
	// spell capability names in case anything ever tried to match on them.
	caps, _, _, _ := cr.resolve(context.Background(), tokenIdentity("greedy",
		tokenScope("**", "*"),
		tokenScope("settings.manage", "*"),
		tokenScope("external.read", "*"),
	))

	forbidden := []string{
		service.CapSettingsManage,
		service.CapTokensManage,
		service.CapUsersManage,
		service.CapPermissionsManage,
		service.CapExternalRead,
		service.CapExternalWrite,
	}
	for _, key := range forbidden {
		if caps.Has(key) {
			t.Errorf("token must never obtain %q, got %v", key, []string(caps))
		}
	}
}

// TestCapResolver_TokenNamedAfterSuperadminIsNotSuperadmin is the
// escalation trap this mapping has to avoid. The superadmin allowlist
// matches on Identity.Subject, and a token's Subject is its operator-
// chosen name — so resolution must branch on the token provider before
// the allowlist is ever consulted.
func TestCapResolver_TokenNamedAfterSuperadminIsNotSuperadmin(t *testing.T) {
	cr := &CapResolver{settings: service.CapabilityMapping{Superadmins: []string{"alice"}}}

	caps, _, _, _ := cr.resolve(context.Background(), tokenIdentity("alice", tokenScope("team-a/**", "read")))

	if caps.Has(service.CapSettingsManage) {
		t.Fatalf("a token named after a superadmin escalated to full capabilities: %v", []string(caps))
	}
	if !caps.Has(service.CapFilesRead) || len(caps) != 1 {
		t.Errorf("expected exactly files.read from the token scope, got %v", []string(caps))
	}
}

// TestCapResolver_TokenRolesAreNotHonored guards the other mapping input.
// RoleMapping exists for IdP-issued roles; a token has none, and must not
// be able to smuggle capabilities in through a forged Roles slice.
func TestCapResolver_TokenRolesAreNotHonored(t *testing.T) {
	cr := &CapResolver{
		settings: service.CapabilityMapping{
			RoleMapping:  map[string][]string{"admin": {"admin-bundle"}},
			ScopeMapping: map[string][]string{"team-a/**": {"admin-bundle"}},
		},
	}

	id := tokenIdentity("tok", tokenScope("team-a/**", "read"))
	id.Roles = []string{"admin"}

	caps, _, _, _ := cr.resolve(context.Background(), id)

	if len(caps) != 1 || !caps.Has(service.CapFilesRead) {
		t.Errorf("token capabilities must derive from scopes alone, got %v", []string(caps))
	}
}
