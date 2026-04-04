package sftpserve

import (
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"github.com/rakunlabs/pika/internal/ftpserve"
	"github.com/rakunlabs/pika/internal/rawfs"
)

// newHandler creates SFTP handlers for the given user.
func (s *Server) newHandler(username string) sftp.Handlers {
	h := &sftpHandler{server: s, username: username}
	return sftp.Handlers{
		FileGet:  h,
		FilePut:  h,
		FileCmd:  h,
		FileList: h,
	}
}

type sftpHandler struct {
	server   *Server
	username string
}

// userShares returns the shares visible to this user.
func (h *sftpHandler) userShares() []ftpserve.Share {
	h.server.mu.RLock()
	defer h.server.mu.RUnlock()

	// Find user
	var user *ftpserve.User
	for i := range h.server.users {
		if h.server.users[i].Username == h.username {
			user = &h.server.users[i]
			break
		}
	}

	shares := h.server.shares

	if user == nil || len(user.Shares) == 0 {
		return shares // no restrictions
	}

	allowed := make(map[string]bool, len(user.Shares))
	for _, s := range user.Shares {
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

func (h *sftpHandler) isReadOnly() bool {
	h.server.mu.RLock()
	defer h.server.mu.RUnlock()

	for i := range h.server.users {
		if h.server.users[i].Username == h.username {
			return h.server.users[i].ReadOnly
		}
	}
	return false
}

// resolveShare parses an SFTP path and returns the share and relative path.
func (h *sftpHandler) resolveShare(p string) (*ftpserve.Share, string, error) {
	p = path.Clean(p)
	p = strings.TrimPrefix(p, "/")
	if p == "" || p == "." {
		return nil, "", nil // root
	}

	parts := strings.SplitN(p, "/", 2)
	shareName := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = parts[1]
	}

	shares := h.userShares()
	for i := range shares {
		if shares[i].Name == shareName {
			return &shares[i], rest, nil
		}
	}

	return nil, "", os.ErrNotExist
}

func sourceFSPath(src *ftpserve.ShareSource, relPath string) string {
	if src.Path == "" {
		return relPath
	}
	if relPath == "" {
		return src.Path
	}
	return src.Path + "/" + relPath
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

// Fileread implements sftp.FileReader.
func (h *sftpHandler) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	share, rest, err := h.resolveShare(r.Filepath)
	if err != nil {
		return nil, sftp.ErrSSHFxNoSuchFile
	}
	if share == nil {
		return nil, sftp.ErrSSHFxPermissionDenied
	}

	src, err := findInSources(share, rest)
	if err != nil {
		return nil, sftp.ErrSSHFxNoSuchFile
	}

	reader, _, err := src.FS.Open(sourceFSPath(src, rest))
	if err != nil {
		return nil, sftp.ErrSSHFxNoSuchFile
	}

	return &readAtCloser{rsc: reader}, nil
}

// Filewrite implements sftp.FileWriter.
func (h *sftpHandler) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	if h.isReadOnly() {
		return nil, sftp.ErrSSHFxPermissionDenied
	}

	share, rest, err := h.resolveShare(r.Filepath)
	if err != nil {
		return nil, sftp.ErrSSHFxNoSuchFile
	}
	if share == nil || share.ReadOnly {
		return nil, sftp.ErrSSHFxPermissionDenied
	}

	// Find writable source
	for i := range share.Sources {
		if wfs, ok := share.Sources[i].FS.(rawfs.WritableRawFS); ok {
			return &writeAtHandler{
				wfs:  wfs,
				path: sourceFSPath(&share.Sources[i], rest),
			}, nil
		}
	}

	return nil, sftp.ErrSSHFxPermissionDenied
}

// Filecmd implements sftp.FileCmder (mkdir, rm, rename, etc.).
func (h *sftpHandler) Filecmd(r *sftp.Request) error {
	switch r.Method {
	case "Mkdir":
		if h.isReadOnly() {
			return sftp.ErrSSHFxPermissionDenied
		}
		share, rest, err := h.resolveShare(r.Filepath)
		if err != nil || share == nil || share.ReadOnly {
			return sftp.ErrSSHFxPermissionDenied
		}
		for i := range share.Sources {
			if wfs, ok := share.Sources[i].FS.(rawfs.WritableRawFS); ok {
				return wfs.MkDir(sourceFSPath(&share.Sources[i], rest))
			}
		}
		return sftp.ErrSSHFxPermissionDenied

	case "Remove":
		if h.isReadOnly() {
			return sftp.ErrSSHFxPermissionDenied
		}
		share, rest, err := h.resolveShare(r.Filepath)
		if err != nil || share == nil || share.ReadOnly {
			return sftp.ErrSSHFxPermissionDenied
		}
		src, err := findInSources(share, rest)
		if err != nil {
			return sftp.ErrSSHFxNoSuchFile
		}
		if wfs, ok := src.FS.(rawfs.WritableRawFS); ok {
			return wfs.Delete(sourceFSPath(src, rest))
		}
		return sftp.ErrSSHFxPermissionDenied

	case "Rmdir":
		return h.Filecmd(&sftp.Request{Method: "Remove", Filepath: r.Filepath})

	case "Rename":
		return sftp.ErrSSHFxOpUnsupported

	case "Symlink":
		return sftp.ErrSSHFxOpUnsupported

	default:
		return sftp.ErrSSHFxOpUnsupported
	}
}

