package api

import (
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
	"github.com/rakunlabs/pika/internal/ftpserve"
	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/service"
	"github.com/rakunlabs/pika/internal/sftpserve"
	"github.com/rakunlabs/pika/internal/tftpserve"
)

// mountEntry holds a prefix and its associated filesystem backend.
type mountEntry struct {
	Prefix   string
	FS       rawfs.RawFS
	Type     string // "local", "s3", "ftp"
	Writable bool
}

// rawHandler holds the mounted filesystem backends.
// Mounts can be updated at runtime (hot-reload from settings).
type rawHandler struct {
	mu         sync.RWMutex
	mounts     []mountEntry
	ftpServer  *ftpserve.Server
	sftpServer *sftpserve.Server
	tftpServer *tftpserve.Server
}

// NewRawHandler creates a new rawHandler with initial mount entries.
func NewRawHandler(entries []mountEntry) *rawHandler {
	return &rawHandler{mounts: entries}
}

// MountInfo holds serializable info about a mount for the API.
type MountInfo struct {
	Prefix   string `json:"prefix"`
	Type     string `json:"type"`
	Writable bool   `json:"writable"`
}

// MountsInfo returns info about all current mounts.
func (h *rawHandler) MountsInfo() []MountInfo {
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

// UpdateMounts replaces the current mounts.
func (h *rawHandler) UpdateMounts(entries []mountEntry) {
	h.mu.Lock()
	h.mounts = entries
	h.mu.Unlock()

	for _, m := range entries {
		slog.Info("raw mount updated", "prefix", "/raw/"+m.Prefix, "type", m.Type, "writable", m.Writable)
	}
}

// resolveMount finds the matching mount for the given request path.
func (h *rawHandler) resolveMount(path string) (*mountEntry, string, error) {
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
func (h *rawHandler) serveRaw(c *ada.Context) error {
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
func (h *rawHandler) serveDirectory(c *ada.Context, fs rawfs.RawFS, path string) error {
	entries, err := fs.ReadDir(path)
	if err != nil {
		return mapFSError(err)
	}

	c.Response.Header().Set("Content-Type", "application/json")
	c.SetStatus(http.StatusOK)
	return json.NewEncoder(c.Response).Encode(entries)
}

// serveFile serves a single file with Range request support.
func (h *rawHandler) serveFile(c *ada.Context, fs rawfs.RawFS, path string) error {
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
func (h *rawHandler) writeFile(c *ada.Context) error {
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
func (h *rawHandler) deleteFile(c *ada.Context) error {
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
func (h *rawHandler) mkDir(c *ada.Context) error {
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
func (h *rawHandler) renameFile(c *ada.Context) error {
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
func (h *rawHandler) copyFile(c *ada.Context) error {
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
func (h *rawHandler) moveFile(c *ada.Context) error {
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

// getRawPublic serves raw files without authentication.
func (a *api) getRawPublic(c *ada.Context) error {
	return a.rawHandler.serveRaw(c)
}
