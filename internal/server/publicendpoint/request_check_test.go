package publicendpoint

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
)

// rulesEndpoint builds a custom-mode endpoint whose shim echoes
// the post-modify (key, variant, body) so tests can assert what
// the shim actually saw after the rule list ran.
func rulesEndpoint(t *testing.T, rules []service.RequestRule) (service.PublicEndpoint, *stubService) {
	t.Helper()
	stub := &stubService{
		files: map[string]stubFile{
			"hello":              {data: []byte("world"), format: "raw"},
			"prod":               {data: []byte("prod-only"), format: "raw"},
			"myapp_config":       {data: []byte("underscore"), format: "raw"},
			"legacy/1-2-3-4-5-6": {data: []byte("dashes"), format: "raw"},
		},
	}
	port := freePort(t)
	ep := service.PublicEndpoint{
		ID: "rc", Name: "rc", Enabled: true,
		ListenHost: "127.0.0.1", ListenPort: port,
		BasePath: "/", Mode: "custom",
		Custom: &service.CustomCompat{
			BodyTemplate: `{"key":"{{ .Key }}","variant":"{{ .Variant }}","data":"{{ .DataString }}"}`,
			ContentType:  "application/json",
		},
		Auth:         service.EndpointAuth{Mode: "none"},
		RequestCheck: &service.RequestCheck{Rules: rules},
	}
	return ep, stub
}

func TestRequestRules_EmptyListFallsThrough(t *testing.T) {
	ep, stub := rulesEndpoint(t, nil)
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	body, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/hello", ep.ListenPort), nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"data":"world"`) {
		t.Fatalf("expected default-allow path, got status=%d body=%s", status, body)
	}
}

func TestRequestRules_BlockOnMissingHeader(t *testing.T) {
	rules := []service.RequestRule{
		{
			Name: "require tenant", Enabled: true,
			When: service.RequestMatch{HeaderAbsent: "X-Tenant"},
			Then: service.RequestAction{Type: "block", Status: 401, Body: "missing tenant"},
		},
	}
	ep, stub := rulesEndpoint(t, rules)
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/hello", ep.ListenPort)

	body, status := httpGet(t, url, nil)
	if status != http.StatusUnauthorized {
		t.Errorf("missing tenant expected 401, got %d body=%s", status, body)
	}
	if string(body) != "missing tenant" {
		t.Errorf("expected custom body, got %q", body)
	}

	_, status = httpGet(t, url, http.Header{"X-Tenant": []string{"acme"}})
	if status != http.StatusOK {
		t.Errorf("with tenant expected 200, got %d", status)
	}
}

func TestRequestRules_BlockOnHeaderEquals(t *testing.T) {
	rules := []service.RequestRule{
		{
			Name: "deny banned", Enabled: true,
			When: service.RequestMatch{
				HeaderEquals: &service.HeaderMatch{Name: "X-Tenant", Value: "banned"},
			},
			Then: service.RequestAction{Type: "block", Status: 403},
		},
	}
	ep, stub := rulesEndpoint(t, rules)
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/hello", ep.ListenPort)

	_, status := httpGet(t, url, http.Header{"X-Tenant": []string{"banned"}})
	if status != http.StatusForbidden {
		t.Errorf("expected 403 for banned tenant, got %d", status)
	}
	_, status = httpGet(t, url, http.Header{"X-Tenant": []string{"good"}})
	if status != http.StatusOK {
		t.Errorf("expected 200 for good tenant, got %d", status)
	}
}

func TestRequestRules_SetQuery_AffectsShim(t *testing.T) {
	rules := []service.RequestRule{
		{
			Enabled: true,
			Then:    service.RequestAction{Type: "set_query", Name: "variant", Value: "prod"},
		},
	}
	ep, stub := rulesEndpoint(t, rules)
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	body, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/hello", ep.ListenPort), nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"variant":"prod"`) {
		t.Errorf("expected variant=prod in shim view, got status=%d body=%s", status, body)
	}
}

