package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rakunlabs/ok"
	"github.com/rakunlabs/pika/internal/external"
)

// GetData retrieves a fully resolved configuration:
// 1. Loads the base file
// 2. If inheritance is configured, fetches parent and applies JSONPath filtering
// 3. Deep-merges parent (filtered) <- current file data
// 4. If variationKey is provided, deep-merges the variation overlay on top
func (s *Service) GetData(ctx context.Context, filePath string, versionStr string, variationKey string) (*DataResult, error) {
	// If a variant is requested, load it as an independent file
	if variationKey != "" {
		return s.getDataForFile(ctx, variantKey(filePath, variationKey), versionStr)
	}

	return s.getDataForFile(ctx, filePath, versionStr)
}

// getDataForFile resolves a single file (base or variant) with its inheritance.
func (s *Service) getDataForFile(ctx context.Context, filePath string, versionStr string) (*DataResult, error) {
	file, err := s.FileByVersion(ctx, filePath, versionStr)
	if err != nil {
		return nil, err
	}

	resolved := file.Data
	format := file.Meta.Format
	needsMerge := len(file.Meta.Inherits) > 0

	// Convert to JSON for merging if needed
	if needsMerge && format != "json" && format != "" && format != "raw" {
		jsonData, err := ConvertFormat(resolved, format, "json")
		if err == nil {
			resolved = jsonData
		}
	}

	// Resolve all inheritance entries
	if len(file.Meta.Inherits) > 0 {
		var err error
		resolved, err = s.resolveInherits(ctx, resolved, file.Meta.Inherits)
		if err != nil {
			return nil, err
		}
	}

	// Convert back to original format after merging
	if needsMerge && format != "json" && format != "" && format != "raw" {
		converted, err := ConvertFormat(resolved, "json", format)
		if err == nil {
			resolved = converted
		}
	}

	return &DataResult{
		Data:   resolved,
		Format: format,
	}, nil
}

// RenderFile resolves a file's configuration for preview purposes.
// Unlike GetData, this accepts raw content and meta from the UI editor
// (which may not be saved yet) and performs resolution.
func (s *Service) RenderFile(ctx context.Context, filePath string, content string, meta *FileMeta) (*RenderResult, error) {
	currentData := []byte(content)
	format := "json"
	if meta != nil && meta.Format != "" {
		format = meta.Format
	}

	// Convert content to JSON for merging (if it's YAML/TOML)
	if format != "json" && format != "raw" {
		jsonData, err := ConvertFormat(currentData, format, "json")
		if err != nil {
			return &RenderResult{
				Data: base64.StdEncoding.EncodeToString(currentData),
			}, nil
		}
		currentData = jsonData
	}

	// Step 1: Resolve all inheritance entries
	if meta != nil && len(meta.Inherits) > 0 {
		var err error
		currentData, err = s.resolveInherits(ctx, currentData, meta.Inherits)
		if err != nil {
			return nil, err
		}
	}

	// Convert back to original format
	if format != "json" && format != "raw" {
		converted, err := ConvertFormat(currentData, "json", format)
		if err == nil {
			currentData = converted
		}
	}

	return &RenderResult{
		Data: base64.StdEncoding.EncodeToString(currentData),
	}, nil
}

// resolveInherits processes multiple inheritance entries and merges them into currentData.
// Each entry is processed in order: fetch source -> filter paths -> inject at target -> merge.
// The current config data always takes precedence over inherited values.
func (s *Service) resolveInherits(ctx context.Context, currentData []byte, entries []InheritEntry) ([]byte, error) {
	for _, entry := range entries {
		if entry.Source == "" && entry.Resource == "" {
			continue
		}

		var sourceData []byte
		var sourceName string
		var err error

		if entry.Resource != "" {
			// External resource: lookup by resource name and fetch with path
			sourceName = entry.Resource + ":" + entry.Path
			sourceData, err = s.fetchExternalConfig(ctx, entry.Resource, entry.Path)
		} else {
			// Internal file or legacy external (backward compat)
			sourceName = entry.Source
			sourceData, err = s.fetchSource(ctx, entry.Source)
		}

		if err != nil {
			return nil, fmt.Errorf("resolving inheritance from %q: %w", sourceName, err)
		}

		// Ensure source data is JSON for merging
		sourceJSON := sourceData
		if entry.Resource == "" {
			// For internal sources, try to detect format and convert
			file, fileErr := s.File(ctx, entry.Source, 0)
			if fileErr == nil && file.Meta.Format != "" && file.Meta.Format != "json" && file.Meta.Format != "raw" {
				converted, convErr := ConvertFormat(sourceData, file.Meta.Format, "json")
				if convErr == nil {
					sourceJSON = converted
				}
			}
		}

		// Filter by paths if specified
		if len(entry.Paths) > 0 {
			filtered, err := filterByPaths(sourceJSON, entry.Paths)
			if err != nil {
				return nil, fmt.Errorf("filtering paths from %q: %w", sourceName, err)
			}
			sourceJSON = filtered
		}

		// Inject at target path if specified
		if entry.Inject != "" {
			injected, err := injectAtPath(sourceJSON, entry.Inject)
			if err != nil {
				return nil, fmt.Errorf("injecting at %q from %q: %w", entry.Inject, sourceName, err)
			}
			sourceJSON = injected
		}

		// Merge: inherited data is the base, current data overrides
		merged, err := mergeJSON(sourceJSON, currentData)
		if err != nil {
			return nil, fmt.Errorf("merging from %q: %w", sourceName, err)
		}
		currentData = merged
	}

	return currentData, nil
}

// fetchSource fetches config data from an internal file.
func (s *Service) fetchSource(ctx context.Context, source string) ([]byte, error) {
	file, err := s.File(ctx, source, 0)
	if err != nil {
		return nil, err
	}
	return file.Data, nil
}

