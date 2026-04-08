// Package webdavserve implements a WebDAV server that bridges rawfs backends
// to serve files over WebDAV. It reuses the FTP share/user model for access control.
package webdavserve

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/serve/ftpserve"
	"github.com/rakunlabs/pika/internal/service"
)

// Server wraps an HTTP server that handles WebDAV requests.
type Server struct {
	httpSrv *http.Server
	mu      sync.RWMutex
	shares  []ftpserve.Share
	users   []ftpserve.User
	prefix  string
}

// NewServer creates a new WebDAV server with the given config, shares, and users.
func NewServer(cfg *service.WebDAVServeSettings, shares []ftpserve.Share, users []ftpserve.User) (*Server, error) {
	port := cfg.Port
	if port == 0 {
		port = 9119
	}

	host := cfg.Host
	if host == "" {
		host = "0.0.0.0"
	}

	prefix := cfg.Prefix
	if prefix == "" {
		prefix = "/"
	}

	s := &Server{
		shares: shares,
		users:  users,
		prefix: prefix,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleWebDAV)

	s.httpSrv = &http.Server{
		Addr:    net.JoinHostPort(host, strconv.Itoa(port)),
		Handler: mux,
	}

	return s, nil
}

// Start starts the WebDAV server in a goroutine.
func (s *Server) Start(ctx context.Context) {
	go func() {
		slog.Info("starting WebDAV server", "addr", s.httpSrv.Addr)
		if err := s.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("WebDAV server failed", "error", err)
		}
	}()

	go func() {
		<-ctx.Done()
		slog.Info("shutting down WebDAV server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.httpSrv.Shutdown(shutdownCtx) //nolint:errcheck
	}()
}

// Stop gracefully shuts down the WebDAV server.
func (s *Server) Stop() {
	slog.Info("stopping WebDAV server")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.httpSrv.Shutdown(shutdownCtx) //nolint:errcheck
}

// UpdateShares replaces the shares served by the WebDAV server.
func (s *Server) UpdateShares(shares []ftpserve.Share) {
	s.mu.Lock()
	s.shares = shares
	s.mu.Unlock()
}

// UpdateUsers replaces the user list for WebDAV auth.
func (s *Server) UpdateUsers(users []ftpserve.User) {
	s.mu.Lock()
	s.users = users
	s.mu.Unlock()
}

// handleWebDAV processes all WebDAV requests with Basic Auth and share-based routing.
func (s *Server) handleWebDAV(w http.ResponseWriter, r *http.Request) {
	user := s.authenticate(r)
	if user == nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="Pika WebDAV"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	fs := &shareFS{
		server: s,
		user:   user,
	}

	handler := &webdav.Handler{
		Prefix:     s.prefix,
		FileSystem: fs,
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				slog.Debug("WebDAV request", "method", r.Method, "path", r.URL.Path, "error", err)
			}
		},
	}

	handler.ServeHTTP(w, r)
}

// authenticate checks HTTP Basic Auth against the user list.
func (s *Server) authenticate(r *http.Request) *ftpserve.User {
	username, password, ok := r.BasicAuth()
	if !ok {
		return nil
	}

	s.mu.RLock()
	users := s.users
	s.mu.RUnlock()

	for i := range users {
		if constantTimeEquals(users[i].Username, username) && constantTimeEquals(users[i].Password, password) {
			u := users[i]
			return &u
		}
	}
	return nil
}

