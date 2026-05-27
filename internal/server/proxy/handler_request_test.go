package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHTTPRequestHandler_PassThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo-Method", r.Method)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "hello upstream")
	}))
	defer upstream.Close()

	cfg := mustJSON(t, map[string]any{
		"url":    upstream.URL + "/abc",
		"method": "GET",
	})
	h, err := buildHTTPRequestHandler(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/whatever")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "hello upstream" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, string(body))
	}
	if resp.Header.Get("X-Echo-Method") != "GET" {
		t.Fatalf("upstream header missing: %v", resp.Header)
	}
}

func TestHTTPRequestHandler_TemplatedURL(t *testing.T) {
	var got atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := mustJSON(t, map[string]any{
		"url":    upstream.URL + `/{{ index .req.headers "X-Tenant" }}/data`,
		"method": "GET",
	})
	h, err := buildHTTPRequestHandler(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/in", nil)
	req.Header.Set("X-Tenant", "acme")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got.Load() != "/acme/data" {
		t.Fatalf("templated url miss: got %v", got.Load())
	}
}

func TestHTTPRequestHandler_BasicAuth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "alice" || p != "secret" {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	cfg := mustJSON(t, map[string]any{
		"url":    upstream.URL,
		"method": "GET",
		"auth":   map[string]any{"kind": "basic", "basic": map[string]string{"user": "alice", "password": "secret"}},
	})
	h, err := buildHTTPRequestHandler(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestHTTPRequestHandler_BodyTemplate(t *testing.T) {
	var seen atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen.Store(string(b))
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := mustJSON(t, map[string]any{
		"url":            upstream.URL,
		"method":         "POST",
		"parse_payload":  true,
		"body_template":  `{"name":{{ .payload.name | printf "%q" }},"path":{{ .req.path | printf "%q" }}}`,
	})
	h, err := buildHTTPRequestHandler(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/in", "application/json", strings.NewReader(`{"name":"acme"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	want := `{"name":"acme","path":"/in"}`
	if got, _ := seen.Load().(string); got != want {
		t.Fatalf("body template:\n got=%s\nwant=%s", got, want)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
