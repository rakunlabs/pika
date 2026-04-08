package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/serve/ftpserve"
	"github.com/rakunlabs/pika/internal/serve/sftpserve"
	"github.com/rakunlabs/pika/internal/serve/tftpserve"
	"github.com/rakunlabs/pika/internal/serve/webdavserve"
	"github.com/rakunlabs/pika/internal/service"
)

// mountEntry holds a prefix and its associated filesystem backend.
type mountEntry struct {
	Prefix   string
	FS       rawfs.RawFS
	Type     string // "local", "s3", "ftp"
	Writable bool
}

// publicServerInfo holds references to the running public HTTP server.
type publicServerInfo struct {
	cancel context.CancelFunc
}

// RawHandler holds the mounted filesystem backends.
// Mounts can be updated at runtime (hot-reload from settings).
type RawHandler struct {
	mu         sync.RWMutex
	mounts     []mountEntry
	ftpServer    *ftpserve.Server
	sftpServer   *sftpserve.Server
	tftpServer   *tftpserve.Server
	webdavServer *webdavserve.Server
	ftpCancel    context.CancelFunc
	sftpCancel   context.CancelFunc
	tftpCancel   context.CancelFunc
	webdavCancel context.CancelFunc
	publicSrv  *publicServerInfo
	appCtx     context.Context
	dispatcher *hook.Dispatcher
}

// NewRawHandler creates a new RawHandler with initial mount entries.
// If dispatcher is non-nil, all mount FS backends are wrapped with hook event emission.
func NewRawHandler(entries []mountEntry, appCtx context.Context, dispatcher *hook.Dispatcher) *RawHandler {
	rh := &RawHandler{appCtx: appCtx, dispatcher: dispatcher}
	rh.mounts = rh.wrapMounts(entries)
	return rh
}

// MountInfo holds serializable info about a mount for the API.
type MountInfo struct {
	Prefix   string `json:"prefix"`
	Type     string `json:"type"`
	Writable bool   `json:"writable"`
}

// MountsInfo returns info about all current mounts.
func (h *RawHandler) MountsInfo() []MountInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]MountInfo, len(h.mounts))
	for i, m := range h.mounts {
		out[i] = MountInfo{
			Prefix:   m.Prefix,
			Type:     m.Type,
			Writable: m.Writable,
		}
	}
	return out
}

// wrapMounts wraps each mount's FS with the hook dispatcher (if set).
func (h *RawHandler) wrapMounts(entries []mountEntry) []mountEntry {
	if h.dispatcher == nil {
		return entries
	}
	wrapped := make([]mountEntry, len(entries))
	for i, m := range entries {
		wrapped[i] = mountEntry{
			Prefix:   m.Prefix,
			FS:       hook.NewHookedFS(m.FS, h.dispatcher, m.Prefix, "http"),
			Type:     m.Type,
			Writable: m.Writable,
		}
	}
	return wrapped
}

// UpdateMounts replaces the current mounts.
func (h *RawHandler) UpdateMounts(entries []mountEntry) {
	wrapped := h.wrapMounts(entries)

	h.mu.Lock()
	h.mounts = wrapped
	h.mu.Unlock()

	for _, m := range entries {
		slog.Info("raw mount updated", "prefix", "/raw/"+m.Prefix, "type", m.Type, "writable", m.Writable)
	}
}

