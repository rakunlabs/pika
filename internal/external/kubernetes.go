package external

import (
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
// If kubeconfig is empty, in-cluster config is used.
func NewKubeClient(kubeconfig string) (*KubeClient, error) {
	if kubeconfig != "" {
		return newKubeClientFromKubeconfig(kubeconfig)
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

// newKubeClientFromKubeconfig creates a client by parsing a kubeconfig file.
func newKubeClientFromKubeconfig(path string) (*KubeClient, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: reading kubeconfig %q: %w", path, err)
	}

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
func (kc *KubeClient) doRequest(ctx context.Context, method, path string) ([]byte, error) {
	url := kc.host + path

	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	token := kc.getToken()
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := kc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
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
	body, err := kc.doRequest(ctx, http.MethodGet, path)
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
	body, err := kc.doRequest(ctx, http.MethodGet, path)
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
	body, err := kc.doRequest(ctx, http.MethodGet, "/api/v1/namespaces")
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
	body, err := kc.doRequest(ctx, http.MethodGet, path)
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
	body, err := kc.doRequest(ctx, http.MethodGet, path)
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
