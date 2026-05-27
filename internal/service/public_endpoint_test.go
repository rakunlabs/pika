package service

import (
	"errors"
	"strings"
	"testing"
)

func TestPublicEndpoint_Validate_OK_Static(t *testing.T) {
	ep := PublicEndpoint{
		Name:       "ok",
		Enabled:    true,
		ListenHost: "127.0.0.1",
		ListenPort: 9090,
		BasePath:   "/data",
		Mode:       "static",
		Static:     &StaticCompat{},
		Auth:       EndpointAuth{Mode: "none"},
	}
	if err := ep.Validate(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestPublicEndpoint_Validate_Static_RequiresBlock(t *testing.T) {
	ep := PublicEndpoint{
		Name:       "ok",
		Enabled:    true,
		ListenHost: "127.0.0.1",
		ListenPort: 9090,
		BasePath:   "/health",
		Mode:       "static",
		// Missing Static block.
		Auth: EndpointAuth{Mode: "none"},
	}
	err := ep.Validate()
	if err == nil || !strings.Contains(err.Error(), "static config block required") {
		t.Fatalf("expected missing-block error, got %v", err)
	}
}

func TestPublicEndpoint_Validate_OK_Consul(t *testing.T) {
	ep := PublicEndpoint{
		Name:       "ok",
		Enabled:    true,
		ListenHost: "127.0.0.1",
		ListenPort: 9090,
		BasePath:   "/consul",
		Mode:       "consul",
		Consul:     &ConsulCompat{},
		Auth:       EndpointAuth{Mode: "none"},
	}
	if err := ep.Validate(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestPublicEndpoint_Validate_OK_External(t *testing.T) {
	ep := PublicEndpoint{
		Name:       "ok",
		Enabled:    true,
		ListenHost: "127.0.0.1",
		ListenPort: 9090,
		BasePath:   "/vault",
		Mode:       "external",
		External:   &ExternalCompat{Resource: "prod-vault"},
		Auth:       EndpointAuth{Mode: "none"},
	}
	if err := ep.Validate(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestPublicEndpoint_Validate_OK_Custom(t *testing.T) {
	ep := PublicEndpoint{
		Name:       "ok",
		Enabled:    true,
		ListenHost: "0.0.0.0",
		ListenPort: 9091,
		BasePath:   "/",
		Mode:       "custom",
		Custom: &CustomCompat{
			BodyTemplate: `{"key":"{{ .Key }}"}`,
			ContentType:  "application/json",
		},
		Auth: EndpointAuth{Mode: "static_token", StaticTokens: []string{"abc"}},
	}
	if err := ep.Validate(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestPublicEndpoint_Validate_Errors(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*PublicEndpoint)
		errLike string
	}{
		{"empty name", func(ep *PublicEndpoint) { ep.Name = "  " }, "name is required"},
		{"bad port low", func(ep *PublicEndpoint) { ep.ListenPort = 0 }, "out of range"},
		{"bad port high", func(ep *PublicEndpoint) { ep.ListenPort = 70000 }, "out of range"},
		{"bad host", func(ep *PublicEndpoint) { ep.ListenHost = "not.an.ip.address.really" }, "invalid listen_host"},
		{"trailing slash", func(ep *PublicEndpoint) { ep.BasePath = "/foo/" }, "must not end with /"},
		{"missing leading slash", func(ep *PublicEndpoint) { ep.BasePath = "foo" }, "must start with /"},
		{"unknown mode", func(ep *PublicEndpoint) { ep.Mode = "etcd" }, "mode must be one of"},
		{"consul mode no block", func(ep *PublicEndpoint) {
			ep.Mode = "consul"
			ep.Consul = nil
			ep.Custom = nil
		}, "consul config block required"},
		{"custom mode no block", func(ep *PublicEndpoint) {
			ep.Mode = "custom"
			ep.Consul = nil
			ep.Custom = nil
		}, "custom config block required"},
		{"external mode no block", func(ep *PublicEndpoint) {
			ep.Mode = "external"
			ep.Consul = nil
			ep.External = nil
		}, "external config block required"},
		{"external mode no resource", func(ep *PublicEndpoint) {
			ep.Mode = "external"
			ep.Consul = nil
			ep.External = &ExternalCompat{}
		}, "external.resource is required"},
		{"custom empty template", func(ep *PublicEndpoint) {
			ep.Mode = "custom"
			ep.Custom = &CustomCompat{BodyTemplate: "   "}
		}, "body_template is required"},
		{"custom bad template", func(ep *PublicEndpoint) {
			ep.Mode = "custom"
			ep.Custom = &CustomCompat{BodyTemplate: "{{ .Unclosed "}
		}, "invalid body_template"},
		{"custom bad status", func(ep *PublicEndpoint) {
			ep.Mode = "custom"
			ep.Custom = &CustomCompat{BodyTemplate: "x", StatusOnMissing: 99}
		}, "not a valid HTTP status"},
		{"auth unknown", func(ep *PublicEndpoint) {
			ep.Auth = EndpointAuth{Mode: "weird"}
		}, "auth.mode must be one of"},
		{"static token empty list", func(ep *PublicEndpoint) {
			ep.Auth = EndpointAuth{Mode: "static_token"}
		}, "static_tokens must contain at least one token"},
		{"static token blank", func(ep *PublicEndpoint) {
			ep.Auth = EndpointAuth{Mode: "static_token", StaticTokens: []string{"  "}}
		}, "is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ep := PublicEndpoint{
				Name:       "ok",
				Enabled:    true,
				ListenHost: "127.0.0.1",
				ListenPort: 9090,
				BasePath:   "/consul",
				Mode:       "consul",
				Consul:     &ConsulCompat{},
				Auth:       EndpointAuth{Mode: "none"},
			}
			tc.mutate(&ep)
			err := ep.Validate()
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, ErrBadRequest) {
				t.Errorf("expected ErrBadRequest wrap, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.errLike) {
				t.Errorf("error %q does not contain %q", err, tc.errLike)
			}
		})
	}
}

func TestValidatePublicEndpoints_DuplicateBind(t *testing.T) {
	eps := []PublicEndpoint{
		{
			ID: "a", Name: "a", Enabled: true,
			ListenHost: "127.0.0.1", ListenPort: 9090, BasePath: "/a",
			Mode: "consul", Consul: &ConsulCompat{}, Auth: EndpointAuth{Mode: "none"},
		},
		{
			ID: "b", Name: "b", Enabled: true,
			ListenHost: "127.0.0.1", ListenPort: 9090, BasePath: "/b",
			Mode: "consul", Consul: &ConsulCompat{}, Auth: EndpointAuth{Mode: "none"},
		},
	}
	err := ValidatePublicEndpoints(eps)
	if err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("expected duplicate bind error, got %v", err)
	}
	if !errors.Is(err, ErrBadRequest) {
		t.Errorf("expected ErrBadRequest wrap, got %v", err)
	}
}

func TestPublicEndpoint_Validate_RequestCheck(t *testing.T) {
	base := func() PublicEndpoint {
		return PublicEndpoint{
			Name: "ok", Enabled: true,
			ListenHost: "127.0.0.1", ListenPort: 9090, BasePath: "/c",
			Mode: "consul", Consul: &ConsulCompat{},
			Auth: EndpointAuth{Mode: "none"},
		}
	}

	// OK: a minimal allow rule.
	ep := base()
	ep.RequestCheck = &RequestCheck{
		Rules: []RequestRule{
			{Enabled: true, Then: RequestAction{Type: "allow"}},
		},
	}
	if err := ep.Validate(); err != nil {
		t.Fatalf("expected nil for allow rule, got %v", err)
	}

	// Bad: unknown then.type
	ep = base()
	ep.RequestCheck = &RequestCheck{
		Rules: []RequestRule{
			{Enabled: true, Then: RequestAction{Type: "yolo"}},
		},
	}
	if err := ep.Validate(); err == nil || !strings.Contains(err.Error(), "unknown then.type") {
		t.Errorf("expected unknown then.type error, got %v", err)
	}

	// Bad: set_header without name
	ep = base()
	ep.RequestCheck = &RequestCheck{
		Rules: []RequestRule{
			{Enabled: true, Then: RequestAction{Type: "set_header", Value: "v"}},
		},
	}
	if err := ep.Validate(); err == nil || !strings.Contains(err.Error(), "requires a header name") {
		t.Errorf("expected missing name error, got %v", err)
	}

	// Bad: set_path without leading /
	ep = base()
	ep.RequestCheck = &RequestCheck{
		Rules: []RequestRule{
			{Enabled: true, Then: RequestAction{Type: "set_path", Value: "foo"}},
		},
	}
	if err := ep.Validate(); err == nil || !strings.Contains(err.Error(), "must start with /") {
		t.Errorf("expected path leading-slash error, got %v", err)
	}

	// OK: regex path replacement.
	ep = base()
	ep.RequestCheck = &RequestCheck{
		Rules: []RequestRule{
			{
				Enabled: true,
				Then: RequestAction{
					Type:    "replace_path",
					Pattern: "^/legacy/(.*)$",
					Value:   "/$1",
				},
			},
		},
	}
	if err := ep.Validate(); err != nil {
		t.Errorf("expected nil for replace_path rule, got %v", err)
	}

	// Bad: invalid regex path replacement.
	ep = base()
	ep.RequestCheck = &RequestCheck{
		Rules: []RequestRule{
			{
				Enabled: true,
				Then: RequestAction{
					Type:    "replace_path",
					Pattern: "(",
					Value:   "/$1",
				},
			},
		},
	}
	if err := ep.Validate(); err == nil || !strings.Contains(err.Error(), "invalid regex") {
		t.Errorf("expected invalid regex error, got %v", err)
	}

	// Bad: block status out of range
	ep = base()
	ep.RequestCheck = &RequestCheck{
		Rules: []RequestRule{
			{Enabled: true, Then: RequestAction{Type: "block", Status: 99}},
		},
	}
	if err := ep.Validate(); err == nil || !strings.Contains(err.Error(), "not a valid HTTP status") {
		t.Errorf("expected bad-status error, got %v", err)
	}

	// Bad: header_equals without name
	ep = base()
	ep.RequestCheck = &RequestCheck{
		Rules: []RequestRule{
			{
				Enabled: true,
				When:    RequestMatch{HeaderEquals: &HeaderMatch{Value: "v"}},
				Then:    RequestAction{Type: "allow"},
			},
		},
	}
	if err := ep.Validate(); err == nil || !strings.Contains(err.Error(), "when.header_equals.name is required") {
		t.Errorf("expected when.header_equals.name error, got %v", err)
	}
}

func TestValidatePublicEndpoints_DuplicateID(t *testing.T) {
	eps := []PublicEndpoint{
		{
			ID: "same", Name: "a", Enabled: true,
			ListenHost: "127.0.0.1", ListenPort: 9090, BasePath: "/a",
			Mode: "consul", Consul: &ConsulCompat{}, Auth: EndpointAuth{Mode: "none"},
		},
		{
			ID: "same", Name: "b", Enabled: true,
			ListenHost: "127.0.0.1", ListenPort: 9091, BasePath: "/b",
			Mode: "consul", Consul: &ConsulCompat{}, Auth: EndpointAuth{Mode: "none"},
		},
	}
	err := ValidatePublicEndpoints(eps)
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("expected duplicate id error, got %v", err)
	}
}
