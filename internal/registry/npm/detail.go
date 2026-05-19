package npm

import (
	"context"
	"fmt"
	"strings"

	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/common/semver"
)

// buildPackageDetail collects per-package metadata for one NPM
// package into the shared registry.PackageDetail shape. Shared
// between Local and Remote so both surface identical fields.
//
// Returns (nil, registry.ErrPackageNotFound) when no versions are
// stored for `name`. Errors loading individual versions are
// non-fatal — the resulting entry is still listed with whatever
// subset is readable.
func buildPackageDetail(_ context.Context, s *Store, name string) (*registry.PackageDetail, error) {
	if name == "" {
		// Empty name is malformed input, not a missing package.
		// Map to ErrInvalidPackageName so the HTTP handler can
		// surface a 400 instead of 404.
		return nil, fmt.Errorf("npm: empty package name: %w", registry.ErrInvalidPackageName)
	}
	versions, err := s.ListVersions(name)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, registry.ErrPackageNotFound
	}
	// Sort newest-first using a semver-ish comparator. Pure lex
	// would put 10.0.0 before 2.0.0; the comparator below splits
	// on dots and compares numerically when possible.
	sortVersionsDesc(versions)

	tags, _ := s.ReadDistTags(name)
	if tags == nil {
		tags = map[string]string{}
	}
	latest := tags["latest"]
	if latest == "" {
		latest = versions[0]
	}

	out := &registry.PackageDetail{
		Type: "npm",
		Name: name,
		NPM: &registry.NPMPackageDetail{
			LatestVersion: latest,
			DistTags:      tags,
			HasReadme:     s.HasReadme(name),
			Versions:      make([]registry.NPMVersionDetail, 0, len(versions)),
		},
	}

	// Top-level package fields are pulled from the latest version's
	// metadata. This mirrors the packument convention: top-level
	// description / keywords / license / etc. track the latest
	// publish.
	if latestMeta, err := s.ReadVersionMeta(name, latest); err == nil {
		applyTopLevelMeta(out.NPM, latestMeta)
	}

	for _, v := range versions {
		row := registry.NPMVersionDetail{Version: v}
		meta, err := s.ReadVersionMeta(name, v)
		if err == nil {
			applyVersionMeta(&row, meta)
			// Backfill tarball size from disk when the meta
			// doesn't carry one.
			if row.Size == 0 {
				if fn := tarballFilenameFromMeta(meta); fn != "" {
					row.Size = s.TarballSize(name, fn)
				}
			}
		}
		out.NPM.Versions = append(out.NPM.Versions, row)
	}
	return out, nil
}

// applyTopLevelMeta picks the fields from a per-version metadata
// blob that belong at the package level (description, license,
// repository, etc.) and writes them into the detail document.
func applyTopLevelMeta(d *registry.NPMPackageDetail, meta map[string]any) {
	if s, ok := meta["description"].(string); ok {
		d.Description = s
	}
	if s, ok := meta["homepage"].(string); ok {
		d.Homepage = s
	}
	d.License = parseLicense(meta["license"])
	if m, ok := meta["repository"].(map[string]any); ok {
		d.Repository = m
	} else if s, ok := meta["repository"].(string); ok {
		// npm allows a string shorthand; normalise into the
		// canonical {type,url} shape so the UI has one path.
		d.Repository = map[string]any{"url": s}
	}
	if m, ok := meta["bugs"].(map[string]any); ok {
		d.Bugs = m
	} else if s, ok := meta["bugs"].(string); ok {
		d.Bugs = map[string]any{"url": s}
	}
	if kw, ok := meta["keywords"].([]any); ok {
		out := make([]string, 0, len(kw))
		for _, k := range kw {
			if s, ok := k.(string); ok {
				out = append(out, s)
			}
		}
		d.Keywords = out
	}
	if m, ok := meta["author"].(map[string]any); ok {
		d.Author = m
	} else if s, ok := meta["author"].(string); ok {
		d.Author = map[string]any{"name": s}
	}
	if m, ok := meta["maintainers"].([]any); ok {
		d.Maintainers = m
	}
}

