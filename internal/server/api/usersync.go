package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/ldapclient"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/pika/internal/usersync"
)

// listUserSyncStatus returns the most-recent run summary for every
// configured source, plus the source's enabled state. Used by the UI
// status pill.
func (a *api) listUserSyncStatus(c *ada.Context) error {
	settings, err := a.svc.Settings(c.Request.Context())
	if err != nil {
		return err
	}

	type entry struct {
		ID            string           `json:"id"`
		Name          string           `json:"name"`
		Enabled       bool             `json:"enabled"`
		Last          *usersync.Report `json:"last,omitempty"`
		ScheduleHuman string           `json:"schedule_human,omitempty"`
	}

	var sources []service.SyncSource
	if settings.UserSync != nil {
		sources = settings.UserSync.Sources
	}

	out := make([]entry, 0, len(sources))
	for _, src := range sources {
		e := entry{ID: src.ID, Name: src.Name, Enabled: src.Enabled, ScheduleHuman: humanSchedule(src.Schedule)}
		if a.syncScheduler != nil {
			if r, ok := a.syncScheduler.LastReport(src.ID); ok {
				rcopy := r
				e.Last = &rcopy
			}
		}
		out = append(out, e)
	}

	return c.SetStatus(http.StatusOK).SendJSON(out)
}

// runUserSync triggers an immediate (synchronous) sync for the named source.
// The Report is returned in the response body.
func (a *api) runUserSync(c *ada.Context) error {
	if a.syncScheduler == nil {
		return errors.Join(fmt.Errorf("sync scheduler not initialized"), service.ErrBadRequest)
	}
	id := c.Request.PathValue("*")
	if id == "" {
		return errors.Join(fmt.Errorf("source id is required"), service.ErrBadRequest)
	}

	src, err := a.findSyncSource(c.Request.Context(), id)
	if err != nil {
		return err
	}

	report := a.syncScheduler.RunNow(c.Request.Context(), *src)
	return c.SetStatus(http.StatusOK).SendJSON(report)
}

// testUserSync binds + searches with size limit 5 and returns the raw
// matching entries (DN + attributes) without writing anything to the
// users table. Used by the UI to verify that the filter and attribute
// mapping pull what the admin expects.
func (a *api) testUserSync(c *ada.Context) error {
	id := c.Request.PathValue("*")
	if id == "" {
		return errors.Join(fmt.Errorf("source id is required"), service.ErrBadRequest)
	}

	src, err := a.findSyncSource(c.Request.Context(), id)
	if err != nil {
		return err
	}
	if src.Type != "ldap" || src.LDAP == nil {
		return errors.Join(fmt.Errorf("source %q is not an LDAP source", id), service.ErrBadRequest)
	}
	spec := src.LDAP

	connector := ldapclient.New(ldapclient.Config{
		Address:      spec.Address,
		TLS:          spec.TLS,
		InsecureSkip: spec.InsecureSkip,
		Timeout:      10 * time.Second,
	})
	conn, err := connector.NewConn(c.Request.Context())
	if err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	defer conn.Close()

	if err := conn.Bind(spec.BindDN, spec.BindPassword); err != nil {
		return errors.Join(fmt.Errorf("bind failed: %w", err), service.ErrBadRequest)
	}

	filter := spec.UserFilter
	if filter == "" {
		filter = "(objectClass=*)"
	}

	// Request every mapped attribute so the test response is useful even
	// when the admin is iterating on the mapping itself.
	attrs := mappedAttrs(spec.Attributes)

	entries, err := conn.SearchAll(spec.UserBaseDN, filter, attrs, 5)
	if err != nil {
		return errors.Join(fmt.Errorf("search failed: %w", err), service.ErrBadRequest)
	}

	// Cap returned entries at 5 even if the directory ignored the page hint.
	if len(entries) > 5 {
		entries = entries[:5]
	}

	type sample struct {
		DN         string              `json:"dn"`
		Attributes map[string][]string `json:"attributes"`
	}
	out := struct {
		Total   int      `json:"total_returned"`
		Entries []sample `json:"entries"`
	}{Total: len(entries)}
	for _, e := range entries {
		out.Entries = append(out.Entries, sample{DN: e.DN, Attributes: e.Attributes})
	}

	return c.SetStatus(http.StatusOK).SendJSON(out)
}

// findSyncSource resolves a source ID to its current settings entry.
func (a *api) findSyncSource(ctx context.Context, id string) (*service.SyncSource, error) {
	settings, err := a.svc.Settings(ctx)
	if err != nil {
		return nil, err
	}
	if settings.UserSync == nil {
		return nil, fmt.Errorf("no sync sources configured: %w", service.ErrNotFound)
	}
	for i := range settings.UserSync.Sources {
		if settings.UserSync.Sources[i].ID == id {
			return &settings.UserSync.Sources[i], nil
		}
	}
	return nil, fmt.Errorf("sync source %q not found: %w", id, service.ErrNotFound)
}

// mappedAttrs returns every non-empty attribute name from the mapping.
func mappedAttrs(m service.LDAPAttributeMap) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, a := range []string{m.Username, m.Subject, m.Email, m.DisplayName, m.GivenName, m.Surname, m.Groups} {
		if a == "" {
			continue
		}
		if _, dup := seen[a]; dup {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	return out
}

// humanSchedule renders a schedule struct as a short label for the UI.
func humanSchedule(s service.SyncSchedule) string {
	if s.Mode == "interval" && s.IntervalMinutes > 0 {
		if s.IntervalMinutes == 1 {
			return "every 1 minute"
		}
		return fmt.Sprintf("every %d minutes", s.IntervalMinutes)
	}
	return "manual"
}
