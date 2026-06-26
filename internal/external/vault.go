package external

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// VaultClient is a minimal HTTP client for HashiCorp Vault.
// It supports AppRole authentication with automatic background token renewal.
type VaultClient struct {
	address    string
	httpClient *http.Client

	mu         sync.RWMutex
	token      string
	tokenExpAt time.Time

	// AppRole credentials for re-authentication
	appRole *VaultAppRole

	// Background renewal
	renewOnce sync.Once
	renewCtx  context.Context
}

// NewVaultClient creates a new Vault client for the given address.
// proxy is an optional outbound proxy URL; empty uses the environment
// proxy (see newHTTPClient).
func NewVaultClient(address, proxy string) *VaultClient {
	return &VaultClient{
		address:    strings.TrimRight(address, "/"),
		httpClient: newHTTPClient(proxy, nil),
	}
}

// SetToken sets a direct Vault token (bypasses AppRole login).
func (vc *VaultClient) SetToken(token string) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.token = token
	vc.tokenExpAt = time.Time{} // no expiry known for direct tokens
}

// SetAppRole configures AppRole credentials for automatic authentication.
func (vc *VaultClient) SetAppRole(appRole *VaultAppRole) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.appRole = appRole
}

// EnsureAuthenticated ensures the client has a valid Vault token.
// If the token is expired or missing, it re-authenticates via AppRole.
func (vc *VaultClient) EnsureAuthenticated(ctx context.Context) error {
	vc.mu.RLock()
	hasToken := vc.token != ""
	expired := !vc.tokenExpAt.IsZero() && time.Now().After(vc.tokenExpAt)
	vc.mu.RUnlock()

	if hasToken && !expired {
		return nil
	}

	vc.mu.Lock()
	defer vc.mu.Unlock()

	// Double-check after acquiring write lock
	if vc.token != "" && (vc.tokenExpAt.IsZero() || time.Now().Before(vc.tokenExpAt)) {
		return nil
	}

	if vc.appRole == nil {
		if vc.token != "" {
			return nil // have a direct token, no way to refresh
		}
		return fmt.Errorf("vault: no authentication method configured (no token or approle)")
	}

	// Login via AppRole
	token, leaseDuration, err := vc.loginAppRole(ctx, vc.appRole)
	if err != nil {
		return fmt.Errorf("vault: approle login failed: %w", err)
	}

	vc.token = token
	if leaseDuration > 0 {
		vc.tokenExpAt = time.Now().Add(time.Duration(leaseDuration) * time.Second)
	} else {
		vc.tokenExpAt = time.Time{} // no expiry
	}

	slog.Info("vault: authenticated via approle",
		"address", vc.address,
		"lease_duration", leaseDuration)

	// Start background renewal loop (only once per client)
	if vc.renewCtx != nil {
		vc.startRenewal()
	}

	return nil
}

// StartRenewal begins the background token renewal goroutine.
// Must be called with a context that lives for the duration of the server.
func (vc *VaultClient) StartRenewal(ctx context.Context) {
	vc.mu.Lock()
	vc.renewCtx = ctx
	vc.mu.Unlock()
}

func (vc *VaultClient) startRenewal() {
	ctx := vc.renewCtx
	vc.renewOnce.Do(func() {
		go vc.renewLoop(ctx)
	})
}

func (vc *VaultClient) renewLoop(ctx context.Context) {
	for {
		vc.mu.RLock()
		expAt := vc.tokenExpAt
		vc.mu.RUnlock()

		if expAt.IsZero() {
			// No expiry known (direct token), check again later
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Minute):
				continue
			}
		}

		// Renew at 75% of the remaining time
		remaining := time.Until(expAt)
		renewAt := time.Duration(float64(remaining) * 0.75)
		if renewAt < 10*time.Second {
			renewAt = 10 * time.Second
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(renewAt):
		}

		if err := vc.renewToken(ctx); err != nil {
			slog.Warn("vault: token renewal failed, re-authenticating via approle",
				"address", vc.address,
				"error", err)

			// Fall back to full re-login
			vc.mu.Lock()
			vc.token = ""
			vc.tokenExpAt = time.Time{}
			vc.mu.Unlock()

			if err := vc.EnsureAuthenticated(ctx); err != nil {
				slog.Error("vault: re-authentication failed",
					"address", vc.address,
					"error", err)
			}
		}
	}
}

