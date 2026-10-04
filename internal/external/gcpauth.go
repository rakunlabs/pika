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
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

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

// gcpAuth holds service-account credentials and a cached OAuth2 access
// token. It is shared by the Secret Manager and Parameter Manager
// clients, which embed it.
type gcpAuth struct {
	errPrefix   string
	clientEmail string
	privateKey  *rsa.PrivateKey
	tokenURI    string
	httpClient  *http.Client

	mu       sync.RWMutex
	token    string
	tokenExp time.Time
}

// newGCPAuth parses a service account JSON key. errPrefix ("gcp",
// "gcp-parameter") prefixes every error so messages identify the backend.
func newGCPAuth(errPrefix, serviceAccountJSON string, httpClient *http.Client) (*gcpAuth, string, error) {
	var key gcpServiceAccountKey
	if err := json.Unmarshal([]byte(serviceAccountJSON), &key); err != nil {
		return nil, "", fmt.Errorf("%s: parsing service account JSON: %w", errPrefix, err)
	}

	if key.Type != "service_account" {
		return nil, "", fmt.Errorf("%s: expected key type \"service_account\", got %q", errPrefix, key.Type)
	}
	if key.ProjectID == "" {
		return nil, "", fmt.Errorf("%s: missing project_id in service account key", errPrefix)
	}
	if key.ClientEmail == "" {
		return nil, "", fmt.Errorf("%s: missing client_email in service account key", errPrefix)
	}
	if key.PrivateKey == "" {
		return nil, "", fmt.Errorf("%s: missing private_key in service account key", errPrefix)
	}
	if key.TokenURI == "" {
		key.TokenURI = "https://oauth2.googleapis.com/token"
	}

	privateKey, err := parseRSAPrivateKey(key.PrivateKey)
	if err != nil {
		return nil, "", fmt.Errorf("%s: parsing private key: %w", errPrefix, err)
	}

	return &gcpAuth{
		errPrefix:   errPrefix,
		clientEmail: key.ClientEmail,
		privateKey:  privateKey,
		tokenURI:    key.TokenURI,
		httpClient:  httpClient,
	}, key.ProjectID, nil
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
func (c *gcpAuth) ensureToken(ctx context.Context) error {
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

	now := time.Now()
	jwtToken, err := c.createJWT(now)
	if err != nil {
		return fmt.Errorf("%s: creating JWT: %w", c.errPrefix, err)
	}

	// Exchange JWT for access token
	formData := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {jwtToken},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURI, strings.NewReader(formData.Encode()))
	if err != nil {
		return fmt.Errorf("%s: creating token request: %w", c.errPrefix, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: executing token request: %w", c.errPrefix, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := readBody(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: reading token response: %w", c.errPrefix, err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: token request returned HTTP %d: %s", c.errPrefix, resp.StatusCode, string(respBody))
	}

	var tokenResp gcpTokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return fmt.Errorf("%s: parsing token response: %w", c.errPrefix, err)
	}

	if tokenResp.AccessToken == "" {
		return fmt.Errorf("%s: no access_token in token response", c.errPrefix)
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
func (c *gcpAuth) createJWT(now time.Time) (string, error) {
	header := map[string]string{
		"alg": "RS256",
		"typ": "JWT",
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("marshaling JWT header: %w", err)
	}

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

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerB64 + "." + claimsB64

	hash := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(nil, c.privateKey, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("signing JWT: %w", err)
	}

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// accessToken returns the cached token; call ensureToken first.
func (c *gcpAuth) accessToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token
}

// invalidateToken drops the cached token so the next call re-authenticates
// (e.g. after a 401/403 that may mean the token was revoked).
func (c *gcpAuth) invalidateToken() {
	c.mu.Lock()
	c.token = ""
	c.tokenExp = time.Time{}
	c.mu.Unlock()
}
