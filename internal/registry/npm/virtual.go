package npm

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
//
// Shared shell (members + resolver + first-hit + header copy +
// PackageDetail delegation) is in internal/registry/virtualbase.
type Virtual struct {
	*virtualbase.Base
}

// NewVirtualFactory returns the Factory for ("npm", "virtual").
func NewVirtualFactory(resolver virtualbase.Resolver) registry.Factory {
	return func(_ context.Context, _ registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		if len(r.Members) == 0 {
			return nil, fmt.Errorf("npm/virtual %s/%s: members required", ns, r.Name)
		}
		return &Virtual{
			Base: virtualbase.New(ns, r.Name, r.Members, resolver),
		}, nil
	}
}

// Type returns the protocol identifier. Kind / Namespace / Name /
// Close come from the embedded Base.
func (v *Virtual) Type() string { return service.RegistryTypeNPM }

// PackageDetail returns the first member that has the package. We
// don't merge across members for the detail view because dependency
// / metadata shadowing across registries is rarely what an operator
// wants to see in the UI — the data-plane packument merge below is
// the right place for that.
func (v *Virtual) PackageDetail(ctx context.Context, name string) (*registry.PackageDetail, error) {
	return v.DelegatePackageDetail(ctx, name)
}

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
		if !v.ServeFirstHit(w, r) {
			writeNotFound(w, "no member served the request")
		}
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
	v.ForEachMember(func(mem registry.Registry) bool {
		rec := httptest.NewRecorder()
		mem.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			return false
		}
		var memPkg map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &memPkg); err != nil {
			return false
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
		return false // keep iterating, this is a union
	})
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

// serveSearchUnion is the search-result union across members. The
// shape mirrors a Local member's search response.
func (v *Virtual) serveSearchUnion(w http.ResponseWriter, r *http.Request) {
	seenNames := map[string]struct{}{}
	type searchObject struct {
		Package map[string]any `json:"package"`
	}
	results := []searchObject{}

	v.ForEachMember(func(mem registry.Registry) bool {
		rec := httptest.NewRecorder()
		mem.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			return false
		}
		var resp struct {
			Objects []searchObject `json:"objects"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			return false
		}
		for _, obj := range resp.Objects {
			n, _ := obj.Package["name"].(string)
			if n == "" {
				continue
			}
			if _, dup := seenNames[n]; dup {
				continue
			}
			seenNames[n] = struct{}{}
			results = append(results, obj)
		}
		return false // keep iterating, this is a union
	})

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
