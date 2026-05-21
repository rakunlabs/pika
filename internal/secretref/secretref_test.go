package secretref

import "testing"

func TestSplit(t *testing.T) {
	loc, selector, ok, err := Split("secrets/app#/registry/token")
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if loc != "secrets/app" || selector != "/registry/token" || !ok {
		t.Fatalf("Split = (%q, %q, %v), want location, selector, true", loc, selector, ok)
	}
}

func TestSelectJSONPointer(t *testing.T) {
	got, err := Select([]byte(`{"registry":{"token":"abc"}}`), "/registry/token")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got != "abc" {
		t.Fatalf("Select = %q, want abc", got)
	}
}

func TestSelectArrayIndex(t *testing.T) {
	got, err := Select([]byte(`{"registry":{"tokens":["first","second"]}}`), "/registry/tokens/1")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got != "second" {
		t.Fatalf("Select = %q, want second", got)
	}
}

func TestSelectYAML(t *testing.T) {
	got, err := Select([]byte("registry:\n  token: yaml-token\n"), "/registry/token")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got != "yaml-token" {
		t.Fatalf("Select = %q, want yaml-token", got)
	}
}

func TestSelectTOML(t *testing.T) {
	got, err := Select([]byte("[registry]\ntoken = \"toml-token\"\n"), "/registry/token")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got != "toml-token" {
		t.Fatalf("Select = %q, want toml-token", got)
	}
}

func TestSelectRejectsSubtree(t *testing.T) {
	if _, err := Select([]byte(`{"registry":{"token":"abc"}}`), "/registry"); err == nil {
		t.Fatalf("Select subtree should fail")
	}
}

func TestSelectRejectsDotPath(t *testing.T) {
	if _, err := Select([]byte(`{"registry":{"token":"abc"}}`), "registry.token"); err == nil {
		t.Fatalf("Select dot path should fail")
	}
}
