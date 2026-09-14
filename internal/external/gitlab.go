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

// GitLab exposes the group's own CI/CD variables for one exact environment
// scope. Keeping the scope on the resource makes keys unambiguous even when
// GitLab has multiple variables with the same name.
type GitLab struct {
	Address          string `json:"address"`
	Token            string `json:"token"`
	Group            string `json:"group"`
	EnvironmentScope string `json:"environment_scope,omitempty"`
	Proxy            string `json:"proxy,omitempty"`
	ProxyMode        string `json:"proxy_mode,omitempty"`
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
	if strings.TrimSpace(p.Config.Group) == "" || strings.TrimSpace(p.Config.Token) == "" {
		return fmt.Errorf("gitlab: group and access token are required")
	}
	return validateProxyConfig(p.Config.ProxyMode, p.Config.Proxy)
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
	if err := p.Validate(); err != nil {
		return nil, nil, 0, err
	}
	endpoint := strings.TrimRight(p.Config.Address, "/") + "/api/v4/groups/" + url.PathEscape(p.Config.Group) + "/variables"
	if key != "" {
		if !gitLabKey.MatchString(key) {
			return nil, nil, 0, fmt.Errorf("gitlab: variable key must contain only letters, digits, or underscores (maximum 255 characters)")
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
			if v.Scope == p.scope() && strings.HasPrefix(v.Key, prefix) {
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
		Value *string `json:"value"`
	}
	if err := json.Unmarshal(body, &variable); err != nil {
		return nil, fmt.Errorf("gitlab: invalid variable response")
	}
	if variable.Value == nil {
		return nil, fmt.Errorf("gitlab: variable value is hidden or unavailable")
	}
	data := map[string]any{"value": *variable.Value}
	raw, _ := json.Marshal(data)
	return &Entry{Data: data, Raw: raw, ContentType: "application/json"}, nil
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
	if !ok || len(data) != 1 {
		return fmt.Errorf("gitlab: expected a single string field named value")
	}
	// Check exact-scope existence before choosing create/update. Some GitLab
	// versions fall back to an unscoped lookup on PUT of a missing variable.
	// Do not use PUT as an existence probe or recreate after a failed update.
	_, _, status, err := p.request(ctx, http.MethodGet, key, nil, nil)
	if status == http.StatusNotFound {
		_, _, _, err = p.request(ctx, http.MethodPost, "", nil, map[string]any{"key": key, "value": value, "environment_scope": p.scope()})
		return err
	}
	if err != nil {
		return err
	}
	// Only update the value: preserve protected/masked/raw/file metadata.
	_, _, _, err = p.request(ctx, http.MethodPut, key, nil, map[string]any{"value": value})
	return err
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
