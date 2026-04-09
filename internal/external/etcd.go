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
	"time"
)

// Etcd configures an etcd external resource.
type Etcd struct {
	// Address is the etcd HTTP API address (e.g., "http://etcd.example.com:2379").
	Address string `json:"address"`
	// Username for basic auth (optional).
	Username string `json:"username,omitempty"`
	// Password for basic auth (optional).
	Password string `json:"password,omitempty"`
}

// EtcdClient is a minimal HTTP client for etcd v3 via gRPC-gateway REST API.
type EtcdClient struct {
	address    string
	username   string
	password   string
	httpClient *http.Client
}

// NewEtcdClient creates a new etcd client.
func NewEtcdClient(address, username, password string) *EtcdClient {
	return &EtcdClient{
		address:  strings.TrimRight(address, "/"),
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
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
