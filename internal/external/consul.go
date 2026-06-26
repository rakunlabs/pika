package external

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Consul configures a Consul KV external resource.
type Consul struct {
	// Address is the Consul HTTP API address (e.g., "http://consul.example.com:8500").
	Address string `json:"address"`
	// Token is the Consul ACL token (optional).
	Token string `json:"token,omitempty"`

	// Proxy is an optional outbound HTTP/HTTPS/SOCKS5 proxy URL used for
	// requests to Consul. Used when ProxyMode is "custom".
	Proxy string `json:"proxy,omitempty"`
	// ProxyMode selects how the outbound proxy is chosen: "environment"
	// (default), "none" (force direct), or "custom" (use Proxy).
	ProxyMode string `json:"proxy_mode,omitempty"`
}

// ConsulClient is a minimal HTTP client for HashiCorp Consul KV.
type ConsulClient struct {
	address    string
	token      string
	httpClient *http.Client
}

// NewConsulClient creates a new Consul KV client.
// proxyMode/proxy control outbound proxy selection (see newHTTPClient).
func NewConsulClient(address, token, proxyMode, proxy string) *ConsulClient {
	return &ConsulClient{
		address:    strings.TrimRight(address, "/"),
		token:      token,
		httpClient: newHTTPClient(proxyMode, proxy, nil),
	}
}

// consulKVEntry represents a single entry from the Consul KV API.
type consulKVEntry struct {
	Key   string `json:"Key"`
	Value string `json:"Value"` // base64-encoded
}

