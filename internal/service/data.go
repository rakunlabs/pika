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
	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/rawfs/localfs"
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

	var convError string

	// Convert to JSON for merging if needed
	if needsMerge && format != "json" && format != "" && format != "raw" {
		jsonData, err := ConvertFormat(resolved, format, "json")
		if err != nil {
			convError = fmt.Sprintf("invalid %s: %v", format, err)
		} else {
			resolved = jsonData
		}
	}

	// Resolve all inheritance entries (skip if conversion failed).
	// The visiting set tracks the in-flight ancestor chain to detect cycles
	// (e.g., A inherits B inherits A) while still allowing diamond
	// inheritance (A inherits B and C, both inherit D).
	if len(file.Meta.Inherits) > 0 && convError == "" {
		visiting := map[string]bool{filePath: true}
		var err error
		resolved, err = s.resolveInherits(ctx, resolved, file.Meta.Inherits, visiting)
		if err != nil {
			return nil, err
		}
	}

	// Convert back to original format after merging
	if needsMerge && format != "json" && format != "" && format != "raw" && convError == "" {
		converted, err := ConvertFormat(resolved, "json", format)
		if err == nil {
			resolved = converted
		}
	}

	return &DataResult{
		Data:   resolved,
		Format: format,
		Error:  convError,
	}, nil
}

