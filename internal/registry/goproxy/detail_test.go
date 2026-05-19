package goproxy

import (
	"context"
	"errors"
	"testing"

	"github.com/rakunlabs/pika/internal/registry"
)

// TestLocal_PackageDetail_SemverSortedAndPopulated verifies that
// PackageDetail returns versions newest-first using the semver
// comparator (so v10 ranks above v2), surfaces the latest pointer,
// and pulls per-version sizes from the on-disk .info/.mod/.zip.
func TestLocal_PackageDetail_SemverSortedAndPopulated(t *testing.T) {
	l := newLocal(t, true)
	mod := "example.com/foo/bar"
	// Upload v1.0.0, v2.0.0, v10.0.0 — semver order should put
	// v10 first, lex order would put v2 first.
	for _, ver := range []string{"v1.0.0", "v2.0.0", "v10.0.0"} {
		uploadInfo(t, l, mod, ver)
		uploadMod(t, l, mod, ver, "module "+mod+"\n\ngo 1.21\n")
		uploadZip(t, l, mod, ver, []byte("zipbody-"+ver))
	}
	det, err := l.PackageDetail(context.Background(), mod)
	if err != nil {
		t.Fatalf("PackageDetail: %v", err)
	}
	if det.Type != "go" || det.Name != mod {
		t.Fatalf("envelope: %+v", det)
	}
	if det.Go == nil {
		t.Fatalf("nil Go")
	}
	if det.Go.LatestVersion != "v10.0.0" {
		t.Errorf("LatestVersion=%q want v10.0.0", det.Go.LatestVersion)
	}
	if len(det.Go.Versions) != 3 {
		t.Fatalf("versions=%d want 3", len(det.Go.Versions))
	}
	if det.Go.Versions[0].Version != "v10.0.0" {
		t.Errorf("first=%q want v10.0.0", det.Go.Versions[0].Version)
	}
	if det.Go.Versions[2].Version != "v1.0.0" {
		t.Errorf("last=%q want v1.0.0", det.Go.Versions[2].Version)
	}
	if det.Go.Versions[0].GoModSize == 0 {
		t.Errorf("v10 gomod_size=0")
	}
	if det.Go.Versions[0].ZipSize == 0 {
		t.Errorf("v10 zip_size=0")
	}
	if det.Go.Versions[0].PublishedAt == "" {
		t.Errorf("v10 published_at empty")
	}
}

// TestLocal_PackageDetail_RetractedVersionsParsed checks that
// retract directives in the latest version's go.mod surface as
// per-version retracted flags in the detail document.
func TestLocal_PackageDetail_RetractedVersionsParsed(t *testing.T) {
	l := newLocal(t, true)
	mod := "example.com/foo/bar"
	uploadInfo(t, l, mod, "v1.0.0")
	uploadMod(t, l, mod, "v1.0.0", "module "+mod+"\n")
	uploadInfo(t, l, mod, "v1.0.1")
	uploadMod(t, l, mod, "v1.0.1", "module "+mod+"\n\nretract v1.0.0 // security fix\n")
	det, err := l.PackageDetail(context.Background(), mod)
	if err != nil {
		t.Fatalf("PackageDetail: %v", err)
	}
	// v1.0.0 should be retracted; v1.0.1 should not.
	var v100, v101 registry.GoVersionDetail
	for _, v := range det.Go.Versions {
		if v.Version == "v1.0.0" {
			v100 = v
		}
		if v.Version == "v1.0.1" {
			v101 = v
		}
	}
	if !v100.Retracted {
		t.Errorf("v1.0.0 not marked retracted")
	}
	if v100.RetractionRationale != "security fix" {
		t.Errorf("rationale=%q want %q", v100.RetractionRationale, "security fix")
	}
	if v101.Retracted {
		t.Errorf("v1.0.1 wrongly retracted")
	}
}

