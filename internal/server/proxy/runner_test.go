package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// freePort grabs an unused TCP port and immediately closes the
// listener. Race-prone in theory; in practice the test grabs+uses
// the port within a few milliseconds and any conflict shows up as
// the actual listener failing — which the test also checks.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// waitFor200 polls a URL until it returns 200 or the timeout expires.
// Necessary because the ada server starts asynchronously inside the
// runner goroutine; the test would otherwise race the listen call.
func waitFor200(t *testing.T, url string, timeout time.Duration) *http.Response {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil && resp.StatusCode == 200 {
			return resp
		}
		if resp != nil {
			resp.Body.Close()
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("URL %s did not return 200 in %s: last err %v", url, timeout, lastErr)
	return nil
}

func TestManager_StartStopReconcile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	port := freePort(t)
	mgr := NewManager(ctx, &fakeService{}, "127.0.0.1", nil)

	// One server, single healthz handler.
	srv := ProxyServer{
		ID: "alpha", Name: "Alpha", Enabled: true, Host: "127.0.0.1", Port: port,
		Nodes: []ProxyNode{
			{ID: "l", Type: NodeTypeListener},
			{ID: "h", Type: NodeTypeHandler, Subtype: "healthz", Config: json.RawMessage(`{"path":"/health/*","body":"alpha-ok"}`)},
		},
		Edges: []ProxyEdge{{ID: "e1", Source: "l", Target: "h"}},
	}
	if err := mgr.Reconcile([]ProxyServer{srv}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	resp := waitFor200(t, "http://127.0.0.1:"+port+"/health/anything", 3*time.Second)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "alpha-ok" {
		t.Fatalf("body: got %q", string(body))
	}

	// Reconcile with the server removed; the listener must shut down.
	if err := mgr.Reconcile(nil); err != nil {
		t.Fatalf("Reconcile (empty): %v", err)
	}
	// Give shutdown a moment.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, err := http.Get("http://127.0.0.1:" + port + "/health/x")
		if err != nil {
			return // listener gone — success
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("listener still reachable after reconcile-to-empty")
}

func TestManager_HashStableNoRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	port := freePort(t)
	mgr := NewManager(ctx, &fakeService{}, "127.0.0.1", nil)

	mk := func(label string) ProxyServer {
		return ProxyServer{
			ID: "x", Enabled: true, Host: "127.0.0.1", Port: port,
			Nodes: []ProxyNode{
				{ID: "l", Type: NodeTypeListener},
				{ID: "h", Type: NodeTypeHandler, Subtype: "healthz", Config: json.RawMessage(`{"path":"/h/*","body":"` + label + `"}`)},
			},
			Edges: []ProxyEdge{{ID: "e1", Source: "l", Target: "h"}},
		}
	}

	if err := mgr.Reconcile([]ProxyServer{mk("v1")}); err != nil {
		t.Fatal(err)
	}
	waitFor200(t, "http://127.0.0.1:"+port+"/h/a", 3*time.Second).Body.Close()

	statusBefore := mgr.Status()
	if len(statusBefore) != 1 {
		t.Fatalf("status len: %d", len(statusBefore))
	}
	startedBefore := statusBefore[0].Started

	// Same graph again -> hash matches -> no restart, Started should
	// not change.
	if err := mgr.Reconcile([]ProxyServer{mk("v1")}); err != nil {
		t.Fatal(err)
	}
	if got := mgr.Status()[0].Started; !got.Equal(startedBefore) {
		t.Fatalf("Started changed unexpectedly: %v -> %v", startedBefore, got)
	}

	// Change the body -> hash differs -> restart, Started must update.
	if err := mgr.Reconcile([]ProxyServer{mk("v2")}); err != nil {
		t.Fatal(err)
	}
	// Wait for the new server to be reachable (the old one shut
	// down + new one bound the same port).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://127.0.0.1:" + port + "/h/a")
		if err == nil && resp.StatusCode == 200 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if strings.Contains(string(body), "v2") {
				if got := mgr.Status()[0].Started; got.Equal(startedBefore) {
					t.Fatal("Started not updated after restart")
				}
				return
			}
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("v2 body never observed")
}

func TestManager_DisabledServerNotStarted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	port := freePort(t)
	mgr := NewManager(ctx, &fakeService{}, "127.0.0.1", nil)
	srv := ProxyServer{
		ID: "off", Enabled: false, Host: "127.0.0.1", Port: port,
		Nodes: []ProxyNode{
			{ID: "l", Type: NodeTypeListener},
			{ID: "h", Type: NodeTypeHandler, Subtype: "healthz"},
		},
		Edges: []ProxyEdge{{ID: "e1", Source: "l", Target: "h"}},
	}
	if err := mgr.Reconcile([]ProxyServer{srv}); err != nil {
		t.Fatal(err)
	}
	// Should be unreachable.
	if _, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 200*time.Millisecond); err == nil {
		t.Fatal("disabled server listening")
	}
}

func TestManager_ValidateReservedPort(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := NewManager(ctx, &fakeService{}, "127.0.0.1", []string{"8080"})
	srv := ProxyServer{
		ID: "x", Enabled: true, Port: "8080",
		Nodes: []ProxyNode{
			{ID: "l", Type: NodeTypeListener},
			{ID: "h", Type: NodeTypeHandler, Subtype: "healthz"},
		},
		Edges: []ProxyEdge{{ID: "e1", Source: "l", Target: "h"}},
	}
	_, err := mgr.Validate(srv)
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("want reserved port error, got %v", err)
	}
}