func TestRequestRules_SetPath_RewritesKey(t *testing.T) {
	rules := []service.RequestRule{
		{
			Enabled: true,
			When:    service.RequestMatch{PathEquals: "/hello"},
			Then:    service.RequestAction{Type: "set_path", Value: "/prod"},
		},
	}
	ep, stub := rulesEndpoint(t, rules)
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	body, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/hello", ep.ListenPort), nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if !strings.Contains(string(body), `"key":"prod"`) ||
		!strings.Contains(string(body), `"data":"prod-only"`) {
		t.Errorf("path rewrite did not reach shim: %s", body)
	}
}

func TestRequestRules_ReplacePathRegex_RewritesKey(t *testing.T) {
	rules := []service.RequestRule{
		{
			Enabled: true,
			When:    service.RequestMatch{PathPrefix: "/legacy/"},
			Then: service.RequestAction{
				Type:    "replace_path",
				Pattern: "^/legacy/(.*)$",
				Value:   "/$1",
			},
		},
	}
	ep, stub := rulesEndpoint(t, rules)
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	body, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/legacy/prod", ep.ListenPort), nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if !strings.Contains(string(body), `"key":"prod"`) ||
		!strings.Contains(string(body), `"data":"prod-only"`) {
		t.Errorf("regex path rewrite did not reach shim: %s", body)
	}
}

func TestRequestRules_MultipleActions_PathTransforms(t *testing.T) {
	rules := []service.RequestRule{
		{
			Enabled: true,
			When:    service.RequestMatch{PathPrefix: "/legacy/"},
			Actions: []service.RequestAction{
				{
					Type:    "replace_path",
					Pattern: "^/legacy/(.*)$",
					Value:   "/${1}",
				},
				{
					Type:    "replace_path",
					Pattern: `([^/]+)/`,
					Value:   `${1}_`,
				},
			},
		},
	}
	ep, stub := rulesEndpoint(t, rules)
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	body, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/legacy/myapp/config", ep.ListenPort), nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if !strings.Contains(string(body), `"key":"myapp_config"`) ||
		!strings.Contains(string(body), `"data":"underscore"`) {
		t.Errorf("multi-action path transform did not reach shim: %s", body)
	}
}

func TestRequestRules_ReplacePathCaptureTransform(t *testing.T) {
	rules := []service.RequestRule{
		{
			Enabled: true,
			When:    service.RequestMatch{PathPrefix: "/legacy/"},
			Then: service.RequestAction{
				Type:    "replace_path",
				Pattern: `^/legacy/(?P<tail>.*)$`,
				Value:   "/legacy/${tail}",
				CaptureTransforms: []service.CaptureTransform{
					{Capture: "tail", Find: "/", Value: "-"},
				},
			},
		},
	}
	ep, stub := rulesEndpoint(t, rules)
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	body, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/legacy/1/2/3/4/5/6", ep.ListenPort), nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if !strings.Contains(string(body), `"key":"legacy/1-2-3-4-5-6"`) ||
		!strings.Contains(string(body), `"data":"dashes"`) {
		t.Errorf("capture transform did not reach shim: %s", body)
	}
}

