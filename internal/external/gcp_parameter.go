package external

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// GCPParameterManagerClient is a minimal HTTP client for GCP Parameter
// Manager. It mirrors GCPSecretManagerClient (same JWT-bearer OAuth2
// exchange, same cloud-platform scope) but targets a different service
// endpoint and resource model. Kept as its own type — and its own
// file — so the Secret Manager and Parameter Manager backends evolve
// independently and one can be removed/updated without dragging the
// other along.
//
// The duplication of the auth/token logic is deliberate: a shared
// "gcpAuth" helper sounds nice on paper but in practice every backend
// here keeps its own credential lifecycle so the cache invalidation
// rules (clear token on 401/403, refresh at 75% of expiry) stay
// readable next to the API calls that actually trigger them.
type GCPParameterManagerClient struct {
	projectID string
	location  string
	// apiHost is the service hostname to call. For "global" this is
	// `parametermanager.googleapis.com`; for any regional location it
	// switches to the per-region replicated endpoint
	// `parametermanager.<region>.rep.googleapis.com`. The global host
	// refuses cross-location queries with a misleading 403 that mimics
	// an IAM error, so the routing decision belongs here at client
	// construction time rather than in the per-request code paths.
	apiHost     string
	clientEmail string
	privateKey  *rsa.PrivateKey
	tokenURI    string
	httpClient  *http.Client

	mu       sync.RWMutex
	token    string
	tokenExp time.Time
}

// gcpRenderParameterResponse represents the Parameter Manager
// :render response shape. We use :render (not :get) so that any
// JSON/YAML payload referencing Secret Manager versions is resolved
// server-side — the alternative would be doing two round trips for
// every parameter that embeds a secret.
type gcpRenderParameterResponse struct {
	ParameterVersion string `json:"parameterVersion"`
	Payload          struct {
		Data string `json:"data"`
	} `json:"payload"`
	RenderedPayload string `json:"renderedPayload"`
}

// gcpListParametersResponse represents the Parameter Manager list
// response. Same pagination contract as Secret Manager.
type gcpListParametersResponse struct {
	Parameters []struct {
		Name string `json:"name"`
	} `json:"parameters"`
	NextPageToken string `json:"nextPageToken"`
}

// gcpListParameterVersionsResponse represents the per-parameter
// versions list response. Fields mirror the
// projects.locations.parameters.versions REST shape — we only project
// what the browser actually renders (name, timestamps, disabled flag)
// to keep the wire model small. `disabled` is the closest Parameter
// Manager equivalent of Vault KV v2's `deletion_time`: the version
// still exists but isn't accessible until re-enabled.
type gcpListParameterVersionsResponse struct {
	ParameterVersions []struct {
		Name       string `json:"name"`
		CreateTime string `json:"createTime"`
		Disabled   bool   `json:"disabled"`
	} `json:"parameterVersions"`
	NextPageToken string `json:"nextPageToken"`
}

// NewGCPParameterManagerClient creates a new GCP Parameter Manager
// client from a service account JSON key. Location defaults to
// "global" when empty.
func NewGCPParameterManagerClient(serviceAccountJSON, location, proxy string) (*GCPParameterManagerClient, error) {
	var key gcpServiceAccountKey
	if err := json.Unmarshal([]byte(serviceAccountJSON), &key); err != nil {
		return nil, fmt.Errorf("gcp-parameter: parsing service account JSON: %w", err)
	}

	if key.Type != "service_account" {
		return nil, fmt.Errorf("gcp-parameter: expected key type \"service_account\", got %q", key.Type)
	}
	if key.ProjectID == "" {
		return nil, fmt.Errorf("gcp-parameter: missing project_id in service account key")
	}
	if key.ClientEmail == "" {
		return nil, fmt.Errorf("gcp-parameter: missing client_email in service account key")
	}
	if key.PrivateKey == "" {
		return nil, fmt.Errorf("gcp-parameter: missing private_key in service account key")
	}
	if key.TokenURI == "" {
		key.TokenURI = "https://oauth2.googleapis.com/token"
	}

	privateKey, err := parseRSAPrivateKey(key.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("gcp-parameter: parsing private key: %w", err)
	}

	if location == "" {
		location = "global"
	}

	return &GCPParameterManagerClient{
		projectID:   key.ProjectID,
		location:    location,
		apiHost:     parameterManagerAPIHost(location),
		clientEmail: key.ClientEmail,
		privateKey:  privateKey,
		tokenURI:    key.TokenURI,
		httpClient:  newHTTPClient(proxy, nil),
	}, nil
}

