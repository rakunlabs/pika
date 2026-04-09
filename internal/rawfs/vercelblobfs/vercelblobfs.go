// Package vercelblobfs implements rawfs.RawFS for Vercel Blob storage.
package vercelblobfs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/rakunlabs/pika/internal/rawfs"
)

const (
	defaultBaseURL    = "https://blob.vercel-storage.com"
	inMemoryThreshold = 32 * 1024 * 1024 // 32 MB
)

func init() {
	rawfs.NewVercelBlobFSFunc = New
}

// FS implements rawfs.RawFS for Vercel Blob storage.
type FS struct {
	token   string
	storeID string
	prefix  string
	client  *http.Client
	baseURL string
}

// New creates a new Vercel Blob filesystem backend.
func New(token, storeID, prefix string) (rawfs.RawFS, error) {
	if token == "" {
		return nil, fmt.Errorf("vercel-blob: token is required")
	}

	prefix = strings.Trim(prefix, "/")

	return &FS{
		token:   token,
		storeID: storeID,
		prefix:  prefix,
		client:  &http.Client{Timeout: 30 * time.Second},
		baseURL: defaultBaseURL,
	}, nil
}

// fullPath returns the full blob pathname for a relative path.
func (f *FS) fullPath(relPath string) string {
	relPath = strings.Trim(relPath, "/")
	if f.prefix == "" {
		return relPath
	}
	if relPath == "" {
		return f.prefix
	}
	return f.prefix + "/" + relPath
}

// listResponse is the JSON structure returned by the Vercel Blob list API.
type listResponse struct {
	Blobs  []blobEntry `json:"blobs"`
	Cursor string      `json:"cursor"`
	HasMore bool       `json:"hasMore"`
}

// blobEntry represents a single blob in the list response.
type blobEntry struct {
	URL        string    `json:"url"`
	Pathname   string    `json:"pathname"`
	Size       int64     `json:"size"`
	UploadedAt time.Time `json:"uploadedAt"`
}

// listBlobs lists all blobs with the given prefix.
func (f *FS) listBlobs(prefix string) ([]blobEntry, error) {
	var allBlobs []blobEntry
	cursor := ""

	for {
		params := url.Values{}
		if prefix != "" {
			params.Set("prefix", prefix)
		}
		params.Set("limit", "1000")
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		if f.storeID != "" {
			params.Set("storeId", f.storeID)
		}

		reqURL := f.baseURL + "?" + params.Encode()
		req, err := http.NewRequest(http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("vercel-blob: creating list request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+f.token)

		resp, err := f.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("vercel-blob: listing blobs: %w", err)
		}

		var listResp listResponse
		err = json.NewDecoder(resp.Body).Decode(&listResp)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("vercel-blob: decoding list response: %w", err)
		}

		allBlobs = append(allBlobs, listResp.Blobs...)

		if !listResp.HasMore || listResp.Cursor == "" {
			break
		}
		cursor = listResp.Cursor
	}

	return allBlobs, nil
}

// Stat returns metadata about a file or "directory" in Vercel Blob.
func (f *FS) Stat(relPath string) (*rawfs.FileInfo, error) {
	fullPath := f.fullPath(relPath)

	// Check for exact file match
	blobs, err := f.listBlobs(fullPath)
	if err != nil {
		return nil, err
	}

	for _, b := range blobs {
		if b.Pathname == fullPath {
			return &rawfs.FileInfo{
				Name:    path.Base(fullPath),
				Size:    b.Size,
				IsDir:   false,
				ModTime: b.UploadedAt,
			}, nil
		}
	}

	// Check if it's a "directory" (prefix has children)
	dirPrefix := fullPath
	if dirPrefix != "" && !strings.HasSuffix(dirPrefix, "/") {
		dirPrefix += "/"
	}

	dirBlobs, err := f.listBlobs(dirPrefix)
	if err != nil {
		return nil, err
	}

	if len(dirBlobs) > 0 {
		name := path.Base(strings.TrimSuffix(fullPath, "/"))
		if name == "" || name == "." {
			name = "root"
		}
		return &rawfs.FileInfo{
			Name:  name,
			IsDir: true,
		}, nil
	}

	return nil, fmt.Errorf("not found: %s: %w", relPath, os.ErrNotExist)
}

// ReadDir lists entries in a "directory" in Vercel Blob.
func (f *FS) ReadDir(relPath string) ([]rawfs.DirEntry, error) {
	dirPrefix := f.fullPath(relPath)
	if dirPrefix != "" && !strings.HasSuffix(dirPrefix, "/") {
		dirPrefix += "/"
	}

	blobs, err := f.listBlobs(dirPrefix)
	if err != nil {
		return nil, err
	}

	var entries []rawfs.DirEntry
	seenDirs := make(map[string]bool)

	for _, b := range blobs {
		// Remove the directory prefix to get the relative name
		rel := strings.TrimPrefix(b.Pathname, dirPrefix)
		if rel == "" {
			continue
		}

		// Check if this is a nested entry (contains /)
		if idx := strings.Index(rel, "/"); idx >= 0 {
			// It's a subdirectory
			dirName := rel[:idx]
			if dirName != "" && !seenDirs[dirName] {
				seenDirs[dirName] = true
				entries = append(entries, rawfs.DirEntry{
					Name:  dirName,
					IsDir: true,
					Size:  0,
				})
			}
		} else {
			// It's a direct child file
			entries = append(entries, rawfs.DirEntry{
				Name:  rel,
				IsDir: false,
				Size:  b.Size,
			})
		}
	}

	return entries, nil
}

// Open returns a seekable reader for a Vercel Blob object.
// Small files are buffered in memory; large files use a temp file.
func (f *FS) Open(relPath string) (rawfs.ReadSeekCloser, *rawfs.FileInfo, error) {
	fullPath := f.fullPath(relPath)

	// Find the blob to get its URL
	blobs, err := f.listBlobs(fullPath)
	if err != nil {
		return nil, nil, err
	}

	var blob *blobEntry
	for i := range blobs {
		if blobs[i].Pathname == fullPath {
			blob = &blobs[i]
			break
		}
	}

	if blob == nil {
		return nil, nil, fmt.Errorf("not found: %s: %w", relPath, os.ErrNotExist)
	}

	fi := &rawfs.FileInfo{
		Name:    path.Base(fullPath),
		Size:    blob.Size,
		IsDir:   false,
		ModTime: blob.UploadedAt,
	}

	// Download the blob content
	req, err := http.NewRequest(http.MethodGet, blob.URL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("vercel-blob: creating download request: %w", err)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("vercel-blob: downloading blob: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("vercel-blob: download returned status %d", resp.StatusCode)
	}

	if blob.Size <= inMemoryThreshold {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, nil, fmt.Errorf("vercel-blob: reading blob: %w", err)
		}
		return &memReadSeekCloser{Reader: bytes.NewReader(data)}, fi, nil
	}

	// Large file: download to temp
	tmpFile, err := os.CreateTemp("", "pika-vercel-blob-*")
	if err != nil {
		return nil, nil, fmt.Errorf("vercel-blob: creating temp file: %w", err)
	}

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return nil, nil, fmt.Errorf("vercel-blob: downloading to temp file: %w", err)
	}

	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return nil, nil, fmt.Errorf("vercel-blob: seeking temp file: %w", err)
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

// Ensure FS implements RawFS at compile time.
var _ rawfs.RawFS = (*FS)(nil)

// Ensure readers implement ReadSeekCloser.
var _ rawfs.ReadSeekCloser = (*memReadSeekCloser)(nil)
var _ rawfs.ReadSeekCloser = (*tempFileReadSeekCloser)(nil)
