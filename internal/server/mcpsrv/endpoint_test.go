package mcpsrv_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/server/mcpsrv"
	"github.com/rakunlabs/pika/internal/service"
)

func saveMCP(t *testing.T, svc *service.Service, cfg service.MCPSettings) {
	t.Helper()
	if err := svc.PatchSettings(t.Context(), &service.PatchSettings{Action: service.ActionKeySet, MCP: &cfg}); err != nil {
		t.Fatal(err)
	}
	stored, err := svc.Settings(t.Context())
	if err != nil || stored.MCP == nil || stored.MCP.Endpoint != cfg.Endpoint {
		t.Fatalf("MCP settings did not round-trip through storage: %v, %v", stored, err)
	}
}

func endpointServer(t *testing.T, svc *service.Service, locked *atomic.Bool) *httptest.Server {
	t.Helper()
	mgr := newAuthManager(t, svc)
	mux := ada.New()
	mux.Use(mcpsrv.Endpoint(svc, mgr, "/pika", "test", "test", locked.Load))
	protected := mux.Group("/pika", mgr.Require(), mgr.CapMiddleware())
	protected.GET("/api/v1/private", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.Group("/pika").Handle("/*", http.NotFoundHandler())
	srv := httptest.NewServer(mux.Mux)
	t.Cleanup(srv.Close)
	return srv
}

func connectEndpoint(t *testing.T, srv *httptest.Server, path string, headers http.Header) *mcp.ClientSession {
	t.Helper()
	return connect(t, &httptest.Server{URL: srv.URL + path}, headers)
}

func TestEndpointProxyScopesAndLiveRevocation(t *testing.T) {
	svc := newTestService(t)
	cfg := service.MCPSettings{Endpoint: "/mcp", AuthDisabled: true, Scopes: []service.TokenScope{
		scope("team-a/**", "read", "write"), scope("team-b/**", "delete"),
	}}
	saveMCP(t, svc, cfg)
	srv := endpointServer(t, svc, &atomic.Bool{})
	// Even a broad bearer credential cannot expand the endpoint's scope.
	key := newToken(t, svc, "broad", scope("**", "read", "write", "delete"))
	session := connectEndpoint(t, srv, "/pika/mcp", bearer(key))
	callTool(t, session, "set_config", map[string]any{"path": "team-a/config.yaml", "content": "value: 1"}, nil)
	callToolExpectError(t, session, "set_config", map[string]any{"path": "team-b/config.yaml", "content": "value: 1"})
	callToolExpectError(t, session, "delete_config", map[string]any{"path": "team-a/config.yaml"})
	callToolExpectError(t, session, "get_config", map[string]any{"path": "team-b/config.yaml"})
	if slices.Contains(toolNames(t, session), "read_external") {
		t.Fatal("external tool exposed")
	}

	cfg.Scopes = []service.TokenScope{scope("team-a/**", "read")}
	saveMCP(t, svc, cfg)
	names := toolNames(t, session)
	for _, forbidden := range []string{"set_config", "delete_config", "delete_folder", "read_external"} {
		if slices.Contains(names, forbidden) {
			t.Fatalf("revoked tool still visible: %s", forbidden)
		}
	}
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "set_config", Arguments: map[string]any{"path": "team-a/config.yaml", "content": "value: 2"}})
	if err == nil && !res.IsError {
		t.Fatal("revoked tool call succeeded")
	}
	// No Pika credentials are needed in proxy mode.
	anonymous := connectEndpoint(t, srv, "/pika/mcp", nil)
	callTool(t, anonymous, "get_config", map[string]any{"path": "team-a/config.yaml"}, nil)
}

