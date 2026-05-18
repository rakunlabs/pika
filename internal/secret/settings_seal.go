package secret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/secret/envelope"
	"github.com/rakunlabs/pika/internal/service"
)

// ErrSealedCorrupt is returned by Get when the SensitivePayload blob
// exists but cannot be decrypted or parsed (wrong key, format drift,
// or on-disk corruption). The row is still returned alongside this
// error so callers that only need public fields can proceed; callers
// that touch sealed slots should check for it with errors.Is and
// surface the situation to the operator.
var ErrSealedCorrupt = errors.New("settings: sealed payload corrupt or unreadable")

// Sealed Settings — at-rest encryption for the user-managed secret
// values that the Settings table carries.
//
// Design rationale (vs. per-field encryption)
//
// Settings is a singleton row whose schema mixes operator-visible
// configuration (port numbers, share names, mount prefixes) with
// secret values (S3 access keys, FTP passwords, hook webhook signing
// secrets, external-resource credentials, SFTP host private keys).
// Per-field encryption would require splitting every secret-bearing
// struct into a public+sealed pair, plumbing the encrypt/decrypt
// through every UI panel, and migrating each field independently.
//
// We use envelope-of-secrets instead: a single sensitivePayload blob
// holds JSON of every secret slot. The wrapper extracts those values
// on Set (clearing them from the persisted plaintext row) and
// re-injects them on Get. From the consumer's point of view nothing
// changes — `settings.RawMounts[0].S3.SecretKey` reads the same as
// before — but the plaintext is never written to disk.
//
// Failure modes
//
// - Locked: Set returns ErrLocked (write fails closed). Get returns
//   the row with secret slots blank and a sentinel error so callers
//   can distinguish "no secrets configured" from "we couldn't read
//   them". The auth/bootstrap paths that read settings before unlock
//   currently never need the sealed slots, so this is non-fatal in
//   practice.
//
// - Wrong key: Get returns the row with secret slots blank + error.
//   The lock gate normally prevents this, but a corrupted verifier
//   is the residual case.

// sensitivePayload is the in-memory, JSON-serializable container
// for every secret value pulled out of *service.Settings before
// persistence. Field names match the Settings struct so the
// encrypt/decrypt helpers below can copy back and forth without
// indirection. Every field is omitempty so an installation that
// doesn't use a given backend doesn't pay JSON overhead for empty
// strings.
//
// Adding a new secret slot:
//  1. Add the field here.
//  2. Move it from Settings → here in extractSecrets().
//  3. Move it back in injectSecrets().
//  4. Bump the version in the envelope payload? No — the envelope
//     already carries a magic byte (envelope.formatV1). JSON's
//     structural tolerance means new fields read as zero on old
//     payloads and old fields are silently ignored on new
//     readers. No version field needed inside the JSON.
type sensitivePayload struct {
	// Per-mount creds. We index parallel arrays to RawMounts so we
	// don't depend on prefix uniqueness; an operator may rename a
	// prefix without losing the credential. Same approach for
	// FTPUsers / Hooks / External.
	Mounts        []sealedMount        `json:"mounts,omitempty"`
	FTPUserPasswd []string             `json:"ftp_user_passwd,omitempty"`
	HookSecrets   []sealedHook         `json:"hook_secrets,omitempty"`
	External      map[string]sealedExt `json:"external,omitempty"`

	// Server-host private keys. SFTPServe.HostKeyPEM is the obvious
	// one; the FTPServe TLS key PEM is included by the same
	// reasoning (a TLS private key is a credential).
	SFTPHostKeyPEM string `json:"sftp_host_key_pem,omitempty"`
	FTPTLSKeyPEM   string `json:"ftp_tls_key_pem,omitempty"`

	// Auth-strategy secrets. OAuth2ClientSecrets is a parallel slice
	// to Settings.Auth.OAuth2 so index ordering carries the matching
	// secret back to the right strategy entry (same pattern as
	// FTPUserPasswd / Mounts).
	OAuth2ClientSecrets []string `json:"oauth2_client_secrets,omitempty"`
	LDAPBindPassword    string   `json:"ldap_bind_password,omitempty"`
}

