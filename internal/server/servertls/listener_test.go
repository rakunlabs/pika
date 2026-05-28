package servertls

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestOptionalListener_HTTPSOnlyRejectsPlainHTTP(t *testing.T) {
	addr, stop := startTestServer(t, Policy{HTTPS: true, PlainHTTP: false})
	defer stop()

	status, body := testGET(t, "https://"+addr, true)
	if status != http.StatusOK || body != "ok" {
		t.Fatalf("https status=%d body=%q", status, body)
	}

	status, body = testGET(t, "http://"+addr, false)
	if status != http.StatusUpgradeRequired {
		t.Fatalf("plain http status=%d body=%q", status, body)
	}
}

func TestOptionalListener_AllowsPlainHTTPWhenPolicyAllows(t *testing.T) {
	addr, stop := startTestServer(t, Policy{HTTPS: true, PlainHTTP: true})
	defer stop()

	status, body := testGET(t, "http://"+addr, false)
	if status != http.StatusOK || body != "ok" {
		t.Fatalf("plain http status=%d body=%q", status, body)
	}
}

func startTestServer(t *testing.T, policy Policy) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	mgr := New(Options{
		CertFile:     filepath.Join(t.TempDir(), "server.crt"),
		KeyFile:      filepath.Join(t.TempDir(), "server.key"),
		DefaultNames: []string{"127.0.0.1", "localhost"},
	})
	tlsConfig, err := mgr.TLSConfig()
	if err != nil {
		t.Fatalf("tls config: %v", err)
	}
	srv := &http.Server{
		TLSConfig: tlsConfig,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ok"))
		}),
	}
	go func() {
		_ = srv.Serve(NewOptionalListener(ln, tlsConfig, func() Policy { return policy }))
	}()
	return ln.Addr().String(), func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

func testGET(t *testing.T, url string, insecure bool) (int, string) {
	t.Helper()
	client := http.DefaultClient
	if insecure {
		client = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}
