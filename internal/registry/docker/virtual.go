package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"

	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/virtualbase"
	"github.com/rakunlabs/pika/internal/service"
)

// Virtual aggregates a list of sibling Docker repos under a single
// URL. Reads dispatch first-hit-wins across members; writes are
// rejected. Useful operator pattern: a single "docker" virtual
// endpoint that fronts both an internal Local (for in-house images)
// and a Remote Docker Hub mirror (for upstream pulls).
//
// Lookup behaviour by operation:
//
//   - Version probe: always 200 (we're the proxy; we're online).
//   - Manifest GET/HEAD by tag or digest: try each member in order,
//     return the first 2xx.
//   - Blob GET/HEAD: same — first member with the blob wins.
//   - Tags list: union across members, sorted.
//   - Catalog: union across members, sorted.
//   - Referrers: union of every member's referrers index for the
//     subject digest.
//
// Token endpoint and uploads are rejected: virtual repos are
// read-only and don't issue their own tokens (clients should use
// the pika-wide bearer that authenticates the entry handler).
//
// Shared shell (members + resolver + first-hit + header copy +
// PackageDetail delegation) is in internal/registry/virtualbase.
type Virtual struct {
	*virtualbase.Base
}

// NewVirtualFactory returns the Factory for ("docker", "virtual").
func NewVirtualFactory(resolver virtualbase.Resolver) registry.Factory {
	return func(_ context.Context, _ registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		if len(r.Members) == 0 {
			return nil, fmt.Errorf("docker/virtual %s/%s: members required", ns, r.Name)
		}
		return &Virtual{
			Base: virtualbase.New(ns, r.Name, r.Members, resolver),
		}, nil
	}
}

// Type returns the protocol identifier. Kind / Namespace / Name /
// Close come from the embedded Base.
func (v *Virtual) Type() string { return service.RegistryTypeDocker }

// PackageDetail delegates to the first member that has the image.
func (v *Virtual) PackageDetail(ctx context.Context, name string) (*registry.PackageDetail, error) {
	return v.DelegatePackageDetail(ctx, name)
}

// ServeHTTP dispatches.
func (v *Virtual) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "DENIED", "virtual registry is read-only")
		return
	}
	req, ok := classify(r.Method, r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "UNSUPPORTED", "unrecognised docker route")
		return
	}
	switch req.Op {
	case opVersionProbe:
		w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
		w.WriteHeader(http.StatusOK)
	case opCatalog:
		v.serveCatalogUnion(w, r)
	case opTagsList:
		v.serveTagsUnion(w, r, req.Name)
	case opReferrers:
		v.serveReferrersUnion(w, r, req)
	case opManifest, opBlob:
		if !v.ServeFirstHit(w, r) {
			writeError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "no member served the request")
		}
	default:
		writeError(w, http.StatusMethodNotAllowed, "DENIED", "operation not supported on virtual")
	}
}

// serveCatalogUnion merges every member's catalog and returns a
// sorted deduped list.
func (v *Virtual) serveCatalogUnion(w http.ResponseWriter, r *http.Request) {
	seen := map[string]struct{}{}
	v.ForEachMember(func(mem registry.Registry) bool {
		rec := httptest.NewRecorder()
		mem.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			return false
		}
		var body struct {
			Repositories []string `json:"repositories"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			return false
		}
		for _, name := range body.Repositories {
			seen[name] = struct{}{}
		}
		return false
	})
	repos := make([]string, 0, len(seen))
	for n := range seen {
		repos = append(repos, n)
	}
	sort.Strings(repos)
	body, _ := json.Marshal(map[string]any{"repositories": repos})
	writeOK(w, "application/json", body)
}

// serveTagsUnion merges every member's tag list for a name.
func (v *Virtual) serveTagsUnion(w http.ResponseWriter, r *http.Request, name string) {
	seen := map[string]struct{}{}
	v.ForEachMember(func(mem registry.Registry) bool {
		rec := httptest.NewRecorder()
		mem.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			return false
		}
		var body struct {
			Name string   `json:"name"`
			Tags []string `json:"tags"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			return false
		}
		for _, t := range body.Tags {
			seen[t] = struct{}{}
		}
		return false
	})
	tags := make([]string, 0, len(seen))
	for t := range seen {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	body, _ := json.Marshal(map[string]any{"name": name, "tags": tags})
	writeOK(w, "application/json", body)
}

// serveReferrersUnion merges every member's referrers index for a
// subject digest. Dedupes by referrer digest.
func (v *Virtual) serveReferrersUnion(w http.ResponseWriter, r *http.Request, _ parsedRequest) {
	seen := map[string]manifestDescriptor{}
	v.ForEachMember(func(mem registry.Registry) bool {
		rec := httptest.NewRecorder()
		mem.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			return false
		}
		var idx ociImageIndex
		if err := json.Unmarshal(rec.Body.Bytes(), &idx); err != nil {
			return false
		}
		for _, m := range idx.Manifests {
			if _, dup := seen[m.Digest]; !dup {
				seen[m.Digest] = m
			}
		}
		return false
	})
	idx := newEmptyReferrersIndex()
	for _, m := range seen {
		idx.Manifests = append(idx.Manifests, m)
	}
	// Stable order for deterministic responses.
	sort.Slice(idx.Manifests, func(i, j int) bool {
		return idx.Manifests[i].Digest < idx.Manifests[j].Digest
	})
	// artifactType filter passthrough.
	if filter := r.URL.Query().Get("artifactType"); filter != "" {
		filtered := idx.Manifests[:0]
		for _, m := range idx.Manifests {
			if m.ArtifactType == filter {
				filtered = append(filtered, m)
			}
		}
		idx.Manifests = filtered
		w.Header().Set("OCI-Filters-Applied", "artifactType")
	}
	body, _ := json.Marshal(idx)
	writeOK(w, "application/vnd.oci.image.index.v1+json", body)
}
