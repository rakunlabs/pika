package mcpsrv

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/pika/internal/service"
)

// defaultSearchLimit caps how many hits a single search_configs call
// returns. Search is an unindexed full-tree walk that reads file contents,
// so an unbounded result set is both slow and useless to a model with a
// finite context window. Callers that need more can narrow the query.
const defaultSearchLimit = 50

// maxSearchLimit is the hard ceiling regardless of what the caller asks for.
const maxSearchLimit = 200

type searchConfigsInput struct {
	Query string `json:"query" jsonschema:"Case-insensitive substring to look for."`
	Mode  string `json:"mode,omitempty" jsonschema:"\"name\" matches only against config paths (fast). \"all\" (default) also scans file contents."`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum number of hits to return. Default 50, maximum 200."`
}

type searchHit struct {
	Path string `json:"path" jsonschema:"Config path. A \"?variant\" suffix means the hit is in a variant overlay."`
	Type string `json:"type" jsonschema:"\"name\" when the path matched, \"content\" when the file body matched."`
}

type searchConfigsOutput struct {
	Hits      []searchHit `json:"hits"`
	Truncated bool        `json:"truncated" jsonschema:"True when the limit was reached and more matches may exist."`
}

type listFolderInput struct {
	Path string `json:"path,omitempty" jsonschema:"Folder path. Omit or pass an empty string for the root folder."`
}

type listFolderOutput struct {
	Path     string              `json:"path"`
	Folders  []string            `json:"folders" jsonschema:"Names of direct subfolders (not full paths)."`
	Files    []string            `json:"files" jsonschema:"Names of configs in this folder (not full paths)."`
	Variants map[string][]string `json:"variants,omitempty" jsonschema:"Variant keys available per config name."`
}

type getConfigInput struct {
	Path    string `json:"path" jsonschema:"Full config path, e.g. \"team-a/service/config.yaml\"."`
	Variant string `json:"variant,omitempty" jsonschema:"Variant key to read instead of the base config, e.g. \"prod\"."`
	Version string `json:"version,omitempty" jsonschema:"Version to read: an internal version number (\"7\") or a semver (\"1.2.0\") matched against version constraints. Omit for the latest."`
}

type configOutput struct {
	Path        string                 `json:"path"`
	Variant     string                 `json:"variant,omitempty"`
	Content     string                 `json:"content" jsonschema:"The stored source text, before inheritance or templating is applied."`
	Format      string                 `json:"format,omitempty" jsonschema:"json, yaml, toml or raw."`
	Description string                 `json:"description,omitempty"`
	GoTemplate  bool                   `json:"go_template" jsonschema:"True when the content is rendered as a Go template before use."`
	Inherits    []service.InheritEntry `json:"inherits,omitempty" jsonschema:"Inheritance sources merged underneath this config."`
}

type getResolvedConfigInput struct {
	Path    string `json:"path" jsonschema:"Full config path."`
	Variant string `json:"variant,omitempty" jsonschema:"Variant key to resolve, e.g. \"prod\"."`
	Version string `json:"version,omitempty" jsonschema:"Version number or semver. Omit for the latest."`
	Format  string `json:"format,omitempty" jsonschema:"Convert the result to this format (json, yaml, toml). Omit to keep the config's own format."`
}

type getResolvedConfigOutput struct {
	Path    string `json:"path"`
	Variant string `json:"variant,omitempty"`
	Content string `json:"content" jsonschema:"The effective configuration a consuming application receives."`
	Format  string `json:"format,omitempty"`
}

type listVersionsInput struct {
	Path    string `json:"path" jsonschema:"Full config path."`
	Variant string `json:"variant,omitempty" jsonschema:"Variant key. Variants keep their own independent version history."`
}

type versionEntry struct {
	Version    int64  `json:"version"`
	Constraint string `json:"constraint,omitempty" jsonschema:"Semver constraint this version satisfies, e.g. \">= 0.2.5\"."`
	Status     string `json:"status" jsonschema:"CREATED or DELETED — the latest status of this version."`
	Author     string `json:"author,omitempty"`
	Timestamp  int64  `json:"timestamp,omitempty" jsonschema:"Unix seconds of the latest status change."`
}

type listVersionsOutput struct {
	Path     string         `json:"path"`
	Variant  string         `json:"variant,omitempty"`
	Versions []versionEntry `json:"versions" jsonschema:"Oldest first."`
}

type listVariantsInput struct {
	Path string `json:"path" jsonschema:"Full config path."`
}

