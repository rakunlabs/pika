package service

import (
	"fmt"
	"strconv"
	"strings"
)

// Semver represents a parsed semantic version.
type Semver struct {
	Major int
	Minor int
	Patch int
}

// ParseSemver parses a version string like "0.2.5", "1.0", "v1.2.3".
// Supports 1, 2, or 3 components. Missing components default to 0.
func ParseSemver(s string) (Semver, error) {
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimSpace(s)

	if s == "" {
		return Semver{}, fmt.Errorf("empty version string")
	}

	parts := strings.SplitN(s, ".", 3)
	var sv Semver

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return Semver{}, fmt.Errorf("invalid major version %q: %w", parts[0], err)
	}
	sv.Major = major

	if len(parts) >= 2 {
		minor, err := strconv.Atoi(parts[1])
		if err != nil {
			return Semver{}, fmt.Errorf("invalid minor version %q: %w", parts[1], err)
		}
		sv.Minor = minor
	}

	if len(parts) >= 3 {
		// Handle pre-release suffixes by taking only the numeric part
		patchStr := parts[2]
		if idx := strings.IndexAny(patchStr, "-+"); idx >= 0 {
			patchStr = patchStr[:idx]
		}
		patch, err := strconv.Atoi(patchStr)
		if err != nil {
			return Semver{}, fmt.Errorf("invalid patch version %q: %w", patchStr, err)
		}
		sv.Patch = patch
	}

	return sv, nil
}

// Compare returns -1 if a < b, 0 if a == b, 1 if a > b.
func (a Semver) Compare(b Semver) int {
	if a.Major != b.Major {
		if a.Major < b.Major {
			return -1
		}
		return 1
	}
	if a.Minor != b.Minor {
		if a.Minor < b.Minor {
			return -1
		}
		return 1
	}
	if a.Patch != b.Patch {
		if a.Patch < b.Patch {
			return -1
		}
		return 1
	}
	return 0
}

// GTE returns true if a >= b.
func (a Semver) GTE(b Semver) bool {
	return a.Compare(b) >= 0
}

func (a Semver) String() string {
	return fmt.Sprintf("%d.%d.%d", a.Major, a.Minor, a.Patch)
}

// ParseConstraint parses a constraint string like ">= 0.2.5".
// Currently only supports ">=" operator.
// Returns the operator and the parsed semver.
func ParseConstraint(s string) (string, Semver, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", Semver{}, fmt.Errorf("empty constraint")
	}

	// Try ">= X.Y.Z"
	if strings.HasPrefix(s, ">=") {
		versionStr := strings.TrimSpace(s[2:])
		sv, err := ParseSemver(versionStr)
		if err != nil {
			return "", Semver{}, fmt.Errorf("invalid constraint %q: %w", s, err)
		}
		return ">=", sv, nil
	}

	// Try just a version number (treated as ">= X.Y.Z")
	sv, err := ParseSemver(s)
	if err != nil {
		return "", Semver{}, fmt.Errorf("invalid constraint %q: %w", s, err)
	}
	return ">=", sv, nil
}

// SatisfiesConstraint checks if requestedVersion satisfies the constraint.
// For ">= 0.2.5": returns true if requestedVersion >= 0.2.5.
func SatisfiesConstraint(requestedVersion Semver, constraint string) bool {
	op, constraintVersion, err := ParseConstraint(constraint)
	if err != nil {
		return false
	}

	switch op {
	case ">=":
		return requestedVersion.GTE(constraintVersion)
	default:
		return false
	}
}

// IsSemverString checks if a string looks like a semver (not a plain integer version).
func IsSemverString(s string) bool {
	s = strings.TrimPrefix(s, "v")
	return strings.Contains(s, ".")
}

// ResolveVersionBySemver finds the correct version for a given semver request.
//
// Algorithm:
// Walk versions from newest to oldest. When we encounter a version with a
// constraint that the requested semver does NOT satisfy, that version and all
// versions above it (until we find a satisfied constraint or unconstrained version)
// are excluded. The result is the latest non-deleted version that falls within
// a satisfied constraint boundary.
//
// Example:
//
//	v1 (no constraint)      → applies to all
//	v3 (>= 0.1.0)          → boundary starts at 0.1.0
//	v5 (>= 0.2.0)          → boundary starts at 0.2.0
//	v8 (>= 0.2.5)          → boundary starts at 0.2.5
//
//	Request 0.2.4 → v7 (latest before v8's unsatisfied boundary)
//	Request 0.2.5 → v8 (satisfies v8's constraint)
//	Request 0.0.9 → v2 (before v3's boundary)
//	Request 0.1.5 → v4 (between v3 and v5 boundaries)
func ResolveVersionBySemver(versions FileVersions, requestedStr string) (*FileVersion, error) {
	requested, err := ParseSemver(requestedStr)
	if err != nil {
		return nil, fmt.Errorf("invalid version %q: %w", requestedStr, err)
	}

	if len(versions) == 0 {
		return nil, ErrNotFound
	}

	// Walk from newest to oldest.
	// When we hit an unsatisfied constraint, we mark everything above as blocked.
	// When we find a version that's valid (either unconstrained or has a satisfied constraint),
	// and we're not blocked, that's our answer.
	for i := len(versions) - 1; i >= 0; i-- {
		v := versions[i]

		// Skip deleted versions
		if len(v.Status) > 0 && v.Status[len(v.Status)-1].Status == FileStatusTypeDeleted {
			continue
		}

		if v.Constraint != "" {
			if !SatisfiesConstraint(requested, v.Constraint) {
				// This constraint is not satisfied. Everything from here up is too new.
				// Keep searching backwards for an older constraint boundary.
				continue
			}
			// Constraint satisfied. This version is valid but we want the latest
			// version in this constraint range. Walk forward from here to find
			// the latest version before the next unsatisfied constraint.
			return findLatestInRange(versions, i, requested)
		}
	}

	// No constraints found at all, or all were unsatisfied.
	// Return the latest valid unconstrained version.
	for i := len(versions) - 1; i >= 0; i-- {
		v := versions[i]
		if len(v.Status) > 0 && v.Status[len(v.Status)-1].Status == FileStatusTypeDeleted {
			continue
		}
		if v.Constraint == "" {
			return &versions[i], nil
		}
	}

	return nil, ErrNotFound
}

// findLatestInRange finds the latest valid version starting from index `from`,
// walking forward until we hit a version whose constraint the requested semver
// doesn't satisfy (or we reach the end).
func findLatestInRange(versions FileVersions, from int, requested Semver) (*FileVersion, error) {
	var result *FileVersion

	for i := from; i < len(versions); i++ {
		v := versions[i]

		// Skip deleted
		if len(v.Status) > 0 && v.Status[len(v.Status)-1].Status == FileStatusTypeDeleted {
			continue
		}

		// If this version has a constraint we don't satisfy, stop here
		if v.Constraint != "" && i > from {
			if !SatisfiesConstraint(requested, v.Constraint) {
				break
			}
		}

		result = &versions[i]
	}

	if result != nil {
		return result, nil
	}

	return nil, ErrNotFound
}