// sealedMount carries the secret subset of a single RawMountEntry.
// Public fields (Prefix, Type, Path, etc.) stay on the plaintext
// row; only the per-backend secret values are extracted here.
type sealedMount struct {
	S3SecretKey     string `json:"s3_secret_key,omitempty"`
	FTPPassword     string `json:"ftp_password,omitempty"`
	SFTPPassword    string `json:"sftp_password,omitempty"`
	SFTPPrivateKey  string `json:"sftp_private_key,omitempty"`
	WebDAVPassword  string `json:"webdav_password,omitempty"`
	VercelBlobToken string `json:"vercel_blob_token,omitempty"`
}

// sealedHook carries any hook-target secret. We capture secrets
// per-target index because a Hook can have multiple Targets and
// each may carry distinct credentials. The per-target sub-struct
// uses a discriminated layout so adding a new target type doesn't
// require renaming existing fields.
type sealedHook struct {
	Targets []sealedHookTarget `json:"targets,omitempty"`
}

// sealedHookTarget is the per-Target secret slot. Like
// sealedMount, fields are named after the target type so the
// extract/inject pair stays mechanically obvious.
type sealedHookTarget struct {
	// Kafka SASL passwords. KafkaSecurity.SASL is a slice; we
	// preserve order so injection puts each password back on the
	// right entry.
	KafkaSASLPlainPass []string `json:"kafka_sasl_plain_pass,omitempty"`
	KafkaSASLSCRAMPass []string `json:"kafka_sasl_scram_pass,omitempty"`
	RedisPassword      string   `json:"redis_password,omitempty"`
	NATSToken          string   `json:"nats_token,omitempty"`
	NATSPassword       string   `json:"nats_password,omitempty"`
}

// sealedExt carries the secret subset of a single external-resource
// entry. The Vault token, AWS secret key, Azure client secret etc.
// belong here — addresses, bucket names, public client IDs stay
// plaintext since they're operationally useful in audit logs.
type sealedExt struct {
	HTTPHeaderAuth  map[string]string `json:"http_header_auth,omitempty"` // future-proof slot
	VaultToken      string            `json:"vault_token,omitempty"`
	VaultRoleSecret string            `json:"vault_role_secret,omitempty"`
	K8sKubeconfig   string            `json:"k8s_kubeconfig,omitempty"`
	AWSSecretKey    string            `json:"aws_secret_key,omitempty"`
	GCPSAJSON       string            `json:"gcp_service_account_json,omitempty"`
	GCPParamSAJSON  string            `json:"gcp_parameter_service_account_json,omitempty"`
	AzureSecret     string            `json:"azure_client_secret,omitempty"`
}

