package goproxy

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"

	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/service"
)

// Virtual is a Registry implementation that aggregates a list of
// sibling local + remote repos and serves them under a single URL.
//
// Member lookup
//
// Members are resolved from the namespace at construction time and
// re-resolved on every reload. For the `@v/list` endpoint, every
// member's list is fetched and the union (deduped, lexicographically
// sorted) is returned. For all other endpoints (.info / .mod / .zip /
// @latest), members are tried in the configured order — the first
// member that returns 200 wins, the rest are skipped.
//
// Writes
//
// Virtual repos reject writes (PUT). Clients that want to publish
// must address a concrete Local member by name. DefaultLocal (when
// set) is a UI hint, not an enforced redirect.
type Virtual struct {
	namespace string
	name      string

	// memberNames are the configured member repo names within the
	// same namespace. The Manager hands us the resolved Registry
	// instances via Resolve(); we don't hold direct pointers
	// because the manager may reload underlying repos
	// independently. Looking up by name on each request keeps the
	// member chain hot-reload-safe.
	memberNames []string
	resolve     func(namespace, repo string) (registry.Registry, bool)
}

// virtualResolver is the narrow surface Virtual needs from the
// manager. Defining the interface here lets tests inject stubs.
type virtualResolver interface {
	Lookup(namespace, repo string) (registry.Registry, bool)
}

// NewVirtualFactory returns the Factory for ("go", "virtual") repos.
// The factory closes over the manager so each new Virtual instance
// can look up its members lazily.
//
// Note on dependency direction: the manager creates this factory,
// the factory creates Virtual instances, Virtual instances call
// back into the manager via Lookup. The closure-over-manager
// pattern (rather than passing the manager through Deps) keeps the
// manager-facing surface invisible to the Local / Remote
// implementations that don't need it.
func NewVirtualFactory(resolver virtualResolver) registry.Factory {
	return func(_ context.Context, _ registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		if len(r.Members) == 0 {
			return nil, fmt.Errorf("goproxy/virtual %s/%s: members required", ns, r.Name)
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
func (v *Virtual) Type() string      { return service.RegistryTypeGo }
func (v *Virtual) Kind() string      { return service.RegistryKindVirtual }
func (v *Virtual) Close() error      { return nil }

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
		v.serveUnionList(w, r, req.Module)
		return
	}
	v.serveFirstHit(w, r)
}

// serveFirstHit forwards the request to each member in order and
// returns the first 2xx response. A member returning 404 prompts
// the next member; any non-404 4xx/5xx is treated the same as 404
// (the spec only differentiates "found / not found" at the
// protocol level — surfacing upstream 502s through a virtual repo
// would be confusing).
//
// Implementation: each member is given a fresh
// httptest.ResponseRecorder, so its response can be inspected
// before being written to the real ResponseWriter. Adds one
// in-memory buffer per call; negligible for typical go module
// payloads.
func (v *Virtual) serveFirstHit(w http.ResponseWriter, r *http.Request) {
	for _, name := range v.memberNames {
		mem, ok := v.resolve(v.namespace, name)
		if !ok {
			// Member references a repo that no longer exists
			// (settings drift). Skip silently — the validator
			// catches this at save time, this is a defence-in-
			// depth guard.
			continue
		}
		rec := httptest.NewRecorder()
		mem.ServeHTTP(rec, r)
		if rec.Code >= 200 && rec.Code < 300 {
			copyHeaders(w.Header(), rec.Header())
			w.WriteHeader(rec.Code)
			_, _ = w.Write(rec.Body.Bytes())
			return
		}
	}
	writeNotFound(w, "no member served the request")
}

// serveUnionList queries every member's @v/list and writes the
// union back to the client. Order is lexicographic (matching what
// the Store does internally) so the output is deterministic.
//
// Members that 404 contribute nothing; members that error are
// skipped — the union is best-effort. The result is therefore
// always a valid list, even if some upstreams are flaky.
func (v *Virtual) serveUnionList(w http.ResponseWriter, r *http.Request, mod string) {
	seen := make(map[string]struct{}, 32)
	for _, name := range v.memberNames {
		mem, ok := v.resolve(v.namespace, name)
		if !ok {
			continue
		}
		rec := httptest.NewRecorder()
		mem.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			continue
		}
		for _, line := range strings.Split(rec.Body.String(), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			seen[line] = struct{}{}
		}
	}

	versions := make([]string, 0, len(seen))
	for v := range seen {
		versions = append(versions, v)
	}
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
	// Note: no ETag here — the response is dependent on N members'
	// freshness so a stable fingerprint isn't worth the bookkeeping.
	// The members' own list endpoints already get ETags.
	_ = mod
}

// copyHeaders copies every non-hop-by-hop header from src to dst.
// Used to forward member responses through the virtual wrapper.
func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		// Filter hop-by-hop headers per RFC 7230 §6.1. The recorder
		// shouldn't have set them in the first place but defensive
		// stripping is cheap.
		switch strings.ToLower(k) {
		case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
			"te", "trailer", "transfer-encoding", "upgrade":
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}
