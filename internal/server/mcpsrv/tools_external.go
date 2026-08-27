package mcpsrv

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/service"
)

// defaultExternalSearchLimit mirrors the REST default. External search
// issues one network round-trip per key in "all" mode, so the cap is a
// latency guard as much as a context-window guard.
const defaultExternalSearchLimit = 50

// maxExternalSearchLimit is the service-layer default and doubles as our
// hard ceiling.
const maxExternalSearchLimit = 200

type listExternalResourcesInput struct{}

type externalResource struct {
	Name        string `json:"name" jsonschema:"Resource name to pass to the other external_* tools."`
	Kind        string `json:"kind" jsonschema:"Backend type: vault, consul, etcd, kubernetes, aws, gcp, azure or http."`
	CanRead     bool   `json:"can_read"`
	CanList     bool   `json:"can_list"`
	CanWrite    bool   `json:"can_write"`
	CanDelete   bool   `json:"can_delete"`
	CanVersions bool   `json:"can_versions"`
}

type listExternalResourcesOutput struct {
	Resources []externalResource `json:"resources"`
}

type listExternalPathsInput struct {
	Resource string `json:"resource" jsonschema:"External resource name from list_external_resources."`
	Prefix   string `json:"prefix,omitempty" jsonschema:"Path prefix to list under. Omit for the root. Backend-specific: a trailing slash usually denotes a folder."`
}

type listExternalPathsOutput struct {
	Resource string   `json:"resource"`
	Prefix   string   `json:"prefix,omitempty"`
	Paths    []string `json:"paths" jsonschema:"Entries directly under the prefix. Empty for backends without list support, such as http."`
}

type searchExternalInput struct {
	Resource string `json:"resource" jsonschema:"External resource name."`
	Query    string `json:"query" jsonschema:"Case-insensitive substring to look for."`
	Mode     string `json:"mode,omitempty" jsonschema:"\"name\" matches only path names (fast, one list per folder). \"all\" (default) also reads and scans every value, which issues one request per key."`
	Limit    int    `json:"limit,omitempty" jsonschema:"Maximum number of hits. Default 50, maximum 200."`
}

type externalSearchHit struct {
	Path    string `json:"path"`
	Type    string `json:"type" jsonschema:"\"name\" when the path matched, \"content\" when the stored value matched."`
	Snippet string `json:"snippet,omitempty" jsonschema:"Short excerpt of the matched value. Present for content hits only."`
}

type searchExternalOutput struct {
	Resource string              `json:"resource"`
	Hits     []externalSearchHit `json:"hits"`
}

type readExternalInput struct {
	Resource string `json:"resource" jsonschema:"External resource name."`
	Path     string `json:"path" jsonschema:"Resource-specific path of the entry to read."`
	Version  string `json:"version,omitempty" jsonschema:"Historical version to read. Only backends reporting can_versions support this; Vault KV v2 is the usual case."`
}

type readExternalOutput struct {
	Resource    string         `json:"resource"`
	Path        string         `json:"path"`
	Version     string         `json:"version,omitempty"`
	Data        map[string]any `json:"data,omitempty" jsonschema:"Structured key/value payload, for backends that store one."`
	Raw         string         `json:"raw,omitempty" jsonschema:"Unstructured body, for backends that return an opaque document."`
	ContentType string         `json:"content_type,omitempty"`
}

type writeExternalInput struct {
	Resource string         `json:"resource" jsonschema:"External resource name. It must report can_write."`
	Path     string         `json:"path" jsonschema:"Resource-specific path to write."`
	Data     map[string]any `json:"data" jsonschema:"Key/value payload. This replaces the whole entry; read it first and send back the complete map to avoid dropping keys."`
}

type deleteExternalInput struct {
	Resource string `json:"resource" jsonschema:"External resource name. It must report can_delete."`
	Path     string `json:"path" jsonschema:"Resource-specific path to delete."`
}

