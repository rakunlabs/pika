package external

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func TestGitLabVariableSettingsRoundTrip(t *testing.T) {
	settings := map[string]any{"masked": true, "protected": true, "raw": false, "variable_type": "file"}
	p := gitLabTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload["value"] != "new-value" {
				t.Errorf("value: %v", payload)
			}
			delete(payload, "value")
			settings = payload
		}
		response := map[string]any{"value": "old-value", "hidden": false}
		for k, v := range settings {
			response[k] = v
		}
		json.NewEncoder(w).Encode(response)
	})
	ctx := context.Background()
	entry, err := p.Read(ctx, "KEY")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range settings {
		if entry.Metadata[k] != v {
			t.Errorf("metadata %s = %v", k, entry.Metadata[k])
		}
	}
	if !reflect.DeepEqual(entry.Data, map[string]any{"value": "old-value"}) || string(entry.Raw) != `{"value":"old-value"}` {
		t.Fatalf("settings leaked into configuration data: %+v", entry)
	}
	if err := p.Write(ctx, "KEY", map[string]any{"value": "new-value", "masked": false, "protected": false, "raw": true, "variable_type": "env_var"}); err != nil {
		t.Fatal(err)
	}
	entry, err = p.Read(ctx, "KEY")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]any{"masked": false, "protected": false, "raw": true, "variable_type": "env_var"} {
		if entry.Metadata[k] != v {
			t.Errorf("updated %s = %v, want %v", k, entry.Metadata[k], v)
		}
	}
	if raw, err := p.Fetch(ctx, "KEY"); err != nil || string(raw) != `{"value":"old-value"}` {
		t.Fatalf("inheritance: %s %v", raw, err)
	}
}

func TestGitLabProjectVariables(t *testing.T) {
	created := false
	methods := []string{}
	p := gitLabTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		base := "/gitlab/api/v4/projects/team%2Fproject/variables"
		if r.URL.EscapedPath() != base && r.URL.EscapedPath() != base+"/KEY" {
			t.Errorf("project path: %s", r.URL.EscapedPath())
		}
		if r.URL.EscapedPath() == base+"/KEY" && r.URL.Query().Get("filter[environment_scope]") != "production" {
			t.Error("missing project scope filter")
		}
		switch r.Method {
		case http.MethodGet:
			if r.URL.EscapedPath() == base {
				fmt.Fprint(w, `[{"key":"KEY","environment_scope":"production"}]`)
			} else if !created {
				w.WriteHeader(http.StatusNotFound)
			} else {
				fmt.Fprint(w, `{"value":"secret-value","masked":true,"protected":true,"raw":true,"variable_type":"file"}`)
			}
		case http.MethodPost:
			var payload map[string]any
			json.NewDecoder(r.Body).Decode(&payload)
			want := map[string]any{"key": "KEY", "value": "secret-value", "masked": true, "protected": true, "raw": true, "variable_type": "file", "environment_scope": "production"}
			if !reflect.DeepEqual(payload, want) {
				t.Errorf("create: %v", payload)
			}
			created = true
			w.WriteHeader(http.StatusCreated)
		case http.MethodPut:
			var payload map[string]any
			json.NewDecoder(r.Body).Decode(&payload)
			if !reflect.DeepEqual(payload, map[string]any{"value": "updated"}) {
				t.Errorf("value-only update changed settings: %v", payload)
			}
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	p.Config.Group = ""
	p.Config.Project = "team/project"
	p.Config.EnvironmentScope = "production"
	ctx := context.Background()
	if err := p.Write(ctx, "KEY", map[string]any{"value": "secret-value", "masked": true, "protected": true, "raw": true, "variable_type": "file"}); err != nil {
		t.Fatal(err)
	}
	if keys, err := p.List(ctx, ""); err != nil || !reflect.DeepEqual(keys, []string{"KEY"}) {
		t.Fatalf("list: %v %v", keys, err)
	}
	if entry, err := p.Read(ctx, "KEY"); err != nil || entry.Metadata["variable_type"] != "file" {
		t.Fatalf("read: %v %v", entry, err)
	}
	if err := p.Write(ctx, "KEY", map[string]any{"value": "updated"}); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(ctx, "KEY"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(methods, []string{"GET", "POST", "GET", "GET", "GET", "PUT", "DELETE"}) {
		t.Fatal(methods)
	}
}

func TestGitLabRejectsInvalidSettingsAndTargets(t *testing.T) {
	p := gitLabTestProvider(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid input reached upstream") })
	for _, data := range []map[string]any{
		{"value": "ok", "masked": "true"}, {"value": "ok", "protected": nil},
		{"value": "ok", "raw": 1}, {"value": "ok", "variable_type": "other"},
		{"value": "ok", "hidden": true}, {"value": "ok", "environment_scope": "other"},
	} {
		if err := p.Write(context.Background(), "KEY", data); err == nil {
			t.Errorf("accepted %v", data)
		}
	}
	p.Config.Project = "123"
	if err := p.Validate(); err == nil {
		t.Error("accepted both group and project")
	}
	p.Config.Group = ""
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.Config.Project = ""
	if err := p.Validate(); err == nil {
		t.Error("accepted no target")
	}
}