func TestRequestRules_DryRunTrace(t *testing.T) {
	rc := &service.RequestCheck{Rules: []service.RequestRule{
		{
			Name:    "legacy rewrite",
			Enabled: true,
			When:    service.RequestMatch{PathPrefix: "/legacy/"},
			Actions: []service.RequestAction{
				{Type: "replace_path", Pattern: "^/legacy/(.*)$", Value: "/${1}"},
				{Type: "set_query", Name: "variant", Value: "prod"},
			},
		},
	}}

	result, err := TestRequestRules(rc, "GET", "/legacy/myapp/config?debug=1", map[string]string{"X-Tenant": "acme"})
	if err != nil {
		t.Fatalf("test rules: %v", err)
	}
	if result.Terminal != requestRuleTerminalDefaultAllow {
		t.Fatalf("terminal=%q", result.Terminal)
	}
	if result.Initial.Path != "/legacy/myapp/config" || result.Final.Path != "/myapp/config" {
		t.Fatalf("path initial=%q final=%q", result.Initial.Path, result.Final.Path)
	}
	if result.Final.RawQuery != "debug=1&variant=prod" {
		t.Fatalf("raw_query=%q", result.Final.RawQuery)
	}
	if got := result.Final.Headers["X-Tenant"]; got != "acme" {
		t.Fatalf("header X-Tenant=%q", got)
	}
	if len(result.MatchedRules) != 1 {
		t.Fatalf("matched_rules=%d", len(result.MatchedRules))
	}
	trace := result.MatchedRules[0]
	if trace.RuleIndex != 0 || trace.RuleName != "legacy rewrite" || len(trace.Actions) != 2 {
		t.Fatalf("trace=%+v", trace)
	}
	if trace.Actions[0].BeforePath != "/legacy/myapp/config" || trace.Actions[0].AfterPath != "/myapp/config" {
		t.Fatalf("rewrite trace=%+v", trace.Actions[0])
	}
	if trace.Actions[1].QueryName != "variant" || trace.Actions[1].QueryAfter != "prod" {
		t.Fatalf("query trace=%+v", trace.Actions[1])
	}
}

func TestRequestRules_DryRunTraceCaptureTransform(t *testing.T) {
	rc := &service.RequestCheck{Rules: []service.RequestRule{
		{
			Enabled: true,
			When:    service.RequestMatch{PathPrefix: "/legacy/"},
			Then: service.RequestAction{
				Type:    "replace_path",
				Pattern: `^/legacy/(.*)$`,
				Value:   "/legacy/${1}",
				CaptureTransforms: []service.CaptureTransform{
					{Capture: "1", Find: "/", Value: "-"},
				},
			},
		},
	}}

	result, err := TestRequestRules(rc, "GET", "/legacy/1/2/3/4/5/6", nil)
	if err != nil {
		t.Fatalf("test rules: %v", err)
	}
	if result.Final.Path != "/legacy/1-2-3-4-5-6" {
		t.Fatalf("final path=%q", result.Final.Path)
	}
	if len(result.MatchedRules) != 1 || len(result.MatchedRules[0].Actions) != 1 {
		t.Fatalf("matched rules=%+v", result.MatchedRules)
	}
	if got := result.MatchedRules[0].Actions[0].AfterPath; got != "/legacy/1-2-3-4-5-6" {
		t.Fatalf("after path=%q", got)
	}
}

func TestRequestRules_DryRunTraceBlock(t *testing.T) {
	rc := &service.RequestCheck{Rules: []service.RequestRule{
		{
			Enabled: true,
			When:    service.RequestMatch{HeaderAbsent: "X-Tenant"},
			Then: service.RequestAction{
				Type: "block", Status: http.StatusUnauthorized,
				Body: "missing tenant", ContentType: "text/plain",
			},
		},
	}}

	result, err := TestRequestRules(rc, "GET", "/myapp/config", nil)
	if err != nil {
		t.Fatalf("test rules: %v", err)
	}
	if result.Terminal != requestRuleTerminalBlock {
		t.Fatalf("terminal=%q", result.Terminal)
	}
	if result.Block == nil || result.Block.Status != http.StatusUnauthorized || result.Block.Body != "missing tenant" {
		t.Fatalf("block=%+v", result.Block)
	}
	if len(result.MatchedRules) != 1 || len(result.MatchedRules[0].Actions) != 1 {
		t.Fatalf("matched_rules=%+v", result.MatchedRules)
	}
	if !result.MatchedRules[0].Actions[0].Terminal {
		t.Fatalf("block action should be terminal: %+v", result.MatchedRules[0].Actions[0])
	}
}

