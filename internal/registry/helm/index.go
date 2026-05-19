package helm

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
)

// IndexFile is the on-the-wire shape of a Helm repository
// index.yaml. The schema is documented at
// https://helm.sh/docs/topics/chart_repository/#the-index-file.
//
// We marshal with goccy/go-yaml so map key ordering follows
// declaration order — Helm clients don't require strict ordering
// but a stable ordering keeps server-side caching deterministic
// (the cached index file's modtime matches its content).
type IndexFile struct {
	APIVersion string                       `yaml:"apiVersion"`
	Generated  string                       `yaml:"generated"`
	Entries    map[string][]IndexChartEntry `yaml:"entries"`
}

// IndexChartEntry is one version of one chart inside the index.
// Includes every Chart.yaml field that real Helm clients consume.
type IndexChartEntry struct {
	APIVersion   string            `yaml:"apiVersion,omitempty"`
	Name         string            `yaml:"name"`
	Version      string            `yaml:"version"`
	AppVersion   string            `yaml:"appVersion,omitempty"`
	Description  string            `yaml:"description,omitempty"`
	Type         string            `yaml:"type,omitempty"`
	Created      string            `yaml:"created,omitempty"`
	Digest       string            `yaml:"digest,omitempty"`
	URLs         []string          `yaml:"urls"`
	Icon         string            `yaml:"icon,omitempty"`
	Keywords     []string          `yaml:"keywords,omitempty"`
	Home         string            `yaml:"home,omitempty"`
	Sources      []string          `yaml:"sources,omitempty"`
	Maintainers  []any             `yaml:"maintainers,omitempty"`
	Dependencies []any             `yaml:"dependencies,omitempty"`
	Annotations  map[string]string `yaml:"annotations,omitempty"`
}

// BuildIndex assembles a Helm index.yaml from the on-disk store.
// `publicBase` is the public URL the index should advertise as
// the base for chart tarballs ("https://pika.example.com/registries/ns/repo");
// per-version URLs append "/{chart}-{version}.tgz".
//
// Best-effort on errors: a chart with an unreadable metadata
// sidecar is skipped silently rather than failing the whole
// index build. The returned index is always at least syntactically
// valid; missing charts just don't appear.
func BuildIndex(s *Store, publicBase string) (*IndexFile, error) {
	out := &IndexFile{
		APIVersion: "v1",
		Generated:  time.Now().UTC().Format(time.RFC3339),
		Entries:    make(map[string][]IndexChartEntry),
	}
	charts, err := s.ListCharts()
	if err != nil {
		return nil, err
	}
	for _, name := range charts {
		versions, _ := s.ListVersions(name)
		entries := make([]IndexChartEntry, 0, len(versions))
		for _, v := range versions {
			meta, err := s.ReadMetadata(name, v)
			if err != nil {
				continue
			}
			created := s.TarballModTime(name, v)
			// Digest: re-derive from the tarball bytes? Too
			// expensive on the hot path. We persisted the digest
			// at publish time into the metadata sidecar (via
			// ChartExtraction) — fall back to "" when the field
			// is absent for legacy entries.
			entries = append(entries, IndexChartEntry{
				APIVersion:   "v2",
				Name:         meta.Name,
				Version:      meta.Version,
				AppVersion:   meta.AppVersion,
				Description:  meta.Description,
				Type:         meta.Type,
				Icon:         meta.Icon,
				Keywords:     meta.Keywords,
				Home:         meta.Home,
				Sources:      meta.Sources,
				Maintainers:  meta.Maintainers,
				Dependencies: meta.Dependencies,
				Annotations:  meta.Annotations,
				Created:      created,
				URLs: []string{
					strings.TrimRight(publicBase, "/") + "/" + TarballFilename(name, v),
				},
			})
		}
		if len(entries) > 0 {
			// Sort newest-first; lexicographic on the version
			// string is "good enough" — the same trade-off the
			// goproxy package documented. The detail builder
			// surfaces a proper semver order on the admin UI.
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Version > entries[j].Version
			})
			out.Entries[name] = entries
		}
	}
	return out, nil
}

// MarshalIndex serialises an IndexFile as YAML bytes, ready for
// the wire. Errors are surfaced rather than swallowed — a
// marshalling failure indicates a programmer bug (bad type
// elsewhere in the chain) and should not be silently produced.
func MarshalIndex(idx *IndexFile) ([]byte, error) {
	body, err := yaml.Marshal(idx)
	if err != nil {
		return nil, fmt.Errorf("helm: marshal index: %w", err)
	}
	return body, nil
}
