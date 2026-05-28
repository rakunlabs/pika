package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rakunlabs/chu"
	"github.com/rakunlabs/pika/internal/cluster"
)

// TestMarshalMapStripsLogDashFields guards against accidentally
// removing `log:"-"` from a sensitive field. The "loaded
// configuration" log line is fed through chu.MarshalMap; if any
// secret-marked field leaks here, it lands in operator log streams
// (and from there into log-aggregation, backup, support bundles…).
//
// Strategy: stuff a unique sentinel into every `log:"-"` field,
// marshal the whole config, and assert no sentinel appears anywhere
// in the serialized output. Every entry in this table corresponds to
// one field that MUST stay masked; removing one without removing the
// `log:"-"` tag on the field will fail this test.
func TestMarshalMapStripsLogDashFields(t *testing.T) {
	const (
		clusterPSK         = "DO-NOT-LEAK-THIS-PSK-9c5f6d3a"
		encryptionPassword = "DO-NOT-LEAK-THIS-ENCRYPTION-PW-7b2e8a"
	)

	cfg := Config{
		LogLevel: "info",
		Cluster: cluster.Config{
			SecurityKey: clusterPSK,
		},
		Encryption: Encryption{
			Password: encryptionPassword,
		},
	}

	out, err := json.Marshal(chu.MarshalMap(cfg))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, sentinel := range []string{clusterPSK, encryptionPassword} {
		if strings.Contains(string(out), sentinel) {
			t.Fatalf("masked field leaked into MarshalMap output (sentinel %q):\n%s", sentinel, out)
		}
	}
}

func TestNormalizeBasePath(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{name: "empty is root", in: "", want: ""},
		{name: "slash is root", in: "/", want: ""},
		{name: "trailing slash", in: "/pika/", want: "/pika"},
		{name: "nested", in: "/admin/pika", want: "/admin/pika"},
		{name: "missing leading slash", in: "pika", wantErr: "must start with /"},
		{name: "query", in: "/pika?x=1", wantErr: "without query or fragment"},
		{name: "fragment", in: "/pika#app", wantErr: "without query or fragment"},
		{name: "empty segment", in: "/admin//pika", wantErr: "empty path segments"},
		{name: "whitespace inside", in: "/admin pika", wantErr: "must not contain whitespace"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeBasePath(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("normalizeBasePath(%q) error = %v, want containing %q", tt.in, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeBasePath(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("normalizeBasePath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
