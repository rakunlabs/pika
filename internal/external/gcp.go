package external

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// GCPSecretManagerClient is a minimal HTTP client for GCP Secret Manager.
// It authenticates using a service account JSON key and caches the access token.
type GCPSecretManagerClient struct {
	projectID   string
	clientEmail string
	privateKey  *rsa.PrivateKey
	tokenURI    string
	httpClient  *http.Client

	mu       sync.RWMutex
	token    string
	tokenExp time.Time
}

// gcpServiceAccountKey represents the relevant fields of a GCP service account JSON key.
type gcpServiceAccountKey struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	TokenURI     string `json:"token_uri"`
}

// gcpTokenResponse represents the OAuth2 token response.
type gcpTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

// gcpAccessSecretResponse represents the Secret Manager access response.
type gcpAccessSecretResponse struct {
	Payload struct {
		Data string `json:"data"`
	} `json:"payload"`
}

// gcpListSecretsResponse represents the Secret Manager list response.
type gcpListSecretsResponse struct {
	Secrets []struct {
		Name string `json:"name"`
	} `json:"secrets"`
	NextPageToken string `json:"nextPageToken"`
}

// NewGCPSecretManagerClient creates a new GCP Secret Manager client from a service account JSON key.
func NewGCPSecretManagerClient(serviceAccountJSON string) (*GCPSecretManagerClient, error) {
	var key gcpServiceAccountKey
	if err := json.Unmarshal([]byte(serviceAccountJSON), &key); err != nil {
		return nil, fmt.Errorf("gcp: parsing service account JSON: %w", err)
	}

	if key.Type != "service_account" {
		return nil, fmt.Errorf("gcp: expected key type \"service_account\", got %q", key.Type)
	}

	if key.ProjectID == "" {
		return nil, fmt.Errorf("gcp: missing project_id in service account key")
	}

	if key.ClientEmail == "" {
		return nil, fmt.Errorf("gcp: missing client_email in service account key")
	}

	if key.PrivateKey == "" {
		return nil, fmt.Errorf("gcp: missing private_key in service account key")
	}

	if key.TokenURI == "" {
		key.TokenURI = "https://oauth2.googleapis.com/token"
	}

	// Parse the PEM-encoded RSA private key
	privateKey, err := parseRSAPrivateKey(key.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("gcp: parsing private key: %w", err)
	}

	return &GCPSecretManagerClient{
		projectID:   key.ProjectID,
		clientEmail: key.ClientEmail,
		privateKey:  privateKey,
		tokenURI:    key.TokenURI,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// parseRSAPrivateKey decodes a PEM-encoded RSA private key (PKCS#1 or PKCS#8).
func parseRSAPrivateKey(pemData string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in private key")
	}

	// Try PKCS#1 first
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	// Try PKCS#8
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key as PKCS#1 or PKCS#8: %w", err)
	}

	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not RSA")
	}

	return rsaKey, nil
}

