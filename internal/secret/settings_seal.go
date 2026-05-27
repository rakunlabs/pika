package secret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

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
// After the raw-mount/registry/proxy/serve extraction, the sealed
// payload covers only the secrets that remain in the configuration
// server: hook delivery target credentials, external-resource auth
// (Vault tokens, AWS secret keys, etc.), and auth-strategy secrets
// (OAuth2 client secret, LDAP bind password).

// sensitivePayload is the in-memory, JSON-serializable container
// for every secret value pulled out of *service.Settings before
// persistence.
type sensitivePayload struct {
	HookSecrets []sealedHook         `json:"hook_secrets,omitempty"`
	External    map[string]sealedExt `json:"external,omitempty"`

	// Auth-strategy secrets. OAuth2ClientSecrets is a parallel slice
	// to Settings.Auth.OAuth2 so index ordering carries the matching
	// secret back to the right strategy entry.
	OAuth2ClientSecrets []string `json:"oauth2_client_secrets,omitempty"`
	LDAPBindPassword    string   `json:"ldap_bind_password,omitempty"`
}

// sealedHook carries any hook-target secret. We capture secrets
// per-target index because a Hook can have multiple Targets and
// each may carry distinct credentials.
type sealedHook struct {
	Targets []sealedHookTarget `json:"targets,omitempty"`
}

// sealedHookTarget is the per-Target secret slot.
type sealedHookTarget struct {
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
// caller can persist a "secrets-stripped" row safely.
func extractSecrets(s *service.Settings) *sensitivePayload {
	if s == nil {
		return &sensitivePayload{}
	}
	p := &sensitivePayload{}

	// Hook target secrets — per-hook slot. Each hook may have
	// multiple targets, each with different credential shapes.
	if len(s.Hooks) > 0 {
		p.HookSecrets = make([]sealedHook, len(s.Hooks))
		for i := range s.Hooks {
			p.HookSecrets[i] = extractHookSecrets(&s.Hooks[i])
		}
	}

	// External resources.
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

// isPlaintextSecret reports whether a value is worth sealing: a
// non-empty string that is not a direct config:// reference.
func isPlaintextSecret(v string) bool {
	if v == "" {
		return false
	}
	return !isRuntimeRef(v)
}

func isRuntimeRef(v string) bool {
	return strings.HasPrefix(v, "config://")
}

// extractHookSecrets factors out the hook-type dispatch so the
// settings-extraction loop above stays readable.
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

// injectSecrets is the inverse of extractSecrets.
func injectSecrets(s *service.Settings, p *sensitivePayload) {
	if s == nil || p == nil {
		return
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
var _ = isPlaintextSecret

// settingsStorageWrapper wraps the backend SettingsStorage with the
// envelope encrypt/decrypt round-trip described above.
type settingsStorageWrapper struct {
	backend service.SettingsStorage
	parent  *Storage
}

// Get reads the row, opens the sealed payload, and stitches the
// secret values back into the typed fields.
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
		return s, nil
	}
	plain, err := envelope.Open(mgr, s.SensitivePayload)
	if err != nil {
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
func (w *settingsStorageWrapper) Set(ctx context.Context, settings *service.Settings) error {
	if settings == nil {
		return w.backend.Set(ctx, settings)
	}
	mgr := w.parent.keymgr
	if mgr == nil || !mgr.IsUnlocked() {
		probe := extractSecrets(settings)
		if isEmptyPayload(probe) {
			return w.backend.Set(ctx, settings)
		}
		injectSecrets(settings, probe)
		return errors.New("settings: server is locked; cannot persist secret values")
	}

	payload := extractSecrets(settings)
	if isEmptyPayload(payload) {
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
// carries any data worth encrypting.
func isEmptyPayload(p *sensitivePayload) bool {
	if p == nil {
		return true
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
			if e.VaultToken != "" || e.VaultRoleSecret != "" ||
				e.K8sKubeconfig != "" || e.AWSSecretKey != "" ||
				e.GCPSAJSON != "" || e.GCPParamSAJSON != "" ||
				e.AzureSecret != "" || len(e.HTTPHeaderAuth) > 0 {
				return false
			}
		}
	}
	if len(p.OAuth2ClientSecrets) > 0 {
		for _, v := range p.OAuth2ClientSecrets {
			if v != "" {
				return false
			}
		}
	}
	if p.LDAPBindPassword != "" {
		return false
	}
	return true
}
