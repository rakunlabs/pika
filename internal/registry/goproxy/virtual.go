package goproxy

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sort"

	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/virtualbase"
	"github.com/rakunlabs/pika/internal/service"
)

// Virtual aggregates several Local + Remote goproxy registries
// behind one URL.
//
// Routing
//
// List endpoints (`@v/list`) union every member's response so
// clients see the full set of available versions. Detail / content
// endpoints (`@latest`, `.info`, `.mod`, `.zip`, `@latest`) use
// first-hit-wins — members are tried in the configured order and
// the first 2xx response is returned.
//
// Writes
//
// Virtual repos reject writes. Clients that want to publish must
// address a concrete Local member by name. DefaultLocal (when set)
// is a UI hint, not an enforced redirect.
//
// Shared shell (members + resolver + first-hit + header copy +
// PackageDetail delegation) is in internal/registry/virtualbase.
// This file carries the goproxy-specific URL parsing and the
// list-union semantic.
type Virtual struct {
	*virtualbase.Base
}

// NewVirtualFactory returns the Factory for ("go", "virtual") repos.
// The factory closes over the manager so each new Virtual instance
// can look up its members lazily.
func NewVirtualFactory(resolver virtualbase.Resolver) registry.Factory {
	return func(_ context.Context, _ registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		if len(r.Members) == 0 {
			return nil, fmt.Errorf("goproxy/virtual %s/%s: members required", ns, r.Name)
		}
		return &Virtual{
			Base: virtualbase.New(ns, r.Name, r.Members, resolver),
		}, nil
	}
}

// Type returns the protocol identifier. Kind / Namespace / Name /
// Close come from the embedded Base.
func (v *Virtual) Type() string { return service.RegistryTypeGo }

// PackageDetail delegates to the first member that has the module.
// Mirrors ServeFirstHit's iteration order so the UI sees the same
// data the data plane would serve.
func (v *Virtual) PackageDetail(ctx context.Context, module string) (*registry.PackageDetail, error) {
	return v.DelegatePackageDetail(ctx, module)
}

// ServeHTTP dispatches. List endpoints union across members; all
// other endpoints first-hit wins.
func (v *Virtual) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, ok := parsePath(r.URL.Path)
	if !ok {
		writeNotFound(w, "unrecognised go module proxy path: "+r.URL.Path)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeMethodNotAllowed(w, "GET, HEAD")
		return
	}

	if req.IsList {
		v.serveUnionList(w, r)
		return
	}
	if !v.ServeFirstHit(w, r) {
		writeNotFound(w, "no member served the request")
	}
}

// serveUnionList writes the merged @v/list response across all
// members. Order is lexicographic (matching what each Store does
// internally) so the output is deterministic. Members that 404
// contribute nothing; members that error are skipped — the union
// is best-effort.
//
// No ETag on the union: the response depends on N members'
// freshness so a stable fingerprint isn't worth the bookkeeping.
// The members' own list endpoints already get ETags.
func (v *Virtual) serveUnionList(w http.ResponseWriter, r *http.Request) {
	versions := v.CollectListLines(r)
	sort.Strings(versions)

	body := bytes.NewBufferString("")
	for _, ver := range versions {
		body.WriteString(ver)
		body.WriteByte('\n')
	}
	w.Header().Set("Content-Type", contentTypeFor("list"))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", body.Len()))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body.Bytes())
}
