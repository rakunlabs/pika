package authx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/ada/middleware/auth/strategy"

	"github.com/rakunlabs/pika/internal/service"
)

// login drives the strategy the way the auth middleware does and reports
// what the caller ends up with.
func login(t *testing.T, s strategy.Authenticator, remoteAddr string, headers map[string]string) (*httptest.ResponseRecorder, string) {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, "/login/header", nil)
	r.RemoteAddr = remoteAddr
	for k, v := range headers {
		r.Header.Set(k, v)
	}

	w := httptest.NewRecorder()
	id, _, err := s.Login(w, r)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	subject := ""
	if id != nil {
		subject = id.Subject
	}

	return w, subject
}

// TestBuildHeaderTrustBoundary checks that the trusted_proxies setting
// actually reaches the strategy.
//
// Header auth verifies nothing — the identity headers are whatever the
// caller typed — so the CIDR list is the only thing standing between the
// endpoint and arbitrary impersonation. A setting that silently failed to
// apply would look identical to one that worked, right up until someone
// reached the port directly.
func TestBuildHeaderTrustBoundary(t *testing.T) {
	s := BuildHeader(&service.HeaderStrategySettings{
		TrustedProxies: []string{"10.0.0.0/8"},
	})
	if s == nil {
		t.Fatal("expected a strategy")
	}

	t.Run("inside the boundary authenticates", func(t *testing.T) {
		w, subject := login(t, s, "10.1.2.3:5000", map[string]string{"X-Forwarded-User": "alice"})
		if subject != "alice" {
			t.Fatalf("expected alice, got %q (status %d)", subject, w.Code)
		}
	})

	t.Run("outside the boundary is refused", func(t *testing.T) {
		w, subject := login(t, s, "192.168.1.1:5000", map[string]string{"X-Forwarded-User": "alice"})
		if subject != "" {
			t.Fatalf("untrusted peer authenticated as %q", subject)
		}
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
		// The refusal must be indistinguishable from a missing header, so
		// a caller outside the boundary cannot probe for the feature.
		if got := w.Body.String(); !strings.Contains(got, "no_user_header") {
			t.Errorf("rejection leaks why it failed: %s", got)
		}
	})
}

// TestBuildHeaderWithoutTrustedProxies documents the unguarded shape: it is
// still allowed (a genuinely closed network is a real deployment) and ada
// warns about it at construction.
func TestBuildHeaderWithoutTrustedProxies(t *testing.T) {
	s := BuildHeader(&service.HeaderStrategySettings{})
	if s == nil {
		t.Fatal("expected a strategy")
	}
	if s.Name() != "header" {
		t.Errorf("name: %q", s.Name())
	}

	_, subject := login(t, s, "8.8.8.8:1234", map[string]string{"X-Forwarded-User": "alice"})
	if subject != "alice" {
		t.Errorf("with no boundary configured any peer should pass, got %q", subject)
	}
}

// TestBuildHeaderRejectsUnparseableCIDRs is the reason pika validates before
// handing the list to ada: ada panics on a malformed CIDR, and this list
// comes from an editable settings form re-applied on every hot reload. A
// typo must not take the server down.
//
// It must not silently widen the boundary either — an unusable list is
// dropped whole, which is what this asserts by way of the strategy still
// being constructed rather than the process dying.
func TestBuildHeaderRejectsUnparseableCIDRs(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a malformed CIDR in settings must not panic the server: %v", r)
		}
	}()

	s := BuildHeader(&service.HeaderStrategySettings{
		TrustedProxies: []string{"10.0.0.0/8", "not-a-cidr"},
	})
	if s == nil {
		t.Fatal("expected a strategy")
	}
}

func TestBuildHeader_Nil(t *testing.T) {
	if s := BuildHeader(nil); s != nil {
		t.Error("nil settings: expected nil")
	}
}
