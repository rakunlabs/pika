package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/service"
)

// backupInfoResponse is what GET /api/v1/backup/info returns. The UI
// uses DBVersion to prefill incremental ("since") and point-in-time
// ("until") inputs without having to first export a backup just to read
// the version.
type backupInfoResponse struct {
	DBVersion uint64 `json:"db_version"`
}

// getBackupInfo handles GET /api/v1/backup/info.
//
// Auth: session/token + CapSettingsManage (enforced at the route).
// We previously also required an "admin secret" body param here as
// defence-in-depth, but the capability gate alone is sufficient —
// anyone holding CapSettingsManage can already export the entire
// DB, so a second factor on the same surface bought us nothing.
func (a *api) getBackupInfo(c *ada.Context) error {
	return c.SetStatus(http.StatusOK).SendJSON(backupInfoResponse{
		DBVersion: a.svc.Version(),
	})
}

// exportBackup handles GET /api/v1/backup.
//
// Streams a `.pikabw` container (header + payload). Optional query/header
// parameters:
//
//	since=<uint64>                — incremental backup (entries newer than)
//	until=<uint64>                — point-in-time snapshot (entries up to)
//	encryption_password=<string>  — encrypt payload with ChaCha20
//	X-Encryption-Password         — header equivalent of the query param
//
// Auth: session/token + CapSettingsManage (enforced at the route).
//
// since and until are mutually exclusive. The DB version captured at
// export time is returned in X-Pika-DB-Version (informational; the same
// value is also embedded in the .pikabw header).
func (a *api) exportBackup(c *ada.Context) error {
	encryptionPassword := c.Request.URL.Query().Get("encryption_password")
	if encryptionPassword == "" {
		encryptionPassword = c.Request.Header.Get("X-Encryption-Password")
	}

	since, err := parseUintParam(c, "since")
	if err != nil {
		return err
	}
	until, err := parseUintParam(c, "until")
	if err != nil {
		return err
	}
	if since != 0 && until != 0 {
		return errors.Join(fmt.Errorf("since and until are mutually exclusive"), service.ErrBadRequest)
	}

	timestamp := time.Now().UTC().Format("20060102-150405")
	filename := fmt.Sprintf("pika-backup-%s-v%d.pikabw", timestamp, a.svc.Version())

	c.Response.Header().Set("Content-Type", "application/octet-stream")
	c.Response.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.SetStatus(http.StatusOK)

	if _, err := a.svc.Backup(c.Request.Context(), c.Response, service.BackupOptions{
		Since:              since,
		Until:              until,
		EncryptionPassword: encryptionPassword,
	}); err != nil {
		// Headers may already be flushed by the time Backup fails, so
		// this error is best-effort. The transfer-aware client should
		// notice the truncated stream.
		return err
	}
	return nil
}

// importBackup handles POST /api/v1/backup.
//
// Body MUST be the raw `.pikabw` stream (Content-Type:
// application/octet-stream). Auth fields ride on headers/query so the
// body can be streamed straight through to Service.Restore without
// having to spool it through JSON.
//
//	X-Encryption-Password   — required when the backup header has the
//	                          encrypted flag set (or ?encryption_password=)
//	X-Pika-Wipe             — set to "true"/"1" to wipe the database
//	                          before restoring (or ?wipe=true). The
//	                          backup is validated (magic + decryption
//	                          test if encrypted) BEFORE any wipe runs.
//
// Auth: session/token + CapSettingsManage (enforced at the route).
//
// Default behaviour is upsert (no wipe). The merge/replace modes from
// the SQLite era are gone; the wipe flag is the closest equivalent of
// the old "replace" mode.
func (a *api) importBackup(c *ada.Context) error {
	encryptionPassword := c.Request.URL.Query().Get("encryption_password")
	if encryptionPassword == "" {
		encryptionPassword = c.Request.Header.Get("X-Encryption-Password")
	}

	wipe, err := parseBoolFlag(
		c.Request.URL.Query().Get("wipe"),
		c.Request.Header.Get("X-Pika-Wipe"),
	)
	if err != nil {
		return errors.Join(fmt.Errorf("wipe must be a boolean: %w", err), service.ErrBadRequest)
	}

	if c.Request.Body == nil {
		return errors.Join(fmt.Errorf("request body is empty"), service.ErrBadRequest)
	}
	defer c.Request.Body.Close()

	if err := a.svc.Restore(c.Request.Context(), c.Request.Body, service.RestoreOptions{
		Password: encryptionPassword,
		Wipe:     wipe,
	}); err != nil {
		return fmt.Errorf("restore failed: %w", err)
	}

	msg := "backup restored successfully"
	if wipe {
		msg = "database wiped and backup restored successfully"
	}
	return c.SetStatus(http.StatusOK).SendJSON(response{Message: msg})
}

// parseBoolFlag returns the first non-empty candidate parsed via
// strconv.ParseBool. Empty candidates are skipped — the caller can
// pass both a query param and a header without having to pre-check
// either, and the first non-empty wins. Returns (false, nil) when
// every candidate is empty.
func parseBoolFlag(candidates ...string) (bool, error) {
	for _, c := range candidates {
		if c == "" {
			continue
		}
		return strconv.ParseBool(c)
	}
	return false, nil
}

// parseUintParam returns the named query parameter as a uint64. An
// empty/missing param returns 0 with no error. A non-numeric value is a
// 400.
func parseUintParam(c *ada.Context, name string) (uint64, error) {
	raw := c.Request.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, errors.Join(fmt.Errorf("%s must be a non-negative integer: %w", name, err), service.ErrBadRequest)
	}
	return v, nil
}

// keep io referenced — the import handler streams Request.Body
// straight to the service layer.
var _ io.Reader = (io.Reader)(nil)
