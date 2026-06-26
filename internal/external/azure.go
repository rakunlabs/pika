package external

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Azure configures an Azure Key Vault external resource.
type Azure struct {
	// VaultURL is the Key Vault URL (e.g., "https://my-vault.vault.azure.net").
	VaultURL string `json:"vault_url"`
	// TenantID is the Azure AD tenant ID.
	TenantID string `json:"tenant_id"`
	// ClientID is the Azure AD application (client) ID.
	ClientID string `json:"client_id"`
	// ClientSecret is the Azure AD client secret.
	ClientSecret string `json:"client_secret"`

	// Proxy is an optional outbound HTTP/HTTPS/SOCKS5 proxy URL used
	// for all requests to Azure (AD token endpoint and Key Vault).
	// Empty falls back to the HTTP_PROXY / HTTPS_PROXY / NO_PROXY
	// environment variables.
	Proxy string `json:"proxy,omitempty"`
}

// AzureKeyVaultClient is a minimal HTTP client for Azure Key Vault.
type AzureKeyVaultClient struct {
	vaultURL     string
	tenantID     string
	clientID     string
	clientSecret string
	httpClient   *http.Client

	mu       sync.RWMutex
	token    string
	tokenExp time.Time
}

// NewAzureKeyVaultClient creates a new Azure Key Vault client.
// proxy is an optional outbound proxy URL; empty uses the environment
// proxy (see newHTTPClient).
func NewAzureKeyVaultClient(vaultURL, tenantID, clientID, clientSecret, proxy string) *AzureKeyVaultClient {
	return &AzureKeyVaultClient{
		vaultURL:     strings.TrimRight(vaultURL, "/"),
		tenantID:     tenantID,
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   newHTTPClient(proxy, nil),
	}
}

