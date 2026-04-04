// Package ftpfs implements rawfs.RawFS for FTP/FTPS servers.
package ftpfs

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"

	"github.com/rakunlabs/pika/internal/rawfs"
)

const (
	connectTimeout    = 10 * time.Second
	inMemoryThreshold = 32 * 1024 * 1024 // 32 MB
)

func init() {
	rawfs.NewFTPFSFunc = New
}

// FS implements rawfs.RawFS for FTP servers.
type FS struct {
	host     string
	username string
	password string
	basePath string
	useTLS   bool

	mu   sync.Mutex
	conn *ftp.ServerConn
}

// New creates a new FTP filesystem backend.
func New(host, username, password, basePath string, useTLS bool) (rawfs.RawFS, error) {
	if host == "" {
		return nil, fmt.Errorf("ftp: host is required")
	}

	// Add default port if not specified
	if !strings.Contains(host, ":") {
		host += ":21"
	}

	basePath = strings.TrimSuffix(basePath, "/")

	fs := &FS{
		host:     host,
		username: username,
		password: password,
		basePath: basePath,
		useTLS:   useTLS,
	}

	// Test connection
	conn, err := fs.connect()
	if err != nil {
		return nil, fmt.Errorf("ftp: initial connection failed: %w", err)
	}
	fs.conn = conn

	return fs, nil
}

// connect establishes a new FTP connection.
func (f *FS) connect() (*ftp.ServerConn, error) {
	opts := []ftp.DialOption{
		ftp.DialWithTimeout(connectTimeout),
	}

	if f.useTLS {
		opts = append(opts, ftp.DialWithExplicitTLS(&tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // user can configure TLS
		}))
	}

	conn, err := ftp.Dial(f.host, opts...)
	if err != nil {
		return nil, err
	}

	if f.username != "" {
		if err := conn.Login(f.username, f.password); err != nil {
			conn.Quit()
			return nil, fmt.Errorf("login failed: %w", err)
		}
	}

	return conn, nil
}

// getConn returns a working FTP connection, reconnecting if necessary.
func (f *FS) getConn() (*ftp.ServerConn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.conn != nil {
		// Test connection with a no-op
		if err := f.conn.NoOp(); err == nil {
			return f.conn, nil
		}
		// Connection is dead, close and reconnect
		f.conn.Quit()
		f.conn = nil
	}

	conn, err := f.connect()
	if err != nil {
		return nil, err
	}

	f.conn = conn
	return conn, nil
}

// fullPath returns the full FTP path for a relative path.
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
	conn, err := f.getConn()
	if err != nil {
		return nil, fmt.Errorf("ftp: %w", err)
	}

	fullPath := f.fullPath(relPath)

	// Try to get entry info by listing the parent directory
	dir := path.Dir(fullPath)
	base := path.Base(fullPath)

	entries, err := conn.List(dir)
	if err != nil {
		return nil, fmt.Errorf("ftp: listing %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.Name == base {
			return &rawfs.FileInfo{
				Name:    entry.Name,
				Size:    int64(entry.Size),
				IsDir:   entry.Type == ftp.EntryTypeFolder,
				ModTime: entry.Time,
			}, nil
		}
	}

	// Check if the path itself is a directory by listing it
	if _, err := conn.List(fullPath); err == nil {
		name := path.Base(fullPath)
		if name == "" || name == "/" {
			name = "/"
		}
		return &rawfs.FileInfo{
			Name:  name,
			IsDir: true,
		}, nil
	}

	return nil, fmt.Errorf("not found: %s: %w", relPath, os.ErrNotExist)
}

// ReadDir lists entries in a directory.
func (f *FS) ReadDir(relPath string) ([]rawfs.DirEntry, error) {
	conn, err := f.getConn()
	if err != nil {
		return nil, fmt.Errorf("ftp: %w", err)
	}

	fullPath := f.fullPath(relPath)

	entries, err := conn.List(fullPath)
	if err != nil {
		return nil, fmt.Errorf("ftp: listing %s: %w", fullPath, err)
	}

	var result []rawfs.DirEntry
	for _, entry := range entries {
		// Skip . and ..
		if entry.Name == "." || entry.Name == ".." {
			continue
		}

		result = append(result, rawfs.DirEntry{
			Name:  entry.Name,
			IsDir: entry.Type == ftp.EntryTypeFolder,
			Size:  int64(entry.Size),
		})
	}

	return result, nil
}

// Open returns a seekable reader for a file.
// Downloads the file to memory (small) or temp file (large).
func (f *FS) Open(relPath string) (rawfs.ReadSeekCloser, *rawfs.FileInfo, error) {
	conn, err := f.getConn()
	if err != nil {
		return nil, nil, fmt.Errorf("ftp: %w", err)
	}

	fullPath := f.fullPath(relPath)

	// Get file size first
	size, err := conn.FileSize(fullPath)
	if err != nil {
		return nil, nil, fmt.Errorf("ftp: getting file size: %w", err)
	}

	resp, err := conn.Retr(fullPath)
	if err != nil {
		return nil, nil, fmt.Errorf("ftp: retrieving %s: %w", fullPath, err)
	}
	defer resp.Close()

	fi := &rawfs.FileInfo{
		Name:    path.Base(fullPath),
		Size:    size,
		IsDir:   false,
		ModTime: time.Time{}, // FTP RETR doesn't provide modtime
	}

	if size <= inMemoryThreshold {
		data, err := io.ReadAll(resp)
		if err != nil {
			return nil, nil, fmt.Errorf("ftp: reading file: %w", err)
		}
		fi.Size = int64(len(data))
		return &memReadSeekCloser{Reader: bytes.NewReader(data)}, fi, nil
	}

	// Download to temp file
	tmpFile, err := os.CreateTemp("", "pika-ftp-*")
	if err != nil {
		return nil, nil, fmt.Errorf("ftp: creating temp file: %w", err)
	}

	n, err := io.Copy(tmpFile, resp)
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return nil, nil, fmt.Errorf("ftp: downloading to temp file: %w", err)
	}
	fi.Size = n

	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return nil, nil, fmt.Errorf("ftp: seeking temp file: %w", err)
	}

	return &tempFileReadSeekCloser{File: tmpFile}, fi, nil
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

// Rename renames a file on the FTP server.
func (f *FS) Rename(oldPath, newPath string) error {
	conn, err := f.getConn()
	if err != nil {
		return fmt.Errorf("ftp: %w", err)
	}

	oldFull := f.fullPath(oldPath)
	newFull := f.fullPath(newPath)

	if err := conn.Rename(oldFull, newFull); err != nil {
		return fmt.Errorf("ftp rename: %w", err)
	}
	return nil
}

var _ rawfs.RawFS = (*FS)(nil)
var _ rawfs.RenamableRawFS = (*FS)(nil)
var _ rawfs.ReadSeekCloser = (*memReadSeekCloser)(nil)
var _ rawfs.ReadSeekCloser = (*tempFileReadSeekCloser)(nil)