// renewToken calls Vault's token renew-self endpoint to extend the current token's lease.
func (vc *VaultClient) renewToken(ctx context.Context) error {
	vc.mu.RLock()
	token := vc.token
	vc.mu.RUnlock()

	if token == "" {
		return fmt.Errorf("no token to renew")
	}

	renewURL := fmt.Sprintf("%s/v1/auth/token/renew-self", vc.address)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, renewURL, nil)
	if err != nil {
		return fmt.Errorf("creating renew request: %w", err)
	}
	req.Header.Set("X-Vault-Token", token)

	resp, err := vc.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("executing renew request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading renew response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("renew returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var authResp vaultAuthResponse
	if err := json.Unmarshal(respBody, &authResp); err != nil {
		return fmt.Errorf("parsing renew response: %w", err)
	}

	if authResp.Auth == nil || authResp.Auth.ClientToken == "" {
		return fmt.Errorf("no client_token in renew response")
	}

	vc.mu.Lock()
	vc.token = authResp.Auth.ClientToken
	if authResp.Auth.LeaseDuration > 0 {
		vc.tokenExpAt = time.Now().Add(time.Duration(authResp.Auth.LeaseDuration) * time.Second)
	}
	vc.mu.Unlock()

	slog.Debug("vault: token renewed",
		"address", vc.address,
		"lease_duration", authResp.Auth.LeaseDuration)

	return nil
}

// vaultAuthResponse represents the Vault auth response.
type vaultAuthResponse struct {
	Auth *struct {
		ClientToken   string `json:"client_token"`
		LeaseDuration int    `json:"lease_duration"`
	} `json:"auth"`
	Errors []string `json:"errors,omitempty"`
}

