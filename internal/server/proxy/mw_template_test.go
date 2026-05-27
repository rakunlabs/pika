package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTemplateTransform_RequestBody(t *testing.T) {
	cfg := mustJSON(t, map[string]any{
		"parse_payload": true,
		"request": map[string]any{
			"body": `{"name":{{ .payload.name | printf "%q" }},"count":42}`,
		},
	})
	mw, err := buildTemplateTransformMW(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	var seen string
	terminal := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = string(b)
	})
	h := mw(terminal)
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(`{"name":"alpha"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	want := `{"name":"alpha","count":42}`
	if seen != want {
		t.Fatalf("body:\n got=%s\nwant=%s", seen, want)
	}
}

func TestTemplateTransform_ResponseStatusAndHeaders(t *testing.T) {
	cfg := mustJSON(t, map[string]any{
		"response": map[string]any{
			"status":  "201",
			"headers": map[string]string{"X-Pika": "yes"},
		},
	})
	mw, err := buildTemplateTransformMW(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	terminal := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	srv := httptest.NewServer(mw(terminal))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Pika") != "yes" {
		t.Fatalf("missing X-Pika header: %v", resp.Header)
	}
}
