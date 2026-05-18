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
// Strategy: stuff a unique sentinel into Cluster.SecurityKey, marshal
// the whole config, and assert the sentinel does not appear anywhere
// in the serialized output.
func TestMarshalMapStripsLogDashFields(t *testing.T) {
	const sentinel = "DO-NOT-LEAK-THIS-PSK-9c5f6d3a"

	cfg := Config{
		LogLevel: "info",
		Cluster: cluster.Config{
			SecurityKey: sentinel,
		},
	}

	out, err := json.Marshal(chu.MarshalMap(cfg))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), sentinel) {
		t.Fatalf("cluster security key leaked into MarshalMap output:\n%s", out)
	}
}
