package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/hook"
)

// (PublicPortSettings / CompatSettings / ConsulKVSettings were removed
// when the standalone /data + /raw "public port" + Consul KV compat
// section was replaced by the user-built Proxy Servers — every
// listener, every middleware and every endpoint shape that used to
// live there is now an explicit node in a ProxyServer graph. The
// settings field disappeared along with the types; any old row that
// still carries the keys is safely ignored on unmarshal.)

// ForwardAuthSettings configures forward-auth middleware that delegates
// authentication to an external service. When Enabled, the middleware is
// hot-swapped into the request pipeline via an ada.Slot; when disabled the
// slot becomes a no-op without a restart. Forward-auth can coexist with
// the built-in session auth — the combined middleware tries session first,
// then falls back to forward-auth.
type ForwardAuthSettings struct {
	Enabled                  bool     `json:"enabled"`
	Address                  string   `json:"address"`
	AuthResponseHeaders      []string `json:"auth_response_headers,omitempty"`
	AuthResponseHeadersRegex string   `json:"auth_response_headers_regex,omitempty"`
	AuthRequestHeaders       []string `json:"auth_request_headers,omitempty"`
	TrustForwardHeader       bool     `json:"trust_forward_header,omitempty"`
	InsecureSkipVerify       bool     `json:"insecure_skip_verify,omitempty"`
	Timeout                  string   `json:"timeout,omitempty"` // Go duration string, e.g. "10s"
	RedirectURL              string   `json:"redirect_url,omitempty"`
	RedirectCode             int      `json:"redirect_code,omitempty"`
	RedirectStatusCodes      []int    `json:"redirect_status_codes,omitempty"`
	RequestMethod            string   `json:"request_method,omitempty"`
}

// ExternalPermissionsSettings configures permission enforcement for
// forward-auth users. When Enabled, pika reads a groups header from
// each request and maps external group names to pika capability keys
// via Mapping. Superadmins is an allowlist of usernames that bypass
// all checks (equivalent to users.is_superadmin for built-in auth).
//
// When Enabled is false, forward-auth users keep the legacy
// "no permissions applied" behavior — their X-User flows through as
// an audit label but nothing is enforced.
type ExternalPermissionsSettings struct {
	// Enabled toggles permission enforcement under forward auth.
	Enabled bool `json:"enabled"`
	// GroupsHeader is the request header that carries the external group
	// list. Default: "X-Groups". The gateway must include this header in
	// its auth_response_headers allowlist for it to reach pika.
	GroupsHeader string `json:"groups_header,omitempty"`
	// GroupsSeparator is the delimiter inside a single header value.
	// Repeated header lines are also concatenated. Default: ",".
	GroupsSeparator string `json:"groups_separator,omitempty"`
	// Mapping translates external group names to pika capability keys.
	// Example: {"pika-editor": ["files.read", "files.write"]}
	Mapping map[string][]string `json:"mapping,omitempty"`
	// Superadmins is the allowlist of forward-auth usernames that bypass
	// all permission checks.
	Superadmins []string `json:"superadmins,omitempty"`
}

type Settings struct {
	External map[string]external.External `json:"external,omitempty"`
	// EncryptionVerifier is the ciphertext of a known plaintext used
	// to detect a wrong server-encryption key on unlock. Written
	// once at server initialization (POST /api/v1/key/initialize),
	// re-encrypted on rotation. Kept inside Settings rather than its
	// own table so the bootstrap path doesn't grow another schema
	// dependency. The plaintext format is documented in
	// service/keyops.go (verifierPlaintext()).
	EncryptionVerifier  []byte                       `json:"encryption_verifier,omitempty"`
	EventLog            *EventLogSettings            `json:"event_log,omitempty"`
	Hooks               []hook.Hook                  `json:"hooks,omitempty"`
	ExternalPermissions *ExternalPermissionsSettings `json:"external_permissions,omitempty"`
	ForwardAuth         *ForwardAuthSettings         `json:"forward_auth,omitempty"`
	Auth                *AuthSettings                `json:"auth,omitempty"`
	UserSync            *UserSyncSettings            `json:"user_sync,omitempty"`
	Vault               *VaultSettings               `json:"vault,omitempty"`
	// PublicEndpoints is the list of operator-defined public HTTP
	// endpoints that expose pika's configuration data directly,
	// through a compatibility shim (Consul KV), from an External
	// resource, or through a user-authored response modifier (custom
	// Go template). Each entry owns its own TCP listener;
	// reconciliation is performed by
	// internal/server/publicendpoint.Manager after every save.
	PublicEndpoints []PublicEndpoint `json:"public_endpoints,omitempty"`

	// SensitivePayload is the at-rest encrypted blob carrying the
	// user-supplied secret values for fields above (S3 access keys,
	// FTP passwords, hook webhook secrets, external-resource creds,
	// SFTP host private keys, etc.). The wrapper layer
	// (internal/secret) inflates this back into the typed fields
	// during Get and re-seals on Set; consumers of *Settings see
	// plaintext as before. The raw byte slot exists here so the
	// row-conversion code in internal/storage/bw can route it to
	// the underlying bucket without dragging the encryption layer
	// into bw. Empty/nil while the server is locked or for fresh
	// installs that haven't written a settings row yet.
	SensitivePayload []byte `json:"sensitive_payload,omitempty"`
}

