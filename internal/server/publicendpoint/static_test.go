package publicendpoint

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
)

// TestStaticMode_HappyPath — static mode resolves the path tail as
// a config key and writes the resolved bytes directly, like /data/*.
func TestStaticMode_HappyPath(t *testing.T) {
	stub := &stubService{files: map[string]stubFile{
		"myapp/config": {data: []byte("database:\n  host: localhost\n"), format: "yaml"},
	}}
	port := freePort(t)
	ep := service.PublicEndpoint{
		ID: "s1", Name: "ok", Enabled: true,
		ListenHost: "127.0.0.1", ListenPort: port,
		BasePath: "/cfg", Mode: "static",
		Static: &service.StaticCompat{},
		Auth:   service.EndpointAuth{Mode: "none"},
	}
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/cfg/myapp/config", port)
	body, status := httpGet(t, url, nil)
	if status != http.StatusOK {
		t.Errorf("status=%d body=%s", status, body)
	}
	if !strings.Contains(string(body), "host: localhost") {
		t.Errorf("body=%q", body)
	}
}

// TestStaticMode_RootBasePathSupportsDeepKeys — with base_path=/,
// the whole URL path becomes the config key, matching /data/{path}.
func TestStaticMode_RootBasePathSupportsDeepKeys(t *testing.T) {
	stub := &stubService{files: map[string]stubFile{
		"deep/path/here": {data: []byte("ok"), format: "raw"},
	}}
	port := freePort(t)
	ep := service.PublicEndpoint{
		ID: "s2", Name: "ok", Enabled: true,
		ListenHost: "127.0.0.1", ListenPort: port,
		BasePath: "/", Mode: "static",
		Static: &service.StaticCompat{},
		Auth:   service.EndpointAuth{Mode: "none"},
	}
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}

	body, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/deep/path/here", port), nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", status, body)
	}
	if string(body) != "ok" {
		t.Errorf("body=%q", body)
	}
}

// TestStaticMode_FormatOverride — ?format= uses the same conversion
// path as /data/*.
func TestStaticMode_FormatOverride(t *testing.T) {
	stub := &stubService{files: map[string]stubFile{
		"app/json": {data: []byte(`{"port":8080}`), format: "json"},
	}}
	port := freePort(t)
	ep := service.PublicEndpoint{
		ID: "s3", Name: "ok", Enabled: true,
		ListenHost: "127.0.0.1", ListenPort: port,
		BasePath: "/", Mode: "static",
		Static: &service.StaticCompat{},
		Auth:   service.EndpointAuth{Mode: "none"},
	}
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}

	resp, body := doRequest(t, http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/app/json?format=yaml", port), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-yaml") {
		t.Errorf("Content-Type=%q", ct)
	}
	if !strings.Contains(string(body), "port: 8080") {
		t.Errorf("converted body=%q", body)
	}
}

// TestStaticMode_NotFound — missing config key maps to 404.
func TestStaticMode_NotFound(t *testing.T) {
	stub := &stubService{files: map[string]stubFile{}}
	port := freePort(t)
	ep := service.PublicEndpoint{
		ID: "s4", Name: "ok", Enabled: true,
		ListenHost: "127.0.0.1", ListenPort: port,
		BasePath: "/", Mode: "static",
		Static: &service.StaticCompat{},
		Auth:   service.EndpointAuth{Mode: "none"},
	}
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}

	_, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/missing", port), nil)
	if status != http.StatusNotFound {
		t.Errorf("expected 404, got %d", status)
	}
}

// TestStaticMode_AuthStillApplies — auth runs before the shim,
// even for the direct config response.
func TestStaticMode_AuthStillApplies(t *testing.T) {
	stub := &stubService{files: map[string]stubFile{"hello": {data: []byte("world"), format: "raw"}}}
	port := freePort(t)
	ep := service.PublicEndpoint{
		ID: "s5", Name: "ok", Enabled: true,
		ListenHost: "127.0.0.1", ListenPort: port,
		BasePath: "/", Mode: "static",
		Static: &service.StaticCompat{},
		Auth: service.EndpointAuth{
			Mode:         "static_token",
			StaticTokens: []string{"good"},
			HeaderName:   "X-Tok",
		},
	}
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/hello", port)

	_, status := httpGet(t, url, nil)
	if status != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", status)
	}
	body, status := httpGet(t, url, http.Header{"X-Tok": []string{"good"}})
	if status != http.StatusOK {
		t.Errorf("expected 200 with token, got %d", status)
	}
	if string(body) != "world" {
		t.Errorf("body=%q", body)
	}
}

