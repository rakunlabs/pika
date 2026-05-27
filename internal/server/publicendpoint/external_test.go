package publicendpoint

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/service"
)

func TestExternalMode_ReadsSelectedResource(t *testing.T) {
	stub := &stubService{externals: map[string]map[string]*external.Entry{
		"prod-vault": {
			"apps/api/db": {
				Raw:         []byte(`{"user":"pika"}`),
				ContentType: "application/json",
			},
		},
	}}
	port := freePort(t)
	ep := service.PublicEndpoint{
		ID: "ext1", Name: "ext", Enabled: true,
		ListenHost: "127.0.0.1", ListenPort: port,
		BasePath: "/vault", Mode: "external",
		External: &service.ExternalCompat{Resource: "prod-vault"},
		Auth:     service.EndpointAuth{Mode: "none"},
	}
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}

	body, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/vault/apps/api/db", port), nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", status, body)
	}
	if string(body) != `{"user":"pika"}` {
		t.Errorf("body=%q", body)
	}
}

func TestExternalMode_VersionQueryUsesReadExternalVersion(t *testing.T) {
	stub := &stubService{externals: map[string]map[string]*external.Entry{
		"vault": {
			"secret/api@2": {Raw: []byte(`v2`), ContentType: "text/plain"},
		},
	}}
	port := freePort(t)
	ep := service.PublicEndpoint{
		ID: "ext2", Name: "ext", Enabled: true,
		ListenHost: "127.0.0.1", ListenPort: port,
		BasePath: "/", Mode: "external",
		External: &service.ExternalCompat{Resource: "vault"},
		Auth:     service.EndpointAuth{Mode: "none"},
	}
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}

	body, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/secret/api?version=2", port), nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", status, body)
	}
	if string(body) != "v2" {
		t.Errorf("body=%q", body)
	}
}

func TestExternalMode_RequestRulesRewriteExternalPath(t *testing.T) {
	stub := &stubService{externals: map[string]map[string]*external.Entry{
		"vault": {
			"apps/api/db": {Raw: []byte(`ok`), ContentType: "text/plain"},
		},
	}}
	port := freePort(t)
	ep := service.PublicEndpoint{
		ID: "ext3", Name: "ext", Enabled: true,
		ListenHost: "127.0.0.1", ListenPort: port,
		BasePath: "/", Mode: "external",
		External: &service.ExternalCompat{Resource: "vault"},
		Auth:     service.EndpointAuth{Mode: "none"},
		RequestCheck: &service.RequestCheck{Rules: []service.RequestRule{
			{
				Enabled: true,
				When:    service.RequestMatch{PathPrefix: "/legacy/"},
				Then: service.RequestAction{
					Type:    "replace_path",
					Pattern: "^/legacy/(.*)$",
					Value:   "/$1",
				},
			},
		}},
	}
	mgr := New(t.Context(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(t.Context()) })
	if err := mgr.Reload(t.Context(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}

	body, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/legacy/apps/api/db", port), nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", status, body)
	}
	if strings.TrimSpace(string(body)) != "ok" {
		t.Errorf("body=%q", body)
	}
}
