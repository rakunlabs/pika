package npm

import (
	"errors"
	"testing"
)

func TestValidatePackageName(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"plain", "lodash", false},
		{"hyphen", "lodash-es", false},
		{"dot", "react.use", false},
		{"underscore-mid", "my_lib", false},
		{"scoped", "@types/node", false},
		{"empty", "", true},
		{"leading-dot", ".sneaky", true},
		{"leading-underscore", "_private", true},
		{"upper", "MyLib", true},
		{"space", "my lib", true},
		{"slash-no-scope", "foo/bar", true},
		{"scoped-bad-scope-empty", "@/name", true},
		{"scoped-no-name", "@scope/", true},
		{"scoped-upper", "@Scope/name", true},
		{"too-long", string(make([]byte, 215)), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePackageName(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidatePackageName(%q) err=%v wantErr=%v", tc.in, err, tc.wantErr)
			}
			if tc.wantErr && err != nil && !errors.Is(err, ErrInvalidPackage) {
				t.Errorf("expected ErrInvalidPackage in chain, got %v", err)
			}
		})
	}
}

func TestParseNameFromPath(t *testing.T) {
	cases := []struct {
		in        string
		wantName  string
		wantRest  string
		wantOK    bool
	}{
		{"/lodash", "lodash", "", true},
		{"/lodash/-/lodash-1.0.0.tgz", "lodash", "/-/lodash-1.0.0.tgz", true},
		{"/@types/node", "@types/node", "", true},
		{"/@types/node/-/node-20.0.0.tgz", "@types/node", "/-/node-20.0.0.tgz", true},
		{"/@types%2Fnode", "@types/node", "", true},
		{"/@types%2Fnode/-/node-20.0.0.tgz", "@types/node", "/-/node-20.0.0.tgz", true},
		{"", "", "", false},
		{"/", "", "", false},
		{"/@scope", "", "", false},   // missing name
		{"/@scope/", "", "", false},  // empty name
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			name, rest, ok := ParseNameFromPath(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if name != tc.wantName || rest != tc.wantRest {
				t.Fatalf("got (%q,%q), want (%q,%q)", name, rest, tc.wantName, tc.wantRest)
			}
		})
	}
}
