package secret

import (
	"testing"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/service"
)

// TestExtractInjectRoundTrip — every secret-bearing slot we extract
// must come back identical after an inject pass.
func TestExtractInjectRoundTrip(t *testing.T) {
	original := buildFixture()
	working := buildFixture()

	payload := extractSecrets(working)

	// After extract: all secret slots in `working` are blank.
	if working.External["my-vault"].Vault.Token != "" {
		t.Errorf("Vault token not cleared after extract")
	}
	if working.Hooks[0].Targets[0].Redis.Password != "" {
		t.Errorf("Redis hook password not cleared after extract")
	}

	// Round-trip: inject back, compare to the untouched original.
	injectSecrets(working, payload)

	if working.External["my-vault"].Vault.Token != original.External["my-vault"].Vault.Token {
		t.Errorf("Vault token lost in round-trip")
	}
	if working.External["aws-prod"].AWS.SecretKey != original.External["aws-prod"].AWS.SecretKey {
		t.Errorf("AWS secret key lost in round-trip")
	}
	if working.Hooks[0].Targets[0].Redis.Password != original.Hooks[0].Targets[0].Redis.Password {
		t.Errorf("Redis hook password lost in round-trip")
	}
}

// TestPublicFieldsPreserved — the extract pass must leave every
// non-secret field alone.
func TestPublicFieldsPreserved(t *testing.T) {
	s := buildFixture()
	original := buildFixture()
	_ = extractSecrets(s)

	if s.External["my-vault"].Vault.Address != original.External["my-vault"].Vault.Address {
		t.Errorf("Vault address mutated by extract")
	}
	if s.Hooks[0].Targets[0].Redis.Address != original.Hooks[0].Targets[0].Redis.Address {
		t.Errorf("Redis hook address mutated by extract")
	}
}

// TestEmptyPayloadDetection — a settings row with no secrets must
// produce an "empty" payload.
func TestEmptyPayloadDetection(t *testing.T) {
	empty := &service.Settings{}
	p := extractSecrets(empty)
	if !isEmptyPayload(p) {
		t.Errorf("isEmptyPayload returned false for secrets-free settings")
	}

	withSecret := buildFixture()
	p2 := extractSecrets(withSecret)
	if isEmptyPayload(p2) {
		t.Errorf("isEmptyPayload returned true for fixture with secrets")
	}
}

func buildFixture() *service.Settings {
	return &service.Settings{
		External: map[string]external.External{
			"my-vault": {
				Vault: &external.Vault{
					Address: "https://vault.example.com",
					Mount:   "secret",
					Token:   "hvs.fake",
				},
			},
			"aws-prod": {
				AWS: &external.AWS{
					Region:    "eu-west-1",
					AccessKey: "AKIA-PUB",
					SecretKey: "aws-secret-zzz",
				},
			},
		},
		Hooks: []hook.Hook{
			{
				Name:    "redis-fanout",
				Enabled: true,
				Targets: []hook.Target{
					{
						Type: "redis",
						Redis: &hook.RedisTarget{
							Address:  "redis:6379",
							Password: "redis-pass",
							Channel:  "events",
						},
					},
				},
			},
		},
		Auth: &service.AuthSettings{
			OAuth2: []service.OAuth2StrategySettings{
				{
					Name:         "google",
					IssuerURL:    "https://accounts.google.com",
					ClientID:     "google-client-id",
					ClientSecret: "google-client-secret",
				},
				{
					Name:         "github",
					IssuerURL:    "https://github.com",
					ClientID:     "gh-client-id",
					ClientSecret: "gh-client-secret",
				},
			},
			LDAP: &service.LDAPStrategySettings{
				Name:         "corp-ldap",
				Addr:         "ldap.example.com:389",
				BindDN:       "cn=admin,dc=example,dc=com",
				BindPassword: "ldap-bind-secret",
			},
		},
	}
}

