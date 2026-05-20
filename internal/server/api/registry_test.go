package api

import (
	"testing"

	"github.com/rakunlabs/pika/internal/service"
)

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
