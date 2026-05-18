package service

import (
	"encoding/json"
	"testing"
)

// decodeWrappedValue is the narrow helper that unwraps the synthetic
// {"value": "<string>"} envelope produced by non-JSON external backends
// (Consul/Etcd/GCP/HTTP) and re-parses the inner string with an explicit
// format hint. Internal package test (not _test) so we can reach the
// unexported function directly.
//
// Each case documents one observable contract — the rest of the
// inheritance pipeline relies on these boundaries holding.
func TestDecodeWrappedValue(t *testing.T) {
	yamlPayload := "host: db.example.com\nport: 5432\n"
	jsonPayload := `{"host":"db.example.com","port":5432}`
	tomlPayload := "host = \"db.example.com\"\nport = 5432\n"
	wrap := func(raw string) []byte {
		b, _ := json.Marshal(map[string]any{"value": raw})
		return b
	}

	tests := []struct {
		name     string
		data     []byte
		format   string
		wantOK   bool
		wantHost string // checked when wantOK; "" means skip
	}{
		{
			name:     "yaml inside wrapper decodes",
			data:     wrap(yamlPayload),
			format:   "yaml",
			wantOK:   true,
			wantHost: "db.example.com",
		},
		{
			name:     "json inside wrapper decodes",
			data:     wrap(jsonPayload),
			format:   "json",
			wantOK:   true,
			wantHost: "db.example.com",
		},
		{
			name:     "toml inside wrapper decodes",
			data:     wrap(tomlPayload),
			format:   "toml",
			wantOK:   true,
			wantHost: "db.example.com",
		},
		{
			name:   "empty format is a no-op (backward compat)",
			data:   wrap(yamlPayload),
			format: "",
			wantOK: false,
		},
		{
			name:   "raw format is a no-op",
			data:   wrap(yamlPayload),
			format: "raw",
			wantOK: false,
		},
		{
			name:   "unknown format is a no-op",
			data:   wrap(yamlPayload),
			format: "ini",
			wantOK: false,
		},
		{
			name:   "non-wrapper object is left alone",
			data:   []byte(`{"host":"x","port":1}`),
			format: "yaml",
			wantOK: false,
		},
		{
			name:   "wrapper with object value is left alone (not a scalar)",
			data:   []byte(`{"value":{"host":"x"}}`),
			format: "yaml",
			wantOK: false,
		},
		{
			name:   "wrapper with multiple keys is not a wrapper",
			data:   []byte(`{"value":"x","extra":1}`),
			format: "yaml",
			wantOK: false,
		},
		{
			name:   "invalid inner payload returns ok=false (keeps original)",
			data:   wrap("this is : not : valid : yaml :: ["),
			format: "yaml",
			wantOK: false,
		},
		{
			name:   "garbage json input is left alone",
			data:   []byte("not json"),
			format: "yaml",
			wantOK: false,
		},
		{
			name:     "format trimming + case-insensitive",
			data:     wrap(jsonPayload),
			format:   "  JSON  ",
			wantOK:   true,
			wantHost: "db.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := decodeWrappedValue(tt.data, tt.format)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (got=%s)", ok, tt.wantOK, string(got))
			}
			if !tt.wantOK {
				// When the function declines to decode, it must hand back
				// the exact original bytes — callers rely on this to fall
				// through to the unmodified wrapper path.
				if string(got) != string(tt.data) {
					t.Fatalf("ok=false must return original bytes; got=%s want=%s", got, tt.data)
				}
				return
			}
			if tt.wantHost == "" {
				return
			}
			var obj map[string]any
			if err := json.Unmarshal(got, &obj); err != nil {
				t.Fatalf("decoded payload is not valid JSON: %v (raw=%s)", err, got)
			}
			if obj["host"] != tt.wantHost {
				t.Fatalf("host = %v, want %v (raw=%s)", obj["host"], tt.wantHost, got)
			}
		})
	}
}
