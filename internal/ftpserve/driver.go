// Package ftpserve implements a goftp driver that bridges rawfs backends
// to serve files over FTP. Shares can combine multiple mount paths.
package ftpserve

import (
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/rakunlabs/pika/internal/rawfs"
	ftpserver "goftp.io/server/v2"
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

// Driver implements goftp.io/server/v2.Driver.
// FTP clients see a virtual filesystem where each share is a top-level directory.
// Share visibility is filtered per-user based on the auth config.
type Driver struct {
	shares []Share
	auth   *MultiUserAuth
}

// NewDriver creates a new FTP driver from the given shares and auth.
func NewDriver(shares []Share, auth *MultiUserAuth) *Driver {
	return &Driver{shares: shares, auth: auth}
}

// userShares returns the shares visible to the user in the given context.
func (d *Driver) userShares(ctx *ftpserver.Context) []Share {
	if d.auth == nil || ctx == nil {
		return d.shares
	}
	user := d.auth.GetUser(ctx.Sess.LoginUser())
	if user == nil || len(user.Shares) == 0 {
		return d.shares // no restrictions
	}

	allowed := make(map[string]bool, len(user.Shares))
	for _, s := range user.Shares {
		allowed[s] = true
	}

	var filtered []Share
	for _, s := range d.shares {
		if allowed[s.Name] {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// UpdateShares replaces the current shares atomically.
func (d *Driver) UpdateShares(shares []Share) {
	d.shares = shares
}

// isUserReadOnly checks if the connected user has read-only access.
func (d *Driver) isUserReadOnly(ctx *ftpserver.Context) bool {
	if d.auth == nil || ctx == nil {
		return false
	}
	user := d.auth.GetUser(ctx.Sess.LoginUser())
	return user != nil && user.ReadOnly
}

// resolveShare maps an FTP path like "/myshare/subdir/file.txt"
// to the share and the relative path within it, respecting per-user access.
func (d *Driver) resolveShare(ctx *ftpserver.Context, ftpPath string) (*Share, string, error) {
	ftpPath = path.Clean(ftpPath)
	ftpPath = strings.TrimPrefix(ftpPath, "/")

	if ftpPath == "" {
		return nil, "", nil // root directory listing
	}

	parts := strings.SplitN(ftpPath, "/", 2)
	shareName := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = parts[1]
	}

	shares := d.userShares(ctx)
	for i := range shares {
		if shares[i].Name == shareName {
			return &shares[i], rest, nil
		}
	}

	return nil, "", os.ErrNotExist
}

// fsPath returns the full rawfs path for a source + relative path.
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
// Returns the first source where the path exists.
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

// firstWritableSource returns the first source whose FS is writable,
// or an error if none are writable.
func firstWritableSource(share *Share) (*ShareSource, rawfs.WritableRawFS, error) {
	for i := range share.Sources {
		if wfs, ok := share.Sources[i].FS.(rawfs.WritableRawFS); ok {
			return &share.Sources[i], wfs, nil
		}
	}
	return nil, nil, fmt.Errorf("no writable source in share")
}

// Stat implements goftp Driver.
func (d *Driver) Stat(ctx *ftpserver.Context, ftpPath string) (os.FileInfo, error) {
	share, rest, err := d.resolveShare(ctx, ftpPath)
	if err != nil {
		return nil, err
	}

	// Root directory
	if share == nil {
		return &virtualFileInfo{name: "/", isDir: true}, nil
	}

	// Share root — always a directory
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

// ListDir implements goftp Driver.
func (d *Driver) ListDir(ctx *ftpserver.Context, ftpPath string, callback func(os.FileInfo) error) error {
	share, rest, err := d.resolveShare(ctx, ftpPath)
	if err != nil {
		return err
	}

	// Root: list shares visible to this user
	if share == nil {
		for _, s := range d.userShares(ctx) {
			if err := callback(&virtualFileInfo{name: s.Name, isDir: true}); err != nil {
				return err
			}
		}
		return nil
	}

	// Merge entries from all sources, deduplicating by name
	seen := make(map[string]bool)
	for i := range share.Sources {
		src := &share.Sources[i]
		entries, err := src.FS.ReadDir(sourceFSPath(src, rest))
		if err != nil {
			continue // skip sources where this path doesn't exist
		}
		for _, e := range entries {
			if seen[e.Name] {
				continue
			}
			seen[e.Name] = true
			if err := callback(&virtualFileInfo{
				name:  e.Name,
				size:  e.Size,
				isDir: e.IsDir,
			}); err != nil {
				return err
			}
		}
	}

	return nil
}

// GetFile implements goftp Driver.
func (d *Driver) GetFile(ctx *ftpserver.Context, ftpPath string, offset int64) (int64, io.ReadCloser, error) {
	share, rest, err := d.resolveShare(ctx, ftpPath)
	if err != nil {
		return 0, nil, err
	}
	if share == nil {
		return 0, nil, fmt.Errorf("cannot read root directory")
	}

	src, err := findInSources(share, rest)
	if err != nil {
		return 0, nil, err
	}

	reader, fi, err := src.FS.Open(sourceFSPath(src, rest))
	if err != nil {
		return 0, nil, err
	}

	if offset > 0 {
		if _, err := reader.Seek(offset, io.SeekStart); err != nil {
			reader.Close()
			return 0, nil, err
		}
	}

	return fi.Size - offset, reader, nil
}

// PutFile implements goftp Driver.
func (d *Driver) PutFile(ctx *ftpserver.Context, ftpPath string, data io.Reader, offset int64) (int64, error) {
	share, rest, err := d.resolveShare(ctx, ftpPath)
	if err != nil {
		return 0, err
	}
	if share == nil {
		return 0, fmt.Errorf("cannot write to root directory")
	}
	if share.ReadOnly || d.isUserReadOnly(ctx) {
		return 0, fmt.Errorf("share %q is read-only", share.Name)
	}

	if offset > 0 {
		return 0, fmt.Errorf("append not supported")
	}

	// Try to find the file in existing sources first for overwrite
	src, findErr := findInSources(share, rest)
	if findErr == nil {
		// Overwrite in the source where the file exists
		if wfs, ok := src.FS.(rawfs.WritableRawFS); ok {
			if err := wfs.Write(sourceFSPath(src, rest), data, -1); err != nil {
				return 0, err
			}
			return 0, nil
		}
		return 0, fmt.Errorf("source is not writable")
	}

	// New file — write to the first writable source
	wsrc, wfs, err := firstWritableSource(share)
	if err != nil {
		return 0, fmt.Errorf("share %q: %w", share.Name, err)
	}
	if err := wfs.Write(sourceFSPath(wsrc, rest), data, -1); err != nil {
		return 0, err
	}
	return 0, nil
}

// DeleteFile implements goftp Driver.
func (d *Driver) DeleteFile(ctx *ftpserver.Context, ftpPath string) error {
	share, rest, err := d.resolveShare(ctx, ftpPath)
	if err != nil {
		return err
	}
	if share == nil {
		return fmt.Errorf("cannot delete root")
	}
	if share.ReadOnly || d.isUserReadOnly(ctx) {
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

// DeleteDir implements goftp Driver.
func (d *Driver) DeleteDir(ctx *ftpserver.Context, ftpPath string) error {
	return d.DeleteFile(ctx, ftpPath)
}

// Rename implements goftp Driver.
func (d *Driver) Rename(ctx *ftpserver.Context, from, to string) error {
	return fmt.Errorf("rename not supported")
}

// MakeDir implements goftp Driver.
func (d *Driver) MakeDir(ctx *ftpserver.Context, ftpPath string) error {
	share, rest, err := d.resolveShare(ctx, ftpPath)
	if err != nil {
		return err
	}
	if share == nil {
		return fmt.Errorf("cannot create directory at root")
	}
	if share.ReadOnly || d.isUserReadOnly(ctx) {
		return fmt.Errorf("share %q is read-only", share.Name)
	}

	wsrc, wfs, err := firstWritableSource(share)
	if err != nil {
		return fmt.Errorf("share %q: %w", share.Name, err)
	}

	return wfs.MkDir(sourceFSPath(wsrc, rest))
}

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

var _ ftpserver.Driver = (*Driver)(nil)
