package mcpsrv_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rakunlabs/pika/internal/server/authx"
	"github.com/rakunlabs/pika/internal/server/mcpsrv"
	"github.com/rakunlabs/pika/internal/service"
	bwstore "github.com/rakunlabs/pika/internal/storage/bw"
)

func newTestService(t *testing.T) *service.Service {
	t.Helper()

	store, err := bwstore.New(t.Context(), &bwstore.Config{InMemory: true})
	if err != nil {
		t.Fatalf("bw.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	return service.New(store)
}

// newAuthManager builds the real auth manager so tests drive the actual
// middleware chain — ada's request authentication, the apikey strategy,
// and pika's capability resolver — rather than a stub of it. That chain is
// precisely what this handler delegates to, so faking it would test
// nothing.
func newAuthManager(t *testing.T, svc *service.Service) *authx.Manager {
	t.Helper()

	mgr := authx.New(authx.Deps{
		Svc:          svc,
		SessionStore: authx.NewSessionStore(svc, "pika_session"),
		BasePath:     "/api/v1/",
		CookieName:   "pika_session",
	})
	if err := mgr.Boot(t.Context(), &service.AuthSettings{}); err != nil {
		t.Fatalf("auth manager boot: %v", err)
	}

	return mgr
}

// newToken mints a real API token so tests exercise the credential an MCP
// client actually presents, rather than a stubbed-out authorization state.
func newToken(t *testing.T, svc *service.Service, name string, scopes ...service.TokenScope) string {
	t.Helper()

	res, err := svc.CreateToken(t.Context(), &service.CreateTokenRequest{Name: name, Scopes: scopes})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	return res.RawKey
}

func scope(path string, ops ...string) service.TokenScope {
	return service.TokenScope{Path: path, Operations: ops}
}

// newTokenServer mounts the handler behind the production middleware
// chain, exactly as api.Handle does on the protected mux.
func newTokenServer(t *testing.T, svc *service.Service) *httptest.Server {
	t.Helper()

	mgr := newAuthManager(t, svc)
	handler := mgr.Require()(mgr.CapMiddleware()(mcpsrv.New(svc, "pika-test", "v0.0.0-test")))

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return srv
}

// newCapServer mounts the handler behind a stand-in for CapMiddleware,
// planting the capability set and path patterns a resolved session would
// carry. Used for the capability-shaped cases (external tools, per-user
// patterns) where minting a real cookie session would add setup without
// adding coverage — the handler reads only what the middleware leaves on
// the context either way.
func newCapServer(t *testing.T, svc *service.Service, caps []string, patterns map[string][]string) *httptest.Server {
	t.Helper()

	handler := mcpsrv.New(svc, "pika-test", "v0.0.0-test")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ctx = service.WithUserInfo(ctx, "alice", "user-1")
		ctx = service.WithCapabilities(ctx, caps)
		ctx = service.WithCapabilityPatterns(ctx, patterns)

		handler.ServeHTTP(w, r.WithContext(ctx))
	}))
	t.Cleanup(srv.Close)

	return srv
}

// connect drives a real MCP client over streamable HTTP against the
// endpoint. Exercising the full transport is the point: it proves the
// credential is accepted and that the caller's authorization survives the
// SDK detaching the handler context from the HTTP request.
func connect(t *testing.T, srv *httptest.Server, header http.Header) *mcp.ClientSession {
	t.Helper()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.0"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL,
		HTTPClient: &http.Client{Transport: headerTransport{header: header}},
		// The server runs stateless, so it answers the standalone SSE
		// GET with 405. Not asking for it keeps the test honest about
		// what the endpoint actually supports.
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	return session
}

type headerTransport struct {
	header http.Header
}

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	for key, values := range h.header {
		for _, v := range values {
			r.Header.Add(key, v)
		}
	}

	return http.DefaultTransport.RoundTrip(r)
}

func bearer(key string) http.Header {
	return http.Header{"Authorization": []string{"Bearer " + key}}
}

// connectWithToken is the common case: a token client against the real
// middleware chain.
func connectWithToken(t *testing.T, svc *service.Service, key string) *mcp.ClientSession {
	t.Helper()

	return connect(t, newTokenServer(t, svc), bearer(key))
}

func toolNames(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()

	res, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)

	return names
}

