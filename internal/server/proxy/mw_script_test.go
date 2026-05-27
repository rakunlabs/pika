package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSScript_SetHeaderAndStatus(t *testing.T) {
	cfg := mustJSON(t, map[string]any{
		"script": `
		  ctx.request.setHeader("X-From-Script", "hi");
		  ctx.response.setStatus(202);
		`,
	})
	mw, err := buildJSScriptMW(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sawHeader string
	terminal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHeader = r.Header.Get("X-From-Script")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "downstream")
	})
	srv := httptest.NewServer(mw(terminal))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if sawHeader != "hi" {
		t.Fatalf("downstream did not see mutated header, got %q", sawHeader)
	}
	if resp.StatusCode != 202 {
		t.Fatalf("status override miss, got %d", resp.StatusCode)
	}
}

func TestJSScript_ShortCircuit(t *testing.T) {
	cfg := mustJSON(t, map[string]any{
		"script": `
		  ctx.response.setStatus(418);
		  ctx.response.setBody({error: "teapot"});
		`,
	})
	mw, err := buildJSScriptMW(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	terminal := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("downstream should not be called on short-circuit")
	})
	srv := httptest.NewServer(mw(terminal))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 418 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "teapot") {
		t.Fatalf("body: %s", body)
	}
}

func TestJSScript_Timeout(t *testing.T) {
	cfg := mustJSON(t, map[string]any{
		"timeout_ms": 20,
		"script":     `while (true) {}`,
	})
	mw, err := buildJSScriptMW(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	terminal := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})
	srv := httptest.NewServer(mw(terminal))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 from timeout, got %d", resp.StatusCode)
	}
}
