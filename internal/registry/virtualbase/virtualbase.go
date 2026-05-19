// Package virtualbase factors out the cross-protocol skeleton of
// virtual registries. The four protocols pika ships (Go, NPM,
// Docker, Helm) all carry a near-identical Virtual struct: same
// fields, same constructor shape, same boilerplate
// Namespace/Name/Type/Kind/Close, same PackageDetail delegation,
// same first-hit ServeFirstHit helper, same hop-by-hop header
// copy. Before this package those ~120 lines of shared shape were
// copy-pasted four times.
//
// Each protocol's Virtual now embeds *Base and only supplies the
// protocol-specific dispatch (npm packument merge, docker
// referrers union, goproxy union list, helm index.yaml first-hit)
// in its own ServeHTTP. Type() / Kind() are still per-protocol
// because the protocol type string lives in the embedding struct;
// PackageDetail is forwarded via DelegatePackageDetail so the
// embedding type just delegates.
//
// Resolver indirection: virtuals don't own their members. They
// look the member registries up through a narrow Resolver
// interface on every request. This is what makes hot-reload safe
// — when settings change, the manager swaps the underlying Local
// / Remote instances and the next Lookup picks them up
// automatically.
package virtualbase

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/rakunlabs/pika/internal/registry"
)

// Resolver is the narrow surface Base needs from the manager.
// Defining it here lets each protocol's tests inject a stub
// without depending on registry.Manager.
type Resolver interface {
	Lookup(namespace, repo string) (registry.Registry, bool)
}

// Base holds the cross-protocol state and behaviour shared by
// every Virtual implementation. Each protocol embeds *Base and
// supplies a Type() string method (the package's
// service.RegistryType* constant).
//
// Fields are unexported with accessor methods because the Base
// is shared and the embedding struct should treat it as
// read-only after construction.
type Base struct {
	namespace   string
	repoName    string
	memberNames []string
	resolver    Resolver
}

// New constructs a Base. Members are defensively copied so the
// caller's slice can be mutated without affecting the live
// registry. Returns nil if members is empty — callers in factory
// constructors should check len(r.Members) before reaching here
// and produce a useful "members required" error.
func New(namespace, repoName string, members []string, resolver Resolver) *Base {
	cp := make([]string, len(members))
	copy(cp, members)
	return &Base{
		namespace:   namespace,
		repoName:    repoName,
		memberNames: cp,
		resolver:    resolver,
	}
}

// Namespace returns the parent namespace name.
func (b *Base) Namespace() string { return b.namespace }

// Name returns the virtual repository name.
func (b *Base) Name() string { return b.repoName }

// Kind always returns "virtual".
func (b *Base) Kind() string { return "virtual" }

// Close is a no-op — virtuals own no resources directly; their
// members are owned by the manager.
func (b *Base) Close() error { return nil }

// Members returns the configured member names. Returned slice is
// shared — treat as read-only.
func (b *Base) Members() []string { return b.memberNames }

// Resolve looks up one member by name within the virtual's parent
// namespace. Returns (nil, false) when the member references a
// repo that no longer exists (settings drift) — callers should
// skip silently.
func (b *Base) Resolve(member string) (registry.Registry, bool) {
	return b.resolver.Lookup(b.namespace, member)
}

// ForEachMember iterates resolved members in configuration order,
// stopping early when `fn` returns true. Members that fail to
// resolve are skipped automatically.
func (b *Base) ForEachMember(fn func(reg registry.Registry) (stop bool)) {
	for _, name := range b.memberNames {
		reg, ok := b.resolver.Lookup(b.namespace, name)
		if !ok {
			continue
		}
		if fn(reg) {
			return
		}
	}
}

// DelegatePackageDetail tries each member in order and returns
// the first PackageDetail without error. Used by every protocol's
// Virtual.PackageDetail — the behaviour is identical across
// Go / NPM / Docker / Helm.
//
// Returns registry.ErrPackageNotFound when no member has the
// package; callers can wrap that with protocol-specific context.
func (b *Base) DelegatePackageDetail(ctx context.Context, name string) (*registry.PackageDetail, error) {
	var found *registry.PackageDetail
	b.ForEachMember(func(reg registry.Registry) bool {
		pd, ok := reg.(registry.PackageDetailer)
		if !ok {
			return false
		}
		out, err := pd.PackageDetail(ctx, name)
		if err == nil {
			found = out
			return true
		}
		return false
	})
	if found != nil {
		return found, nil
	}
	return nil, registry.ErrPackageNotFound
}

// ServeFirstHit forwards `r` to each member in order using an
// in-memory recorder so the response can be inspected before being
// written to the real ResponseWriter. Writes the first 2xx and
// returns true; if every member returned non-2xx the caller is
// responsible for writing the protocol-specific not-found envelope
// (returns false).
//
// The recorder pattern keeps the per-member miss invisible to the
// client — no partial writes leak out.
func (b *Base) ServeFirstHit(w http.ResponseWriter, r *http.Request) bool {
	served := false
	b.ForEachMember(func(reg registry.Registry) bool {
		rec := httptest.NewRecorder()
		reg.ServeHTTP(rec, r)
		if rec.Code >= 200 && rec.Code < 300 {
			CopyHeaders(w.Header(), rec.Header())
			w.WriteHeader(rec.Code)
			_, _ = w.Write(rec.Body.Bytes())
			served = true
			return true
		}
		return false
	})
	return served
}

// CollectListLines iterates every member, queries it via the
// same request shape `r`, and returns the union of newline-
// separated body lines across all 2xx responses. Used by Go
// modules' @v/list union and any future protocol that needs a
// line-union semantic.
//
// Order matches member iteration; duplicates are collapsed via
// the de-dup map. Members that 4xx/5xx contribute nothing.
func (b *Base) CollectListLines(r *http.Request) []string {
	seen := make(map[string]struct{}, 32)
	var out []string
	b.ForEachMember(func(reg registry.Registry) bool {
		rec := httptest.NewRecorder()
		reg.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			return false
		}
		for _, line := range strings.Split(rec.Body.String(), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if _, dup := seen[line]; dup {
				continue
			}
			seen[line] = struct{}{}
			out = append(out, line)
		}
		return false
	})
	return out
}

// CopyHeaders copies every entry from src to dst, skipping the
// hop-by-hop headers that should not be forwarded across the
// virtual boundary (per RFC 7230 §6.1). The pre-extraction set of
// hop-by-hop headers were each duplicated across protocol packages;
// centralising avoids drift.
func CopyHeaders(dst, src http.Header) {
	for k, vs := range src {
		if isHopByHop(k) {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

var hopByHop = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

func isHopByHop(h string) bool {
	_, ok := hopByHop[http.CanonicalHeaderKey(h)]
	return ok
}
