package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rakunlabs/pika/internal/external"
)

type tokenScopesContextKey struct{}

// WithTokenScopes preserves resource/path/operation pairings below HTTP and MCP.
// A non-nil empty slice means deny all; absence means a session/internal caller.
func WithTokenScopes(ctx context.Context, scopes []TokenScope) context.Context {
	if scopes == nil {
		scopes = []TokenScope{}
	}
	return context.WithValue(ctx, tokenScopesContextKey{}, scopes)
}

func tokenScopesFromContext(ctx context.Context) []TokenScope {
	scopes, _ := ctx.Value(tokenScopesContextKey{}).([]TokenScope)
	return scopes
}

func validateExternalTokenScopes(scopes []TokenScope) error {
	for _, sc := range scopes {
		if sc.Resource == "" {
			continue
		}
		if strings.TrimSpace(sc.Resource) != sc.Resource || strings.ContainsAny(sc.Resource, "/\\*?%#") {
			return fmt.Errorf("external scope requires an exact resource name: %w", ErrBadRequest)
		}
		if !validExternalScopePath(sc.Path) || len(sc.Operations) == 0 {
			return fmt.Errorf("external scope requires a clean path and operations: %w", ErrBadRequest)
		}
		for _, op := range sc.Operations {
			if op != "read" && op != "write" && op != "delete" && op != "*" {
				return fmt.Errorf("invalid external scope operation %q: %w", op, ErrBadRequest)
			}
		}
	}
	return nil
}

func validExternalScopePath(p string) bool {
	if p == "" || strings.TrimSpace(p) != p || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\%?#") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func externalScopeAllows(scopes []TokenScope, resource, p, op string, ancestor bool) bool {
	if scopes == nil {
		return true
	}
	p = strings.Trim(p, "/")
	if p != "" && !validExternalScopePath(p) {
		return false
	}
	for _, sc := range scopes {
		if sc.Resource != resource || sc.Resource == "" || !containsOperation(sc.Operations, op) {
			continue
		}
		if matchPath(sc.Path, p) {
			return true
		}
		if ancestor {
			// Use literal token-segment semantics, with * and ** as the
			// only wildcard segments, exactly as the config token matcher.
			pattern := sc.Path
			if pattern == "*" {
				pattern = "**"
			}
			parts := strings.Split(pattern, "/")
			for i, part := range parts {
				if part != "*" && part != "**" {
					parts[i] = strings.NewReplacer("[", "\\[", "]", "\\]", "{", "\\{", "}", "\\}", "*", "\\*", "?", "\\?").Replace(part)
				}
			}
			if (CapabilityPatterns{CapExternalRead: {strings.Join(parts, "/")}}).AllowsAncestor(CapExternalRead, p) {
				return true
			}
		}
	}
	return false
}

// scopedExternalProvider enforces token grants for every consumer: REST, MCP,
// search, version history and resolved-config inheritance all use this wrapper.
type scopedExternalProvider struct {
	external.Provider
	resource string
	scopes   []TokenScope
}

func (p *scopedExternalProvider) allow(path, op string, ancestor bool) error {
	if !externalScopeAllows(p.scopes, p.resource, path, op, ancestor) {
		return fmt.Errorf("external path is not permitted for %s: %w", op, ErrForbidden)
	}
	return nil
}

func (p *scopedExternalProvider) Capabilities() external.Capabilities {
	c := p.Provider.Capabilities()
	r := externalScopeAllows(p.scopes, p.resource, "", "read", true)
	c.CanRead = c.CanRead && r
	c.CanList = c.CanList && r
	c.CanVersions = c.CanVersions && r
	w := externalScopeAllows(p.scopes, p.resource, "", "write", true)
	c.CanWrite = c.CanWrite && w
	// Token scopes have a single "write" operation, so they narrow both
	// halves of a resource's create/update split rather than replacing it.
	if c.CanCreate != nil {
		create := *c.CanCreate && w
		c.CanCreate = &create
	}
	if c.CanUpdate != nil {
		update := *c.CanUpdate && w
		c.CanUpdate = &update
	}
	c.CanDelete = c.CanDelete && externalScopeAllows(p.scopes, p.resource, "", "delete", true)
	return c
}

func (p *scopedExternalProvider) Fetch(ctx context.Context, path string) ([]byte, error) {
	if err := p.allow(path, "read", false); err != nil {
		return nil, err
	}
	return p.Provider.Fetch(ctx, path)
}
func (p *scopedExternalProvider) Read(ctx context.Context, path string) (*external.Entry, error) {
	if err := p.allow(path, "read", false); err != nil {
		return nil, err
	}
	return p.Provider.Read(ctx, path)
}
func (p *scopedExternalProvider) ReadVersion(ctx context.Context, path, version string) (*external.Entry, error) {
	if err := p.allow(path, "read", false); err != nil {
		return nil, err
	}
	return p.Provider.ReadVersion(ctx, path, version)
}
func (p *scopedExternalProvider) ListVersions(ctx context.Context, path string) ([]external.Version, error) {
	if err := p.allow(path, "read", false); err != nil {
		return nil, err
	}
	return p.Provider.ListVersions(ctx, path)
}
func (p *scopedExternalProvider) Write(ctx context.Context, path string, data map[string]any) error {
	if err := p.allow(path, "write", false); err != nil {
		return err
	}
	return p.Provider.Write(ctx, path, data)
}
func (p *scopedExternalProvider) Delete(ctx context.Context, path string) error {
	if err := p.allow(path, "delete", false); err != nil {
		return err
	}
	return p.Provider.Delete(ctx, path)
}
func (p *scopedExternalProvider) List(ctx context.Context, prefix string) ([]string, error) {
	if err := p.allow(prefix, "read", true); err != nil {
		return nil, err
	}
	children, err := p.Provider.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(children))
	for _, child := range children {
		if p.allow(joinPath(prefix, child), "read", true) == nil {
			out = append(out, child)
		}
	}
	return out, nil
}
func (p *scopedExternalProvider) Test(context.Context) external.TestResult {
	return external.TestResult{Message: "Connection tests require a user session"}
}