// fetchExternalConfig fetches configuration data from an external resource.
// The resourceName is looked up in settings.External, and path is the
// resource-specific path (e.g., Vault secret path or HTTP endpoint path).
func (s *Service) fetchExternalConfig(ctx context.Context, resourceName string, path string) ([]byte, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading settings: %w", err)
	}

	ext, exists := settings.External[resourceName]
	if !exists {
		return nil, fmt.Errorf("external resource %q not found in settings", resourceName)
	}

	// HTTP-based external resource
	if ext.Http != nil {
		return s.fetchHTTPConfig(ctx, ext.Http, path)
	}

	// Vault-based external resource
	if ext.Vault != nil {
		return s.fetchVaultConfig(ctx, ext.Vault, path)
	}

	// Kubernetes external resource
	if ext.Kubernetes != nil {
		return s.fetchKubernetesConfig(ctx, ext.Kubernetes, path)
	}

	return nil, fmt.Errorf("external resource %q has no configured provider (http, vault, or kubernetes)", resourceName)
}

// fetchVaultConfig reads a secret from Vault and returns it as JSON bytes.
// The secretPath is constructed from vault.Mount + the path.
// For example: mount="secret", path="myapp/db" → reads "secret/data/myapp/db"
func (s *Service) fetchVaultConfig(ctx context.Context, vault *external.Vault, path string) ([]byte, error) {
	if vault.Address == "" {
		return nil, fmt.Errorf("vault address is empty")
	}

	client := s.getVaultClient(ctx, vault)

	// Construct the full secret path: mount + "/data/" + path (KV v2 convention)
	// If using the old BasePath field, use it directly as before
	mount := vault.Mount
	var secretPath string
	if path != "" {
		secretPath = strings.TrimRight(mount, "/") + "/" + strings.TrimLeft(path, "/")
	} else {
		secretPath = mount
	}

	data, err := client.ReadSecret(ctx, secretPath)
	if err != nil {
		return nil, fmt.Errorf("reading vault secret at %q: %w", secretPath, err)
	}

	// Serialize the secret data as JSON bytes for merging
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("serializing vault secret data: %w", err)
	}

	return jsonBytes, nil
}

// fetchHTTPConfig fetches config from an HTTP endpoint using the ok client.
func (s *Service) fetchHTTPConfig(ctx context.Context, cfg *ok.Config, path string) ([]byte, error) {
	client, err := cfg.New()
	if err != nil {
		return nil, fmt.Errorf("creating HTTP client: %w", err)
	}

	endpoint := "/"
	if path != "" {
		endpoint = "/" + strings.TrimLeft(path, "/")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}

	var body []byte
	if err := client.Do(req, func(resp *http.Response) error {
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTP request returned status %d", resp.StatusCode)
		}
		var readErr error
		body, readErr = io.ReadAll(resp.Body)
		return readErr
	}); err != nil {
		return nil, fmt.Errorf("fetching HTTP config: %w", err)
	}

	return body, nil
}

// fetchKubernetesConfig fetches config from a Kubernetes Secret or ConfigMap.
// Path format: "namespace/secret/name" or "namespace/configmap/name".
func (s *Service) fetchKubernetesConfig(ctx context.Context, k8s *external.Kubernetes, path string) ([]byte, error) {
	client, err := s.getKubeClient(k8s)
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

// ListExternalPaths lists available paths from an external resource.
// For Vault, this lists secrets under the mount path.
// For HTTP, this is not supported (returns empty).
func (s *Service) ListExternalPaths(ctx context.Context, resourceName string, prefix string) ([]string, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading settings: %w", err)
	}

	ext, exists := settings.External[resourceName]
	if !exists {
		return nil, fmt.Errorf("external resource %q not found in settings: %w", resourceName, ErrNotFound)
	}

	if ext.Vault != nil {
		return s.listVaultSecrets(ctx, ext.Vault, prefix)
	}

	// Kubernetes resource listing
	if ext.Kubernetes != nil {
		return s.listKubernetesResources(ctx, ext.Kubernetes, prefix)
	}

	// HTTP doesn't support listing
	return []string{}, nil
}

// listVaultSecrets lists secret keys from Vault under the given prefix.
func (s *Service) listVaultSecrets(ctx context.Context, vault *external.Vault, prefix string) ([]string, error) {
	if vault.Address == "" {
		return nil, fmt.Errorf("vault address is empty")
	}

	client := s.getVaultClient(ctx, vault)

	mount := vault.Mount
	// For KV v2 LIST, the path is: mount + "/metadata/" + prefix
	// For KV v1 LIST, the path is: mount + "/" + prefix
	// We try KV v2 first, then fall back to v1 style

	listPath := strings.TrimRight(mount, "/") + "/metadata/"
	if prefix != "" {
		listPath += strings.TrimLeft(prefix, "/")
	}

	keys, err := client.ListSecrets(ctx, listPath)
	if err != nil {
		// Try without /metadata/ (KV v1 or direct mount)
		listPath = strings.TrimRight(mount, "/") + "/"
		if prefix != "" {
			listPath += strings.TrimLeft(prefix, "/")
		}
		keys, err = client.ListSecrets(ctx, listPath)
		if err != nil {
			return nil, err
		}
	}

	return keys, nil
}

// listKubernetesResources lists available Kubernetes resources for the path browser.
func (s *Service) listKubernetesResources(ctx context.Context, k8s *external.Kubernetes, prefix string) ([]string, error) {
	client, err := s.getKubeClient(k8s)
	if err != nil {
		return nil, fmt.Errorf("kubernetes client: %w", err)
	}

	return client.ListResources(ctx, prefix)
}
