package external

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// GitLab exposes a group's or project's own CI/CD variables for one exact environment
// scope. Keeping the scope on the resource makes keys unambiguous even when
// GitLab has multiple variables with the same name.
type GitLab struct {
	Address           string  `json:"address"`
	Token             string  `json:"token"`
	Group             string  `json:"group,omitempty"`
	Project           string  `json:"project,omitempty"`
	EnvironmentScope  string  `json:"environment_scope,omitempty"`
	VariableAllowlist *string `json:"variable_allowlist,omitempty"`

	// NewKeyPolicy decides what happens when someone creates a variable the
	// allowlist does not cover. It only matters while an allowlist is
	// configured; without one every name is already permitted.
	//
	//   - "" / "deny"  — reject the create (the original behaviour, kept as
	//     the default so upgrades never widen access).
	//   - "allow"      — create it, but leave the allowlist alone. The new
	//     variable is then invisible through this resource, which is what
	//     you want when pika may seed values it must not read back.
	//   - "append"     — create it and add the exact name to the allowlist,
	//     so the variable stays manageable afterwards. The settings write
	//     happens server-side in service.WriteExternal; the provider itself
	//     has no storage access.
	//
	// Updates to *existing* variables are never covered by this policy: a
	// name outside the allowlist stays untouchable once it exists.
	NewKeyPolicy string `json:"new_key_policy,omitempty"`

	Proxy     string `json:"proxy,omitempty"`
	ProxyMode string `json:"proxy_mode,omitempty"`
}

// New-key policy values for GitLab.NewKeyPolicy.
const (
	GitLabNewKeyDeny   = "deny"
	GitLabNewKeyAllow  = "allow"
	GitLabNewKeyAppend = "append"
)

// GetNewKeyPolicy normalises NewKeyPolicy, defaulting to "deny". A nil
// receiver also returns "deny" so callers don't need a separate guard.
func (g *GitLab) GetNewKeyPolicy() string {
	if g == nil {
		return GitLabNewKeyDeny
	}
	switch strings.TrimSpace(g.NewKeyPolicy) {
	case GitLabNewKeyAllow:
		return GitLabNewKeyAllow
	case GitLabNewKeyAppend:
		return GitLabNewKeyAppend
	default:
		return GitLabNewKeyDeny
	}
}

// AllowsVariable reports whether key passes the configured allowlist. No
// allowlist means everything passes. Exposed so the service layer can decide
// whether a freshly created key needs appending without re-implementing the
// matching rules.
func (g *GitLab) AllowsVariable(key string) (bool, error) {
	p := &GitLabProvider{Config: g}
	allowlist, err := p.variableAllowlist()
	if err != nil {
		return false, err
	}
	return allowlist == nil || allowlist.MatchString(key), nil
}

// AppendAllowedVariable adds key to the allowlist as an exact-name rule and
// reports whether anything changed. It is a no-op when there is no allowlist
// (nothing to widen) or when the key already matches. The caller persists the
// updated config.
func (g *GitLab) AppendAllowedVariable(key string) (bool, error) {
	if g == nil || g.VariableAllowlist == nil {
		return false, nil
	}
	if !gitLabKey.MatchString(key) {
		return false, fmt.Errorf("gitlab: invalid variable key")
	}
	allowed, err := g.AllowsVariable(key)
	if err != nil || allowed {
		return false, err
	}
	updated := strings.TrimRight(*g.VariableAllowlist, "\n")
	if updated != "" {
		updated += "\n"
	}
	updated += key
	g.VariableAllowlist = &updated
	return true, nil
}

type GitLabProvider struct{ Config *GitLab }

var _ Provider = (*GitLabProvider)(nil)
var gitLabKey = regexp.MustCompile(`^[A-Za-z0-9_]{1,255}$`)

func (p *GitLabProvider) Kind() string { return "gitlab" }
func (p *GitLabProvider) Capabilities() Capabilities {
	return Capabilities{CanRead: true, CanList: true, CanWrite: true, CanDelete: true}
}