func TestStaticMode_StaticTokenDefaultHeader(t *testing.T) {
	stub := &stubService{files: map[string]stubFile{"hello": {data: []byte("world"), format: "raw"}}}
	port := freePort(t)
	ep := service.PublicEndpoint{
		ID: "s-default-token", Name: "ok", Enabled: true,
		ListenHost: "127.0.0.1", ListenPort: port,
		BasePath: "/", Mode: "static",
		Static: &service.StaticCompat{},
		Auth: service.EndpointAuth{
			Mode:         "static_token",
			StaticTokens: []string{"good"},
		},
	}
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/hello", port)

	_, status := httpGet(t, url, http.Header{"Authorization": []string{"good"}})
	if status != http.StatusUnauthorized {
		t.Errorf("expected 401 with old default Authorization header, got %d", status)
	}
	body, status := httpGet(t, url, http.Header{"X-Pika-Token": []string{"good"}})
	if status != http.StatusOK {
		t.Errorf("expected 200 with X-Pika-Token, got %d", status)
	}
	if string(body) != "world" {
		t.Errorf("body=%q", body)
	}
}

// TestStaticMode_RequestRulesStillApply — request rules run before
// the static shim, so a blocking rule prevents data resolution.
func TestStaticMode_RequestRulesStillApply(t *testing.T) {
	stub := &stubService{files: map[string]stubFile{"hello": {data: []byte("world"), format: "raw"}}}
	port := freePort(t)
	ep := service.PublicEndpoint{
		ID: "s6", Name: "ok", Enabled: true,
		ListenHost: "127.0.0.1", ListenPort: port,
		BasePath: "/", Mode: "static",
		Static: &service.StaticCompat{},
		Auth:   service.EndpointAuth{Mode: "none"},
		RequestCheck: &service.RequestCheck{Rules: []service.RequestRule{
			{
				Name:    "require tenant",
				Enabled: true,
				When:    service.RequestMatch{HeaderAbsent: "X-Tenant"},
				Then: service.RequestAction{
					Type:        "block",
					Status:      401,
					Body:        "missing tenant",
					ContentType: "text/plain",
				},
			},
		}},
	}
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/hello", port)

	body, status := httpGet(t, url, nil)
	if status != http.StatusUnauthorized {
		t.Errorf("expected 401 without tenant, got %d", status)
	}
	if string(body) != "missing tenant" {
		t.Errorf("block body=%q", body)
	}
	body, status = httpGet(t, url, http.Header{"X-Tenant": []string{"prod"}})
	if status != http.StatusOK {
		t.Errorf("expected 200 with tenant, got %d", status)
	}
	if string(body) != "world" {
		t.Errorf("static body=%q", body)
	}
}

// TestStaticMode_RejectsNonGET — POST/PUT etc. -> 405.
func TestStaticMode_RejectsNonGET(t *testing.T) {
	stub := &stubService{files: map[string]stubFile{"hello": {data: []byte("world"), format: "raw"}}}
	port := freePort(t)
	ep := service.PublicEndpoint{
		ID: "s7", Name: "ok", Enabled: true,
		ListenHost: "127.0.0.1", ListenPort: port,
		BasePath: "/", Mode: "static",
		Static: &service.StaticCompat{},
		Auth:   service.EndpointAuth{Mode: "none"},
	}
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	resp, body := doRequest(t, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/hello", port), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for POST, got %d body=%s", resp.StatusCode, body)
	}
}

func doRequest(t *testing.T, method, url string, hdr http.Header) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}