func TestRequestRules_StackedModifyThenTerminal(t *testing.T) {
	// First rule modifies request, second rule blocks based on a
	// header that didn't exist on the wire — only present after
	// the first rule set it. Demonstrates that modify rules don't
	// terminate evaluation.
	rules := []service.RequestRule{
		{
			Enabled: true,
			Then:    service.RequestAction{Type: "set_header", Name: "X-Internal", Value: "y"},
		},
		{
			Enabled: true,
			When:    service.RequestMatch{HeaderPresent: "X-Internal"},
			Then:    service.RequestAction{Type: "block", Status: 418, Body: "teapot"},
		},
	}
	ep, stub := rulesEndpoint(t, rules)
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	_, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/hello", ep.ListenPort), nil)
	if status != http.StatusTeapot {
		t.Errorf("expected 418 after stacked set+block, got %d", status)
	}
}

func TestRequestRules_AllowShortCircuits(t *testing.T) {
	// First rule allows on a header match; second rule (block all)
	// must NOT run.
	rules := []service.RequestRule{
		{
			Enabled: true,
			When:    service.RequestMatch{HeaderEquals: &service.HeaderMatch{Name: "X-Admin", Value: "yes"}},
			Then:    service.RequestAction{Type: "allow"},
		},
		{
			Enabled: true,
			Then:    service.RequestAction{Type: "block", Status: 403, Body: "no"},
		},
	}
	ep, stub := rulesEndpoint(t, rules)
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/hello", ep.ListenPort)

	// admin → 200 (first rule fires, second never runs)
	_, status := httpGet(t, url, http.Header{"X-Admin": []string{"yes"}})
	if status != http.StatusOK {
		t.Errorf("admin path expected 200, got %d", status)
	}
	// no admin → second rule blocks
	_, status = httpGet(t, url, nil)
	if status != http.StatusForbidden {
		t.Errorf("non-admin path expected 403, got %d", status)
	}
}

func TestRequestRules_DisabledRuleSkipped(t *testing.T) {
	rules := []service.RequestRule{
		{
			Enabled: false, // would block everything if enabled
			Then:    service.RequestAction{Type: "block", Status: 403, Body: "blocked"},
		},
	}
	ep, stub := rulesEndpoint(t, rules)
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	_, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/hello", ep.ListenPort), nil)
	if status != http.StatusOK {
		t.Errorf("expected 200 with disabled rule, got %d", status)
	}
}

func TestRequestRules_RunsAfterAuth(t *testing.T) {
	// Auth-first: if auth fails the rule list never runs.
	stub := &stubService{files: map[string]stubFile{"hello": {data: []byte("w"), format: "raw"}}}
	port := freePort(t)
	ep := service.PublicEndpoint{
		ID: "rc-auth", Name: "rc-auth", Enabled: true,
		ListenHost: "127.0.0.1", ListenPort: port,
		BasePath: "/", Mode: "custom",
		Custom: &service.CustomCompat{
			BodyTemplate: `ok`, ContentType: "text/plain",
		},
		Auth: service.EndpointAuth{
			Mode:         "static_token",
			StaticTokens: []string{"good"},
			HeaderName:   "X-Tok",
		},
		RequestCheck: &service.RequestCheck{
			Rules: []service.RequestRule{
				{
					Enabled: true,
					Then:    service.RequestAction{Type: "block", Status: 418, Body: "teapot"},
				},
			},
		},
	}
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/hello", port)

	// No token → 401 (auth wins; rule never runs)
	_, status := httpGet(t, url, nil)
	if status != http.StatusUnauthorized {
		t.Errorf("expected 401 (auth-first), got %d", status)
	}

	// Good token → 418 (auth passed, then block rule fires)
	_, status = httpGet(t, url, http.Header{"X-Tok": []string{"good"}})
	if status != http.StatusTeapot {
		t.Errorf("expected 418 after auth+block, got %d", status)
	}
}
