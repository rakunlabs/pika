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

// Etcd configures an etcd external resource.
type Etcd struct {
	// Address is the etcd HTTP API address (e.g., "http://etcd.example.com:2379").
	Address string `json:"address"`
	// Username for basic auth (optional).
	Username string `json:"username,omitempty"`
	// Password for basic auth (optional).
	Password string `json:"password,omitempty"`

	// Proxy is an optional outbound HTTP/HTTPS/SOCKS5 proxy URL used for
	// requests to etcd. Used when ProxyMode is "custom".
	Proxy string `json:"proxy,omitempty"`
	// ProxyMode selects how the outbound proxy is chosen: "environment"
	// (default), "none" (force direct), or "custom" (use Proxy).
	ProxyMode string `json:"proxy_mode,omitempty"`
}

// EtcdClient is a minimal HTTP client for etcd v3 via gRPC-gateway REST API.
type EtcdClient struct {
	address    string
	username   string
	password   string
	httpClient *http.Client
}

// NewEtcdClient creates a new etcd client.
// proxyMode/proxy control outbound proxy selection (see newHTTPClient).
func NewEtcdClient(address, username, password, proxyMode, proxy string) *EtcdClient {
	return &EtcdClient{
		address:    strings.TrimRight(address, "/"),
		username:   username,
		password:   password,
		httpClient: newHTTPClient(proxyMode, proxy, nil),
	}
}

// etcdRangeResponse represents the response from /v3/kv/range.
type etcdRangeResponse struct {
	Kvs []struct {
		Key   string `json:"key"`   // base64-encoded
		Value string `json:"value"` // base64-encoded
	} `json:"kvs"`
	Count string `json:"count"`
}

// ReadSecret reads a value from etcd at the given key.
func (e *EtcdClient) ReadSecret(ctx context.Context, key string) (map[string]any, error) {
	keyB64 := base64.StdEncoding.EncodeToString([]byte(key))

	body, err := json.Marshal(map[string]string{
		"key": keyB64,
	})
	if err != nil {
		return nil, fmt.Errorf("etcd: marshaling request: %w", err)
	}

	respBody, err := e.doPost(ctx, "/v3/kv/range", body)
	if err != nil {
		return nil, err
	}

	var rangeResp etcdRangeResponse
	if err := json.Unmarshal(respBody, &rangeResp); err != nil {
		return nil, fmt.Errorf("etcd: parsing response: %w", err)
	}

	if len(rangeResp.Kvs) == 0 {
		return nil, fmt.Errorf("etcd: key %q not found", key)
	}

	// Decode the value
	decoded, err := base64.StdEncoding.DecodeString(rangeResp.Kvs[0].Value)
	if err != nil {
		return nil, fmt.Errorf("etcd: decoding value: %w", err)
	}

	// Try to parse as JSON
	var data map[string]any
	if err := json.Unmarshal(decoded, &data); err == nil {
		return data, nil
	}

	return map[string]any{"value": string(decoded)}, nil
}

// ListSecrets lists keys under the given prefix in etcd.
func (e *EtcdClient) ListSecrets(ctx context.Context, prefix string) ([]string, error) {
	keyB64 := base64.StdEncoding.EncodeToString([]byte(prefix))

	// range_end is the prefix with the last byte incremented (prefix scan)
	rangeEnd := prefixEnd(prefix)
	rangeEndB64 := base64.StdEncoding.EncodeToString(rangeEnd)

	body, err := json.Marshal(map[string]any{
		"key":       keyB64,
		"range_end": rangeEndB64,
		"keys_only": true,
	})
	if err != nil {
		return nil, fmt.Errorf("etcd: marshaling list request: %w", err)
	}

	respBody, err := e.doPost(ctx, "/v3/kv/range", body)
	if err != nil {
		return nil, err
	}

	var rangeResp etcdRangeResponse
	if err := json.Unmarshal(respBody, &rangeResp); err != nil {
		return nil, fmt.Errorf("etcd: parsing list response: %w", err)
	}

	// Extract key names relative to prefix, deduplicate directories
	seenDirs := make(map[string]bool)
	var result []string

	for _, kv := range rangeResp.Kvs {
		keyBytes, err := base64.StdEncoding.DecodeString(kv.Key)
		if err != nil {
			continue
		}
		rel := strings.TrimPrefix(string(keyBytes), prefix)
		if rel == "" {
			continue
		}

		// If the key contains a /, treat the first segment as a directory
		if idx := strings.Index(rel, "/"); idx >= 0 {
			dir := rel[:idx+1] // include trailing /
			if !seenDirs[dir] {
				seenDirs[dir] = true
				result = append(result, dir)
			}
		} else {
			result = append(result, rel)
		}
	}

	return result, nil
}

