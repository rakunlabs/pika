package authx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rakunlabs/ada/middleware/auth/strategy"

	"github.com/rakunlabs/pika/internal/service"
)

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
	})
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
	})
	if err != nil {
		t.Fatalf("BuildOAuth2: %v", err)
	}
	if got := len(authenticators); got != 0 {
		t.Fatalf("authenticators len=%d, want 0", got)
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
	})
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
