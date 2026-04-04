// Package s3fs implements rawfs.WritableRawFS for S3-compatible storage.
package s3fs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/rakunlabs/pika/internal/rawfs"
)

const (
	// Files smaller than this are buffered in memory for seeking.
	inMemoryThreshold = 32 * 1024 * 1024 // 32 MB
)

func init() {
	rawfs.NewS3FSFunc = New
}

// FS implements rawfs.WritableRawFS for S3-compatible storage.
type FS struct {
	client *minio.Client
	bucket string
	prefix string // key prefix within bucket (no trailing slash)
}

// New creates a new S3 filesystem backend.
func New(bucket, region, endpoint, accessKey, secretKey, prefix string, pathStyle bool, secure *bool) (rawfs.RawFS, error) {
	if bucket == "" {
		return nil, fmt.Errorf("s3: bucket is required")
	}
	if region == "" {
		region = "us-east-1"
	}

	useSSL := true
	if secure != nil {
		useSSL = *secure
	}

	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: region,
	}

	if pathStyle {
		opts.BucketLookup = minio.BucketLookupPath
	}

	resolvedEndpoint := endpoint
	if resolvedEndpoint == "" {
		resolvedEndpoint = "s3.amazonaws.com"
	}

	client, err := minio.New(resolvedEndpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("s3: creating client: %w", err)
	}

	// Normalize prefix: remove leading/trailing slashes
	prefix = strings.Trim(prefix, "/")

	return &FS{
		client: client,
		bucket: bucket,
		prefix: prefix,
	}, nil
}

// fullKey returns the S3 key for a relative path.
func (f *FS) fullKey(relPath string) string {
	relPath = strings.Trim(relPath, "/")
	if f.prefix == "" {
		return relPath
	}
	if relPath == "" {
		return f.prefix
	}
	return f.prefix + "/" + relPath
}

// Stat returns metadata about a file or "directory" in S3.
func (f *FS) Stat(relPath string) (*rawfs.FileInfo, error) {
	ctx := context.Background()
	key := f.fullKey(relPath)

	// Check if it's an object
	if key != "" {
		objInfo, err := f.client.StatObject(ctx, f.bucket, key, minio.StatObjectOptions{})
		if err == nil {
			return &rawfs.FileInfo{
				Name:    path.Base(key),
				Size:    objInfo.Size,
				IsDir:   false,
				ModTime: objInfo.LastModified,
			}, nil
		}
	}

	// Check if it's a "directory" (has objects with this prefix)
	dirPrefix := key
	if dirPrefix != "" && !strings.HasSuffix(dirPrefix, "/") {
		dirPrefix += "/"
	}

	objectsCh := f.client.ListObjects(ctx, f.bucket, minio.ListObjectsOptions{
		Prefix:  dirPrefix,
		MaxKeys: 1,
	})

	for range objectsCh {
		name := path.Base(strings.TrimSuffix(key, "/"))
		if name == "" || name == "." {
			name = f.bucket
		}
		return &rawfs.FileInfo{
			Name:  name,
			IsDir: true,
		}, nil
	}

	return nil, fmt.Errorf("not found: %s: %w", relPath, os.ErrNotExist)
}

// ReadDir lists entries in a "directory" in S3.
func (f *FS) ReadDir(relPath string) ([]rawfs.DirEntry, error) {
	ctx := context.Background()
	dirPrefix := f.fullKey(relPath)
	if dirPrefix != "" && !strings.HasSuffix(dirPrefix, "/") {
		dirPrefix += "/"
	}

	var entries []rawfs.DirEntry
	seenDirs := make(map[string]bool)

	objectsCh := f.client.ListObjects(ctx, f.bucket, minio.ListObjectsOptions{
		Prefix:    dirPrefix,
		Recursive: false,
	})

	for obj := range objectsCh {
		if obj.Err != nil {
			return nil, fmt.Errorf("s3: listing objects: %w", obj.Err)
		}

		// Remove the directory prefix to get the relative name
		name := strings.TrimPrefix(obj.Key, dirPrefix)

		if strings.HasSuffix(name, "/") {
			// Directory
			dirName := strings.TrimSuffix(name, "/")
			if dirName != "" && !seenDirs[dirName] {
				seenDirs[dirName] = true
				entries = append(entries, rawfs.DirEntry{
					Name:  dirName,
					IsDir: true,
					Size:  0,
				})
			}
		} else if name != "" {
			entries = append(entries, rawfs.DirEntry{
				Name:  name,
				IsDir: false,
				Size:  obj.Size,
			})
		}
	}

	return entries, nil
}

