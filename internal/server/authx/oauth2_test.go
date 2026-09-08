package authx

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/rakunlabs/ada/middleware/auth/strategy"

	"github.com/rakunlabs/pika/internal/service"
)

func TestBuildOAuth2ManualJWKS(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name        string
		jwks        bool
		userInfo    bool
		wrongKey    bool
		audience    string
		wantSuccess bool
	}{
		{name: "valid JWT without userinfo", jwks: true, audience: "client-id", wantSuccess: true},
		{name: "invalid signature", jwks: true, wrongKey: true, audience: "client-id"},
		{name: "invalid audience", jwks: true, audience: "other-client"},
		{name: "missing JWKS and userinfo", audience: "client-id"},
		{name: "userinfo without JWKS", userInfo: true, audience: "client-id", wantSuccess: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			signingKey := key
			if tc.wrongKey {
				signingKey = otherKey
			}
			signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: signingKey},
				(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key"))
			if err != nil {
				t.Fatal(err)
			}
			token, err := jwt.Signed(signer).Claims(map[string]any{
				"sub": "user-123", "name": "JWT User", "email": "user@example.com",
				"aud": tc.audience, "exp": time.Now().Add(time.Hour).Unix(),
			}).Serialize()
			if err != nil {
				t.Fatal(err)
			}
			var jwksHits, userInfoHits atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				var body any
				switch r.URL.Path {
				case "/token":
					if r.Method != http.MethodPost || r.FormValue("grant_type") != "password" {
						t.Errorf("unexpected token request: %s %s", r.Method, r.FormValue("grant_type"))
					}
					body = map[string]string{"access_token": "access-token", "token_type": "Bearer", "id_token": token}
				case "/jwks":
					jwksHits.Add(1)
					body = jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
						Key: &key.PublicKey, KeyID: "test-key", Algorithm: string(jose.ES256), Use: "sig",
					}}}
				case "/userinfo":
					userInfoHits.Add(1)
					if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
						t.Errorf("userinfo Authorization=%q", got)
					}
					body = map[string]string{"sub": "user-123", "name": "UserInfo User", "email": "user@example.com"}
				default:
					t.Errorf("unexpected IdP request: %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				if err := json.NewEncoder(w).Encode(body); err != nil {
					t.Errorf("encode IdP response: %v", err)
				}
			}))
			t.Cleanup(ts.Close)

			settings := service.OAuth2StrategySettings{
				Name: "manual", TokenURL: ts.URL + "/token", ClientID: "client-id", PasswordFlow: true,
			}
			if tc.jwks {
				settings.JWKSURL = ts.URL + "/jwks"
			}
			if tc.userInfo {
				settings.UserInfoURL = ts.URL + "/userinfo"
			}
			data, err := json.Marshal(settings)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if tc.jwks && string(fields["jwks_url"]) != `"`+settings.JWKSURL+`"` {
				t.Fatalf("persisted jwks_url=%s, want %q", fields["jwks_url"], settings.JWKSURL)
			}
			var restored service.OAuth2StrategySettings
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			if restored.JWKSURL != settings.JWKSURL {
				t.Fatalf("restored JWKSURL=%q, want %q", restored.JWKSURL, settings.JWKSURL)
			}
			authenticators, err := BuildOAuth2([]service.OAuth2StrategySettings{restored}, "/")
			if err != nil || len(authenticators) != 1 {
				t.Fatalf("BuildOAuth2: len=%d, err=%v", len(authenticators), err)
			}
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "http://pika.example/login/manual",
				strings.NewReader(`{"username":"user","password":"password"}`))
			req.Header.Set("Content-Type", "application/json")
			id, outcome, err := authenticators[0].Login(rr, req)
			if err != nil {
				t.Fatalf("Login: %v", err)
			}
			if tc.wantSuccess {
				wantName := "JWT User"
				if tc.userInfo {
					wantName = "UserInfo User"
				}
				if outcome != strategy.OutcomeContinue || id == nil || id.Subject != "user-123" || id.Name != wantName || id.Email != "user@example.com" {
					t.Fatalf("identity=%+v, outcome=%v, body=%s", id, outcome, rr.Body)
				}
			} else if outcome != strategy.OutcomeFailed || id != nil || rr.Code != http.StatusBadGateway {
				t.Fatalf("expected identity rejection: identity=%+v, outcome=%v, status=%d, body=%s", id, outcome, rr.Code, rr.Body)
			}
			if got := jwksHits.Load(); (got > 0) != tc.jwks {
				t.Errorf("JWKS hits=%d, configured=%v", got, tc.jwks)
			}
			wantUserInfoHits := int32(0)
			if tc.userInfo {
				wantUserInfoHits = 1
			}
			if got := userInfoHits.Load(); got != wantUserInfoHits {
				t.Errorf("userinfo hits=%d, want %d", got, wantUserInfoHits)
			}
		})
	}
}