func constantTimeEquals(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// shareFS implements webdav.FileSystem by bridging to rawfs via shares.
type shareFS struct {
	server *Server
	user   *ftpserve.User
}

var _ webdav.FileSystem = (*shareFS)(nil)

func (fs *shareFS) userShares() []ftpserve.Share {
	fs.server.mu.RLock()
	shares := fs.server.shares
	fs.server.mu.RUnlock()

	if fs.user == nil || len(fs.user.Shares) == 0 {
		return shares
	}

	allowed := make(map[string]bool, len(fs.user.Shares))
	for _, s := range fs.user.Shares {
		allowed[s] = true
	}

	var filtered []ftpserve.Share
	for _, s := range shares {
		if allowed[s.Name] {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func (fs *shareFS) findRootShare() *ftpserve.Share {
	shares := fs.userShares()
	for i := range shares {
		if shares[i].Root {
			return &shares[i]
		}
	}
	return nil
}

func (fs *shareFS) resolveShare(reqPath string) (*ftpserve.Share, string, error) {
	reqPath = path.Clean(reqPath)
	reqPath = strings.TrimPrefix(reqPath, "/")

	if root := fs.findRootShare(); root != nil {
		if reqPath == "" || reqPath == "." {
			return root, "", nil
		}
		return root, reqPath, nil
	}

	if reqPath == "" || reqPath == "." {
		return nil, "", nil // root directory (virtual share listing)
	}

	parts := strings.SplitN(reqPath, "/", 2)
	shareName := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = parts[1]
	}

	shares := fs.userShares()
	for i := range shares {
		if shares[i].Name == shareName {
			return &shares[i], rest, nil
		}
	}

	return nil, "", os.ErrNotExist
}

func (fs *shareFS) isReadOnly(share *ftpserve.Share) bool {
	return (share != nil && share.ReadOnly) || (fs.user != nil && fs.user.ReadOnly)
}

// Mkdir implements webdav.FileSystem.
func (fs *shareFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	share, rest, err := fs.resolveShare(name)
	if err != nil {
		return err
	}
	if share == nil || rest == "" {
		return os.ErrPermission
	}
	if fs.isReadOnly(share) {
		return os.ErrPermission
	}

	wsrc, wfs, err := firstWritableSource(share)
	if err != nil {
		return os.ErrPermission
	}

	return wfs.MkDir(sourceFSPath(wsrc, rest))
}

// OpenFile implements webdav.FileSystem.
func (fs *shareFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	share, rest, err := fs.resolveShare(name)
	if err != nil {
		return nil, err
	}

	// Root directory (share listing)
	if share == nil {
		return &davDirFile{name: "/", fs: fs}, nil
	}

	// Share root
	if rest == "" {
		displayName := share.Name
		if share.Root {
			displayName = "/"
		}
		return &davDirFile{name: displayName, fs: fs, share: share}, nil
	}

	isWrite := flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC) != 0

	// Check if target is a directory
	if src, findErr := findInSources(share, rest); findErr == nil {
		fi, statErr := src.FS.Stat(sourceFSPath(src, rest))
		if statErr == nil && fi.IsDir {
			return &davDirFile{name: path.Base(rest), fs: fs, share: share, rest: rest}, nil
		}
	}

	// Write mode
	if isWrite {
		if fs.isReadOnly(share) {
			return nil, os.ErrPermission
		}

		// Try existing source first
		src, findErr := findInSources(share, rest)
		if findErr == nil {
			wfs, ok := src.FS.(rawfs.WritableRawFS)
			if !ok {
				return nil, os.ErrPermission
			}
			return newDavWriteFile(path.Base(rest), wfs, sourceFSPath(src, rest)), nil
		}

		// New file — write to first writable source
		wsrc, wfs, err := firstWritableSource(share)
		if err != nil {
			return nil, os.ErrPermission
		}
		return newDavWriteFile(path.Base(rest), wfs, sourceFSPath(wsrc, rest)), nil
	}

	// Read mode
	src, err := findInSources(share, rest)
	if err != nil {
		return nil, os.ErrNotExist
	}

	reader, rawFI, err := src.FS.Open(sourceFSPath(src, rest))
	if err != nil {
		return nil, err
	}

	return &davReadFile{
		name:    path.Base(rest),
		reader:  reader,
		size:    rawFI.Size,
		modTime: rawFI.ModTime,
	}, nil
}

// RemoveAll implements webdav.FileSystem.
func (fs *shareFS) RemoveAll(ctx context.Context, name string) error {
	share, rest, err := fs.resolveShare(name)
	if err != nil {
		return err
	}
	if share == nil || rest == "" {
		return os.ErrPermission
	}
	if fs.isReadOnly(share) {
		return os.ErrPermission
	}

	src, err := findInSources(share, rest)
	if err != nil {
		return err
	}

	wfs, ok := src.FS.(rawfs.WritableRawFS)
	if !ok {
		return os.ErrPermission
	}

	return wfs.Delete(sourceFSPath(src, rest))
}

// Rename implements webdav.FileSystem.
func (fs *shareFS) Rename(ctx context.Context, oldName, newName string) error {
	oldShare, oldRest, err := fs.resolveShare(oldName)
	if err != nil {
		return err
	}
	newShare, newRest, err := fs.resolveShare(newName)
	if err != nil {
		return err
	}

	if oldShare == nil || newShare == nil || oldRest == "" || newRest == "" {
		return os.ErrPermission
	}
	if fs.isReadOnly(oldShare) || fs.isReadOnly(newShare) {
		return os.ErrPermission
	}

	src, err := findInSources(oldShare, oldRest)
	if err != nil {
		return err
	}

	rfs, ok := src.FS.(rawfs.RenamableRawFS)
	if !ok {
		return fmt.Errorf("rename not supported on this backend")
	}

	return rfs.Rename(sourceFSPath(src, oldRest), sourceFSPath(src, newRest))
}

// Stat implements webdav.FileSystem.
func (fs *shareFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	share, rest, err := fs.resolveShare(name)
	if err != nil {
		return nil, err
	}
	if share == nil {
		return &virtualFileInfo{name: "/", isDir: true}, nil
	}
	if rest == "" {
		displayName := share.Name
		if share.Root {
			displayName = "/"
		}
		return &virtualFileInfo{name: displayName, isDir: true}, nil
	}

	src, err := findInSources(share, rest)
	if err != nil {
		return nil, os.ErrNotExist
	}

	fi, err := src.FS.Stat(sourceFSPath(src, rest))
	if err != nil {
		return nil, err
	}

	return &virtualFileInfo{
		name:    fi.Name,
		size:    fi.Size,
		isDir:   fi.IsDir,
		modTime: fi.ModTime,
	}, nil
}

// --- Helper functions (mirrored from ftpserve) ---

func sourceFSPath(s *ftpserve.ShareSource, relPath string) string {
	if s.Path == "" {
		return relPath
	}
	if relPath == "" {
		return s.Path
	}
	return s.Path + "/" + relPath
}

func findInSources(share *ftpserve.Share, relPath string) (*ftpserve.ShareSource, error) {
	for i := range share.Sources {
		src := &share.Sources[i]
		_, err := src.FS.Stat(sourceFSPath(src, relPath))
		if err == nil {
			return src, nil
		}
	}
	return nil, os.ErrNotExist
}

func firstWritableSource(share *ftpserve.Share) (*ftpserve.ShareSource, rawfs.WritableRawFS, error) {
	for i := range share.Sources {
		if wfs, ok := share.Sources[i].FS.(rawfs.WritableRawFS); ok {
			return &share.Sources[i], wfs, nil
		}
	}
	return nil, nil, fmt.Errorf("no writable source in share")
}

// --- WebDAV file types ---

// davReadFile implements webdav.File for reading.
type davReadFile struct {
	name    string
	reader  rawfs.ReadSeekCloser
	size    int64
	modTime time.Time
}

var _ webdav.File = (*davReadFile)(nil)

func (f *davReadFile) Read(p []byte) (int, error)              { return f.reader.Read(p) }
func (f *davReadFile) Seek(offset int64, whence int) (int64, error) { return f.reader.Seek(offset, whence) }
func (f *davReadFile) Close() error                             { return f.reader.Close() }
func (f *davReadFile) Write(p []byte) (int, error)              { return 0, os.ErrInvalid }
func (f *davReadFile) Readdir(count int) ([]os.FileInfo, error) { return nil, os.ErrInvalid }

func (f *davReadFile) Stat() (os.FileInfo, error) {
	return &virtualFileInfo{name: f.name, size: f.size, modTime: f.modTime}, nil
}

// davWriteFile implements webdav.File for writing via pipe.
type davWriteFile struct {
	name string
	pw   *io.PipeWriter
	done chan error
}

var _ webdav.File = (*davWriteFile)(nil)

func newDavWriteFile(name string, wfs rawfs.WritableRawFS, fsPath string) *davWriteFile {
	pr, pw := io.Pipe()
	done := make(chan error, 1)

	go func() {
		done <- wfs.Write(fsPath, pr, -1)
	}()

	return &davWriteFile{name: name, pw: pw, done: done}
}

func (f *davWriteFile) Read(p []byte) (int, error)                   { return 0, os.ErrInvalid }
func (f *davWriteFile) Seek(offset int64, whence int) (int64, error) { return 0, os.ErrInvalid }
func (f *davWriteFile) Write(p []byte) (int, error)                  { return f.pw.Write(p) }
func (f *davWriteFile) Readdir(count int) ([]os.FileInfo, error)     { return nil, os.ErrInvalid }

func (f *davWriteFile) Stat() (os.FileInfo, error) {
	return &virtualFileInfo{name: f.name}, nil
}

func (f *davWriteFile) Close() error {
	f.pw.Close()
	return <-f.done
}

// davDirFile implements webdav.File for directories.
type davDirFile struct {
	name  string
	fs    *shareFS
	share *ftpserve.Share
	rest  string
}

var _ webdav.File = (*davDirFile)(nil)

func (f *davDirFile) Read(p []byte) (int, error)                   { return 0, os.ErrInvalid }
func (f *davDirFile) Write(p []byte) (int, error)                  { return 0, os.ErrInvalid }
func (f *davDirFile) Seek(offset int64, whence int) (int64, error) { return 0, os.ErrInvalid }
func (f *davDirFile) Close() error                                 { return nil }

func (f *davDirFile) Stat() (os.FileInfo, error) {
	return &virtualFileInfo{name: f.name, isDir: true}, nil
}

func (f *davDirFile) Readdir(count int) ([]os.FileInfo, error) {
	// Root directory: list shares
	if f.share == nil {
		shares := f.fs.userShares()
		infos := make([]os.FileInfo, len(shares))
		for i, s := range shares {
			infos[i] = &virtualFileInfo{name: s.Name, isDir: true}
		}
		return infos, nil
	}

	// Directory within a share: merge sources
	seen := make(map[string]bool)
	var infos []os.FileInfo

	for i := range f.share.Sources {
		src := &f.share.Sources[i]
		entries, err := src.FS.ReadDir(sourceFSPath(src, f.rest))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if seen[e.Name] {
				continue
			}
			seen[e.Name] = true
			infos = append(infos, &virtualFileInfo{
				name:  e.Name,
				size:  e.Size,
				isDir: e.IsDir,
			})
		}
	}

	return infos, nil
}

// --- virtualFileInfo ---

type virtualFileInfo struct {
	name    string
	size    int64
	isDir   bool
	modTime time.Time
}

func (v *virtualFileInfo) Name() string { return v.name }
func (v *virtualFileInfo) Size() int64  { return v.size }
func (v *virtualFileInfo) Mode() os.FileMode {
	if v.isDir {
		return os.ModeDir | 0o755
	}
	return 0o644
}
func (v *virtualFileInfo) ModTime() time.Time {
	if v.modTime.IsZero() {
		return time.Now()
	}
	return v.modTime
}
func (v *virtualFileInfo) IsDir() bool { return v.isDir }
func (v *virtualFileInfo) Sys() any    { return nil }
