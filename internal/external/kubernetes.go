package external

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-yaml"
)

const (
	inClusterTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	inClusterCAPath    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	inClusterNSPath    = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	kubeServiceHostEnv = "KUBERNETES_SERVICE_HOST"
	kubeServicePortEnv = "KUBERNETES_SERVICE_PORT"
)

// KubeClient is a minimal HTTP client for the Kubernetes API.
// It supports in-cluster and kubeconfig authentication.
type KubeClient struct {
	host       string
	httpClient *http.Client

	mu    sync.RWMutex
	token string
	// tokenPath is set for in-cluster auth so the token can be re-read if it rotates.
	tokenPath string
}

// NewKubeClient creates a KubeClient from a Kubernetes config.
//
// Selection rules (in priority order):
//  1. cfg.KubeconfigContent — parse the kubeconfig YAML directly.
//  2. cfg.Kubeconfig        — read kubeconfig YAML from this filesystem path.
//  3. otherwise             — use in-cluster config (service account token).
func NewKubeClient(cfg *Kubernetes) (*KubeClient, error) {
	if cfg == nil {
		return newKubeClientInCluster()
	}
	if cfg.KubeconfigContent != "" {
		return newKubeClientFromKubeconfigBytes([]byte(cfg.KubeconfigContent))
	}
	if cfg.Kubeconfig != "" {
		data, err := os.ReadFile(cfg.Kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("kubernetes: reading kubeconfig %q: %w", cfg.Kubeconfig, err)
		}
		return newKubeClientFromKubeconfigBytes(data)
	}
	return newKubeClientInCluster()
}

// newKubeClientInCluster creates a client using the service account token and CA cert
// mounted inside a Kubernetes pod.
func newKubeClientInCluster() (*KubeClient, error) {
	host := os.Getenv(kubeServiceHostEnv)
	port := os.Getenv(kubeServicePortEnv)
	if host == "" || port == "" {
		return nil, fmt.Errorf("kubernetes: not running in-cluster (missing %s or %s env vars)", kubeServiceHostEnv, kubeServicePortEnv)
	}

	apiHost := fmt.Sprintf("https://%s:%s", host, port)

	token, err := os.ReadFile(inClusterTokenPath)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: reading service account token: %w", err)
	}

	tlsConfig, err := loadCACert(inClusterCAPath)
	if err != nil {
		return nil, err
	}

	return &KubeClient{
		host: apiHost,
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsConfig},
		},
		token:     string(token),
		tokenPath: inClusterTokenPath,
	}, nil
}

// kubeConfig is a minimal representation of a kubeconfig file.
type kubeConfig struct {
	Clusters       []kubeConfigCluster `yaml:"clusters"`
	Users          []kubeConfigUser    `yaml:"users"`
	Contexts       []kubeConfigContext `yaml:"contexts"`
	CurrentContext string              `yaml:"current-context"`
}

type kubeConfigCluster struct {
	Name    string `yaml:"name"`
	Cluster struct {
		Server                   string `yaml:"server"`
		CertificateAuthorityData string `yaml:"certificate-authority-data"`
		CertificateAuthority     string `yaml:"certificate-authority"`
		InsecureSkipTLSVerify    bool   `yaml:"insecure-skip-tls-verify"`
	} `yaml:"cluster"`
}

type kubeConfigUser struct {
	Name string `yaml:"name"`
	User struct {
		Token                 string `yaml:"token"`
		ClientCertificateData string `yaml:"client-certificate-data"`
		ClientKeyData         string `yaml:"client-key-data"`
	} `yaml:"user"`
}

type kubeConfigContext struct {
	Name    string `yaml:"name"`
	Context struct {
		Cluster string `yaml:"cluster"`
		User    string `yaml:"user"`
	} `yaml:"context"`
}

