package service

import (
	"context"
	"testing"
)

func TestWithCapabilities_RoundTrip(t *testing.T) {
	ctx := context.Background()
	if got := CapabilitiesFromContext(ctx); len(got) != 0 {
		t.Fatalf("empty ctx: got %v", got)
	}

	ctx = WithCapabilities(ctx, []string{"files.read", "files.write"})
	got := CapabilitiesFromContext(ctx)
	if len(got) != 2 || got[0] != "files.read" {
		t.Fatalf("round-trip: got %v", got)
	}
}

func TestCapabilitiesFromContext_Has(t *testing.T) {
	caps := Capabilities([]string{"files.read", "users.manage"})
	if !caps.Has("files.read") {
		t.Error("Has: expected true for files.read")
	}
	if caps.Has("files.write") {
		t.Error("Has: expected false for files.write")
	}
}

func TestCapabilityPatterns_Allows(t *testing.T) {
	cases := []struct {
		name     string
		patterns CapabilityPatterns
		key      string
		path     string
		want     bool
	}{
		{"nil patterns allows everything", nil, "files.read", "anything/here", true},
		{"empty patterns map allows everything", CapabilityPatterns{}, "files.read", "anything", true},
		{"key with no patterns allows everything", CapabilityPatterns{"files.write": {"x/**"}}, "files.read", "anything", true},
		{"empty path always allowed (caller already passed cap check)", CapabilityPatterns{"files.read": {"configs/**"}}, "files.read", "", true},
		{"matching glob", CapabilityPatterns{"files.read": {"configs/**"}}, "files.read", "configs/team-a/app.yaml", true},
		{"non-matching glob", CapabilityPatterns{"files.read": {"configs/team-a/**"}}, "files.read", "configs/team-b/app.yaml", false},
		{"leading slash on path is normalized", CapabilityPatterns{"files.read": {"configs/**"}}, "files.read", "/configs/x", true},
		{"single-segment glob", CapabilityPatterns{"files.read": {"shared/*.yaml"}}, "files.read", "shared/app.yaml", true},
		{"single-segment glob doesn't cross /", CapabilityPatterns{"files.read": {"shared/*.yaml"}}, "files.read", "shared/sub/app.yaml", false},
		{"any-of patterns", CapabilityPatterns{"files.read": {"a/**", "b/**"}}, "files.read", "b/file", true},
		{"malformed pattern is ignored", CapabilityPatterns{"files.read": {"[invalid", "configs/**"}}, "files.read", "configs/x", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.patterns.Allows(tc.key, tc.path); got != tc.want {
				t.Errorf("Allows(%q, %q): got %v want %v", tc.key, tc.path, got, tc.want)
			}
		})
	}
}

func TestCapabilityPatterns_AllowsAncestor(t *testing.T) {
	cases := []struct {
		name     string
		patterns CapabilityPatterns
		key      string
		path     string
		want     bool
	}{
		{"empty patterns: allowed", CapabilityPatterns{}, "files.read", "anywhere", true},
		{"root listing under deep pattern", CapabilityPatterns{"files.read": {"configs/team-a/**"}}, "files.read", "", true},
		{"intermediate ancestor", CapabilityPatterns{"files.read": {"configs/team-a/**"}}, "files.read", "configs", true},
		{"exact match still allowed", CapabilityPatterns{"files.read": {"configs/team-a/**"}}, "files.read", "configs/team-a/x.yaml", true},
		{"sibling ancestor not allowed", CapabilityPatterns{"files.read": {"configs/team-a/**"}}, "files.read", "configs/team-b", false},
		{"deeper than pattern not allowed when no doublestar", CapabilityPatterns{"files.read": {"configs/team-a"}}, "files.read", "configs/team-a/sub", false},
		{"** at root: any ancestor allowed", CapabilityPatterns{"files.read": {"**/secrets.yaml"}}, "files.read", "anywhere/here", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.patterns.AllowsAncestor(tc.key, tc.path); got != tc.want {
				t.Errorf("AllowsAncestor(%q, %q): got %v want %v", tc.key, tc.path, got, tc.want)
			}
		})
	}
}

func TestWithCapabilityPatterns_RoundTrip(t *testing.T) {
	ctx := context.Background()
	if got := CapabilityPatternsFromContext(ctx); got != nil {
		t.Fatalf("empty ctx: got %v", got)
	}
	ctx = WithCapabilityPatterns(ctx, map[string][]string{"files.read": {"configs/**"}})
	got := CapabilityPatternsFromContext(ctx)
	if !got.Allows("files.read", "configs/x") {
		t.Errorf("round-trip: pattern not preserved: %v", got)
	}
}
