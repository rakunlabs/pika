// Package ftpserve implements an FTP server that bridges rawfs backends
// to serve files over FTP using ftpserverlib. Shares can combine multiple mount paths.
package ftpserve

import (
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/spf13/afero"

	"github.com/rakunlabs/pika/internal/rawfs"
)

// ShareSource is a single mount+path entry within a share.
type ShareSource struct {
	Mount string      // raw mount prefix
	Path  string      // sub-path within mount (empty = root)
	FS    rawfs.RawFS // resolved filesystem backend
}

// Share represents a named folder shared via FTP.
// It can reference multiple mount paths; their contents are merged.
type Share struct {
	Name     string
	Sources  []ShareSource
	ReadOnly bool
}

// clientFS implements afero.Fs (ftpserver.ClientDriver) for per-user file access.
// It also implements ClientDriverExtensionFileList for directory listing.
type clientFS struct {
	drv  *mainDriver
	user *User
}

var _ afero.Fs = (*clientFS)(nil)

func (fs *clientFS) Name() string { return "pika" }

// userShares returns shares visible to this user.
func (fs *clientFS) userShares() []Share {
	fs.drv.mu.RLock()
	shares := fs.drv.shares
	fs.drv.mu.RUnlock()

	if fs.user == nil || len(fs.user.Shares) == 0 {
		return shares
	}

	allowed := make(map[string]bool, len(fs.user.Shares))
	for _, s := range fs.user.Shares {
		allowed[s] = true
	}

	var filtered []Share
	for _, s := range shares {
		if allowed[s.Name] {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// resolveShare maps an FTP path to a share and relative path within it.
func (fs *clientFS) resolveShare(ftpPath string) (*Share, string, error) {
	ftpPath = path.Clean(ftpPath)
	ftpPath = strings.TrimPrefix(ftpPath, "/")

	if ftpPath == "" || ftpPath == "." {
		return nil, "", nil // root directory
	}

	parts := strings.SplitN(ftpPath, "/", 2)
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

func (fs *clientFS) isReadOnly(share *Share) bool {
	return (share != nil && share.ReadOnly) || (fs.user != nil && fs.user.ReadOnly)
}

// --- afero.Fs implementation ---

// Stat implements afero.Fs.
func (fs *clientFS) Stat(name string) (os.FileInfo, error) {
	share, rest, err := fs.resolveShare(name)
	if err != nil {
		return nil, err
	}
	if share == nil {
		return &virtualFileInfo{name: "/", isDir: true}, nil
	}
	if rest == "" {
		return &virtualFileInfo{name: share.Name, isDir: true}, nil
	}

	src, err := findInSources(share, rest)
	if err != nil {
		return nil, err
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

// Open implements afero.Fs.
func (fs *clientFS) Open(name string) (afero.File, error) {
	return fs.OpenFile(name, os.O_RDONLY, 0)
}

// OpenFile implements afero.Fs.
func (fs *clientFS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	share, rest, err := fs.resolveShare(name)
	if err != nil {
		return nil, err
	}

	// Directory open
	if share == nil {
		return &dirFile{name: "/", fs: fs}, nil
	}
	if rest == "" {
		return &dirFile{name: share.Name, fs: fs, share: share}, nil
	}

	// Check if target is a directory
	if fi, statErr := fs.Stat(name); statErr == nil && fi.IsDir() {
		return &dirFile{name: path.Base(name), fs: fs, share: share, rest: rest}, nil
	}

	// Write mode
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC) != 0 {
		if fs.isReadOnly(share) {
			return nil, fmt.Errorf("share %q is read-only", share.Name)
		}
		if flag&os.O_APPEND != 0 {
			return nil, fmt.Errorf("append not supported")
		}
		return fs.openWrite(share, rest, name)
	}

	// Read mode
	src, err := findInSources(share, rest)
	if err != nil {
		return nil, err
	}

	reader, rawFI, err := src.FS.Open(sourceFSPath(src, rest))
	if err != nil {
		return nil, err
	}

	return &readFile{
		name:   path.Base(name),
		reader: reader,
		size:   rawFI.Size,
	}, nil
}

func (fs *clientFS) openWrite(share *Share, rest, fullName string) (afero.File, error) {
	// Try to overwrite in existing source
	src, findErr := findInSources(share, rest)
	if findErr == nil {
		wfs, ok := src.FS.(rawfs.WritableRawFS)
		if !ok {
			return nil, fmt.Errorf("source is not writable")
		}
		return newWriteFile(path.Base(fullName), wfs, sourceFSPath(src, rest)), nil
	}

	// New file — write to first writable source
	wsrc, wfs, err := firstWritableSource(share)
	if err != nil {
		return nil, fmt.Errorf("share %q: %w", share.Name, err)
	}
	return newWriteFile(path.Base(fullName), wfs, sourceFSPath(wsrc, rest)), nil
}

// Create implements afero.Fs.
func (fs *clientFS) Create(name string) (afero.File, error) {
	return fs.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
}

// Mkdir implements afero.Fs.
func (fs *clientFS) Mkdir(name string, perm os.FileMode) error {
	share, rest, err := fs.resolveShare(name)
	if err != nil {
		return err
	}
	if share == nil {
		return fmt.Errorf("cannot create directory at root")
	}
	if fs.isReadOnly(share) {
		return fmt.Errorf("share %q is read-only", share.Name)
	}

	wsrc, wfs, err := firstWritableSource(share)
	if err != nil {
		return fmt.Errorf("share %q: %w", share.Name, err)
	}

	return wfs.MkDir(sourceFSPath(wsrc, rest))
}

// MkdirAll implements afero.Fs.
func (fs *clientFS) MkdirAll(p string, perm os.FileMode) error {
	return fs.Mkdir(p, perm)
}

// Remove implements afero.Fs.
func (fs *clientFS) Remove(name string) error {
	share, rest, err := fs.resolveShare(name)
	if err != nil {
		return err
	}
	if share == nil {
		return fmt.Errorf("cannot delete root")
	}
	if fs.isReadOnly(share) {
		return fmt.Errorf("share %q is read-only", share.Name)
	}

	src, err := findInSources(share, rest)
	if err != nil {
		return err
	}

	wfs, ok := src.FS.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("source is not writable")
	}

	return wfs.Delete(sourceFSPath(src, rest))
}

// RemoveAll implements afero.Fs.
func (fs *clientFS) RemoveAll(p string) error {
	return fs.Remove(p)
}

// Rename implements afero.Fs.
func (fs *clientFS) Rename(oldname, newname string) error {
	return fmt.Errorf("rename not supported")
}

func (fs *clientFS) Chmod(name string, mode os.FileMode) error            { return nil }
func (fs *clientFS) Chown(name string, uid, gid int) error                { return nil }
func (fs *clientFS) Chtimes(name string, atime time.Time, mtime time.Time) error { return nil }

// ReadDir implements ftpserver.ClientDriverExtensionFileList for directory listing.
func (fs *clientFS) ReadDir(name string) ([]os.FileInfo, error) {
	share, rest, err := fs.resolveShare(name)
	if err != nil {
		return nil, err
	}

	if share == nil {
		shares := fs.userShares()
		infos := make([]os.FileInfo, len(shares))
		for i, s := range shares {
			infos[i] = &virtualFileInfo{name: s.Name, isDir: true}
		}
		return infos, nil
	}

	seen := make(map[string]bool)
	var infos []os.FileInfo

	for i := range share.Sources {
		src := &share.Sources[i]
		entries, err := src.FS.ReadDir(sourceFSPath(src, rest))
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

// --- Helper functions ---

// sourceFSPath returns the full rawfs path for a source + relative path.
func sourceFSPath(s *ShareSource, relPath string) string {
	if s.Path == "" {
		return relPath
	}
	if relPath == "" {
		return s.Path
	}
	return s.Path + "/" + relPath
}

// findInSources looks up a relative path across all sources in a share.
func findInSources(share *Share, relPath string) (*ShareSource, error) {
	for i := range share.Sources {
		src := &share.Sources[i]
		_, err := src.FS.Stat(sourceFSPath(src, relPath))
		if err == nil {
			return src, nil
		}
	}
	return nil, os.ErrNotExist
}

// firstWritableSource returns the first source whose FS is writable.
func firstWritableSource(share *Share) (*ShareSource, rawfs.WritableRawFS, error) {
	for i := range share.Sources {
		if wfs, ok := share.Sources[i].FS.(rawfs.WritableRawFS); ok {
			return &share.Sources[i], wfs, nil
		}
	}
	return nil, nil, fmt.Errorf("no writable source in share")
}

// --- File types ---

// readFile implements afero.File for reading.
type readFile struct {
	name   string
	reader rawfs.ReadSeekCloser
	size   int64
}

var _ afero.File = (*readFile)(nil)

func (f *readFile) Name() string                                    { return f.name }
func (f *readFile) Read(p []byte) (int, error)                      { return f.reader.Read(p) }
func (f *readFile) ReadAt(p []byte, off int64) (int, error)         { return 0, os.ErrInvalid }
func (f *readFile) Seek(offset int64, whence int) (int64, error)    { return f.reader.Seek(offset, whence) }
func (f *readFile) Write(p []byte) (int, error)                     { return 0, os.ErrInvalid }
func (f *readFile) WriteAt(p []byte, off int64) (int, error)        { return 0, os.ErrInvalid }
func (f *readFile) Close() error                                    { return f.reader.Close() }
func (f *readFile) Readdir(count int) ([]os.FileInfo, error)        { return nil, os.ErrInvalid }
func (f *readFile) Readdirnames(count int) ([]string, error)        { return nil, os.ErrInvalid }
func (f *readFile) Sync() error                                     { return nil }
func (f *readFile) Truncate(size int64) error                       { return os.ErrInvalid }
func (f *readFile) WriteString(s string) (int, error)               { return 0, os.ErrInvalid }
func (f *readFile) Stat() (os.FileInfo, error) {
	return &virtualFileInfo{name: f.name, size: f.size}, nil
}

// writeFile implements afero.File for writing via pipe.
type writeFile struct {
	name string
	pw   *io.PipeWriter
	done chan error
}

var _ afero.File = (*writeFile)(nil)

func newWriteFile(name string, wfs rawfs.WritableRawFS, fsPath string) *writeFile {
	pr, pw := io.Pipe()
	done := make(chan error, 1)

	go func() {
		done <- wfs.Write(fsPath, pr, -1)
	}()

	return &writeFile{name: name, pw: pw, done: done}
}

func (f *writeFile) Name() string                                    { return f.name }
func (f *writeFile) Read(p []byte) (int, error)                      { return 0, os.ErrInvalid }
func (f *writeFile) ReadAt(p []byte, off int64) (int, error)         { return 0, os.ErrInvalid }
func (f *writeFile) Seek(offset int64, whence int) (int64, error)    { return 0, os.ErrInvalid }
func (f *writeFile) Write(p []byte) (int, error)                     { return f.pw.Write(p) }
func (f *writeFile) WriteAt(p []byte, off int64) (int, error)        { return 0, os.ErrInvalid }
func (f *writeFile) Readdir(count int) ([]os.FileInfo, error)        { return nil, os.ErrInvalid }
func (f *writeFile) Readdirnames(count int) ([]string, error)        { return nil, os.ErrInvalid }
func (f *writeFile) Sync() error                                     { return nil }
func (f *writeFile) Truncate(size int64) error                       { return os.ErrInvalid }
func (f *writeFile) WriteString(s string) (int, error)               { return f.pw.Write([]byte(s)) }
func (f *writeFile) Stat() (os.FileInfo, error) {
	return &virtualFileInfo{name: f.name}, nil
}
func (f *writeFile) Close() error {
	f.pw.Close()
	return <-f.done
}

// dirFile implements afero.File for directory entries.
type dirFile struct {
	name  string
	fs    *clientFS
	share *Share
	rest  string
}

var _ afero.File = (*dirFile)(nil)

func (f *dirFile) Name() string                                    { return f.name }
func (f *dirFile) Read(p []byte) (int, error)                      { return 0, os.ErrInvalid }
func (f *dirFile) ReadAt(p []byte, off int64) (int, error)         { return 0, os.ErrInvalid }
func (f *dirFile) Seek(offset int64, whence int) (int64, error)    { return 0, os.ErrInvalid }
func (f *dirFile) Write(p []byte) (int, error)                     { return 0, os.ErrInvalid }
func (f *dirFile) WriteAt(p []byte, off int64) (int, error)        { return 0, os.ErrInvalid }
func (f *dirFile) Close() error                                    { return nil }
func (f *dirFile) Sync() error                                     { return nil }
func (f *dirFile) Truncate(size int64) error                       { return os.ErrInvalid }
func (f *dirFile) WriteString(s string) (int, error)               { return 0, os.ErrInvalid }

func (f *dirFile) Stat() (os.FileInfo, error) {
	return &virtualFileInfo{name: f.name, isDir: true}, nil
}

func (f *dirFile) Readdir(count int) ([]os.FileInfo, error) {
	fullPath := "/"
	if f.share != nil {
		fullPath = "/" + f.share.Name
		if f.rest != "" {
			fullPath += "/" + f.rest
		}
	}
	return f.fs.ReadDir(fullPath)
}

func (f *dirFile) Readdirnames(count int) ([]string, error) {
	infos, err := f.Readdir(count)
	if err != nil {
		return nil, err
	}

	names := make([]string, len(infos))
	for i, fi := range infos {
		names[i] = fi.Name()
	}
	return names, nil
}

// --- virtualFileInfo ---

// virtualFileInfo implements os.FileInfo for virtual entries.
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
