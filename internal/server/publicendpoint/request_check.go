package publicendpoint

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/rakunlabs/pika/internal/service"
)

// requestCheckMiddleware wraps next with the operator-defined rule
// list. When the rule set is empty the original handler is
// returned unchanged so there is zero overhead for endpoints that
// don't configure the stage.
//
// Evaluation model:
//   - Rules are walked top-to-bottom.
//   - A rule fires when every populated entry in its When block
//     matches the current request (AND semantics). An empty When
//     fires on every request.
//   - "allow" and "block" are terminal — they short-circuit
//     evaluation. "block" writes the configured response; "allow"
//     forwards to the shim immediately, skipping any later rules.
//   - "set_*" / "del_*" / "replace_*" actions mutate the request in place and
//     evaluation continues with the next rule. This lets an
//     operator stack multiple rewrites and then terminate with a
//     terminal rule.
//   - If no rule terminates, the request falls through to the
//     shim (default-allow). Same semantics as a firewall that
//     ends with an implicit ACCEPT.
func requestCheckMiddleware(rc *service.RequestCheck, next http.Handler) http.Handler {
	if rc == nil || len(rc.Rules) == 0 {
		return next
	}
	// Snapshot the rules so a downstream mutation (which should
	// not happen — the manager hands us a frozen slice) cannot
	// race with the request loop.
	rules := make([]compiledRule, 0, len(rc.Rules))
	for i := range rc.Rules {
		cr := compiledRule{rule: rc.Rules[i]}
		if rc.Rules[i].Then.Type == "replace_path" {
			re, err := regexp.Compile(rc.Rules[i].Then.Pattern)
			if err == nil {
				cr.pathRE = re
			}
		}
		rules = append(rules, cr)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dirty := false
		for i := range rules {
			rule := &rules[i].rule
			if !rule.Enabled {
				continue
			}
			if !matchesRule(r, &rule.When) {
				continue
			}
			switch rule.Then.Type {
			case "allow":
				if dirty {
					syncRequestURI(r)
				}
				next.ServeHTTP(w, r)
				return
			case "block":
				writeBlock(w, &rule.Then)
				return
			case "set_header":
				r.Header.Set(rule.Then.Name, rule.Then.Value)
			case "del_header":
				r.Header.Del(rule.Then.Name)
			case "set_query":
				q := r.URL.Query()
				q.Set(rule.Then.Name, rule.Then.Value)
				r.URL.RawQuery = q.Encode()
				dirty = true
			case "del_query":
				q := r.URL.Query()
				q.Del(rule.Then.Name)
				r.URL.RawQuery = q.Encode()
				dirty = true
			case "set_path":
				path := rule.Then.Value
				if !strings.HasPrefix(path, "/") {
					path = "/" + path
				}
				r.URL.Path = path
				dirty = true
			case "replace_path":
				re := rules[i].pathRE
				if re == nil {
					// Validate should have rejected this at save time.
					writeJSONError(w, http.StatusInternalServerError,
						"request_check: invalid replace_path regex")
					return
				}
				path := re.ReplaceAllString(r.URL.Path, rule.Then.Value)
				if !strings.HasPrefix(path, "/") {
					path = "/" + path
				}
				r.URL.Path = path
				dirty = true
			default:
				// Unknown action type — Validate should have
				// rejected this at save time. Fail closed.
				writeJSONError(w, http.StatusInternalServerError,
					"request_check: unknown action "+rule.Then.Type)
				return
			}
		}
		// Implicit default-allow.
		if dirty {
			syncRequestURI(r)
		}
		next.ServeHTTP(w, r)
	})
}

type compiledRule struct {
	rule   service.RequestRule
	pathRE *regexp.Regexp
}

// matchesRule reports whether every populated predicate in m
// matches the current request. An empty m matches everything.
func matchesRule(r *http.Request, m *service.RequestMatch) bool {
	if m == nil {
		return true
	}
	if m.Method != "" && !strings.EqualFold(m.Method, r.Method) {
		return false
	}
	if m.PathEquals != "" && r.URL.Path != m.PathEquals {
		return false
	}
	if m.PathPrefix != "" && !strings.HasPrefix(r.URL.Path, m.PathPrefix) {
		return false
	}
	if m.HeaderEquals != nil {
		got := firstHeader(r.Header, m.HeaderEquals.Name)
		if got != m.HeaderEquals.Value {
			return false
		}
	}
	if m.HeaderPresent != "" && firstHeader(r.Header, m.HeaderPresent) == "" {
		return false
	}
	if m.HeaderAbsent != "" && firstHeader(r.Header, m.HeaderAbsent) != "" {
		return false
	}
	if m.QueryEquals != nil {
		q := r.URL.Query()
		if q.Get(m.QueryEquals.Name) != m.QueryEquals.Value {
			return false
		}
	}
	if m.QueryPresent != "" && r.URL.Query().Get(m.QueryPresent) == "" {
		return false
	}
	if m.QueryAbsent != "" && r.URL.Query().Get(m.QueryAbsent) != "" {
		return false
	}
	return true
}

// firstHeader returns the first non-empty value of the named
// header. Header names go through CanonicalHeaderKey so operators
// can type "x-tenant" or "X-Tenant" interchangeably.
func firstHeader(h http.Header, name string) string {
	if vs, ok := h[name]; ok {
		for _, v := range vs {
			if v != "" {
				return v
			}
		}
	}
	if c := http.CanonicalHeaderKey(name); c != name {
		if vs, ok := h[c]; ok {
			for _, v := range vs {
				if v != "" {
					return v
				}
			}
		}
	}
	return ""
}

// writeBlock emits the response described by a "block" action.
// Defaults: status=403, content-type=application/json, body=
// `{"message":"blocked"}`.
func writeBlock(w http.ResponseWriter, a *service.RequestAction) {
	status := a.Status
	if status == 0 {
		status = http.StatusForbidden
	}
	ct := a.ContentType
	if ct == "" {
		ct = "application/json"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(status)
	if a.Body != "" {
		_, _ = w.Write([]byte(a.Body))
	} else {
		_, _ = w.Write([]byte(`{"message":"blocked"}`))
	}
}

// syncRequestURI keeps r.RequestURI consistent with the post-
// modify URL so downstream logging middleware sees the rewritten
// shape rather than the original wire path.
func syncRequestURI(r *http.Request) {
	r.RequestURI = (&url.URL{Path: r.URL.Path, RawQuery: r.URL.RawQuery}).RequestURI()
}
