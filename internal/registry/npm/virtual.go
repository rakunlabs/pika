package npm

import (
	"bytes"
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

// Virtual aggregates a list of sibling NPM repositories under one
// URL. Packument requests merge versions from every member; tarball
// requests use first-hit-wins. Writes are rejected.
//
// Merge semantics for packument
//
// Each member's packument is fetched, the versions maps are unioned,
// dist-tags are merged with the first member's value winning on
// conflict, and the top-level fields (description, readme, etc.)
// are taken from the first member that supplies them. This matches
// Artifactory's behaviour: deeper members augment, shallower members
// shadow.
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

// NewVirtualFactory returns the Factory for ("npm", "virtual").
func NewVirtualFactory(resolver virtualResolver) registry.Factory {
	return func(_ context.Context, _ registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		if len(r.Members) == 0 {
			return nil, fmt.Errorf("npm/virtual %s/%s: members required", ns, r.Name)
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
func (v *Virtual) Type() string      { return service.RegistryTypeNPM }
func (v *Virtual) Kind() string      { return service.RegistryKindVirtual }
func (v *Virtual) Close() error      { return nil }

// ServeHTTP dispatches.
func (v *Virtual) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "virtual registries are read-only")
		return
	}
	req := classify(r.Method, r.URL.Path)
	switch req.Op {
	case "packument":
		v.servePackumentUnion(w, r, req.Pkg)
	case "tarball", "dist-tags":
		v.serveFirstHit(w, r)
	case "search":
		v.serveSearchUnion(w, r)
	case "whoami":
		serveWhoami(w, r)
	default:
		writeNotFound(w, "unrecognised npm route on virtual repo")
	}
}

// servePackumentUnion merges packuments from every member.
func (v *Virtual) servePackumentUnion(w http.ResponseWriter, r *http.Request, name string) {
	merged := map[string]any{
		"name":      name,
		"versions":  map[string]any{},
		"dist-tags": map[string]any{},
	}
	hit := false
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
		var memPkg map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &memPkg); err != nil {
			continue
		}
		hit = true

		// Merge versions.
		if memVersions, ok := memPkg["versions"].(map[string]any); ok {
			mergedVersions := merged["versions"].(map[string]any)
			for ver, meta := range memVersions {
				if _, exists := mergedVersions[ver]; !exists {
					mergedVersions[ver] = meta
				}
			}
		}
		// Merge dist-tags (first-member-wins).
		if memTags, ok := memPkg["dist-tags"].(map[string]any); ok {
			mergedTags := merged["dist-tags"].(map[string]any)
			for tag, ver := range memTags {
				if _, exists := mergedTags[tag]; !exists {
					mergedTags[tag] = ver
				}
			}
		}
		// First-member-wins on top-level fields.
		for _, k := range []string{"description", "readme", "maintainers", "repository", "_id", "_rev"} {
			if _, has := merged[k]; has {
				continue
			}
			if mv, ok := memPkg[k]; ok {
				merged[k] = mv
			}
		}
	}
	if !hit {
		writeNotFound(w, name+": no member served the package")
		return
	}
	body, err := json.Marshal(merged)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	_, _ = w.Write(body)
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
			copyResponseHeaders(w.Header(), rec.Header())
			w.WriteHeader(rec.Code)
			_, _ = w.Write(rec.Body.Bytes())
			return
		}
	}
	writeNotFound(w, "no member served the request")
}

// serveSearchUnion is the search-result union across members. The
// shape mirrors a Local member's search response.
func (v *Virtual) serveSearchUnion(w http.ResponseWriter, r *http.Request) {
	seenNames := map[string]struct{}{}
	type searchObject struct {
		Package map[string]any `json:"package"`
	}
	results := []searchObject{}

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
		var resp struct {
			Objects []searchObject `json:"objects"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			continue
		}
		for _, obj := range resp.Objects {
			name, _ := obj.Package["name"].(string)
			if name == "" {
				continue
			}
			if _, dup := seenNames[name]; dup {
				continue
			}
			seenNames[name] = struct{}{}
			results = append(results, obj)
		}
	}

	sort.Slice(results, func(i, j int) bool {
		a, _ := results[i].Package["name"].(string)
		b, _ := results[j].Package["name"].(string)
		return a < b
	})

	writeJSON(w, struct {
		Objects []searchObject `json:"objects"`
		Total   int            `json:"total"`
	}{
		Objects: results,
		Total:   len(results),
	})
}

func copyResponseHeaders(dst, src http.Header) {
	for k, vs := range src {
		switch strings.ToLower(k) {
		case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
			"te", "trailer", "transfer-encoding", "upgrade":
			continue
		}
		for _, val := range vs {
			dst.Add(k, val)
		}
	}
}

var _ = bytes.NewReader
