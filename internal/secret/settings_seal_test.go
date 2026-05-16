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
	}
}