type listVariantsOutput struct {
	Path     string   `json:"path"`
	Variants []string `json:"variants"`
}

type setConfigInput struct {
	Path        string                  `json:"path" jsonschema:"Full config path. Parent folders are created automatically."`
	Content     string                  `json:"content" jsonschema:"The full new source text. Writes replace content wholesale; there is no partial patch."`
	Variant     string                  `json:"variant,omitempty" jsonschema:"Write a variant overlay instead of the base config."`
	Format      string                  `json:"format,omitempty" jsonschema:"json, yaml, toml or raw. Omitted: keep the existing config's format, or infer it from the path extension for a new config."`
	Description string                  `json:"description,omitempty" jsonschema:"Omitted: keep the existing description."`
	Constraint  string                  `json:"constraint,omitempty" jsonschema:"Semver constraint to attach to the new version, e.g. \">= 1.2.0\"."`
	GoTemplate  *bool                   `json:"go_template,omitempty" jsonschema:"Omitted: keep the existing setting."`
	Inherits    *[]service.InheritEntry `json:"inherits,omitempty" jsonschema:"Replace the inheritance list. Omitted: keep the existing list. Pass an empty array to clear it."`

	ExpectedVersion *int64 `json:"expected_version,omitempty" jsonschema:"Latest version you based this edit on (from get_config or list_versions). The write fails instead of clobbering a concurrent change if it no longer matches."`
}

type setConfigOutput struct {
	Path    string `json:"path"`
	Variant string `json:"variant,omitempty"`
	Version int64  `json:"version" jsonschema:"The newly created version number."`
}

type deleteConfigInput struct {
	Path    string `json:"path" jsonschema:"Full config path."`
	Variant string `json:"variant,omitempty" jsonschema:"Delete this variant instead of the base config."`
	Version int64  `json:"version,omitempty" jsonschema:"Mark only this version deleted. Omit (or 0) to delete the config and its entire history."`
}

type deleteFolderInput struct {
	Path string `json:"path" jsonschema:"Folder path. Deletes the folder and everything under it, recursively."`
}

type okOutput struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// registerConfigTools installs the configuration-tree tools. Read tools are
// gated on files.read, write tools on files.write, matching the REST routes
// one for one so an operator's existing permission bundles govern MCP
// without any new vocabulary.
func (h *Handler) registerConfigTools(srv *mcp.Server, scope authScope) {
	addTool(srv, scope, service.CapFilesRead, opRead, &mcp.Tool{
		Name:        "search_configs",
		Title:       "Search configurations",
		Description: "Find configurations by path or by content across the whole tree. Use this first when you do not already know the exact path of a config.",
		Annotations: readOnly,
	}, h.searchConfigs(scope))

	addTool(srv, scope, service.CapFilesRead, opRead, &mcp.Tool{
		Name:        "list_folder",
		Title:       "List folder",
		Description: "List the subfolders and configurations directly inside a folder. Use it to walk the tree one level at a time. Entries your credentials cannot reach are omitted, so what you see is what you can open.",
		Annotations: readOnly,
	}, h.listFolder(scope))

	addTool(srv, scope, service.CapFilesRead, opRead, &mcp.Tool{
		Name:        "get_config",
		Title:       "Get configuration source",
		Description: "Read a configuration's stored source text and metadata, exactly as saved. Use this before editing. For the effective value an application would receive, use get_resolved_config instead.",
		Annotations: readOnly,
	}, h.getConfig(scope))

	addTool(srv, scope, service.CapFilesRead, opRead, &mcp.Tool{
		Name:        "get_resolved_config",
		Title:       "Get resolved configuration",
		Description: "Read a configuration after inheritance, templating and format conversion have been applied — the exact bytes a consuming application receives from the data endpoint. Use this to answer \"what value is service X actually running with\".",
		Annotations: readOnly,
	}, h.getResolvedConfig(scope))

	addTool(srv, scope, service.CapFilesRead, opRead, &mcp.Tool{
		Name:        "list_versions",
		Title:       "List configuration versions",
		Description: "List the version history of a configuration, including who wrote each version, when, and any semver constraint attached to it.",
		Annotations: readOnly,
	}, h.listVersions(scope))

	addTool(srv, scope, service.CapFilesRead, opRead, &mcp.Tool{
		Name:        "list_variants",
		Title:       "List configuration variants",
		Description: "List the environment variant keys defined for a configuration.",
		Annotations: readOnly,
	}, h.listVariants(scope))

	addTool(srv, scope, service.CapFilesWrite, opWrite, &mcp.Tool{
		Name:        "set_config",
		Title:       "Write configuration",
		Description: "Create a configuration or save a new version of an existing one. Content is replaced wholesale, so read the config first and send back the complete new text. Every call creates a new version; nothing is overwritten in place.",
		Annotations: additive,
	}, h.setConfig(scope))

	addTool(srv, scope, service.CapFilesWrite, opDelete, &mcp.Tool{
		Name:        "delete_config",
		Title:       "Delete configuration",
		Description: "Delete a configuration, a single one of its versions, or a variant.",
		Annotations: destructive,
	}, h.deleteConfig(scope))

	addTool(srv, scope, service.CapFilesWrite, opDelete, &mcp.Tool{
		Name:        "delete_folder",
		Title:       "Delete folder",
		Description: "Delete a folder and every configuration underneath it, recursively. This cannot be undone.",
		Annotations: destructive,
	}, h.deleteFolder(scope))
}

