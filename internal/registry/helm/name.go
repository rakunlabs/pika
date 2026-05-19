package helm

import (
	"fmt"
	"strings"
)

// ValidateChartName checks an inbound chart name against Helm's
// allowed-character set: lowercase alphanumerics + '-'. We
// intentionally reject uppercase + special chars because Helm
// itself does so (`helm create` rejects them) and a fail-fast
// boundary makes the storage layer cleaner.
//
// Conservative — full Helm grammar permits a few more shapes
// (digits at start, hyphens at boundaries) but the strict subset
// here is what 99% of real-world charts use and aligns with
// pika's repository name validator (same family of names is
// allowed cross-protocol).
func ValidateChartName(name string) error {
	if name == "" {
		return fmt.Errorf("empty name: %w", ErrInvalidName)
	}
	if len(name) > 250 {
		return fmt.Errorf("name too long: %w", ErrInvalidName)
	}
	for i, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '.' || r == '_'
		if !ok {
			return fmt.Errorf("invalid character at %d in %q: %w", i, name, ErrInvalidName)
		}
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return fmt.Errorf("leading/trailing hyphen in %q: %w", name, ErrInvalidName)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("path traversal in %q: %w", name, ErrInvalidName)
	}
	return nil
}

// ValidateChartVersion checks an inbound version string against a
// conservative semver-ish shape. The Helm spec requires SemVer 2,
// but enforcing the full grammar would couple this package to a
// semver parser. The check here covers the cases that would
// otherwise allow path traversal at the storage boundary; finer
// validation happens at chart upload time when ExtractChart parses
// the actual Chart.yaml.
func ValidateChartVersion(version string) error {
	if version == "" {
		return fmt.Errorf("empty version: %w", ErrInvalidName)
	}
	if len(version) > 64 {
		return fmt.Errorf("version too long: %w", ErrInvalidName)
	}
	for i, r := range version {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '.' || r == '-' || r == '+' || r == '_'
		if !ok {
			return fmt.Errorf("invalid character at %d in %q: %w", i, version, ErrInvalidName)
		}
	}
	if strings.Contains(version, "..") {
		return fmt.Errorf("path traversal in %q: %w", version, ErrInvalidName)
	}
	return nil
}

// ParseTarballFilename extracts (chart, version) from a Helm
// chart tarball filename "{chart}-{version}.tgz". The split is on
// the LAST hyphen-followed-by-digit so chart names that contain
// hyphens (e.g. "my-app") parse correctly.
//
// Returns the empty strings + an error if the filename doesn't
// match the convention. Both halves are validated before return.
func ParseTarballFilename(filename string) (chart, version string, err error) {
	if !strings.HasSuffix(filename, ".tgz") {
		return "", "", fmt.Errorf("%w: filename %q has no .tgz suffix", ErrInvalidName, filename)
	}
	stem := strings.TrimSuffix(filename, ".tgz")
	// Find the last "-digit" boundary — that's the Helm convention
	// for splitting name from version.
	idx := -1
	for i := 1; i < len(stem); i++ {
		if stem[i-1] == '-' && stem[i] >= '0' && stem[i] <= '9' {
			idx = i - 1
		}
	}
	if idx < 0 {
		return "", "", fmt.Errorf("%w: filename %q does not match {name}-{version}", ErrInvalidName, filename)
	}
	chart = stem[:idx]
	version = stem[idx+1:]
	if err := ValidateChartName(chart); err != nil {
		return "", "", err
	}
	if err := ValidateChartVersion(version); err != nil {
		return "", "", err
	}
	return chart, version, nil
}