// callTool invokes a tool and decodes its structured output into out.
// A tool-level error fails the test; use callToolExpectError for the
// negative cases.
func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any, out any) {
	t.Helper()

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if res.IsError {
		t.Fatalf("CallTool(%s) returned tool error: %s", name, contentText(res))
	}
	if out == nil {
		return
	}

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode structured content %s: %v", raw, err)
	}
}

// callToolExpectError asserts the tool reported a failure the model can
// read, rather than a protocol-level error.
func callToolExpectError(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): unexpected protocol error: %v", name, err)
	}
	if !res.IsError {
		t.Fatalf("CallTool(%s): expected a tool error, got success", name)
	}

	return contentText(res)
}

func contentText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, content := range res.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}

	return b.String()
}

// TestBearerTokenIsAccepted is the regression guard for the property the
// whole endpoint rests on: an API token must authenticate on the protected
// mux. ada's session middleware used to understand only the session
// cookie, so a Bearer request routed through it was answered with a 307 to
// the login page and no MCP client could ever connect.
func TestBearerTokenIsAccepted(t *testing.T) {
	svc := newTestService(t)
	key := newToken(t, svc, "agent", scope("**", "read"))

	session := connectWithToken(t, svc, key)

	if got := session.InitializeResult().ServerInfo; got.Name != "pika-test" {
		t.Fatalf("unexpected server info: %+v", got)
	}
}

// TestRejectsBadCredentials covers the ways a request fails before any MCP
// framing exists, where the answer has to be a plain HTTP status. The
// statuses come from the middleware chain, not from this package — the
// test pins them because an MCP client has to distinguish "authenticate
// differently" from "you are not allowed".
func TestRejectsBadCredentials(t *testing.T) {
	svc := newTestService(t)
	key := newToken(t, svc, "revoked", scope("**", "read"))

	tokens, _, err := svc.ListTokens(t.Context(), nil)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("ListTokens: %v (%d tokens)", err, len(tokens))
	}
	inactive := false
	if err := svc.PatchToken(t.Context(), tokens[0].ID, &service.PatchTokenRequest{Active: &inactive}); err != nil {
		t.Fatalf("PatchToken: %v", err)
	}

	srv := newTokenServer(t, svc)

	tests := []struct {
		name   string
		header http.Header
		want   int
	}{
		// No credentials is the browser case: redirected to the login UI.
		{name: "no credentials", header: nil, want: http.StatusTemporaryRedirect},
		{name: "unknown token", header: bearer("pika_nope"), want: http.StatusUnauthorized},
		{name: "disabled token", header: bearer(key), want: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			for key, values := range tt.header {
				req.Header[key] = values
			}

			// Do not follow the login redirect — the status is the
			// assertion, and the login page redirects to itself.
			client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}}

			res, err := client.Do(req)
			if err != nil {
				t.Fatalf("POST: %v", err)
			}
			defer func() { _ = res.Body.Close() }()

			if res.StatusCode != tt.want {
				t.Errorf("expected %d, got %d", tt.want, res.StatusCode)
			}
			if tt.want == http.StatusUnauthorized && res.Header.Get("WWW-Authenticate") == "" {
				t.Error("401 should advertise WWW-Authenticate so a client can discover how to authenticate")
			}
		})
	}
}

// TestTokenToolVisibility is load-bearing for the design: tools/list must
// reflect the token's scope operations, so an agent never plans around a
// tool it will be denied.
func TestTokenToolVisibility(t *testing.T) {
	svc := newTestService(t)

	tests := []struct {
		name     string
		scopes   []service.TokenScope
		wantSome []string
		wantNone []string
	}{
		{
			name:     "read only",
			scopes:   []service.TokenScope{scope("**", "read")},
			wantSome: []string{"get_config", "get_resolved_config", "list_folder", "list_variants", "list_versions", "search_configs"},
			wantNone: []string{"set_config", "delete_config", "delete_folder"},
		},
		{
			name:     "read and write, no delete",
			scopes:   []service.TokenScope{scope("**", "read", "write")},
			wantSome: []string{"get_config", "set_config"},
			wantNone: []string{"delete_config", "delete_folder"},
		},
		{
			name:     "wildcard operation",
			scopes:   []service.TokenScope{scope("**", "*")},
			wantSome: []string{"get_config", "set_config", "delete_config", "delete_folder"},
		},
		{
			name:   "external tools are never reachable by a token",
			scopes: []service.TokenScope{scope("**", "*")},
			// Tokens carry no capability list, and /api/v1/external/*
			// requires external.read / external.write. Exposing those
			// tools to a token would be a privilege escalation over REST.
			wantNone: []string{"list_external_resources", "read_external", "write_external", "delete_external", "search_external", "list_external_paths"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := newToken(t, svc, tt.name, tt.scopes...)
			names := toolNames(t, connectWithToken(t, svc, key))

			for _, want := range tt.wantSome {
				if !slices.Contains(names, want) {
					t.Errorf("missing tool %q; got %v", want, names)
				}
			}
			for _, notWant := range tt.wantNone {
				if slices.Contains(names, notWant) {
					t.Errorf("tool %q must not be exposed; got %v", notWant, names)
				}
			}
		})
	}
}