// resolveMount finds the matching mount for the given request path.
func (h *RawHandler) resolveMount(path string) (*mountEntry, string, error) {
	prefix := path
	rest := ""
	if idx := strings.IndexByte(path, '/'); idx >= 0 {
		prefix = path[:idx]
		rest = path[idx+1:]
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for i := range h.mounts {
		if h.mounts[i].Prefix == prefix {
			return &h.mounts[i], rest, nil
		}
	}

	return nil, "", fmt.Errorf("no mount found for prefix %q: %w", prefix, service.ErrNotFound)
}

// serveRaw handles a raw file read request.
func (h *RawHandler) serveRaw(c *ada.Context) error {
	path := c.Request.PathValue("*")

	mount, rest, err := h.resolveMount(path)
	if err != nil {
		return err
	}

	info, err := mount.FS.Stat(rest)
	if err != nil {
		return mapFSError(err)
	}

	if info.IsDir {
		return h.serveDirectory(c, mount.FS, rest)
	}

	return h.serveFile(c, mount.FS, rest)
}

// serveDirectory returns a JSON listing of directory contents.
func (h *RawHandler) serveDirectory(c *ada.Context, fs rawfs.RawFS, path string) error {
	entries, err := fs.ReadDir(path)
	if err != nil {
		return mapFSError(err)
	}

	c.Response.Header().Set("Content-Type", "application/json")
	c.SetStatus(http.StatusOK)
	return json.NewEncoder(c.Response).Encode(entries)
}

// serveFile serves a single file with Range request support.
func (h *RawHandler) serveFile(c *ada.Context, fs rawfs.RawFS, path string) error {
	reader, info, err := fs.Open(path)
	if err != nil {
		return mapFSError(err)
	}
	defer reader.Close()

	// Detect Content-Type from extension first
	ext := filepath.Ext(info.Name)
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		// Fallback: read first 512 bytes for detection
		buf := make([]byte, 512)
		n, _ := reader.Read(buf)
		contentType = http.DetectContentType(buf[:n])
		if _, err := reader.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("seeking file: %w", err)
		}
	}
	c.Response.Header().Set("Content-Type", contentType)

	modTime := info.ModTime
	if modTime.IsZero() {
		modTime = time.Now()
	}

	// ServeContent handles Range, Content-Length, Last-Modified, etc.
	http.ServeContent(c.Response, c.Request, info.Name, modTime, reader)
	return nil
}

// writeFile handles a PUT request to create/overwrite a file.
func (h *RawHandler) writeFile(c *ada.Context) error {
	path := c.Request.PathValue("*")

	mount, rest, err := h.resolveMount(path)
	if err != nil {
		return err
	}

	wfs, ok := mount.FS.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("mount %q is read-only: %w", mount.Prefix, service.ErrBadRequest)
	}

	if err := wfs.Write(rest, c.Request.Body, c.Request.ContentLength); err != nil {
		return mapFSError(err)
	}

	c.SetStatus(http.StatusNoContent)
	return nil
}

// deleteFile handles a DELETE request to remove a file.
func (h *RawHandler) deleteFile(c *ada.Context) error {
	path := c.Request.PathValue("*")

	mount, rest, err := h.resolveMount(path)
	if err != nil {
		return err
	}

	wfs, ok := mount.FS.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("mount %q is read-only: %w", mount.Prefix, service.ErrBadRequest)
	}

	if err := wfs.Delete(rest); err != nil {
		return mapFSError(err)
	}

	c.SetStatus(http.StatusNoContent)
	return nil
}

// mkDir handles a POST request to create a directory.
func (h *RawHandler) mkDir(c *ada.Context) error {
	path := c.Request.PathValue("*")

	mount, rest, err := h.resolveMount(path)
	if err != nil {
		return err
	}

	wfs, ok := mount.FS.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("mount %q is read-only: %w", mount.Prefix, service.ErrBadRequest)
	}

	if err := wfs.MkDir(rest); err != nil {
		return mapFSError(err)
	}

	c.SetStatus(http.StatusNoContent)
	return nil
}

// fileOpRequest is the JSON body for rename/copy/move operations.
type fileOpRequest struct {
	Src string `json:"src"` // "mount/path/to/source"
	Dst string `json:"dst"` // "mount/path/to/destination"
}

// renameFile handles a POST request to rename/move a file within a mount.
func (h *RawHandler) renameFile(c *ada.Context) error {
	var req fileOpRequest
	if err := c.Bind(&req); err != nil {
		return fmt.Errorf("invalid request: %w", service.ErrBadRequest)
	}

	srcMount, srcRest, err := h.resolveMount(req.Src)
	if err != nil {
		return err
	}
	dstMount, dstRest, err := h.resolveMount(req.Dst)
	if err != nil {
		return err
	}

	// Same mount: try native rename
	if srcMount.Prefix == dstMount.Prefix {
		if rfs, ok := srcMount.FS.(rawfs.RenamableRawFS); ok {
			if err := rfs.Rename(srcRest, dstRest); err != nil {
				return mapFSError(err)
			}
			c.SetStatus(http.StatusNoContent)
			return nil
		}
	}

	// Cross-mount or no native rename: copy + delete
	dstWFS, ok := dstMount.FS.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("destination mount %q is read-only: %w", dstMount.Prefix, service.ErrBadRequest)
	}
	srcWFS, ok2 := srcMount.FS.(rawfs.WritableRawFS)
	if !ok2 {
		return fmt.Errorf("source mount %q is read-only (cannot delete after copy): %w", srcMount.Prefix, service.ErrBadRequest)
	}

	if err := rawfs.GenericCopy(srcMount.FS, srcRest, dstWFS, dstRest); err != nil {
		return mapFSError(err)
	}
	if err := srcWFS.Delete(srcRest); err != nil {
		return mapFSError(err)
	}

	c.SetStatus(http.StatusNoContent)
	return nil
}