func (h *Handler) searchConfigs(scope authScope) func(context.Context, searchConfigsInput) (searchConfigsOutput, error) {
	return func(ctx context.Context, in searchConfigsInput) (searchConfigsOutput, error) {
		if in.Query == "" {
			return searchConfigsOutput{}, fmt.Errorf("query is required")
		}

		limit := in.Limit
		if limit <= 0 {
			limit = defaultSearchLimit
		}
		if limit > maxSearchLimit {
			limit = maxSearchLimit
		}

		// Cancel the walk as soon as the limit is hit — Search keeps
		// producing until the tree is exhausted otherwise, and it reads
		// every file to do it.
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		results := make(chan service.SearchResult, 16)
		go func() {
			_ = h.svc.Search(ctx, service.SearchOptions{
				Query:    in.Query,
				NameOnly: in.Mode == "name",
			}, results)
		}()

		out := searchConfigsOutput{Hits: []searchHit{}}
		for res := range results {
			// Path-scoped grants filter results the same way the SSE
			// search endpoint does: a caller restricted to team-a/**
			// must not learn that team-b/secrets exists.
			if !scope.allowResult(service.CapFilesRead, opRead, res.Path) {
				continue
			}

			if len(out.Hits) >= limit {
				out.Truncated = true
				cancel()

				// Keep draining: Search writes to the channel and only
				// notices cancellation between sends, so abandoning the
				// range here would block its goroutine forever.
				continue
			}

			out.Hits = append(out.Hits, searchHit{Path: res.Path, Type: res.Type})
		}

		return out, nil
	}
}

func (h *Handler) listFolder(scope authScope) func(context.Context, listFolderInput) (listFolderOutput, error) {
	return func(ctx context.Context, in listFolderInput) (listFolderOutput, error) {
		// Ancestor semantics: a caller scoped to team-a/** still needs to
		// be able to list "" and "team-a" to reach what they can read.
		if err := scope.allowAncestorPath(service.CapFilesRead, opRead, in.Path); err != nil {
			return listFolderOutput{}, err
		}

		folder, err := h.svc.Folder(ctx, in.Path)
		if err != nil {
			return listFolderOutput{}, err
		}

		// Filter the children to what this caller can actually reach.
		//
		// The ancestor rule above is what makes navigation possible, but
		// on its own it also hands a caller scoped to team-b/** the name
		// of every top-level folder. The REST folder endpoint accepts
		// that; here it is worth closing, because an agent driving these
		// tools will otherwise spend calls descending into subtrees it
		// will be refused at every step — and because a folder name can
		// itself be the sensitive part ("customers/acme-corp").
		//
		// Subfolders use the ancestor test (they may lead somewhere
		// permitted); files use the exact test (they are the leaf).
		out := listFolderOutput{Path: in.Path, Folders: []string{}, Files: []string{}}

		for _, name := range folder.Folders {
			if scope.allowAncestorPath(service.CapFilesRead, opRead, childPath(in.Path, name)) == nil {
				out.Folders = append(out.Folders, name)
			}
		}

		for _, name := range folder.Files {
			if !scope.allowResult(service.CapFilesRead, opRead, childPath(in.Path, name)) {
				continue
			}

			out.Files = append(out.Files, name)
			if variants, ok := folder.Variants[name]; ok {
				if out.Variants == nil {
					out.Variants = map[string][]string{}
				}
				out.Variants[name] = variants
			}
		}

		return out, nil
	}
}

