package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

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

// fetchExternalConfig fetches configuration data from an external
// resource. The resourceName is looked up in settings.External, and
// path is the resource-specific path (e.g., Vault secret path or HTTP
// endpoint path).
//
// All backend-specific logic now lives in internal/external/<backend>.go
// behind the Provider interface — this dispatcher only resolves the
// record and delegates.
func (s *Service) fetchExternalConfig(ctx context.Context, resourceName string, path string) ([]byte, error) {
	provider, err := s.externalProvider(ctx, resourceName)
	if err != nil {
		return nil, err
	}
	return provider.Fetch(ctx, path)
}

// externalProvider resolves a Provider for the named resource. It is
// the only place that maps "settings + name" to "live driver". Callers
// (Fetch / List / Test) all funnel through here so a future change to
// resolution rules (e.g. multi-tenant prefixing) lands in one spot.
func (s *Service) externalProvider(ctx context.Context, resourceName string) (external.Provider, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading settings: %w", err)
	}

	ext, exists := settings.External[resourceName]
	if !exists {
		return nil, fmt.Errorf("external resource %q not found in settings: %w", resourceName, ErrNotFound)
	}

	provider, err := external.ResourceProvider(ext, s)
	if err != nil {
		return nil, fmt.Errorf("external resource %q: %w", resourceName, err)
	}
	return provider, nil
}

// ListExternalPaths enumerates resource paths under the given prefix
// for the named external resource. Returns an empty slice (not an
// error) for backends that have no list semantics — HTTP is the only
// such case today.
func (s *Service) ListExternalPaths(ctx context.Context, resourceName string, prefix string) ([]string, error) {
	provider, err := s.externalProvider(ctx, resourceName)
	if err != nil {
		return nil, err
	}
	return provider.List(ctx, prefix)
}

// ExternalTestResult is the wire shape returned by TestExternal. It
// re-exports external.TestResult so existing callers (the API handler)
// don't have to import internal/external for a type they only marshal
// to JSON.
type ExternalTestResult = external.TestResult

// TestExternal performs a live connection check against the named
// external resource using its stored credentials. The intent is "can
// pika reach this backend with what's configured" — not a deep
// correctness check. Errors during the probe are flattened into the
// TestResult.Message field, mirroring what each Provider returns so
// the SPA can render a single error path.
func (s *Service) TestExternal(ctx context.Context, resourceName string) (*ExternalTestResult, error) {
	provider, err := s.externalProvider(ctx, resourceName)
	if err != nil {
		return nil, err
	}
	r := provider.Test(ctx)
	return &r, nil
}

// ReadExternal returns a single entry from the named resource at the
// given resource-specific path. The shape mirrors external.Entry.
func (s *Service) ReadExternal(ctx context.Context, resourceName, path string) (*external.Entry, error) {
	provider, err := s.externalProvider(ctx, resourceName)
	if err != nil {
		return nil, err
	}
	return provider.Read(ctx, path)
}

// WriteExternal persists a new value at the given path. Returns
// external.ErrNotSupported (wrapped) if the backend doesn't support
// writes — the API layer translates this into a 405 so the SPA can
// show a "not supported" message instead of a generic 500.
func (s *Service) WriteExternal(ctx context.Context, resourceName, path string, data map[string]any) error {
	provider, err := s.externalProvider(ctx, resourceName)
	if err != nil {
		return err
	}
	return provider.Write(ctx, path, data)
}

// DeleteExternal removes the entry at the path. Same not-supported
// translation as WriteExternal applies.
func (s *Service) DeleteExternal(ctx context.Context, resourceName, path string) error {
	provider, err := s.externalProvider(ctx, resourceName)
	if err != nil {
		return err
	}
	return provider.Delete(ctx, path)
}

// ListExternalVersions returns the version history for a path. Only
// Vault KV v2 has real history today; everything else returns
// ErrNotSupported, which the SPA renders as a hidden version selector.
func (s *Service) ListExternalVersions(ctx context.Context, resourceName, path string) ([]external.Version, error) {
	provider, err := s.externalProvider(ctx, resourceName)
	if err != nil {
		return nil, err
	}
	return provider.ListVersions(ctx, path)
}