// newKubeClientFromKubeconfigBytes creates a client by parsing a kubeconfig YAML
// document supplied directly as bytes (read from a file, pasted in the UI, etc.).
func newKubeClientFromKubeconfigBytes(data []byte) (*KubeClient, error) {
	var cfg kubeConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("kubernetes: parsing kubeconfig: %w", err)
	}

	if cfg.CurrentContext == "" && len(cfg.Contexts) > 0 {
		cfg.CurrentContext = cfg.Contexts[0].Name
	}

	// Find the current context
	var clusterName, userName string
	for _, ctx := range cfg.Contexts {
		if ctx.Name == cfg.CurrentContext {
			clusterName = ctx.Context.Cluster
			userName = ctx.Context.User
			break
		}
	}

	if clusterName == "" {
		return nil, fmt.Errorf("kubernetes: context %q not found in kubeconfig", cfg.CurrentContext)
	}

	// Find cluster
	var server string
	var caData string
	var caFile string
	var insecure bool
	for _, c := range cfg.Clusters {
		if c.Name == clusterName {
			server = c.Cluster.Server
			caData = c.Cluster.CertificateAuthorityData
			caFile = c.Cluster.CertificateAuthority
			insecure = c.Cluster.InsecureSkipTLSVerify
			break
		}
	}

	if server == "" {
		return nil, fmt.Errorf("kubernetes: cluster %q not found in kubeconfig", clusterName)
	}

	// Find user
	var token string
	var clientCertData, clientKeyData string
	for _, u := range cfg.Users {
		if u.Name == userName {
			token = u.User.Token
			clientCertData = u.User.ClientCertificateData
			clientKeyData = u.User.ClientKeyData
			break
		}
	}

	// Build TLS config
	tlsConfig := &tls.Config{
		InsecureSkipVerify: insecure,
	}

	// Load CA cert
	if caData != "" {
		caBytes, err := base64.StdEncoding.DecodeString(caData)
		if err != nil {
			return nil, fmt.Errorf("kubernetes: decoding CA data: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caBytes) {
			return nil, fmt.Errorf("kubernetes: invalid CA certificate data")
		}
		tlsConfig.RootCAs = pool
	} else if caFile != "" {
		pool, err := loadCACertPool(caFile)
		if err != nil {
			return nil, err
		}
		tlsConfig.RootCAs = pool
	}

	// Load client certificate (mTLS)
	if clientCertData != "" && clientKeyData != "" {
		certPEM, err := base64.StdEncoding.DecodeString(clientCertData)
		if err != nil {
			return nil, fmt.Errorf("kubernetes: decoding client cert: %w", err)
		}
		keyPEM, err := base64.StdEncoding.DecodeString(clientKeyData)
		if err != nil {
			return nil, fmt.Errorf("kubernetes: decoding client key: %w", err)
		}
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("kubernetes: loading client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return &KubeClient{
		host:  strings.TrimRight(server, "/"),
		token: token,
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsConfig},
		},
	}, nil
}

func loadCACert(path string) (*tls.Config, error) {
	pool, err := loadCACertPool(path)
	if err != nil {
		return nil, err
	}
	return &tls.Config{RootCAs: pool}, nil
}

func loadCACertPool(path string) (*x509.CertPool, error) {
	caCert, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: reading CA cert %q: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("kubernetes: invalid CA certificate at %q", path)
	}
	return pool, nil
}

// getToken returns the current bearer token, re-reading from disk for in-cluster tokens
// (Kubernetes rotates projected service account tokens).
func (kc *KubeClient) getToken() string {
	kc.mu.RLock()
	tokenPath := kc.tokenPath
	token := kc.token
	kc.mu.RUnlock()

	if tokenPath != "" {
		// Re-read token from disk in case it was rotated
		if data, err := os.ReadFile(tokenPath); err == nil && len(data) > 0 {
			newToken := string(data)
			if newToken != token {
				kc.mu.Lock()
				kc.token = newToken
				kc.mu.Unlock()
				return newToken
			}
		}
	}

	return token
}

