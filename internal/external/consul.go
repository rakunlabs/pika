package external

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Consul configures a Consul KV external resource.
type Consul struct {
	// Address is the Consul HTTP API address (e.g., "http://consul.example.com:8500").
	Address string `json:"address"`
	// Token is the Consul ACL token (optional).
	Token string `json:"token,omitempty"`
}

// ConsulClient is a minimal HTTP client for HashiCorp Consul KV.
type ConsulClient struct {
	address    string
	token      string
	httpClient *http.Client
}

// NewConsulClient creates a new Consul KV client.
func NewConsulClient(address, token string) *ConsulClient {
	return &ConsulClient{
		address: strings.TrimRight(address, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
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
