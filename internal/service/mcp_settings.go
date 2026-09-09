package service

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

const DefaultMCPEndpoint = "/api/v1/mcp"

// MCPSettings controls the main listener's MCP route. Anonymous callers use
// explicit config scopes; authentication at a reverse proxy is deployment-owned.
type MCPSettings struct {
	Endpoint     string       `json:"endpoint"`
	AuthDisabled bool         `json:"auth_disabled"`
	Scopes       []TokenScope `json:"scopes"`
}

func EffectiveMCPSettings(s *MCPSettings) MCPSettings {
	if s == nil {
		return MCPSettings{Endpoint: DefaultMCPEndpoint}
	}
	out := *s
	if out.Endpoint == "" {
		out.Endpoint = DefaultMCPEndpoint
	}
	return out
}

var mcpEndpointPath = regexp.MustCompile(`^/[a-zA-Z0-9_/-]+$`)

func (s MCPSettings) Validate() error {
	if err := validateExternalTokenScopes(s.Scopes); err != nil {
		return err
	}
	ep := EffectiveMCPSettings(&s).Endpoint
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
