package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/service"
	bwstore "github.com/rakunlabs/pika/internal/storage/bw"
)

func TestHandleRegistersRoutesWithoutGreedyPanic(t *testing.T) {
	store, err := bwstore.New(t.Context(), &bwstore.Config{InMemory: true})
	if err != nil {
		t.Fatalf("bw.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	rh := NewRawHandler(nil, ctx, nil)

	if err := Handle(ada.NewMux(), ada.NewMux(), ada.NewMux(), service.New(store), Info{}, nil, nil, rh, nil, nil); err != nil {
		t.Fatalf("Handle: %v", err)
	}
}

func TestRegistryWildcardRoutesCaptureNestedNames(t *testing.T) {
	m := ada.NewMux()

	var gotPackageName, gotModulePath string
	m.GET("/api/v1/registries/go/{ns}/{repo}/modules", m.Wrap(func(c *ada.Context) error {
		return c.SendNoContent()
	}))
	m.GET("/api/v1/registries/go/{ns}/{repo}/packages/*", m.Wrap(func(c *ada.Context) error {
		gotPackageName = c.Request.PathValue("*")
		return c.SendNoContent()
	}))
	m.GET("/api/v1/registries/go/{ns}/{repo}/modules/*", m.Wrap(func(c *ada.Context) error {
		gotModulePath = c.Request.PathValue("*")
		return c.SendNoContent()
	}))

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/registries/go/default/mirror/packages/example.com/acme/foo", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("package detail route status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if gotPackageName != "example.com/acme/foo" {
		t.Fatalf("package detail wildcard capture = %q, want example.com/acme/foo", gotPackageName)
	}

	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/registries/go/default/mirror/modules/example.com/acme/foo/versions/v1.2.3/gomod", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("go.mod route status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if gotModulePath != "example.com/acme/foo/versions/v1.2.3/gomod" {
		t.Fatalf("go.mod wildcard capture = %q", gotModulePath)
	}
}

func TestParseGoModuleGoModPath(t *testing.T) {
	tests := []struct {
		path        string
		wantName    string
		wantVersion string
		wantOK      bool
	}{
		{
			path:        "example.com/acme/foo/versions/v1.2.3/gomod",
			wantName:    "example.com/acme/foo",
			wantVersion: "v1.2.3",
			wantOK:      true,
		},
		{
			path:        "/example.com/acme/versions/lib/versions/v0.1.0/gomod",
			wantName:    "example.com/acme/versions/lib",
			wantVersion: "v0.1.0",
			wantOK:      true,
		},
		{path: "example.com/mod/versions/v1.0.0/gomod/extra"},
		{path: "example.com/mod/versions//gomod"},
		{path: "example.com/mod/gomod"},
	}

	for _, tt := range tests {
		gotName, gotVersion, gotOK := parseGoModuleGoModPath(tt.path)
		if gotName != tt.wantName || gotVersion != tt.wantVersion || gotOK != tt.wantOK {
			t.Fatalf("parseGoModuleGoModPath(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.path, gotName, gotVersion, gotOK, tt.wantName, tt.wantVersion, tt.wantOK)
		}
	}
}

func TestRegistrySettingsForResponseRedactsSecretsForReadOnly(t *testing.T) {
	rs := &service.RegistrySettings{Namespaces: []service.RegistryNamespace{{
		Name: "default",
		Repositories: []service.RegistryRepository{
			{
				Name: "mirror", Type: service.RegistryTypeGo, Kind: service.RegistryKindRemote,
				URL: "https://proxy.golang.org", Mount: "m", BasePath: "go-cache",
				Auth: &service.RegistryUpstreamAuth{
					Type:     service.RegistryAuthHeader,
					Username: "user-visible",
					Password: "p",
					Token:    "raw://secrets/token",
					Header:   "X-Api-Key",
					Value:    "v",
				},
			},
			{
				Name: "local", Type: service.RegistryTypeDocker, Kind: service.RegistryKindLocal,
				Mount: "m", BasePath: "docker", AllowPush: true,
				Policy: &service.RegistryPolicy{
					ImmutableTags: []string{"prod"},
					Retention: &service.RegistryRetentionPolicy{
						GCMinAgeSeconds:              7200,
						AbandonedUploadMaxAgeSeconds: 300,
					},
				},
			},
		},
	}}}

	got := registrySettingsForResponse(rs, false)
	auth := got.Namespaces[0].Repositories[0].Auth
	if auth.Password != redactedRegistrySecret || auth.Token != redactedRegistrySecret || auth.Value != redactedRegistrySecret {
		t.Fatalf("secrets were not redacted: %+v", auth)
	}
	if auth.Username != "user-visible" || auth.Header != "X-Api-Key" {
		t.Fatalf("non-secret auth fields changed: %+v", auth)
	}
	if rs.Namespaces[0].Repositories[0].Auth.Token != "raw://secrets/token" {
		t.Fatalf("redaction mutated source settings")
	}
	got.Namespaces[0].Repositories[1].Policy.ImmutableTags[0] = "changed"
	if rs.Namespaces[0].Repositories[1].Policy.ImmutableTags[0] != "prod" {
		t.Fatalf("policy clone mutated source settings")
	}
}

func TestRegistrySettingsForResponseIncludesSecretsForAdmin(t *testing.T) {
	rs := &service.RegistrySettings{Namespaces: []service.RegistryNamespace{{
		Name: "default",
		Repositories: []service.RegistryRepository{{
			Name: "mirror", Type: service.RegistryTypeGo, Kind: service.RegistryKindRemote,
			URL: "https://proxy.golang.org", Mount: "m", BasePath: "go-cache",
			Auth: &service.RegistryUpstreamAuth{Type: service.RegistryAuthBearer, Token: "tk_secret"},
		}},
	}}}

	got := registrySettingsForResponse(rs, true)
	if got.Namespaces[0].Repositories[0].Auth.Token != "tk_secret" {
		t.Fatalf("admin response should include secret, got %+v", got.Namespaces[0].Repositories[0].Auth)
	}
}
