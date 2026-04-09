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

// ReadSecret reads a secret from GCP Secret Manager and returns its data as a map.
// It fetches the latest version, base64-decodes the payload, and attempts to unmarshal
// it as JSON. If the payload is not valid JSON, it returns {"value": <string>}.
func (c *GCPSecretManagerClient) ReadSecret(ctx context.Context, secretName string) (map[string]any, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
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
		return nil, fmt.Errorf("gcp: creating read request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gcp: executing read request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gcp: reading response: %w", err)
	}

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		// Token may have been revoked — clear it so next call re-authenticates
		c.mu.Lock()
		c.token = ""
		c.tokenExp = time.Time{}
		c.mu.Unlock()
		return nil, fmt.Errorf("gcp: returned HTTP %d (token may be expired): %s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("gcp: secret %q not found", secretName)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gcp: returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var accessResp gcpAccessSecretResponse
	if err := json.Unmarshal(respBody, &accessResp); err != nil {
		return nil, fmt.Errorf("gcp: parsing secret response: %w", err)
	}

	// Decode base64 payload
	decoded, err := base64.StdEncoding.DecodeString(accessResp.Payload.Data)
	if err != nil {
		return nil, fmt.Errorf("gcp: decoding secret payload: %w", err)
	}

	// Try to unmarshal as JSON
	var data map[string]any
	if err := json.Unmarshal(decoded, &data); err == nil {
		return data, nil
	}

	// Fallback: return as plain string value
	return map[string]any{"value": string(decoded)}, nil
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

