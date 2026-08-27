package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/service"
)

// exportFilenameUnsafe matches everything we refuse to put into a
// Content-Disposition filename. Resource names are operator-chosen and
// may legitimately contain spaces, dots or slashes; the download name
// is cosmetic, so we flatten anything outside this set rather than
// rejecting the request.
var exportFilenameUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// exportExternalResource handles GET /api/v1/external/{name}/export.
//
// Streams a zip archive containing every readable key of the named
// resource. Query params:
//
//	prefix=<string> — restrict the walk to a subtree (default: whole resource)
//	limit=<int>     — cap the number of exported keys (default: service default)
//
// Auth: session/token + CapSettingsManage (enforced at the route). This
// is deliberately stricter than the rest of /api/v1/external/*, which
// only needs CapExternalRead — see the route comment in api.go.
//
// Only Consul and Vault resources are exportable today; anything else
// returns 400. That check runs before any header is written, because
// once the zip stream starts the status code is already committed and
// the client would only see a truncated download.
func (a *api) exportExternalResource(c *ada.Context) error {
	ctx := c.Request.Context()
	resourceName := c.Request.PathValue("name")

	if err := a.svc.CheckExternalExportable(ctx, resourceName); err != nil {
		return err
	}

	prefix := c.Request.URL.Query().Get("prefix")

	limit := 0
	if raw := strings.TrimSpace(c.Request.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return errors.Join(fmt.Errorf("limit must be a positive integer"), service.ErrBadRequest)
		}
		limit = parsed
	}

	timestamp := time.Now().UTC().Format("20060102-150405")
	safeName := exportFilenameUnsafe.ReplaceAllString(resourceName, "-")
	if safeName == "" {
		safeName = "resource"
	}
	filename := fmt.Sprintf("pika-external-%s-%s.zip", safeName, timestamp)

	c.Response.Header().Set("Content-Type", "application/zip")
	c.Response.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.SetStatus(http.StatusOK)

	stats, err := a.svc.ExportExternal(ctx, resourceName, c.Response, service.ExternalExportOptions{
		Prefix:     prefix,
		MaxEntries: limit,
	})
	if err != nil {
		// Headers are already flushed, so this error can't reach the
		// client as a status code — the truncated (invalid) zip is the
		// signal. Log it so the operator can see why.
		slog.Error("external export failed",
			"resource", resourceName,
			"prefix", prefix,
			"entries", stats.Entries,
			"error", err,
		)
		return err
	}

	slog.Info("external export completed",
		"resource", resourceName,
		"prefix", prefix,
		"entries", stats.Entries,
		"failed", stats.Failed,
		"truncated", stats.Truncated,
	)
	return nil
}
