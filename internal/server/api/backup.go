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
// Optionally accepts encryption_password query param or X-Encryption-Password header
// to encrypt the backup payload.
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

	encryptionPassword := c.Request.URL.Query().Get("encryption_password")
	if encryptionPassword == "" {
		encryptionPassword = c.Request.Header.Get("X-Encryption-Password")
	}

	c.Response.Header().Set("Content-Type", "application/json")
	c.Response.Header().Set("Content-Disposition", `attachment; filename="pika-backup.json"`)
	c.SetStatus(http.StatusOK)

	encoder := json.NewEncoder(c.Response)
	encoder.SetIndent("", "  ")

	if encryptionPassword != "" {
		encrypted, err := a.svc.ExportEncrypted(c.Request.Context(), encryptionPassword)
		if err != nil {
			return fmt.Errorf("encrypted export failed: %w", err)
		}
		return encoder.Encode(encrypted)
	}

	backup, err := a.svc.Export(c.Request.Context())
	if err != nil {
		return fmt.Errorf("export failed: %w", err)
	}

	return encoder.Encode(backup)
}

// importBackup handles POST /api/v1/backup
// Expects a JSON body with: { "admin_secret": "...", "mode": "replace"|"merge", "data": { ... } }
// The "data" field can be either a plain BackupData object or an EncryptedBackup envelope.
// If the data contains "encrypted": true, the "encryption_password" field is required to decrypt it.
func (a *api) importBackup(c *ada.Context) error {
	var req struct {
		AdminSecret        string          `json:"admin_secret"`
		Mode               string          `json:"mode"`
		EncryptionPassword string          `json:"encryption_password"`
		Data               json.RawMessage `json:"data"`
	}
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}

	if req.AdminSecret == "" {
		return errors.Join(fmt.Errorf("admin_secret is required"), service.ErrBadRequest)
	}
	if len(req.Data) == 0 {
		return errors.Join(fmt.Errorf("data is required"), service.ErrBadRequest)
	}
	if req.Mode == "" {
		req.Mode = service.ImportModeMerge
	}

	if err := a.svc.VerifyAdminSecret(c.Request.Context(), req.AdminSecret); err != nil {
		return err
	}

	// Detect whether the data is an encrypted envelope or plain backup
	var backupData *service.BackupData

	var probe struct {
		Encrypted bool `json:"encrypted"`
	}
	if err := json.Unmarshal(req.Data, &probe); err != nil {
		return errors.Join(fmt.Errorf("invalid data format"), service.ErrBadRequest)
	}

	if probe.Encrypted {
		// Encrypted backup — decrypt first
		var envelope service.EncryptedBackup
		if err := json.Unmarshal(req.Data, &envelope); err != nil {
			return errors.Join(fmt.Errorf("invalid encrypted backup format"), service.ErrBadRequest)
		}

		decrypted, err := service.DecryptBackupData(&envelope, req.EncryptionPassword)
		if err != nil {
			return err
		}
		backupData = decrypted
	} else {
		// Plain backup — parse directly
		var plain service.BackupData
		if err := json.Unmarshal(req.Data, &plain); err != nil {
			return errors.Join(fmt.Errorf("invalid backup data format"), service.ErrBadRequest)
		}
		backupData = &plain
	}

	if err := a.svc.Import(c.Request.Context(), backupData, req.Mode); err != nil {
		return fmt.Errorf("import failed: %w", err)
	}

	return c.SetStatus(http.StatusOK).SendJSON(response{Message: "backup imported successfully"})
}
