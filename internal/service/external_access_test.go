package service_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/service"
)

// gitLabResource wires a service-level external resource against a fake GitLab
// that reports `existing` keys in the group and records creates.
func gitLabResource(t *testing.T, svc *service.Service, name string, existing map[string]bool, cfg func(*external.GitLab)) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			key := r.URL.EscapedPath()[len("/api/v4/groups/team/variables/"):]
			if !existing[key] {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			fmt.Fprint(w, `{"value":"stored"}`)
		case http.MethodPost:
			var payload map[string]any
			json.NewDecoder(r.Body).Decode(&payload)
			existing[fmt.Sprint(payload["key"])] = true
			w.WriteHeader(http.StatusCreated)
		case http.MethodPut:
			fmt.Fprint(w, `{}`)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)

	gl := &external.GitLab{Address: srv.URL, Group: "team", Token: "t", ProxyMode: "none"}
	if cfg != nil {
		cfg(gl)
	}
	if err := svc.PatchSettings(t.Context(), &service.PatchSettings{
		Action:   service.ActionKeySet,
		External: map[string]external.External{name: {GitLab: gl}},
	}); err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}
}

func storedAllowlist(t *testing.T, svc *service.Service, name string) string {
	t.Helper()
	settings, err := svc.Settings(t.Context())
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	list := settings.External[name].GitLab.VariableAllowlist
	if list == nil {
		return "<nil>"
	}
	return *list
}

// A created key must land in the allowlist when the resource asked for it,
// otherwise the variable would be written and then immediately invisible.
func TestWriteExternalAppendsCreatedKeyToAllowlist(t *testing.T) {
	svc := newTestService(t)
	existing := map[string]bool{}
	allowlist := "PUBLIC"
	gitLabResource(t, svc, "gl", existing, func(g *external.GitLab) {
		g.VariableAllowlist = &allowlist
		g.NewKeyPolicy = external.GitLabNewKeyAppend
	})

	if err := svc.WriteExternal(t.Context(), "gl", "NEW_KEY", map[string]any{"value": "v"}); err != nil {
		t.Fatalf("WriteExternal: %v", err)
	}
	if !existing["NEW_KEY"] {
		t.Fatal("variable was not created upstream")
	}
	if got := storedAllowlist(t, svc, "gl"); got != "PUBLIC\nNEW_KEY" {
		t.Fatalf("allowlist = %q", got)
	}
	// Now that it is allowlisted, ordinary updates and reads work.
	if err := svc.WriteExternal(t.Context(), "gl", "NEW_KEY", map[string]any{"value": "v2"}); err != nil {
		t.Fatalf("update after append: %v", err)
	}
	if got := storedAllowlist(t, svc, "gl"); got != "PUBLIC\nNEW_KEY" {
		t.Fatalf("update re-appended: %q", got)
	}
}

// "allow" creates without widening the allowlist: pika may seed a value it is
// not permitted to read back.
func TestWriteExternalAllowKeepsAllowlistUnchanged(t *testing.T) {
	svc := newTestService(t)
	existing := map[string]bool{}
	allowlist := "PUBLIC"
	gitLabResource(t, svc, "gl", existing, func(g *external.GitLab) {
		g.VariableAllowlist = &allowlist
		g.NewKeyPolicy = external.GitLabNewKeyAllow
	})

	if err := svc.WriteExternal(t.Context(), "gl", "NEW_KEY", map[string]any{"value": "v"}); err != nil {
		t.Fatalf("WriteExternal: %v", err)
	}
	if got := storedAllowlist(t, svc, "gl"); got != "PUBLIC" {
		t.Fatalf("allowlist = %q", got)
	}
	if _, err := svc.ReadExternal(t.Context(), "gl", "NEW_KEY"); err == nil {
		t.Fatal("key created under allow policy became readable")
	}
}

// The default policy stays deny, so upgrades never widen what a resource can do.
func TestWriteExternalDefaultPolicyDeniesNewKeys(t *testing.T) {
	svc := newTestService(t)
	existing := map[string]bool{}
	allowlist := "PUBLIC"
	gitLabResource(t, svc, "gl", existing, func(g *external.GitLab) {
		g.VariableAllowlist = &allowlist
	})

	if err := svc.WriteExternal(t.Context(), "gl", "NEW_KEY", map[string]any{"value": "v"}); err == nil {
		t.Fatal("new key was accepted")
	}
	if existing["NEW_KEY"] {
		t.Fatal("variable reached upstream")
	}
	if got := storedAllowlist(t, svc, "gl"); got != "PUBLIC" {
		t.Fatalf("allowlist = %q", got)
	}
}

// Resource access settings apply to session callers too: a read-only resource
// stays read-only for everyone, and the summary endpoint reports it so the SPA
// hides the buttons.
func TestExternalAccessSettingsRestrictSessionCallers(t *testing.T) {
	svc := newTestService(t)
	existing := map[string]bool{"OLD_KEY": true}
	gitLabResource(t, svc, "gl", existing, nil)

	deny := false
	settings, err := svc.Settings(t.Context())
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	ext := settings.External["gl"]
	ext.Access = &external.Access{Create: &deny, Delete: &deny}
	if err := svc.PatchSettings(t.Context(), &service.PatchSettings{
		Action:   service.ActionKeySet,
		External: map[string]external.External{"gl": ext},
	}); err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}

	if err := svc.WriteExternal(t.Context(), "gl", "NEW_KEY", map[string]any{"value": "v"}); !errors.Is(err, external.ErrAccessDenied) {
		t.Fatalf("create err = %v, want ErrAccessDenied", err)
	}
	if existing["NEW_KEY"] {
		t.Fatal("denied create reached upstream")
	}
	if err := svc.WriteExternal(t.Context(), "gl", "OLD_KEY", map[string]any{"value": "v"}); err != nil {
		t.Fatalf("update denied: %v", err)
	}
	if err := svc.DeleteExternal(t.Context(), "gl", "OLD_KEY"); !errors.Is(err, external.ErrAccessDenied) {
		t.Fatalf("delete err = %v, want ErrAccessDenied", err)
	}

	resources, err := svc.ListExternalResources(t.Context())
	if err != nil {
		t.Fatalf("ListExternalResources: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("resources = %+v", resources)
	}
	caps := resources[0].Capabilities
	if caps.EffectiveCreate() || !caps.EffectiveUpdate() || caps.CanDelete || !caps.CanRead {
		t.Fatalf("capabilities = %+v", caps)
	}
}