// ensureToken ensures the client has a valid access token, refreshing if needed.
// It refreshes at 75% of the token's lifetime to avoid using an almost-expired token.
func (c *GCPSecretManagerClient) ensureToken(ctx context.Context) error {
	c.mu.RLock()
	hasToken := c.token != ""
	expired := !c.tokenExp.IsZero() && time.Now().After(c.tokenExp)
	c.mu.RUnlock()

	if hasToken && !expired {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.token != "" && (c.tokenExp.IsZero() || time.Now().Before(c.tokenExp)) {
		return nil
	}

	// Create JWT
	now := time.Now()
	jwtToken, err := c.createJWT(now)
	if err != nil {
		return fmt.Errorf("gcp: creating JWT: %w", err)
	}

	// Exchange JWT for access token
	formData := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {jwtToken},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURI, strings.NewReader(formData.Encode()))
	if err != nil {
		return fmt.Errorf("gcp: creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("gcp: executing token request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("gcp: reading token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gcp: token request returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var tokenResp gcpTokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return fmt.Errorf("gcp: parsing token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return fmt.Errorf("gcp: no access_token in token response")
	}

	c.token = tokenResp.AccessToken
	if tokenResp.ExpiresIn > 0 {
		// Refresh at 75% of expiry to avoid using an almost-expired token
		refreshDuration := time.Duration(float64(tokenResp.ExpiresIn)*0.75) * time.Second
		c.tokenExp = now.Add(refreshDuration)
	} else {
		c.tokenExp = now.Add(45 * time.Minute) // default 75% of 1 hour
	}

	return nil
}

// createJWT builds and signs a JWT for the Google OAuth2 token exchange.
func (c *GCPSecretManagerClient) createJWT(now time.Time) (string, error) {
	// Header
	header := map[string]string{
		"alg": "RS256",
		"typ": "JWT",
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("marshaling JWT header: %w", err)
	}

	// Claims
	claims := map[string]any{
		"iss":   c.clientEmail,
		"scope": "https://www.googleapis.com/auth/cloud-platform",
		"aud":   c.tokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshaling JWT claims: %w", err)
	}

	// Encode header and claims
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerB64 + "." + claimsB64

	// Sign with RSA-SHA256
	hash := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(nil, c.privateKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("signing JWT: %w", err)
	}

	signatureB64 := base64.RawURLEncoding.EncodeToString(signature)

	return signingInput + "." + signatureB64, nil
}

// ReadSecret reads a secret from GCP Secret Manager and returns its
// data as a map suitable for the inheritance pipeline.
//
// Decoding rules (legacy, preserved for Fetch / inheritance callers):
//   - If the decoded payload parses as a JSON object → that object.
//   - Otherwise → {"value": "<string>"}.
//
// New callers that need access to the original bytes (raw mode in
// GCPProvider.Read) should use ReadSecretRaw instead.
func (c *GCPSecretManagerClient) ReadSecret(ctx context.Context, secretName string) (map[string]any, error) {
	data, raw, err := c.ReadSecretRaw(ctx, secretName)
	if err != nil {
		return nil, err
	}
	if data != nil {
		return data, nil
	}
	return map[string]any{"value": string(raw)}, nil
}

// ReadSecretRaw fetches the latest secret version, base64-decodes the
// payload, and reports both the decoded bytes and (when applicable)
// the parsed JSON-object view. data is nil when the payload is not a
// JSON object — callers should use raw bytes in that case. raw is
// always populated on success.
//
// The split lets GCPProvider.Read serve the secret to direct callers
// with the operator's preferred Content-Type, without losing the
// JSON-map view the inheritance pipeline still wants.
func (c *GCPSecretManagerClient) ReadSecretRaw(ctx context.Context, secretName string) (map[string]any, []byte, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, nil, err
	}

	c.mu.RLock()
	token := c.token
	c.mu.RUnlock()

	secretURL := fmt.Sprintf(
		"https://secretmanager.googleapis.com/v1/projects/%s/secrets/%s/versions/latest:access",
		c.projectID, secretName,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, secretURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("gcp: creating read request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("gcp: executing read request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("gcp: reading response: %w", err)
	}

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		// Token may have been revoked — clear it so next call re-authenticates
		c.mu.Lock()
		c.token = ""
		c.tokenExp = time.Time{}
		c.mu.Unlock()
		return nil, nil, fmt.Errorf("gcp: returned HTTP %d (token may be expired): %s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil, fmt.Errorf("gcp: secret %q not found", secretName)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("gcp: returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var accessResp gcpAccessSecretResponse
	if err := json.Unmarshal(respBody, &accessResp); err != nil {
		return nil, nil, fmt.Errorf("gcp: parsing secret response: %w", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(accessResp.Payload.Data)
	if err != nil {
		return nil, nil, fmt.Errorf("gcp: decoding secret payload: %w", err)
	}

	// Try to unmarshal as a JSON object so inheritance callers get a
	// structured view. Non-object JSON (arrays, scalars) intentionally
	// falls through to "raw only" because the merge layer can't merge
	// those anyway.
	var data map[string]any
	if err := json.Unmarshal(decoded, &data); err == nil {
		return data, decoded, nil
	}

	return nil, decoded, nil
}

// ListSecrets lists all secret names in the GCP project.
// It handles pagination and extracts short names from full resource paths.
func (c *GCPSecretManagerClient) ListSecrets(ctx context.Context) ([]string, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}

	c.mu.RLock()
	token := c.token
	c.mu.RUnlock()

	var allNames []string
	pageToken := ""

	for {
		listURL := fmt.Sprintf(
			"https://secretmanager.googleapis.com/v1/projects/%s/secrets?pageSize=100",
			c.projectID,
		)
		if pageToken != "" {
			listURL += "&pageToken=" + url.QueryEscape(pageToken)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
		if err != nil {
			return nil, fmt.Errorf("gcp: creating list request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("gcp: executing list request: %w", err)
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if err != nil {
			return nil, fmt.Errorf("gcp: reading list response: %w", err)
		}

		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			c.mu.Lock()
			c.token = ""
			c.tokenExp = time.Time{}
			c.mu.Unlock()
			return nil, fmt.Errorf("gcp: list returned HTTP %d (token may be expired): %s", resp.StatusCode, string(respBody))
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("gcp: list returned HTTP %d: %s", resp.StatusCode, string(respBody))
		}

		var listResp gcpListSecretsResponse
		if err := json.Unmarshal(respBody, &listResp); err != nil {
			return nil, fmt.Errorf("gcp: parsing list response: %w", err)
		}

		for _, secret := range listResp.Secrets {
			// Extract short name from "projects/{project}/secrets/{name}"
			name := extractSecretName(secret.Name)
			allNames = append(allNames, name)
		}

		if listResp.NextPageToken == "" {
			break
		}
		pageToken = listResp.NextPageToken
	}

	return allNames, nil
}

// extractSecretName extracts the short secret name from a full resource path.
// e.g., "projects/my-project/secrets/my-secret" -> "my-secret"
func extractSecretName(fullName string) string {
	parts := strings.Split(fullName, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return fullName
}

// ── Provider ──────────────────────────────────────────────────────────
// GCPProvider implements Provider for GCP Secret Manager. The signed-
// JWT exchange that produces an access token is expensive, so the
// resulting client is cached in Service and we route through Deps.

type GCPProvider struct {
	Config *GCP
	Deps   Deps
}

func (p *GCPProvider) Kind() string { return "gcp" }

func (p *GCPProvider) Capabilities() Capabilities {
	return Capabilities{CanRead: true, CanList: true}
}

func (p *GCPProvider) Validate() error {
	if p.Config == nil {
		return fmt.Errorf("gcp: config is required")
	}
	if strings.TrimSpace(p.Config.ServiceAccountJSON) == "" {
		return fmt.Errorf("gcp: service_account_json is required")
	}
	// Cheap structural check — full parsing happens at first call.
	var probe map[string]any
	if err := json.Unmarshal([]byte(p.Config.ServiceAccountJSON), &probe); err != nil {
		return fmt.Errorf("gcp: service_account_json is not valid JSON: %w", err)
	}
	if _, ok := probe["project_id"]; !ok {
		return fmt.Errorf("gcp: service_account_json missing project_id")
	}
	return nil
}

func (p *GCPProvider) Fetch(ctx context.Context, path string) ([]byte, error) {
	client, err := p.Deps.GCPClient(p.Config)
	if err != nil {
		return nil, err
	}
	data, err := client.ReadSecret(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("reading GCP secret %q: %w", path, err)
	}
	return json.Marshal(data)
}

func (p *GCPProvider) List(ctx context.Context, _ string) ([]string, error) {
	client, err := p.Deps.GCPClient(p.Config)
	if err != nil {
		return nil, err
	}
	return client.ListSecrets(ctx)
}

func (p *GCPProvider) Test(ctx context.Context) TestResult {
	paths, err := p.List(ctx, "")
	if err != nil {
		return TestResult{OK: false, Message: err.Error()}
	}
	msg := fmt.Sprintf("Reachable. %d secret(s) discovered.", len(paths))
	if len(paths) == 0 {
		msg = "Reachable. No secrets discovered (check IAM scope on the service account)."
	}
	return TestResult{OK: true, Message: msg, Sample: capSample(paths, 10)}
}

// Read returns the secret to direct callers (public endpoints,
// /external/{name}/read). Behaviour depends on the resource config:
//
//   - p.Config.GetRawValue() == true (default for new resources):
//     return the GCP payload bytes as-is, tagged with the operator's
//     configured Content-Type (default application/yaml). When the
//     payload is structured JSON we also surface the parsed map in
//     Data so UI-level inspectors (which prefer Data when present)
//     can still render a key/value view; the public-endpoint writer
//     (writeExternalEntry) prefers Raw + ContentType so direct
//     serving uses the raw bytes path.
//
//   - p.Config.GetRawValue() == false (explicit opt-out): legacy
//     behaviour — non-JSON payloads are wrapped as
//     `{"value": "<string>"}` and served as application/json.
func (p *GCPProvider) Read(ctx context.Context, path string) (*Entry, error) {
	client, err := p.Deps.GCPClient(p.Config)
	if err != nil {
		return nil, err
	}

	data, raw, err := client.ReadSecretRaw(ctx, path)
	if err != nil {
		return nil, err
	}

	if p.Config.GetRawValue() {
		// Raw mode: serve operator's bytes with operator's Content-Type.
		// Data is included only when the payload was a JSON object so
		// API consumers that read Entry.Data still get a structured
		// view; writeExternalEntry takes the Raw+ContentType path so
		// direct serving stays byte-exact.
		return &Entry{
			Data:        data,
			Raw:         raw,
			ContentType: p.Config.GetContentType(),
		}, nil
	}

	// Legacy mode: rebuild the wrapped JSON map and serve as JSON. We
	// reconstruct the wrapper here (instead of calling ReadSecret) so
	// the read+wrap split stays in one place and the raw bytes we
	// already paid the network cost for don't get thrown away.
	if data == nil {
		data = map[string]any{"value": string(raw)}
	}
	wrappedRaw, _ := json.Marshal(data)
	return &Entry{Data: data, Raw: wrappedRaw, ContentType: "application/json"}, nil
}

func (p *GCPProvider) Write(ctx context.Context, path string, data map[string]any) error {
	return fmt.Errorf("gcp: %w", ErrNotSupported)
}

func (p *GCPProvider) Delete(ctx context.Context, path string) error {
	return fmt.Errorf("gcp: %w", ErrNotSupported)
}

func (p *GCPProvider) ListVersions(ctx context.Context, path string) ([]Version, error) {
	return nil, fmt.Errorf("gcp: %w", ErrNotSupported)
}

func (p *GCPProvider) ReadVersion(ctx context.Context, path, version string) (*Entry, error) {
	return nil, fmt.Errorf("gcp: %w", ErrNotSupported)
}