// extractSecrets walks the settings struct, copies every secret
// field into a sensitivePayload, and zeroes the source so the
// caller can persist a "secrets-stripped" row safely. The original
// pointer is mutated in place — callers that need the original
// values back must call injectSecrets().
//
// Two arrays at the same length (Mounts vs. RawMounts, etc.) is the
// invariant; downstream injectSecrets() validates length-match
// before re-attaching. A length mismatch on the read path means
// either a partial write or a hand-edited row — we log and skip the
// secret slot rather than crashing.
func extractSecrets(s *service.Settings) *sensitivePayload {
	if s == nil {
		return &sensitivePayload{}
	}
	p := &sensitivePayload{}

	// Raw mounts — per-mount slot.
	if len(s.RawMounts) > 0 {
		p.Mounts = make([]sealedMount, len(s.RawMounts))
		for i := range s.RawMounts {
			m := &s.RawMounts[i]
			sm := sealedMount{}
			if m.S3 != nil {
				sm.S3SecretKey = m.S3.SecretKey
				m.S3.SecretKey = ""
			}
			if m.FTP != nil {
				sm.FTPPassword = m.FTP.Password
				m.FTP.Password = ""
			}
			if m.SFTP != nil {
				sm.SFTPPassword = m.SFTP.Password
				sm.SFTPPrivateKey = m.SFTP.PrivateKey
				m.SFTP.Password = ""
				m.SFTP.PrivateKey = ""
			}
			if m.WebDAV != nil {
				sm.WebDAVPassword = m.WebDAV.Password
				m.WebDAV.Password = ""
			}
			if m.VercelBlob != nil {
				sm.VercelBlobToken = m.VercelBlob.Token
				m.VercelBlob.Token = ""
			}
			p.Mounts[i] = sm
		}
	}

	// FTP user passwords. The bcrypt-style verification that the FTP
	// server uses still wants the cleartext (it forwards to the
	// user's chosen auth backend), so we encrypt rather than hash.
	if len(s.FTPUsers) > 0 {
		p.FTPUserPasswd = make([]string, len(s.FTPUsers))
		for i := range s.FTPUsers {
			p.FTPUserPasswd[i] = s.FTPUsers[i].Password
			s.FTPUsers[i].Password = ""
		}
	}

	// Hook target secrets — per-hook slot. Each hook may have
	// multiple targets, each with different credential shapes.
	if len(s.Hooks) > 0 {
		p.HookSecrets = make([]sealedHook, len(s.Hooks))
		for i := range s.Hooks {
			p.HookSecrets[i] = extractHookSecrets(&s.Hooks[i])
		}
	}

	// External resources. Map keys (resource names) are
	// operator-visible, so we keep them as the index.
	if len(s.External) > 0 {
		p.External = make(map[string]sealedExt, len(s.External))
		for name, ext := range s.External {
			se := sealedExt{}
			if ext.Vault != nil {
				se.VaultToken = ext.Vault.Token
				ext.Vault.Token = ""
				if ext.Vault.AppRole != nil {
					se.VaultRoleSecret = ext.Vault.AppRole.SecretID
					ext.Vault.AppRole.SecretID = ""
				}
			}
			if ext.Kubernetes != nil {
				se.K8sKubeconfig = ext.Kubernetes.KubeconfigContent
				ext.Kubernetes.KubeconfigContent = ""
			}
			if ext.AWS != nil {
				se.AWSSecretKey = ext.AWS.SecretKey
				ext.AWS.SecretKey = ""
			}
			if ext.GCP != nil {
				se.GCPSAJSON = ext.GCP.ServiceAccountJSON
				ext.GCP.ServiceAccountJSON = ""
			}
			if ext.GCPParameter != nil {
				se.GCPParamSAJSON = ext.GCPParameter.ServiceAccountJSON
				ext.GCPParameter.ServiceAccountJSON = ""
			}
			if ext.Azure != nil {
				se.AzureSecret = ext.Azure.ClientSecret
				ext.Azure.ClientSecret = ""
			}
			p.External[name] = se
			s.External[name] = ext
		}
	}

	// Server-host TLS / SSH keys.
	if s.SFTPServe != nil && s.SFTPServe.HostKeyPEM != "" {
		p.SFTPHostKeyPEM = s.SFTPServe.HostKeyPEM
		s.SFTPServe.HostKeyPEM = ""
	}
	if s.FTPServe != nil && s.FTPServe.TLSKeyPEM != "" {
		p.FTPTLSKeyPEM = s.FTPServe.TLSKeyPEM
		s.FTPServe.TLSKeyPEM = ""
	}

	// Auth-strategy secrets.
	if s.Auth != nil {
		if len(s.Auth.OAuth2) > 0 {
			p.OAuth2ClientSecrets = make([]string, len(s.Auth.OAuth2))
			for i := range s.Auth.OAuth2 {
				p.OAuth2ClientSecrets[i] = s.Auth.OAuth2[i].ClientSecret
				s.Auth.OAuth2[i].ClientSecret = ""
			}
		}
		if s.Auth.LDAP != nil && s.Auth.LDAP.BindPassword != "" {
			p.LDAPBindPassword = s.Auth.LDAP.BindPassword
			s.Auth.LDAP.BindPassword = ""
		}
	}

	return p
}

