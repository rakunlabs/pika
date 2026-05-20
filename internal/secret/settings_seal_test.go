package secret

import (
	"testing"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/service"
)

// TestExtractInjectRoundTrip — every secret-bearing slot we
// extract must come back identical after an inject pass.  We build
// a fixture covering every supported source (mounts, FTP users,
// hooks, externals, server-host TLS) and assert nothing was
// dropped or scrambled.
func TestExtractInjectRoundTrip(t *testing.T) {
	original := buildFixture()
	working := buildFixture()

	payload := extractSecrets(working)

	// After extract: all secret slots in `working` are blank, all
	// secret slots in `payload` carry the original values.
	if working.RawMounts[0].S3.SecretKey != "" {
		t.Errorf("S3 SecretKey not cleared after extract")
	}
	if working.External["my-vault"].Vault.Token != "" {
		t.Errorf("Vault token not cleared after extract")
	}
	if working.SFTPServe.HostKeyPEM != "" {
		t.Errorf("SFTP host key not cleared after extract")
	}

	// Round-trip: inject back, compare to the untouched original.
	injectSecrets(working, payload)

	if working.RawMounts[0].S3.SecretKey != original.RawMounts[0].S3.SecretKey {
		t.Errorf("S3 SecretKey lost in round-trip: got %q want %q",
			working.RawMounts[0].S3.SecretKey, original.RawMounts[0].S3.SecretKey)
	}
	if working.RawMounts[1].SFTP.PrivateKey != original.RawMounts[1].SFTP.PrivateKey {
		t.Errorf("SFTP private key lost in round-trip")
	}
	if working.FTPUsers[0].Password != original.FTPUsers[0].Password {
		t.Errorf("FTP user password lost in round-trip")
	}
	if working.External["my-vault"].Vault.Token != original.External["my-vault"].Vault.Token {
		t.Errorf("Vault token lost in round-trip")
	}
	if working.External["aws-prod"].AWS.SecretKey != original.External["aws-prod"].AWS.SecretKey {
		t.Errorf("AWS secret key lost in round-trip")
	}
	if working.SFTPServe.HostKeyPEM != original.SFTPServe.HostKeyPEM {
		t.Errorf("SFTP host key lost in round-trip")
	}
	if working.Hooks[0].Targets[0].Redis.Password != original.Hooks[0].Targets[0].Redis.Password {
		t.Errorf("Redis hook password lost in round-trip")
	}
}

// TestPublicFieldsPreserved — the extract pass must leave every
// non-secret field alone. This is the property that lets us write
// the sanitized row to the public bucket without losing any
// operator-visible config.
func TestPublicFieldsPreserved(t *testing.T) {
	s := buildFixture()
	original := buildFixture()
	_ = extractSecrets(s)

	if s.RawMounts[0].Prefix != original.RawMounts[0].Prefix {
		t.Errorf("Mount prefix mutated by extract")
	}
	if s.RawMounts[0].S3.Bucket != original.RawMounts[0].S3.Bucket {
		t.Errorf("S3 bucket mutated by extract")
	}
	if s.RawMounts[0].S3.AccessKey != original.RawMounts[0].S3.AccessKey {
		t.Errorf("S3 access key (intentionally public) was scrubbed by extract")
	}
	if s.External["my-vault"].Vault.Address != original.External["my-vault"].Vault.Address {
		t.Errorf("Vault address mutated by extract")
	}
	if s.SFTPServe.Port != original.SFTPServe.Port {
		t.Errorf("SFTP port mutated by extract")
	}
}

