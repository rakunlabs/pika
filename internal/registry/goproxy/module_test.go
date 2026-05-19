package goproxy

import (
	"strings"
	"testing"
)

func TestEncodeModulePath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"github.com/foo/bar", "github.com/foo/bar"},
		{"github.com/Azure/azure-sdk-for-go", "github.com/!azure/azure-sdk-for-go"},
		{"github.com/MakeNowJust/heredoc", "github.com/!make!now!just/heredoc"},
		{"golang.org/x/sync", "golang.org/x/sync"},
		{"", ""},
		{"ABC", "!a!b!c"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := EncodeModulePath(tc.in)
			if got != tc.want {
				t.Fatalf("EncodeModulePath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDecodeModulePath(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"github.com/foo/bar", "github.com/foo/bar", false},
		{"github.com/!azure/azure-sdk-for-go", "github.com/Azure/azure-sdk-for-go", false},
		{"github.com/!make!now!just/heredoc", "github.com/MakeNowJust/heredoc", false},
		{"!a!b!c", "ABC", false},
		{"foo!", "", true},        // trailing !
		{"foo!1bar", "", true},    // ! not followed by lowercase letter
		{"foo!Bar", "", true},     // ! followed by uppercase
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := DecodeModulePath(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("DecodeModulePath(%q) err=%v wantErr=%v", tc.in, err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Fatalf("DecodeModulePath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	originals := []string{
		"github.com/Azure/azure-sdk-for-go",
		"github.com/foo/bar",
		"k8s.io/api",
		"gopkg.in/yaml.v3",
		"golang.org/x/net",
	}
	for _, orig := range originals {
		t.Run(orig, func(t *testing.T) {
			encoded := EncodeModulePath(orig)
			decoded, err := DecodeModulePath(encoded)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if decoded != orig {
				t.Fatalf("round-trip mismatch: %q → %q → %q", orig, encoded, decoded)
			}
		})
	}
}

func TestValidateModulePath(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"valid simple", "github.com/foo/bar", false},
		{"valid versioned", "github.com/foo/bar/v2", false},
		{"valid with caps", "github.com/Azure/foo", false}, // caps allowed pre-encoding
		{"empty", "", true},
		{"dot dot", "github.com/foo/../bar", true},
		{"leading slash", "/github.com/foo", true},
		{"trailing slash", "github.com/foo/", true},
		{"whitespace", "github.com/foo bar", true},
		{"empty segment", "github.com//bar", true},
		{"tab", "github.com/foo\tbar", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateModulePath(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateModulePath(%q) err=%v wantErr=%v", tc.in, err, tc.wantErr)
			}
		})
	}
}

func TestValidateVersion(t *testing.T) {
	cases := []struct {
		v       string
		wantErr bool
	}{
		{"v1.0.0", false},
		{"v1.2.3", false},
		{"v0.0.0-20240101000000-abcdef012345", false},
		{"v1.2.3-rc.1", false},
		{"v1.2.3-0.20240101000000-abcdef012345", false},
		{"v1.2.3+incompatible", false},
		{"v2.0.0+build.42", false},
		{"", true},
		{"1.2.3", true},   // no v prefix
		{"latest", true},  // not a literal version
		{"v1.2.3 ", true}, // trailing space
		{"v1.2.3/x", true},
		{"v1..2.3", true},
	}
	for _, tc := range cases {
		t.Run(tc.v, func(t *testing.T) {
			err := ValidateVersion(tc.v)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateVersion(%q) err=%v wantErr=%v", tc.v, err, tc.wantErr)
			}
			if tc.wantErr && err != nil && !strings.Contains(err.Error(), "version") {
				t.Errorf("error should mention version: %v", err)
			}
		})
	}
}