// TestSessionToolVisibility checks the other credential path: capability
// bundles, including the external tools a token can never reach.
func TestSessionToolVisibility(t *testing.T) {
	svc := newTestService(t)

	tests := []struct {
		name      string
		caps      []string
		wantSome  []string
		wantNone  []string
		wantEmpty bool
	}{
		{
			name:     "files read",
			caps:     []string{service.CapFilesRead},
			wantSome: []string{"search_configs", "get_config"},
			wantNone: []string{"set_config", "read_external"},
		},
		{
			name:     "external read",
			caps:     []string{service.CapExternalRead},
			wantSome: []string{"list_external_resources", "read_external", "search_external"},
			wantNone: []string{"write_external", "get_config"},
		},
		{
			name:      "unrelated capability grants nothing",
			caps:      []string{service.CapTokensManage},
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			names := toolNames(t, connect(t, newCapServer(t, svc, tt.caps, nil), nil))

			if tt.wantEmpty {
				if len(names) != 0 {
					t.Fatalf("expected no tools, got %v", names)
				}

				return
			}

			for _, want := range tt.wantSome {
				if !slices.Contains(names, want) {
					t.Errorf("missing tool %q; got %v", want, names)
				}
			}
			for _, notWant := range tt.wantNone {
				if slices.Contains(names, notWant) {
					t.Errorf("tool %q must not be exposed to caps %v", notWant, tt.caps)
				}
			}
		})
	}
}

// TestTokenScopesAreEnforcedPerPath checks that a path-scoped token is
// restricted exactly as it is on /data/*, including the ancestor carve-out
// that lets it navigate down to the subtree it owns.
func TestTokenScopesAreEnforcedPerPath(t *testing.T) {
	svc := newTestService(t)

	seed := func(path, content string) {
		t.Helper()
		if _, err := svc.SetFile(t.Context(), path, &service.File{
			Meta: service.FileMeta{Format: "yaml"},
			Data: []byte(content),
		}, nil, ""); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}
	seed("team-a/app.yaml", "secret: alpha\n")
	seed("team-b/app.yaml", "secret: bravo\n")

	key := newToken(t, svc, "scoped", scope("team-a/**", "read", "write"))
	session := connectWithToken(t, svc, key)

	var in struct {
		Content string `json:"content"`
	}
	callTool(t, session, "get_config", map[string]any{"path": "team-a/app.yaml"}, &in)
	if in.Content != "secret: alpha\n" {
		t.Fatalf("in-scope read returned %q", in.Content)
	}

	if msg := callToolExpectError(t, session, "get_config", map[string]any{"path": "team-b/app.yaml"}); !strings.Contains(msg, "not permitted") {
		t.Errorf("expected a scope error, got %q", msg)
	}
	if msg := callToolExpectError(t, session, "set_config", map[string]any{
		"path": "team-b/app.yaml", "content": "x: 1\n",
	}); !strings.Contains(msg, "not permitted") {
		t.Errorf("expected a scope error on write, got %q", msg)
	}

	// The root listing must still work, otherwise a scoped token has no
	// way to discover the branch it does have access to — but it must
	// only show that branch. A folder name can be the sensitive part.
	var root struct {
		Folders []string `json:"folders"`
	}
	callTool(t, session, "list_folder", map[string]any{}, &root)
	if !slices.Contains(root.Folders, "team-a") {
		t.Errorf("root listing should expose the in-scope branch, got %+v", root.Folders)
	}
	if slices.Contains(root.Folders, "team-b") {
		t.Errorf("root listing leaked out-of-scope folder name, got %+v", root.Folders)
	}

	// Files are filtered by the exact path, not the ancestor rule.
	var teamB struct {
		Files []string `json:"files"`
	}
	callTool(t, session, "list_folder", map[string]any{"path": "team-a"}, &teamB)
	if !slices.Contains(teamB.Files, "app.yaml") {
		t.Errorf("in-scope file should be listed, got %+v", teamB.Files)
	}

	// Search results are filtered too: an out-of-scope path must not even
	// be disclosed as existing.
	var found struct {
		Hits []struct {
			Path string `json:"path"`
		} `json:"hits"`
	}
	callTool(t, session, "search_configs", map[string]any{"query": "secret"}, &found)
	if len(found.Hits) == 0 {
		t.Fatal("expected at least the in-scope hit")
	}
	for _, hit := range found.Hits {
		if strings.HasPrefix(hit.Path, "team-b/") {
			t.Errorf("search leaked out-of-scope path %q", hit.Path)
		}
	}
}