// loginAppRole authenticates using AppRole and returns a client token.
func (vc *VaultClient) loginAppRole(ctx context.Context, appRole *VaultAppRole) (string, int, error) {
	mountPath := appRole.GetAppRoleBasePath()
	loginURL := fmt.Sprintf("%s/v1/auth/%s/login", vc.address, mountPath)

	body, err := json.Marshal(map[string]string{
		"role_id":   appRole.RoleID,
		"secret_id": appRole.SecretID,
	})
	if err != nil {
		return "", 0, fmt.Errorf("marshaling login body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, bytes.NewReader(body))
	if err != nil {
		return "", 0, fmt.Errorf("creating login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := vc.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("executing login request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("reading login response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("login returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var authResp vaultAuthResponse
	if err := json.Unmarshal(respBody, &authResp); err != nil {
		return "", 0, fmt.Errorf("parsing login response: %w", err)
	}

	if authResp.Auth == nil || authResp.Auth.ClientToken == "" {
		errMsg := "no client_token in response"
		if len(authResp.Errors) > 0 {
			errMsg = strings.Join(authResp.Errors, "; ")
		}
		return "", 0, fmt.Errorf("login failed: %s", errMsg)
	}

	return authResp.Auth.ClientToken, authResp.Auth.LeaseDuration, nil
}

// vaultSecretResponse represents the Vault secret read response.
// Supports both KV v1 and KV v2 responses.
type vaultSecretResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []string        `json:"errors,omitempty"`
}

// vaultKVv2Data represents the inner data wrapper for KV v2.
type vaultKVv2Data struct {
	Data     json.RawMessage `json:"data"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// ReadSecret reads a secret from Vault at the given path.
// It returns the secret data as a map. For KV v2, it unwraps the inner data.
func (vc *VaultClient) ReadSecret(ctx context.Context, secretPath string) (map[string]any, error) {
	if err := vc.EnsureAuthenticated(ctx); err != nil {
		return nil, err
	}

	vc.mu.RLock()
	token := vc.token
	vc.mu.RUnlock()

	secretURL := fmt.Sprintf("%s/v1/%s", vc.address, strings.TrimLeft(secretPath, "/"))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, secretURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating read request: %w", err)
	}
	req.Header.Set("X-Vault-Token", token)

	resp, err := vc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing read request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		// Token may have been revoked — clear it so next call re-authenticates
		vc.mu.Lock()
		vc.token = ""
		vc.tokenExpAt = time.Time{}
		vc.mu.Unlock()
		return nil, fmt.Errorf("vault returned HTTP %d (token may be expired): %s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("vault secret not found at path %q", secretPath)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vault returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var secretResp vaultSecretResponse
	if err := json.Unmarshal(respBody, &secretResp); err != nil {
		return nil, fmt.Errorf("parsing secret response: %w", err)
	}

	if len(secretResp.Errors) > 0 {
		return nil, fmt.Errorf("vault errors: %s", strings.Join(secretResp.Errors, "; "))
	}

	// Try to unwrap KV v2 format (data.data pattern)
	data, err := unwrapSecretData(secretResp.Data)
	if err != nil {
		return nil, fmt.Errorf("unwrapping secret data: %w", err)
	}

	return data, nil
}

// ListSecrets lists secret keys at the given path using Vault's LIST method.
// Returns a list of key names. Keys ending with "/" are sub-directories.
func (vc *VaultClient) ListSecrets(ctx context.Context, listPath string) ([]string, error) {
	if err := vc.EnsureAuthenticated(ctx); err != nil {
		return nil, err
	}

	vc.mu.RLock()
	token := vc.token
	vc.mu.RUnlock()

	listURL := fmt.Sprintf("%s/v1/%s", vc.address, strings.TrimLeft(listPath, "/"))

	req, err := http.NewRequestWithContext(ctx, "LIST", listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating list request: %w", err)
	}
	req.Header.Set("X-Vault-Token", token)

	resp, err := vc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing list request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading list response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return []string{}, nil
	}

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		vc.mu.Lock()
		vc.token = ""
		vc.tokenExpAt = time.Time{}
		vc.mu.Unlock()
		return nil, fmt.Errorf("vault returned HTTP %d on list: %s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vault list returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse the list response: { data: { keys: ["key1", "key2/"] } }
	var listResp struct {
		Data *struct {
			Keys []string `json:"keys"`
		} `json:"data"`
		Errors []string `json:"errors,omitempty"`
	}

	if err := json.Unmarshal(respBody, &listResp); err != nil {
		return nil, fmt.Errorf("parsing list response: %w", err)
	}

	if len(listResp.Errors) > 0 {
		return nil, fmt.Errorf("vault list errors: %s", strings.Join(listResp.Errors, "; "))
	}

	if listResp.Data == nil {
		return []string{}, nil
	}

	return listResp.Data.Keys, nil
}

// WriteSecret writes a secret value at the given Vault path. The body
// shape differs between KV v2 (must wrap as {"data": {...}}) and KV v1
// (raw map at top level). Callers that go through the Provider don't
// need to care — VaultProvider routes the path through /data/ for v2
// and bare mount for v1, and this function only does the HTTP POST.
//
// secretPath is the FULL Vault API path (e.g. "secret/data/myapp/db"
// for v2 or "secret/myapp/db" for v1), not the user-facing relative
// path. The Provider builds that.
func (vc *VaultClient) WriteSecret(ctx context.Context, secretPath string, body map[string]any) error {
	if err := vc.EnsureAuthenticated(ctx); err != nil {
		return err
	}

	vc.mu.RLock()
	token := vc.token
	vc.mu.RUnlock()

	url := fmt.Sprintf("%s/v1/%s", vc.address, strings.TrimLeft(secretPath, "/"))

	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshaling write body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("creating write request: %w", err)
	}
	req.Header.Set("X-Vault-Token", token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := vc.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("executing write request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		vc.mu.Lock()
		vc.token = ""
		vc.tokenExpAt = time.Time{}
		vc.mu.Unlock()
		return fmt.Errorf("vault returned HTTP %d on write: %s", resp.StatusCode, string(respBody))
	}
	// 200 = KV v2 (returns metadata), 204 = KV v1 (no body).
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("vault write returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// DeleteSecret issues a Vault DELETE at the given API path. KV v2 soft-
// deletes (the version is hidden but recoverable from metadata); KV v1
// removes the key outright. The Provider chooses between /metadata/
// (destroy all versions) and /data/ (soft-delete latest) by building
// the path accordingly — this function just performs the HTTP DELETE.
func (vc *VaultClient) DeleteSecret(ctx context.Context, secretPath string) error {
	if err := vc.EnsureAuthenticated(ctx); err != nil {
		return err
	}

	vc.mu.RLock()
	token := vc.token
	vc.mu.RUnlock()

	url := fmt.Sprintf("%s/v1/%s", vc.address, strings.TrimLeft(secretPath, "/"))

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("creating delete request: %w", err)
	}
	req.Header.Set("X-Vault-Token", token)

	resp, err := vc.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("executing delete request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		vc.mu.Lock()
		vc.token = ""
		vc.tokenExpAt = time.Time{}
		vc.mu.Unlock()
		return fmt.Errorf("vault returned HTTP %d on delete: %s", resp.StatusCode, string(respBody))
	}
	// 204 = success, 404 = already gone (idempotent).
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("vault delete returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// VaultVersion is one entry in a Vault KV v2 version history.
type VaultVersion struct {
	Version      int    `json:"version"`
	CreatedTime  string `json:"created_time"`
	DeletionTime string `json:"deletion_time"`
	Destroyed    bool   `json:"destroyed"`
}

// ListVersions reads the version metadata for a Vault KV v2 path.
// metadataPath is the full /metadata/ path (e.g. "secret/metadata/db").
// KV v1 doesn't track versions; callers should branch on the KV layout
// before invoking this.
func (vc *VaultClient) ListVersions(ctx context.Context, metadataPath string) ([]VaultVersion, error) {
	if err := vc.EnsureAuthenticated(ctx); err != nil {
		return nil, err
	}

	vc.mu.RLock()
	token := vc.token
	vc.mu.RUnlock()

	url := fmt.Sprintf("%s/v1/%s", vc.address, strings.TrimLeft(metadataPath, "/"))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating metadata request: %w", err)
	}
	req.Header.Set("X-Vault-Token", token)

	resp, err := vc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing metadata request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vault metadata returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// Response shape:
	//   { data: { versions: { "1": {...}, "2": {...} }, current_version: 2 } }
	var metaResp struct {
		Data *struct {
			Versions map[string]struct {
				CreatedTime  string `json:"created_time"`
				DeletionTime string `json:"deletion_time"`
				Destroyed    bool   `json:"destroyed"`
			} `json:"versions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &metaResp); err != nil {
		return nil, fmt.Errorf("parsing metadata response: %w", err)
	}
	if metaResp.Data == nil {
		return nil, nil
	}

	out := make([]VaultVersion, 0, len(metaResp.Data.Versions))
	for k, v := range metaResp.Data.Versions {
		// Vault returns the version key as a string; parse to int so
		// we can sort newest-first below. Bad data → skip.
		var ver int
		if _, err := fmt.Sscanf(k, "%d", &ver); err != nil {
			continue
		}
		out = append(out, VaultVersion{
			Version:      ver,
			CreatedTime:  v.CreatedTime,
			DeletionTime: v.DeletionTime,
			Destroyed:    v.Destroyed,
		})
	}
	// Sort newest first.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Version > out[i].Version {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

// ReadSecretVersion reads a specific KV v2 version. dataPath must be
// the /data/ path; version is the integer version number.
func (vc *VaultClient) ReadSecretVersion(ctx context.Context, dataPath string, version int) (map[string]any, error) {
	if err := vc.EnsureAuthenticated(ctx); err != nil {
		return nil, err
	}

	vc.mu.RLock()
	token := vc.token
	vc.mu.RUnlock()

	url := fmt.Sprintf("%s/v1/%s?version=%d", vc.address, strings.TrimLeft(dataPath, "/"), version)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating version-read request: %w", err)
	}
	req.Header.Set("X-Vault-Token", token)

	resp, err := vc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing version-read request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("vault secret version %d not found at %q", version, dataPath)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vault version read returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var secretResp vaultSecretResponse
	if err := json.Unmarshal(respBody, &secretResp); err != nil {
		return nil, fmt.Errorf("parsing version response: %w", err)
	}
	return unwrapSecretData(secretResp.Data)
}

// unwrapSecretData attempts to extract the actual secret data.
// For KV v2: the response has { data: { data: {...}, metadata: {...} } }
// For KV v1: the response has { data: {...} }
func unwrapSecretData(raw json.RawMessage) (map[string]any, error) {
	if raw == nil {
		return nil, fmt.Errorf("no data in response")
	}

	// First try KV v2 format: see if data contains a "data" key with nested object
	var kvV2 vaultKVv2Data
	if err := json.Unmarshal(raw, &kvV2); err == nil && kvV2.Data != nil {
		// Check if the inner data is a valid JSON object
		var innerData map[string]any
		if err := json.Unmarshal(kvV2.Data, &innerData); err == nil {
			return innerData, nil
		}
	}

	// Fall back to KV v1 format: data is the secret itself
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("parsing secret data: %w", err)
	}

	return data, nil
}

// ── Provider ──────────────────────────────────────────────────────────
// VaultProvider is the unified Provider implementation for HashiCorp
// Vault. The shared cache of authenticated VaultClient instances lives
// in service.Service (token-renewal goroutines are tied to its life
// cycle) and is reached through Deps so this file stays free of
// service-layer imports.

// VaultProvider implements Provider for HashiCorp Vault KV.
type VaultProvider struct {
	Config *Vault
	Deps   Deps
}

func (p *VaultProvider) Kind() string { return "vault" }

func (p *VaultProvider) Capabilities() Capabilities {
	// Vault KV supports the full set. Even KV v1 honours read/write/
	// delete; only versions are KV-v2-specific. The browser still calls
	// ListVersions unconditionally — on a v1 mount the response just
	// comes back empty, which the SPA treats as "single-version" mode.
	return Capabilities{CanRead: true, CanList: true, CanWrite: true, CanDelete: true, CanVersions: true}
}

func (p *VaultProvider) Validate() error {
	if p.Config == nil {
		return fmt.Errorf("vault: config is required")
	}
	if strings.TrimSpace(p.Config.Address) == "" {
		return fmt.Errorf("vault: address is required")
	}
	if strings.TrimSpace(p.Config.Mount) == "" {
		return fmt.Errorf("vault: mount is required")
	}
	// AppRole is the only currently-supported credential bundle when
	// the UI is the writer; a bare static token is allowed but only
	// surfaces when an operator hand-edits settings. We do NOT reject
	// the token-only path here because that would break those installs.
	if p.Config.AppRole != nil {
		if strings.TrimSpace(p.Config.AppRole.RoleID) == "" || strings.TrimSpace(p.Config.AppRole.SecretID) == "" {
			return fmt.Errorf("vault: app_role.role_id and app_role.secret_id are required")
		}
	}
	if err := validateProxy(p.Config.Proxy); err != nil {
		return fmt.Errorf("vault: %w", err)
	}
	return nil
}

func (p *VaultProvider) Fetch(ctx context.Context, path string) ([]byte, error) {
	client := p.Deps.VaultClient(ctx, p.Config)

	mount := p.Config.Mount
	var secretPath string
	if path != "" {
		secretPath = strings.TrimRight(mount, "/") + "/" + strings.TrimLeft(path, "/")
	} else {
		secretPath = mount
	}

	data, err := client.ReadSecret(ctx, secretPath)
	if err != nil {
		return nil, fmt.Errorf("reading vault secret at %q: %w", secretPath, err)
	}

	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("serializing vault secret data: %w", err)
	}
	return jsonBytes, nil
}

// List enumerates secrets under the mount, trying KV v2 metadata first
// and falling back to KV v1 layout. The two-tier strategy mirrors the
// pre-refactor behaviour so existing installs see no change.
func (p *VaultProvider) List(ctx context.Context, prefix string) ([]string, error) {
	client := p.Deps.VaultClient(ctx, p.Config)
	mount := p.Config.Mount

	listPath := strings.TrimRight(mount, "/") + "/metadata/"
	if prefix != "" {
		listPath += strings.TrimLeft(prefix, "/")
	}

	keys, err := client.ListSecrets(ctx, listPath)
	if err != nil {
		// Retry as KV v1 (direct mount, no /metadata/ segment).
		listPath = strings.TrimRight(mount, "/") + "/"
		if prefix != "" {
			listPath += strings.TrimLeft(prefix, "/")
		}
		keys, err = client.ListSecrets(ctx, listPath)
		if err != nil {
			return nil, err
		}
	}
	return keys, nil
}

func (p *VaultProvider) Test(ctx context.Context) TestResult {
	paths, err := p.List(ctx, "")
	if err != nil {
		return TestResult{OK: false, Message: err.Error()}
	}
	msg := fmt.Sprintf("Reachable. %d path(s) discovered.", len(paths))
	if len(paths) == 0 {
		msg = "Reachable. No paths discovered (this may be expected for empty mounts)."
	}
	return TestResult{OK: true, Message: msg, Sample: capSample(paths, 10)}
}

// kvDataPath assembles the Vault API path for a single secret. KV v2
// secrets live under /data/, v1 secrets directly under the mount. We
// try v2 first by default — operators on v1 mounts will see the read
// fail with a not-found and the browser falls back to the v1 layout
// transparently via Fetch (which already handles the discrepancy).
func (p *VaultProvider) kvDataPath(path string) string {
	mount := strings.TrimRight(p.Config.Mount, "/")
	if path == "" {
		return mount + "/data"
	}
	return mount + "/data/" + strings.TrimLeft(path, "/")
}

// kvMetadataPath is the metadata endpoint for KV v2 (version history
// and destructive delete-all). v1 has no equivalent — callers should
// branch on Capabilities (or just accept the not-found that comes
// back if they call this against a v1 mount).
func (p *VaultProvider) kvMetadataPath(path string) string {
	mount := strings.TrimRight(p.Config.Mount, "/")
	if path == "" {
		return mount + "/metadata"
	}
	return mount + "/metadata/" + strings.TrimLeft(path, "/")
}

// Read returns the latest version of a secret as an Entry. We try the
// KV v2 /data/ path first; if it 404s we retry against the bare mount
// path (KV v1). This is consistent with what Fetch does internally
// and lets the browser work against both KV layouts without asking
// the operator to declare it.
func (p *VaultProvider) Read(ctx context.Context, path string) (*Entry, error) {
	client := p.Deps.VaultClient(ctx, p.Config)
	// Try KV v2 layout first.
	data, err := client.ReadSecret(ctx, p.kvDataPath(path))
	if err != nil && strings.Contains(err.Error(), "not found") {
		// Fall back to KV v1 (no /data/ segment).
		mount := strings.TrimRight(p.Config.Mount, "/")
		v1Path := mount
		if path != "" {
			v1Path = mount + "/" + strings.TrimLeft(path, "/")
		}
		data, err = client.ReadSecret(ctx, v1Path)
	}
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(data)
	return &Entry{Data: data, Raw: raw, ContentType: "application/json"}, nil
}

// Write puts a new value at the path. The KV v2 API requires the body
// to be wrapped as {"data": {...}}; v1 takes the map at the top level.
// We try v2 first; on 404/400 we retry as v1. Vault returns 404 when
// the *path* doesn't exist; for v1 mounts the first write actually
// returns 204 so the v2 attempt's 404 is the right signal.
func (p *VaultProvider) Write(ctx context.Context, path string, data map[string]any) error {
	client := p.Deps.VaultClient(ctx, p.Config)

	// KV v2 attempt.
	v2Body := map[string]any{"data": data}
	if err := client.WriteSecret(ctx, p.kvDataPath(path), v2Body); err == nil {
		return nil
	} else if !strings.Contains(err.Error(), "HTTP 404") && !strings.Contains(err.Error(), "HTTP 405") {
		// Real error (auth/perm/etc.) — surface it.
		return err
	}

	// KV v1 fallback.
	mount := strings.TrimRight(p.Config.Mount, "/")
	v1Path := mount + "/" + strings.TrimLeft(path, "/")
	return client.WriteSecret(ctx, v1Path, data)
}

// Delete soft-deletes the latest version under KV v2 (use /metadata/
// to destroy all versions) and outright removes under v1. We attempt
// v2 first, fall through to v1 on the same not-found signal Write
// uses.
func (p *VaultProvider) Delete(ctx context.Context, path string) error {
	client := p.Deps.VaultClient(ctx, p.Config)

	// v2: DELETE /data/ soft-deletes latest version.
	if err := client.DeleteSecret(ctx, p.kvDataPath(path)); err == nil {
		return nil
	} else if !strings.Contains(err.Error(), "HTTP 404") && !strings.Contains(err.Error(), "HTTP 405") {
		return err
	}

	// v1 fallback.
	mount := strings.TrimRight(p.Config.Mount, "/")
	v1Path := mount + "/" + strings.TrimLeft(path, "/")
	return client.DeleteSecret(ctx, v1Path)
}

// ListVersions returns the version history for a KV v2 path. On KV v1
// the metadata endpoint doesn't exist (404) — we translate that into
// an empty slice so the browser doesn't show a spurious error; the SPA
// then renders the entry as a single, unversioned record.
func (p *VaultProvider) ListVersions(ctx context.Context, path string) ([]Version, error) {
	client := p.Deps.VaultClient(ctx, p.Config)
	raws, err := client.ListVersions(ctx, p.kvMetadataPath(path))
	if err != nil {
		// 404 from a v1 mount: not an error, just no versions.
		if strings.Contains(err.Error(), "HTTP 404") || strings.Contains(err.Error(), "HTTP 405") {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Version, 0, len(raws))
	for _, v := range raws {
		out = append(out, Version{
			ID:        fmt.Sprintf("%d", v.Version),
			CreatedAt: v.CreatedTime,
			Deleted:   v.DeletionTime != "",
			Destroyed: v.Destroyed,
		})
	}
	return out, nil
}

// ReadVersion fetches a specific historical version. Only meaningful
// for KV v2; on v1 the underlying call 404s and we surface the error.
func (p *VaultProvider) ReadVersion(ctx context.Context, path string, version string) (*Entry, error) {
	var ver int
	if _, err := fmt.Sscanf(version, "%d", &ver); err != nil {
		return nil, fmt.Errorf("invalid vault version %q: %w", version, err)
	}
	client := p.Deps.VaultClient(ctx, p.Config)
	data, err := client.ReadSecretVersion(ctx, p.kvDataPath(path), ver)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(data)
	return &Entry{Data: data, Raw: raw, ContentType: "application/json", Version: version}, nil
}
