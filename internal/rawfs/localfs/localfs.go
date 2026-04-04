// Package localfs implements the rawfs.RawFS interface for the local filesystem.
package localfs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rakunlabs/pika/internal/rawfs"
)

// FS serves files from a local directory.
type FS struct {
	root string // absolute path to mount root
}

// New creates a new local filesystem backend rooted at the given directory.
// The path must exist and be a directory.
func New(root string) (*FS, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving local mount root: %w", err)
	}

	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("stat local mount root %q: %w", absRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("local mount root %q is not a directory", absRoot)
	}

	return &FS{root: absRoot}, nil
}

// safePath validates and resolves a relative path within the mount root.
// Prevents directory traversal attacks.
func (f *FS) safePath(relPath string) (string, error) {
	cleaned := filepath.Clean("/" + relPath)
	cleaned = strings.TrimPrefix(cleaned, "/")

	for _, part := range strings.Split(cleaned, string(filepath.Separator)) {
		if part == ".." {
			return "", fmt.Errorf("path traversal not allowed")
		}
	}

	full := filepath.Join(f.root, cleaned)

	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", fmt.Errorf("resolving file path: %w", err)
	}

	if !strings.HasPrefix(absFull, f.root+string(filepath.Separator)) && absFull != f.root {
		return "", fmt.Errorf("path escapes mount root")
	}

	return absFull, nil
}

// Stat returns metadata about a file or directory.
func (f *FS) Stat(path string) (*rawfs.FileInfo, error) {
	fsPath, err := f.safePath(path)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(fsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("not found: %s: %w", path, os.ErrNotExist)
		}
		return nil, err
	}

	return &rawfs.FileInfo{
		Name:    info.Name(),
		Size:    info.Size(),
		IsDir:   info.IsDir(),
		ModTime: info.ModTime(),
	}, nil
}

// ReadDir lists entries in a directory.
func (f *FS) ReadDir(path string) ([]rawfs.DirEntry, error) {
	fsPath, err := f.safePath(path)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(fsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("not found: %s: %w", path, os.ErrNotExist)
		}
		return nil, err
	}

	result := make([]rawfs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		result = append(result, rawfs.DirEntry{
			Name:  entry.Name(),
			IsDir: entry.IsDir(),
			Size:  info.Size(),
		})
	}

	return result, nil
}

// Open returns a seekable reader for a file.
func (f *FS) Open(path string) (rawfs.ReadSeekCloser, *rawfs.FileInfo, error) {
	fsPath, err := f.safePath(path)
	if err != nil {
		return nil, nil, err
	}

	file, err := os.Open(fsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("not found: %s: %w", path, os.ErrNotExist)
		}
		return nil, nil, err
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}

	fi := &rawfs.FileInfo{
		Name:    info.Name(),
		Size:    info.Size(),
		IsDir:   info.IsDir(),
		ModTime: info.ModTime(),
	}

	return file, fi, nil
}

// Write creates or overwrites a file at the given path.
func (f *FS) Write(path string, r io.Reader, size int64) error {
	fsPath, err := f.safePath(path)
	if err != nil {
		return err
	}

	// Ensure parent directory exists
	dir := filepath.Dir(fsPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating parent directory: %w", err)
	}

	file, err := os.Create(fsPath)
	if err != nil {
		return fmt.Errorf("creating file: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, r); err != nil {
		return fmt.Errorf("writing file: %w", err)
	}

	return nil
}

// Delete removes a file or empty directory.
func (f *FS) Delete(path string) error {
	fsPath, err := f.safePath(path)
	if err != nil {
		return err
	}

	if err := os.Remove(fsPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("not found: %s: %w", path, os.ErrNotExist)
		}
		return err
	}
	return nil
}

// MkDir creates a directory at the given path.
func (f *FS) MkDir(path string) error {
	fsPath, err := f.safePath(path)
	if err != nil {
		return err
	}

	return os.MkdirAll(fsPath, 0o755)
}

// Rename renames or moves a file/directory within the local filesystem.
func (f *FS) Rename(oldPath, newPath string) error {
	oldFS, err := f.safePath(oldPath)
	if err != nil {
		return err
	}
	newFS, err := f.safePath(newPath)
	if err != nil {
		return err
	}

	// Ensure parent of new path exists
	dir := filepath.Dir(newFS)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating parent directory: %w", err)
	}

	return os.Rename(oldFS, newFS)
}

// Copy copies a file within the local filesystem.
func (f *FS) Copy(srcPath, dstPath string) error {
	srcFS, err := f.safePath(srcPath)
	if err != nil {
		return err
	}
	dstFS, err := f.safePath(dstPath)
	if err != nil {
		return err
	}

	// Ensure parent of destination exists
	dir := filepath.Dir(dstFS)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating parent directory: %w", err)
	}

	src, err := os.Open(srcFS)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(dstFS)
	if err != nil {
		return err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return err
	}

	return nil
}

// Compile-time interface checks.
var _ rawfs.WritableRawFS = (*FS)(nil)
var _ rawfs.RenamableRawFS = (*FS)(nil)
var _ rawfs.CopyableRawFS = (*FS)(nil)
