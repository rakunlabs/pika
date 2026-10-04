package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/pika/internal/server/publicendpoint"
	"github.com/rakunlabs/pika/internal/service"
)

// getPublicEndpointStatus returns the live diagnostic view of every
// configured public endpoint (running, disabled, bind-failed).
func (a *api) getPublicEndpointStatus(c *ada.Context) error {
	if a.publicEndpoints == nil {
		return c.SetStatus(http.StatusOK).SendJSON([]publicendpoint.EndpointStatus{})
	}
	return c.SetStatus(http.StatusOK).SendJSON(a.publicEndpoints.Status())
}

// testPublicEndpoint runs a synthetic GET through the endpoint's
// live handler chain — the same chain the public listener serves —
// and returns the status, headers and body. Two reasons we do this
// through the real handler instead of hitting the bound port:
//   - Operators can probe disabled endpoints (manager refuses to
//     bind a port for them but the handler is still constructed).
//   - It works without the admin SPA having direct network access to
//     the public bind, which is common in proxy-fronted deployments.
//
// Request body:
//
//	{ "key": "...", "variant": "...", "version": "...",
//	  "raw": bool, "format": "...",
//	  "headers": {"X-Tenant": "acme", ...} }
//
// The "headers" map is forwarded verbatim onto the synthetic
// request so an operator can exercise both the auth chain and
// any request-check rules they configured. Both Authorization-
// style auth tokens and policy-relevant headers (e.g. X-Tenant
// matched by a rule) go through this same map.
//
// Response:
//
//	{ "status": 200, "headers": {...}, "body": "..." }
func (a *api) testPublicEndpoint(c *ada.Context) error {
	if a.publicEndpoints == nil {
		return errors.Join(errors.New("public endpoints manager not wired"), service.ErrInternal)
	}
	id := c.Request.PathValue("id")
	if id == "" {
		return errors.Join(errors.New("id is required"), service.ErrBadRequest)
	}

	var req struct {
		Key     string            `json:"key"`
		Variant string            `json:"variant,omitempty"`
		Version string            `json:"version,omitempty"`
		Raw     bool              `json:"raw,omitempty"`
		Format  string            `json:"format,omitempty"`
		Headers map[string]string `json:"headers,omitempty"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	// Resolve the endpoint so we know its mode and base path.
	settings, err := a.svc.Settings(c.Request.Context())
	if err != nil {
		return err
	}
	var ep *service.PublicEndpoint
	for i := range settings.PublicEndpoints {
		if settings.PublicEndpoints[i].ID == id {
			ep = &settings.PublicEndpoints[i]
			break
		}
	}
	if ep == nil {
		return errors.Join(fmt.Errorf("public endpoint %q not found", id), service.ErrNotFound)
	}

	handler := a.publicEndpoints.HandlerForID(id)
	if handler == nil {
		// Endpoint exists but isn't running (disabled or bind
		// failure). Build a transient handler for the probe so
		// operators can still validate their template / shim.
		built, berr := publicendpoint.BuildHandlerForProbe(*ep, a.svc, slog.Default())
		if berr != nil {
			return errors.Join(berr, service.ErrBadRequest)
		}
		handler = built
	}

	// Construct the synthetic URL. For consul mode we emit the
	// well-known "{basePath}/v1/kv/<key>" shape; for custom mode the
	// key is appended directly to the base path. The auth chain on
	// the handler will still apply — operators wanting to bypass
	// auth for a one-off probe should toggle the auth mode to
	// "none" first.
	probePath := buildProbePath(*ep, req.Key)
	q := buildProbeQuery(req.Variant, req.Version, req.Raw, req.Format)
	if q != "" {
		probePath += "?" + q
	}

	rec := httptest.NewRecorder()
	probe := httptest.NewRequest(http.MethodGet, probePath, nil)
	// Custom headers from the probe form. Operators use this to
	// supply auth tokens, tenant headers checked by request rules,
	// or anything else the live handler chain inspects.
	for k, v := range req.Headers {
		if k == "" {
			continue
		}
		probe.Header.Set(k, v)
	}
	// Backward-compat shortcut: a single Authorization header may
	// also arrive via X-PublicEndpoint-Auth (used by an earlier
	// UI revision). Honour it only when the operator did not
	// already set Authorization through the new headers map.
	if probe.Header.Get("Authorization") == "" {
		if ah := c.Request.Header.Get("X-PublicEndpoint-Auth"); ah != "" {
			probe.Header.Set("Authorization", ah)
		}
	}
	handler.ServeHTTP(rec, probe)

	headers := make(map[string]string, len(rec.Result().Header))
	for k, v := range rec.Result().Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	return c.SetStatus(http.StatusOK).SendJSON(struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	}{
		Status:  rec.Result().StatusCode,
		Headers: headers,
		Body:    rec.Body.String(),
	})
}

// testPublicEndpointRules dry-runs a draft request_check rule list
// without requiring the operator to save the endpoint. It runs the
// same Go evaluator used by live Endpoints and returns a trace so the
// UI can show matched rules, applied actions and the final request
// shape that would reach the mode shim.
func (a *api) testPublicEndpointRules(c *ada.Context) error {
	var req struct {
		RequestCheck service.RequestCheck `json:"request_check"`
		Method       string               `json:"method,omitempty"`
		Path         string               `json:"path"`
		Headers      map[string]string    `json:"headers,omitempty"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	ep := service.PublicEndpoint{
		Name:         "request-rule-test",
		ListenHost:   "127.0.0.1",
		ListenPort:   1,
		BasePath:     "/",
		Mode:         "static",
		Static:       &service.StaticCompat{},
		Auth:         service.EndpointAuth{Mode: "none"},
		RequestCheck: &req.RequestCheck,
	}
	if err := ep.Validate(); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	result, err := publicendpoint.TestRequestRules(&req.RequestCheck, req.Method, req.Path, req.Headers)
	if err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	return c.SendJSON(result)
}

// buildProbePath assembles the URL the test handler walks. For
// consul-mode endpoints we use the well-known /v1/kv/<key> shape
// so the shim's path parsing matches a real call. For custom- and
// static-mode endpoints we simply join the base path and the key;
// static resolves that path tail exactly like /data/<key>.
func buildProbePath(ep service.PublicEndpoint, key string) string {
	bp := ep.BasePath
	if bp == "" || bp == "/" {
		bp = ""
	}
	key = strings.TrimPrefix(key, "/")
	switch ep.Mode {
	case "consul":
		if key == "" {
			return bp + "/v1/kv/"
		}
		return bp + "/v1/kv/" + key
	default:
		if bp == "" {
			return "/" + key
		}
		return bp + "/" + key
	}
}

func buildProbeQuery(variant, version string, raw bool, format string) string {
	parts := []string{}
	if variant != "" {
		parts = append(parts, "variant="+url.QueryEscape(variant))
	}
	if version != "" {
		parts = append(parts, "version="+url.QueryEscape(version))
	}
	if raw {
		parts = append(parts, "raw")
	}
	if format != "" {
		parts = append(parts, "format="+url.QueryEscape(format))
	}
	return strings.Join(parts, "&")
}