// TestSessionPatternsAreEnforced is the same guarantee for the cookie path,
// where restrictions come from capability patterns instead of token scopes.
func TestSessionPatternsAreEnforced(t *testing.T) {
	svc := newTestService(t)

	for _, path := range []string{"team-a/app.yaml", "team-b/app.yaml"} {
		if _, err := svc.SetFile(t.Context(), path, &service.File{
			Meta: service.FileMeta{Format: "yaml"},
			Data: []byte("secret: x\n"),
		}, nil, ""); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}

	srv := newCapServer(t, svc,
		[]string{service.CapFilesRead},
		map[string][]string{service.CapFilesRead: {"team-a/**"}},
	)
	session := connect(t, srv, nil)

	callTool(t, session, "get_config", map[string]any{"path": "team-a/app.yaml"}, nil)

	if msg := callToolExpectError(t, session, "get_config", map[string]any{"path": "team-b/app.yaml"}); !strings.Contains(msg, "not permitted") {
		t.Errorf("expected a permission error, got %q", msg)
	}

	var root struct {
		Folders []string `json:"folders"`
	}
	callTool(t, session, "list_folder", map[string]any{}, &root)
	if !slices.Contains(root.Folders, "team-a") {
		t.Errorf("root listing should expose the in-scope branch, got %+v", root.Folders)
	}
	if slices.Contains(root.Folders, "team-b") {
		t.Errorf("root listing leaked out-of-scope folder name, got %+v", root.Folders)
	}
}

// TestSetConfigWritesVersionWithAuthor checks that the caller's identity
// reaches the service layer across the hop into the MCP SDK's detached
// handler context — otherwise every MCP write lands in the audit trail
// attributed to "system".
func TestSetConfigWritesVersionWithAuthor(t *testing.T) {
	svc := newTestService(t)
	key := newToken(t, svc, "ci-agent", scope("**", "read", "write"))
	session := connectWithToken(t, svc, key)

	var wrote struct {
		Path    string `json:"path"`
		Version int64  `json:"version"`
	}
	callTool(t, session, "set_config", map[string]any{
		"path":    "team-a/app/config.yaml",
		"content": "port: 8080\n",
	}, &wrote)

	if wrote.Version != 1 {
		t.Fatalf("expected first version to be 1, got %d", wrote.Version)
	}

	versions, err := svc.FileVersionsList(t.Context(), "team-a/app/config.yaml")
	if err != nil {
		t.Fatalf("FileVersionsList: %v", err)
	}
	if len(versions) != 1 || len(versions[0].Status) == 0 {
		t.Fatalf("unexpected version history: %+v", versions)
	}
	if author := versions[0].Status[0].Author; author != "ci-agent" {
		t.Fatalf("expected author %q, got %q", "ci-agent", author)
	}

	// Format is inferred from the extension when the caller omits it —
	// otherwise the config would be stored as "raw" and inheritance and
	// conversion would silently stop working for it.
	var got struct {
		Content string `json:"content"`
		Format  string `json:"format"`
	}
	callTool(t, session, "get_config", map[string]any{"path": "team-a/app/config.yaml"}, &got)
	if got.Format != "yaml" {
		t.Errorf("expected inferred format yaml, got %q", got.Format)
	}
	if got.Content != "port: 8080\n" {
		t.Errorf("unexpected content %q", got.Content)
	}
}

