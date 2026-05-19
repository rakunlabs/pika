package npm

import (
	"encoding/json"
	"fmt"
)

// Packument is the JSON shape npm clients expect at `GET /{pkg}`.
// It collects every published version's metadata under a single
// document along with dist-tags and a few package-level fields.
//
// Shape reference: https://github.com/npm/registry/blob/main/docs/responses/package-metadata.md
//
// We use map[string]any throughout instead of typed structs because
// the per-version metadata is wildly heterogeneous in practice
// (every package adds custom fields) and round-tripping through a
// strict struct would silently drop them. The published version's
// JSON is opaque to us — we hand it back verbatim.
type Packument struct {
	// Name of the package ("lodash" or "@scope/name").
	Name string `json:"name"`
	// DistTags maps tag → version. Always carries at least "latest".
	DistTags map[string]string `json:"dist-tags"`
	// Versions: version → per-version metadata blob.
	Versions map[string]map[string]any `json:"versions"`
	// Time records when each version was published. Optional;
	// some npm tooling expects it, others ignore.
	Time map[string]string `json:"time,omitempty"`
	// Readme is the long-form README from the latest publish.
	// Optional — many internal registries omit it.
	Readme string `json:"readme,omitempty"`
	// Description, Maintainers, Repository: pulled from the latest
	// version's metadata so packument top-level fields stay in sync
	// with the published version. All three are optional.
	Description string         `json:"description,omitempty"`
	Maintainers []any          `json:"maintainers,omitempty"`
	Repository  map[string]any `json:"repository,omitempty"`
	// Underscore-id is the legacy CouchDB id field; npm clients
	// occasionally consume it.
	UnderscoreID string `json:"_id,omitempty"`
	// Rev is the CouchDB-style revision token. We synthesise a
	// monotonically-increasing string from the version count so npm
	// doesn't reject the packument over a missing _rev.
	Rev string `json:"_rev,omitempty"`
}

// BuildPackument assembles a Packument from the store's view of one
// package. Returns ErrPackageNotFound when no versions exist.
//
// The rebuild walks every per-version metadata file once, which is
// O(versions). For packages with 100s of versions this is still
// cheap (each meta is small JSON); the cost is amortised by the
// cached packument.json file the store keeps next to versions/.
func BuildPackument(s *Store, name string) (*Packument, error) {
	versions, err := s.ListVersions(name)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("%s: %w", name, ErrPackageNotFound)
	}

	tags, err := s.ReadDistTags(name)
	if err != nil {
		return nil, fmt.Errorf("read dist-tags: %w", err)
	}
	if _, hasLatest := tags["latest"]; !hasLatest {
		// Without an explicit "latest" dist-tag, fall back to the
		// lexicographically-highest version. Real semver order
		// would be preferable but ListVersions already sorts
		// lexicographically and most real packages follow vX.Y.Z
		// where lex == semver for two-digit numbers.
		tags["latest"] = versions[len(versions)-1]
	}

	pkg := &Packument{
		Name:         name,
		DistTags:     tags,
		Versions:     make(map[string]map[string]any, len(versions)),
		Time:         make(map[string]string, len(versions)+2),
		UnderscoreID: name,
		Rev:          fmt.Sprintf("%d-rev", len(versions)),
	}
	for _, v := range versions {
		meta, err := s.ReadVersionMeta(name, v)
		if err != nil {
			// Skip versions whose metadata is unreadable rather
			// than failing the whole packument — the broken
			// version is invisible until an operator fixes it.
			continue
		}
		// Ensure the per-version "name" + "version" fields are
		// populated; some upstream bodies omit them at publish
		// time and rely on the server to backfill.
		if _, ok := meta["name"]; !ok {
			meta["name"] = name
		}
		if _, ok := meta["version"]; !ok {
			meta["version"] = v
		}
		pkg.Versions[v] = meta
		// Pull the publish time out of the version meta if the
		// client included it. Optional — absence is fine.
		if t, ok := meta["_publishedTime"].(string); ok {
			pkg.Time[v] = t
		}
	}

	// Top-level package metadata comes from the "latest" version.
	if latest, ok := tags["latest"]; ok {
		if meta, ok := pkg.Versions[latest]; ok {
			if d, ok := meta["description"].(string); ok {
				pkg.Description = d
			}
			if m, ok := meta["maintainers"].([]any); ok {
				pkg.Maintainers = m
			}
			if r, ok := meta["repository"].(map[string]any); ok {
				pkg.Repository = r
			}
		}
	}

	if readme, _ := s.ReadReadme(name); readme != "" {
		pkg.Readme = readme
	}
	return pkg, nil
}

// PackumentJSON encodes the packument as a compact JSON byte slice
// ready for the wire. Indent is intentionally absent — npm clients
// don't pretty-print, and shrinking the payload pays off on every
// `npm install`.
func PackumentJSON(pkg *Packument) ([]byte, error) {
	return json.Marshal(pkg)
}