// VaultSettings configures the personal-vault feature at the
// deployment level. The feature itself is shipped with every Pika
// build; this struct only lets an administrator turn the SPA + API
// surface on or off without redeploying.
//
// Why a "Disabled" flag instead of "Enabled":
//
//   - Backward compatibility. A pre-existing Settings row with no
//     vault config decodes to Vault == nil, and downstream code
//     treats nil as "default state". The default has been "vault
//     available" for the entire history of the feature, so a flag
//     whose zero value preserves that behaviour is "Disabled=false".
//     Flipping to "Enabled=false" would silently disable the vault
//     on every existing install during the upgrade.
//
// Disabling does NOT delete vault data. Every existing
// vault_accounts / vault_items row stays where it is; flipping
// Disabled=true just hides the routes from the API and the link
// from the SPA so no new operations can run. Re-enabling restores
// access to the same data with the same master password.
type VaultSettings struct {
	// Disabled hides the /vault feature from the UI and turns every
	// /api/v1/me/vault/* endpoint into a 404. Existing data is
	// preserved; this is a feature-flag, not a destructive action.
	Disabled bool `json:"disabled"`
}

// EventLogSettings controls Pika's built-in event log line. Nil settings mean
// enabled so existing deployments get observability without a migration.
type EventLogSettings struct {
	Disabled bool `json:"disabled"`
}

func (s *Settings) EventLogEnabled() bool {
	return s == nil || s.EventLog == nil || !s.EventLog.Disabled
}

type PatchSettings struct {
	Action              ActionKey                    `json:"action"`
	External            map[string]external.External `json:"external,omitempty"`
	EventLog            *EventLogSettings            `json:"event_log,omitempty"`
	Hooks               *[]hook.Hook                 `json:"hooks,omitempty"` // pointer to distinguish nil from empty
	ExternalPermissions *ExternalPermissionsSettings `json:"external_permissions,omitempty"`
	ForwardAuth         *ForwardAuthSettings         `json:"forward_auth,omitempty"`
	Auth                *AuthSettings                `json:"auth,omitempty"`
	UserSync            *UserSyncSettings            `json:"user_sync,omitempty"`
	Vault               *VaultSettings               `json:"vault,omitempty"`
	// PublicEndpoints is a full-replace patch — pointer-to-slice so
	// nil ("don't touch") is distinguishable from empty ("clear the
	// list"). Matches the Hooks shape exactly.
	PublicEndpoints *[]PublicEndpoint `json:"public_endpoints,omitempty"`
}

type ActionKey string

const (
	ActionKeySet    ActionKey = "set"
	ActionKeyRemove ActionKey = "remove"
)

func (s *Service) Settings(ctx context.Context) (*Settings, error) {
	settings, err := s.store.Settings().Get(ctx)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Return empty settings on first use
			return &Settings{
				External: make(map[string]external.External),
			}, nil
		}
		return nil, err
	}

	return settings, nil
}