// kubeAPIResponse is a generic Kubernetes API response for error handling.
type kubeAPIResponse struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// doRequest performs an authenticated HTTP request to the Kubernetes API.
// Pass a non-nil reqBody and contentType for write methods (PUT/POST/PATCH);
// reads / deletes can leave both empty.
func (kc *KubeClient) doRequest(ctx context.Context, method, path string, reqBody []byte, contentType string) ([]byte, error) {
	url := kc.host + path

	var bodyReader io.Reader
	if reqBody != nil {
		bodyReader = bytes.NewReader(reqBody)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	token := kc.getToken()
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := kc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	// 200 OK (GET, PUT), 201 Created (POST), 204 No Content (DELETE).
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		var apiResp kubeAPIResponse
		if json.Unmarshal(body, &apiResp) == nil && apiResp.Message != "" {
			return nil, fmt.Errorf("kubernetes API error (HTTP %d): %s", resp.StatusCode, apiResp.Message)
		}
		return nil, fmt.Errorf("kubernetes API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// kubeSecret represents a Kubernetes Secret resource.
type kubeSecret struct {
	Data map[string]string `json:"data"` // base64-encoded values
}

// kubeConfigMap represents a Kubernetes ConfigMap resource.
type kubeConfigMap struct {
	Data map[string]string `json:"data"`
}

// kubeResourceList represents a Kubernetes resource list (for listing secrets/configmaps).
type kubeResourceList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
	} `json:"items"`
}

// kubeNamespaceList represents a Kubernetes namespace list.
type kubeNamespaceList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
	} `json:"items"`
}

// ReadSecret reads a Kubernetes Secret and returns its data as a map with base64-decoded values.
func (kc *KubeClient) ReadSecret(ctx context.Context, namespace, name string) (map[string]any, error) {
	path := fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", namespace, name)
	body, err := kc.doRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, fmt.Errorf("reading secret %s/%s: %w", namespace, name, err)
	}

	var secret kubeSecret
	if err := json.Unmarshal(body, &secret); err != nil {
		return nil, fmt.Errorf("parsing secret response: %w", err)
	}

	// Auto-decode base64 values
	result := make(map[string]any, len(secret.Data))
	for k, v := range secret.Data {
		decoded, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			result[k] = v // keep raw if decode fails
		} else {
			result[k] = string(decoded)
		}
	}

	return result, nil
}

// ReadConfigMap reads a Kubernetes ConfigMap and returns its data.
func (kc *KubeClient) ReadConfigMap(ctx context.Context, namespace, name string) (map[string]any, error) {
	path := fmt.Sprintf("/api/v1/namespaces/%s/configmaps/%s", namespace, name)
	body, err := kc.doRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, fmt.Errorf("reading configmap %s/%s: %w", namespace, name, err)
	}

	var cm kubeConfigMap
	if err := json.Unmarshal(body, &cm); err != nil {
		return nil, fmt.Errorf("parsing configmap response: %w", err)
	}

	result := make(map[string]any, len(cm.Data))
	for k, v := range cm.Data {
		result[k] = v
	}

	return result, nil
}

// ListResources lists available resources for the path browser in the UI.
// It supports hierarchical browsing:
//   - prefix=""           → list namespaces (returned as "namespace/")
//   - prefix="default/"   → returns ["secret/", "configmap/"] as resource types
//   - prefix="default/secret/" → list secrets in namespace (returned as "name")
//   - prefix="default/configmap/" → list configmaps in namespace (returned as "name")
func (kc *KubeClient) ListResources(ctx context.Context, prefix string) ([]string, error) {
	prefix = strings.TrimLeft(prefix, "/")
	parts := strings.SplitN(prefix, "/", 3)

	switch {
	case prefix == "" || (len(parts) == 1 && !strings.HasSuffix(prefix, "/")):
		// List namespaces
		return kc.listNamespaces(ctx)

	case len(parts) == 2 && parts[1] == "":
		// e.g., "default/" → show resource types
		return []string{"secret/", "configmap/"}, nil

	case len(parts) >= 2:
		// e.g., "default/secret/" or "default/configmap/"
		namespace := parts[0]
		resourceType := strings.TrimRight(parts[1], "/")

		switch resourceType {
		case "secret":
			return kc.listSecrets(ctx, namespace)
		case "configmap":
			return kc.listConfigMaps(ctx, namespace)
		default:
			return []string{"secret/", "configmap/"}, nil
		}

	default:
		return []string{}, nil
	}
}

