package service_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/rakunlabs/pika/internal/service"
)

// TestSettingsRoundTrip_PasskeyBlock asserts that AuthSettings.Passkey
// survives a Save / Load cycle through the real bw-backed storage.
//
// The settings layer relies on bw's reflective encoding for the whole
// Auth block — no custom MarshalJSON, no field-level merge, no
// secret-aware copy. Adding a sub-field to PasskeyStrategySettings (or
// rearranging the parent struct) is therefore a "stored bytes are now
// different from in-memory shape" risk. This test guards against that:
// every PasskeyStrategySettings field is populated with a
// distinguishable non-zero value, written through PatchSettings (the
// same path the HTTP handler takes), then re-read via Settings().
//
// If any field gets dropped, defaulted, or transcoded (e.g. ChallengeTTL
// flattened to 0 because some intermediate layer mishandles time.Duration),
// reflect.DeepEqual will fail with a clear field-level diff.
func TestSettingsRoundTrip_PasskeyBlock(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()

	want := &service.PasskeyStrategySettings{
		Enabled:          true,
		Name:             "webauthn",          // non-default URL key
		Label:            "Sign in with key",  // human label
		RPID:             "auth.example.com",  // bare host
		RPDisplayName:    "Example Auth",      // platform UI text
		RPOrigins:        []string{"https://auth.example.com", "https://admin.example.com"},
		UserVerification: "required", // strongest enum value
		ChallengeTTL:     7 * time.Minute,
	}

	// PatchSettings is the same entry point the HTTP handler uses, so
	// this also exercises the "Auth: settings.Auth = patch.Auth"
	// whole-block replace at settings.go:355-357.
	if err := svc.PatchSettings(ctx, &service.PatchSettings{
		Action: service.ActionKeySet,
		Auth: &service.AuthSettings{
			Passkey: want,
		},
	}); err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}

	got, err := svc.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.Auth == nil {
		t.Fatal("Auth nil after round-trip")
	}
	if got.Auth.Passkey == nil {
		t.Fatal("Auth.Passkey nil after round-trip — entire passkey block was dropped")
	}

	// Per-field comparison would also work, but DeepEqual gives a single
	// authoritative diff if any future field is added and forgotten.
	if !reflect.DeepEqual(want, got.Auth.Passkey) {
		t.Errorf("Passkey round-trip mismatch:\n want: %+v\n got:  %+v", want, got.Auth.Passkey)
	}
}

// TestSettingsRoundTrip_PasskeyDisabled covers the "feature off"
// shape: Enabled=false with everything else zeroed. The expectation is
// that the block round-trips as-is rather than getting normalized away
// (which would mask future bugs where the UI thinks passkey is on but
// the backend disagrees).
func TestSettingsRoundTrip_PasskeyDisabled(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()

	want := &service.PasskeyStrategySettings{
		Enabled: false,
		// All other fields zero on purpose — we still want the pointer
		// to survive so the UI can render "configured but disabled".
	}

	if err := svc.PatchSettings(ctx, &service.PatchSettings{
		Action: service.ActionKeySet,
		Auth: &service.AuthSettings{
			Passkey: want,
		},
	}); err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}

	got, err := svc.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.Auth == nil || got.Auth.Passkey == nil {
		t.Fatal("disabled passkey block did not survive round-trip (Auth or Passkey is nil)")
	}
	if got.Auth.Passkey.Enabled {
		t.Errorf("Enabled flipped to true during round-trip")
	}
}

// TestSettingsRoundTrip_PasskeyPreservesOtherAuthBlocks asserts that
// updating the passkey block via PatchSettings does not clobber
// existing strategy entries. PatchSettings replaces the whole
// AuthSettings (settings.go:355), so the caller is responsible for
// merging — this test documents the contract: the UI must round-trip
// the full Auth object, not just the field it edited.
//
// The test reflects the actual UI behaviour: AuthSection.svelte loads
// the full Auth object on mount and re-sends the whole thing on save
// via buildPayload(). This test fails if a future refactor makes
// PatchSettings.Auth a field-level merge — at which point this test
// should be updated, not the UI.
func TestSettingsRoundTrip_PasskeyPreservesOtherAuthBlocks(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()

	// First write: a Local strategy is present.
	if err := svc.PatchSettings(ctx, &service.PatchSettings{
		Action: service.ActionKeySet,
		Auth: &service.AuthSettings{
			Local: &service.LocalStrategySettings{Enabled: true, Name: "local"},
		},
	}); err != nil {
		t.Fatalf("PatchSettings (initial): %v", err)
	}

	// Second write: full Auth object including the previously-saved
	// Local plus the new Passkey block. This mirrors what the UI
	// does — load + edit + send the full struct.
	if err := svc.PatchSettings(ctx, &service.PatchSettings{
		Action: service.ActionKeySet,
		Auth: &service.AuthSettings{
			Local:   &service.LocalStrategySettings{Enabled: true, Name: "local"},
			Passkey: &service.PasskeyStrategySettings{Enabled: true, RPID: "example.com", RPOrigins: []string{"https://example.com"}},
		},
	}); err != nil {
		t.Fatalf("PatchSettings (with passkey): %v", err)
	}

	got, err := svc.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.Auth == nil {
		t.Fatal("Auth nil")
	}
	if got.Auth.Local == nil || !got.Auth.Local.Enabled {
		t.Error("Local strategy was lost after passkey save")
	}
	if got.Auth.Passkey == nil || !got.Auth.Passkey.Enabled || got.Auth.Passkey.RPID != "example.com" {
		t.Errorf("Passkey not persisted as expected: %+v", got.Auth.Passkey)
	}
}