// parameterManagerAPIHost returns the service hostname for the
// requested location. GCP exposes Parameter Manager through a global
// endpoint for "global" parameters and through per-region endpoints
// (`parametermanager.<region>.rep.googleapis.com`) for everything
// else. The global endpoint refuses requests for regional locations
// with HTTP 403 "Read access to project ... was denied", which is
// indistinguishable from an IAM error in the response body — hence
// this routing has to happen on the client side.
//
// Reference:
//
//	https://cloud.google.com/secret-manager/parameter-manager/docs/list-parameter#REST_2
func parameterManagerAPIHost(location string) string {
	if location == "" || location == "global" {
		return "parametermanager.googleapis.com"
	}
	return "parametermanager." + location + ".rep.googleapis.com"
}

// ensureToken ensures the client has a valid access token, refreshing
// if needed. Mirrors GCPSecretManagerClient.ensureToken; see that
// implementation for the rationale on the 75% refresh window.
func (c *GCPParameterManagerClient) ensureToken(ctx context.Context) error {
	c.mu.RLock()
	hasToken := c.token != ""
	expired := !c.tokenExp.IsZero() && time.Now().After(c.tokenExp)
	c.mu.RUnlock()

	if hasToken && !expired {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && (c.tokenExp.IsZero() || time.Now().Before(c.tokenExp)) {
		return nil
	}

	now := time.Now()
	jwtToken, err := c.createJWT(now)
	if err != nil {
		return fmt.Errorf("gcp-parameter: creating JWT: %w", err)
	}

	formData := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {jwtToken},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURI, strings.NewReader(formData.Encode()))
	if err != nil {
		return fmt.Errorf("gcp-parameter: creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("gcp-parameter: executing token request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("gcp-parameter: reading token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gcp-parameter: token request returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var tokenResp gcpTokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return fmt.Errorf("gcp-parameter: parsing token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return fmt.Errorf("gcp-parameter: no access_token in token response")
	}

	c.token = tokenResp.AccessToken
	if tokenResp.ExpiresIn > 0 {
		refreshDuration := time.Duration(float64(tokenResp.ExpiresIn)*0.75) * time.Second
		c.tokenExp = now.Add(refreshDuration)
	} else {
		c.tokenExp = now.Add(45 * time.Minute)
	}

	return nil
}

// createJWT builds and signs a JWT for the Google OAuth2 token
// exchange. Same scope ("cloud-platform") as the Secret Manager
// client — Parameter Manager doesn't need a narrower scope.
func (c *GCPParameterManagerClient) createJWT(now time.Time) (string, error) {
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

	signatureB64 := base64.RawURLEncoding.EncodeToString(signature)
	return signingInput + "." + signatureB64, nil
}

// ReadParameter reads a parameter from GCP Parameter Manager and
// returns its data as a map.
//
// Path semantics: callers pass either a bare parameter name (e.g.
// "app-config") which is resolved as "<param>/versions/latest", or an
// explicit "<param>/versions/<id>" string. The latter form lets the
// inheritance pipeline pin a specific version when needed.
//
// We hit :render (not :get) so that JSON/YAML parameters with
// embedded SecretManager references resolve server-side. The
// renderedPayload is base64 like Secret Manager's data field; if it
// parses as JSON we return it structured, otherwise we wrap the
// string in {"value": ...} the same way ReadSecret does.
func (c *GCPParameterManagerClient) ReadParameter(ctx context.Context, parameterPath string) (map[string]any, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}

	c.mu.RLock()
	token := c.token
	c.mu.RUnlock()

	resourceName := normalizeParameterPath(parameterPath)

	renderURL := fmt.Sprintf(
		"https://%s/v1/projects/%s/locations/%s/parameters/%s:render",
		c.apiHost, c.projectID, c.location, resourceName,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, renderURL, nil)
	if err != nil {
		return nil, fmt.Errorf("gcp-parameter: creating render request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gcp-parameter: executing render request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gcp-parameter: reading response: %w", err)
	}

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		c.mu.Lock()
		c.token = ""
		c.tokenExp = time.Time{}
		c.mu.Unlock()
		return nil, fmt.Errorf("gcp-parameter: returned HTTP %d (token may be expired): %s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("gcp-parameter: parameter %q not found in location %q", parameterPath, c.location)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gcp-parameter: returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var renderResp gcpRenderParameterResponse
	if err := json.Unmarshal(respBody, &renderResp); err != nil {
		return nil, fmt.Errorf("gcp-parameter: parsing render response: %w", err)
	}

	// renderedPayload is the secret-substituted form; payload.data
	// is the verbatim user-supplied bytes. Prefer rendered when
	// present (JSON/YAML parameters), fall back to the raw payload
	// for UNFORMATTED parameters where rendering is a no-op.
	encoded := renderResp.RenderedPayload
	if encoded == "" {
		encoded = renderResp.Payload.Data
	}

	if encoded == "" {
		// Empty parameter — return an empty map rather than nil so
		// callers can iterate without a nil-check.
		return map[string]any{}, nil
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("gcp-parameter: decoding payload: %w", err)
	}

	var data map[string]any
	if err := json.Unmarshal(decoded, &data); err == nil {
		return data, nil
	}

	return map[string]any{"value": string(decoded)}, nil
}

// ListParameters lists all parameter names under the configured
// location. Returns short names ("app-config"), not full resource
// paths.
func (c *GCPParameterManagerClient) ListParameters(ctx context.Context) ([]string, error) {
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
			"https://%s/v1/projects/%s/locations/%s/parameters?pageSize=100",
			c.apiHost, c.projectID, c.location,
		)
		if pageToken != "" {
			listURL += "&pageToken=" + url.QueryEscape(pageToken)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
		if err != nil {
			return nil, fmt.Errorf("gcp-parameter: creating list request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("gcp-parameter: executing list request: %w", err)
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if err != nil {
			return nil, fmt.Errorf("gcp-parameter: reading list response: %w", err)
		}

		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			c.mu.Lock()
			c.token = ""
			c.tokenExp = time.Time{}
			c.mu.Unlock()
			return nil, fmt.Errorf("gcp-parameter: list returned HTTP %d (token may be expired): %s", resp.StatusCode, string(respBody))
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("gcp-parameter: list returned HTTP %d: %s", resp.StatusCode, string(respBody))
		}

		var listResp gcpListParametersResponse
		if err := json.Unmarshal(respBody, &listResp); err != nil {
			return nil, fmt.Errorf("gcp-parameter: parsing list response: %w", err)
		}

		for _, p := range listResp.Parameters {
			allNames = append(allNames, extractSecretName(p.Name))
		}

		if listResp.NextPageToken == "" {
			break
		}
		pageToken = listResp.NextPageToken
	}

	return allNames, nil
}

// ParameterVersionInfo is the subset of a Parameter Manager version
// the SPA actually renders. We keep this type separate from
// external.Version so the client doesn't need to import the provider
// types; the provider layer maps between the two.
type ParameterVersionInfo struct {
	// ID is the short version name as it appears in URLs and the UI
	// (e.g. "v1", "prod", "rev3") — not the full resource path.
	ID string
	// CreatedAt is the RFC3339 creation timestamp as returned by GCP,
	// passed through unchanged. Empty when the server omits it.
	CreatedAt string
	// Disabled mirrors GCP's `disabled` flag. A disabled version still
	// exists but can't be rendered; the SPA paints it amber so the
	// operator knows it's there but unusable.
	Disabled bool
}

// ListParameterVersions returns every version of the given parameter,
// newest-first. `parameterName` is the bare parameter id
// ("my-config"), not a versioned path — version listing operates on
// the parameter, not on a specific version.
//
// We sort the result by createTime descending so the SPA's version
// strip matches the "newest leftmost" expectation users have from
// Vault's KV v2 history. GCP doesn't promise any default ordering
// in the list response (the API has an optional orderBy parameter
// but its supported values aren't part of the public REST surface
// yet), so doing the sort on the client side is the safest bet.
func (c *GCPParameterManagerClient) ListParameterVersions(ctx context.Context, parameterName string) ([]ParameterVersionInfo, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}

	c.mu.RLock()
	token := c.token
	c.mu.RUnlock()

	// Strip any "/versions/..." suffix the caller may have passed —
	// callers sometimes hand us paths copied from a render request.
	parameterName = strings.Trim(parameterName, "/")
	if idx := strings.Index(parameterName, "/versions/"); idx >= 0 {
		parameterName = parameterName[:idx]
	}
	if parameterName == "" {
		return nil, fmt.Errorf("gcp-parameter: parameter name is required for version listing")
	}

	var all []ParameterVersionInfo
	pageToken := ""

	for {
		listURL := fmt.Sprintf(
			"https://%s/v1/projects/%s/locations/%s/parameters/%s/versions?pageSize=100",
			c.apiHost, c.projectID, c.location, parameterName,
		)
		if pageToken != "" {
			listURL += "&pageToken=" + url.QueryEscape(pageToken)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
		if err != nil {
			return nil, fmt.Errorf("gcp-parameter: creating list-versions request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("gcp-parameter: executing list-versions request: %w", err)
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("gcp-parameter: reading list-versions response: %w", err)
		}

		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			c.mu.Lock()
			c.token = ""
			c.tokenExp = time.Time{}
			c.mu.Unlock()
			return nil, fmt.Errorf("gcp-parameter: list-versions returned HTTP %d (token may be expired): %s", resp.StatusCode, string(respBody))
		}

		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("gcp-parameter: parameter %q not found in location %q", parameterName, c.location)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("gcp-parameter: list-versions returned HTTP %d: %s", resp.StatusCode, string(respBody))
		}

		var listResp gcpListParameterVersionsResponse
		if err := json.Unmarshal(respBody, &listResp); err != nil {
			return nil, fmt.Errorf("gcp-parameter: parsing list-versions response: %w", err)
		}

		for _, v := range listResp.ParameterVersions {
			all = append(all, ParameterVersionInfo{
				ID:        extractSecretName(v.Name),
				CreatedAt: v.CreateTime,
				Disabled:  v.Disabled,
			})
		}

		if listResp.NextPageToken == "" {
			break
		}
		pageToken = listResp.NextPageToken
	}

	// Newest first. CreateTime is RFC3339, so lexicographic descending
	// is equivalent to chronological descending. We tolerate missing
	// timestamps (sort them last) instead of erroring — a half-populated
	// list is more useful than no list.
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].CreatedAt > all[j].CreatedAt
	})

	return all, nil
}

