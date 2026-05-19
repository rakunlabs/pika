package helm

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/virtualbase"
	"github.com/rakunlabs/pika/internal/service"
)

// Virtual aggregates Helm Local + Remote members under one URL.
// index.yaml and tarball requests use first-hit-wins. Writes are
// rejected.
//
// Shared shell (namespace/name/members/resolver, ForEachMember,
// PackageDetail delegation, ServeFirstHit, header copy) lives in
// internal/registry/virtualbase. This file only carries the
// protocol-specific bits: the type string and the URL routing.
type Virtual struct {
	*virtualbase.Base
}

// NewVirtualFactory returns the Factory for ("helm", "virtual").
func NewVirtualFactory(resolver virtualbase.Resolver) registry.Factory {
	return func(_ context.Context, _ registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		if len(r.Members) == 0 {
			return nil, fmt.Errorf("helm/virtual %s/%s: members required", ns, r.Name)
		}
		return &Virtual{
			Base: virtualbase.New(ns, r.Name, r.Members, resolver),
		}, nil
	}
}

// Type returns the protocol identifier. Kind / Namespace / Name /
// Close come from the embedded Base.
func (v *Virtual) Type() string { return service.RegistryTypeHelm }

// PackageDetail delegates to the first member that has the chart.
func (v *Virtual) PackageDetail(ctx context.Context, name string) (*registry.PackageDetail, error) {
	return v.DelegatePackageDetail(ctx, name)
}

// ServeHTTP dispatches GET/HEAD. index.yaml + .tgz both use
// first-hit-wins. A future enhancement could union index.yaml
// entries across members; for the MVP the operator typically
// points a virtual at one big upstream + a small local set,
// where merge order matters less.
func (v *Virtual) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "virtual registry is read-only")
		return
	}
	p := r.URL.Path
	if p == "/index.yaml" || p == "/index.yaml/" || strings.HasSuffix(p, ".tgz") {
		if !v.ServeFirstHit(w, r) {
			writeError(w, http.StatusNotFound, "no member served the request")
		}
		return
	}
	writeError(w, http.StatusNotFound, "no helm route matches "+r.Method+" "+p)
}
