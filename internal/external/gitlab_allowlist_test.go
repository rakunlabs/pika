package external

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func TestGitLabVariableAllowlist(t *testing.T) {
	for _, tc := range []struct {
		name    string
		list    string
		allowed []string
		denied  []string
		invalid bool
	}{
		{name: "exact and regex", list: " CONFIG \r\n\n/APP_.*/\n/FOO|BAR/", allowed: []string{"CONFIG", "APP_KEY", "FOO", "BAR"}, denied: []string{"config", "SECRET", "XCONFIG", "CONFIG_X", "XAPP_KEY", "FOOX", "XBAR"}},
		{name: "empty", denied: []string{"CONFIG"}},
		{name: "blank lines", list: " \r\n\t", denied: []string{"CONFIG"}},
		{name: "invalid regex", list: "CONFIG\n/[/", invalid: true},
		{name: "missing delimiter", list: "/APP_.*", invalid: true},
		{name: "invalid name", list: "APP_*", invalid: true},
		{name: "unsupported regex", list: "/(?=SECRET)/", invalid: true},
		{name: "unbalanced groups", list: "/x)|.*(?:/", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &GitLabProvider{Config: &GitLab{Address: "https://gitlab.example", Group: "1", Token: "test", VariableAllowlist: &tc.list}}
			if err := p.Validate(); (err != nil) != tc.invalid {
				t.Fatalf("Validate() = %v", err)
			}
			if tc.invalid {
				return
			}
			matcher, err := p.variableAllowlist()
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range tc.allowed {
				if !matcher.MatchString(key) {
					t.Errorf("denied %q", key)
				}
			}
			for _, key := range tc.denied {
				if matcher.MatchString(key) {
					t.Errorf("allowed %q", key)
				}
			}
		})
	}
}

func TestGitLabAllowlistDeniesBeforeUpstream(t *testing.T) {
	for _, list := range []string{"PUBLIC\n/APP_.*/", "", "/[/"} {
		t.Run(list, func(t *testing.T) {
			calls := 0
			p := gitLabTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				fmt.Fprint(w, `{"value":"secret"}`)
			})
			p.Config.VariableAllowlist = &list
			ctx := context.Background()
			if entry, err := p.Read(ctx, "SECRET"); err == nil || entry != nil {
				t.Fatal("read was not denied")
			}
			if raw, err := p.Fetch(ctx, "SECRET"); err == nil || raw != nil {
				t.Fatal("fetch was not denied")
			}
			if err := p.Write(ctx, "SECRET", map[string]any{"value": "changed"}); err == nil {
				t.Fatal("write was not denied")
			}
			if err := p.Delete(ctx, "SECRET"); err == nil {
				t.Fatal("delete was not denied")
			}
			if calls != 0 {
				t.Fatalf("made %d upstream requests", calls)
			}
		})
	}
}

func TestGitLabAllowlistFiltersListAndTest(t *testing.T) {
	p := gitLabTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			w.Header().Set("X-Next-Page", "2")
			fmt.Fprint(w, `[{"key":"SECRET","value":"secret"},{"key":"APP_ONE"},{"key":"APP_OTHER","environment_scope":"production"}]`)
		} else {
			fmt.Fprint(w, `[{"key":"PUBLIC"},{"key":"APP_TWO"},{"key":"XAPP_THREE"}]`)
		}
	})
	list := "PUBLIC\n/APP_.*/"
	p.Config.VariableAllowlist = &list
	ctx := context.Background()
	for prefix, want := range map[string][]string{"": {"APP_ONE", "APP_TWO", "PUBLIC"}, "APP_": {"ONE", "TWO"}, "SECRET": {}} {
		got, err := p.List(ctx, prefix)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("prefix=%q got=%v want=%v err=%v", prefix, got, want, err)
		}
	}
	result := p.Test(ctx)
	if !result.OK || !reflect.DeepEqual(result.Sample, []string{"APP_ONE", "APP_TWO", "PUBLIC"}) {
		t.Fatalf("test result=%+v", result)
	}
	list = ""
	keys, err := p.List(ctx, "")
	if err != nil || len(keys) != 0 {
		t.Fatalf("empty allowlist: keys=%v err=%v", keys, err)
	}
}

func TestGitLabAllowlistAllowsCRUD(t *testing.T) {
	for _, key := range []string{"PUBLIC", "APP_NEW"} {
		t.Run(key, func(t *testing.T) {
			exists := false
			p := gitLabTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					if !exists {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					fmt.Fprint(w, `{"value":"visible"}`)
				case http.MethodPost, http.MethodPut:
					exists = true
					fmt.Fprint(w, `{}`)
				case http.MethodDelete:
					exists = false
				}
			})
			list := "PUBLIC\n/APP_.*/"
			p.Config.VariableAllowlist = &list
			ctx := context.Background()
			for range 2 {
				if err := p.Write(ctx, key, map[string]any{"value": "visible"}); err != nil {
					t.Fatal(err)
				}
			}
			if raw, err := p.Fetch(ctx, key); err != nil || string(raw) != `{"value":"visible"}` {
				t.Fatalf("fetch=%s err=%v", raw, err)
			}
			if err := p.Delete(ctx, key); err != nil {
				t.Fatal(err)
			}
			if exists {
				t.Fatal("delete failed")
			}
		})
	}
}

func TestGitLabAllowlistJSON(t *testing.T) {
	for _, input := range []string{`{}`, `{"variable_allowlist":null}`, `{"variable_allowlist":""}`, `{"variable_allowlist":"PUBLIC\n/APP_.*/"}`} {
		var config GitLab
		if err := json.Unmarshal([]byte(input), &config); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		var restored GitLab
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(config.VariableAllowlist, restored.VariableAllowlist) {
			t.Fatalf("allowlist changed: %s", data)
		}
	}
}