// ReadExternalVersion fetches a specific historical version.
func (s *Service) ReadExternalVersion(ctx context.Context, resourceName, path, version string) (*external.Entry, error) {
	provider, err := s.externalProvider(ctx, resourceName)
	if err != nil {
		return nil, err
	}
	return provider.ReadVersion(ctx, path, version)
}

// ExternalSearchHit is one entry in a search response. Type is
// either "name" (the path itself matched the query) or "content" (the
// stored value contained the query). Snippet is populated for content
// hits only — a short slice of the matched value around the hit
// position, with the matched substring left in place so the SPA can
// highlight it client-side.
type ExternalSearchHit struct {
	Path    string `json:"path"`
	Type    string `json:"type"`
	Snippet string `json:"snippet,omitempty"`
}

// ExternalSearchMode controls how SearchExternal walks the resource.
//
//   - "name": only the path strings are compared against the query. No
//     reads issued; cheap and works on any backend that supports List.
//   - "all":  every leaf path is also Read() and the response value is
//     scanned for the query. Substantially slower (O(N) reads) but
//     finds matches inside values too.
type ExternalSearchMode string

const (
	ExternalSearchModeName ExternalSearchMode = "name"
	ExternalSearchModeAll  ExternalSearchMode = "all"
)

// SearchExternal walks the named resource looking for paths and/or
// values that match the query. Implementation notes:
//
//   - We use breadth-first traversal so the first results returned
//     are shallow and feel responsive even on large trees. A DFS
//     would either delay the first match until the deepest folder is
//     fully explored, or require the caller to wait for the full
//     result set.
//
//   - The traversal short-circuits when len(hits) >= limit so a 100k-
//     key Consul instance doesn't burn the operator's CPU on a search
//     for "x". A future enhancement could stream results via SSE; for
//     now a hard cap is sufficient — the operator who needs every
//     match can adjust the limit query param.
//
//   - List() failures on a sub-prefix are tolerated (logged via the
//     returned error only if the root fails). Sub-prefix failures
//     are common on permission-restricted backends (e.g. a Vault
//     token with KV access to /myapp/* but not /shared/*); we don't
//     want one inaccessible folder to break search of accessible
//     siblings.
//
//   - For mode=all, we issue Read() on every leaf path. Read errors
//     for individual paths are skipped silently for the same reason
//     as List failures — partial results beat no results.
//
// Query matching is case-insensitive substring on both path and
// stringified value. We don't expose a regex hook to keep the
// surface small and predictable.
func (s *Service) SearchExternal(
	ctx context.Context,
	resourceName, query string,
	mode ExternalSearchMode,
	limit int,
) ([]ExternalSearchHit, error) {
	if limit <= 0 {
		limit = 200
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, nil
	}

	provider, err := s.externalProvider(ctx, resourceName)
	if err != nil {
		return nil, err
	}

	hits := make([]ExternalSearchHit, 0, 32)

	// BFS frontier. We track prefixes that are folders (path ends in
	// "/") separately from leaves so we know what to Read vs. what to
	// recurse into. Different backends differ on this naming
	// convention (Vault folders end with "/", K8s paths don't), so
	// the leaf check is "no trailing slash AND no further children".
	type item struct{ prefix string }
	frontier := []item{{prefix: ""}}

	for len(frontier) > 0 && len(hits) < limit {
		cur := frontier[0]
		frontier = frontier[1:]

		children, listErr := provider.List(ctx, cur.prefix)
		if listErr != nil {
			// Root failure surfaces; sub-prefix failures are skipped.
			if cur.prefix == "" {
				return nil, listErr
			}
			continue
		}

		for _, child := range children {
			if len(hits) >= limit {
				break
			}

			full := joinPath(cur.prefix, child)
			lowerFull := strings.ToLower(full)
			isFolder := strings.HasSuffix(child, "/")

			// Name match — applies whether the entry is a folder or a
			// leaf. Folders get a name-type hit too because the
			// operator might want to navigate to "myapp/" by name.
			if strings.Contains(lowerFull, q) {
				hits = append(hits, ExternalSearchHit{
					Path: strings.TrimSuffix(full, "/"),
					Type: "name",
				})
			}

			if isFolder {
				frontier = append(frontier, item{prefix: full})
				continue
			}

			// Leaf. Content mode reads the value and probes it for
			// the query; name mode skips the network round trip.
			if mode != ExternalSearchModeAll {
				continue
			}
			entry, err := provider.Read(ctx, full)
			if err != nil || entry == nil {
				continue
			}
			body := stringifyEntry(entry)
			lowerBody := strings.ToLower(body)
			idx := strings.Index(lowerBody, q)
			if idx < 0 {
				continue
			}
			hits = append(hits, ExternalSearchHit{
				Path:    full,
				Type:    "content",
				Snippet: makeSnippet(body, idx, len(q)),
			})
		}
	}

	return hits, nil
}

