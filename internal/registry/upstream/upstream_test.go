package upstream

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/pika/internal/service"
)

// stubResolver is a SecretResolver that returns canned plaintexts
// for raw:// and config:// references. Used by tests that exercise the
// auth path without depending on the real secret store.
type stubResolver struct {
	values map[string]string
}

func (s *stubResolver) ResolveSecret(_ context.Context, ref string) (string, error) {
	if v, ok := s.values[ref]; ok {
		return v, nil
	}
	return "", errors.New("unknown secret ref: " + ref)
}

func TestNewClient_RequiresBaseURL(t *testing.T) {
	if _, err := NewClient(Config{}); err == nil {
		t.Fatal("expected error for empty base url")
	}
}

func TestClient_GetReturnsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/foo/bar" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"abc"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c, err := NewClient(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	resp, err := c.Get(context.Background(), "/foo/bar")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if resp.ContentType != "application/json" {
		t.Fatalf("content-type %q", resp.ContentType)
	}
	if resp.ETag != `"abc"` {
		t.Fatalf("etag %q", resp.ETag)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body %q", body)
	}
}

func TestClient_404IsErrNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c, _ := NewClient(Config{BaseURL: srv.URL})
	_, err := c.Get(context.Background(), "/missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestClient_401Is401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c, _ := NewClient(Config{BaseURL: srv.URL})
	_, err := c.Get(context.Background(), "/protected")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestClient_5xxIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c, _ := NewClient(Config{BaseURL: srv.URL})
	_, err := c.Get(context.Background(), "/up")
	if !errors.Is(err, ErrTransient) {
		t.Fatalf("expected ErrTransient, got %v", err)
	}
}

func TestClient_BasicAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "alice" || p != "s3cret" {
			t.Errorf("bad auth: u=%q p=%q ok=%v", u, p, ok)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _ := NewClient(Config{
		BaseURL: srv.URL,
		Auth: &service.RegistryUpstreamAuth{
			Type: service.RegistryAuthBasic, Username: "alice", Password: "s3cret",
		},
	})
	resp, err := c.Get(context.Background(), "/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()
}

func TestClient_BasicAuthWithUsernameRef(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "resolved-user" || p != "resolved-pass" {
			t.Errorf("bad auth: u=%q p=%q ok=%v", u, p, ok)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resolver := &stubResolver{values: map[string]string{
		"config://upstream/docker#/auth/user": "resolved-user",
		"raw://upstream/docker-pass":          "resolved-pass",
	}}
	c, _ := NewClient(Config{
		BaseURL: srv.URL,
		Auth: &service.RegistryUpstreamAuth{
			Type:     service.RegistryAuthBasic,
			Username: "config://upstream/docker#/auth/user",
			Password: "raw://upstream/docker-pass",
		},
		Resolver: resolver,
	})
	resp, err := c.Get(context.Background(), "/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()
}

func TestClient_BearerWithRawRef(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if got != "Bearer real-token-from-secret" {
			t.Errorf("Authorization=%q", got)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resolver := &stubResolver{values: map[string]string{
		"raw://upstream/go/token": "real-token-from-secret",
	}}
	c, _ := NewClient(Config{
		BaseURL:  srv.URL,
		Auth:     &service.RegistryUpstreamAuth{Type: service.RegistryAuthBearer, Token: "raw://upstream/go/token"},
		Resolver: resolver,
	})
	resp, err := c.Get(context.Background(), "/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()
}

func TestClient_BearerInlineToken(t *testing.T) {
	// Without a supported reference prefix the value is used verbatim, even
	// when a resolver is configured.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer plain-text-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _ := NewClient(Config{
		BaseURL:  srv.URL,
		Auth:     &service.RegistryUpstreamAuth{Type: service.RegistryAuthBearer, Token: "plain-text-token"},
		Resolver: &stubResolver{},
	})
	resp, err := c.Get(context.Background(), "/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()
}

func TestClient_CustomHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Key") != "abc123" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _ := NewClient(Config{
		BaseURL: srv.URL,
		Auth: &service.RegistryUpstreamAuth{
			Type: service.RegistryAuthHeader, Header: "X-Internal-Key", Value: "abc123",
		},
	})
	resp, err := c.Get(context.Background(), "/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()
}

func TestClient_AbsoluteURLPassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("from-absolute"))
	}))
	defer srv.Close()

	// Build a client with a base URL that points to a black hole;
	// then fetch via an absolute URL. The absolute URL should be
	// used verbatim instead of being joined to the base.
	c, _ := NewClient(Config{BaseURL: "http://127.0.0.1:1"})
	resp, err := c.Get(context.Background(), srv.URL+"/foo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "from-absolute") {
		t.Fatalf("body %q", body)
	}
}

func TestClient_Head(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("method %s", r.Method)
		}
		w.Header().Set("Content-Length", "1234")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _ := NewClient(Config{BaseURL: srv.URL})
	resp, err := c.Head(context.Background(), "/probe")
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestClient_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _ := NewClient(Config{BaseURL: srv.URL, Timeout: 50 * time.Millisecond})
	_, err := c.Get(context.Background(), "/slow")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestClient_ResolveSecretError(t *testing.T) {
	c, _ := NewClient(Config{
		BaseURL:  "http://127.0.0.1:1",
		Auth:     &service.RegistryUpstreamAuth{Type: service.RegistryAuthBearer, Token: "raw://unknown"},
		Resolver: &stubResolver{values: map[string]string{}},
	})
	_, err := c.Get(context.Background(), "/")
	if err == nil || !strings.Contains(err.Error(), "resolve bearer token") {
		t.Fatalf("expected secret resolution error, got %v", err)
	}
}

func TestClient_SecretSchemeRejected(t *testing.T) {
	c, _ := NewClient(Config{
		BaseURL:  "http://127.0.0.1:1",
		Auth:     &service.RegistryUpstreamAuth{Type: service.RegistryAuthBearer, Token: "secret://old-style"},
		Resolver: &stubResolver{},
	})
	_, err := c.Get(context.Background(), "/")
	if err == nil || !strings.Contains(err.Error(), "secret:// references are no longer supported") {
		t.Fatalf("expected secret:// rejection, got %v", err)
	}
}

func TestProbe_AuthChallengeIsReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="registry"`)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("auth required"))
	}))
	defer srv.Close()

	c, _ := NewClient(Config{BaseURL: srv.URL})
	health := Probe(context.Background(), c, "/v2/")
	if !health.OK {
		t.Fatalf("expected reachable auth challenge, got %+v", health)
	}
	if health.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", health.StatusCode)
	}
	if !strings.Contains(health.BodyPreview, "auth required") {
		t.Fatalf("preview %q", health.BodyPreview)
	}
}

func TestProbe_ServerErrorKeepsStatusAndPreview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("bad gateway from proxy"))
	}))
	defer srv.Close()

	c, _ := NewClient(Config{BaseURL: srv.URL})
	health := Probe(context.Background(), c, "/-/ping")
	if health.OK {
		t.Fatalf("expected failed probe, got %+v", health)
	}
	if health.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d", health.StatusCode)
	}
	if !strings.Contains(health.BodyPreview, "bad gateway") {
		t.Fatalf("preview %q", health.BodyPreview)
	}
}
