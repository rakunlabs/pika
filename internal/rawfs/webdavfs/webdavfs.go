// Package webdavfs implements rawfs.RawFS for remote WebDAV servers.
package webdavfs

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/studio-b12/gowebdav"

	"github.com/rakunlabs/pika/internal/rawfs"
)

const (
	inMemoryThreshold = 32 * 1024 * 1024 // 32 MB
)

func init() {
	rawfs.NewWebDAVFSFunc = New
}

// FS implements rawfs.RawFS for WebDAV servers.
type FS struct {
	client   *gowebdav.Client
	basePath string
}

// New creates a new WebDAV filesystem backend.
func New(url, username, password, basePath string) (rawfs.RawFS, error) {
	if url == "" {
		return nil, fmt.Errorf("webdav: url is required")
	}

	basePath = strings.TrimSuffix(basePath, "/")

	client := gowebdav.NewClient(url, username, password)
	if err := client.Connect(); err != nil {
		return nil, fmt.Errorf("webdav: initial connection failed: %w", err)
	}

	return &FS{
		client:   client,
		basePath: basePath,
	}, nil
}

// fullPath returns the full WebDAV path for a relative path.
func (f *FS) fullPath(relPath string) string {
	relPath = strings.Trim(relPath, "/")
	if f.basePath == "" {
		if relPath == "" {
			return "/"
		}
		return "/" + relPath
	}
	if relPath == "" {
		return f.basePath
	}
	return f.basePath + "/" + relPath
}

// Stat returns metadata about a file or directory.
func (f *FS) Stat(relPath string) (*rawfs.FileInfo, error) {
	fullPath := f.fullPath(relPath)

	fi, err := f.client.Stat(fullPath)
	if err != nil {
		if gowebdav.IsErrNotFound(err) {
			return nil, fmt.Errorf("not found: %s: %w", relPath, os.ErrNotExist)
		}
		return nil, fmt.Errorf("webdav: stat %s: %w", fullPath, err)
	}

	return &rawfs.FileInfo{
		Name:    fi.Name(),
		Size:    fi.Size(),
		IsDir:   fi.IsDir(),
		ModTime: fi.ModTime(),
	}, nil
}

// ReadDir lists entries in a directory.
func (f *FS) ReadDir(relPath string) ([]rawfs.DirEntry, error) {
	fullPath := f.fullPath(relPath)

	files, err := f.client.ReadDir(fullPath)
	if err != nil {
		if gowebdav.IsErrNotFound(err) {
			return nil, fmt.Errorf("not found: %s: %w", relPath, os.ErrNotExist)
		}
		return nil, fmt.Errorf("webdav: readdir %s: %w", fullPath, err)
	}

	result := make([]rawfs.DirEntry, 0, len(files))
	for _, fi := range files {
		name := fi.Name()
		if name == "." || name == ".." {
			continue
		}
		result = append(result, rawfs.DirEntry{
			Name:  name,
			IsDir: fi.IsDir(),
			Size:  fi.Size(),
		})
	}

	return result, nil
}

// Open returns a seekable reader for a file.
// Downloads the file to memory (small) or temp file (large).
func (f *FS) Open(relPath string) (rawfs.ReadSeekCloser, *rawfs.FileInfo, error) {
	fullPath := f.fullPath(relPath)

	// Get file info first
	fi, err := f.client.Stat(fullPath)
	if err != nil {
		if gowebdav.IsErrNotFound(err) {
			return nil, nil, fmt.Errorf("not found: %s: %w", relPath, os.ErrNotExist)
		}
		return nil, nil, fmt.Errorf("webdav: stat %s: %w", fullPath, err)
	}

	reader, err := f.client.ReadStream(fullPath)
	if err != nil {
		return nil, nil, fmt.Errorf("webdav: reading %s: %w", fullPath, err)
	}
	defer reader.Close()

	info := &rawfs.FileInfo{
		Name:    fi.Name(),
		Size:    fi.Size(),
		IsDir:   false,
		ModTime: fi.ModTime(),
	}

	if fi.Size() <= inMemoryThreshold {
		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, nil, fmt.Errorf("webdav: reading file: %w", err)
		}
		info.Size = int64(len(data))
		return &memReadSeekCloser{Reader: bytes.NewReader(data)}, info, nil
	}

	// Download to temp file
	tmpFile, err := os.CreateTemp("", "pika-webdav-*")
	if err != nil {
		return nil, nil, fmt.Errorf("webdav: creating temp file: %w", err)
	}

	n, err := io.Copy(tmpFile, reader)
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return nil, nil, fmt.Errorf("webdav: downloading to temp file: %w", err)
	}
	info.Size = n

	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return nil, nil, fmt.Errorf("webdav: seeking temp file: %w", err)
	}

	return &tempFileReadSeekCloser{File: tmpFile}, info, nil
}

// Write creates or overwrites a file at the given path.
func (f *FS) Write(filePath string, r io.Reader, size int64) error {
	fullPath := f.fullPath(filePath)
	return f.client.WriteStream(fullPath, r, 0o644)
}

// Delete removes a file at the given path.
func (f *FS) Delete(filePath string) error {
	fullPath := f.fullPath(filePath)
	err := f.client.Remove(fullPath)
	if err != nil && gowebdav.IsErrNotFound(err) {
		return fmt.Errorf("not found: %s: %w", filePath, os.ErrNotExist)
	}
	return err
}

// MkDir creates a directory at the given path.
func (f *FS) MkDir(dirPath string) error {
	fullPath := f.fullPath(dirPath)
	return f.client.Mkdir(fullPath, 0o755)
}

// Rename moves a file or directory.
func (f *FS) Rename(oldPath, newPath string) error {
	return f.client.Rename(f.fullPath(oldPath), f.fullPath(newPath), true)
}

// Copy copies a file on the server side.
func (f *FS) Copy(srcPath, dstPath string) error {
	return f.client.Copy(f.fullPath(srcPath), f.fullPath(dstPath), true)
}

// memReadSeekCloser wraps a bytes.Reader to implement ReadSeekCloser.
type memReadSeekCloser struct {
	*bytes.Reader
}

func (m *memReadSeekCloser) Close() error { return nil }

// tempFileReadSeekCloser wraps an os.File that auto-deletes on Close.
type tempFileReadSeekCloser struct {
	*os.File
}

func (t *tempFileReadSeekCloser) Close() error {
	name := t.File.Name()
	t.File.Close()
	return os.Remove(name)
}

var _ rawfs.RawFS = (*FS)(nil)
var _ rawfs.WritableRawFS = (*FS)(nil)
var _ rawfs.RenamableRawFS = (*FS)(nil)
var _ rawfs.CopyableRawFS = (*FS)(nil)
var _ rawfs.ReadSeekCloser = (*memReadSeekCloser)(nil)
var _ rawfs.ReadSeekCloser = (*tempFileReadSeekCloser)(nil)

// IsErrNotFound checks if an error is a WebDAV "not found" error.
func IsErrNotFound(err error) bool {
	return gowebdav.IsErrNotFound(err)
}

// normalizePath cleans up a path for WebDAV.
func normalizePath(p string) string {
	p = path.Clean(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}