// registerExternalTools installs the external-backend tools, gated on
// external.read / external.write exactly like the REST namespace.
//
// Note the deliberate asymmetry with configs: external entries are secrets
// (Vault, cloud secret managers), so reading one returns its actual value
// into the model's context. That is the point of the tool, but it is also
// why external.read is a separate capability an operator grants
// deliberately rather than something bundled with files.read.
func (h *Handler) registerExternalTools(srv *mcp.Server, scope authScope) {
	addTool(srv, scope, service.CapExternalRead, opRead, &mcp.Tool{
		Name:        "list_external_resources",
		Title:       "List external resources",
		Description: "List the external backends configured on this server (Vault, Consul, etcd, Kubernetes, AWS, GCP, Azure, HTTP) and what each one supports. Call this first: every other external_* tool needs a resource name from here, and the capability flags tell you whether a write or a version lookup will work at all.",
		Annotations: readOnly,
	}, h.listExternalResources())

	addTool(srv, scope, service.CapExternalRead, opRead, &mcp.Tool{
		Name:        "list_external_paths",
		Title:       "List external paths",
		Description: "List entries under a prefix in an external resource. Use it to walk a backend one level at a time.",
		Annotations: readOnly,
	}, h.listExternalPaths())

	addTool(srv, scope, service.CapExternalRead, opRead, &mcp.Tool{
		Name:        "search_external",
		Title:       "Search external resource",
		Description: "Find entries in an external resource by path or by stored value. Prefer mode \"name\" on large backends — mode \"all\" reads every key it walks.",
		Annotations: readOnly,
	}, h.searchExternal())

	addTool(srv, scope, service.CapExternalRead, opRead, &mcp.Tool{
		Name:        "read_external",
		Title:       "Read external entry",
		Description: "Read a single entry from an external resource, optionally at a historical version. The returned value may be a secret.",
		Annotations: readOnly,
	}, h.readExternal())

	addTool(srv, scope, service.CapExternalWrite, opWrite, &mcp.Tool{
		Name:        "write_external",
		Title:       "Write external entry",
		Description: "Create or replace an entry in an external resource. The payload replaces the entry wholesale, so read it first and send back every key you intend to keep.",
		Annotations: destructive,
	}, h.writeExternal())

	addTool(srv, scope, service.CapExternalWrite, opDelete, &mcp.Tool{
		Name:        "delete_external",
		Title:       "Delete external entry",
		Description: "Delete an entry from an external resource.",
		Annotations: destructive,
	}, h.deleteExternal())
}

func (h *Handler) listExternalResources() func(context.Context, listExternalResourcesInput) (listExternalResourcesOutput, error) {
	return func(ctx context.Context, _ listExternalResourcesInput) (listExternalResourcesOutput, error) {
		resources, err := h.svc.ListExternalResources(ctx)
		if err != nil {
			return listExternalResourcesOutput{}, err
		}

		out := listExternalResourcesOutput{Resources: make([]externalResource, 0, len(resources))}
		for _, r := range resources {
			out.Resources = append(out.Resources, externalResource{
				Name:        r.Name,
				Kind:        r.Kind,
				CanRead:     r.Capabilities.CanRead,
				CanList:     r.Capabilities.CanList,
				CanWrite:    r.Capabilities.CanWrite,
				CanDelete:   r.Capabilities.CanDelete,
				CanVersions: r.Capabilities.CanVersions,
			})
		}

		return out, nil
	}
}

func (h *Handler) listExternalPaths() func(context.Context, listExternalPathsInput) (listExternalPathsOutput, error) {
	return func(ctx context.Context, in listExternalPathsInput) (listExternalPathsOutput, error) {
		if in.Resource == "" {
			return listExternalPathsOutput{}, fmt.Errorf("resource is required")
		}

		paths, err := h.svc.ListExternalPaths(ctx, in.Resource, in.Prefix)
		if err != nil {
			return listExternalPathsOutput{}, translateNotSupported(in.Resource, "listing", err)
		}
		if paths == nil {
			paths = []string{}
		}

		return listExternalPathsOutput{Resource: in.Resource, Prefix: in.Prefix, Paths: paths}, nil
	}
}