// TestEmptyPayloadDetection — a settings row with no secrets must
// produce an "empty" payload so the wrapper can short-circuit the
// seal step. Catches false-positives in isEmptyPayload that would
// otherwise either skip needed encryption or unnecessarily encrypt
// every settings write.
func TestEmptyPayloadDetection(t *testing.T) {
	empty := &service.Settings{
		// Public-only mount: no S3 secret key.
		RawMounts: []service.RawMountEntry{
			{Prefix: "local-files", Type: "local", Path: "/var/data"},
		},
	}
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

// TestInjectLengthMismatch — when the persisted payload is shorter
// than the current Settings (operator added a mount in a separate
// transaction), the existing entries should still get their
// secrets back; the new entry stays blank rather than crashing.
func TestInjectLengthMismatch(t *testing.T) {
	current := buildFixture()
	payload := extractSecrets(buildFixture())

	// Operator just added a third mount that wasn't in the payload.
	current.RawMounts = append(current.RawMounts, service.RawMountEntry{
		Prefix: "new-mount",
		Type:   "s3",
		S3:     &service.S3ConfigEntry{Bucket: "fresh"},
	})

	injectSecrets(current, payload)

	if current.RawMounts[0].S3.SecretKey == "" {
		t.Errorf("first mount lost its secret key")
	}
	if current.RawMounts[2].S3.SecretKey != "" {
		t.Errorf("new mount unexpectedly got a secret key")
	}
}

func buildFixture() *service.Settings {
	tlsKey := "-----BEGIN PRIVATE KEY-----\nfake-tls\n-----END PRIVATE KEY-----"
	hostKey := "-----BEGIN OPENSSH PRIVATE KEY-----\nfake-host\n-----END OPENSSH PRIVATE KEY-----"

	return &service.Settings{
		RawMounts: []service.RawMountEntry{
			{
				Prefix: "s3-prod",
				Type:   "s3",
				S3: &service.S3ConfigEntry{
					Bucket:    "prod-data",
					AccessKey: "AKIA-PUBLIC",
					SecretKey: "super-secret-aws-key",
				},
			},
			{
				Prefix: "sftp-eu",
				Type:   "sftp",
				SFTP: &service.SFTPConfigEntry{
					Host:       "sftp.example.com",
					Username:   "deploy",
					Password:   "p@ssw0rd",
					PrivateKey: "-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----",
				},
			},
		},
		FTPUsers: []service.FTPUserEntry{
			{Username: "alice", Password: "alice-pass"},
		},
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
		SFTPServe: &service.SFTPServeSettings{
			Enabled:    true,
			Port:       2222,
			HostKeyPEM: hostKey,
		},
		FTPServe: &service.FTPServeSettings{
			Enabled:   true,
			Port:      21,
			TLSKeyPEM: tlsKey,
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

// TestAuthSecretRoundTrip — OAuth2 ClientSecret and LDAP BindPassword
// must survive an extract→inject pass identically, and be cleared
// from the source struct after extract so the persisted plaintext
// row contains no auth secrets.
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

// TestRegistryUpstreamSecretRoundTrip — plaintext upstream creds in
// Registry repos must be extracted into the sealed payload, cleared
// from the source struct, then restored identically on inject.
func TestRegistryUpstreamSecretRoundTrip(t *testing.T) {
	build := func() *service.Settings {
		return &service.Settings{
			Registry: &service.RegistrySettings{
				Namespaces: []service.RegistryNamespace{
					{
						Name: "default",
						Repositories: []service.RegistryRepository{
							{
								Name: "private-mirror", Type: "go", Kind: "remote",
								URL: "https://internal-proxy.example",
								Auth: &service.RegistryUpstreamAuth{
									Type: "basic", Username: "ops", Password: "s3cret!",
								},
							},
							{
								Name: "bearer-mirror", Type: "npm", Kind: "remote",
								URL: "https://registry.example",
								Auth: &service.RegistryUpstreamAuth{
									Type: "bearer", Token: "tk_abcdef",
								},
							},
						},
					},
				},
			},
		}
	}

	working := build()
	original := build()

	payload := extractSecrets(working)

	// Plaintext secrets must be moved out of the source struct.
	if working.Registry.Namespaces[0].Repositories[0].Auth.Password != "" {
		t.Errorf("basic password not cleared after extract")
	}
	if working.Registry.Namespaces[0].Repositories[1].Auth.Token != "" {
		t.Errorf("bearer token not cleared after extract")
	}
	if len(payload.RegistryUpstream) != 2 {
		t.Fatalf("RegistryUpstream len=%d, want 2", len(payload.RegistryUpstream))
	}

	injectSecrets(working, payload)

	if working.Registry.Namespaces[0].Repositories[0].Auth.Password !=
		original.Registry.Namespaces[0].Repositories[0].Auth.Password {
		t.Errorf("basic password lost in round-trip")
	}
	if working.Registry.Namespaces[0].Repositories[1].Auth.Token !=
		original.Registry.Namespaces[0].Repositories[1].Auth.Token {
		t.Errorf("bearer token lost in round-trip")
	}
}

// TestRegistryUpstreamPreservesRuntimeRefs — values that start with
// "raw://" or "config://" must NOT be moved into the sealed payload. They're
// already references resolved against their source store at runtime;
// sealing them would add no security and would force a more complex
// edit experience.
func TestRegistryUpstreamPreservesRuntimeRefs(t *testing.T) {
	s := &service.Settings{
		Registry: &service.RegistrySettings{
			Namespaces: []service.RegistryNamespace{
				{
					Name: "default",
					Repositories: []service.RegistryRepository{
						{
							Name: "ref-mirror", Type: "docker", Kind: "remote",
							URL: "https://registry-1.docker.io",
							Auth: &service.RegistryUpstreamAuth{
								Type: "bearer", Token: "raw://creds/docker",
							},
						},
					},
				},
			},
		},
	}

	payload := extractSecrets(s)

	// Reference must stay in place on the public struct.
	if s.Registry.Namespaces[0].Repositories[0].Auth.Token != "raw://creds/docker" {
		t.Errorf("raw:// reference was moved into payload, want preserved in-place. Got: %q",
			s.Registry.Namespaces[0].Repositories[0].Auth.Token)
	}
	// Nothing should have been added to the sealed payload —
	// references aren't secrets-to-seal.
	for _, row := range payload.RegistryUpstream {
		if row.AuthToken != "" {
			t.Errorf("sealed payload picked up a runtime reference: %+v", row)
		}
	}
}

// TestRegistryUpstreamMixedPlaintextAndRef — a single repo can
// combine plaintext and runtime reference values across the three slots.
// The seal layer must split them: plaintext into the payload,
// references kept in-place.
func TestRegistryUpstreamMixedPlaintextAndRef(t *testing.T) {
	s := &service.Settings{
		Registry: &service.RegistrySettings{
			Namespaces: []service.RegistryNamespace{
				{
					Name: "default",
					Repositories: []service.RegistryRepository{
						{
							Name: "mixed", Type: "docker", Kind: "remote",
							URL: "https://registry.example",
							Auth: &service.RegistryUpstreamAuth{
								Type:     "header",
								Username: "ignored", // basic-only field; left alone
								Token:    "config://creds/token",
								Header:   "X-Api-Key",
								Value:    "plain-value-1234",
							},
						},
					},
				},
			},
		},
	}

	payload := extractSecrets(s)

	auth := s.Registry.Namespaces[0].Repositories[0].Auth
	if auth.Token != "config://creds/token" {
		t.Errorf("Token reference moved: %q", auth.Token)
	}
	if auth.Value != "" {
		t.Errorf("plaintext Value not cleared from source: %q", auth.Value)
	}
	if len(payload.RegistryUpstream) != 1 {
		t.Fatalf("expected 1 sealed registry row, got %d", len(payload.RegistryUpstream))
	}
	if got := payload.RegistryUpstream[0].AuthHeaderValue; got != "plain-value-1234" {
		t.Errorf("sealed AuthHeaderValue = %q, want plain-value-1234", got)
	}
}

// TestRegistryUpstreamRenameLosesBinding documents the rename
// trade-off: the seal layer keys sealed slots by (namespace, repo
// name) rather than by parallel index. A repo rename in Settings
// detaches the sealed secret silently — the operator's next save
// will re-seal under the new name. This is the same behaviour the
// External map and Hook target slots exhibit, so we pin it here.
func TestRegistryUpstreamRenameLosesBinding(t *testing.T) {
	// Step 1: build a settings row and seal it.
	original := &service.Settings{
		Registry: &service.RegistrySettings{
			Namespaces: []service.RegistryNamespace{
				{
					Name: "default",
					Repositories: []service.RegistryRepository{
						{
							Name: "old-name", Type: "go", Kind: "remote",
							URL:  "https://proxy.example",
							Auth: &service.RegistryUpstreamAuth{Type: "bearer", Token: "tk_secret"},
						},
					},
				},
			},
		},
	}
	payload := extractSecrets(original)

	// Step 2: simulate the operator renaming the repo via the UI.
	// We rebuild Settings as it would arrive on the next save: the
	// secret is no longer in the JSON (we sent only the public
	// shape) but the sealed payload still carries the old name.
	renamed := &service.Settings{
		Registry: &service.RegistrySettings{
			Namespaces: []service.RegistryNamespace{
				{
					Name: "default",
					Repositories: []service.RegistryRepository{
						{
							Name: "new-name", Type: "go", Kind: "remote",
							URL:  "https://proxy.example",
							Auth: &service.RegistryUpstreamAuth{Type: "bearer"},
						},
					},
				},
			},
		},
	}
	injectSecrets(renamed, payload)

	if renamed.Registry.Namespaces[0].Repositories[0].Auth.Token != "" {
		t.Errorf("rename should have lost the sealed secret, got %q",
			renamed.Registry.Namespaces[0].Repositories[0].Auth.Token)
	}
}
