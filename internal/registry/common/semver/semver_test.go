package semver_test

import (
	"reflect"
	"testing"

	"github.com/rakunlabs/pika/internal/registry/common/semver"
)

func TestCompare_Strict(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.3", 0},
		{"v1.2.3", "v1.2.4", -1},
		{"v1.2.4", "v1.2.3", 1},
		{"v1.2.3", "v1.2.3-rc.1", 1},  // release > prerelease
		{"v1.2.3-rc.1", "v1.2.3", -1}, // prerelease < release
		{"v1.2.3-alpha", "v1.2.3-beta", -1},
		{"v2.0.0", "v1.99.99", 1},
		// Build metadata ignored.
		{"v1.2.3+sha.abc", "v1.2.3", 0},
		// Missing "v" → lex fallback under Strict.
		{"1.2.3", "v1.2.3", -1},
	}
	for _, c := range cases {
		got := semver.Compare(c.a, c.b, semver.ModeStrict)
		if got != c.want {
			t.Errorf("Compare(%q, %q, Strict) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCompare_Lenient(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		// "v" prefix optional and equivalent.
		{"v1.2.3", "1.2.3", 0},
		{"1.2.3", "v1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.2.3", "1.2.3-rc.1", 1},
		// Shorter version → trailing zeros.
		{"1.2", "1.2.1", -1},
		{"1.2", "1.2.0", 0},
	}
	for _, c := range cases {
		got := semver.Compare(c.a, c.b, semver.ModeLenient)
		if got != c.want {
			t.Errorf("Compare(%q, %q, Lenient) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSortDesc(t *testing.T) {
	in := []string{"v1.2.3", "v0.1.0", "v2.0.0", "v1.10.0", "v1.2.3-rc.1"}
	want := []string{"v2.0.0", "v1.10.0", "v1.2.3", "v1.2.3-rc.1", "v0.1.0"}
	semver.SortDesc(in, semver.ModeStrict)
	if !reflect.DeepEqual(in, want) {
		t.Errorf("SortDesc = %v, want %v", in, want)
	}
}

func TestSortDesc_LenientNPM(t *testing.T) {
	// NPM-flavoured versions — no "v" prefix.
	in := []string{"1.2.3", "0.1.0", "2.0.0", "1.10.0"}
	want := []string{"2.0.0", "1.10.0", "1.2.3", "0.1.0"}
	semver.SortDesc(in, semver.ModeLenient)
	if !reflect.DeepEqual(in, want) {
		t.Errorf("SortDesc = %v, want %v", in, want)
	}
}