// TestLocal_PackageDetail_NotFound verifies the sentinel comes
// through wrapping. The API layer keys on this to return 404.
func TestLocal_PackageDetail_NotFound(t *testing.T) {
	l := newLocal(t, true)
	_, err := l.PackageDetail(context.Background(), "example.com/missing")
	if !errors.Is(err, registry.ErrPackageNotFound) {
		t.Errorf("err=%v want ErrPackageNotFound", err)
	}
}

// TestLocal_PackageDetail_BadModuleIsInvalid verifies that an
// invalid module path (uppercase, blank segments) maps to
// ErrInvalidPackageName so the HTTP handler can return 400 instead
// of 500. Before L4 the bad-module case bubbled as a generic
// goproxy error, producing 500 on what is really a client bug.
func TestLocal_PackageDetail_BadModuleIsInvalid(t *testing.T) {
	l := newLocal(t, true)
	// Empty path triggers ValidateModulePath.
	_, err := l.PackageDetail(context.Background(), "")
	if !errors.Is(err, registry.ErrInvalidPackageName) {
		t.Errorf("err=%v want ErrInvalidPackageName", err)
	}
}

// TestCompareVersions_SemverOrdering covers a handful of cases the
// goproxy lex sort would mis-rank.
func TestCompareVersions_SemverOrdering(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v2.0.0", -1},
		{"v2.0.0", "v10.0.0", -1},
		{"v1.0.0-alpha", "v1.0.0", -1}, // pre-release sorts below release
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.0+build", "v1.0.0", 0}, // build metadata ignored
		{"v1.2.3", "v1.2.4", -1},
	}
	for _, c := range cases {
		got := CompareVersions(c.a, c.b)
		// Normalise to sign — only the ordering relation matters.
		sign := func(n int) int {
			switch {
			case n < 0:
				return -1
			case n > 0:
				return 1
			}
			return 0
		}
		if sign(got) != c.want {
			t.Errorf("CompareVersions(%q, %q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestCachedLatest_PicksSemverHighest verifies the data-plane
// @latest endpoint returns v10 (not v2) when both are present.
// Pre-B4 this used lex sort and was wrong.
func TestCachedLatest_PicksSemverHighest(t *testing.T) {
	l := newLocal(t, true)
	mod := "example.com/foo"
	for _, ver := range []string{"v1.0.0", "v2.0.0", "v10.0.0"} {
		uploadInfo(t, l, mod, ver)
	}
	body, err := l.store.CachedLatest(mod, 0)
	if err != nil {
		t.Fatalf("CachedLatest: %v", err)
	}
	// Body is the {Version,Time} JSON from v10.0.0's .info.
	if !contains(string(body), "v10.0.0") {
		t.Errorf("latest body=%q want v10.0.0", string(body))
	}
}

// contains is a tiny helper to keep the test free of an extra
// strings import.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestParseGoMod_RetractBlock covers both the single-line and
// block forms of the retract directive.
func TestParseGoMod_RetractBlock(t *testing.T) {
	body := []byte(`module example.com/foo

retract v1.0.0 // bad release

retract (
    v1.1.0 // typo
    v1.1.1
)
`)
	info := ParseGoMod(body)
	if info.Module != "example.com/foo" {
		t.Errorf("module=%q", info.Module)
	}
	if len(info.Retracts) != 3 {
		t.Fatalf("retracts=%d want 3", len(info.Retracts))
	}
	if info.Retracts[0].Version != "v1.0.0" || info.Retracts[0].Rationale != "bad release" {
		t.Errorf("retract[0]=%+v", info.Retracts[0])
	}
	if info.Retracts[1].Version != "v1.1.0" || info.Retracts[1].Rationale != "typo" {
		t.Errorf("retract[1]=%+v", info.Retracts[1])
	}
	if info.Retracts[2].Version != "v1.1.1" {
		t.Errorf("retract[2]=%+v", info.Retracts[2])
	}
}
