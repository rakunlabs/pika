package service_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/ok"
	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/service"
)

// TestRenderRequiresExternalReadForExternalInherit guards the privilege-
// escalation path: a user with files.read but no external.read used to
// be able to read any backend by editing a config to inherit from it
// and hitting Render. The gate inside resolveInherits should now
// surface a 403-mappable ErrForbidden instead of silently fetching.
//
// The check is conditional on a CapabilitiesFromContext set being
// attached — i.e. the request went through CapResolver (authenticated
// UI). The /data consumer endpoint deliberately does NOT attach caps
// because it's token-authed; that path stays unaffected, see the
// sibling test below for proof.
func TestRenderRequiresExternalReadForExternalInherit(t *testing.T) {
	// Fake backend that would happily serve a body if reached; the
	// point of the test is that we never reach it.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("host: should-not-be-fetched\n"))
	}))
	t.Cleanup(server.Close)

	svc := newInheritTestService(t)
	if err := svc.SaveSettings(t.Context(), &service.Settings{
		External: map[string]external.External{
			"fake-http": {Http: &ok.Config{BaseURL: server.URL}},
		},
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	// Render context with caps={files.read} only — no external.read.
	// This mirrors what CapResolver would attach for a user with the
	// "Configurations Read/Write" bundle but without the new
	// "External Resources Read" bundle.
	ctx := service.WithCapabilities(t.Context(), []string{
		service.CapFilesRead,
		service.CapFilesWrite,
	})

	meta := &service.FileMeta{
		Format: "json",
		Inherits: []service.InheritEntry{{
			Resource: "fake-http",
			Path:     "/anything",
			Format:   "yaml",
		}},
	}

	_, err := svc.RenderFile(ctx, "child.json", "", "{}", meta)
	if err == nil {
		t.Fatalf("RenderFile must fail without external.read; got nil error")
	}
	if !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("error must wrap ErrForbidden; got %v", err)
	}
}

// TestRenderAllowsExternalInheritWithReadCap is the positive
// counterpart: same setup but with external.read in the cap set, the
// render must succeed.
func TestRenderAllowsExternalInheritWithReadCap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("host: ok\n"))
	}))
	t.Cleanup(server.Close)

	svc := newInheritTestService(t)
	if err := svc.SaveSettings(t.Context(), &service.Settings{
		External: map[string]external.External{
			"fake-http": {Http: &ok.Config{BaseURL: server.URL}},
		},
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	ctx := service.WithCapabilities(t.Context(), []string{
		service.CapFilesRead,
		service.CapExternalRead,
	})

	meta := &service.FileMeta{
		Format: "json",
		Inherits: []service.InheritEntry{{
			Resource: "fake-http",
			Path:     "/anything",
			Format:   "yaml",
		}},
	}
	if _, err := svc.RenderFile(ctx, "child.json", "", "{}", meta); err != nil {
		t.Fatalf("RenderFile with external.read failed: %v", err)
	}
}

// TestConsumerDataPathSkipsCapCheck proves the /data consumer flow is
// unchanged: when ctx has NO Capabilities attached (token-auth path),
// the inherit resolver must NOT gate on external.read. The application
// consuming its own config at runtime mustn't suddenly start failing
// because of a UI-level check.
func TestConsumerDataPathSkipsCapCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("host: ok\n"))
	}))
	t.Cleanup(server.Close)

	svc := newInheritTestService(t)
	if err := svc.SaveSettings(t.Context(), &service.Settings{
		External: map[string]external.External{
			"fake-http": {Http: &ok.Config{BaseURL: server.URL}},
		},
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	// Plain t.Context() — no WithCapabilities attached, exactly what
	// the /data endpoint produces. Render path used for convenience
	// (same resolveInherits underneath).
	meta := &service.FileMeta{
		Format: "json",
		Inherits: []service.InheritEntry{{
			Resource: "fake-http",
			Path:     "/anything",
			Format:   "yaml",
		}},
	}
	if _, err := svc.RenderFile(t.Context(), "child.json", "", "{}", meta); err != nil {
		t.Fatalf("consumer-style render must succeed without caps in ctx; got %v", err)
	}
}

// TestRenderRequiresRawReadForMountInherit mirrors the external test
// for the mount branch. raw.read is the matching capability —
// inherits from a raw mount must surface ErrForbidden when missing.
func TestRenderRequiresRawReadForMountInherit(t *testing.T) {
	svc := newInheritTestService(t)
	// We don't need an actual mount on disk: the cap gate runs BEFORE
	// fetchRawMountConfig, so the test never reaches the filesystem.
	ctx := service.WithCapabilities(t.Context(), []string{service.CapFilesRead})

	meta := &service.FileMeta{
		Format: "json",
		Inherits: []service.InheritEntry{{
			Mount: "some-mount",
			Path:  "any/file.yaml",
		}},
	}

	_, err := svc.RenderFile(ctx, "child.json", "", "{}", meta)
	if err == nil {
		t.Fatalf("RenderFile must fail without raw.read; got nil error")
	}
	if !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("error must wrap ErrForbidden; got %v", err)
	}
}