// joinPath joins a folder prefix and a child name into a full path.
// Tolerates a missing trailing slash on the prefix because backends
// disagree on the convention.
func joinPath(prefix, child string) string {
	if prefix == "" {
		return child
	}
	if strings.HasSuffix(prefix, "/") {
		return prefix + child
	}
	return prefix + "/" + child
}

// stringifyEntry collapses the entry into a single searchable string.
// Prefers the raw JSON when available (covers every backend uniformly
// — including structured maps and JSON-formatted strings) and falls
// back to a JSON-marshal of the Data map for entries that didn't
// populate Raw.
func stringifyEntry(e *external.Entry) string {
	if len(e.Raw) > 0 {
		return string(e.Raw)
	}
	b, _ := json.Marshal(e.Data)
	return string(b)
}

// makeSnippet returns a ~120-char window around the match. The
// snippet preserves the matched substring verbatim and surrounds it
// with up to ~40 chars on each side, so SPA-side highlighting can
// just substring-match the query against the snippet to colour it.
func makeSnippet(body string, matchIdx, matchLen int) string {
	const window = 40
	start := matchIdx - window
	if start < 0 {
		start = 0
	}
	end := matchIdx + matchLen + window
	if end > len(body) {
		end = len(body)
	}
	snippet := body[start:end]
	// Trim partial UTF-8 sequences at the edges so the response is
	// always valid UTF-8. ToValidUTF8 replaces invalid bytes with the
	// replacement rune — cheaper than locating the nearest valid
	// boundary by hand.
	snippet = strings.ToValidUTF8(snippet, "\ufffd")
	// Visual breadcrumbs for truncation. Match Configuration search's
	// snippet contract (… on either side when truncated).
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(body) {
		snippet = snippet + "…"
	}
	return snippet
}

// ExternalResourceSummary is the metadata the SPA browser needs to
// render its left pane: name, backend kind, and capability flags. We
// expose this as a dedicated endpoint so the browser doesn't need to
// pull /api/v1/settings (which carries the full secret-stripped
// settings tree and requires settings.manage); a future split could
// gate the browser on a narrower capability.
type ExternalResourceSummary struct {
	Name         string                `json:"name"`
	Kind         string                `json:"kind"`
	Capabilities external.Capabilities `json:"capabilities"`
}

// ListExternalResources enumerates every configured external resource
// with its Kind + Capabilities. Order is alphabetical by name so the
// SPA can render a stable list without sorting itself.
func (s *Service) ListExternalResources(ctx context.Context) ([]ExternalResourceSummary, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading settings: %w", err)
	}
	out := make([]ExternalResourceSummary, 0, len(settings.External))
	for name, ext := range settings.External {
		// Construct a provider purely to read its Capabilities; we
		// don't call any network method, so even a misconfigured
		// resource shows up here (the browser surfaces the error
		// later when an operation actually runs).
		provider, perr := external.ResourceProvider(ext, s)
		if perr != nil {
			// Unknown / empty backend — list it as "unknown" with no
			// caps so the user sees something they can clean up.
			out = append(out, ExternalResourceSummary{Name: name, Kind: "unknown"})
			continue
		}
		out = append(out, ExternalResourceSummary{
			Name:         name,
			Kind:         provider.Kind(),
			Capabilities: provider.Capabilities(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
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
