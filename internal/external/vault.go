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
// It supports AppRole authentication with automatic token renewal.
type VaultClient struct {
	address    string
	httpClient *http.Client

	mu         sync.RWMutex
	token      string
	tokenExpAt time.Time

	// AppRole credentials for re-authentication
	appRole *VaultAppRole
}

// NewVaultClient creates a new Vault client for the given address.
func NewVaultClient(address string) *VaultClient {
	return &VaultClient{
		address: strings.TrimRight(address, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
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
		// Renew slightly before expiry (90% of lease)
		renewIn := time.Duration(float64(leaseDuration) * 0.9)
		vc.tokenExpAt = time.Now().Add(renewIn * time.Second)
	} else {
		vc.tokenExpAt = time.Time{} // no expiry
	}

	slog.Info("vault: authenticated via approle",
		"address", vc.address,
		"lease_duration", leaseDuration)

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