// copyFile handles a POST request to copy a file.
func (h *RawHandler) copyFile(c *ada.Context) error {
	var req fileOpRequest
	if err := c.Bind(&req); err != nil {
		return fmt.Errorf("invalid request: %w", service.ErrBadRequest)
	}

	srcMount, srcRest, err := h.resolveMount(req.Src)
	if err != nil {
		return err
	}
	dstMount, dstRest, err := h.resolveMount(req.Dst)
	if err != nil {
		return err
	}

	// Same mount: try native copy
	if srcMount.Prefix == dstMount.Prefix {
		if cfs, ok := srcMount.FS.(rawfs.CopyableRawFS); ok {
			if err := cfs.Copy(srcRest, dstRest); err != nil {
				return mapFSError(err)
			}
			c.SetStatus(http.StatusNoContent)
			return nil
		}
	}

	// Fallback: generic copy
	dstWFS, ok := dstMount.FS.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("destination mount %q is read-only: %w", dstMount.Prefix, service.ErrBadRequest)
	}

	if err := rawfs.GenericCopy(srcMount.FS, srcRest, dstWFS, dstRest); err != nil {
		return mapFSError(err)
	}

	c.SetStatus(http.StatusNoContent)
	return nil
}

// moveFile handles a POST request to move a file (copy + delete).
func (h *RawHandler) moveFile(c *ada.Context) error {
	return h.renameFile(c) // move is the same as rename
}

// mapFSError converts filesystem errors to service errors for proper HTTP status codes.
func mapFSError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%v: %w", err, service.ErrNotFound)
	}
	return err
}

// getRaw serves raw files with token authentication.
func (a *api) getRaw(c *ada.Context) error {
	tokenRaw := c.Request.Header.Get("Authorization")
	if len(tokenRaw) > 7 && tokenRaw[:7] == "Bearer " {
		tokenRaw = tokenRaw[7:]
	}

	if tokenRaw == "" {
		return errors.Join(errors.New("missing authentication token"), service.ErrUnauthorized)
	}

	key := "raw/" + c.Request.PathValue("*")

	if err := a.svc.ValidateToken(c.Request.Context(), tokenRaw, key, "read"); err != nil {
		return err
	}

	return a.rawHandler.serveRaw(c)
}

// putRaw handles authenticated file uploads.
func (a *api) putRaw(c *ada.Context) error {
	tokenRaw := c.Request.Header.Get("Authorization")
	if len(tokenRaw) > 7 && tokenRaw[:7] == "Bearer " {
		tokenRaw = tokenRaw[7:]
	}

	if tokenRaw == "" {
		return errors.Join(errors.New("missing authentication token"), service.ErrUnauthorized)
	}

	key := "raw/" + c.Request.PathValue("*")

	if err := a.svc.ValidateToken(c.Request.Context(), tokenRaw, key, "write"); err != nil {
		return err
	}

	return a.rawHandler.writeFile(c)
}

// deleteRaw handles authenticated file deletion.
func (a *api) deleteRaw(c *ada.Context) error {
	tokenRaw := c.Request.Header.Get("Authorization")
	if len(tokenRaw) > 7 && tokenRaw[:7] == "Bearer " {
		tokenRaw = tokenRaw[7:]
	}

	if tokenRaw == "" {
		return errors.Join(errors.New("missing authentication token"), service.ErrUnauthorized)
	}

	key := "raw/" + c.Request.PathValue("*")

	if err := a.svc.ValidateToken(c.Request.Context(), tokenRaw, key, "delete"); err != nil {
		return err
	}

	return a.rawHandler.deleteFile(c)
}

// Dispatcher returns the hook dispatcher (may be nil if no mounts are configured).
func (h *RawHandler) Dispatcher() *hook.Dispatcher {
	if h == nil {
		return nil
	}
	return h.dispatcher
}

// getRawPublic serves raw files without authentication.
func (a *api) getRawPublic(c *ada.Context) error {
	return a.rawHandler.serveRaw(c)
}
