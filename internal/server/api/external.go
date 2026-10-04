package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/pika/internal/external"
	"github.com/rakunlabs/pika/internal/service"
)

func (a *api) listExternalPaths(c *ada.Context) error {
	resourceName := c.Request.PathValue("name")
	prefix := c.Request.URL.Query().Get("prefix")

	paths, err := a.svc.ListExternalPaths(c.Request.Context(), resourceName, prefix)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(paths)
}

// searchExternal walks the named resource looking for paths/values
// that match the `q` query string. Query params:
//
//	q     — search term (required, non-empty)
//	mode  — "name" (default) or "all" (also greps values)
//	limit — max hits to return (default 200, hard cap inside service)
//
// Returns a JSON array of {path, type, snippet} objects so the SPA
// can render mixed name/content hits in one list. The handler maps
// query strings rather than a JSON body because search responses are
// safe to cache by an intermediary on the q+mode combination, and a
// GET makes that cacheability explicit (whereas POST always says
// "uncachable" to proxies). The query never carries credentials —
// those live in the resource configuration on the server.
func (a *api) searchExternal(c *ada.Context) error {
	resourceName := c.Request.PathValue("name")
	q := c.Request.URL.Query().Get("q")
	if strings.TrimSpace(q) == "" {
		// Empty query is not an error — just nothing to return.
		// Matches the SPA's behaviour where clearing the search box
		// resets the result list without throwing.
		return c.SetStatus(http.StatusOK).SendJSON([]service.ExternalSearchHit{})
	}
	mode := service.ExternalSearchMode(c.Request.URL.Query().Get("mode"))
	if mode != service.ExternalSearchModeAll {
		mode = service.ExternalSearchModeName
	}
	limit := 0
	if raw := c.Request.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return fmt.Errorf("limit must be a non-negative integer: %w", service.ErrBadRequest)
		}
		limit = n
	}

	hits, err := a.svc.SearchExternal(c.Request.Context(), resourceName, q, mode, limit)
	if err != nil {
		return err
	}
	if hits == nil {
		// Always emit `[]` so SPA destructuring doesn't have to
		// guard against null. Same convention as the rest of the
		// external endpoints.
		hits = []service.ExternalSearchHit{}
	}
	return c.SetStatus(http.StatusOK).SendJSON(hits)
}

// testExternalResource runs a live connectivity check against the named
// external resource. The SPA's External page calls this from its "Test"
// action — the response body always has shape {ok, message, sample} so the
// UI can render the outcome uniformly regardless of backend type.
func (a *api) testExternalResource(c *ada.Context) error {
	resourceName := c.Request.PathValue("name")

	result, err := a.svc.TestExternal(c.Request.Context(), resourceName)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(result)
}

// listExternalResources powers the left pane of the External browser
// page. Returns names, kinds and capability flags for every
// configured resource. Does NOT touch the network.
func (a *api) listExternalResources(c *ada.Context) error {
	resources, err := a.svc.ListExternalResources(c.Request.Context())
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(resources)
}

// externalEntryReq is the shared request body for read/write/delete/
// versions/version endpoints. Path-bearing operations all share this
// shape so the SPA can use one helper. We use POST + JSON body
// (rather than GET + query string) for two reasons: many backend
// paths embed "/" (e.g. Kubernetes "namespace/secret/name", Vault
// "myapp/db") and URL-encoding them through path segments fights the
// router; and POST is uncached by every layer that might sit in
// front, so secret payloads don't end up in proxy logs.
type externalEntryReq struct {
	Path    string         `json:"path"`
	Data    map[string]any `json:"data,omitempty"`
	Version string         `json:"version,omitempty"`
}

// translateNotSupported maps provider-level refusals onto HTTP semantics:
// ErrNotSupported ("this backend can't do that") becomes a 4xx client error,
// while ErrAccessDenied ("the resource's own access settings forbid it")
// becomes 403 so the SPA can tell "impossible" from "not permitted".
// Anything else is passed through to the default error handler.
func translateNotSupported(err error) error {
	if errors.Is(err, external.ErrNotSupported) {
		return errors.Join(err, service.ErrBadRequest)
	}
	if errors.Is(err, external.ErrAccessDenied) {
		return errors.Join(err, service.ErrForbidden)
	}
	return err
}

// externalEntryHandler binds the shared entry request body and maps
// provider refusals onto HTTP statuses, so each route only states the
// service call it makes.
func externalEntryHandler(fn func(c *ada.Context, resourceName string, req *externalEntryReq) error) ada.HandlerFunc {
	return func(c *ada.Context) error {
		var req externalEntryReq
		if err := c.Bind(&req); err != nil {
			return errors.Join(err, service.ErrBadRequest)
		}
		return translateNotSupported(fn(c, c.Request.PathValue("name"), &req))
	}
}

func (a *api) readExternalEntry(c *ada.Context, resourceName string, req *externalEntryReq) error {
	entry, err := a.svc.ReadExternal(c.Request.Context(), resourceName, req.Path)
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(entry)
}

func (a *api) writeExternalEntry(c *ada.Context, resourceName string, req *externalEntryReq) error {
	if err := a.svc.WriteExternal(c.Request.Context(), resourceName, req.Path, req.Data); err != nil {
		return err
	}
	return c.SendNoContent()
}

func (a *api) deleteExternalEntry(c *ada.Context, resourceName string, req *externalEntryReq) error {
	if err := a.svc.DeleteExternal(c.Request.Context(), resourceName, req.Path); err != nil {
		return err
	}
	return c.SendNoContent()
}

func (a *api) listExternalVersions(c *ada.Context, resourceName string, req *externalEntryReq) error {
	versions, err := a.svc.ListExternalVersions(c.Request.Context(), resourceName, req.Path)
	if err != nil {
		return err
	}
	if versions == nil {
		// Always emit [] over null so the SPA can iterate without
		// a null-guard at every call site.
		versions = []external.Version{}
	}
	return c.SetStatus(http.StatusOK).SendJSON(versions)
}

func (a *api) readExternalVersion(c *ada.Context, resourceName string, req *externalEntryReq) error {
	entry, err := a.svc.ReadExternalVersion(c.Request.Context(), resourceName, req.Path, req.Version)
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(entry)
}