func (h *Handler) searchExternal() func(context.Context, searchExternalInput) (searchExternalOutput, error) {
	return func(ctx context.Context, in searchExternalInput) (searchExternalOutput, error) {
		if in.Resource == "" {
			return searchExternalOutput{}, fmt.Errorf("resource is required")
		}
		if in.Query == "" {
			return searchExternalOutput{}, fmt.Errorf("query is required")
		}

		limit := in.Limit
		if limit <= 0 {
			limit = defaultExternalSearchLimit
		}
		if limit > maxExternalSearchLimit {
			limit = maxExternalSearchLimit
		}

		mode := service.ExternalSearchModeAll
		if in.Mode == string(service.ExternalSearchModeName) {
			mode = service.ExternalSearchModeName
		}

		hits, err := h.svc.SearchExternal(ctx, in.Resource, in.Query, mode, limit)
		if err != nil {
			return searchExternalOutput{}, translateNotSupported(in.Resource, "searching", err)
		}

		out := searchExternalOutput{Resource: in.Resource, Hits: make([]externalSearchHit, 0, len(hits))}
		for _, hit := range hits {
			out.Hits = append(out.Hits, externalSearchHit{Path: hit.Path, Type: hit.Type, Snippet: hit.Snippet})
		}

		return out, nil
	}
}

func (h *Handler) readExternal() func(context.Context, readExternalInput) (readExternalOutput, error) {
	return func(ctx context.Context, in readExternalInput) (readExternalOutput, error) {
		if in.Resource == "" || in.Path == "" {
			return readExternalOutput{}, fmt.Errorf("resource and path are required")
		}

		var (
			entry *external.Entry
			err   error
		)
		if in.Version != "" {
			entry, err = h.svc.ReadExternalVersion(ctx, in.Resource, in.Path, in.Version)
		} else {
			entry, err = h.svc.ReadExternal(ctx, in.Resource, in.Path)
		}
		if err != nil {
			return readExternalOutput{}, translateNotSupported(in.Resource, "reading", err)
		}
		if entry == nil {
			return readExternalOutput{}, fmt.Errorf("external resource %q has no entry at %q", in.Resource, in.Path)
		}

		return readExternalOutput{
			Resource:    in.Resource,
			Path:        in.Path,
			Version:     entry.Version,
			Data:        entry.Data,
			Raw:         string(entry.Raw),
			ContentType: entry.ContentType,
		}, nil
	}
}

func (h *Handler) writeExternal() func(context.Context, writeExternalInput) (okOutput, error) {
	return func(ctx context.Context, in writeExternalInput) (okOutput, error) {
		if in.Resource == "" || in.Path == "" {
			return okOutput{}, fmt.Errorf("resource and path are required")
		}
		if len(in.Data) == 0 {
			return okOutput{}, fmt.Errorf("data is required; refusing to write an empty entry")
		}

		if err := h.svc.WriteExternal(ctx, in.Resource, in.Path, in.Data); err != nil {
			return okOutput{}, translateNotSupported(in.Resource, "writing", err)
		}

		return okOutput{OK: true, Message: fmt.Sprintf("wrote %s on %s", in.Path, in.Resource)}, nil
	}
}

func (h *Handler) deleteExternal() func(context.Context, deleteExternalInput) (okOutput, error) {
	return func(ctx context.Context, in deleteExternalInput) (okOutput, error) {
		if in.Resource == "" || in.Path == "" {
			return okOutput{}, fmt.Errorf("resource and path are required")
		}

		if err := h.svc.DeleteExternal(ctx, in.Resource, in.Path); err != nil {
			return okOutput{}, translateNotSupported(in.Resource, "deleting", err)
		}

		return okOutput{OK: true, Message: fmt.Sprintf("deleted %s on %s", in.Path, in.Resource)}, nil
	}
}

// translateNotSupported turns the provider-capability sentinel into a
// message that tells the model what to do next instead of leaving it to
// retry a call the backend can never satisfy.
func translateNotSupported(resource, op string, err error) error {
	if errors.Is(err, external.ErrNotSupported) {
		return fmt.Errorf("external resource %q does not support %s; check its capability flags with list_external_resources", resource, op)
	}

	return err
}