func (p *GitLabProvider) Validate() error {
	if p.Config == nil {
		return fmt.Errorf("gitlab: config is required")
	}
	u, err := url.Parse(p.Config.Address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("gitlab: a valid HTTP(S) instance URL is required")
	}
	if (strings.TrimSpace(p.Config.Group) == "") == (strings.TrimSpace(p.Config.Project) == "") {
		return fmt.Errorf("gitlab: configure exactly one group or project")
	}
	if strings.TrimSpace(p.Config.Token) == "" {
		return fmt.Errorf("gitlab: access token is required")
	}
	if _, err := p.variableAllowlist(); err != nil {
		return err
	}
	switch strings.TrimSpace(p.Config.NewKeyPolicy) {
	case "", GitLabNewKeyDeny, GitLabNewKeyAllow, GitLabNewKeyAppend:
	default:
		return fmt.Errorf("gitlab: new key policy must be deny, allow, or append")
	}
	return validateProxyConfig(p.Config.ProxyMode, p.Config.Proxy)
}

// A missing list preserves unrestricted resources; an explicitly empty list
// denies all. Anchor each rule to the entire key, including regex alternatives.
func (p *GitLabProvider) variableAllowlist() (*regexp.Regexp, error) {
	if p.Config == nil {
		return nil, fmt.Errorf("gitlab: config is required")
	}
	if p.Config.VariableAllowlist == nil {
		return nil, nil
	}
	patterns := []string{`\b\B`}
	for i, line := range strings.Split(*p.Config.VariableAllowlist, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pattern := regexp.QuoteMeta(line)
		if strings.HasPrefix(line, "/") && strings.HasSuffix(line, "/") && len(line) >= 2 {
			pattern = line[1 : len(line)-1]
		} else if !gitLabKey.MatchString(line) {
			return nil, fmt.Errorf("gitlab: variable allowlist line %d must be a variable name or /regex/", i+1)
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return nil, fmt.Errorf("gitlab: invalid variable allowlist regex on line %d", i+1)
		}
		patterns = append(patterns, `\A(?:` + pattern + `)\z`)
	}
	return regexp.Compile(strings.Join(patterns, "|"))
}

func (p *GitLabProvider) scope() string {
	if p.Config.EnvironmentScope == "" {
		return "*"
	}
	return p.Config.EnvironmentScope
}

// request never includes upstream response bodies in errors: they can contain
// variable values. Redirects are rejected to keep PRIVATE-TOKEN on this host.
func (p *GitLabProvider) request(ctx context.Context, method, key string, query url.Values, data any) ([]byte, http.Header, int, error) {
	return p.requestAllowing(ctx, method, key, query, data, false)
}

