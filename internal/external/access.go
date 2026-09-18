package external

import (
	"context"
	"errors"
	"fmt"
)

// ErrAccessDenied is returned when a resource's own access settings forbid an
// operation. It is distinct from ErrNotSupported: the backend can do it, the
// operator decided it must not. The API layer maps it to 403 so the SPA shows
// "not permitted" instead of a generic failure.
var ErrAccessDenied = errors.New("operation denied by external resource access settings")

// Access restricts what a configured external resource is allowed to do,
// regardless of who is calling. It is the resource-side counterpart of the
// caller-side controls (capabilities and token scopes): a read-only Vault
// record stays read-only even for a superadmin session, and a GitLab resource
// can expose existing variables without letting anyone add new ones.
//
// Every field is tri-state on purpose:
//   - nil   — unrestricted. Resources saved before this field existed keep
//     their previous behaviour, which is what upgrades must do.
//   - true  — explicitly allowed (same effect as nil, but survives round-trips
//     so the UI can render a deliberate choice).
//   - false — denied. The guard rejects the operation before the backend is
//     contacted, and Capabilities() drops the matching flag so the SPA hides
//     the button instead of offering an action that always fails.
//
// Create and Update are separate because "add a new key" and "change an
// existing key" are different risks — the common ask is "you may edit what is
// already there, but do not introduce new entries". Splitting them requires
// knowing whether a path already exists, which only backends implementing
// ExistenceChecker can answer; see accessProvider.Write for the fallback.
type Access struct {
	Read   *bool `json:"read,omitempty"`
	List   *bool `json:"list,omitempty"`
	Create *bool `json:"create,omitempty"`
	Update *bool `json:"update,omitempty"`
	Delete *bool `json:"delete,omitempty"`
}

func allowed(v *bool) bool { return v == nil || *v }

// CanRead reports whether single-entry reads (and the inheritance Fetch path,
// plus version history) are permitted. A nil receiver is unrestricted.
func (a *Access) CanRead() bool { return a == nil || allowed(a.Read) }

// CanList reports whether path enumeration is permitted. Listing is separate
// from reading so an operator can hand out "you may read the keys you already
// know" without publishing the full inventory.
func (a *Access) CanList() bool { return a == nil || allowed(a.List) }

// CanCreate reports whether writing to a path that does not exist yet is
// permitted.
func (a *Access) CanCreate() bool { return a == nil || allowed(a.Create) }

// CanUpdate reports whether overwriting an existing path is permitted.
func (a *Access) CanUpdate() bool { return a == nil || allowed(a.Update) }

// CanDelete reports whether deletes are permitted.
func (a *Access) CanDelete() bool { return a == nil || allowed(a.Delete) }

// Restricted reports whether any operation is denied. ResourceProvider skips
// the guard wrapper entirely when nothing is restricted, so the unrestricted
// path stays allocation-free and behaves exactly as it did before.
func (a *Access) Restricted() bool {
	if a == nil {
		return false
	}
	return !a.CanRead() || !a.CanList() || !a.CanCreate() || !a.CanUpdate() || !a.CanDelete()
}

// ExistenceChecker is the optional interface a Provider implements when it can
// cheaply answer "does this path already exist" without reading the value.
// Only backends implementing it can distinguish create from update; see
// GitLabProvider.Exists for the reference implementation.
type ExistenceChecker interface {
	Exists(ctx context.Context, path string) (bool, error)
}

// accessProvider enforces Access for every consumer — REST, MCP, public
// endpoints, search and config inheritance all resolve providers through
// ResourceProvider, so there is no path around this wrapper.
//
// It sits *inside* the token-scope wrapper (service.scopedExternalProvider):
// resource settings are the outer ceiling, token scopes narrow further, and
// both must allow an operation for it to run.
type accessProvider struct {
	Provider
	access *Access
}

func (p *accessProvider) deny(op string) error {
	return fmt.Errorf("%s is disabled for this external resource: %w", op, ErrAccessDenied)
}

func (p *accessProvider) Capabilities() Capabilities {
	c := p.Provider.Capabilities()
	c.CanRead = c.CanRead && p.access.CanRead()
	c.CanVersions = c.CanVersions && p.access.CanRead()
	c.CanList = c.CanList && p.access.CanList()
	c.CanWrite = c.CanWrite && (p.access.CanCreate() || p.access.CanUpdate())
	c.CanDelete = c.CanDelete && p.access.CanDelete()
	// Publish the split only when the backend can honour it; otherwise the
	// SPA would offer a "new entry" form that Write always rejects.
	if _, splits := p.Provider.(ExistenceChecker); splits {
		create, update := p.access.CanCreate(), p.access.CanUpdate()
		c.CanCreate, c.CanUpdate = &create, &update
	}
	return c
}

func (p *accessProvider) Fetch(ctx context.Context, path string) ([]byte, error) {
	if !p.access.CanRead() {
		return nil, p.deny("reading")
	}
	return p.Provider.Fetch(ctx, path)
}

func (p *accessProvider) Read(ctx context.Context, path string) (*Entry, error) {
	if !p.access.CanRead() {
		return nil, p.deny("reading")
	}
	return p.Provider.Read(ctx, path)
}

func (p *accessProvider) ListVersions(ctx context.Context, path string) ([]Version, error) {
	if !p.access.CanRead() {
		return nil, p.deny("reading")
	}
	return p.Provider.ListVersions(ctx, path)
}

func (p *accessProvider) ReadVersion(ctx context.Context, path, version string) (*Entry, error) {
	if !p.access.CanRead() {
		return nil, p.deny("reading")
	}
	return p.Provider.ReadVersion(ctx, path, version)
}

func (p *accessProvider) List(ctx context.Context, prefix string) ([]string, error) {
	if !p.access.CanList() {
		return nil, p.deny("listing")
	}
	return p.Provider.List(ctx, prefix)
}

func (p *accessProvider) Delete(ctx context.Context, path string) error {
	if !p.access.CanDelete() {
		return p.deny("deleting")
	}
	return p.Provider.Delete(ctx, path)
}

// Write splits into create and update. When both are allowed (or both denied)
// no probe is needed. When they differ we ask the backend whether the path
// exists; backends that can't answer refuse the write rather than guessing,
// because guessing would either leak new keys past a "no new entries" rule or
// silently reject legitimate updates.
func (p *accessProvider) Write(ctx context.Context, path string, data map[string]any) error {
	create, update := p.access.CanCreate(), p.access.CanUpdate()
	switch {
	case create && update:
	case !create && !update:
		return p.deny("writing")
	default:
		checker, ok := p.Provider.(ExistenceChecker)
		if !ok {
			return fmt.Errorf("this backend cannot tell new entries from existing ones: allow both create and update, or neither: %w", ErrAccessDenied)
		}
		exists, err := checker.Exists(ctx, path)
		if err != nil {
			return err
		}
		if exists && !update {
			return p.deny("changing existing entries")
		}
		if !exists && !create {
			return p.deny("creating new entries")
		}
	}
	return p.Provider.Write(ctx, path, data)
}

// Test stays reachable even on a fully denied resource: operators still need
// to verify credentials. It reports through List, so a resource with listing
// denied says so instead of pretending the backend is unreachable.
func (p *accessProvider) Test(ctx context.Context) TestResult {
	if !p.access.CanList() {
		return TestResult{Message: "Listing is disabled for this external resource"}
	}
	return p.Provider.Test(ctx)
}