// TestSetConfigPreservesMetadata covers the partial-update contract: an
// agent editing only the body must not silently drop the description or
// the inheritance list that came with the config.
func TestSetConfigPreservesMetadata(t *testing.T) {
	svc := newTestService(t)
	key := newToken(t, svc, "agent", scope("**", "read", "write"))
	session := connectWithToken(t, svc, key)

	if _, err := svc.SetFile(t.Context(), "app/config.json", &service.File{
		Meta: service.FileMeta{
			Format:      "json",
			Description: "primary service config",
			Inherits:    []service.InheritEntry{{Source: "base/defaults.json"}},
			GoTemplate:  true,
		},
		Data: []byte(`{"port":8080}`),
	}, nil, ""); err != nil {
		t.Fatalf("seed SetFile: %v", err)
	}

	callTool(t, session, "set_config", map[string]any{
		"path":    "app/config.json",
		"content": `{"port":9090}`,
	}, nil)

	file, err := svc.File(t.Context(), "app/config.json", 0)
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	if file.Meta.Description != "primary service config" {
		t.Errorf("description was dropped: %q", file.Meta.Description)
	}
	if !file.Meta.GoTemplate {
		t.Error("go_template flag was dropped")
	}
	if len(file.Meta.Inherits) != 1 || file.Meta.Inherits[0].Source != "base/defaults.json" {
		t.Errorf("inherits was dropped: %+v", file.Meta.Inherits)
	}
	if string(file.Data) != `{"port":9090}` {
		t.Errorf("content not updated: %q", file.Data)
	}

	// An explicit empty list is the documented way to clear inheritance.
	callTool(t, session, "set_config", map[string]any{
		"path":     "app/config.json",
		"content":  `{"port":9090}`,
		"inherits": []any{},
	}, nil)

	file, err = svc.File(t.Context(), "app/config.json", 0)
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	if len(file.Meta.Inherits) != 0 {
		t.Errorf("expected inherits to be cleared, got %+v", file.Meta.Inherits)
	}
}

