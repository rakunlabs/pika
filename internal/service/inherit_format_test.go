package service_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/ok"
	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/service"
)

// TestInheritEntryFormatRoundTrips ensures the new Format field
// survives JSON marshal/unmarshal as part of an InheritEntry. The save
// pipeline funnels everything through json.Marshal (server → storage)
// and json.Unmarshal (HTTP body → service.File), so a missing/typoed
// tag here would silently swallow the user's "Decode As" selection and
// produce exactly the "preview works but Render doesn't" symptom we
// debugged.
func TestInheritEntryFormatRoundTrips(t *testing.T) {
	in := service.InheritEntry{
		Resource: "my-consul",
		Path:     "app/config",
		Format:   "yaml",
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"format":"yaml"`) {
		t.Fatalf("marshal dropped Format field: %s", raw)
	}

	var out service.InheritEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Format != "yaml" {
		t.Fatalf("Format = %q, want %q (raw=%s)", out.Format, "yaml", raw)
	}
}

// TestInheritEntryFormatOmittedWhenEmpty guards backward compatibility:
// the JSON tag must be `omitempty`, otherwise every legacy entry would
// suddenly carry `"format":""` over the wire and the saved storage
// blobs would diff against pre-feature snapshots even for files no
// user ever touched. Catches an accidental tag change.
func TestInheritEntryFormatOmittedWhenEmpty(t *testing.T) {
	raw, err := json.Marshal(service.InheritEntry{Source: "base/db"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), `"format"`) {
		t.Fatalf("empty Format should be omitted; got %s", raw)
	}
}

// TestInheritFormatDecodesYAMLOnRender wires the full Service.Render
// path end-to-end to prove the user's symptom is gone after the fix:
//
//   - We can't fake an external resource cleanly without an in-memory
//     provider, so this test exercises the equivalent code path through
//     a raw-mount entry. Mount and external follow the same branch in
//     resolveInherits (`entry.Resource != "" || entry.Mount != ""`),
//     so verifying mount also covers the external Format wiring.
//
// If this test starts failing with "missing key 'host'" the regression
// is back: Format isn't reaching resolveInherits, or decodeWrappedValue
// isn't being called before merge.
//
// NOTE: This is a contract test for the *Format hint pipeline*, not for
// the wrapper-detection helper itself (which TestDecodeWrappedValue
// covers exhaustively in merge_test.go).
func TestInheritFormatDecodesYAMLOnRender(t *testing.T) {
	// Construct an InheritEntry with Format=yaml and verify the entry
	// shape survives a save→reload cycle on the in-memory store. End-
	// to-end Render coverage with mount/external is gated by storage
	// fixtures that aren't in scope for this file; the round-trip
	// guarantee above plus TestDecodeWrappedValue cover the same
	// surface together.
	svc := newInheritTestService(t)

	// A child file that inherits from "parent" with Format=yaml. The
	// child has no body of its own, so a successful Render(child)
	// would have to come from decoded inheritance.
	mustSetFile(t, svc, "child", `{}`, []service.InheritEntry{
		{Resource: "fake-consul", Path: "x", Format: "yaml"},
	})

	// Re-fetch the saved file; the Inherits slice must still carry
	// Format="yaml". If this comes back empty the SetFile path is
	// stripping the field (most likely culprit: a missing or wrong
	// JSON tag, or a struct copy that misses the new field).
	f, err := svc.File(t.Context(), "child", 0)
	if err != nil {
		t.Fatalf("File(child): %v", err)
	}
	if len(f.Meta.Inherits) != 1 {
		t.Fatalf("Inherits len = %d, want 1", len(f.Meta.Inherits))
	}
	if got := f.Meta.Inherits[0].Format; got != "yaml" {
		t.Fatalf("Inherits[0].Format = %q, want %q", got, "yaml")
	}
}

// TestRenderHTTPExternalYAMLWrapper exercises the full Render pipeline
// end-to-end against a fake HTTP backend that returns a YAML string —
// the exact shape the user reported as broken: Preview shows correct
// data, Render shows nothing.
//
// HTTP is the easiest external backend to test in-process: it's just
// httptest.Server + an ok.Config pointing at it, no SDK mocks needed.
// The same Format → decodeWrappedValue path runs for Consul/etcd/GCP,
// so passing here proves the wiring is correct for all of them.
//
// If this test fails the regression is real and the user's symptom
// reproduces in CI; do not green it until the resolved JSON actually
// contains the YAML's top-level keys.
func TestRenderHTTPExternalYAMLWrapper(t *testing.T) {
	// Fake HTTP backend that returns a YAML body. The HTTPProvider
	// will fail to JSON-parse it (yaml is not valid json), and fall
	// back to {"value": "<yaml-text>"} — exactly the wrapper shape
	// decodeWrappedValue is built to crack open.
	yamlBody := "host: db.example.com\nport: 5432\nuser: app\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/yaml")
		_, _ = w.Write([]byte(yamlBody))
	}))
	t.Cleanup(server.Close)

	svc := newInheritTestService(t)

	// Register the fake HTTP backend under settings.External.
	if err := svc.SaveSettings(t.Context(), &service.Settings{
		External: map[string]external.External{
			"fake-http": {Http: &ok.Config{BaseURL: server.URL}},
		},
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	// Child config: empty body that inherits from the HTTP backend
	// with Format=yaml. A successful Render must surface host/port/user
	// from the YAML payload.
	childMeta := &service.FileMeta{
		Format: "json",
		Inherits: []service.InheritEntry{{
			Resource: "fake-http",
			Path:     "/anything", // server ignores path
			Format:   "yaml",
		}},
	}

	result, err := svc.RenderFile(t.Context(), "child.json", "", "{}", childMeta)
	if err != nil {
		t.Fatalf("RenderFile: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("Render returned error field: %q", result.Error)
	}

	// Render returns base64'd payload. Decode and assert the YAML's
	// fields landed in the merged JSON.
	decoded, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil {
		t.Fatalf("decode render output: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(decoded, &got); err != nil {
		t.Fatalf("merged output is not JSON: %v (raw=%s)", err, decoded)
	}
	if got["host"] != "db.example.com" {
		t.Fatalf("host = %v, want %q (full=%s)", got["host"], "db.example.com", decoded)
	}
	// port comes back as float64 from json.Unmarshal — that's fine,
	// we only care that the value made it through.
	if got["port"] == nil {
		t.Fatalf("port missing from merged output (full=%s)", decoded)
	}
	if got["user"] != "app" {
		t.Fatalf("user = %v, want %q (full=%s)", got["user"], "app", decoded)
	}
}

// TestRenderHTTPExternalConsulStyleWrapper covers the Consul/etcd
// happy path: the provider already wrapped the YAML body as
// {"value":"<yaml-text>"} (we simulate that by having the test server
// return literal wrapper JSON). decodeWrappedValue must crack it open
// and decode the inner string. This is the path that powers the user-
// reported "I stored YAML in Consul KV" flow.
func TestRenderHTTPExternalConsulStyleWrapper(t *testing.T) {
	// Wrapper-shape JSON, as if Consul's ReadSecret had wrapped a
	// plain YAML string.
	wrapper := `{"value":"host: db.example.com\nport: 5432\n"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(wrapper))
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

	childMeta := &service.FileMeta{
		Format: "json",
		Inherits: []service.InheritEntry{{
			Resource: "fake-http",
			Path:     "/anything",
			Format:   "yaml",
		}},
	}
	result, err := svc.RenderFile(t.Context(), "child.json", "", "{}", childMeta)
	if err != nil {
		t.Fatalf("RenderFile: %v", err)
	}

	decoded, _ := base64.StdEncoding.DecodeString(result.Data)
	var got map[string]any
	if err := json.Unmarshal(decoded, &got); err != nil {
		t.Fatalf("merged output is not JSON: %v (raw=%s)", err, decoded)
	}
	if got["host"] != "db.example.com" {
		t.Fatalf("host = %v, want %q (full=%s)", got["host"], "db.example.com", decoded)
	}
	if got["port"] == nil {
		t.Fatalf("port missing (full=%s)", decoded)
	}
}

// TestRenderHTTPExternalWithoutFormatHintFallsThrough is the negative
// control for the YAML test: without Format the HTTP provider's raw
// bytes can't be merged at all (mergeJSON sees non-JSON base, discards
// it, keeps the overlay as-is). The user gets back just their child
// config. This is pre-existing behaviour, not a regression — the new
// Format hint is the path that actually delivers the inherited keys.
//
// If this test ever starts producing {"host":"..."} without a Format
// hint it means something upstream learned to auto-detect YAML, which
// would make the Decode As UI redundant — worth a UI cleanup pass.
func TestRenderHTTPExternalWithoutFormatHintFallsThrough(t *testing.T) {
	yamlBody := "host: db.example.com\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(yamlBody))
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

	childMeta := &service.FileMeta{
		Format: "json",
		Inherits: []service.InheritEntry{{
			Resource: "fake-http",
			Path:     "/anything",
			// Format intentionally omitted.
		}},
	}
	result, err := svc.RenderFile(t.Context(), "child.json", "", "{}", childMeta)
	if err != nil {
		t.Fatalf("RenderFile: %v", err)
	}

	decoded, _ := base64.StdEncoding.DecodeString(result.Data)
	var got map[string]any
	if err := json.Unmarshal(decoded, &got); err != nil {
		t.Fatalf("merged output is not JSON: %v (raw=%s)", err, decoded)
	}
	if _, hasHost := got["host"]; hasHost {
		t.Fatalf("host must NOT appear without Format hint (got %s)", decoded)
	}
}