// WriteValue puts a single value at the given etcd key. The HTTP API
// expects a base64-encoded key/value pair; we wrap the supplied bytes
// as the value verbatim so callers can pass either a primitive string
// or a marshalled JSON object.
func (e *EtcdClient) WriteValue(ctx context.Context, key string, value []byte) error {
	body, err := json.Marshal(map[string]string{
		"key":   base64.StdEncoding.EncodeToString([]byte(key)),
		"value": base64.StdEncoding.EncodeToString(value),
	})
	if err != nil {
		return fmt.Errorf("etcd: marshaling put request: %w", err)
	}
	if _, err := e.doPost(ctx, "/v3/kv/put", body); err != nil {
		return err
	}
	return nil
}

// DeleteKey removes a single etcd key. Idempotent at the etcd layer
// (the response carries a "deleted" count we don't surface — caller
// gets nil error either way).
func (e *EtcdClient) DeleteKey(ctx context.Context, key string) error {
	body, err := json.Marshal(map[string]string{
		"key": base64.StdEncoding.EncodeToString([]byte(key)),
	})
	if err != nil {
		return fmt.Errorf("etcd: marshaling delete request: %w", err)
	}
	if _, err := e.doPost(ctx, "/v3/kv/deleterange", body); err != nil {
		return err
	}
	return nil
}

// doPost performs a POST request to the etcd gRPC-gateway.
func (e *EtcdClient) doPost(ctx context.Context, path string, body []byte) ([]byte, error) {
	reqURL := e.address + path

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("etcd: creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if e.username != "" {
		req.SetBasicAuth(e.username, e.password)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("etcd: executing request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("etcd: reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("etcd: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// prefixEnd returns the end key for a prefix scan.
// It increments the last byte of the prefix. If the last byte is 0xFF,
// it moves to the previous byte and increments that, and so on.
func prefixEnd(prefix string) []byte {
	end := []byte(prefix)
	for i := len(end) - 1; i >= 0; i-- {
		if end[i] < 0xFF {
			end[i]++
			return end[:i+1]
		}
	}
	// All 0xFF — return "\x00" which means no upper bound
	return []byte{0}
}

// ── Provider ──────────────────────────────────────────────────────────

// EtcdProvider exposes etcd KV through the unified Provider interface.
type EtcdProvider struct {
	Config *Etcd
}

func (p *EtcdProvider) Kind() string { return "etcd" }

func (p *EtcdProvider) Capabilities() Capabilities {
	return Capabilities{CanRead: true, CanList: true, CanWrite: true, CanDelete: true}
}

func (p *EtcdProvider) Validate() error {
	if p.Config == nil {
		return fmt.Errorf("etcd: config is required")
	}
	if strings.TrimSpace(p.Config.Address) == "" {
		return fmt.Errorf("etcd: address is required")
	}
	if err := validateProxyConfig(p.Config.ProxyMode, p.Config.Proxy); err != nil {
		return fmt.Errorf("etcd: %w", err)
	}
	return nil
}

func (p *EtcdProvider) Fetch(ctx context.Context, path string) ([]byte, error) {
	client := NewEtcdClient(p.Config.Address, p.Config.Username, p.Config.Password, p.Config.ProxyMode, p.Config.Proxy)
	data, err := client.ReadSecret(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("reading etcd key at %q: %w", path, err)
	}
	return json.Marshal(data)
}

func (p *EtcdProvider) List(ctx context.Context, prefix string) ([]string, error) {
	client := NewEtcdClient(p.Config.Address, p.Config.Username, p.Config.Password, p.Config.ProxyMode, p.Config.Proxy)
	return client.ListSecrets(ctx, prefix)
}

func (p *EtcdProvider) Test(ctx context.Context) TestResult {
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

// Read returns the parsed value at key. Mirrors ConsulProvider.Read.
func (p *EtcdProvider) Read(ctx context.Context, path string) (*Entry, error) {
	client := NewEtcdClient(p.Config.Address, p.Config.Username, p.Config.Password, p.Config.ProxyMode, p.Config.Proxy)
	data, err := client.ReadSecret(ctx, path)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(data)
	return &Entry{Data: data, Raw: raw, ContentType: "application/json"}, nil
}

// Write — same value convention as ConsulProvider.Write: a single
// "value" key is stored verbatim, anything richer is JSON-encoded.
func (p *EtcdProvider) Write(ctx context.Context, path string, data map[string]any) error {
	client := NewEtcdClient(p.Config.Address, p.Config.Username, p.Config.Password, p.Config.ProxyMode, p.Config.Proxy)
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
			return fmt.Errorf("etcd: marshal value: %w", err)
		}
		payload = b
	}
	return client.WriteValue(ctx, path, payload)
}

func (p *EtcdProvider) Delete(ctx context.Context, path string) error {
	client := NewEtcdClient(p.Config.Address, p.Config.Username, p.Config.Password, p.Config.ProxyMode, p.Config.Proxy)
	return client.DeleteKey(ctx, path)
}

func (p *EtcdProvider) ListVersions(ctx context.Context, path string) ([]Version, error) {
	return nil, fmt.Errorf("etcd: %w", ErrNotSupported)
}

func (p *EtcdProvider) ReadVersion(ctx context.Context, path, version string) (*Entry, error) {
	return nil, fmt.Errorf("etcd: %w", ErrNotSupported)
}
