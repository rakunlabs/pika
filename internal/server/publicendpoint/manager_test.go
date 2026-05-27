package publicendpoint

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/service"
)

// stubService is a minimal Service for tests. It serves a small
// in-memory map of "key -> {data, format}" and accepts any bearer
// token whose value matches AllowedToken.
type stubService struct {
	files        map[string]stubFile
	externals    map[string]map[string]*external.Entry
	allowedToken string
}

type stubFile struct {
	data   []byte
	format string
}

func (s *stubService) GetData(ctx context.Context, key, version, variant string) (*service.DataResult, error) {
	f, ok := s.files[key]
	if !ok {
		return nil, service.ErrNotFound
	}
	return &service.DataResult{Data: f.data, Format: f.format}, nil
}

func (s *stubService) ReadExternal(ctx context.Context, resourceName, path string) (*external.Entry, error) {
	res, ok := s.externals[resourceName]
	if !ok {
		return nil, service.ErrNotFound
	}
	entry, ok := res[path]
	if !ok {
		return nil, service.ErrNotFound
	}
	return entry, nil
}

func (s *stubService) ReadExternalVersion(ctx context.Context, resourceName, path, version string) (*external.Entry, error) {
	return s.ReadExternal(ctx, resourceName, path+"@"+version)
}

func (s *stubService) ValidateToken(ctx context.Context, raw, scope, op string) error {
	if s.allowedToken == "" || raw != s.allowedToken {
		return service.ErrUnauthorized
	}
	return nil
}

// freePort grabs an OS-assigned port and immediately closes the
// listener; the test then reuses the port number for the manager's
// own bind. There is a tiny race window but it is acceptable for
// in-process tests.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	_ = ln.Close()
	return addr.Port
}

func TestManager_ConsulShim_RoundTrip(t *testing.T) {
	stub := &stubService{
		files: map[string]stubFile{
			"myapp/config": {data: []byte("database:\n  host: localhost\n"), format: "yaml"},
		},
	}
	port := freePort(t)
	ep := service.PublicEndpoint{
		ID: "ep1", Name: "ok", Enabled: true,
		ListenHost: "127.0.0.1", ListenPort: port,
		BasePath: "/consul", Mode: "consul",
		Consul: &service.ConsulCompat{},
		Auth:   service.EndpointAuth{Mode: "none"},
	}
	mgr := New(context.Background(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })

	if err := mgr.Reload(context.Background(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}

	// Default Consul envelope
	body, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/consul/v1/kv/myapp/config", port), nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var envelope []map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("envelope decode: %v body=%s", err, body)
	}
	if len(envelope) != 1 {
		t.Fatalf("expected single entry, got %d", len(envelope))
	}
	if envelope[0]["Key"] != "myapp/config" {
		t.Errorf("Key=%v", envelope[0]["Key"])
	}
	val, _ := envelope[0]["Value"].(string)
	dec, err := base64.StdEncoding.DecodeString(val)
	if err != nil {
		t.Fatalf("decode Value: %v", err)
	}
	if !strings.Contains(string(dec), "host: localhost") {
		t.Errorf("decoded value missing payload: %q", dec)
	}

	// ?raw returns raw bytes with format-mapped Content-Type.
	rawBody, rawStatus := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/consul/v1/kv/myapp/config?raw", port), nil)
	if rawStatus != http.StatusOK {
		t.Fatalf("?raw status=%d body=%s", rawStatus, rawBody)
	}
	if !strings.Contains(string(rawBody), "host: localhost") {
		t.Errorf("?raw body missing payload: %q", rawBody)
	}

	// 404 returns empty body.
	missBody, missStatus := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/consul/v1/kv/missing", port), nil)
	if missStatus != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", missStatus, missBody)
	}
	if len(missBody) != 0 {
		t.Errorf("expected empty body on 404, got %q", missBody)
	}
}

func TestManager_CustomShim_Template(t *testing.T) {
	stub := &stubService{
		files: map[string]stubFile{
			"hello": {data: []byte("world"), format: "raw"},
		},
	}
	port := freePort(t)
	ep := service.PublicEndpoint{
		ID: "epc", Name: "custom", Enabled: true,
		ListenHost: "127.0.0.1", ListenPort: port,
		BasePath: "/cfg", Mode: "custom",
		Custom: &service.CustomCompat{
			BodyTemplate: `{"k":"{{ .Key }}","b64":"{{ .DataB64 }}","found":{{ .Found }}}`,
			ContentType:  "application/json",
		},
		Auth: service.EndpointAuth{Mode: "none"},
	}
	mgr := New(context.Background(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })

	if err := mgr.Reload(context.Background(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}

	body, status := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/cfg/hello", port), nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if got["k"] != "hello" {
		t.Errorf("k=%v", got["k"])
	}
	if got["found"] != true {
		t.Errorf("found=%v", got["found"])
	}
	wantB64 := base64.StdEncoding.EncodeToString([]byte("world"))
	if got["b64"] != wantB64 {
		t.Errorf("b64=%v want=%v", got["b64"], wantB64)
	}
}

