package external

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func gitLabTestProvider(t *testing.T, handler http.HandlerFunc) *GitLabProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &GitLabProvider{Config: &GitLab{Address: srv.URL + "/gitlab", Group: "team/subgroup", Token: "test-token", ProxyMode: "none"}}
}

func TestGitLabListPaginationAndScope(t *testing.T) {
	pages := 0
	p := gitLabTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		pages++
		if r.URL.EscapedPath() != "/gitlab/api/v4/groups/team%2Fsubgroup/variables" || r.Header.Get("PRIVATE-TOKEN") != "test-token" {
			t.Errorf("incorrect path or authentication: %s", r.URL.EscapedPath())
		}
		if r.URL.Query().Get("per_page") != "100" {
			t.Error("missing page size")
		}
		switch r.URL.Query().Get("page") {
		case "1":
			w.Header().Set("X-Next-Page", "2")
			fmt.Fprint(w, `[{"key":"Z","environment_scope":"*"},{"key":"Z","environment_scope":"production"}]`)
		case "2":
			fmt.Fprint(w, `[{"key":"A","environment_scope":"*"}]`)
		default:
			t.Error("unexpected page")
		}
	})
	keys, err := p.List(context.Background(), "")
	if err != nil || !reflect.DeepEqual(keys, []string{"A", "Z"}) || pages != 2 {
		t.Fatalf("keys=%v pages=%d err=%v", keys, pages, err)
	}
}

func TestGitLabReadWritePreservesMetadata(t *testing.T) {
	value := "first\nline"
	p := gitLabTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("filter[environment_scope]") != "review/*" {
			t.Error("scope filter missing")
		}
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"value": value, "masked": true, "protected": true, "variable_type": "file"})
		case http.MethodPut:
			var data map[string]any
			if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
				t.Fatal(err)
			}
			if len(data) != 1 {
				t.Errorf("write would modify metadata: %v", data)
			}
			value = data["value"].(string)
			fmt.Fprint(w, `{}`)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	})
	p.Config.EnvironmentScope = "review/*"
	ctx := context.Background()
	e, err := p.Read(ctx, "CONFIG")
	if err != nil || e.Data["value"] != value {
		t.Fatalf("read=%v err=%v", e, err)
	}
	if err := p.Write(ctx, "CONFIG", map[string]any{"value": ""}); err != nil {
		t.Fatal(err)
	}
	raw, err := p.Fetch(ctx, "CONFIG")
	if err != nil || string(raw) != `{"value":""}` {
		t.Fatalf("fetch=%s err=%v", raw, err)
	}
}

func TestGitLabCreateAndDelete(t *testing.T) {
	methods := []string{}
	p := gitLabTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method == http.MethodPost {
			var data map[string]any
			json.NewDecoder(r.Body).Decode(&data)
			if !reflect.DeepEqual(data, map[string]any{"key": "NEW", "value": "hello", "environment_scope": "*"}) {
				t.Errorf("create payload: %v", data)
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	if err := p.Write(context.Background(), "NEW", map[string]any{"value": "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(context.Background(), "NEW"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(methods, []string{"GET", "POST", "DELETE"}) {
		t.Fatal(methods)
	}
}

func TestGitLabCreateDoesNotUpdateAnotherScope(t *testing.T) {
	variables := map[string]string{"*": "default-value"}
	p := gitLabTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Get("filter[environment_scope]") != "production" {
				t.Error("missing exact-scope lookup")
			}
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut:
			// Reproduce GitLab's missing scoped-variable fallback. A PUT here
			// can select another variable instead of returning 404.
			variables["*"] = "incorrectly-updated"
			fmt.Fprint(w, `{}`)
		case http.MethodPost:
			var data map[string]string
			if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
				t.Error(err)
			}
			variables[data["environment_scope"]] = data["value"]
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{}`)
		}
	})
	p.Config.EnvironmentScope = "production"
	if err := p.Write(context.Background(), "KEY", map[string]any{"value": "production-value"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(variables, map[string]string{"*": "default-value", "production": "production-value"}) {
		t.Fatalf("scope isolation failed: %v", variables)
	}
}

func TestGitLabFailedUpdateDoesNotRecreate(t *testing.T) {
	methods := []string{}
	p := gitLabTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"value":"existing"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	if err := p.Write(context.Background(), "KEY", map[string]any{"value": "new"}); err == nil {
		t.Fatal("concurrent removal should fail the update")
	}
	if !reflect.DeepEqual(methods, []string{"GET", "PUT"}) {
		t.Fatalf("unexpected fallback: %v", methods)
	}
}

func TestGitLabErrorsDoNotLeakOrCreate(t *testing.T) {
	for _, status := range []int{400, 401, 403, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			p := gitLabTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(status)
				fmt.Fprint(w, `{"value":"sensitive-upstream-value"}`)
			})
			err := p.Write(context.Background(), "KEY", map[string]any{"value": "hello"})
			if err == nil || strings.Contains(err.Error(), "sensitive") || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestGitLabRejectsInvalidKeysAndHiddenValues(t *testing.T) {
	calls := 0
	p := gitLabTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"key":"HIDDEN","value":null}`)
	})
	for _, key := range []string{"", "../other", "A/B", "A?x=y", strings.Repeat("A", 256)} {
		if _, err := p.Read(context.Background(), key); err == nil {
			t.Errorf("accepted key %q", key)
		}
		if err := p.Write(context.Background(), key, map[string]any{"value": "test"}); err == nil {
			t.Errorf("accepted write key %q", key)
		}
	}
	if calls != 0 {
		t.Fatal("invalid keys reached upstream")
	}
	if _, err := p.Read(context.Background(), "HIDDEN"); err == nil {
		t.Fatal("hidden value rendered as empty")
	}
}

func TestGitLabRejectsRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed redirect") }))
	defer target.Close()
	p := gitLabTestProvider(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) })
	if _, err := p.List(context.Background(), ""); err == nil {
		t.Fatal("accepted redirect")
	}
}

func TestGitLabConfigAndDispatch(t *testing.T) {
	config := &GitLab{Address: "https://gitlab.com", Group: "123", Token: "token"}
	p, err := ResourceProvider(External{GitLab: config}, nil)
	if err != nil || Kind(External{GitLab: config}) != "gitlab" {
		t.Fatal(err)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"", "file:///tmp/gitlab", "https://user:pass@gitlab.com", "https://gitlab.com?x=y"} {
		config.Address = address
		if err := p.Validate(); err == nil {
			t.Errorf("accepted address %q", address)
		}
	}
}