func (kc *KubeClient) listNamespaces(ctx context.Context) ([]string, error) {
	body, err := kc.doRequest(ctx, http.MethodGet, "/api/v1/namespaces", nil, "")
	if err != nil {
		return nil, fmt.Errorf("listing namespaces: %w", err)
	}

	var list kubeNamespaceList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("parsing namespace list: %w", err)
	}

	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.Metadata.Name+"/")
	}
	return names, nil
}

func (kc *KubeClient) listSecrets(ctx context.Context, namespace string) ([]string, error) {
	path := fmt.Sprintf("/api/v1/namespaces/%s/secrets", namespace)
	body, err := kc.doRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, fmt.Errorf("listing secrets in %s: %w", namespace, err)
	}

	var list kubeResourceList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("parsing secret list: %w", err)
	}

	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.Metadata.Name)
	}
	return names, nil
}

func (kc *KubeClient) listConfigMaps(ctx context.Context, namespace string) ([]string, error) {
	path := fmt.Sprintf("/api/v1/namespaces/%s/configmaps", namespace)
	body, err := kc.doRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, fmt.Errorf("listing configmaps in %s: %w", namespace, err)
	}

	var list kubeResourceList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("parsing configmap list: %w", err)
	}

	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.Metadata.Name)
	}
	return names, nil
}

// WriteSecret creates or updates a Kubernetes Secret. data values are
// taken as cleartext and base64-encoded for the wire (Kubernetes
// Secret.data is always base64). We use PUT with apiVersion/kind/
// metadata embedded so the request works whether or not the secret
// already exists — kubectl-style server-side create-or-replace.
//
// Note: this is a *replace* (the Secret object's data map is set to
// exactly the supplied values; keys not present in `data` disappear).
// Browsers that want merge semantics should read-modify-write.
func (kc *KubeClient) WriteSecret(ctx context.Context, namespace, name string, data map[string]string) error {
	encoded := make(map[string]string, len(data))
	for k, v := range data {
		encoded[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]string{"name": name, "namespace": namespace},
		"type":       "Opaque",
		"data":       encoded,
	})
	if err != nil {
		return fmt.Errorf("marshaling secret: %w", err)
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", namespace, name)
	// Try PUT first (update). On 404 fall back to POST (create).
	_, err = kc.doRequest(ctx, http.MethodPut, path, body, "application/json")
	if err != nil && strings.Contains(err.Error(), "HTTP 404") {
		createPath := fmt.Sprintf("/api/v1/namespaces/%s/secrets", namespace)
		_, err = kc.doRequest(ctx, http.MethodPost, createPath, body, "application/json")
	}
	return err
}

// WriteConfigMap creates or updates a Kubernetes ConfigMap with
// cleartext values (ConfigMap.data is plain strings, no encoding).
// Replace semantics, same as WriteSecret.
func (kc *KubeClient) WriteConfigMap(ctx context.Context, namespace, name string, data map[string]string) error {
	body, err := json.Marshal(map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]string{"name": name, "namespace": namespace},
		"data":       data,
	})
	if err != nil {
		return fmt.Errorf("marshaling configmap: %w", err)
	}
	path := fmt.Sprintf("/api/v1/namespaces/%s/configmaps/%s", namespace, name)
	_, err = kc.doRequest(ctx, http.MethodPut, path, body, "application/json")
	if err != nil && strings.Contains(err.Error(), "HTTP 404") {
		createPath := fmt.Sprintf("/api/v1/namespaces/%s/configmaps", namespace)
		_, err = kc.doRequest(ctx, http.MethodPost, createPath, body, "application/json")
	}
	return err
}

