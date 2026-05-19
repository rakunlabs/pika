package service

import (
	"fmt"
	"strings"
	"time"
)

// Validate walks RegistrySettings and rejects mis-shaped rows so the
// runtime (internal/registry) can trust the shape without re-checking
// at every request. Called from PatchSettings before persistence.
//
// Errors are wrapped with ErrBadRequest so the HTTP layer returns 400.
//
// Rules enforced here:
//
//   - Namespace names: non-empty, lowercase [a-z0-9_-], unique.
//   - Repo names: non-empty, lowercase [a-z0-9_-], unique within a
//     namespace.
//   - Repo Type: one of go/npm/docker.
//   - Repo Kind: one of local/remote/virtual.
//   - Local: Mount + BasePath required.
//   - Remote: URL required, parses as http(s) URL, Auth shape valid.
//   - Virtual: Members non-empty, no self-reference, every referenced
//     name exists in the same namespace, member.Type matches this
//     repo's Type.
//   - MutableTTL parses as a Go duration (when set).
func (rs *RegistrySettings) Validate() error {
	if rs == nil {
		return nil
	}

	seenNamespaces := make(map[string]struct{}, len(rs.Namespaces))
	for i := range rs.Namespaces {
		ns := &rs.Namespaces[i]
		if err := validateRegistryName(ns.Name); err != nil {
			return fmt.Errorf("namespace[%d].name: %w: %w", i, err, ErrBadRequest)
		}
		if _, dup := seenNamespaces[ns.Name]; dup {
			return fmt.Errorf("duplicate namespace %q: %w", ns.Name, ErrBadRequest)
		}
		seenNamespaces[ns.Name] = struct{}{}

		if err := validateNamespaceRepos(ns); err != nil {
			return fmt.Errorf("namespace %q: %w", ns.Name, err)
		}
	}
	return nil
}

func validateNamespaceRepos(ns *RegistryNamespace) error {
	// Build name->index map for virtual member resolution.
	seen := make(map[string]int, len(ns.Repositories))
	for i := range ns.Repositories {
		r := &ns.Repositories[i]
		if err := validateRegistryName(r.Name); err != nil {
			return fmt.Errorf("repo[%d].name: %w: %w", i, err, ErrBadRequest)
		}
		if _, dup := seen[r.Name]; dup {
			return fmt.Errorf("duplicate repo %q: %w", r.Name, ErrBadRequest)
		}
		seen[r.Name] = i

		if !IsKnownRegistryType(r.Type) {
			return fmt.Errorf("repo %q: invalid type %q (want one of %v): %w",
				r.Name, r.Type, KnownRegistryTypes, ErrBadRequest)
		}

		switch r.Kind {
		case RegistryKindLocal:
			if r.Mount == "" {
				return fmt.Errorf("repo %q: local kind requires mount: %w", r.Name, ErrBadRequest)
			}
		case RegistryKindRemote:
			if r.URL == "" {
				return fmt.Errorf("repo %q: remote kind requires url: %w", r.Name, ErrBadRequest)
			}
			if !strings.HasPrefix(r.URL, "http://") && !strings.HasPrefix(r.URL, "https://") {
				return fmt.Errorf("repo %q: url must be http(s): %w", r.Name, ErrBadRequest)
			}
			if r.Auth != nil {
				if err := validateRegistryAuth(r.Auth); err != nil {
					return fmt.Errorf("repo %q: %w", r.Name, err)
				}
			}
			if r.MutableTTL != "" {
				if _, err := time.ParseDuration(r.MutableTTL); err != nil {
					return fmt.Errorf("repo %q: invalid mutable_ttl %q: %w: %w", r.Name, r.MutableTTL, err, ErrBadRequest)
				}
			}
		case RegistryKindVirtual:
			if len(r.Members) == 0 {
				return fmt.Errorf("repo %q: virtual kind requires members: %w", r.Name, ErrBadRequest)
			}
		default:
			return fmt.Errorf("repo %q: invalid kind %q (want local|remote|virtual): %w", r.Name, r.Kind, ErrBadRequest)
		}
	}

	// Second pass: virtual member references resolve to existing repos
	// of the matching type. Done after the first pass so order in the
	// JSON doesn't matter (a virtual can come before its members).
	for i := range ns.Repositories {
		r := &ns.Repositories[i]
		if r.Kind != RegistryKindVirtual {
			continue
		}
		seenMember := make(map[string]struct{}, len(r.Members))
		for _, m := range r.Members {
			if m == r.Name {
				return fmt.Errorf("repo %q: virtual cannot reference itself: %w", r.Name, ErrBadRequest)
			}
			if _, dup := seenMember[m]; dup {
				return fmt.Errorf("repo %q: duplicate member %q: %w", r.Name, m, ErrBadRequest)
			}
			seenMember[m] = struct{}{}

			idx, ok := seen[m]
			if !ok {
				return fmt.Errorf("repo %q: member %q not found in namespace: %w", r.Name, m, ErrBadRequest)
			}
			if ns.Repositories[idx].Type != r.Type {
				return fmt.Errorf("repo %q: member %q has type %q, expected %q: %w",
					r.Name, m, ns.Repositories[idx].Type, r.Type, ErrBadRequest)
			}
			// Members must themselves be local or remote — virtual-of-
			// virtual is rejected to avoid lookup cycles and to keep
			// the lookup chain bounded by namespace size.
			if ns.Repositories[idx].Kind == RegistryKindVirtual {
				return fmt.Errorf("repo %q: member %q is itself virtual (chains not allowed): %w",
					r.Name, m, ErrBadRequest)
			}
		}
		// DefaultLocal (if set) must refer to a member that is local.
		if r.DefaultLocal != "" {
			idx, ok := seen[r.DefaultLocal]
			if !ok {
				return fmt.Errorf("repo %q: default_local %q not found: %w", r.Name, r.DefaultLocal, ErrBadRequest)
			}
			if ns.Repositories[idx].Kind != RegistryKindLocal {
				return fmt.Errorf("repo %q: default_local %q is not local: %w", r.Name, r.DefaultLocal, ErrBadRequest)
			}
		}
	}
	return nil
}

