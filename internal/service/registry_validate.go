package service

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/rakunlabs/pika/internal/secretref"
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
//   - Repo Type: one of go/npm/docker/helm.
//   - Repo Kind: one of local/remote/virtual.
//   - Kind-specific fields must not be mixed across local/remote/virtual.
//   - MaxUploadSize must be non-negative.
//   - Policy fields must be well-formed and attached to supported repo shapes.
//   - Local: Mount + BasePath required.
//   - Remote: URL + cache Mount/BasePath required, parses as http(s)
//     URL, Auth shape valid.
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
		if r.MaxUploadSize < 0 {
			return fmt.Errorf("repo %q: max_upload_size must be >= 0: %w", r.Name, ErrBadRequest)
		}

		switch r.Kind {
		case RegistryKindLocal:
			if err := validateRegistryKindFields(r); err != nil {
				return err
			}
			if r.Mount == "" {
				return fmt.Errorf("repo %q: local kind requires mount: %w", r.Name, ErrBadRequest)
			}
			if r.BasePath == "" {
				return fmt.Errorf("repo %q: local kind requires base_path: %w", r.Name, ErrBadRequest)
			}
		case RegistryKindRemote:
			if err := validateRegistryKindFields(r); err != nil {
				return err
			}
			if r.URL == "" {
				return fmt.Errorf("repo %q: remote kind requires url: %w", r.Name, ErrBadRequest)
			}
			u, err := url.Parse(r.URL)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return fmt.Errorf("repo %q: url must be http(s): %w", r.Name, ErrBadRequest)
			}
			if r.Mount == "" {
				return fmt.Errorf("repo %q: remote kind requires cache mount: %w", r.Name, ErrBadRequest)
			}
			if r.BasePath == "" {
				return fmt.Errorf("repo %q: remote kind requires cache base_path: %w", r.Name, ErrBadRequest)
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
			if err := validateRegistryKindFields(r); err != nil {
				return err
			}
			if len(r.Members) == 0 {
				return fmt.Errorf("repo %q: virtual kind requires members: %w", r.Name, ErrBadRequest)
			}
		default:
			return fmt.Errorf("repo %q: invalid kind %q (want local|remote|virtual): %w", r.Name, r.Kind, ErrBadRequest)
		}
		if err := validateRegistryPolicy(r); err != nil {
			return err
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

func validateRegistryKindFields(r *RegistryRepository) error {
	switch r.Kind {
	case RegistryKindLocal:
		if r.URL != "" {
			return registryKindFieldError(r, "url")
		}
		if r.Auth != nil {
			return registryKindFieldError(r, "auth")
		}
		if r.MutableTTL != "" {
			return registryKindFieldError(r, "mutable_ttl")
		}
		if len(r.FloatingTags) > 0 {
			return registryKindFieldError(r, "floating_tags")
		}
		if r.InsecureSkipVerify {
			return registryKindFieldError(r, "insecure_skip_verify")
		}
		if len(r.Members) > 0 {
			return registryKindFieldError(r, "members")
		}
		if r.DefaultLocal != "" {
			return registryKindFieldError(r, "default_local")
		}
	case RegistryKindRemote:
		if r.AllowPush {
			return registryKindFieldError(r, "allow_push")
		}
		if len(r.Members) > 0 {
			return registryKindFieldError(r, "members")
		}
		if r.DefaultLocal != "" {
			return registryKindFieldError(r, "default_local")
		}
	case RegistryKindVirtual:
		if r.Mount != "" {
			return registryKindFieldError(r, "mount")
		}
		if r.BasePath != "" {
			return registryKindFieldError(r, "base_path")
		}
		if r.AllowPush {
			return registryKindFieldError(r, "allow_push")
		}
		if r.URL != "" {
			return registryKindFieldError(r, "url")
		}
		if r.Auth != nil {
			return registryKindFieldError(r, "auth")
		}
		if r.MutableTTL != "" {
			return registryKindFieldError(r, "mutable_ttl")
		}
		if len(r.FloatingTags) > 0 {
			return registryKindFieldError(r, "floating_tags")
		}
		if r.InsecureSkipVerify {
			return registryKindFieldError(r, "insecure_skip_verify")
		}
	}
	return nil
}

func registryKindFieldError(r *RegistryRepository, field string) error {
	return fmt.Errorf("repo %q: %s kind does not support %s: %w", r.Name, r.Kind, field, ErrBadRequest)
}

func validateRegistryPolicy(r *RegistryRepository) error {
	if r.Policy == nil {
		return nil
	}
	if len(r.Policy.ImmutableTags) > 0 {
		if r.Type != RegistryTypeDocker || r.Kind != RegistryKindLocal {
			return fmt.Errorf("repo %q: immutable_tags policy is supported only for docker local repositories: %w", r.Name, ErrBadRequest)
		}
		for _, pat := range r.Policy.ImmutableTags {
			trimmed := strings.TrimSpace(pat)
			if trimmed == "" {
				return fmt.Errorf("repo %q: immutable_tags contains an empty pattern: %w", r.Name, ErrBadRequest)
			}
			if pat != trimmed {
				return fmt.Errorf("repo %q: immutable_tags pattern %q must not contain leading or trailing spaces: %w", r.Name, pat, ErrBadRequest)
			}
			if strings.Contains(pat, "/") {
				return fmt.Errorf("repo %q: immutable_tags pattern %q must match a tag, not a path: %w", r.Name, pat, ErrBadRequest)
			}
			if _, err := path.Match(pat, "candidate"); err != nil {
				return fmt.Errorf("repo %q: immutable_tags pattern %q is invalid: %w: %w", r.Name, pat, err, ErrBadRequest)
			}
		}
	}
	if r.Policy.Retention != nil {
		if r.Type != RegistryTypeDocker || r.Kind != RegistryKindLocal {
			return fmt.Errorf("repo %q: retention policy is supported only for docker local repositories: %w", r.Name, ErrBadRequest)
		}
		if r.Policy.Retention.GCMinAgeSeconds < 0 {
			return fmt.Errorf("repo %q: retention.gc_min_age_seconds must be >= 0: %w", r.Name, ErrBadRequest)
		}
		if r.Policy.Retention.AbandonedUploadMaxAgeSeconds < 0 {
			return fmt.Errorf("repo %q: retention.abandoned_upload_max_age_seconds must be >= 0: %w", r.Name, ErrBadRequest)
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
		if err := validateRegistryAuthSecretValue("basic password", a.Password); err != nil {
			return err
		}
	case RegistryAuthBearer:
		if a.Token == "" {
			return fmt.Errorf("bearer auth requires token: %w", ErrBadRequest)
		}
		if err := validateRegistryAuthSecretValue("bearer token", a.Token); err != nil {
			return err
		}
	case RegistryAuthHeader:
		if a.Header == "" {
			return fmt.Errorf("header auth requires header name: %w", ErrBadRequest)
		}
		if a.Value == "" {
			return fmt.Errorf("header auth requires value: %w", ErrBadRequest)
		}
		if err := validateRegistryAuthSecretValue("header value", a.Value); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid auth type %q (want basic|bearer|header): %w", a.Type, ErrBadRequest)
	}
	return nil
}

func validateRegistryAuthSecretValue(field, value string) error {
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "secret://") {
		return fmt.Errorf("%s uses unsupported secret:// reference (use raw://mount/path or config://key): %w", field, ErrBadRequest)
	}
	if strings.HasPrefix(value, "raw://") {
		ref, _, _, err := secretref.Split(strings.TrimPrefix(value, "raw://"))
		if err != nil {
			return fmt.Errorf("%s raw:// reference is invalid: %w: %w", field, err, ErrBadRequest)
		}
		parts := strings.SplitN(ref, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("%s raw:// reference must be raw://mount/path: %w", field, ErrBadRequest)
		}
	}
	if strings.HasPrefix(value, "config://") {
		if _, _, _, err := secretref.Split(strings.TrimPrefix(value, "config://")); err != nil {
			return fmt.Errorf("%s config:// reference is invalid: %w: %w", field, err, ErrBadRequest)
		}
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