func TestEndpointRoutingAuthAndLock(t *testing.T) {
	svc := newTestService(t)
	var locked atomic.Bool
	srv := endpointServer(t, svc, &locked)
	key := newToken(t, svc, "reader", scope("**", "read"))
	connectEndpoint(t, srv, "/pika/api/v1/mcp", bearer(key))
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	status := func(path string) int {
		t.Helper()
		res, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}
	if got := status("/pika/api/v1/mcp"); got == http.StatusMethodNotAllowed || got == http.StatusOK {
		t.Fatalf("default endpoint bypasses auth: %d", got)
	}
	cfg := service.MCPSettings{Endpoint: "/agents/mcp", AuthDisabled: true, Scopes: []service.TokenScope{scope("**", "read")}}
	saveMCP(t, svc, cfg)
	if got := status("/pika/api/v1/mcp"); got != http.StatusNotFound {
		t.Fatalf("old default route: %d", got)
	}
	if got := status("/pika/agents/mcp"); got != http.StatusMethodNotAllowed {
		t.Fatalf("new route: %d", got)
	}
	if got := status("/pika/api/v1/private"); got == http.StatusNoContent {
		t.Fatal("REST auth bypassed")
	}
	locked.Store(true)
	if got := status("/pika/agents/mcp"); got != http.StatusServiceUnavailable {
		t.Fatalf("locked custom endpoint: %d", got)
	}
	locked.Store(false)
	cfg.Endpoint = "/mcp"
	cfg.AuthDisabled = false
	saveMCP(t, svc, cfg)
	if got := status("/pika/agents/mcp"); got != http.StatusNotFound {
		t.Fatalf("old custom route: %d", got)
	}
	if got := status("/pika/mcp"); got == http.StatusMethodNotAllowed {
		t.Fatal("auth was not restored")
	}
	connectEndpoint(t, srv, "/pika/mcp", bearer(key))
}

func TestProxyUserHeaderIsAuditOnly(t *testing.T) {
	svc := newTestService(t)
	saveMCP(t, svc, service.MCPSettings{Endpoint: "/mcp", AuthDisabled: true, Scopes: []service.TokenScope{scope("team-a/**", "read", "write")}})
	srv := endpointServer(t, svc, &atomic.Bool{})
	for _, username := range []string{"", "alice", "admin"} {
		headers := http.Header{}
		if username != "" {
			headers.Set("X-User", username)
		}
		session := connectEndpoint(t, srv, "/pika/mcp", headers)
		expected := username
		if expected == "" {
			expected = "mcp-proxy"
		}
		path := "team-a/" + expected + ".yaml"
		callTool(t, session, "set_config", map[string]any{"path": path, "content": "ok: true"}, nil)
		versions, err := svc.FileVersionsList(t.Context(), path)
		if err != nil || len(versions) != 1 || len(versions[0].Status) == 0 || versions[0].Status[0].Author != expected {
			t.Fatalf("audit author not preserved: %+v, %v", versions, err)
		}
		callToolExpectError(t, session, "set_config", map[string]any{"path": "team-b/config.yaml", "content": "ok: true"})
		if slices.Contains(toolNames(t, session), "delete_config") {
			t.Fatal("X-User granted delete")
		}
	}
}

func TestMCPSettingsValidation(t *testing.T) {
	svc := newTestService(t)
	for _, endpoint := range []string{"/", "/api/v1/settings", "/login/pass", "/healthz", "https://example.com/mcp", "/mcp?key=1", "/a/../mcp", "/mcp/", "//mcp"} {
		if err := svc.PatchSettings(t.Context(), &service.PatchSettings{Action: service.ActionKeySet, MCP: &service.MCPSettings{Endpoint: endpoint}}); err == nil {
			t.Errorf("accepted endpoint %q", endpoint)
		}
	}
	for _, scopes := range [][]service.TokenScope{nil, {scope("**", "admin")}, {scope("../**", "read")}, {scope("team/**")}} {
		if err := (service.MCPSettings{Endpoint: "/mcp", AuthDisabled: true, Scopes: scopes}).Validate(); err == nil {
			t.Errorf("accepted scopes %+v", scopes)
		}
	}
}