// Filelist implements sftp.FileLister (list, stat).
func (h *sftpHandler) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	switch r.Method {
	case "List":
		share, rest, err := h.resolveShare(r.Filepath)
		if err != nil {
			return nil, sftp.ErrSSHFxNoSuchFile
		}

		var entries []os.FileInfo

		if share == nil {
			// Root: list shares
			for _, s := range h.userShares() {
				entries = append(entries, &vFileInfo{name: s.Name, isDir: true})
			}
		} else {
			// Merge entries from all sources
			seen := make(map[string]bool)
			for i := range share.Sources {
				src := &share.Sources[i]
				dirEntries, err := src.FS.ReadDir(sourceFSPath(src, rest))
				if err != nil {
					continue
				}
				for _, e := range dirEntries {
					if seen[e.Name] {
						continue
					}
					seen[e.Name] = true
					entries = append(entries, &vFileInfo{
						name:  e.Name,
						size:  e.Size,
						isDir: e.IsDir,
					})
				}
			}
		}

		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})

		return listerAt(entries), nil

	case "Stat":
		share, rest, err := h.resolveShare(r.Filepath)
		if err != nil {
			return nil, sftp.ErrSSHFxNoSuchFile
		}
		if share == nil {
			return listerAt([]os.FileInfo{&vFileInfo{name: "/", isDir: true}}), nil
		}
		if rest == "" {
			return listerAt([]os.FileInfo{&vFileInfo{name: share.Name, isDir: true}}), nil
		}
		src, err := findInSources(share, rest)
		if err != nil {
			return nil, sftp.ErrSSHFxNoSuchFile
		}
		fi, err := src.FS.Stat(sourceFSPath(src, rest))
		if err != nil {
			return nil, sftp.ErrSSHFxNoSuchFile
		}
		return listerAt([]os.FileInfo{&vFileInfo{
			name:    fi.Name,
			size:    fi.Size,
			isDir:   fi.IsDir,
			modTime: fi.ModTime,
		}}), nil

	default:
		return nil, sftp.ErrSSHFxOpUnsupported
	}
}

// listerAt implements sftp.ListerAt.
type listerAt []os.FileInfo

func (l listerAt) ListAt(ls []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(ls, l[offset:])
	if n < len(ls) {
		return n, io.EOF
	}
	return n, nil
}

// readAtCloser adapts a ReadSeekCloser to io.ReaderAt.
type readAtCloser struct {
	rsc rawfs.ReadSeekCloser
}

func (r *readAtCloser) ReadAt(p []byte, off int64) (int, error) {
	_, err := r.rsc.Seek(off, io.SeekStart)
	if err != nil {
		return 0, err
	}
	return r.rsc.Read(p)
}

func (r *readAtCloser) Close() error {
	return r.rsc.Close()
}

// writeAtHandler provides io.WriterAt by buffering and writing on close.
// This is a simplified implementation — full random write is not supported.
type writeAtHandler struct {
	wfs  rawfs.WritableRawFS
	path string
	pr   *io.PipeReader
	pw   *io.PipeWriter
	done chan error
}

func (w *writeAtHandler) WriteAt(p []byte, off int64) (int, error) {
	if w.pw == nil {
		// Start pipe on first write
		w.pr, w.pw = io.Pipe()
		w.done = make(chan error, 1)
		go func() {
			w.done <- w.wfs.Write(w.path, w.pr, -1)
		}()
	}
	return w.pw.Write(p)
}

func (w *writeAtHandler) Close() error {
	if w.pw == nil {
		return nil
	}
	w.pw.Close()
	return <-w.done
}

// vFileInfo implements os.FileInfo.
type vFileInfo struct {
	name    string
	size    int64
	isDir   bool
	modTime time.Time
}

func (v *vFileInfo) Name() string { return v.name }
func (v *vFileInfo) Size() int64  { return v.size }
func (v *vFileInfo) Mode() os.FileMode {
	if v.isDir {
		return os.ModeDir | 0o755
	}
	return 0o644
}
func (v *vFileInfo) ModTime() time.Time {
	if v.modTime.IsZero() {
		return time.Now()
	}
	return v.modTime
}
func (v *vFileInfo) IsDir() bool { return v.isDir }
func (v *vFileInfo) Sys() any    { return nil }