// DeleteSecret removes a Kubernetes Secret. 404 is squashed (idempotent).
func (kc *KubeClient) DeleteSecret(ctx context.Context, namespace, name string) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", namespace, name)
	_, err := kc.doRequest(ctx, http.MethodDelete, path, nil, "")
	if err != nil && strings.Contains(err.Error(), "HTTP 404") {
		return nil
	}
	return err
}

// DeleteConfigMap removes a Kubernetes ConfigMap. 404 is squashed.
func (kc *KubeClient) DeleteConfigMap(ctx context.Context, namespace, name string) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s/configmaps/%s", namespace, name)
	_, err := kc.doRequest(ctx, http.MethodDelete, path, nil, "")
	if err != nil && strings.Contains(err.Error(), "HTTP 404") {
		return nil
	}
	return err
}

// ── Provider ──────────────────────────────────────────────────────────
// KubernetesProvider implements Provider for Kubernetes Secrets and
// ConfigMaps. The underlying KubeClient is cached per (kubeconfig
// contents | path | in-cluster) by Service so repeated lookups don't
// re-resolve TLS material.

type KubernetesProvider struct {
	Config *Kubernetes
	Deps   Deps
}

func (p *KubernetesProvider) Kind() string { return "kubernetes" }

func (p *KubernetesProvider) Capabilities() Capabilities {
	// Kubernetes supports read/list/write/delete on Secret and
	// ConfigMap (which are the only two resource types we expose).
	// No version concept — when secret/configmap is updated it gets a
	// new resourceVersion but that's a CAS token, not a history.
	return Capabilities{CanRead: true, CanList: true, CanWrite: true, CanDelete: true}
}

// Validate: no fields are strictly required. An empty struct means
// "in-cluster service account" which is a valid configuration on its
// own. We only check that, when content/path is provided, it's non-
// blank — empty strings smell like UI bugs (user toggled a mode but
// never typed anything).
func (p *KubernetesProvider) Validate() error {
	if p.Config == nil {
		return fmt.Errorf("kubernetes: config is required")
	}
	if p.Config.KubeconfigContent != "" && strings.TrimSpace(p.Config.KubeconfigContent) == "" {
		return fmt.Errorf("kubernetes: kubeconfig_content cannot be whitespace-only")
	}
	if p.Config.Kubeconfig != "" && strings.TrimSpace(p.Config.Kubeconfig) == "" {
		return fmt.Errorf("kubernetes: kubeconfig path cannot be whitespace-only")
	}
	return nil
}

func (p *KubernetesProvider) Fetch(ctx context.Context, path string) ([]byte, error) {
	client, err := p.Deps.KubeClient(p.Config)
	if err != nil {
		return nil, fmt.Errorf("kubernetes client: %w", err)
	}

	parts := strings.SplitN(path, "/", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid kubernetes path %q: expected namespace/type/name (e.g., default/secret/my-secret)", path)
	}
	namespace, resourceType, name := parts[0], parts[1], parts[2]

	var data map[string]any
	switch resourceType {
	case "secret":
		data, err = client.ReadSecret(ctx, namespace, name)
	case "configmap":
		data, err = client.ReadConfigMap(ctx, namespace, name)
	default:
		return nil, fmt.Errorf("unsupported kubernetes resource type %q: expected secret or configmap", resourceType)
	}
	if err != nil {
		return nil, err
	}

	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("serializing kubernetes data: %w", err)
	}
	return jsonBytes, nil
}

func (p *KubernetesProvider) List(ctx context.Context, prefix string) ([]string, error) {
	client, err := p.Deps.KubeClient(p.Config)
	if err != nil {
		return nil, fmt.Errorf("kubernetes client: %w", err)
	}
	return client.ListResources(ctx, prefix)
}