func TestManager_StaticTokenAuth(t *testing.T) {
	stub := &stubService{files: map[string]stubFile{"x": {data: []byte("hi"), format: "raw"}}}
	port := freePort(t)
	ep := service.PublicEndpoint{
		ID: "ep2", Name: "auth", Enabled: true,
		ListenHost: "127.0.0.1", ListenPort: port,
		BasePath: "/", Mode: "consul",
		Consul: &service.ConsulCompat{},
		Auth: service.EndpointAuth{
			Mode:         "static_token",
			StaticTokens: []string{"secret-1"},
			HeaderName:   "X-Consul-Token",
		},
	}
	mgr := New(context.Background(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })

	if err := mgr.Reload(context.Background(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload: %v", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/v1/kv/x?raw", port)
	// Missing token → 401
	_, status := httpGet(t, url, nil)
	if status != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", status)
	}
	// Wrong token → 401
	_, status = httpGet(t, url, http.Header{"X-Consul-Token": []string{"nope"}})
	if status != http.StatusUnauthorized {
		t.Errorf("expected 401 with bad token, got %d", status)
	}
	// Correct token → 200
	_, status = httpGet(t, url, http.Header{"X-Consul-Token": []string{"secret-1"}})
	if status != http.StatusOK {
		t.Errorf("expected 200 with good token, got %d", status)
	}
}

func TestManager_Reload_DisabledStops(t *testing.T) {
	stub := &stubService{files: map[string]stubFile{"k": {data: []byte("x"), format: "raw"}}}
	port := freePort(t)
	ep := service.PublicEndpoint{
		ID: "ep3", Name: "tog", Enabled: true,
		ListenHost: "127.0.0.1", ListenPort: port,
		BasePath: "/", Mode: "consul",
		Consul: &service.ConsulCompat{},
		Auth:   service.EndpointAuth{Mode: "none"},
	}
	mgr := New(context.Background(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })

	if err := mgr.Reload(context.Background(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload 1: %v", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/v1/kv/k?raw", port)
	if _, status := httpGet(t, url, nil); status != http.StatusOK {
		t.Fatalf("initial GET expected 200, got %d", status)
	}

	ep.Enabled = false
	if err := mgr.Reload(context.Background(), []service.PublicEndpoint{ep}); err != nil {
		t.Fatalf("reload 2: %v", err)
	}

	// Listener should be gone now — give it a moment for OS cleanup.
	time.Sleep(50 * time.Millisecond)
	if _, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 250*time.Millisecond); err == nil {
		t.Errorf("expected port %d to be free after disable", port)
	}
	st := mgr.Status()
	if len(st) != 1 {
		t.Fatalf("expected 1 status row, got %d", len(st))
	}
	if st[0].Running {
		t.Errorf("expected Running=false after disable, got true")
	}
}

func TestManager_DuplicateBindReportedInStatus(t *testing.T) {
	stub := &stubService{}
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("setup listener: %v", err)
	}
	port := occupied.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() { _ = occupied.Close() })

	ep := service.PublicEndpoint{
		ID: "epx", Name: "boom", Enabled: true,
		ListenHost: "127.0.0.1", ListenPort: port,
		BasePath: "/", Mode: "consul",
		Consul: &service.ConsulCompat{},
		Auth:   service.EndpointAuth{Mode: "none"},
	}
	mgr := New(context.Background(), stub, nil)
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })

	err = mgr.Reload(context.Background(), []service.PublicEndpoint{ep})
	if err == nil {
		t.Fatalf("expected bind failure error, got nil")
	}
	st := mgr.Status()
	if len(st) != 1 || st[0].Running || st[0].LastError == "" {
		t.Fatalf("expected status with running=false + last_error, got %#v", st)
	}
}

// httpGet is a tiny helper that returns body + status, failing the
// test on any transport-level error. Used so individual test cases
// stay short.
func httpGet(t *testing.T, url string, hdr http.Header) ([]byte, int) {
	t.Helper()
	// Retry briefly in case the manager has not finished binding.
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("new req: %v", err)
		}
		for k, vs := range hdr {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(25 * time.Millisecond)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return body, resp.StatusCode
	}
	t.Fatalf("GET %s: %v", url, lastErr)
	return nil, 0
}

// Sanity: ensure the Service interface compiles against the real *service.Service.
// (Build-time check; never invoked at runtime.)
var _ Service = (*service.Service)(nil)

// Compile-time guard: stubService satisfies Service.
var _ Service = (*stubService)(nil)

// Reference http.NewServeMux to keep import alive.
var _ = httptest.NewRecorder
