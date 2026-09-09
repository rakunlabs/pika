package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/server/api"
	"github.com/rakunlabs/pika/internal/server/authx"
	"github.com/rakunlabs/pika/internal/server/mcpsrv"
	"github.com/rakunlabs/pika/internal/service"
	bwstore "github.com/rakunlabs/pika/internal/storage/bw"
)

// Exercise MCP alongside the real API, login routes and embedded SPA. The
// transport-only tests do not include these competing route registrations.
func TestMCPWithApplicationRoutes(t *testing.T) {
	for _, basePath := range []string{"", "/pika"} {
		t.Run("base="+basePath, func(t *testing.T) {
			store, err := bwstore.New(t.Context(), &bwstore.Config{InMemory: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			svc := service.New(store)
			app := ada.New()
			if basePath != "" {
				app.GET("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			}
			mgr := authx.New(authx.Deps{
				Svc: svc, BasePath: authBasePath(basePath), CookieName: "pika_session",
				SessionStore: authx.NewSessionStore(svc, "pika_session"),
			})
			if err := mgr.Boot(t.Context(), &service.AuthSettings{}); err != nil {
				t.Fatal(err)
			}
			app.Use(mcpsrv.Endpoint(svc, mgr, basePath, "test", "test", nil))
			mData, m, mAuth := app.Group(basePath), app.Group(basePath), app.Group(basePath)
			mgr.Mount(app.Group(""))
			m.Use(mgr.Require(), mgr.CapMiddleware())
			if err := api.Handle(m, mData, mAuth, svc, api.Info{}, nil, mgr, nil, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			if err := folderHandler(mAuth); err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(app.Mux)
			t.Cleanup(srv.Close)

			endpoints := []service.MCPEndpoint{}
			for _, path := range []string{service.DefaultMCPEndpoint, "/mcp", "/agents/mcp"} {
				endpoints = append(endpoints, service.MCPEndpoint{Endpoint: path, AuthDisabled: true, Scopes: []service.TokenScope{{Path: "**", Operations: []string{"read"}}}})
			}
			if err := svc.PatchSettings(t.Context(), &service.PatchSettings{Action: service.ActionKeySet, MCP: &service.MCPSettings{Endpoints: &endpoints}}); err != nil {
				t.Fatal(err)
			}
			for _, endpoint := range endpoints {
				url := srv.URL + basePath + endpoint.Endpoint
				client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
				session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: url, DisableStandaloneSSE: true}, nil)
				if err != nil {
					t.Fatalf("MCP connect %s: %v", url, err)
				}
				tools, err := session.ListTools(t.Context(), nil)
				if err != nil || len(tools.Tools) != 6 {
					t.Fatalf("MCP tools %s: %+v, %v", url, tools, err)
				}
				_ = session.Close()
				res, err := http.Get(url)
				if err != nil {
					t.Fatal(err)
				}
				_ = res.Body.Close()
				if res.StatusCode != http.StatusMethodNotAllowed {
					t.Fatalf("GET %s: got %d, want 405", url, res.StatusCode)
				}
			}
		})
	}
}
