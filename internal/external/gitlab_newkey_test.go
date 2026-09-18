package external

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// newKeyServer records what reaches GitLab and reports the given keys as
// already existing in the resource's environment scope.
func newKeyServer(t *testing.T, existing map[string]bool) (*GitLabProvider, *[]string) {
	t.Helper()
	methods := []string{}
	p := gitLabTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		switch r.Method {
		case http.MethodGet:
			key := r.URL.EscapedPath()[len("/gitlab/api/v4/groups/team%2Fsubgroup/variables/"):]
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
		}
	})
	return p, &methods
}

func TestGitLabNewKeyPolicyDenyIsDefault(t *testing.T) {
	for _, policy := range []string{"", GitLabNewKeyDeny} {
		t.Run("policy="+policy, func(t *testing.T) {
			p, methods := newKeyServer(t, map[string]bool{})
			list := "PUBLIC"
			p.Config.VariableAllowlist, p.Config.NewKeyPolicy = &list, policy
			if err := p.Write(context.Background(), "NEW_KEY", map[string]any{"value": "v"}); err == nil {
				t.Fatal("create was not denied")
			}
			if len(*methods) != 0 {
				t.Fatalf("reached upstream: %v", *methods)
			}
		})
	}
}

func TestGitLabNewKeyPolicyCreatesOutsideAllowlist(t *testing.T) {
	for _, policy := range []string{GitLabNewKeyAllow, GitLabNewKeyAppend} {
		t.Run(policy, func(t *testing.T) {
			existing := map[string]bool{}
			p, methods := newKeyServer(t, existing)
			list := "PUBLIC"
			p.Config.VariableAllowlist, p.Config.NewKeyPolicy = &list, policy
			ctx := context.Background()
			if err := p.Write(ctx, "NEW_KEY", map[string]any{"value": "v"}); err != nil {
				t.Fatalf("create denied: %v", err)
			}
			if !existing["NEW_KEY"] {
				t.Fatal("variable was not created")
			}
			if got := *methods; len(got) != 2 || got[0] != http.MethodGet || got[1] != http.MethodPost {
				t.Fatalf("methods = %v", got)
			}
			// The provider never widens the allowlist itself; only the
			// service layer does, and only for the append policy.
			if *p.Config.VariableAllowlist != "PUBLIC" {
				t.Fatalf("provider mutated allowlist: %q", *p.Config.VariableAllowlist)
			}
			// The variable now exists, so it is an update — still denied.
			if err := p.Write(ctx, "NEW_KEY", map[string]any{"value": "v2"}); err == nil {
				t.Fatal("update outside allowlist was allowed")
			}
			// Reads stay denied regardless of the policy.
			if _, err := p.Read(ctx, "NEW_KEY"); err == nil {
				t.Fatal("read outside allowlist was allowed")
			}
			if err := p.Delete(ctx, "NEW_KEY"); err == nil {
				t.Fatal("delete outside allowlist was allowed")
			}
		})
	}
}

func TestGitLabNewKeyPolicyValidation(t *testing.T) {
	p, _ := newKeyServer(t, map[string]bool{})
	for _, policy := range []string{"", GitLabNewKeyDeny, GitLabNewKeyAllow, GitLabNewKeyAppend} {
		p.Config.NewKeyPolicy = policy
		if err := p.Validate(); err != nil {
			t.Fatalf("policy %q rejected: %v", policy, err)
		}
	}
	p.Config.NewKeyPolicy = "append-always"
	if err := p.Validate(); err == nil {
		t.Fatal("accepted unknown policy")
	}
}

func TestGitLabAppendAllowedVariable(t *testing.T) {
	list := "PUBLIC\n"
	cfg := &GitLab{Address: "https://gitlab.example", Group: "1", Token: "t", VariableAllowlist: &list}
	changed, err := cfg.AppendAllowedVariable("NEW_KEY")
	if err != nil || !changed || *cfg.VariableAllowlist != "PUBLIC\nNEW_KEY" {
		t.Fatalf("append: changed=%v list=%q err=%v", changed, *cfg.VariableAllowlist, err)
	}
	// Already covered — nothing to do.
	if changed, err := cfg.AppendAllowedVariable("NEW_KEY"); err != nil || changed {
		t.Fatalf("duplicate append: changed=%v err=%v", changed, err)
	}
	// No allowlist means unrestricted: appending would only narrow it.
	unrestricted := &GitLab{Address: "https://gitlab.example", Group: "1", Token: "t"}
	if changed, err := unrestricted.AppendAllowedVariable("NEW_KEY"); err != nil || changed {
		t.Fatalf("unrestricted append: changed=%v err=%v", changed, err)
	}
	if _, err := cfg.AppendAllowedVariable("bad key"); err == nil {
		t.Fatal("accepted invalid key")
	}
}

func TestGitLabExists(t *testing.T) {
	p, _ := newKeyServer(t, map[string]bool{"PUBLIC": true})
	ctx := context.Background()
	if ok, err := p.Exists(ctx, "PUBLIC"); err != nil || !ok {
		t.Fatalf("existing: %v %v", ok, err)
	}
	if ok, err := p.Exists(ctx, "MISSING"); err != nil || ok {
		t.Fatalf("missing: %v %v", ok, err)
	}
	list := "PUBLIC"
	p.Config.VariableAllowlist = &list
	if _, err := p.Exists(ctx, "SECRET"); err == nil {
		t.Fatal("allowlist bypassed")
	}
	p.Config.NewKeyPolicy = GitLabNewKeyAllow
	if ok, err := p.Exists(ctx, "SECRET"); err != nil || ok {
		t.Fatalf("new-key candidate: %v %v", ok, err)
	}
}
