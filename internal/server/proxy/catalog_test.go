package proxy

import "testing"

func TestBuildCatalog_StableOrderAndCoverage(t *testing.T) {
	cat := BuildCatalog()
	if len(cat.Middlewares) == 0 || len(cat.Handlers) == 0 {
		t.Fatalf("empty catalog: %+v", cat)
	}
	// Stable order: every subsequent subtype must be >= the previous.
	for i := 1; i < len(cat.Middlewares); i++ {
		if cat.Middlewares[i].Subtype < cat.Middlewares[i-1].Subtype {
			t.Fatalf("middlewares not sorted at %d: %q < %q",
				i, cat.Middlewares[i].Subtype, cat.Middlewares[i-1].Subtype)
		}
	}
	for i := 1; i < len(cat.Handlers); i++ {
		if cat.Handlers[i].Subtype < cat.Handlers[i-1].Subtype {
			t.Fatalf("handlers not sorted at %d: %q < %q",
				i, cat.Handlers[i].Subtype, cat.Handlers[i-1].Subtype)
		}
	}

	// Spot-check that the kinds we ship by contract are present.
	wantMW := []string{"auth-bearer", "cors", "ratelimit", "logger", "basic-auth", "compress"}
	for _, k := range wantMW {
		found := false
		for _, m := range cat.Middlewares {
			if m.Subtype == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("middleware %q missing from catalog", k)
		}
	}
	wantHandlers := []string{"data", "external", "proxy-pass", "consul-kv", "healthz"}
	for _, k := range wantHandlers {
		found := false
		for _, h := range cat.Handlers {
			if h.Subtype == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("handler %q missing from catalog", k)
		}
	}
}