// ReadSecret reads a value from Consul KV at the given key path.
func (c *ConsulClient) ReadSecret(ctx context.Context, key string) (map[string]any, error) {
	reqURL := fmt.Sprintf("%s/v1/kv/%s", c.address, strings.TrimLeft(key, "/"))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("consul: creating request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("X-Consul-Token", c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("consul: executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("consul: reading response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("consul: key %q not found", key)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("consul: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var entries []consulKVEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("consul: parsing response: %w", err)
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("consul: key %q not found", key)
	}

	// Decode the base64 value
	decoded, err := base64.StdEncoding.DecodeString(entries[0].Value)
	if err != nil {
		return nil, fmt.Errorf("consul: decoding value: %w", err)
	}

	// Try to parse as JSON
	var data map[string]any
	if err := json.Unmarshal(decoded, &data); err == nil {
		return data, nil
	}

	// Return as plain string value
	return map[string]any{"value": string(decoded)}, nil
}

// ListSecrets lists keys under the given prefix in Consul KV.
func (c *ConsulClient) ListSecrets(ctx context.Context, prefix string) ([]string, error) {
	prefix = strings.TrimLeft(prefix, "/")
	reqURL := fmt.Sprintf("%s/v1/kv/%s?keys&separator=/", c.address, prefix)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("consul: creating list request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("X-Consul-Token", c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("consul: executing list request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("consul: reading list response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return []string{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("consul: list HTTP %d: %s", resp.StatusCode, string(body))
	}

	var keys []string
	if err := json.Unmarshal(body, &keys); err != nil {
		return nil, fmt.Errorf("consul: parsing list response: %w", err)
	}

	// Make keys relative to the prefix
	result := make([]string, 0, len(keys))
	for _, k := range keys {
		rel := strings.TrimPrefix(k, prefix)
		if rel != "" {
			result = append(result, rel)
		}
	}

	return result, nil
}

// WriteValue puts a single value at the given Consul KV key. The
// callers' map is collapsed to a single string body using the "value"
// convention shared by Etcd as well: pass {"value": "..."} or pass
// JSON that we'll re-marshal verbatim.
func (c *ConsulClient) WriteValue(ctx context.Context, key string, body []byte) error {
	reqURL := fmt.Sprintf("%s/v1/kv/%s", c.address, strings.TrimLeft(key, "/"))

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("consul: creating write request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("X-Consul-Token", c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("consul: executing write request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("consul: write returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	// Consul KV PUT returns the literal "true" / "false". A "false"
	// here means CAS failed; we don't use CAS so treat both as success.
	return nil
}

// DeleteKey removes a single key from Consul KV. Idempotent at the
// HTTP layer (Consul returns 200 even when the key doesn't exist).
func (c *ConsulClient) DeleteKey(ctx context.Context, key string) error {
	reqURL := fmt.Sprintf("%s/v1/kv/%s", c.address, strings.TrimLeft(key, "/"))

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, reqURL, nil)
	if err != nil {
		return fmt.Errorf("consul: creating delete request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("X-Consul-Token", c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("consul: executing delete request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("consul: delete returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// ── Provider ──────────────────────────────────────────────────────────
// ConsulProvider exposes Consul KV through the unified Provider
// interface. Consul has no shared client state (every call creates a
// fresh ConsulClient — the underlying *http.Client is cheap and the
// token doesn't need renewal), so it doesn't need Deps.

type ConsulProvider struct {
	Config *Consul
}

func (p *ConsulProvider) Kind() string { return "consul" }

func (p *ConsulProvider) Capabilities() Capabilities {
	return Capabilities{CanRead: true, CanList: true, CanWrite: true, CanDelete: true}
}

func (p *ConsulProvider) Validate() error {
	if p.Config == nil {
		return fmt.Errorf("consul: config is required")
	}
	if strings.TrimSpace(p.Config.Address) == "" {
		return fmt.Errorf("consul: address is required")
	}
	if err := validateProxyConfig(p.Config.ProxyMode, p.Config.Proxy); err != nil {
		return fmt.Errorf("consul: %w", err)
	}
	return nil
}

func (p *ConsulProvider) Fetch(ctx context.Context, path string) ([]byte, error) {
	client := NewConsulClient(p.Config.Address, p.Config.Token, p.Config.ProxyMode, p.Config.Proxy)
	data, err := client.ReadSecret(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("reading consul key at %q: %w", path, err)
	}
	return json.Marshal(data)
}

func (p *ConsulProvider) List(ctx context.Context, prefix string) ([]string, error) {
	client := NewConsulClient(p.Config.Address, p.Config.Token, p.Config.ProxyMode, p.Config.Proxy)
	return client.ListSecrets(ctx, prefix)
}

func (p *ConsulProvider) Test(ctx context.Context) TestResult {
	paths, err := p.List(ctx, "")
	if err != nil {
		return TestResult{OK: false, Message: err.Error()}
	}
	msg := fmt.Sprintf("Reachable. %d key(s) discovered.", len(paths))
	if len(paths) == 0 {
		msg = "Reachable. No keys discovered."
	}
	return TestResult{OK: true, Message: msg, Sample: capSample(paths, 10)}
}

// Read returns the parsed value at key. The Consul client already
// attempts JSON parsing and falls back to {"value": "..."} for plain
// strings; we wrap that as an Entry so the browser sees a uniform
// shape across all backends.
func (p *ConsulProvider) Read(ctx context.Context, path string) (*Entry, error) {
	client := NewConsulClient(p.Config.Address, p.Config.Token, p.Config.ProxyMode, p.Config.Proxy)
	data, err := client.ReadSecret(ctx, path)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(data)
	return &Entry{Data: data, Raw: raw, ContentType: "application/json"}, nil
}

// Write stores a single value. If the supplied map has a "value" key
// we store its string form verbatim (lets users write plain strings).
// Otherwise we serialise the whole map as JSON — symmetric with what
// Read does on the way back.
func (p *ConsulProvider) Write(ctx context.Context, path string, data map[string]any) error {
	client := NewConsulClient(p.Config.Address, p.Config.Token, p.Config.ProxyMode, p.Config.Proxy)
	var payload []byte
	if v, ok := data["value"]; ok && len(data) == 1 {
		if s, ok := v.(string); ok {
			payload = []byte(s)
		} else {
			b, _ := json.Marshal(v)
			payload = b
		}
	} else {
		b, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("consul: marshal value: %w", err)
		}
		payload = b
	}
	return client.WriteValue(ctx, path, payload)
}

func (p *ConsulProvider) Delete(ctx context.Context, path string) error {
	client := NewConsulClient(p.Config.Address, p.Config.Token, p.Config.ProxyMode, p.Config.Proxy)
	return client.DeleteKey(ctx, path)
}

func (p *ConsulProvider) ListVersions(ctx context.Context, path string) ([]Version, error) {
	return nil, fmt.Errorf("consul: %w", ErrNotSupported)
}

func (p *ConsulProvider) ReadVersion(ctx context.Context, path, version string) (*Entry, error) {
	return nil, fmt.Errorf("consul: %w", ErrNotSupported)
}
