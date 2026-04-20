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
