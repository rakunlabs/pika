// Package rawfs defines the interface for raw filesystem backends used by the
// raw file serving feature. Implementations include local filesystem, S3, and FTP.
package rawfs

import (
	"io"
	"time"
)

// FileInfo holds metadata about a file or directory.
type FileInfo struct {
	Name    string
	Size    int64
	IsDir   bool
	ModTime time.Time
}

// DirEntry represents a single entry in a directory listing.
type DirEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

// ReadSeekCloser combines io.Reader, io.Seeker, and io.Closer.
// Required by http.ServeContent for Range request support.
type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

// RawFS is the read-only filesystem interface for raw mount backends.
type RawFS interface {
	// Stat returns metadata about a file or directory at the given path.
	// Returns an error wrapping service.ErrNotFound if the path does not exist.
	Stat(path string) (*FileInfo, error)

	// ReadDir lists the entries in a directory.
	ReadDir(path string) ([]DirEntry, error)

	// Open returns a seekable reader for the file at the given path,
	// along with its metadata. The caller must Close the reader.
	Open(path string) (ReadSeekCloser, *FileInfo, error)
}

// WritableRawFS extends RawFS with write operations.
// Only certain backends (e.g., S3) implement this interface.
type WritableRawFS interface {
	RawFS

	// Write creates or overwrites a file at the given path.
	Write(path string, r io.Reader, size int64) error

	// Delete removes a file at the given path.
	Delete(path string) error

	// MkDir creates a directory at the given path.
	// For object stores like S3, this creates a zero-byte key with trailing slash.
	MkDir(path string) error
}

// RenamableRawFS extends RawFS with rename support.
// Backends that support native rename implement this for efficiency.
type RenamableRawFS interface {
	RawFS
	// Rename renames/moves a file or directory within the same backend.
	Rename(oldPath, newPath string) error
}

// CopyableRawFS extends RawFS with server-side copy support.
// Backends like S3 can copy without re-downloading data.
type CopyableRawFS interface {
	RawFS
	// Copy copies a file from srcPath to dstPath within the same backend.
	Copy(srcPath, dstPath string) error
}

// IsWritable returns true if the filesystem supports write operations.
func IsWritable(fs RawFS) bool {
	_, ok := fs.(WritableRawFS)
	return ok
}

// IsRenamable returns true if the filesystem supports native rename.
func IsRenamable(fs RawFS) bool {
	_, ok := fs.(RenamableRawFS)
	return ok
}

// IsCopyable returns true if the filesystem supports native copy.
func IsCopyable(fs RawFS) bool {
	_, ok := fs.(CopyableRawFS)
	return ok
}

// GenericCopy performs a copy by reading from src and writing to dst.
// Used as a fallback when the backend doesn't support native copy.
func GenericCopy(srcFS RawFS, srcPath string, dstFS WritableRawFS, dstPath string) error {
	reader, fi, err := srcFS.Open(srcPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	return dstFS.Write(dstPath, reader, fi.Size)
}