// extractHookSecrets factors out the hook-type dispatch so the
// settings-extraction loop above stays readable. Mirrored by
// injectHookSecrets below.
//
// We capture per-target secret bundles into a parallel slice so
// re-injection just walks both slices in lockstep — losing the
// order would let an admin's "swap target order" UI action
// silently re-attach the wrong creds to the wrong target.
func extractHookSecrets(h *hook.Hook) sealedHook {
	sh := sealedHook{}
	if len(h.Targets) == 0 {
		return sh
	}
	sh.Targets = make([]sealedHookTarget, len(h.Targets))
	for i := range h.Targets {
		t := &h.Targets[i]
		st := sealedHookTarget{}
		if t.Kafka != nil {
			// SASL is a slice of mechanism configs. Capture each
			// mechanism's Plain/SCRAM password under a parallel
			// slot so injection puts them back on the right entry.
			if len(t.Kafka.Security.SASL) > 0 {
				st.KafkaSASLPlainPass = make([]string, len(t.Kafka.Security.SASL))
				st.KafkaSASLSCRAMPass = make([]string, len(t.Kafka.Security.SASL))
				for j := range t.Kafka.Security.SASL {
					m := &t.Kafka.Security.SASL[j]
					if m.Plain != nil {
						st.KafkaSASLPlainPass[j] = m.Plain.Pass
						m.Plain.Pass = ""
					}
					if m.SCRAM != nil {
						st.KafkaSASLSCRAMPass[j] = m.SCRAM.Pass
						m.SCRAM.Pass = ""
					}
				}
			}
		}
		if t.Redis != nil {
			st.RedisPassword = t.Redis.Password
			t.Redis.Password = ""
		}
		if t.NATS != nil {
			st.NATSToken = t.NATS.Token
			st.NATSPassword = t.NATS.Password
			t.NATS.Token = ""
			t.NATS.Password = ""
		}
		sh.Targets[i] = st
	}
	return sh
}

// injectSecrets is the inverse of extractSecrets — given a Settings
// row that came back from storage with secret slots blank and a
// decrypted sensitivePayload, re-attaches every secret value.
//
// Length mismatches between parallel arrays are silently truncated:
// they imply the operator added a mount in one transaction and the
// payload was written before that mount. The unaffected mounts
// still get their secrets back; the new mount appears with empty
// credentials, exactly the state the operator is about to fix in
// the UI.
func injectSecrets(s *service.Settings, p *sensitivePayload) {
	if s == nil || p == nil {
		return
	}

	for i := 0; i < len(s.RawMounts) && i < len(p.Mounts); i++ {
		m := &s.RawMounts[i]
		sm := p.Mounts[i]
		if m.S3 != nil {
			m.S3.SecretKey = sm.S3SecretKey
		}
		if m.FTP != nil {
			m.FTP.Password = sm.FTPPassword
		}
		if m.SFTP != nil {
			m.SFTP.Password = sm.SFTPPassword
			m.SFTP.PrivateKey = sm.SFTPPrivateKey
		}
		if m.WebDAV != nil {
			m.WebDAV.Password = sm.WebDAVPassword
		}
		if m.VercelBlob != nil {
			m.VercelBlob.Token = sm.VercelBlobToken
		}
	}

	for i := 0; i < len(s.FTPUsers) && i < len(p.FTPUserPasswd); i++ {
		s.FTPUsers[i].Password = p.FTPUserPasswd[i]
	}

	for i := 0; i < len(s.Hooks) && i < len(p.HookSecrets); i++ {
		injectHookSecrets(&s.Hooks[i], &p.HookSecrets[i])
	}

	for name, se := range p.External {
		ext, ok := s.External[name]
		if !ok {
			continue
		}
		if ext.Vault != nil {
			ext.Vault.Token = se.VaultToken
			if ext.Vault.AppRole != nil {
				ext.Vault.AppRole.SecretID = se.VaultRoleSecret
			}
		}
		if ext.Kubernetes != nil {
			ext.Kubernetes.KubeconfigContent = se.K8sKubeconfig
		}
		if ext.AWS != nil {
			ext.AWS.SecretKey = se.AWSSecretKey
		}
		if ext.GCP != nil {
			ext.GCP.ServiceAccountJSON = se.GCPSAJSON
		}
		if ext.GCPParameter != nil {
			ext.GCPParameter.ServiceAccountJSON = se.GCPParamSAJSON
		}
		if ext.Azure != nil {
			ext.Azure.ClientSecret = se.AzureSecret
		}
		s.External[name] = ext
	}

	if s.SFTPServe != nil && p.SFTPHostKeyPEM != "" {
		s.SFTPServe.HostKeyPEM = p.SFTPHostKeyPEM
	}
	if s.FTPServe != nil && p.FTPTLSKeyPEM != "" {
		s.FTPServe.TLSKeyPEM = p.FTPTLSKeyPEM
	}

	if s.Auth != nil {
		for i := 0; i < len(s.Auth.OAuth2) && i < len(p.OAuth2ClientSecrets); i++ {
			s.Auth.OAuth2[i].ClientSecret = p.OAuth2ClientSecrets[i]
		}
		if s.Auth.LDAP != nil && p.LDAPBindPassword != "" {
			s.Auth.LDAP.BindPassword = p.LDAPBindPassword
		}
	}
}