// normalizeParameterPath turns the user-facing path into the suffix
// that goes after "parameters/" in the render URL.
//
// Inputs accepted:
//   - "name"                      -> "name/versions/latest"
//   - "name/versions/v1"          -> "name/versions/v1"
//   - "name/v1" (shorthand)       -> "name/versions/v1"
//
// Anything more elaborate is passed through verbatim so power users
// can address future API surface (e.g. label selectors) without us
// needing to keep parsing in sync.
func normalizeParameterPath(p string) string {
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	if strings.Contains(p, "/versions/") {
		return p
	}
	parts := strings.SplitN(p, "/", 2)
	if len(parts) == 1 {
		return parts[0] + "/versions/latest"
	}
	// "name/v1" shorthand
	return parts[0] + "/versions/" + parts[1]
}

// ── Provider ──────────────────────────────────────────────────────────
// GCPParameterProvider implements Provider for GCP Parameter Manager.
// Same caching rationale as GCPProvider: the signed-JWT exchange is
// expensive, so the resulting client is held in Service and routed
// through Deps.

type GCPParameterProvider struct {
	Config *GCPParameter
	Deps   Deps
}

func (p *GCPParameterProvider) Kind() string { return "gcp-parameter" }

func (p *GCPParameterProvider) Capabilities() Capabilities {
	return Capabilities{CanRead: true, CanList: true, CanVersions: true}
}

