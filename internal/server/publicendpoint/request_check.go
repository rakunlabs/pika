package publicendpoint

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
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
//   - A rule may contain one action or an ordered action list.
//   - "set_*" / "del_*" / "replace_*" actions mutate the
//     request in place and evaluation continues with the next
//     action, then the next rule. This lets an operator stack
//     multiple rewrites and then terminate with a terminal rule.
//   - If no rule terminates, the request falls through to the
//     shim (default-allow). Same semantics as a firewall that
//     ends with an implicit ACCEPT.
func requestCheckMiddleware(rc *service.RequestCheck, next http.Handler) http.Handler {
	if rc == nil || len(rc.Rules) == 0 {
		return next
	}
	rules, err := compileRequestRules(rc)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decision, err := evaluateRequestRules(r, rules, false)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if decision.terminal == requestRuleTerminalBlock {
			writeBlock(w, &decision.blockAction)
			return
		}
		next.ServeHTTP(w, r)
	})
}

const (
	requestRuleTerminalAllow        = "allow"
	requestRuleTerminalBlock        = "block"
	requestRuleTerminalDefaultAllow = "default_allow"
)

// RequestRuleSnapshot is a compact view of the request before or
// after evaluating the rule list. It is returned by the admin-only
// rule tester so the UI can show what the shim would see.
type RequestRuleSnapshot struct {
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	RawQuery string            `json:"raw_query,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}

// RequestRuleBlockResult describes the response produced by a block
// action during a dry-run test.
type RequestRuleBlockResult struct {
	Status      int    `json:"status"`
	Body        string `json:"body"`
	ContentType string `json:"content_type"`
}

// RequestRuleActionTrace records one action that ran inside a
// matching rule.
type RequestRuleActionTrace struct {
	ActionIndex  int                     `json:"action_index"`
	Type         string                  `json:"type"`
	BeforePath   string                  `json:"before_path,omitempty"`
	AfterPath    string                  `json:"after_path,omitempty"`
	BeforeQuery  string                  `json:"before_query,omitempty"`
	AfterQuery   string                  `json:"after_query,omitempty"`
	HeaderName   string                  `json:"header_name,omitempty"`
	HeaderBefore string                  `json:"header_before,omitempty"`
	HeaderAfter  string                  `json:"header_after,omitempty"`
	QueryName    string                  `json:"query_name,omitempty"`
	QueryBefore  string                  `json:"query_before,omitempty"`
	QueryAfter   string                  `json:"query_after,omitempty"`
	Terminal     bool                    `json:"terminal,omitempty"`
	Block        *RequestRuleBlockResult `json:"block,omitempty"`
}

// RequestRuleTrace records a matched rule and the actions it ran.
type RequestRuleTrace struct {
	RuleIndex int                      `json:"rule_index"`
	RuleName  string                   `json:"rule_name,omitempty"`
	Actions   []RequestRuleActionTrace `json:"actions"`
}

// RequestRuleTestResult is returned by TestRequestRules and the
// admin API dry-run endpoint.
type RequestRuleTestResult struct {
	Initial      RequestRuleSnapshot     `json:"initial"`
	Final        RequestRuleSnapshot     `json:"final"`
	Terminal     string                  `json:"terminal"`
	MatchedRules []RequestRuleTrace      `json:"matched_rules"`
	Block        *RequestRuleBlockResult `json:"block,omitempty"`
}

type requestRuleDecision struct {
	terminal    string
	blockAction service.RequestAction
	result      *RequestRuleTestResult
}

// TestRequestRules runs a draft RequestCheck against a synthetic
// request and returns a trace. It is intentionally backed by the same
// evaluator used by requestCheckMiddleware so the UI dry-run cannot
// drift from runtime semantics.
func TestRequestRules(rc *service.RequestCheck, method, path string, headers map[string]string) (*RequestRuleTestResult, error) {
	r, err := newRuleTestRequest(method, path, headers)
	if err != nil {
		return nil, err
	}
	rules, err := compileRequestRules(rc)
	if err != nil {
		return nil, err
	}
	decision, err := evaluateRequestRules(r, rules, true)
	if err != nil {
		return nil, err
	}
	return decision.result, nil
}

func compileRequestRules(rc *service.RequestCheck) ([]compiledRule, error) {
	if rc == nil || len(rc.Rules) == 0 {
		return nil, nil
	}
	// Snapshot the rules so a downstream mutation (which should
	// not happen — the manager hands us a frozen slice) cannot
	// race with the request loop.
	rules := make([]compiledRule, 0, len(rc.Rules))
	for i := range rc.Rules {
		cr := compiledRule{rule: rc.Rules[i]}
		actions := effectiveRuleActions(&rc.Rules[i])
		for j := range actions {
			if actions[j].Type == "replace_path" {
				re, err := regexp.Compile(actions[j].Pattern)
				if err != nil {
					return nil, fmt.Errorf("request_check.rules[%d].actions[%d].replace_path: invalid regex: %w", i, j, err)
				}
				if cr.pathRE == nil {
					cr.pathRE = make(map[int]*regexp.Regexp)
				}
				cr.pathRE[j] = re
			}
		}
		rules = append(rules, cr)
	}
	return rules, nil
}

func newRuleTestRequest(method, path string, headers map[string]string) (*http.Request, error) {
	method = strings.TrimSpace(method)
	if method == "" {
		method = http.MethodGet
	}
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u, err := url.ParseRequestURI(path)
	if err != nil {
		return nil, fmt.Errorf("invalid test path %q: %w", path, err)
	}
	r := &http.Request{
		Method:     method,
		URL:        u,
		Header:     make(http.Header),
		RequestURI: u.RequestURI(),
	}
	for k, v := range headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		r.Header.Set(k, v)
	}
	return r, nil
}

func evaluateRequestRules(r *http.Request, rules []compiledRule, collectTrace bool) (requestRuleDecision, error) {
	dirty := false
	var result *RequestRuleTestResult
	if collectTrace {
		result = &RequestRuleTestResult{
			Initial:      snapshotRequest(r),
			MatchedRules: []RequestRuleTrace{},
		}
	}
	finish := func(terminal string) requestRuleDecision {
		if dirty && terminal != requestRuleTerminalBlock {
			syncRequestURI(r)
		}
		if result != nil {
			result.Terminal = terminal
			result.Final = snapshotRequest(r)
		}
		return requestRuleDecision{terminal: terminal, result: result}
	}

	for i := range rules {
		rule := &rules[i].rule
		if !rule.Enabled {
			continue
		}
		if !matchesRule(r, &rule.When) {
			continue
		}
		var ruleTrace RequestRuleTrace
		if result != nil {
			ruleTrace = RequestRuleTrace{RuleIndex: i, RuleName: rule.Name}
		}
		actions := effectiveRuleActions(rule)
		for j := range actions {
			action := &actions[j]
			actionTrace := RequestRuleActionTrace{
				ActionIndex: j,
				Type:        action.Type,
				BeforePath:  r.URL.Path,
				BeforeQuery: r.URL.RawQuery,
			}
			switch action.Type {
			case "allow":
				actionTrace.Terminal = true
				actionTrace.AfterPath = r.URL.Path
				actionTrace.AfterQuery = r.URL.RawQuery
				if result != nil {
					ruleTrace.Actions = append(ruleTrace.Actions, actionTrace)
					result.MatchedRules = append(result.MatchedRules, ruleTrace)
				}
				return finish(requestRuleTerminalAllow), nil
			case "block":
				block := blockResponse(action)
				actionTrace.Terminal = true
				actionTrace.Block = &block
				actionTrace.AfterPath = r.URL.Path
				actionTrace.AfterQuery = r.URL.RawQuery
				if result != nil {
					ruleTrace.Actions = append(ruleTrace.Actions, actionTrace)
					result.MatchedRules = append(result.MatchedRules, ruleTrace)
					result.Block = &block
				}
				decision := finish(requestRuleTerminalBlock)
				decision.blockAction = *action
				return decision, nil
			case "set_header":
				actionTrace.HeaderName = action.Name
				actionTrace.HeaderBefore = firstHeader(r.Header, action.Name)
				r.Header.Set(action.Name, action.Value)
				actionTrace.HeaderAfter = firstHeader(r.Header, action.Name)
			case "del_header":
				actionTrace.HeaderName = action.Name
				actionTrace.HeaderBefore = firstHeader(r.Header, action.Name)
				r.Header.Del(action.Name)
				actionTrace.HeaderAfter = firstHeader(r.Header, action.Name)
			case "set_query":
				actionTrace.QueryName = action.Name
				actionTrace.QueryBefore = r.URL.Query().Get(action.Name)
				q := r.URL.Query()
				q.Set(action.Name, action.Value)
				r.URL.RawQuery = q.Encode()
				actionTrace.QueryAfter = r.URL.Query().Get(action.Name)
				dirty = true
			case "del_query":
				actionTrace.QueryName = action.Name
				actionTrace.QueryBefore = r.URL.Query().Get(action.Name)
				q := r.URL.Query()
				q.Del(action.Name)
				r.URL.RawQuery = q.Encode()
				actionTrace.QueryAfter = r.URL.Query().Get(action.Name)
				dirty = true
			case "set_path":
				path := action.Value
				if !strings.HasPrefix(path, "/") {
					path = "/" + path
				}
				r.URL.Path = path
				dirty = true
			case "replace_path":
				re := rules[i].pathRE[j]
				if re == nil {
					return requestRuleDecision{}, fmt.Errorf("request_check: invalid replace_path regex")
				}
				path, err := replacePath(re, r.URL.Path, action)
				if err != nil {
					return requestRuleDecision{}, err
				}
				if !strings.HasPrefix(path, "/") {
					path = "/" + path
				}
				r.URL.Path = path
				dirty = true
			default:
				return requestRuleDecision{}, fmt.Errorf("request_check: unknown action %s", action.Type)
			}
			actionTrace.AfterPath = r.URL.Path
			actionTrace.AfterQuery = r.URL.RawQuery
			if result != nil {
				ruleTrace.Actions = append(ruleTrace.Actions, actionTrace)
			}
		}
		if result != nil {
			result.MatchedRules = append(result.MatchedRules, ruleTrace)
		}
	}
	return finish(requestRuleTerminalDefaultAllow), nil
}

type compiledRule struct {
	rule   service.RequestRule
	pathRE map[int]*regexp.Regexp
}

func effectiveRuleActions(rule *service.RequestRule) []service.RequestAction {
	if len(rule.Actions) > 0 {
		return rule.Actions
	}
	if rule.Then.Type == "" {
		return nil
	}
	return []service.RequestAction{rule.Then}
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

func snapshotRequest(r *http.Request) RequestRuleSnapshot {
	headers := make(map[string]string, len(r.Header))
	for k, vs := range r.Header {
		if len(vs) > 0 {
			headers[k] = vs[0]
		}
	}
	if len(headers) == 0 {
		headers = nil
	}
	return RequestRuleSnapshot{
		Method:   r.Method,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
		Headers:  headers,
	}
}

func blockResponse(a *service.RequestAction) RequestRuleBlockResult {
	status := a.Status
	if status == 0 {
		status = http.StatusForbidden
	}
	ct := a.ContentType
	if ct == "" {
		ct = "application/json"
	}
	body := a.Body
	if body == "" {
		body = `{"message":"blocked"}`
	}
	return RequestRuleBlockResult{Status: status, Body: body, ContentType: ct}
}

func replacePath(re *regexp.Regexp, path string, action *service.RequestAction) (string, error) {
	if len(action.CaptureTransforms) == 0 {
		return re.ReplaceAllString(path, action.Value), nil
	}
	matches := re.FindAllStringSubmatchIndex(path, -1)
	if len(matches) == 0 {
		return path, nil
	}
	names := re.SubexpNames()
	var b strings.Builder
	last := 0
	for _, match := range matches {
		b.WriteString(path[last:match[0]])
		captures := capturesForMatch(path, match)
		for _, tr := range action.CaptureTransforms {
			idx, ok := captureIndex(names, strings.TrimSpace(tr.Capture))
			if !ok || idx >= len(captures) {
				return "", fmt.Errorf("request_check: capture %q does not exist in replace_path pattern", tr.Capture)
			}
			captures[idx] = strings.ReplaceAll(captures[idx], tr.Find, tr.Value)
		}
		b.WriteString(expandPathReplacement(action.Value, captures, names))
		last = match[1]
	}
	b.WriteString(path[last:])
	return b.String(), nil
}

func capturesForMatch(src string, match []int) []string {
	captures := make([]string, len(match)/2)
	for i := range captures {
		start, end := match[2*i], match[2*i+1]
		if start >= 0 && end >= 0 {
			captures[i] = src[start:end]
		}
	}
	return captures
}

func expandPathReplacement(template string, captures []string, names []string) string {
	var b strings.Builder
	for i := 0; i < len(template); i++ {
		if template[i] != '$' || i+1 >= len(template) {
			b.WriteByte(template[i])
			continue
		}
		if template[i+1] == '$' {
			b.WriteByte('$')
			i++
			continue
		}
		if template[i+1] == '{' {
			end := strings.IndexByte(template[i+2:], '}')
			if end < 0 {
				b.WriteByte(template[i])
				continue
			}
			name := template[i+2 : i+2+end]
			b.WriteString(captureValue(captures, names, name))
			i += end + 2
			continue
		}
		j := i + 1
		for j < len(template) && isCaptureNameChar(template[j]) {
			j++
		}
		if j == i+1 {
			b.WriteByte(template[i])
			continue
		}
		b.WriteString(captureValue(captures, names, template[i+1:j]))
		i = j - 1
	}
	return b.String()
}

func captureValue(captures []string, names []string, name string) string {
	idx, ok := captureIndex(names, name)
	if !ok || idx >= len(captures) {
		return ""
	}
	return captures[idx]
}

func captureIndex(names []string, capture string) (int, bool) {
	if n, err := strconv.Atoi(capture); err == nil {
		return n, n >= 0 && n < len(names)
	}
	for i, name := range names {
		if i > 0 && name == capture {
			return i, true
		}
	}
	return -1, false
}

func isCaptureNameChar(c byte) bool {
	return c == '_' ||
		(c >= '0' && c <= '9') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z')
}

// writeBlock emits the response described by a "block" action.
// Defaults: status=403, content-type=application/json, body=
// `{"message":"blocked"}`.
func writeBlock(w http.ResponseWriter, a *service.RequestAction) {
	block := blockResponse(a)
	w.Header().Set("Content-Type", block.ContentType)
	w.WriteHeader(block.Status)
	_, _ = w.Write([]byte(block.Body))
}

// syncRequestURI keeps r.RequestURI consistent with the post-
// modify URL so downstream logging middleware sees the rewritten
// shape rather than the original wire path.
func syncRequestURI(r *http.Request) {
	r.RequestURI = (&url.URL{Path: r.URL.Path, RawQuery: r.URL.RawQuery}).RequestURI()
}