func (p *KubernetesProvider) Test(ctx context.Context) TestResult {
	paths, err := p.List(ctx, "")
	if err != nil {
		return TestResult{OK: false, Message: err.Error()}
	}
	msg := fmt.Sprintf("Reachable. %d resource(s) discovered.", len(paths))
	if len(paths) == 0 {
		msg = "Reachable. No resources discovered (check RBAC scope on the service account)."
	}
	return TestResult{OK: true, Message: msg, Sample: capSample(paths, 10)}
}

// parseKubePath splits the SPA-facing "namespace/type/name" path.
// We tolerate extra trailing slashes but reject anything that isn't
// the three-segment shape — that protects the client methods from
// being called with bogus inputs.
func (p *KubernetesProvider) parseKubePath(path string) (namespace, resourceType, name string, err error) {
	parts := strings.SplitN(strings.Trim(path, "/"), "/", 3)
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("invalid kubernetes path %q: expected namespace/type/name (e.g., default/secret/my-secret)", path)
	}
	return parts[0], parts[1], parts[2], nil
}

// Read mirrors Fetch but returns a structured Entry (with the same
// underlying data) so the browser can render keys and values without
// re-parsing JSON.
func (p *KubernetesProvider) Read(ctx context.Context, path string) (*Entry, error) {
	client, err := p.Deps.KubeClient(p.Config)
	if err != nil {
		return nil, fmt.Errorf("kubernetes client: %w", err)
	}
	namespace, kind, name, err := p.parseKubePath(path)
	if err != nil {
		return nil, err
	}

	var data map[string]any
	switch kind {
	case "secret":
		data, err = client.ReadSecret(ctx, namespace, name)
	case "configmap":
		data, err = client.ReadConfigMap(ctx, namespace, name)
	default:
		return nil, fmt.Errorf("unsupported kubernetes resource type %q: expected secret or configmap", kind)
	}
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(data)
	return &Entry{Data: data, Raw: raw, ContentType: "application/json"}, nil
}

// Write replaces the named Secret/ConfigMap. The map's values are
// coerced to strings before being sent because both resource types
// store strings — numbers / bools / nested objects don't have a
// representation in Kubernetes Secret.data or ConfigMap.data and
// would silently degrade if we passed them through as JSON.
func (p *KubernetesProvider) Write(ctx context.Context, path string, data map[string]any) error {
	client, err := p.Deps.KubeClient(p.Config)
	if err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}
	namespace, kind, name, err := p.parseKubePath(path)
	if err != nil {
		return err
	}
	flat := make(map[string]string, len(data))
	for k, v := range data {
		switch t := v.(type) {
		case string:
			flat[k] = t
		default:
			b, _ := json.Marshal(t)
			flat[k] = string(b)
		}
	}
	switch kind {
	case "secret":
		return client.WriteSecret(ctx, namespace, name, flat)
	case "configmap":
		return client.WriteConfigMap(ctx, namespace, name, flat)
	default:
		return fmt.Errorf("unsupported kubernetes resource type %q", kind)
	}
}

// Delete removes the named Secret/ConfigMap. Idempotent.
func (p *KubernetesProvider) Delete(ctx context.Context, path string) error {
	client, err := p.Deps.KubeClient(p.Config)
	if err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}
	namespace, kind, name, err := p.parseKubePath(path)
	if err != nil {
		return err
	}
	switch kind {
	case "secret":
		return client.DeleteSecret(ctx, namespace, name)
	case "configmap":
		return client.DeleteConfigMap(ctx, namespace, name)
	default:
		return fmt.Errorf("unsupported kubernetes resource type %q", kind)
	}
}

// ListVersions is not supported — Kubernetes Secrets/ConfigMaps don't
// expose a version history. Returning ErrNotSupported lets the SPA
// hide the version selector entirely.
func (p *KubernetesProvider) ListVersions(ctx context.Context, path string) ([]Version, error) {
	return nil, fmt.Errorf("kubernetes: %w", ErrNotSupported)
}

func (p *KubernetesProvider) ReadVersion(ctx context.Context, path, version string) (*Entry, error) {
	return nil, fmt.Errorf("kubernetes: %w", ErrNotSupported)
}