func (p *GCPParameterProvider) Validate() error {
	if p.Config == nil {
		return fmt.Errorf("gcp-parameter: config is required")
	}
	if strings.TrimSpace(p.Config.ServiceAccountJSON) == "" {
		return fmt.Errorf("gcp-parameter: service_account_json is required")
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(p.Config.ServiceAccountJSON), &probe); err != nil {
		return fmt.Errorf("gcp-parameter: service_account_json is not valid JSON: %w", err)
	}
	if _, ok := probe["project_id"]; !ok {
		return fmt.Errorf("gcp-parameter: service_account_json missing project_id")
	}
	if err := validateProxy(p.Config.Proxy); err != nil {
		return fmt.Errorf("gcp-parameter: %w", err)
	}
	return nil
}

func (p *GCPParameterProvider) Fetch(ctx context.Context, path string) ([]byte, error) {
	client, err := p.Deps.GCPParameterClient(p.Config)
	if err != nil {
		return nil, err
	}
	data, err := client.ReadParameter(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("reading GCP parameter %q: %w", path, err)
	}
	return json.Marshal(data)
}

func (p *GCPParameterProvider) List(ctx context.Context, _ string) ([]string, error) {
	client, err := p.Deps.GCPParameterClient(p.Config)
	if err != nil {
		return nil, err
	}
	return client.ListParameters(ctx)
}