func TestBuildOAuth2ManualEndpointsAvoidDiscovery(t *testing.T) {
	var discoveryHits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			discoveryHits.Add(1)
			http.Error(w, "unexpected discovery", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)

	authenticators, err := BuildOAuth2([]service.OAuth2StrategySettings{
		{
			Name:        "gitlab",
			DisplayName: "GitLab",
			IssuerURL:   ts.URL,
			AuthURL:     ts.URL + "/oauth/authorize",
			TokenURL:    ts.URL + "/oauth/token",
			UserInfoURL: ts.URL + "/oauth/userinfo",
			ClientID:    "client-id",
			Scopes:      []string{"openid", "profile"},
		},
	}, "/")
	if err != nil {
		t.Fatalf("BuildOAuth2: %v", err)
	}
	if got := len(authenticators); got != 1 {
		t.Fatalf("authenticators len=%d, want 1", got)
	}
	if got := discoveryHits.Load(); got != 0 {
		t.Fatalf("discovery hits=%d, want 0", got)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://pika.example/login/gitlab", nil)
	_, outcome, err := authenticators[0].Login(rr, req)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if outcome != strategy.OutcomePending {
		t.Fatalf("outcome=%v, want %v", outcome, strategy.OutcomePending)
	}
	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status=%d, want %d", rr.Code, http.StatusTemporaryRedirect)
	}
	location := rr.Header().Get("Location")
	if !strings.HasPrefix(location, ts.URL+"/oauth/authorize?") {
		t.Fatalf("redirect location %q does not use manual auth_url", location)
	}
}

func TestBuildOAuth2SkipsIncompleteManualProvider(t *testing.T) {
	authenticators, err := BuildOAuth2([]service.OAuth2StrategySettings{
		{
			Name:     "broken",
			AuthURL:  "https://idp.example/oauth/authorize",
			ClientID: "client-id",
		},
	}, "/")
	if err != nil {
		t.Fatalf("BuildOAuth2: %v", err)
	}
	if got := len(authenticators); got != 0 {
		t.Fatalf("authenticators len=%d, want 0", got)
	}
}

// TestBuildOAuth2RedirectURIIncludesBasePath is the regression guard for the
// base_path bug: a strategy built via BuildOAuth2 (the path taken by both Boot
// and Manager.Reload) must emit a redirect_uri that carries the server base
// path and matches the mounted "{base}/login/callback/{strategy}" route —
// without relying on ada's Mount-time SetCallbackBasePath, which Reload skips.
func TestBuildOAuth2RedirectURIIncludesBasePath(t *testing.T) {
	authenticators, err := BuildOAuth2([]service.OAuth2StrategySettings{
		{
			Name:     "gitlab",
			AuthURL:  "https://idp.example/oauth/authorize",
			TokenURL: "https://idp.example/oauth/token",
			ClientID: "client-id",
		},
	}, "/pika/")
	if err != nil {
		t.Fatalf("BuildOAuth2: %v", err)
	}
	if got := len(authenticators); got != 1 {
		t.Fatalf("authenticators len=%d, want 1", got)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://pika.example/pika/login/gitlab", nil)
	if _, _, err := authenticators[0].Login(rr, req); err != nil {
		t.Fatalf("Login: %v", err)
	}

	loc, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location %q: %v", rr.Header().Get("Location"), err)
	}
	redirectURI := loc.Query().Get("redirect_uri")
	const want = "http://pika.example/pika/login/callback/gitlab"
	if redirectURI != want {
		t.Fatalf("redirect_uri = %q, want %q", redirectURI, want)
	}
}

func TestBuildOAuth2PasswordFlowOnlyRequiresTokenURL(t *testing.T) {
	authenticators, err := BuildOAuth2([]service.OAuth2StrategySettings{
		{
			Name:         "password-idp",
			TokenURL:     "https://idp.example/oauth/token",
			ClientID:     "client-id",
			PasswordFlow: true,
		},
	}, "/")
	if err != nil {
		t.Fatalf("BuildOAuth2: %v", err)
	}
	if got := len(authenticators); got != 1 {
		t.Fatalf("authenticators len=%d, want 1", got)
	}
	if got := authenticators[0].Descriptor().Kind; got != "password" {
		t.Fatalf("descriptor kind=%q, want password", got)
	}
}