func injectHookSecrets(h *hook.Hook, sh *sealedHook) {
	if sh == nil {
		return
	}
	for i := 0; i < len(h.Targets) && i < len(sh.Targets); i++ {
		t := &h.Targets[i]
		st := sh.Targets[i]
		if t.Kafka != nil {
			for j := 0; j < len(t.Kafka.Security.SASL); j++ {
				m := &t.Kafka.Security.SASL[j]
				if m.Plain != nil && j < len(st.KafkaSASLPlainPass) {
					m.Plain.Pass = st.KafkaSASLPlainPass[j]
				}
				if m.SCRAM != nil && j < len(st.KafkaSASLSCRAMPass) {
					m.SCRAM.Pass = st.KafkaSASLSCRAMPass[j]
				}
			}
		}
		if t.Redis != nil {
			t.Redis.Password = st.RedisPassword
		}
		if t.NATS != nil {
			t.NATS.Token = st.NATSToken
			t.NATS.Password = st.NATSPassword
		}
	}
}

// keep imports live even when no callers invoke them in some build
// configurations (extension typings move around in tests).
var _ = json.Marshal
var _ external.External

// settingsStorageWrapper wraps the backend SettingsStorage with the
// envelope encrypt/decrypt round-trip described above.
type settingsStorageWrapper struct {
	backend service.SettingsStorage
	parent  *Storage
}

// Get reads the row, opens the sealed payload, and stitches the
// secret values back into the typed fields. When the manager is
// locked we still return the row (so callers can read public
// settings — auth, ports, etc.) but the secret slots stay blank
// and we don't error: locked-but-readable matches the Phase-1
// design where bootstrap reads of Settings happen before unlock.
func (w *settingsStorageWrapper) Get(ctx context.Context) (*service.Settings, error) {
	s, err := w.backend.Get(ctx)
	if err != nil {
		return nil, err
	}
	if s == nil || len(s.SensitivePayload) == 0 {
		return s, nil
	}
	mgr := w.parent.keymgr
	if mgr == nil || !mgr.IsUnlocked() {
		// Server is locked. Return public fields only; secret slots
		// stay zero. This is intentional — the auth bootstrap path
		// reads Settings before unlock and would crash if we
		// errored here.
		return s, nil
	}
	plain, err := envelope.Open(mgr, s.SensitivePayload)
	if err != nil {
		// Couldn't decrypt — wrong key, format drift, or on-disk
		// corruption. Leave SensitivePayload intact on the returned
		// row (so callers can round-trip an unchanged Set) and
		// surface the situation: log loudly AND wrap into
		// ErrSealedCorrupt so callers can branch with errors.Is.
		slog.Error("settings: sealed payload could not be decrypted",
			"bytes", len(s.SensitivePayload),
			"error", err)
		return s, fmt.Errorf("%w: %v", ErrSealedCorrupt, err)
	}
	var p sensitivePayload
	if err := json.Unmarshal(plain, &p); err != nil {
		slog.Error("settings: decrypted sealed payload failed to parse",
			"bytes", len(plain),
			"error", err)
		return s, fmt.Errorf("%w: %v", ErrSealedCorrupt, err)
	}
	injectSecrets(s, &p)
	return s, nil
}

