package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/service"
)

// exportBackup handles GET /api/v1/backup
// Requires admin_secret as a query parameter or X-Admin-Secret header.
func (a *api) exportBackup(c *ada.Context) error {
	adminSecret := c.Request.URL.Query().Get("admin_secret")
	if adminSecret == "" {
		adminSecret = c.Request.Header.Get("X-Admin-Secret")
	}
	if adminSecret == "" {
		return errors.Join(fmt.Errorf("admin_secret is required"), service.ErrBadRequest)
	}

	if err := a.svc.VerifyAdminSecret(c.Request.Context(), adminSecret); err != nil {
		return err
	}

	backup, err := a.svc.Export(c.Request.Context())
	if err != nil {
		return fmt.Errorf("export failed: %w", err)
	}

	c.Response.Header().Set("Content-Type", "application/json")
	c.Response.Header().Set("Content-Disposition", `attachment; filename="pika-backup.json"`)
	c.SetStatus(http.StatusOK)

	encoder := json.NewEncoder(c.Response)
	encoder.SetIndent("", "  ")

	return encoder.Encode(backup)
}

// importBackup handles POST /api/v1/backup
// Expects a JSON body with: { "admin_secret": "...", "mode": "replace"|"merge", "data": { ... } }
func (a *api) importBackup(c *ada.Context) error {
	var req struct {
		AdminSecret string              `json:"admin_secret"`
		Mode        string              `json:"mode"`
		Data        *service.BackupData `json:"data"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if req.AdminSecret == "" {
		return errors.Join(fmt.Errorf("admin_secret is required"), service.ErrBadRequest)
	}
	if req.Data == nil {
		return errors.Join(fmt.Errorf("data is required"), service.ErrBadRequest)
	}
	if req.Mode == "" {
		req.Mode = service.ImportModeMerge
	}

	if err := a.svc.VerifyAdminSecret(c.Request.Context(), req.AdminSecret); err != nil {
		return err
	}

	if err := a.svc.Import(c.Request.Context(), req.Data, req.Mode); err != nil {
		return fmt.Errorf("import failed: %w", err)
	}

	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "backup imported successfully"})
}
