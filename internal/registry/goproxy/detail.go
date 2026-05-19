package goproxy

import (
	"context"
	"fmt"

	"github.com/rakunlabs/pika/internal/registry"
)

// buildPackageDetail collects per-module metadata into the shared
// registry.PackageDetail shape. It is the single source of truth
// for both Local.PackageDetail and Remote.PackageDetail so the two
// kinds surface the same fields with the same semantics — the only
// difference is which Store they back onto.
//
// Returns (nil, registry.ErrPackageNotFound) when the module has
// no versions stored. Errors from the version walk are non-fatal:
// versions whose .info/.mod can't be read are still listed with
// whatever subset of metadata is available.
func buildPackageDetail(_ context.Context, s *Store, module string) (*registry.PackageDetail, error) {
	if err := ValidateModulePath(module); err != nil {
		// Wrap both ErrBadModule (protocol-specific detail) and
		// registry.ErrInvalidPackageName (cross-protocol sentinel
		// the HTTP handler maps to 400).
		return nil, fmt.Errorf("%w: %w: %w", err, ErrBadModule, registry.ErrInvalidPackageName)
	}
	versions, err := s.ListVersions(module)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, registry.ErrPackageNotFound
	}
	SortVersionsDesc(versions)
	out := &registry.PackageDetail{
		Type: "go",
		Name: module,
		Go: &registry.GoModuleDetail{
			LatestVersion: versions[0],
			Versions:      make([]registry.GoVersionDetail, 0, len(versions)),
		},
	}
	// Build a fast retract lookup from each version's go.mod. We
	// parse every .mod once to gather the union of retract
	// directives — historically a retract added in v1.3.0 applies
	// to v1.2.x retroactively, so the freshest go.mod has the most
	// authoritative list.
	retractMap := map[string]string{} // version → rationale
	if latest := out.Go.LatestVersion; latest != "" {
		if body, err := s.ReadGoMod(module, latest); err == nil {
			info := ParseGoMod(body)
			for _, r := range info.Retracts {
				retractMap[r.Version] = r.Rationale
			}
		}
	}
	for _, v := range versions {
		row := registry.GoVersionDetail{Version: v}
		if info, err := s.ReadVersionInfo(module, v); err == nil {
			if !info.Time.IsZero() {
				row.PublishedAt = info.Time.UTC().Format("2006-01-02T15:04:05Z")
			}
		}
		row.GoModSize = s.VersionSize(module, v, "mod")
		row.ZipSize = s.VersionSize(module, v, "zip")
		if rationale, retracted := retractMap[v]; retracted {
			row.Retracted = true
			row.RetractionRationale = rationale
		}
		out.Go.Versions = append(out.Go.Versions, row)
	}
	return out, nil
}