func TestExternalScopesThroughTokenAndProxy(t *testing.T) {
	for _, proxy := range []bool{false, true} {
		name := "token"
		if proxy {
			name = "proxy"
		}
		t.Run(name, func(t *testing.T) {
			svc := newTestService(t)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				p := strings.TrimPrefix(r.URL.Path, "/v1/kv/")
				if r.URL.Query().Has("keys") {
					if p == "" {
						_ = json.NewEncoder(w).Encode([]string{"team-a/", "team-b/"})
						return
					}
					if p == "team-a/" {
						_ = json.NewEncoder(w).Encode([]string{"team-a/config"})
						return
					}
				}
				if p != "team-a/config" {
					t.Errorf("out-of-scope backend request: %s %s", r.Method, r.URL)
					http.Error(w, "denied", 403)
					return
				}
				if r.Method == http.MethodGet {
					_ = json.NewEncoder(w).Encode([]map[string]string{{"Key": p, "Value": base64.StdEncoding.EncodeToString([]byte("needle"))}})
				} else {
					_ = json.NewEncoder(w).Encode(true)
				}
			}))
			t.Cleanup(backend.Close)
			if err := svc.PatchSettings(t.Context(), &service.PatchSettings{Action: service.ActionKeySet, External: map[string]external.External{
				"prod-consul":  {Consul: &external.Consul{Address: backend.URL}},
				"other-consul": {Consul: &external.Consul{Address: backend.URL}},
			}}); err != nil {
				t.Fatal(err)
			}
			ext := service.TokenScope{Resource: "prod-consul", Path: "team-a/**", Operations: []string{"read", "write"}}
			scopes := []service.TokenScope{ext, {Resource: "other-consul", Path: "team-b/**", Operations: []string{"delete"}}, scope("**", "read")}
			cfg := service.MCPSettings{Endpoint: "/mcp", AuthDisabled: proxy, Scopes: scopes}
			saveMCP(t, svc, cfg)
			srv := endpointServer(t, svc, &atomic.Bool{})
			var headers http.Header
			var tokenID string
			if !proxy {
				res, err := svc.CreateToken(t.Context(), &service.CreateTokenRequest{Name: "external-agent", Scopes: scopes})
				if err != nil {
					t.Fatal(err)
				}
				tokenID = res.ID
				headers = bearer(res.RawKey)
				if err := svc.ValidateToken(t.Context(), res.RawKey, "team-a/config", "write"); err == nil {
					t.Fatal("external write scope granted config write")
				}
			}
			session := connectEndpoint(t, srv, "/pika/mcp", headers)
			names := toolNames(t, session)
			if !slices.Contains(names, "read_external") || !slices.Contains(names, "write_external") || slices.Contains(names, "set_config") {
				t.Fatalf("incorrect tool visibility: %v", names)
			}
			var resources struct {
				Resources []struct {
					Name      string `json:"name"`
					CanDelete bool   `json:"can_delete"`
				} `json:"resources"`
			}
			callTool(t, session, "list_external_resources", nil, &resources)
			if len(resources.Resources) != 1 || resources.Resources[0].Name != "prod-consul" || resources.Resources[0].CanDelete {
				t.Fatalf("resource grants leaked: %+v", resources)
			}
			var paths struct {
				Paths []string `json:"paths"`
			}
			callTool(t, session, "list_external_paths", map[string]any{"resource": "prod-consul"}, &paths)
			if !slices.Equal(paths.Paths, []string{"team-a/"}) {
				t.Fatalf("paths leaked: %v", paths.Paths)
			}
			callTool(t, session, "read_external", map[string]any{"resource": "prod-consul", "path": "team-a/config"}, nil)
			callTool(t, session, "write_external", map[string]any{"resource": "prod-consul", "path": "team-a/config", "data": map[string]any{"value": "new"}}, nil)
			for _, p := range []string{"team-b/config", "team-a/../team-b/config", "team-a/%2e%2e/team-b/config", "team-a/config?recurse"} {
				callToolExpectError(t, session, "read_external", map[string]any{"resource": "prod-consul", "path": p})
			}
			callToolExpectError(t, session, "read_external", map[string]any{"resource": "other-consul", "path": "team-a/config"})
			callToolExpectError(t, session, "delete_external", map[string]any{"resource": "prod-consul", "path": "team-a/config"})
			var search struct {
				Hits []struct {
					Path string `json:"path"`
				} `json:"hits"`
			}
			callTool(t, session, "search_external", map[string]any{"resource": "prod-consul", "query": "needle"}, &search)
			if len(search.Hits) != 1 || search.Hits[0].Path != "team-a/config" {
				t.Fatalf("unexpected search results: %+v", search)
			}
			ext.Operations = []string{"read"}
			if proxy {
				cfg.Scopes = []service.TokenScope{ext}
				saveMCP(t, svc, cfg)
			} else {
				if err := svc.PatchToken(t.Context(), tokenID, &service.PatchTokenRequest{Scopes: []service.TokenScope{ext}}); err != nil {
					t.Fatal(err)
				}
			}
			names = toolNames(t, session)
			for _, hidden := range []string{"write_external", "delete_external", "get_config", "set_config"} {
				if slices.Contains(names, hidden) {
					t.Fatalf("tool remains after revocation: %s", hidden)
				}
			}
		})
	}
}
