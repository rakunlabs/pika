package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/rakunlabs/pika/internal/service"
)

// TestManager_HostRouting attaches two HTTP graphs to one listener
// and verifies that the right graph fires based on Host header.
func TestManager_HostRouting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	port := freePort(t)
	mgr := NewManager(ctx, &fakeService{}, "127.0.0.1", nil)

	listener := service.ProxyListener{
		ID: "ln1", Name: "shared", Enabled: true,
		Protocol: "http", Host: "127.0.0.1", Port: port,
	}
	mkGraph := func(id, body string, hosts []string) service.ProxyServer {
		return service.ProxyServer{
			ID: id, Enabled: true, ListenerID: listener.ID, Protocol: "http",
			HostMatch: hosts,
			Nodes: []service.ProxyNode{
				{ID: "l", Type: NodeTypeListener},
				{ID: "h", Type: NodeTypeHandler, Subtype: "healthz",
					Config: json.RawMessage(`{"path":"/h/*","body":"` + body + `"}`)},
			},
			Edges: []service.ProxyEdge{{ID: "e1", Source: "l", Target: "h"}},
		}
	}
	graphs := []service.ProxyServer{
		mkGraph("a", "alpha", []string{"alpha.example"}),
		mkGraph("b", "beta", []string{"*.beta.example"}),
		mkGraph("c", "catch", nil), // catch-all
	}
	if err := mgr.ReconcileAll([]service.ProxyListener{listener}, graphs); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	hit := func(host string) string {
		// Poll until listener is bound.
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			req, _ := http.NewRequest("GET", "http://127.0.0.1:"+port+"/h/x", nil)
			req.Host = host
			resp, err := http.DefaultClient.Do(req)
			if err == nil && resp.StatusCode == 200 {
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				return string(b)
			}
			if resp != nil {
				resp.Body.Close()
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatalf("listener did not respond for host %s", host)
		return ""
	}

	if got := hit("alpha.example"); got != "alpha" {
		t.Fatalf("alpha exact: got %q", got)
	}
	if got := hit("foo.beta.example"); got != "beta" {
		t.Fatalf("beta suffix: got %q", got)
	}
	if got := hit("anything.else"); got != "catch" {
		t.Fatalf("catch-all: got %q", got)
	}
}

// TestManager_ListenerDeleteBlocksOnAttachedGraphs checks that the
// runtime does not error when a listener disappears with graphs
// attached — the graphs get recorded with a per-row error but the
// listener teardown still happens cleanly.
func TestManager_OrphanedGraphsAreRecorded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := NewManager(ctx, &fakeService{}, "127.0.0.1", nil)

	graph := service.ProxyServer{
		ID: "g1", Enabled: true, ListenerID: "missing", Protocol: "http",
		Nodes: []service.ProxyNode{
			{ID: "l", Type: NodeTypeListener},
			{ID: "h", Type: NodeTypeHandler, Subtype: "healthz"},
		},
		Edges: []service.ProxyEdge{{ID: "e1", Source: "l", Target: "h"}},
	}
	err := mgr.ReconcileAll(nil, []service.ProxyServer{graph})
	if err == nil || !contains(err.Error(), "listener missing not found") {
		t.Fatalf("want missing-listener error, got %v", err)
	}
	st := mgr.Status()
	if len(st) != 1 || st[0].LastErr == "" {
		t.Fatalf("graph error not recorded: %+v", st)
	}
}

func TestSynthesizeLegacyListeners(t *testing.T) {
	graphs := []service.ProxyServer{
		{ID: "a", Host: "127.0.0.1", Port: "9001", Protocol: "http"},
		{ID: "b", Host: "127.0.0.1", Port: "9001", Protocol: "http"},
		{ID: "c", Host: "127.0.0.1", Port: "9002", Protocol: "tcp"},
		{ID: "d", ListenerID: "ln-x", Protocol: "http"},
	}
	listeners := synthesizeLegacyListeners(graphs, "127.0.0.1")
	if len(listeners) != 2 {
		t.Fatalf("want 2 synthesized listeners, got %d (%+v)", len(listeners), listeners)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
outer:
	for i := 0; i+len(sub) <= len(s); i++ {
		for j := 0; j < len(sub); j++ {
			if s[i+j] != sub[j] {
				continue outer
			}
		}
		return i
	}
	return -1
}
