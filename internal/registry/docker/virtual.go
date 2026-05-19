package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"

	"github.com/rakunlabs/pika/internal/registry"
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
type Virtual struct {
	namespace   string
	name        string
	memberNames []string
	resolve     func(namespace, repo string) (registry.Registry, bool)
}

// virtualResolver is the narrow surface Virtual needs from the
// manager.
type virtualResolver interface {
	Lookup(namespace, repo string) (registry.Registry, bool)
}

// NewVirtualFactory returns the Factory for ("docker", "virtual").
func NewVirtualFactory(resolver virtualResolver) registry.Factory {
	return func(_ context.Context, _ registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		if len(r.Members) == 0 {
			return nil, fmt.Errorf("docker/virtual %s/%s: members required", ns, r.Name)
		}
		members := make([]string, len(r.Members))
		copy(members, r.Members)
		return &Virtual{
			namespace:   ns,
			name:        r.Name,
			memberNames: members,
			resolve: func(namespace, repo string) (registry.Registry, bool) {
				return resolver.Lookup(namespace, repo)
			},
		}, nil
	}
}

func (v *Virtual) Namespace() string { return v.namespace }
func (v *Virtual) Name() string      { return v.name }
func (v *Virtual) Type() string      { return service.RegistryTypeDocker }
func (v *Virtual) Kind() string      { return service.RegistryKindVirtual }
func (v *Virtual) Close() error      { return nil }

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
		v.serveFirstHit(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "DENIED", "operation not supported on virtual")
	}
}

// serveFirstHit forwards to each member; first 2xx wins.
func (v *Virtual) serveFirstHit(w http.ResponseWriter, r *http.Request) {
	for _, memberName := range v.memberNames {
		mem, ok := v.resolve(v.namespace, memberName)
		if !ok {
			continue
		}
		rec := httptest.NewRecorder()
		mem.ServeHTTP(rec, r)
		if rec.Code >= 200 && rec.Code < 300 {
			for k, vs := range rec.Header() {
				for _, val := range vs {
					w.Header().Add(k, val)
				}
			}
			w.WriteHeader(rec.Code)
			_, _ = w.Write(rec.Body.Bytes())
			return
		}
	}
	writeError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "no member served the request")
}

// serveCatalogUnion merges every member's catalog and returns a
// sorted deduped list.
func (v *Virtual) serveCatalogUnion(w http.ResponseWriter, r *http.Request) {
	seen := map[string]struct{}{}
	for _, memberName := range v.memberNames {
		mem, ok := v.resolve(v.namespace, memberName)
		if !ok {
			continue
		}
		rec := httptest.NewRecorder()
		mem.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			continue
		}
		var body struct {
			Repositories []string `json:"repositories"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			continue
		}
		for _, name := range body.Repositories {
			seen[name] = struct{}{}
		}
	}
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
	for _, memberName := range v.memberNames {
		mem, ok := v.resolve(v.namespace, memberName)
		if !ok {
			continue
		}
		rec := httptest.NewRecorder()
		mem.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			continue
		}
		var body struct {
			Name string   `json:"name"`
			Tags []string `json:"tags"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			continue
		}
		for _, t := range body.Tags {
			seen[t] = struct{}{}
		}
	}
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
func (v *Virtual) serveReferrersUnion(w http.ResponseWriter, r *http.Request, req parsedRequest) {
	seen := map[string]manifestDescriptor{}
	for _, memberName := range v.memberNames {
		mem, ok := v.resolve(v.namespace, memberName)
		if !ok {
			continue
		}
		rec := httptest.NewRecorder()
		mem.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			continue
		}
		var idx ociImageIndex
		if err := json.Unmarshal(rec.Body.Bytes(), &idx); err != nil {
			continue
		}
		for _, m := range idx.Manifests {
			if _, dup := seen[m.Digest]; !dup {
				seen[m.Digest] = m
			}
		}
	}
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

// _ kept to silence unused-import linters across optional refactors.
var _ = strings.Join
