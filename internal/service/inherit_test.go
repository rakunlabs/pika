package service_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
	bwstore "github.com/rakunlabs/pika/internal/storage/bw"
)

// newInheritTestService spins up an in-memory bw-backed Service for
// inheritance-resolution tests.
func newInheritTestService(t *testing.T) *service.Service {
	t.Helper()
	store, err := bwstore.New(t.Context(), &bwstore.Config{InMemory: true})
	if err != nil {
		t.Fatalf("bw.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return service.New(store)
}

func mustSetFile(t *testing.T, svc *service.Service, path string, body string, inherits []service.InheritEntry) {
	t.Helper()
	if _, err := svc.SetFile(t.Context(), path, &service.File{
		Meta: service.FileMeta{
			Format:   "json",
			Inherits: inherits,
		},
		Data: []byte(body),
	}, nil, ""); err != nil {
		t.Fatalf("SetFile(%q): %v", path, err)
	}
}

// TestInheritsTransitive verifies that A inherits B inherits C composes
// correctly — C's values must surface in A's resolved view. This was
// previously broken: only B's raw payload was merged into A.
func TestInheritsTransitive(t *testing.T) {
	svc := newInheritTestService(t)

	// C is the root: defines `host` and `port`.
	mustSetFile(t, svc, "c", `{"host":"db.example.com","port":5432}`, nil)

	// B inherits from C and overrides port + adds `user`.
	mustSetFile(t, svc, "b", `{"port":6543,"user":"app"}`,
		[]service.InheritEntry{{Source: "c"}})

	// A inherits from B and overrides user + adds `db`.
	mustSetFile(t, svc, "a", `{"user":"admin","db":"prod"}`,
		[]service.InheritEntry{{Source: "b"}})

	got, err := svc.GetData(t.Context(), "a", "0", "")
	if err != nil {
		t.Fatalf("GetData(a): %v", err)
	}

	// Expected merge: host from C, port from B (overrides C), user
	// and db from A (overrides B).
	want := `{"db":"prod","host":"db.example.com","port":6543,"user":"admin"}`
	if string(got.Data) != want {
		t.Fatalf("transitive merge mismatch:\nwant %s\n got %s", want, got.Data)
	}
}

// TestInheritsCycleDetected ensures A -> B -> A errors rather than
// looping forever or stack-overflowing.
func TestInheritsCycleDetected(t *testing.T) {
	svc := newInheritTestService(t)

	mustSetFile(t, svc, "a", `{"from":"a"}`,
		[]service.InheritEntry{{Source: "b"}})
	mustSetFile(t, svc, "b", `{"from":"b"}`,
		[]service.InheritEntry{{Source: "a"}})

	_, err := svc.GetData(t.Context(), "a", "0", "")
	if err == nil {
		t.Fatalf("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got: %v", err)
	}
}

// TestInheritsSelfCycle: a file that directly inherits from itself
// must error.
func TestInheritsSelfCycle(t *testing.T) {
	svc := newInheritTestService(t)

	mustSetFile(t, svc, "self", `{"x":1}`,
		[]service.InheritEntry{{Source: "self"}})

	_, err := svc.GetData(t.Context(), "self", "0", "")
	if err == nil {
		t.Fatalf("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got: %v", err)
	}
}

// TestInheritsDiamondAllowed: A inherits B and C, both inherit D. D
// appears twice in the resolution tree but never on the same ancestor
// chain, so this must succeed (diamond pattern is legal).
func TestInheritsDiamondAllowed(t *testing.T) {
	svc := newInheritTestService(t)

	mustSetFile(t, svc, "d", `{"shared":"D"}`, nil)
	mustSetFile(t, svc, "b", `{"b_key":"B"}`,
		[]service.InheritEntry{{Source: "d"}})
	mustSetFile(t, svc, "c", `{"c_key":"C"}`,
		[]service.InheritEntry{{Source: "d"}})
	mustSetFile(t, svc, "a", `{"a_key":"A"}`,
		[]service.InheritEntry{{Source: "b"}, {Source: "c"}})

	got, err := svc.GetData(t.Context(), "a", "0", "")
	if err != nil {
		t.Fatalf("GetData(a) for diamond: %v", err)
	}

	// All four keys must be present in the resolved output.
	for _, key := range []string{`"shared":"D"`, `"b_key":"B"`, `"c_key":"C"`, `"a_key":"A"`} {
		if !strings.Contains(string(got.Data), key) {
			t.Fatalf("expected %s in resolved output, got: %s", key, got.Data)
		}
	}
}

// TestRenderVariantInheritingParent reproduces the case where a variant
// (e.g., "app/config@prod") is rendered from the UI with the unsaved
// editor content + a single inherit entry pointing back to the parent
// ("app/config"). The cycle guard must seed itself with the variant's
// storage key ("app/config@prod"), not the bare parent path, otherwise
// the legitimate inherit-from-parent setup is mis-detected as a cycle.
func TestRenderVariantInheritingParent(t *testing.T) {
	svc := newInheritTestService(t)

	// Parent file already saved.
	mustSetFile(t, svc, "app/config", `{"host":"base","port":80}`, nil)

	// Render the variant — the UI sends the parent path + the variant
	// key + the editor content + meta containing the auto-injected
	// inherit pointing at the parent. The variant doesn't even need to
	// exist on disk for render.
	result, err := svc.RenderFile(
		t.Context(),
		"app/config",
		"prod",
		`{"port":443}`,
		&service.FileMeta{
			Format:   "json",
			Inherits: []service.InheritEntry{{Source: "app/config"}},
		},
	)
	if err != nil {
		t.Fatalf("RenderFile for variant inheriting parent: %v", err)
	}

	// result.Data is base64 — decode and inspect.
	decoded, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil {
		t.Fatalf("decoding render result: %v", err)
	}
	if !strings.Contains(string(decoded), `"host":"base"`) {
		t.Fatalf("expected host from parent, got: %s", decoded)
	}
	if !strings.Contains(string(decoded), `"port":443`) {
		t.Fatalf("expected variant override port=443, got: %s", decoded)
	}
}

// TestRenderFileSelfCycle: if a non-variant file is rendered with an
// inherit entry pointing at itself, that's a real cycle and must error.
func TestRenderFileSelfCycle(t *testing.T) {
	svc := newInheritTestService(t)

	mustSetFile(t, svc, "loop", `{"x":1}`, nil)

	_, err := svc.RenderFile(
		t.Context(),
		"loop",
		"", // no variant — render the base file
		`{"x":1}`,
		&service.FileMeta{
			Format:   "json",
			Inherits: []service.InheritEntry{{Source: "loop"}},
		},
	)
	if err == nil {
		t.Fatalf("expected cycle error for self-inherit, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got: %v", err)
	}
}

// TestInheritsTransitiveWithFilter: when B selectively filters from C
// via Paths, the filter must still apply transitively from A's view.
func TestInheritsTransitiveWithFilter(t *testing.T) {
	svc := newInheritTestService(t)

	mustSetFile(t, svc, "c", `{"host":"x","secret":"s","port":42}`, nil)
	mustSetFile(t, svc, "b", `{"b":"b"}`,
		[]service.InheritEntry{{Source: "c", Paths: []string{"host", "port"}}})
	mustSetFile(t, svc, "a", `{"a":"a"}`,
		[]service.InheritEntry{{Source: "b"}})

	got, err := svc.GetData(t.Context(), "a", "0", "")
	if err != nil {
		t.Fatalf("GetData(a): %v", err)
	}
	if strings.Contains(string(got.Data), "secret") {
		t.Fatalf("filtered field leaked through transitive inheritance: %s", got.Data)
	}
	for _, key := range []string{`"host":"x"`, `"port":42`, `"b":"b"`, `"a":"a"`} {
		if !strings.Contains(string(got.Data), key) {
			t.Fatalf("expected %s in resolved output, got: %s", key, got.Data)
		}
	}
}
