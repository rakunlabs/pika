package cluster

import (
	"testing"

	"github.com/rakunlabs/bw"
)

func TestStatusDisabledReportsStandalone(t *testing.T) {
	t.Parallel()

	c, err := New(Config{}, nil)
	if err != nil {
		t.Fatalf("New disabled cluster: %v", err)
	}

	status := c.Status()
	if status.Enabled {
		t.Fatalf("status.Enabled = true, want false")
	}
	if status.Role != "standalone" {
		t.Fatalf("status.Role = %q, want standalone", status.Role)
	}
	if !status.HasQuorum || !status.IsLeader || !status.LeaderHealthy {
		t.Fatalf("disabled status should be healthy single-node: %+v", status)
	}
	if status.OnlineNodes != 1 || len(status.Nodes) != 1 || !status.Nodes[0].Self {
		t.Fatalf("disabled status nodes mismatch: %+v", status.Nodes)
	}
	if status.Config.Port != 5000 || status.Config.LockKey != "pika-leader" || status.Config.Prefix != "pika" {
		t.Fatalf("defaults not resolved in status config: %+v", status.Config)
	}
}

func TestStatusEnabledBeforeStart(t *testing.T) {
	t.Parallel()

	db, err := bw.Open("", bw.WithInMemory(true), bw.WithLogger(nil))
	if err != nil {
		t.Fatalf("bw.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	c, err := New(Config{
		Enabled:     true,
		BindAddr:    "127.0.0.1",
		Replicas:    3,
		SecurityKey: "secret",
	}, db)
	if err != nil {
		t.Fatalf("New enabled cluster: %v", err)
	}

	status := c.Status()
	if !status.Enabled {
		t.Fatalf("status.Enabled = false, want true")
	}
	if status.Role != "follower" {
		t.Fatalf("status.Role = %q, want follower", status.Role)
	}
	if status.HasQuorum {
		t.Fatalf("status.HasQuorum = true before peers connect, want false")
	}
	if status.ExpectedReplicas != 3 || status.QuorumNodesRequired != 2 {
		t.Fatalf("quorum fields mismatch: %+v", status)
	}
	if status.Config.Port != 5000 || !status.Config.SecurityEnabled {
		t.Fatalf("resolved config mismatch: %+v", status.Config)
	}
	if status.OnlineNodes != 1 || len(status.Nodes) != 1 || !status.Nodes[0].Self {
		t.Fatalf("status nodes mismatch: %+v", status.Nodes)
	}
}