// applyVersionMeta extracts per-version fields into the detail row.
func applyVersionMeta(row *registry.NPMVersionDetail, meta map[string]any) {
	if t, ok := meta["_publishedTime"].(string); ok {
		row.PublishedAt = t
	}
	if dist, ok := meta["dist"].(map[string]any); ok {
		if s, ok := dist["integrity"].(string); ok {
			row.Integrity = s
		}
		if s, ok := dist["shasum"].(string); ok {
			row.Shasum = s
		}
		if s, ok := dist["tarball"].(string); ok {
			row.TarballURL = s
		}
		// Some clients embed an explicit unpackedSize / fileCount
		// at dist; npm.org publishes a "size" via dist.size in the
		// public registry. Honour either if present.
		if n, ok := numericFromAny(dist["unpackedSize"]); ok {
			row.Size = n
		} else if n, ok := numericFromAny(dist["size"]); ok {
			row.Size = n
		}
	}
	if m, ok := meta["dependencies"].(map[string]any); ok {
		row.Dependencies = m
	}
	if m, ok := meta["devDependencies"].(map[string]any); ok {
		row.DevDependencies = m
	}
	if m, ok := meta["peerDependencies"].(map[string]any); ok {
		row.PeerDependencies = m
	}
	if m, ok := meta["engines"].(map[string]any); ok {
		row.Engines = m
	}
	// `deprecated` may appear at the version root: any string makes
	// the version a "deprecated with reason" entry. Boolean true is
	// rare but normalise it to a placeholder reason.
	switch v := meta["deprecated"].(type) {
	case string:
		if v != "" {
			row.Deprecated = v
		}
	case bool:
		if v {
			row.Deprecated = "deprecated"
		}
	}
}

// parseLicense normalises the various shapes the `license` field
// appears in across npm history into a single human-friendly
// string. SPDX expressions are returned as-is; the object form
// ({type, url}) collapses to "type".
func parseLicense(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if s, ok := t["type"].(string); ok {
			return s
		}
	case []any:
		// Legacy: an array of {type,url} objects. Join the type
		// fields with " OR " so the UI can render at least
		// something sensible.
		parts := make([]string, 0, len(t))
		for _, e := range t {
			if m, ok := e.(map[string]any); ok {
				if s, ok := m["type"].(string); ok {
					parts = append(parts, s)
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, " OR ")
		}
	}
	return ""
}

// tarballFilenameFromMeta extracts the filename portion of
// meta.dist.tarball. Used to look up the on-disk tarball size when
// meta.dist.unpackedSize / size is absent.
func tarballFilenameFromMeta(meta map[string]any) string {
	dist, ok := meta["dist"].(map[string]any)
	if !ok {
		return ""
	}
	t, _ := dist["tarball"].(string)
	if t == "" {
		return ""
	}
	if i := strings.LastIndex(t, "/"); i >= 0 {
		return t[i+1:]
	}
	return t
}

// numericFromAny tries to coerce a JSON value into int64. JSON
// decoders produce float64 for numbers; this is the canonical
// "unwrap" pattern used across pika.
func numericFromAny(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	}
	return 0, false
}

// sortVersionsDesc orders semver-ish version strings newest-first.
// Numeric components are compared numerically when possible;
// pre-release tags ("-rc.1") sort below the matching release; if a
// version doesn't parse it falls back to reverse-lex order so the
// result is at least stable.
// sortVersionsDesc orders the slice newest-first using the
// shared common/semver comparator in lenient mode (npm permits
// both "v1.2.3" and "1.2.3" in package.json).
func sortVersionsDesc(v []string) {
	semver.SortDesc(v, semver.ModeLenient)
}