// ensureToken fetches or refreshes the Azure AD OAuth2 access token.
func (a *AzureKeyVaultClient) ensureToken(ctx context.Context) error {
	a.mu.RLock()
	hasToken := a.token != ""
	valid := time.Now().Before(a.tokenExp)
	a.mu.RUnlock()

	if hasToken && valid {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Double-check after acquiring write lock
	if a.token != "" && time.Now().Before(a.tokenExp) {
		return nil
	}

	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", a.tenantID)

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {a.clientID},
		"client_secret": {a.clientSecret},
		"scope":         {"https://vault.azure.net/.default"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("azure: creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("azure: executing token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("azure: reading token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("azure: token request HTTP %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return fmt.Errorf("azure: parsing token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return fmt.Errorf("azure: no access_token in response")
	}

	a.token = tokenResp.AccessToken
	// Refresh at 75% of expiry
	if tokenResp.ExpiresIn > 0 {
		a.tokenExp = time.Now().Add(time.Duration(float64(tokenResp.ExpiresIn)*0.75) * time.Second)
	} else {
		a.tokenExp = time.Now().Add(30 * time.Minute)
	}

	return nil
}

// ReadSecret reads a secret from Azure Key Vault.
func (a *AzureKeyVaultClient) ReadSecret(ctx context.Context, name string) (map[string]any, error) {
	if err := a.ensureToken(ctx); err != nil {
		return nil, err
	}

	a.mu.RLock()
	token := a.token
	a.mu.RUnlock()

	reqURL := fmt.Sprintf("%s/secrets/%s?api-version=7.4", a.vaultURL, name)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("azure: creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("azure: executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("azure: reading response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		a.mu.Lock()
		a.token = ""
		a.tokenExp = time.Time{}
		a.mu.Unlock()
		return nil, fmt.Errorf("azure: HTTP %d (token may be expired): %s", resp.StatusCode, string(body))
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("azure: secret %q not found", name)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("azure: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var secretResp struct {
		Value string `json:"value"`
		ID    string `json:"id"`
	}
	if err := json.Unmarshal(body, &secretResp); err != nil {
		return nil, fmt.Errorf("azure: parsing response: %w", err)
	}

	// Try to parse value as JSON
	var data map[string]any
	if err := json.Unmarshal([]byte(secretResp.Value), &data); err == nil {
		return data, nil
	}

	return map[string]any{"value": secretResp.Value}, nil
}

// ListSecrets lists secrets in Azure Key Vault.
func (a *AzureKeyVaultClient) ListSecrets(ctx context.Context) ([]string, error) {
	if err := a.ensureToken(ctx); err != nil {
		return nil, err
	}

	var allNames []string
	nextURL := fmt.Sprintf("%s/secrets?api-version=7.4", a.vaultURL)

	for nextURL != "" {
		names, next, err := a.listSecretsPage(ctx, nextURL)
		if err != nil {
			return nil, err
		}
		allNames = append(allNames, names...)
		nextURL = next
	}

	return allNames, nil
}

func (a *AzureKeyVaultClient) listSecretsPage(ctx context.Context, reqURL string) ([]string, string, error) {
	a.mu.RLock()
	token := a.token
	a.mu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("azure: creating list request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("azure: executing list request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("azure: reading list response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("azure: list HTTP %d: %s", resp.StatusCode, string(body))
	}

	var listResp struct {
		Value []struct {
			ID string `json:"id"`
		} `json:"value"`
		NextLink string `json:"nextLink"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, "", fmt.Errorf("azure: parsing list response: %w", err)
	}

	names := make([]string, 0, len(listResp.Value))
	for _, s := range listResp.Value {
		// ID is like "https://vault.vault.azure.net/secrets/my-secret"
		// Extract the name from the last path segment
		parts := strings.Split(strings.TrimRight(s.ID, "/"), "/")
		if len(parts) > 0 {
			names = append(names, parts[len(parts)-1])
		}
	}

	return names, listResp.NextLink, nil
}

// ── Provider ──────────────────────────────────────────────────────────
// AzureProvider exposes Azure Key Vault through the Provider interface.
// The OAuth2 client-credentials token is cached inside the AzureKeyVault
// Client, which itself lives in Service-managed cache (one per vault URL).

type AzureProvider struct {
	Config *Azure
	Deps   Deps
}

func (p *AzureProvider) Kind() string { return "azure" }

func (p *AzureProvider) Capabilities() Capabilities {
	return Capabilities{CanRead: true, CanList: true}
}

func (p *AzureProvider) Validate() error {
	if p.Config == nil {
		return fmt.Errorf("azure: config is required")
	}
	if strings.TrimSpace(p.Config.VaultURL) == "" {
		return fmt.Errorf("azure: vault_url is required")
	}
	if strings.TrimSpace(p.Config.TenantID) == "" || strings.TrimSpace(p.Config.ClientID) == "" || strings.TrimSpace(p.Config.ClientSecret) == "" {
		return fmt.Errorf("azure: tenant_id, client_id and client_secret are required")
	}
	if err := validateProxy(p.Config.Proxy); err != nil {
		return fmt.Errorf("azure: %w", err)
	}
	return nil
}

func (p *AzureProvider) Fetch(ctx context.Context, path string) ([]byte, error) {
	client := p.Deps.AzureClient(p.Config)
	data, err := client.ReadSecret(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("reading Azure secret %q: %w", path, err)
	}
	return json.Marshal(data)
}

func (p *AzureProvider) List(ctx context.Context, _ string) ([]string, error) {
	client := p.Deps.AzureClient(p.Config)
	return client.ListSecrets(ctx)
}

func (p *AzureProvider) Test(ctx context.Context) TestResult {
	paths, err := p.List(ctx, "")
	if err != nil {
		return TestResult{OK: false, Message: err.Error()}
	}
	msg := fmt.Sprintf("Reachable. %d secret(s) discovered.", len(paths))
	if len(paths) == 0 {
		msg = "Reachable. No secrets discovered."
	}
	return TestResult{OK: true, Message: msg, Sample: capSample(paths, 10)}
}

func (p *AzureProvider) Read(ctx context.Context, path string) (*Entry, error) {
	client := p.Deps.AzureClient(p.Config)
	data, err := client.ReadSecret(ctx, path)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(data)
	return &Entry{Data: data, Raw: raw, ContentType: "application/json"}, nil
}

func (p *AzureProvider) Write(ctx context.Context, path string, data map[string]any) error {
	return fmt.Errorf("azure: %w", ErrNotSupported)
}

func (p *AzureProvider) Delete(ctx context.Context, path string) error {
	return fmt.Errorf("azure: %w", ErrNotSupported)
}

func (p *AzureProvider) ListVersions(ctx context.Context, path string) ([]Version, error) {
	return nil, fmt.Errorf("azure: %w", ErrNotSupported)
}

func (p *AzureProvider) ReadVersion(ctx context.Context, path, version string) (*Entry, error) {
	return nil, fmt.Errorf("azure: %w", ErrNotSupported)
}
