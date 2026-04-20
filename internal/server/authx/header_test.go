package authx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
)

func TestTrustedProxyGuard_Allows(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	wrapped := wrapTrustedProxies(inner, []string{"10.0.0.0/8"})

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.1.2.3:5000"
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("trusted: code %d", w.Code)
	}
}

func TestTrustedProxyGuard_Denies(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	wrapped := wrapTrustedProxies(inner, []string{"10.0.0.0/8"})

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.168.1.1:5000"
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("untrusted: code %d", w.Code)
	}
}

func TestTrustedProxyGuard_EmptyList_AllowAll(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	wrapped := wrapTrustedProxies(inner, nil)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "8.8.8.8:1234"
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("empty allowlist should allow: code %d", w.Code)
	}
}

func TestBuildHeader_Nil(t *testing.T) {
	if s := BuildHeader(nil); s != nil {
		t.Error("nil settings: expected nil")
	}
}

func TestBuildHeader_Defaults(t *testing.T) {
	s := BuildHeader(&service.HeaderStrategySettings{})
	if s == nil {
		t.Fatal("expected non-nil strategy")
	}
	if s.Name() != "header" {
		t.Errorf("name: %q", s.Name())
	}
}
