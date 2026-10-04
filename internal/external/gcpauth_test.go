package external

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func testServiceAccountJSON(t *testing.T, tokenURI string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	b, _ := json.Marshal(gcpServiceAccountKey{
		Type:        "service_account",
		ProjectID:   "proj",
		ClientEmail: "sa@proj.iam.gserviceaccount.com",
		PrivateKey:  string(pemKey),
		TokenURI:    tokenURI,
	})
	return string(b)
}

func TestNewGCPAuthValidation(t *testing.T) {
	if _, _, err := newGCPAuth("gcp", "{", http.DefaultClient); err == nil || !strings.HasPrefix(err.Error(), "gcp:") {
		t.Fatalf("bad JSON: err = %v", err)
	}
	if _, _, err := newGCPAuth("gcp-parameter", `{"type":"user"}`, http.DefaultClient); err == nil || !strings.HasPrefix(err.Error(), "gcp-parameter:") {
		t.Fatalf("wrong type: err = %v", err)
	}
}

func TestGCPAuthTokenCacheAndInvalidate(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := r.ParseForm(); err != nil || r.Form.Get("assertion") == "" {
			http.Error(w, "missing assertion", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(gcpTokenResponse{AccessToken: "tok", ExpiresIn: 3600})
	}))
	t.Cleanup(srv.Close)

	auth, projectID, err := newGCPAuth("gcp", testServiceAccountJSON(t, srv.URL), srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if projectID != "proj" {
		t.Fatalf("projectID = %q", projectID)
	}

	for range 2 {
		if err := auth.ensureToken(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 || auth.accessToken() != "tok" {
		t.Fatalf("token not cached: calls=%d token=%q", calls.Load(), auth.accessToken())
	}

	auth.invalidateToken()
	if err := auth.ensureToken(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("invalidate did not force refresh: calls=%d", calls.Load())
	}
}
