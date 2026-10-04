package api

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/pika/internal/service"
)

// Personal vault file browser. Files are stored unencrypted on the
// configured blob backend (Settings → Personal Vault → File storage);
// metadata lives in the main database. Every endpoint is scoped to the
// calling user.

func (a *api) vaultFilesUser(c *ada.Context) (string, error) {
	ctx := c.Request.Context()
	userID := service.UserIDFromContext(ctx)
	if userID == "" {
		return "", errors.Join(errors.New("no user in context"), service.ErrUnauthorized)
	}
	if a.svc.VaultCoordFor(ctx) == nil {
		return "", errVaultUnavailable
	}
	return userID, nil
}

var errVaultUnavailable = errors.New("vault not configured")

func (a *api) vaultFilesError(c *ada.Context, err error) error {
	switch {
	case errors.Is(err, errVaultUnavailable):
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: err.Error()})
	case errors.Is(err, service.ErrVaultFilesDisabled):
		return c.SetStatus(http.StatusServiceUnavailable).SendJSON(response{Message: err.Error()})
	}
	return err
}

func (a *api) listMyVaultFiles(c *ada.Context) error {
	userID, err := a.vaultFilesUser(c)
	if err != nil {
		return a.vaultFilesError(c, err)
	}
	out, err := a.svc.ListVaultFiles(c.Request.Context(), userID)
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(out)
}

type vaultFolderRequest struct {
	ParentID string `json:"parent_id"`
	Name     string `json:"name"`
}

func (a *api) createMyVaultFolder(c *ada.Context) error {
	userID, err := a.vaultFilesUser(c)
	if err != nil {
		return a.vaultFilesError(c, err)
	}
	var req vaultFolderRequest
	if err := c.Bind(&req); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	f, err := a.svc.CreateVaultFolder(c.Request.Context(), userID, req.ParentID, req.Name)
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusCreated).SendJSON(f)
}

// uploadMyVaultFile streams the raw request body (not multipart) to the
// blob store. Query: name (required), parent_id, path (relative folder
// created on demand, used for folder drag-and-drop), replace=true.
func (a *api) uploadMyVaultFile(c *ada.Context) error {
	userID, err := a.vaultFilesUser(c)
	if err != nil {
		return a.vaultFilesError(c, err)
	}
	r := c.Request
	q := r.URL.Query()
	if r.ContentLength < 0 {
		return c.SetStatus(http.StatusLengthRequired).SendJSON(response{Message: "Content-Length is required"})
	}

	name := q.Get("name")
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	if mt, _, err := mime.ParseMediaType(contentType); err != nil ||
		mt == "application/octet-stream" ||
		mt == "application/x-www-form-urlencoded" ||
		strings.HasPrefix(mt, "multipart/") {
		contentType = ""
	}
	body := bufio.NewReaderSize(r.Body, 4096)
	if contentType == "" {
		contentType = mime.TypeByExtension(strings.ToLower(path.Ext(name)))
	}
	if contentType == "" {
		head, _ := body.Peek(512)
		contentType = http.DetectContentType(head)
	}

	f, err := a.svc.UploadVaultFile(r.Context(), userID, service.VaultUploadRequest{
		ParentID:    q.Get("parent_id"),
		RelDir:      q.Get("path"),
		Name:        name,
		Size:        r.ContentLength,
		ContentType: contentType,
		Replace:     parseBoolQuery(q.Get("replace")),
		Body:        body,
	})
	if err != nil {
		return a.vaultFilesError(c, err)
	}
	return c.SetStatus(http.StatusCreated).SendJSON(f)
}

// inlineSafe reports whether a content type may be rendered inline in
// the browser. Everything else (HTML, SVG, JS, ...) is forced to
// download so user uploads can never execute on the app origin.
func inlineSafe(ct string) bool {
	mt, _, _ := mime.ParseMediaType(ct)
	switch {
	case mt == "image/svg+xml":
		return false
	case strings.HasPrefix(mt, "image/"), strings.HasPrefix(mt, "video/"), strings.HasPrefix(mt, "audio/"):
		return true
	case mt == "application/pdf", mt == "text/plain", mt == "application/json":
		return true
	}
	return false
}

func contentDisposition(kind, name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, kind, ascii, url.PathEscape(name))
}

func (a *api) getMyVaultFileContent(c *ada.Context) error {
	userID, err := a.vaultFilesUser(c)
	if err != nil {
		return a.vaultFilesError(c, err)
	}
	id := c.Request.PathValue("*")
	f, rc, err := a.svc.OpenVaultFile(c.Request.Context(), userID, id)
	if err != nil {
		return a.vaultFilesError(c, err)
	}
	defer rc.Close()

	ct := f.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	inline := c.Request.URL.Query().Get("download") == "" && inlineSafe(ct)
	h := c.Response.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	if mt, _, _ := mime.ParseMediaType(ct); inline && mt == "application/pdf" {
		// Browsers refuse to render PDFs in a sandboxed document; the
		// built-in viewer runs isolated from the page anyway.
		h.Set("Content-Security-Policy", "default-src 'none'; object-src 'self'")
	} else {
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'")
	}
	h.Set("Cache-Control", "private, no-store")
	if inline {
		h.Set("Content-Type", ct)
		h.Set("Content-Disposition", contentDisposition("inline", f.Name))
	} else {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", contentDisposition("attachment", f.Name))
	}

	if rs, ok := rc.(io.ReadSeeker); ok {
		http.ServeContent(c.Response, c.Request, "", f.UpdatedAt, rs)
		return nil
	}
	h.Set("Content-Length", strconv.FormatInt(f.Size, 10))
	c.Response.WriteHeader(http.StatusOK)
	if c.Request.Method == http.MethodHead {
		return nil
	}
	_, err = io.Copy(c.Response, rc)
	return err
}

