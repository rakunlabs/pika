package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/pika/internal/server/api"
	"github.com/rakunlabs/pika/internal/server/authx"
	"github.com/rakunlabs/pika/internal/service"
	bwstore "github.com/rakunlabs/pika/internal/storage/bw"
)

// TestAuditRecordsWrites drives a real token-authenticated write through
// the full API stack and checks it lands in the audit log without the
// request body.
func TestAuditRecordsWrites(t *testing.T) {
	store, err := bwstore.New(t.Context(), &bwstore.Config{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := service.New(store)
	t.Cleanup(svc.Close)

	mgr := authx.New(authx.Deps{
		Svc: svc, BasePath: "/", CookieName: "pika_session",
		SessionStore: authx.NewSessionStore(svc, "pika_session"),
	})
	if err := mgr.Boot(t.Context(), &service.AuthSettings{}); err != nil {
		t.Fatal(err)
	}

	app := ada.New()
	mData, m, mAuth := app.Group(""), app.Group(""), app.Group("")
	mgr.Mount(app.Group(""))
	m.Use(mgr.Require(), mgr.CapMiddleware())
	if err := api.Handle(api.Muxes{Protected: m, Data: mData, Public: mAuth}, api.Deps{Svc: svc, Mgr: mgr}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Mux)
	t.Cleanup(srv.Close)

	tok, err := svc.CreateToken(t.Context(), &service.CreateTokenRequest{
		Name:   "deployer",
		Scopes: []service.TokenScope{{Path: "**", Operations: []string{"read", "write"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/file/app/config",
		strings.NewReader(`{"data":"password: hunter2","format":"yaml"}`))
	req.Header.Set("Authorization", "Bearer "+tok.RawKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	entries, _, err := svc.ListAudit(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var found *service.AuditEntry
	for i := range entries {
		if entries[i].Target == "/api/v1/file/app/config" {
			found = &entries[i]
		}
	}
	if found == nil {
		t.Fatalf("write not audited; entries=%+v", entries)
	}
	if found.Actor != "token:deployer" || found.ActorType != "token" {
		t.Fatalf("actor = %q/%q", found.Actor, found.ActorType)
	}
	if found.Action != "POST /api/v1/file/*" || found.Status != resp.StatusCode {
		t.Fatalf("action=%q status=%d (resp %d)", found.Action, found.Status, resp.StatusCode)
	}
	for _, e := range entries {
		if strings.Contains(e.Detail+e.Target+e.Action, "hunter2") {
			t.Fatal("request body leaked into audit log")
		}
	}
}