// TestSearchLimitTruncates guards the context-window protection: the walk
// must stop and say so rather than returning the whole tree.
func TestSearchLimitTruncates(t *testing.T) {
	svc := newTestService(t)

	for _, name := range []string{"a", "b", "c", "d", "e"} {
		if _, err := svc.SetFile(t.Context(), "configs/"+name+".yaml", &service.File{
			Meta: service.FileMeta{Format: "yaml"},
			Data: []byte("key: value\n"),
		}, nil, ""); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	key := newToken(t, svc, "agent", scope("**", "read"))
	session := connectWithToken(t, svc, key)

	var res struct {
		Hits []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"hits"`
		Truncated bool `json:"truncated"`
	}
	callTool(t, session, "search_configs", map[string]any{
		"query": "configs",
		"mode":  "name",
		"limit": 2,
	}, &res)

	if len(res.Hits) != 2 {
		t.Fatalf("expected exactly 2 hits, got %d", len(res.Hits))
	}
	if !res.Truncated {
		t.Error("expected truncated to be reported")
	}
}

// TestGetResolvedConfigAppliesInheritance distinguishes the two read
// tools: get_config returns the source, get_resolved_config returns what
// a consuming application actually receives.
func TestGetResolvedConfigAppliesInheritance(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.SetFile(ctx, "base/defaults.json", &service.File{
		Meta: service.FileMeta{Format: "json"},
		Data: []byte(`{"timeout":30,"port":80}`),
	}, nil, ""); err != nil {
		t.Fatalf("seed base: %v", err)
	}
	if _, err := svc.SetFile(ctx, "app/config.json", &service.File{
		Meta: service.FileMeta{
			Format:   "json",
			Inherits: []service.InheritEntry{{Source: "base/defaults.json"}},
		},
		Data: []byte(`{"port":8080}`),
	}, nil, ""); err != nil {
		t.Fatalf("seed app: %v", err)
	}

	key := newToken(t, svc, "agent", scope("**", "read"))
	session := connectWithToken(t, svc, key)

	var source struct {
		Content string `json:"content"`
	}
	callTool(t, session, "get_config", map[string]any{"path": "app/config.json"}, &source)
	if strings.Contains(source.Content, "timeout") {
		t.Errorf("get_config must return the stored source, got %q", source.Content)
	}

	var resolved struct {
		Content string `json:"content"`
		Format  string `json:"format"`
	}
	callTool(t, session, "get_resolved_config", map[string]any{"path": "app/config.json"}, &resolved)

	var merged map[string]any
	if err := json.Unmarshal([]byte(resolved.Content), &merged); err != nil {
		t.Fatalf("resolved content is not JSON (%q): %v", resolved.Content, err)
	}
	if merged["timeout"] != float64(30) {
		t.Errorf("inherited field missing: %+v", merged)
	}
	if merged["port"] != float64(8080) {
		t.Errorf("override lost: %+v", merged)
	}
}

// TestVariantRoundTrip covers the variant plumbing end to end, since
// variants are stored under a synthesized key and are easy to get wrong.
func TestVariantRoundTrip(t *testing.T) {
	svc := newTestService(t)
	key := newToken(t, svc, "agent", scope("**", "read", "write"))
	session := connectWithToken(t, svc, key)

	callTool(t, session, "set_config", map[string]any{
		"path": "app/config.yaml", "content": "port: 80\n",
	}, nil)
	callTool(t, session, "set_config", map[string]any{
		"path": "app/config.yaml", "variant": "prod", "content": "port: 443\n",
	}, nil)

	var variants struct {
		Variants []string `json:"variants"`
	}
	callTool(t, session, "list_variants", map[string]any{"path": "app/config.yaml"}, &variants)
	if !slices.Contains(variants.Variants, "prod") {
		t.Fatalf("variant not registered: %+v", variants.Variants)
	}

	var got struct {
		Content string `json:"content"`
	}
	callTool(t, session, "get_config", map[string]any{"path": "app/config.yaml", "variant": "prod"}, &got)
	if got.Content != "port: 443\n" {
		t.Errorf("variant content mismatch: %q", got.Content)
	}

	callTool(t, session, "get_config", map[string]any{"path": "app/config.yaml"}, &got)
	if got.Content != "port: 80\n" {
		t.Errorf("base config was overwritten by the variant write: %q", got.Content)
	}
}

// TestExpectedVersionConflict verifies the optimistic-concurrency escape
// hatch reaches the model as a readable error instead of a silent
// overwrite of someone else's change.
func TestExpectedVersionConflict(t *testing.T) {
	svc := newTestService(t)
	key := newToken(t, svc, "agent", scope("**", "read", "write"))
	session := connectWithToken(t, svc, key)

	callTool(t, session, "set_config", map[string]any{"path": "app/c.yaml", "content": "a: 1\n"}, nil)
	callTool(t, session, "set_config", map[string]any{"path": "app/c.yaml", "content": "a: 2\n"}, nil)

	msg := callToolExpectError(t, session, "set_config", map[string]any{
		"path": "app/c.yaml", "content": "a: 3\n", "expected_version": 1,
	})
	if !strings.Contains(msg, "expected version") {
		t.Errorf("expected a version-conflict message, got %q", msg)
	}
}

// TestInstructionsAndServerIdentity checks the initialize payload, which
// is the only briefing a model gets about pika's domain model.
func TestInstructionsAndServerIdentity(t *testing.T) {
	svc := newTestService(t)
	key := newToken(t, svc, "agent", scope("**", "read"))
	session := connectWithToken(t, svc, key)

	if got := session.InitializeResult().Instructions; !strings.Contains(got, "get_resolved_config") {
		t.Errorf("instructions should explain the resolved-vs-source distinction, got %q", got)
	}
	if got := session.InitializeResult().ServerInfo; got.Name != "pika-test" || got.Version != "v0.0.0-test" {
		t.Errorf("server identity not propagated: %+v", got)
	}
}

// TestStatelessRejectsGet documents the transport contract: there is no
// standalone SSE stream to attach to, and the endpoint says so properly.
func TestStatelessRejectsGet(t *testing.T) {
	svc := newTestService(t)
	key := newToken(t, svc, "agent", scope("**", "read"))
	srv := newTokenServer(t, svc)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for GET, got %d", res.StatusCode)
	}
}