// Open returns a seekable reader for an S3 object.
// Small files are buffered in memory; large files use a temp file.
func (f *FS) Open(relPath string) (rawfs.ReadSeekCloser, *rawfs.FileInfo, error) {
	ctx := context.Background()
	key := f.fullKey(relPath)

	obj, err := f.client.GetObject(ctx, f.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("s3: getting object: %w", err)
	}

	objInfo, err := obj.Stat()
	if err != nil {
		obj.Close()
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "NoSuchKey" {
			return nil, nil, fmt.Errorf("not found: %s: %w", relPath, os.ErrNotExist)
		}
		return nil, nil, fmt.Errorf("s3: stat object: %w", err)
	}

	fi := &rawfs.FileInfo{
		Name:    path.Base(key),
		Size:    objInfo.Size,
		IsDir:   false,
		ModTime: objInfo.LastModified,
	}

	if objInfo.Size <= inMemoryThreshold {
		// Buffer in memory
		data, err := io.ReadAll(obj)
		obj.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("s3: reading object: %w", err)
		}
		return &memReadSeekCloser{Reader: bytes.NewReader(data)}, fi, nil
	}

	// Download to temp file
	tmpFile, err := os.CreateTemp("", "pika-s3-*")
	if err != nil {
		obj.Close()
		return nil, nil, fmt.Errorf("s3: creating temp file: %w", err)
	}

	if _, err := io.Copy(tmpFile, obj); err != nil {
		obj.Close()
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return nil, nil, fmt.Errorf("s3: downloading to temp file: %w", err)
	}
	obj.Close()

	// Seek to beginning
	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return nil, nil, fmt.Errorf("s3: seeking temp file: %w", err)
	}

	return &tempFileReadSeekCloser{File: tmpFile}, fi, nil
}

// Write creates or overwrites an object in S3.
func (f *FS) Write(relPath string, r io.Reader, size int64) error {
	ctx := context.Background()
	key := f.fullKey(relPath)

	putOpts := minio.PutObjectOptions{}

	_, err := f.client.PutObject(ctx, f.bucket, key, r, size, putOpts)
	if err != nil {
		return fmt.Errorf("s3: putting object: %w", err)
	}

	return nil
}

// Delete removes an object from S3.
func (f *FS) Delete(relPath string) error {
	ctx := context.Background()
	key := f.fullKey(relPath)

	err := f.client.RemoveObject(ctx, f.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("s3: removing object: %w", err)
	}

	return nil
}

// MkDir creates a "directory" in S3 (zero-byte object with trailing slash).
func (f *FS) MkDir(relPath string) error {
	ctx := context.Background()
	key := f.fullKey(relPath)
	if !strings.HasSuffix(key, "/") {
		key += "/"
	}

	_, err := f.client.PutObject(ctx, f.bucket, key, bytes.NewReader(nil), 0, minio.PutObjectOptions{})
	if err != nil {
		return fmt.Errorf("s3: creating directory marker: %w", err)
	}

	return nil
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

// Copy copies an object within the same bucket using server-side copy (no re-download).
func (f *FS) Copy(srcPath, dstPath string) error {
	ctx := context.Background()
	srcKey := f.fullKey(srcPath)
	dstKey := f.fullKey(dstPath)

	src := minio.CopySrcOptions{
		Bucket: f.bucket,
		Object: srcKey,
	}
	dst := minio.CopyDestOptions{
		Bucket: f.bucket,
		Object: dstKey,
	}

	_, err := f.client.CopyObject(ctx, dst, src)
	if err != nil {
		return fmt.Errorf("s3: copying object: %w", err)
	}
	return nil
}

// Rename emulates rename by copying then deleting the source.
func (f *FS) Rename(oldPath, newPath string) error {
	if err := f.Copy(oldPath, newPath); err != nil {
		return err
	}
	return f.Delete(oldPath)
}

// Ensure FS implements WritableRawFS at compile time.
var _ rawfs.WritableRawFS = (*FS)(nil)
var _ rawfs.RenamableRawFS = (*FS)(nil)
var _ rawfs.CopyableRawFS = (*FS)(nil)

// Ensure readers implement ReadSeekCloser.
var _ rawfs.ReadSeekCloser = (*memReadSeekCloser)(nil)
var _ rawfs.ReadSeekCloser = (*tempFileReadSeekCloser)(nil)

// Ensure the init function is not optimized away.
var _ = time.Now
