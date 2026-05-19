package helm

import (
	"context"

	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/common/semver"
)

// buildPackageDetail collects per-chart metadata into the shared
// registry.PackageDetail shape. Shared between Local and Remote.
func buildPackageDetail(_ context.Context, s *Store, name string) (*registry.PackageDetail, error) {
	versions, err := s.ListVersions(name)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, registry.ErrPackageNotFound
	}
	sortVersionsDesc(versions)
	latest := versions[0]
	out := &registry.PackageDetail{
		Type: "helm",
		Name: name,
		Helm: &registry.HelmChartDetail{
			LatestVersion: latest,
			HasReadme:     s.HasReadme(name),
			Versions:      make([]registry.HelmVersionDetail, 0, len(versions)),
		},
	}
	// Top-level fields from the latest version's Chart.yaml.
	if meta, err := s.ReadMetadata(name, latest); err == nil {
		out.Helm.Description = meta.Description
		out.Helm.Icon = meta.Icon
		out.Helm.AppVersion = meta.AppVersion
		out.Helm.Keywords = meta.Keywords
		out.Helm.Maintainers = meta.Maintainers
	}
	for _, v := range versions {
		row := registry.HelmVersionDetail{Version: v}
		if meta, err := s.ReadMetadata(name, v); err == nil {
			row.AppVersion = meta.AppVersion
			row.Description = meta.Description
		}
		row.Created = s.TarballModTime(name, v)
		row.Size = s.TarballSize(name, v)
		out.Helm.Versions = append(out.Helm.Versions, row)
	}
	return out, nil
}

// sortVersionsDesc orders the slice newest-first using the shared
// common/semver comparator in lenient mode (Helm chart versions
// follow the same "v" prefix is optional convention as npm).
func sortVersionsDesc(v []string) {
	semver.SortDesc(v, semver.ModeLenient)
}
