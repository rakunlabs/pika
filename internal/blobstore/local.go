package blobstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const localTempPrefix = ".pika-tmp-"

// Local stores objects under a directory on the server. Every path
// operation goes through os.Root, which guarantees a crafted key cannot
// resolve outside the root (including via symlinks).
type Local struct {
	root string
}

// NewLocal returns a local-disk store rooted at dir. The directory is
// created on first use.
func NewLocal(dir string) (*Local, error) {
	if dir == "" {
		return nil, errors.New("local storage path is required")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve local storage path: %w", err)
	}
	return &Local{root: abs}, nil
}

func (l *Local) open() (*os.Root, error) {
	if err := os.MkdirAll(l.root, 0o700); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	root, err := os.OpenRoot(l.root)
	if err != nil {
		return nil, fmt.Errorf("open storage root: %w", err)
	}
	return root, nil
}

// Put writes atomically: the payload lands in a temporary file next to
// the target and is renamed over it, so readers never observe a partial
// object and a failed write leaves nothing behind.
func (l *Local) Put(ctx context.Context, key string, r io.Reader, _ int64, _ string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	root, err := l.open()
	if err != nil {
		return err
	}
	defer root.Close()

	dir := path.Dir(key)
	if dir != "." {
		if err := root.MkdirAll(filepath.FromSlash(dir), 0o700); err != nil {
			return fmt.Errorf("create object directory: %w", err)
		}
	}
	temp := filepath.FromSlash(path.Join(dir, localTempPrefix+randomSuffix()))
	f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	_, werr := io.Copy(f, ctxReader{ctx: ctx, r: r})
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = root.Remove(temp)
		return fmt.Errorf("write object: %w", err)
	}
	if err := root.Rename(temp, filepath.FromSlash(key)); err != nil {
		_ = root.Remove(temp)
		return fmt.Errorf("commit object: %w", err)
	}
	return nil
}

// Get returns an *os.File, which implements io.ReadSeeker.
func (l *Local) Get(_ context.Context, key string) (io.ReadCloser, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	root, err := l.open()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(filepath.FromSlash(key))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("open object: %w", err)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("stat object: %w", err)
		}
		return nil, fmt.Errorf("object %q is not a regular file", key)
	}
	return f, nil
}

func (l *Local) Delete(_ context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	root, err := l.open()
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(filepath.FromSlash(key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

// Check writes, reads back and removes a probe file. Stat alone would not
// catch the common misconfiguration of a read-only or foreign-owned root.
func (l *Local) Check(ctx context.Context) error {
	key := localTempPrefix + "probe-" + randomSuffix()
	want := "pika-storage-probe"
	if err := l.Put(ctx, key, strings.NewReader(want), int64(len(want)), ""); err != nil {
		return err
	}
	defer l.Delete(ctx, key) //nolint:errcheck // best effort
	rc, err := l.Get(ctx, key)
	if err != nil {
		return err
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		return fmt.Errorf("read probe: %w", err)
	}
	if string(got) != want {
		return errors.New("storage probe read back different content")
	}
	return nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func randomSuffix() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Errorf("blobstore: rng failure: %w", err))
	}
	return hex.EncodeToString(b[:])
}