func (s *Service) PatchSettings(ctx context.Context, patch *PatchSettings) error {
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}

	switch patch.Action {
	case ActionKeySet:
		// Validate every incoming external resource through its
		// Provider before persisting. This is the single chokepoint
		// for "is this configuration well-formed" — both the SPA's
		// Add/Edit flows and any direct API caller pass through here,
		// so a misconfigured record can never reach storage.
		// We pass *Service as Deps; Validate() does not call into
		// Deps but the constructor accepts one for uniformity.
		for name, ext := range patch.External {
			provider, err := external.ResourceProvider(ext, s)
			if err != nil {
				return fmt.Errorf("external resource %q: %w: %w", name, err, ErrBadRequest)
			}
			if err := provider.Validate(); err != nil {
				return fmt.Errorf("external resource %q: %w: %w", name, err, ErrBadRequest)
			}
		}
		if settings.External == nil {
			settings.External = make(map[string]external.External)
		}
		maps.Copy(settings.External, patch.External)
	case ActionKeyRemove:
		if settings.External != nil {
			for k := range patch.External {
				delete(settings.External, k)
			}
		}
	default:
		return ErrBadRequest
	}

	if patch.EventLog != nil {
		settings.EventLog = patch.EventLog
	}

	// Handle hooks update (if provided)
	if patch.Hooks != nil {
		settings.Hooks = *patch.Hooks
	}

	// Handle external-permissions update (if provided)
	if patch.ExternalPermissions != nil {
		settings.ExternalPermissions = patch.ExternalPermissions
	}

	// Handle forward-auth update (if provided)
	if patch.ForwardAuth != nil {
		settings.ForwardAuth = patch.ForwardAuth
	}

	// Handle auth settings update (if provided)
	if patch.Auth != nil {
		settings.Auth = patch.Auth
	}

	// Handle user-sync settings update (if provided)
	if patch.UserSync != nil {
		settings.UserSync = patch.UserSync
	}

	// Handle vault settings update (if provided). The struct only
	// carries a boolean today; we still update the whole pointer so
	// future fields (e.g. per-deployment item-type allowlist) get
	// the same patch-update treatment for free.
	if patch.Vault != nil {
		settings.Vault = patch.Vault
	}

	// Handle public-endpoints update (if provided). Full-replace
	// semantics: caller submits the desired final list. We:
	//   1. validate the whole list (per-entry + cross-entry
	//      conflicts) before touching state,
	//   2. fill in missing IDs and timestamps so the UI never has
	//      to mint them client-side,
	//   3. preserve CreatedAt for endpoints that already existed.
	if patch.PublicEndpoints != nil {
		incoming := *patch.PublicEndpoints
		if err := ValidatePublicEndpoints(incoming); err != nil {
			return err
		}
		for _, ep := range incoming {
			if ep.Mode != "external" || ep.External == nil {
				continue
			}
			if _, ok := settings.External[ep.External.Resource]; !ok {
				return fmt.Errorf("public endpoint %q: external resource %q not found: %w",
					ep.Name, ep.External.Resource, ErrBadRequest)
			}
		}
		// Build a lookup from the existing list so we can preserve
		// CreatedAt for endpoints the operator is editing rather
		// than creating.
		existing := make(map[string]PublicEndpoint, len(settings.PublicEndpoints))
		for _, ep := range settings.PublicEndpoints {
			if ep.ID != "" {
				existing[ep.ID] = ep
			}
		}
		now := time.Now().UTC()
		out := make([]PublicEndpoint, len(incoming))
		for i, ep := range incoming {
			if ep.ID == "" {
				id, err := newPublicEndpointID()
				if err != nil {
					return fmt.Errorf("public endpoint %q: %w", ep.Name, err)
				}
				ep.ID = id
			}
			if prev, ok := existing[ep.ID]; ok && !prev.CreatedAt.IsZero() {
				ep.CreatedAt = prev.CreatedAt
			} else {
				ep.CreatedAt = now
			}
			ep.UpdatedAt = now
			out[i] = ep
		}
		settings.PublicEndpoints = out
	}

	return s.UpdateSettings(ctx, settings)
}

// newPublicEndpointID mints a 16-byte hex identifier for a fresh
// public endpoint. Kept private to the service package so the bw and
// API layers can't accidentally invent their own scheme.
func newPublicEndpointID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate public endpoint id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// GetExternalPermissionsSettings returns the current external-permissions
// configuration or nil if none has been set. Used by the API layer to
// decide whether to enforce permissions under forward auth and by the
// permission resolver to map external groups to capability keys.
func (s *Service) GetExternalPermissionsSettings(ctx context.Context) *ExternalPermissionsSettings {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil
	}
	return settings.ExternalPermissions
}

// GetForwardAuthSettings returns the current forward-auth configuration
// or nil if none has been set. Used by the server layer to configure
// the forward-auth Slot at startup and on settings changes.
func (s *Service) GetForwardAuthSettings(ctx context.Context) *ForwardAuthSettings {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil
	}
	return settings.ForwardAuth
}

func (s *Service) UpdateSettings(ctx context.Context, settings *Settings) error {
	return s.store.Settings().Set(ctx, settings)
}

// SaveSettings persists a full Settings object — used by the auth migration path at boot.
func (s *Service) SaveSettings(ctx context.Context, settings *Settings) error {
	return s.store.Settings().Set(ctx, settings)
}
