package external

import "testing"

// Guards GCP.GetRawValue and GCP.GetContentType against accidental
// default changes. Both defaults are user-visible:
//   - GetRawValue() default is false (legacy `{"value": "..."}`
//     wrapper preserved). Flipping to true would silently break
//     External.svelte:301 and InheritDialog.svelte:152-153, both
//     of which read `entry.data.value` on wrapper backends.
//   - GetContentType() default is "application/yaml". Changing it
//     would surprise raw-mode operators whose dashboards parse YAML
//     bodies based on the content-type header.

func boolPtr(b bool) *bool { return &b }

func TestGCPGetRawValue(t *testing.T) {
	tests := []struct {
		name string
		cfg  *GCP
		want bool
	}{
		{"nil receiver defaults to false (legacy wrapper)", nil, false},
		{"nil RawValue defaults to false (legacy wrapper)", &GCP{}, false},
		{"explicit true opts into raw mode", &GCP{RawValue: boolPtr(true)}, true},
		{"explicit false matches default", &GCP{RawValue: boolPtr(false)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.GetRawValue(); got != tt.want {
				t.Fatalf("GetRawValue() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGCPGetContentType(t *testing.T) {
	tests := []struct {
		name string
		cfg  *GCP
		want string
	}{
		{"nil receiver defaults to yaml", nil, "application/yaml"},
		{"empty defaults to yaml", &GCP{}, "application/yaml"},
		{"whitespace falls back to yaml", &GCP{ContentType: "   "}, "application/yaml"},
		{"explicit value preserved", &GCP{ContentType: "application/json"}, "application/json"},
		{"whitespace is trimmed", &GCP{ContentType: "  text/plain  "}, "text/plain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.GetContentType(); got != tt.want {
				t.Fatalf("GetContentType() = %q, want %q", got, tt.want)
			}
		})
	}
}