// childPath joins a folder path with a direct child name. The root folder
// is the empty string, so a plain join would produce a leading slash and
// silently fail every glob match.
func childPath(folder, name string) string {
	if folder == "" {
		return name
	}

	return folder + "/" + name
}

func (h *Handler) getConfig(scope authScope) func(context.Context, getConfigInput) (configOutput, error) {
	return func(ctx context.Context, in getConfigInput) (configOutput, error) {
		if in.Path == "" {
			return configOutput{}, fmt.Errorf("path is required")
		}
		if err := scope.allowPath(service.CapFilesRead, opRead, in.Path); err != nil {
			return configOutput{}, err
		}

		var (
			file *service.File
			err  error
		)
		if in.Variant != "" {
			file, err = h.svc.VariantByVersion(ctx, in.Path, in.Variant, in.Version)
		} else {
			file, err = h.svc.FileByVersion(ctx, in.Path, in.Version)
		}
		if err != nil {
			return configOutput{}, err
		}

		return configOutput{
			Path:        in.Path,
			Variant:     in.Variant,
			Content:     string(file.Data),
			Format:      file.Meta.Format,
			Description: file.Meta.Description,
			GoTemplate:  file.Meta.GoTemplate,
			Inherits:    file.Meta.Inherits,
		}, nil
	}
}

func (h *Handler) getResolvedConfig(scope authScope) func(context.Context, getResolvedConfigInput) (getResolvedConfigOutput, error) {
	return func(ctx context.Context, in getResolvedConfigInput) (getResolvedConfigOutput, error) {
		if in.Path == "" {
			return getResolvedConfigOutput{}, fmt.Errorf("path is required")
		}
		if err := scope.allowPath(service.CapFilesRead, opRead, in.Path); err != nil {
			return getResolvedConfigOutput{}, err
		}

		result, err := h.svc.GetData(ctx, in.Path, in.Version, in.Variant)
		if err != nil {
			return getResolvedConfigOutput{}, err
		}
		if result.Error != "" {
			return getResolvedConfigOutput{}, fmt.Errorf("configuration has errors: %s", result.Error)
		}

		data, format := result.Data, result.Format
		if in.Format != "" && in.Format != result.Format {
			converted, err := service.ConvertFormat(result.Data, result.Format, in.Format)
			if err != nil {
				return getResolvedConfigOutput{}, fmt.Errorf("converting from %s to %s: %w", result.Format, in.Format, err)
			}
			data, format = converted, in.Format
		}

		return getResolvedConfigOutput{
			Path:    in.Path,
			Variant: in.Variant,
			Content: string(data),
			Format:  format,
		}, nil
	}
}

func (h *Handler) listVersions(scope authScope) func(context.Context, listVersionsInput) (listVersionsOutput, error) {
	return func(ctx context.Context, in listVersionsInput) (listVersionsOutput, error) {
		if in.Path == "" {
			return listVersionsOutput{}, fmt.Errorf("path is required")
		}
		if err := scope.allowPath(service.CapFilesRead, opRead, in.Path); err != nil {
			return listVersionsOutput{}, err
		}

		var (
			versions service.FileVersions
			err      error
		)
		if in.Variant != "" {
			versions, err = h.svc.VariantVersions(ctx, in.Path, in.Variant)
		} else {
			versions, err = h.svc.FileVersionsList(ctx, in.Path)
		}
		if err != nil {
			return listVersionsOutput{}, err
		}

		out := listVersionsOutput{Path: in.Path, Variant: in.Variant, Versions: make([]versionEntry, 0, len(versions))}
		for _, v := range versions {
			entry := versionEntry{Version: v.Version, Constraint: v.Constraint}
			// Status is an append-only audit log; the last entry is the
			// current state of that version.
			if n := len(v.Status); n > 0 {
				last := v.Status[n-1]
				entry.Status = string(last.Status)
				entry.Author = last.Author
				entry.Timestamp = last.Timestamp
			}
			out.Versions = append(out.Versions, entry)
		}

		return out, nil
	}
}

func (h *Handler) listVariants(scope authScope) func(context.Context, listVariantsInput) (listVariantsOutput, error) {
	return func(ctx context.Context, in listVariantsInput) (listVariantsOutput, error) {
		if in.Path == "" {
			return listVariantsOutput{}, fmt.Errorf("path is required")
		}
		if err := scope.allowPath(service.CapFilesRead, opRead, in.Path); err != nil {
			return listVariantsOutput{}, err
		}

		variants, err := h.svc.ListVariants(ctx, in.Path)
		if err != nil {
			return listVariantsOutput{}, err
		}

		return listVariantsOutput{Path: in.Path, Variants: variants}, nil
	}
}

