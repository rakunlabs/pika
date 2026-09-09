package service

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

const DefaultMCPEndpoint = "/api/v1/mcp"

// MCPSettings controls the main listener's MCP routes. A nil Endpoints list
// reads the legacy single-endpoint fields; an explicit empty list disables MCP.
type MCPSettings struct {
	Endpoints    *[]MCPEndpoint `json:"endpoints,omitempty"`
	Endpoint     string         `json:"endpoint,omitempty"`
	AuthDisabled bool           `json:"auth_disabled,omitempty"`
	Scopes       []TokenScope   `json:"scopes,omitempty"`
}

type MCPEndpoint struct {
	Name         string       `json:"name,omitempty"`
	Endpoint     string       `json:"endpoint"`
	Disabled     bool         `json:"disabled,omitempty"`
	AuthDisabled bool         `json:"auth_disabled"`
	Scopes       []TokenScope `json:"scopes"`
}

func EffectiveMCPEndpoints(s *MCPSettings) []MCPEndpoint {
	if s != nil && s.Endpoints != nil {
		return *s.Endpoints
	}
	legacy := EffectiveMCPSettings(s)
	return []MCPEndpoint{{Endpoint: legacy.Endpoint, AuthDisabled: legacy.AuthDisabled, Scopes: legacy.Scopes}}
}

func EffectiveMCPSettings(s *MCPSettings) MCPSettings {
	if s == nil {
		return MCPSettings{Endpoint: DefaultMCPEndpoint}
	}
	out := *s
	if out.Endpoints != nil {
		// List settings are authoritative; never resurrect the legacy route.
		out.Endpoint, out.AuthDisabled, out.Scopes = "", false, nil
		return out
	}
	if out.Endpoint == "" {
		out.Endpoint = DefaultMCPEndpoint
	}
	return out
}

var mcpEndpointPath = regexp.MustCompile(`^/[a-zA-Z0-9_/-]+$`)

func (s MCPSettings) Validate() error {
	seen := make(map[string]bool)
	for _, ep := range EffectiveMCPEndpoints(&s) {
		if err := ep.Validate(); err != nil {
			return fmt.Errorf("MCP endpoint %q: %w", ep.Endpoint, err)
		}
		if seen[ep.Endpoint] {
			return fmt.Errorf("duplicate MCP endpoint %q: %w", ep.Endpoint, ErrBadRequest)
		}
		seen[ep.Endpoint] = true
	}
	return nil
}

func (s MCPEndpoint) Validate() error {
	if err := validateExternalTokenScopes(s.Scopes); err != nil {
		return err
	}
	ep := s.Endpoint
	if ep != DefaultMCPEndpoint {
		if !mcpEndpointPath.MatchString(ep) || ep == "/" || path.Clean(ep) != ep {
			return fmt.Errorf("mcp: endpoint must be a clean absolute path, e.g. /mcp: %w", ErrBadRequest)
		}
		first := strings.Split(strings.TrimPrefix(ep, "/"), "/")[0]
		switch first {
		case "api", "data", "login", "logout", "healthz", "assets", "static", "favicon", "settings":
			return fmt.Errorf("mcp: endpoint uses reserved prefix /%s: %w", first, ErrBadRequest)
		}
	}
	if s.AuthDisabled && len(s.Scopes) == 0 {
		return fmt.Errorf("mcp: add at least one scope for access without Pika authentication: %w", ErrBadRequest)
	}
	for _, sc := range s.Scopes {
		if strings.TrimSpace(sc.Path) == "" || strings.HasPrefix(sc.Path, "/") || strings.Contains(sc.Path, "\\") || !doublestar.ValidatePattern(sc.Path) {
			return fmt.Errorf("mcp: invalid scope path %q: %w", sc.Path, ErrBadRequest)
		}
		for _, part := range strings.Split(sc.Path, "/") {
			if part == ".." || part == "." || part == "" {
				return fmt.Errorf("mcp: scope paths must use config-relative globs: %w", ErrBadRequest)
			}
		}
		if len(sc.Operations) == 0 {
			return fmt.Errorf("mcp: select an operation for scope %q: %w", sc.Path, ErrBadRequest)
		}
		for _, op := range sc.Operations {
			if op != "read" && op != "write" && op != "delete" {
				return fmt.Errorf("mcp: invalid scope operation %q: %w", op, ErrBadRequest)
			}
		}
	}
	return nil
}