// putMyVaultFileContent replaces a file's bytes with the raw request
// body. ?if_updated_at=<RFC3339Nano> enables the conflict check.
func (a *api) putMyVaultFileContent(c *ada.Context) error {
	userID, err := a.vaultFilesUser(c)
	if err != nil {
		return a.vaultFilesError(c, err)
	}
	r := c.Request
	if r.ContentLength < 0 {
		return c.SetStatus(http.StatusLengthRequired).SendJSON(response{Message: "Content-Length is required"})
	}
	var expected time.Time
	if v := r.URL.Query().Get("if_updated_at"); v != "" {
		if expected, err = time.Parse(time.RFC3339Nano, v); err != nil {
			return errors.Join(fmt.Errorf("invalid if_updated_at: %w", err), service.ErrBadRequest)
		}
	}
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	if mt, _, err := mime.ParseMediaType(contentType); err != nil ||
		mt == "application/octet-stream" ||
		mt == "application/x-www-form-urlencoded" ||
		strings.HasPrefix(mt, "multipart/") {
		contentType = ""
	}
	f, err := a.svc.WriteVaultFileContent(r.Context(), userID, r.PathValue("*"), service.VaultContentRequest{
		ExpectedUpdatedAt: expected,
		Size:              r.ContentLength,
		ContentType:       contentType,
		Body:              r.Body,
	})
	if err != nil {
		return a.vaultFilesError(c, err)
	}
	return c.SetStatus(http.StatusOK).SendJSON(f)
}

// downloadMyVaultZip streams a folder (or, without an id, every file
// the user owns) as a zip archive. Everything that can fail cleanly is
// checked before the first byte is written; after that the status is
// committed and a truncated archive is the only failure signal.
func (a *api) downloadMyVaultZip(c *ada.Context) error {
	userID, err := a.vaultFilesUser(c)
	if err != nil {
		return a.vaultFilesError(c, err)
	}
	ctx := c.Request.Context()
	id := strings.Trim(c.Request.PathValue("*"), "/")
	z, err := a.svc.PrepareVaultZip(ctx, userID, id)
	if err != nil {
		return a.vaultFilesError(c, err)
	}

	h := c.Response.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", contentDisposition("attachment", z.Name+".zip"))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-store")
	c.Response.WriteHeader(http.StatusOK)

	stats, err := z.WriteTo(ctx, c.Response)
	if err != nil {
		slog.Error("vault zip download failed", "user", userID, "folder", id, "files", stats.Files, "error", err)
		return nil
	}
	if stats.Failed > 0 {
		slog.Warn("vault zip download incomplete", "user", userID, "folder", id, "files", stats.Files, "failed", stats.Failed)
	}
	return nil
}

func (a *api) updateMyVaultFile(c *ada.Context) error {
	userID, err := a.vaultFilesUser(c)
	if err != nil {
		return a.vaultFilesError(c, err)
	}
	var patch service.VaultFilePatch
	if err := c.Bind(&patch); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	f, err := a.svc.UpdateVaultFile(c.Request.Context(), userID, c.Request.PathValue("*"), patch)
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(f)
}

type vaultFileDeleteResponse struct {
	Deleted int `json:"deleted"`
}

func (a *api) deleteMyVaultFile(c *ada.Context) error {
	userID, err := a.vaultFilesUser(c)
	if err != nil {
		return a.vaultFilesError(c, err)
	}
	n, err := a.svc.DeleteVaultFile(c.Request.Context(), userID, c.Request.PathValue("*"))
	if err != nil {
		return err
	}
	return c.SetStatus(http.StatusOK).SendJSON(vaultFileDeleteResponse{Deleted: n})
}

type storageTestResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// testVaultFilesSettings probes an unsaved storage configuration and
// reports the backend error verbatim so the operator can fix it.
func (a *api) testVaultFilesSettings(c *ada.Context) error {
	var cfg service.VaultFilesSettings
	if err := c.Bind(&cfg); err != nil {
		return errors.Join(err, service.ErrBadRequest)
	}
	if err := a.svc.TestVaultFilesSettings(c.Request.Context(), cfg); err != nil {
		return c.SetStatus(http.StatusOK).SendJSON(storageTestResponse{OK: false, Message: err.Error()})
	}
	return c.SetStatus(http.StatusOK).SendJSON(storageTestResponse{OK: true})
}