func (h *Handler) setConfig(scope authScope) func(context.Context, setConfigInput) (setConfigOutput, error) {
	return func(ctx context.Context, in setConfigInput) (setConfigOutput, error) {
		if in.Path == "" {
			return setConfigOutput{}, fmt.Errorf("path is required")
		}
		if err := scope.allowPath(service.CapFilesWrite, opWrite, in.Path); err != nil {
			return setConfigOutput{}, err
		}

		meta := h.mergeMeta(ctx, in)

		file := &service.File{Meta: meta, Data: []byte(in.Content)}

		var (
			version int64
			err     error
		)
		if in.Variant != "" {
			version, err = h.svc.SetVariant(ctx, in.Path, in.Variant, file, in.ExpectedVersion, in.Constraint)
		} else {
			version, err = h.svc.SetFile(ctx, in.Path, file, in.ExpectedVersion, in.Constraint)
		}
		if err != nil {
			return setConfigOutput{}, err
		}

		return setConfigOutput{Path: in.Path, Variant: in.Variant, Version: version}, nil
	}
}

// mergeMeta builds the metadata for a write.
//
// A write replaces the whole file record, so an agent that only wants to
// change the content would silently drop the description, format and
// inheritance list if we took the input at face value. Instead the current
// version's metadata is the baseline and only explicitly-supplied fields
// override it. Format falls back to the path extension for brand-new
// configs so the common case needs no extra argument.
func (h *Handler) mergeMeta(ctx context.Context, in setConfigInput) service.FileMeta {
	var meta service.FileMeta

	var (
		current *service.File
		err     error
	)
	if in.Variant != "" {
		current, err = h.svc.Variant(ctx, in.Path, in.Variant, 0)
	} else {
		current, err = h.svc.File(ctx, in.Path, 0)
	}
	if err == nil && current != nil {
		meta = current.Meta
	}

	if in.Format != "" {
		meta.Format = in.Format
	}
	if meta.Format == "" {
		meta.Format = formatFromPath(in.Path)
	}
	if in.Description != "" {
		meta.Description = in.Description
	}
	if in.GoTemplate != nil {
		meta.GoTemplate = *in.GoTemplate
	}
	if in.Inherits != nil {
		meta.Inherits = *in.Inherits
	}

	return meta
}

// formatFromPath guesses a config format from the file extension. Anything
// unrecognized becomes "raw", which pika serves verbatim without trying to
// parse or merge it — the safe default for a file we cannot classify.
func formatFromPath(path string) string {
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	default:
		return "raw"
	}
}

func (h *Handler) deleteConfig(scope authScope) func(context.Context, deleteConfigInput) (okOutput, error) {
	return func(ctx context.Context, in deleteConfigInput) (okOutput, error) {
		if in.Path == "" {
			return okOutput{}, fmt.Errorf("path is required")
		}
		if err := scope.allowPath(service.CapFilesWrite, opDelete, in.Path); err != nil {
			return okOutput{}, err
		}

		var err error
		if in.Variant != "" {
			err = h.svc.DeleteVariant(ctx, in.Path, in.Variant, in.Version)
		} else {
			err = h.svc.DeleteFile(ctx, in.Path, in.Version)
		}
		if err != nil {
			return okOutput{}, err
		}

		msg := fmt.Sprintf("deleted %s", in.Path)
		if in.Version > 0 {
			msg = fmt.Sprintf("deleted version %d of %s", in.Version, in.Path)
		}

		return okOutput{OK: true, Message: msg}, nil
	}
}

func (h *Handler) deleteFolder(scope authScope) func(context.Context, deleteFolderInput) (okOutput, error) {
	return func(ctx context.Context, in deleteFolderInput) (okOutput, error) {
		if in.Path == "" {
			return okOutput{}, fmt.Errorf("path is required; refusing to delete the root folder")
		}
		if err := scope.allowPath(service.CapFilesWrite, opDelete, in.Path); err != nil {
			return okOutput{}, err
		}

		if err := h.svc.DeleteFolder(ctx, in.Path); err != nil {
			return okOutput{}, err
		}

		return okOutput{OK: true, Message: fmt.Sprintf("deleted folder %s and its contents", in.Path)}, nil
	}
}