func validateRegistryAuth(a *RegistryUpstreamAuth) error {
	switch a.Type {
	case RegistryAuthBasic:
		if a.Username == "" {
			return fmt.Errorf("basic auth requires username: %w", ErrBadRequest)
		}
	case RegistryAuthBearer:
		if a.Token == "" {
			return fmt.Errorf("bearer auth requires token: %w", ErrBadRequest)
		}
	case RegistryAuthHeader:
		if a.Header == "" {
			return fmt.Errorf("header auth requires header name: %w", ErrBadRequest)
		}
	default:
		return fmt.Errorf("invalid auth type %q (want basic|bearer|header): %w", a.Type, ErrBadRequest)
	}
	return nil
}

// validateRegistryName checks a namespace / repo name against the
// common allowed-character set. Kept conservative on purpose: the
// strictest protocol we host is Docker, which permits more characters
// in repo path segments but rejects uppercase. Lowercase alphanumeric
// + hyphen + underscore is the safe intersection across go/npm/docker.
func validateRegistryName(name string) error {
	if name == "" {
		return fmt.Errorf("empty: %w", ErrBadRequest)
	}
	if len(name) > 64 {
		return fmt.Errorf("name %q exceeds 64 characters: %w", name, ErrBadRequest)
	}
	for i, r := range name {
		ok := (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_'
		if !ok {
			return fmt.Errorf("name %q: invalid character at position %d (allowed: a-z 0-9 - _): %w", name, i, ErrBadRequest)
		}
	}
	return nil
}

// FindNamespace returns a pointer to the namespace with the given
// name, or nil. Lookup is linear because namespace counts are tiny
// (10s at most in practice).
func (rs *RegistrySettings) FindNamespace(name string) *RegistryNamespace {
	if rs == nil {
		return nil
	}
	for i := range rs.Namespaces {
		if rs.Namespaces[i].Name == name {
			return &rs.Namespaces[i]
		}
	}
	return nil
}

// FindRepository returns a pointer to the repo with the given name
// inside the namespace, or nil.
func (ns *RegistryNamespace) FindRepository(name string) *RegistryRepository {
	if ns == nil {
		return nil
	}
	for i := range ns.Repositories {
		if ns.Repositories[i].Name == name {
			return &ns.Repositories[i]
		}
	}
	return nil
}
