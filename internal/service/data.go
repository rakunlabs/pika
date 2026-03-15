package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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
		if entry.Source == "" {
			continue
		}

		// Fetch the source data
		sourceData, err := s.fetchSource(ctx, entry.Source)
		if err != nil {
			return nil, fmt.Errorf("resolving inheritance from %q: %w", entry.Source, err)
		}

		// Ensure source data is JSON for merging
		// Try to detect format from the source file's meta if internal
		sourceJSON := sourceData
		file, fileErr := s.File(ctx, entry.Source, 0)
		if fileErr == nil && file.Meta.Format != "" && file.Meta.Format != "json" && file.Meta.Format != "raw" {
			converted, convErr := ConvertFormat(sourceData, file.Meta.Format, "json")
			if convErr == nil {
				sourceJSON = converted
			}
		}

		// Filter by paths if specified
		if len(entry.Paths) > 0 {
			filtered, err := filterByPaths(sourceJSON, entry.Paths)
			if err != nil {
				return nil, fmt.Errorf("filtering paths from %q: %w", entry.Source, err)
			}
			sourceJSON = filtered
		}

		// Inject at target path if specified
		if entry.Inject != "" {
			injected, err := injectAtPath(sourceJSON, entry.Inject)
			if err != nil {
				return nil, fmt.Errorf("injecting at %q from %q: %w", entry.Inject, entry.Source, err)
			}
			sourceJSON = injected
		}

		// Merge: inherited data is the base, current data overrides
		merged, err := mergeJSON(sourceJSON, currentData)
		if err != nil {
			return nil, fmt.Errorf("merging from %q: %w", entry.Source, err)
		}
		currentData = merged
	}

	return currentData, nil
}

// fetchSource fetches config data from an internal file or external resource.
func (s *Service) fetchSource(ctx context.Context, source string) ([]byte, error) {
	// Try internal file first
	file, err := s.File(ctx, source, 0)
	if err == nil {
		return file.Data, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	// Not internal — try external resource
	return s.fetchExternalConfig(ctx, source)
}

// fetchExternalConfig fetches configuration data from an external resource.
// The source is looked up in settings.External by name and fetched via HTTP.
func (s *Service) fetchExternalConfig(ctx context.Context, sourceName string) ([]byte, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading settings: %w", err)
	}

	ext, exists := settings.External[sourceName]
	if !exists {
		return nil, fmt.Errorf("external resource %q not found in settings", sourceName)
	}

	// HTTP-based external resource
	if ext.Http != nil {
		return s.fetchHTTPConfig(ctx, ext.Http.BaseURL)
	}

	// Vault-based external resource
	if ext.Vault != nil {
		return s.fetchVaultConfig(ctx, ext.Vault, sourceName)
	}

	return nil, fmt.Errorf("external resource %q has no configured provider (http or vault)", sourceName)
}

// fetchVaultConfig reads a secret from Vault and returns it as JSON bytes.
// The secretPath is constructed from vault.BasePath + the config's inherit source context.
// For example: basePath="secret/data", configPath="myapp/db" → reads "secret/data/myapp/db"
func (s *Service) fetchVaultConfig(ctx context.Context, vault *external.Vault, configPath string) ([]byte, error) {
	if vault.Address == "" {
		return nil, fmt.Errorf("vault address is empty")
	}

	client := s.getVaultClient(vault)

	// Construct the full secret path: basePath + configPath
	secretPath := vault.BasePath
	if configPath != "" {
		secretPath = strings.TrimRight(secretPath, "/") + "/" + strings.TrimLeft(configPath, "/")
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

// fetchHTTPConfig fetches JSON/YAML config from an HTTP URL.
func (s *Service) fetchHTTPConfig(ctx context.Context, url string) ([]byte, error) {
	if url == "" {
		return nil, fmt.Errorf("empty URL for HTTP external resource")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP request returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading HTTP response: %w", err)
	}

	return body, nil
}
