package api

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/felixge/httpsnoop"
	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/identity"
	mrequestid "github.com/rakunlabs/ada/middleware/requestid"
	"github.com/rakunlabs/query"

	"github.com/rakunlabs/pika/internal/server/authx"
	"github.com/rakunlabs/pika/internal/service"
)

// auditMiddleware records every state-changing request on the protected
// API (method != GET/HEAD/OPTIONS) with its outcome. Request and response
// bodies are never recorded, so secrets don't end up in the audit log.
//
// It must run after authentication + capability resolution so the actor
// is known.
func auditMiddleware(svc *service.Service, basePath string, trustedProxies []*net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}

			m := httpsnoop.CaptureMetrics(next, w, r)

			actor, actorType := auditActor(r)
			path := strings.TrimPrefix(r.URL.Path, basePath)
			svc.Audit(service.AuditEntry{
				Action:    r.Method + " " + auditRoute(r, basePath),
				Actor:     actor,
				ActorType: actorType,
				Target:    path,
				Status:    m.Code,
				IP:        authx.ClientIP(r, trustedProxies),
				RequestID: r.Header.Get(mrequestid.HeaderXRequestID),
			})
		})
	}
}

// auditRoute returns the matched route pattern without the method and
// base path (e.g. "/api/v1/file/*"), falling back to the raw path.
func auditRoute(r *http.Request, basePath string) string {
	p := r.Pattern
	if i := strings.IndexByte(p, ' '); i >= 0 {
		p = p[i+1:]
	}
	if p == "" {
		p = r.URL.Path
	}
	return strings.TrimPrefix(p, basePath)
}

func auditActor(r *http.Request) (string, string) {
	id := identity.FromContext(r.Context())
	if id != nil && id.Provider == service.TokenProvider {
		return "token:" + id.Name, "token"
	}
	if u := service.UserFromContext(r.Context()); u != "" {
		return u, "user"
	}
	return "", "anonymous"
}

// listAudit returns audit entries, newest first unless _sort is given.
//
//	_limit=50 _offset=0 _sort=-time
//	actor=alice  action=POST%20/api/v1/settings
//	time[gte]=2026-01-01T00:00:00Z  time[lt]=...
func (a *api) listAudit(c *ada.Context) error {
	raw := c.Request.URL.RawQuery
	q, err := query.Parse(raw, query.WithDefaultLimit(50))
	if err != nil {
		return errors.Join(fmt.Errorf("invalid query parameters: %w", err), service.ErrBadRequest)
	}
	entries, total, err := a.svc.ListAudit(c.Request.Context(), q)
	if err != nil {
		return err
	}

	return c.SetStatus(http.StatusOK).SendJSON(struct {
		Entries []service.AuditEntry `json:"entries"`
		Total   int64                `json:"total"`
	}{Entries: entries, Total: total})
}

// getAuditRetention reports the retention in effect and whether it comes
// from settings or the config file.
func (a *api) getAuditRetention(c *ada.Context) error {
	return c.SetStatus(http.StatusOK).SendJSON(a.svc.AuditRetention(c.Request.Context()))
}