// requestAllowing is request with an explicit allowlist escape hatch. Only the
// create path sets skipAllowlist, and only after establishing that the key is
// new and the resource's new-key policy permits it — see Write.
func (p *GitLabProvider) requestAllowing(ctx context.Context, method, key string, query url.Values, data any, skipAllowlist bool) ([]byte, http.Header, int, error) {
	if err := p.Validate(); err != nil {
		return nil, nil, 0, err
	}
	target, namespace := p.Config.Group, "groups"
	if p.Config.Project != "" {
		target, namespace = p.Config.Project, "projects"
	}
	endpoint := strings.TrimRight(p.Config.Address, "/") + "/api/v4/" + namespace + "/" + url.PathEscape(target) + "/variables"
	if key != "" {
		if !gitLabKey.MatchString(key) {
			return nil, nil, 0, fmt.Errorf("gitlab: variable key must contain only letters, digits, or underscores (maximum 255 characters)")
		}
		if !skipAllowlist {
			allowlist, err := p.variableAllowlist()
			if err != nil {
				return nil, nil, 0, err
			}
			if allowlist != nil && !allowlist.MatchString(key) {
				return nil, nil, 0, fmt.Errorf("gitlab: variable access denied by allowlist")
			}
		}
		endpoint += "/" + key
		if query == nil {
			query = url.Values{}
		}
		query.Set("filter[environment_scope]", p.scope())
	}
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var body []byte
	if data != nil {
		var err error
		body, err = json.Marshal(data)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("gitlab: encode request: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, nil, 0, err
	}
	req.Header.Set("PRIVATE-TOKEN", p.Config.Token)
	req.Header.Set("Content-Type", "application/json")
	client := newHTTPClient(p.Config.ProxyMode, p.Config.Proxy, nil)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("gitlab: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.Header, resp.StatusCode, fmt.Errorf("gitlab: HTTP %d (%s)", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20+1))
	if err != nil {
		return nil, nil, resp.StatusCode, fmt.Errorf("gitlab: read response: %w", err)
	}
	if len(b) > 16<<20 {
		return nil, nil, resp.StatusCode, fmt.Errorf("gitlab: response exceeds 16 MiB")
	}
	return b, resp.Header, resp.StatusCode, nil
}

func (p *GitLabProvider) List(ctx context.Context, prefix string) ([]string, error) {
	keys := []string{}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	allowlist, err := p.variableAllowlist()
	if err != nil {
		return nil, err
	}
	for page := 1; ; {
		body, headers, _, err := p.request(ctx, http.MethodGet, "", url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}, nil)
		if err != nil {
			return nil, err
		}
		var variables []struct {
			Key   string `json:"key"`
			Scope string `json:"environment_scope"`
		}
		if err := json.Unmarshal(body, &variables); err != nil {
			return nil, fmt.Errorf("gitlab: invalid variable list")
		}
		for _, v := range variables {
			if v.Scope == "" {
				v.Scope = "*"
			}
			if v.Scope == p.scope() && strings.HasPrefix(v.Key, prefix) && (allowlist == nil || allowlist.MatchString(v.Key)) {
				keys = append(keys, strings.TrimPrefix(v.Key, prefix))
			}
		}
		next := headers.Get("X-Next-Page")
		if next == "" {
			if len(variables) < 100 {
				break
			}
			page++ // Some proxies strip pagination headers.
		} else {
			n, err := strconv.Atoi(next)
			if err != nil || n <= page {
				return nil, fmt.Errorf("gitlab: invalid pagination response")
			}
			page = n
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (p *GitLabProvider) Read(ctx context.Context, key string) (*Entry, error) {
	if key == "" {
		return nil, fmt.Errorf("gitlab: variable key is required")
	}
	body, _, _, err := p.request(ctx, http.MethodGet, key, nil, nil)
	if err != nil {
		return nil, err
	}
	var variable struct {
		Value        *string `json:"value"`
		Masked       bool    `json:"masked"`
		Protected    bool    `json:"protected"`
		Raw          bool    `json:"raw"`
		VariableType string  `json:"variable_type"`
		Hidden       bool    `json:"hidden"`
	}
	if err := json.Unmarshal(body, &variable); err != nil {
		return nil, fmt.Errorf("gitlab: invalid variable response")
	}
	if variable.Value == nil {
		return nil, fmt.Errorf("gitlab: variable value is hidden or unavailable")
	}
	data := map[string]any{"value": *variable.Value}
	raw, _ := json.Marshal(data)
	return &Entry{Data: data, Raw: raw, ContentType: "application/json", Metadata: map[string]any{
		"masked": variable.Masked, "protected": variable.Protected,
		"raw": variable.Raw, "variable_type": variable.VariableType,
		"hidden": variable.Hidden,
	}}, nil
}

func (p *GitLabProvider) Fetch(ctx context.Context, key string) ([]byte, error) {
	e, err := p.Read(ctx, key)
	if err != nil {
		return nil, err
	}
	return e.Raw, nil
}

func (p *GitLabProvider) Write(ctx context.Context, key string, data map[string]any) error {
	if !gitLabKey.MatchString(key) {
		return fmt.Errorf("gitlab: invalid variable key")
	}
	value, ok := data["value"].(string)
	if !ok {
		return fmt.Errorf("gitlab: value must be a string")
	}
	// Optional settings support explicit false as well as true. Omitted fields
	// stay omitted so existing value-only API clients preserve upstream settings.
	payload := map[string]any{"value": value}
	for field, v := range data {
		switch field {
		case "value":
		case "masked", "protected", "raw":
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("gitlab: %s must be a boolean", field)
			}
			payload[field] = v
		case "variable_type":
			if v != "env_var" && v != "file" {
				return fmt.Errorf("gitlab: variable_type must be env_var or file")
			}
			payload[field] = v
		default:
			return fmt.Errorf("gitlab: unsupported variable setting %q", field)
		}
	}
	// Keys outside the allowlist are normally rejected before any upstream
	// call. A non-deny new-key policy relaxes that for creates only, so we
	// probe with the allowlist bypassed and re-deny if the variable turns
	// out to already exist.
	allowed, err := p.Config.AllowsVariable(key)
	if err != nil {
		return err
	}
	bypass := false
	if !allowed {
		if p.Config.GetNewKeyPolicy() == GitLabNewKeyDeny {
			return fmt.Errorf("gitlab: variable access denied by allowlist")
		}
		bypass = true
	}

	// Check exact-scope existence before choosing create/update. Some GitLab
	// versions fall back to an unscoped lookup on PUT of a missing variable.
	// Do not use PUT as an existence probe or recreate after a failed update.
	_, _, status, err := p.requestAllowing(ctx, http.MethodGet, key, nil, nil, bypass)
	if status == http.StatusNotFound {
		payload["key"] = key
		payload["environment_scope"] = p.scope()
		_, _, _, err = p.requestAllowing(ctx, http.MethodPost, "", nil, payload, bypass)
		return err
	}
	if err != nil {
		return err
	}
	if bypass {
		return fmt.Errorf("gitlab: variable access denied by allowlist")
	}
	_, _, _, err = p.requestAllowing(ctx, http.MethodPut, key, nil, payload, false)
	return err
}

// Exists reports whether the variable already exists in this resource's exact
// environment scope, without reading its value. It powers the create/update
// split in the resource access guard (see access.go). Keys blocked by the
// allowlist report an error rather than "absent", unless a non-deny new-key
// policy is in force — in that case they are genuinely candidates for
// creation and the probe runs with the allowlist bypassed.
func (p *GitLabProvider) Exists(ctx context.Context, key string) (bool, error) {
	if !gitLabKey.MatchString(key) {
		return false, fmt.Errorf("gitlab: invalid variable key")
	}
	allowed, err := p.Config.AllowsVariable(key)
	if err != nil {
		return false, err
	}
	bypass := false
	if !allowed {
		if p.Config.GetNewKeyPolicy() == GitLabNewKeyDeny {
			return false, fmt.Errorf("gitlab: variable access denied by allowlist")
		}
		bypass = true
	}
	_, _, status, err := p.requestAllowing(ctx, http.MethodGet, key, nil, nil, bypass)
	if status == http.StatusNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (p *GitLabProvider) Delete(ctx context.Context, key string) error {
	if key == "" {
		return fmt.Errorf("gitlab: variable key is required")
	}
	_, _, status, err := p.request(ctx, http.MethodDelete, key, nil, nil)
	if status == http.StatusNotFound {
		return nil
	}
	return err
}

func (p *GitLabProvider) ListVersions(context.Context, string) ([]Version, error) {
	return nil, ErrNotSupported
}
func (p *GitLabProvider) ReadVersion(context.Context, string, string) (*Entry, error) {
	return nil, ErrNotSupported
}
func (p *GitLabProvider) Test(ctx context.Context) TestResult {
	keys, err := p.List(ctx, "")
	if err != nil {
		return TestResult{Message: err.Error()}
	}
	return TestResult{OK: true, Message: fmt.Sprintf("Reachable. %d variable(s) in environment scope %q.", len(keys), p.scope()), Sample: capSample(keys, 10)}
}