// TestPublicEndpointTokenRoundTrip — static auth tokens on a
// PublicEndpoint must be stripped on extract and re-attached on
// inject, keyed by endpoint ID so reordering the list is safe.
func TestPublicEndpointTokenRoundTrip(t *testing.T) {
	working := &service.Settings{
		PublicEndpoints: []service.PublicEndpoint{
			{
				ID: "a", Name: "alpha", Enabled: true,
				ListenHost: "127.0.0.1", ListenPort: 9090, BasePath: "/a",
				Mode: "consul", Consul: &service.ConsulCompat{},
				Auth: service.EndpointAuth{
					Mode:         "static_token",
					StaticTokens: []string{"alpha-1", "alpha-2"},
				},
			},
			{
				ID: "b", Name: "beta", Enabled: true,
				ListenHost: "0.0.0.0", ListenPort: 9091, BasePath: "/b",
				Mode: "consul", Consul: &service.ConsulCompat{},
				Auth: service.EndpointAuth{Mode: "none"},
			},
		},
	}

	payload := extractSecrets(working)

	if working.PublicEndpoints[0].Auth.StaticTokens != nil {
		t.Errorf("static tokens not cleared after extract")
	}
	if got := payload.PublicEndpointStaticTokens["a"]; len(got) != 2 ||
		got[0] != "alpha-1" || got[1] != "alpha-2" {
		t.Errorf("payload tokens for a wrong: %v", got)
	}
	if _, ok := payload.PublicEndpointStaticTokens["b"]; ok {
		t.Errorf("non-static-mode endpoint should not appear in payload")
	}

	// Simulate the operator reordering the list between extract and
	// inject — the ID-keyed map must still steer tokens to the
	// right entry.
	working.PublicEndpoints[0], working.PublicEndpoints[1] = working.PublicEndpoints[1], working.PublicEndpoints[0]

	injectSecrets(working, payload)

	var alpha *service.PublicEndpoint
	for i := range working.PublicEndpoints {
		if working.PublicEndpoints[i].ID == "a" {
			alpha = &working.PublicEndpoints[i]
		}
	}
	if alpha == nil {
		t.Fatalf("missing endpoint a after inject")
	}
	if len(alpha.Auth.StaticTokens) != 2 ||
		alpha.Auth.StaticTokens[0] != "alpha-1" ||
		alpha.Auth.StaticTokens[1] != "alpha-2" {
		t.Errorf("tokens lost after reorder + inject: %v", alpha.Auth.StaticTokens)
	}
	if isEmptyPayload(payload) {
		t.Errorf("isEmptyPayload returned true for payload with tokens")
	}
}

// TestAuthSecretRoundTrip — OAuth2 ClientSecret and LDAP BindPassword
// must survive an extract→inject pass identically.
func TestAuthSecretRoundTrip(t *testing.T) {
	working := buildFixture()
	original := buildFixture()

	payload := extractSecrets(working)

	if working.Auth.OAuth2[0].ClientSecret != "" || working.Auth.OAuth2[1].ClientSecret != "" {
		t.Errorf("OAuth2 client secret not cleared after extract")
	}
	if working.Auth.LDAP.BindPassword != "" {
		t.Errorf("LDAP bind password not cleared after extract")
	}
	if len(payload.OAuth2ClientSecrets) != 2 {
		t.Fatalf("payload OAuth2ClientSecrets len=%d, want 2", len(payload.OAuth2ClientSecrets))
	}

	injectSecrets(working, payload)

	if working.Auth.OAuth2[0].ClientSecret != original.Auth.OAuth2[0].ClientSecret {
		t.Errorf("OAuth2 secret #0 lost in round-trip")
	}
	if working.Auth.OAuth2[1].ClientSecret != original.Auth.OAuth2[1].ClientSecret {
		t.Errorf("OAuth2 secret #1 lost in round-trip")
	}
	if working.Auth.LDAP.BindPassword != original.Auth.LDAP.BindPassword {
		t.Errorf("LDAP bind password lost in round-trip")
	}
}