// RenderFile resolves a file's configuration for preview purposes.
// Unlike GetData, this accepts raw content and meta from the UI editor
// (which may not be saved yet) and performs resolution.
//
// variationKey, when non-empty, identifies this render as a variant of
// filePath — the cycle guard is then seeded with the variant's storage
// key (filePath@variationKey) instead of the bare parent path, so a
// variant that inherits from its parent file isn't mis-flagged as a
// self-cycle.
func (s *Service) RenderFile(ctx context.Context, filePath string, variationKey string, content string, meta *FileMeta) (*RenderResult, error) {
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
				Data:  base64.StdEncoding.EncodeToString(currentData),
				Error: fmt.Sprintf("invalid %s: %v", format, err),
			}, nil
		}
		currentData = jsonData
	}

	// Step 1: Resolve all inheritance entries. Seed the cycle guard with
	// this file's storage identity so it can't transitively inherit
	// from itself — but use the variant storage key when rendering a
	// variant (so a variant inheriting from its parent file is not
	// mis-detected as a self-cycle).
	if meta != nil && len(meta.Inherits) > 0 {
		visiting := map[string]bool{}
		if filePath != "" {
			seed := filePath
			if variationKey != "" {
				seed = variantKey(filePath, variationKey)
			}
			visiting[seed] = true
		}
		var err error
		currentData, err = s.resolveInherits(ctx, currentData, meta.Inherits, visiting)
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
//
// visiting tracks the set of internal file paths currently on the resolution
// stack so we can detect cycles (e.g., A inherits B and B inherits A). It is
// safe to pass nil — a fresh set is allocated. The same path may appear
// multiple times across sibling branches (diamond inheritance) as long as it
// isn't already on the ancestor chain.
func (s *Service) resolveInherits(ctx context.Context, currentData []byte, entries []InheritEntry, visiting map[string]bool) ([]byte, error) {
	if visiting == nil {
		visiting = map[string]bool{}
	}
	for _, entry := range entries {
		if entry.Source == "" && entry.Resource == "" && entry.Mount == "" {
			continue
		}

		var sourceData []byte
		var sourceName string
		var sourceMeta *FileMeta // populated for internal sources so we can recurse
		var err error

		if entry.Mount != "" {
			// Raw mount: lookup mount by prefix and read file at path
			sourceName = "mount:" + entry.Mount + "/" + entry.Path
			sourceData, err = s.fetchRawMountConfig(ctx, entry.Mount, entry.Path)
		} else if entry.Resource != "" {
			// External resource: lookup by resource name and fetch with path
			sourceName = entry.Resource + ":" + entry.Path
			sourceData, err = s.fetchExternalConfig(ctx, entry.Resource, entry.Path)
		} else {
			// Internal file or legacy external (backward compat).
			// Cycle guard: if this path is already on the current ancestor
			// chain we'd recurse forever — fail fast with a clear error.
			if visiting[entry.Source] {
				return nil, fmt.Errorf("inheritance cycle detected at %q", entry.Source)
			}
			sourceName = entry.Source
			var srcFile *File
			srcFile, err = s.File(ctx, entry.Source, 0)
			if err == nil {
				sourceData = srcFile.Data
				meta := srcFile.Meta
				sourceMeta = &meta
			}
		}

		if err != nil {
			return nil, fmt.Errorf("resolving inheritance from %q: %w", sourceName, err)
		}

		// Ensure source data is JSON for merging
		sourceJSON := sourceData
		if entry.Resource == "" && entry.Mount == "" {
			// For internal sources, try to detect format and convert
			if sourceMeta != nil && sourceMeta.Format != "" && sourceMeta.Format != "json" && sourceMeta.Format != "raw" {
				converted, convErr := ConvertFormat(sourceData, sourceMeta.Format, "json")
				if convErr == nil {
					sourceJSON = converted
				}
			}

			// Transitively resolve the source's own inherits before
			// applying paths/inject/merge so e.g. A -> B -> C composes
			// correctly: B's view of itself includes everything inherited
			// from C, which is then what A sees.
			if sourceMeta != nil && len(sourceMeta.Inherits) > 0 {
				visiting[entry.Source] = true
				resolved, recErr := s.resolveInherits(ctx, sourceJSON, sourceMeta.Inherits, visiting)
				delete(visiting, entry.Source)
				if recErr != nil {
					return nil, fmt.Errorf("resolving nested inheritance from %q: %w", sourceName, recErr)
				}
				sourceJSON = resolved
			}
		} else if entry.Mount != "" {
			// For raw mount sources, try to detect format from file extension
			format := detectFormatFromPath(entry.Path)
			if format != "" && format != "json" && format != "raw" {
				converted, convErr := ConvertFormat(sourceData, format, "json")
				if convErr == nil {
					sourceJSON = converted
				}
			}
		}

		// Rename the hardcoded "value" wrapper key used by non-JSON
		// secret backends (GCP/Etcd/Consul) so the user's "Include paths"
		// selection can pick it up under a meaningful key.
		if len(entry.Paths) > 0 && (entry.Resource != "" || entry.Mount != "") {
			renamed, ok := renameValueWrapperKey(sourceJSON, entry.Paths[0])
			if ok {
				sourceJSON = renamed
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

	// Consul KV
	if ext.Consul != nil {
		return s.fetchConsulConfig(ctx, ext.Consul, path)
	}

	// etcd
	if ext.Etcd != nil {
		return s.fetchEtcdConfig(ctx, ext.Etcd, path)
	}

	// AWS Secrets Manager / SSM
	if ext.AWS != nil {
		return s.fetchAWSConfig(ctx, ext.AWS, path)
	}

	// GCP Secret Manager
	if ext.GCP != nil {
		return s.fetchGCPConfig(ctx, ext.GCP, path)
	}

	// Azure Key Vault
	if ext.Azure != nil {
		return s.fetchAzureConfig(ctx, ext.Azure, path)
	}

	return nil, fmt.Errorf("external resource %q has no configured provider", resourceName)
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

	// Consul KV listing
	if ext.Consul != nil {
		client := external.NewConsulClient(ext.Consul.Address, ext.Consul.Token)
		return client.ListSecrets(ctx, prefix)
	}

	// etcd listing
	if ext.Etcd != nil {
		client := external.NewEtcdClient(ext.Etcd.Address, ext.Etcd.Username, ext.Etcd.Password)
		return client.ListSecrets(ctx, prefix)
	}

	// AWS listing
	if ext.AWS != nil {
		client := external.NewAWSClient(ext.AWS.Region, ext.AWS.AccessKey, ext.AWS.SecretKey)
		if ext.AWS.Service == "ssm" {
			return client.ListSSMParameters(ctx, prefix)
		}
		return client.ListSecretsManagerSecrets(ctx)
	}

	// GCP listing
	if ext.GCP != nil {
		client, err := s.getGCPClient(ext.GCP)
		if err != nil {
			return nil, err
		}
		return client.ListSecrets(ctx)
	}

	// Azure listing
	if ext.Azure != nil {
		client := s.getAzureClient(ext.Azure)
		return client.ListSecrets(ctx)
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

// fetchConsulConfig reads a value from Consul KV.
func (s *Service) fetchConsulConfig(ctx context.Context, consul *external.Consul, path string) ([]byte, error) {
	if consul.Address == "" {
		return nil, fmt.Errorf("consul address is empty")
	}

	client := external.NewConsulClient(consul.Address, consul.Token)
	data, err := client.ReadSecret(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("reading consul key at %q: %w", path, err)
	}

	return json.Marshal(data)
}

// fetchEtcdConfig reads a value from etcd.
func (s *Service) fetchEtcdConfig(ctx context.Context, etcd *external.Etcd, path string) ([]byte, error) {
	if etcd.Address == "" {
		return nil, fmt.Errorf("etcd address is empty")
	}

	client := external.NewEtcdClient(etcd.Address, etcd.Username, etcd.Password)
	data, err := client.ReadSecret(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("reading etcd key at %q: %w", path, err)
	}

	return json.Marshal(data)
}

// fetchAWSConfig reads a secret from AWS Secrets Manager or SSM Parameter Store.
func (s *Service) fetchAWSConfig(ctx context.Context, aws *external.AWS, path string) ([]byte, error) {
	client := external.NewAWSClient(aws.Region, aws.AccessKey, aws.SecretKey)

	var data map[string]any
	var err error

	if aws.Service == "ssm" {
		data, err = client.ReadSSMParameter(ctx, path)
	} else {
		data, err = client.ReadSecretsManagerSecret(ctx, path)
	}
	if err != nil {
		return nil, fmt.Errorf("reading AWS %s at %q: %w", aws.Service, path, err)
	}

	return json.Marshal(data)
}

// fetchGCPConfig reads a secret from GCP Secret Manager.
func (s *Service) fetchGCPConfig(ctx context.Context, gcp *external.GCP, path string) ([]byte, error) {
	client, err := s.getGCPClient(gcp)
	if err != nil {
		return nil, err
	}

	data, err := client.ReadSecret(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("reading GCP secret %q: %w", path, err)
	}

	return json.Marshal(data)
}

// fetchAzureConfig reads a secret from Azure Key Vault.
func (s *Service) fetchAzureConfig(ctx context.Context, azure *external.Azure, path string) ([]byte, error) {
	client := s.getAzureClient(azure)

	data, err := client.ReadSecret(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("reading Azure secret %q: %w", path, err)
	}

	return json.Marshal(data)
}

// fetchRawMountConfig reads a file from a raw mount and returns its contents.
// The mountPrefix identifies which raw mount to use, and path is the file path within it.
func (s *Service) fetchRawMountConfig(ctx context.Context, mountPrefix string, path string) ([]byte, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading settings: %w", err)
	}

	var mountEntry *RawMountEntry
	for i := range settings.RawMounts {
		if settings.RawMounts[i].Prefix == mountPrefix {
			mountEntry = &settings.RawMounts[i]
			break
		}
	}
	if mountEntry == nil {
		return nil, fmt.Errorf("raw mount %q not found in settings", mountPrefix)
	}

	fs, err := newRawFSFromMountEntry(*mountEntry)
	if err != nil {
		return nil, fmt.Errorf("creating filesystem for mount %q: %w", mountPrefix, err)
	}

	reader, _, err := fs.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading file %q from mount %q: %w", path, mountPrefix, err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("reading file %q from mount %q: %w", path, mountPrefix, err)
	}

	return data, nil
}

// newRawFSFromMountEntry creates a RawFS instance from a RawMountEntry.
func newRawFSFromMountEntry(m RawMountEntry) (rawfs.RawFS, error) {
	mountType := m.Type
	if mountType == "" {
		mountType = "local"
	}

	switch mountType {
	case "local":
		if m.Path == "" {
			return nil, fmt.Errorf("path is required for local mount")
		}
		return localfs.New(m.Path)
	case "s3":
		if m.S3 == nil {
			return nil, fmt.Errorf("s3 config is required")
		}
		if rawfs.NewS3FSFunc == nil {
			return nil, fmt.Errorf("s3 backend not available")
		}
		return rawfs.NewS3FSFunc(m.S3.Bucket, m.S3.Region, m.S3.Endpoint, m.S3.AccessKey, m.S3.SecretKey, m.S3.Prefix, m.S3.PathStyle, m.S3.Secure)
	case "ftp":
		if m.FTP == nil {
			return nil, fmt.Errorf("ftp config is required")
		}
		if rawfs.NewFTPFSFunc == nil {
			return nil, fmt.Errorf("ftp backend not available")
		}
		return rawfs.NewFTPFSFunc(m.FTP.Host, m.FTP.Username, m.FTP.Password, m.FTP.BasePath, m.FTP.TLS)
	case "sftp":
		if m.SFTP == nil {
			return nil, fmt.Errorf("sftp config is required")
		}
		if rawfs.NewSFTPFSFunc == nil {
			return nil, fmt.Errorf("sftp backend not available")
		}
		return rawfs.NewSFTPFSFunc(m.SFTP.Host, m.SFTP.Username, m.SFTP.Password, m.SFTP.PrivateKey, m.SFTP.BasePath)
	case "webdav":
		if m.WebDAV == nil {
			return nil, fmt.Errorf("webdav config is required")
		}
		if rawfs.NewWebDAVFSFunc == nil {
			return nil, fmt.Errorf("webdav backend not available")
		}
		return rawfs.NewWebDAVFSFunc(m.WebDAV.URL, m.WebDAV.Username, m.WebDAV.Password, m.WebDAV.BasePath)
	case "vercel-blob":
		if m.VercelBlob == nil {
			return nil, fmt.Errorf("vercelBlob config is required")
		}
		if rawfs.NewVercelBlobFSFunc == nil {
			return nil, fmt.Errorf("vercel-blob backend not available")
		}
		return rawfs.NewVercelBlobFSFunc(m.VercelBlob.Token, m.VercelBlob.StoreID, m.VercelBlob.Prefix)
	default:
		return nil, fmt.Errorf("unknown mount type %q", mountType)
	}
}

// detectFormatFromPath guesses the file format from a file path extension.
func detectFormatFromPath(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml"):
		return "yaml"
	case strings.HasSuffix(lower, ".toml"):
		return "toml"
	case strings.HasSuffix(lower, ".json"):
		return "json"
	default:
		return ""
	}
}
