package external

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// GCPSecretManagerClient is a minimal HTTP client for GCP Secret Manager.
// It authenticates using a service account JSON key and caches the access token.
type GCPSecretManagerClient struct {
	*gcpAuth
	projectID string
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
// proxyMode/proxy control outbound proxy selection (see newHTTPClient).
func NewGCPSecretManagerClient(serviceAccountJSON, proxyMode, proxy string) (*GCPSecretManagerClient, error) {
	auth, projectID, err := newGCPAuth("gcp", serviceAccountJSON, newHTTPClient(proxyMode, proxy, nil))
	if err != nil {
		return nil, err
	}
	return &GCPSecretManagerClient{gcpAuth: auth, projectID: projectID}, nil
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

	token := c.accessToken()

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

	respBody, err := readBody(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("gcp: reading response: %w", err)
	}

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		// Token may have been revoked — clear it so next call re-authenticates
		c.invalidateToken()
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

	token := c.accessToken()

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

		respBody, err := readBody(resp.Body)
		resp.Body.Close()

		if err != nil {
			return nil, fmt.Errorf("gcp: reading list response: %w", err)
		}

		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			c.invalidateToken()
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
	if err := validateProxyConfig(p.Config.ProxyMode, p.Config.Proxy); err != nil {
		return fmt.Errorf("gcp: %w", err)
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