func (p *GCPParameterProvider) Test(ctx context.Context) TestResult {
	paths, err := p.List(ctx, "")
	if err != nil {
		return TestResult{OK: false, Message: err.Error()}
	}
	msg := fmt.Sprintf("Reachable. %d parameter(s) discovered in %q.", len(paths), p.Config.GetLocation())
	if len(paths) == 0 {
		msg = fmt.Sprintf("Reachable. No parameters discovered in %q (check IAM scope on the service account).", p.Config.GetLocation())
	}
	return TestResult{OK: true, Message: msg, Sample: capSample(paths, 10)}
}

func (p *GCPParameterProvider) Read(ctx context.Context, path string) (*Entry, error) {
	client, err := p.Deps.GCPParameterClient(p.Config)
	if err != nil {
		return nil, err
	}
	data, err := client.ReadParameter(ctx, path)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(data)
	return &Entry{Data: data, Raw: raw, ContentType: "application/json"}, nil
}

func (p *GCPParameterProvider) Write(ctx context.Context, path string, data map[string]any) error {
	return fmt.Errorf("gcp-parameter: %w", ErrNotSupported)
}

func (p *GCPParameterProvider) Delete(ctx context.Context, path string) error {
	return fmt.Errorf("gcp-parameter: %w", ErrNotSupported)
}

// ListVersions returns every version of the parameter at `path`,
// newest first. `path` is the bare parameter name; any "/versions/..."
// suffix is stripped before the API call so users can paste a render
// path here without surprising results.
//
// Mapping into the shared Version struct:
//   - ID:        GCP's short version name (user-provided, may be a
//     string like "prod" or a number like "1")
//   - CreatedAt: GCP createTime, passed through verbatim
//   - Deleted:   mirrors GCP's `disabled` flag — closest analogue to
//     Vault's soft-delete semantics. The SPA paints these amber.
//   - Destroyed: always false; Parameter Manager has no hard-destroy
//     concept (deleted versions vanish from the list entirely).
func (p *GCPParameterProvider) ListVersions(ctx context.Context, path string) ([]Version, error) {
	client, err := p.Deps.GCPParameterClient(p.Config)
	if err != nil {
		return nil, err
	}
	raws, err := client.ListParameterVersions(ctx, path)
	if err != nil {
		return nil, err
	}
	out := make([]Version, 0, len(raws))
	for _, v := range raws {
		out = append(out, Version{
			ID:        v.ID,
			CreatedAt: v.CreatedAt,
			Deleted:   v.Disabled,
		})
	}
	return out, nil
}

// ReadVersion reads a specific historical version. We reuse
// ReadParameter via the "name/versions/<id>" path shorthand that
// normalizeParameterPath already understands — the render endpoint
// supports both `latest` and explicit version IDs uniformly, so no
// extra HTTP code path is needed here.
func (p *GCPParameterProvider) ReadVersion(ctx context.Context, path, version string) (*Entry, error) {
	if strings.TrimSpace(version) == "" {
		return nil, fmt.Errorf("gcp-parameter: version is required")
	}
	client, err := p.Deps.GCPParameterClient(p.Config)
	if err != nil {
		return nil, err
	}
	// Strip a redundant /versions/... suffix the caller might have
	// already included so we don't end up with .../versions/<id>/versions/<id>.
	base := strings.Trim(path, "/")
	if idx := strings.Index(base, "/versions/"); idx >= 0 {
		base = base[:idx]
	}
	versioned := base + "/versions/" + version
	data, err := client.ReadParameter(ctx, versioned)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(data)
	return &Entry{Data: data, Raw: raw, ContentType: "application/json", Version: version}, nil
}
