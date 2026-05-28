package publicendpoint

import (
	"reflect"
	"testing"

	"github.com/rakunlabs/pika/internal/external"
)

// applyEndpointOverrides drives a small state machine (nil / true /
// false × CT empty / set × upstream-Raw / upstream-Data). The cases
// are easy to get wrong and hard to spot at the integration-test
// level because writeExternalEntry hides the Entry shape behind
// HTTP bytes. The tests below exercise every combination directly.

func TestApplyEndpointOverrides_NoOverridePassesThrough(t *testing.T) {
	in := &external.Entry{
		Data:        map[string]any{"value": "hello"},
		Raw:         []byte(`{"value":"hello"}`),
		ContentType: "application/json",
	}
	got := applyEndpointOverrides(in, nil, "")
	if !reflect.DeepEqual(got.Data, in.Data) ||
		string(got.Raw) != string(in.Raw) ||
		got.ContentType != in.ContentType {
		t.Fatalf("expected passthrough, got %+v", got)
	}
}

func TestApplyEndpointOverrides_ContentTypeOnlyOverride(t *testing.T) {
	in := &external.Entry{
		Raw:         []byte("---\nfoo: 1\n"),
		ContentType: "text/plain",
	}
	got := applyEndpointOverrides(in, nil, "application/yaml")
	if got.ContentType != "application/yaml" {
		t.Fatalf("expected ContentType swap, got %q", got.ContentType)
	}
	if string(got.Raw) != string(in.Raw) {
		t.Fatalf("Raw must stay untouched, got %q", got.Raw)
	}
}

func TestApplyEndpointOverrides_ForceRawUnwrapsValueWrapper(t *testing.T) {
	// Provider returned the legacy `{"value":"plain"}` shape with
	// application/json. Endpoint forces raw; we expect the inner
	// "plain" string to come out as Raw + the yaml default.
	in := &external.Entry{
		Data:        map[string]any{"value": "plain yaml: 1\n"},
		Raw:         []byte(`{"value":"plain yaml: 1\n"}`),
		ContentType: "application/json",
	}
	raw := true
	got := applyEndpointOverrides(in, &raw, "")
	if string(got.Raw) != "plain yaml: 1\n" {
		t.Fatalf("expected unwrapped Raw, got %q", got.Raw)
	}
	if got.Data != nil {
		t.Fatalf("Data must be cleared so writeExternalEntry uses Raw, got %+v", got.Data)
	}
	// upstream had application/json; we don't touch ContentType
	// unless it was empty. Operator who wants yaml on raw should
	// set ctOverride explicitly.
	if got.ContentType != "application/json" {
		t.Fatalf("ContentType should be preserved when upstream had one, got %q", got.ContentType)
	}
}

func TestApplyEndpointOverrides_ForceRawDefaultsToYamlWhenUpstreamCTEmpty(t *testing.T) {
	in := &external.Entry{
		Data: map[string]any{"value": "abc"},
	}
	raw := true
	got := applyEndpointOverrides(in, &raw, "")
	if got.ContentType != "application/yaml" {
		t.Fatalf("expected yaml default when upstream CT empty, got %q", got.ContentType)
	}
}

func TestApplyEndpointOverrides_ForceRawWithExplicitCT(t *testing.T) {
	in := &external.Entry{
		Data:        map[string]any{"value": "abc"},
		ContentType: "application/json",
	}
	raw := true
	got := applyEndpointOverrides(in, &raw, "text/plain")
	if got.ContentType != "text/plain" {
		t.Fatalf("expected explicit CT, got %q", got.ContentType)
	}
	if string(got.Raw) != "abc" {
		t.Fatalf("Raw=%q", got.Raw)
	}
}

func TestApplyEndpointOverrides_ForceRawMarshalsMultiKeyData(t *testing.T) {
	// Not a value wrapper — provider returned a real JSON object.
	// Forcing raw should serialise the whole map.
	in := &external.Entry{
		Data:        map[string]any{"a": "1", "b": "2"},
		ContentType: "application/json",
	}
	raw := true
	got := applyEndpointOverrides(in, &raw, "application/json")
	// Map ordering isn't guaranteed; just check it's valid JSON
	// containing both keys and that Data was cleared.
	if got.Data != nil {
		t.Fatalf("Data should be cleared, got %+v", got.Data)
	}
	body := string(got.Raw)
	if body == "" || !containsAll(body, `"a"`, `"b"`, `"1"`, `"2"`) {
		t.Fatalf("expected marshaled map, got %q", body)
	}
}

func TestApplyEndpointOverrides_ForceWrappedRewrapsRawBytes(t *testing.T) {
	// Provider returned raw bytes (e.g. GCP in raw mode). Endpoint
	// forces wrap → we expect {"value":"<bytes>"} JSON with the
	// legacy application/json CT.
	in := &external.Entry{
		Raw:         []byte("plain text"),
		ContentType: "text/plain",
	}
	raw := false
	got := applyEndpointOverrides(in, &raw, "")
	if string(got.Raw) != `{"value":"plain text"}` {
		t.Fatalf("expected re-wrapped JSON, got %q", got.Raw)
	}
	if got.ContentType != "application/json" {
		t.Fatalf("expected application/json default for wrap mode, got %q", got.ContentType)
	}
	if got.Data != nil {
		t.Fatalf("Data should be cleared, got %+v", got.Data)
	}
}

func TestApplyEndpointOverrides_ForceWrappedPreservesExistingData(t *testing.T) {
	in := &external.Entry{
		Data:        map[string]any{"value": "hello"},
		Raw:         []byte(`{"value":"hello"}`),
		ContentType: "application/json",
	}
	raw := false
	got := applyEndpointOverrides(in, &raw, "")
	if string(got.Raw) != `{"value":"hello"}` {
		t.Fatalf("expected preserved wrap, got %q", got.Raw)
	}
	if got.ContentType != "application/json" {
		t.Fatalf("CT=%q", got.ContentType)
	}
}

func TestApplyEndpointOverrides_ForceWrappedWithExplicitCT(t *testing.T) {
	in := &external.Entry{
		Raw:         []byte("hello"),
		ContentType: "text/plain",
	}
	raw := false
	got := applyEndpointOverrides(in, &raw, "application/problem+json")
	if got.ContentType != "application/problem+json" {
		t.Fatalf("expected explicit CT, got %q", got.ContentType)
	}
}

func TestApplyEndpointOverrides_NilEntryReturnsNil(t *testing.T) {
	raw := true
	if got := applyEndpointOverrides(nil, &raw, "text/plain"); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestApplyEndpointOverrides_DoesNotMutateInput(t *testing.T) {
	in := &external.Entry{
		Data:        map[string]any{"value": "x"},
		Raw:         []byte(`{"value":"x"}`),
		ContentType: "application/json",
	}
	raw := true
	_ = applyEndpointOverrides(in, &raw, "text/plain")
	// Original entry should be intact so a cached provider entry
	// shared across requests isn't corrupted.
	if in.Data == nil || in.Data["value"] != "x" {
		t.Fatalf("input Data mutated: %+v", in.Data)
	}
	if string(in.Raw) != `{"value":"x"}` {
		t.Fatalf("input Raw mutated: %q", in.Raw)
	}
	if in.ContentType != "application/json" {
		t.Fatalf("input ContentType mutated: %q", in.ContentType)
	}
}

func containsAll(s string, needles ...string) bool {
	for _, n := range needles {
		found := false
		for i := 0; i+len(n) <= len(s); i++ {
			if s[i:i+len(n)] == n {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