// Set extracts every secret slot from `settings`, encrypts the
// resulting payload, and writes the sanitized row to the backend.
// The caller's *Settings is mutated in place — its secret fields
// are zeroed during the extraction pass. Callers that need to
// continue using the same struct after Set must re-fetch (or call
// injectSecrets manually with the payload they retained).
//
// Fails closed when locked: a write attempt with no live key
// returns ErrLocked rather than persisting plaintext.
func (w *settingsStorageWrapper) Set(ctx context.Context, settings *service.Settings) error {
	if settings == nil {
		return w.backend.Set(ctx, settings)
	}
	mgr := w.parent.keymgr
	// Decide encryption strategy:
	//   - locked: reject (write fails closed)
	//   - unlocked: extract → seal → write
	if mgr == nil || !mgr.IsUnlocked() {
		// Detect "no secrets to write" case so legitimate updates
		// to public-only fields can still happen while locked
		// (e.g. enabling a port). We do that by extracting first,
		// checking if anything came out, and short-circuiting
		// when not.
		probe := extractSecrets(settings)
		if isEmptyPayload(probe) {
			// Public-only update — preserve the existing sealed
			// payload byte-for-byte and write through.
			return w.backend.Set(ctx, settings)
		}
		// Restore the secrets we just extracted so the caller's
		// struct isn't left mutated when we fail.
		injectSecrets(settings, probe)
		return errors.New("settings: server is locked; cannot persist secret values")
	}

	payload := extractSecrets(settings)
	if isEmptyPayload(payload) {
		// Settings has no secrets at all — clear any prior payload
		// rather than re-encrypting an empty struct (saves a few
		// bytes and produces a deterministic empty state).
		settings.SensitivePayload = nil
	} else {
		raw, err := json.Marshal(payload)
		if err != nil {
			injectSecrets(settings, payload)
			return fmt.Errorf("settings: marshal sensitive payload: %w", err)
		}
		sealed, err := envelope.Seal(mgr, raw)
		if err != nil {
			injectSecrets(settings, payload)
			return fmt.Errorf("settings: seal sensitive payload: %w", err)
		}
		settings.SensitivePayload = sealed
	}
	return w.backend.Set(ctx, settings)
}

// isEmptyPayload reports whether the sensitive-payload struct
// carries any data worth encrypting. We use this to skip the seal
// round-trip on settings rows that don't (yet) have any secrets,
// keeping bootstrap-time writes cheap.
func isEmptyPayload(p *sensitivePayload) bool {
	if p == nil {
		return true
	}
	if len(p.Mounts) > 0 {
		for _, m := range p.Mounts {
			if m != (sealedMount{}) {
				return false
			}
		}
	}
	if len(p.FTPUserPasswd) > 0 {
		for _, s := range p.FTPUserPasswd {
			if s != "" {
				return false
			}
		}
	}
	if len(p.HookSecrets) > 0 {
		for _, h := range p.HookSecrets {
			for _, t := range h.Targets {
				if t.RedisPassword != "" || t.NATSToken != "" || t.NATSPassword != "" {
					return false
				}
				for _, s := range t.KafkaSASLPlainPass {
					if s != "" {
						return false
					}
				}
				for _, s := range t.KafkaSASLSCRAMPass {
					if s != "" {
						return false
					}
				}
			}
		}
	}
	if len(p.External) > 0 {
		for _, e := range p.External {
			// Map fields can't use struct equality; check field by field.
			if e.VaultToken != "" || e.VaultRoleSecret != "" ||
				e.K8sKubeconfig != "" || e.AWSSecretKey != "" ||
				e.GCPSAJSON != "" || e.GCPParamSAJSON != "" ||
				e.AzureSecret != "" || len(e.HTTPHeaderAuth) > 0 {
				return false
			}
		}
	}
	if p.SFTPHostKeyPEM != "" || p.FTPTLSKeyPEM != "" {
		return false
	}
	return true
}
