package service

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/rakunlabs/pika/internal/blobstore"
)

// VaultZip is a validated folder download, ready to stream. Splitting
// preparation from writing lets the HTTP handler fail with a proper
// status code before the zip stream commits the response.
type VaultZip struct {
	// Name is the suggested archive base name (folder name, or
	// "vault-files" for the root).
	Name    string
	entries []vaultZipEntry
	store   blobstore.Store
	backend string
}

type vaultZipEntry struct {
	path string // slash-separated, "/"-suffixed for folders
	file VaultFile
}

// VaultZipStats summarizes a finished archive.
type VaultZipStats struct {
	Files  int
	Failed int
}

// PrepareVaultZip collects the subtree under id ("" = every file the
// user owns). Entries are prefixed with the folder's own name so the
// archive extracts into a single directory.
func (s *Service) PrepareVaultZip(ctx context.Context, userID, id string) (*VaultZip, error) {
	z := &VaultZip{Name: "vault-files"}
	prefix := ""
	if id != "" {
		root, err := s.store.VaultFiles().Get(ctx, userID, id)
		if err != nil {
			return nil, err
		}
		if !root.IsDir {
			return nil, fmt.Errorf("only folders can be downloaded as zip: %w", ErrBadRequest)
		}
		z.Name = root.Name
		prefix = root.Name + "/"
	}
	store, backend, err := s.vaultFilesStore(ctx)
	if err != nil {
		return nil, err
	}
	z.store, z.backend = store, backend

	all, err := s.store.VaultFiles().ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	byParent := make(map[string][]VaultFile)
	for _, f := range all {
		byParent[f.ParentID] = append(byParent[f.ParentID], f)
	}
	for _, list := range byParent {
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	}

	var walk func(parent, dir string)
	walk = func(parent, dir string) {
		for _, f := range byParent[parent] {
			p := dir + f.Name
			if f.IsDir {
				z.entries = append(z.entries, vaultZipEntry{path: p + "/", file: f})
				walk(f.ID, p+"/")
				continue
			}
			z.entries = append(z.entries, vaultZipEntry{path: p, file: f})
		}
	}
	if prefix != "" {
		z.entries = append(z.entries, vaultZipEntry{path: prefix})
	}
	walk(id, prefix)
	return z, nil
}

// WriteTo streams the archive into w. A file whose content can't be
// read is skipped and listed in _errors.txt so a partial archive is
// never mistaken for a complete one. A returned error means the writer
// failed or ctx was cancelled.
func (z *VaultZip) WriteTo(ctx context.Context, w io.Writer) (VaultZipStats, error) {
	var stats VaultZipStats
	var errs []string
	zw := zip.NewWriter(w)

	for _, e := range z.entries {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		modified := e.file.UpdatedAt
		if modified.IsZero() {
			modified = time.Now()
		}
		if strings.HasSuffix(e.path, "/") {
			if _, err := zw.CreateHeader(&zip.FileHeader{Name: e.path, Modified: modified}); err != nil {
				return stats, fmt.Errorf("creating zip folder %q: %w", e.path, err)
			}
			continue
		}
		if err := z.copyFile(ctx, zw, e.path, e.file, modified); err != nil {
			var skip *vaultZipSkip
			if errors.As(err, &skip) {
				stats.Failed++
				errs = append(errs, fmt.Sprintf("%s: %s", e.path, skip.reason))
				continue
			}
			return stats, err
		}
		stats.Files++
	}

	if len(errs) > 0 {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: "_errors.txt", Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			return stats, fmt.Errorf("creating _errors.txt: %w", err)
		}
		body := fmt.Sprintf("%d file(s) could not be included:\n\n%s\n", len(errs), strings.Join(errs, "\n"))
		if _, err := io.WriteString(f, body); err != nil {
			return stats, fmt.Errorf("writing _errors.txt: %w", err)
		}
	}
	if err := zw.Close(); err != nil {
		return stats, fmt.Errorf("finalizing zip archive: %w", err)
	}
	return stats, nil
}

// vaultZipSkip marks a per-file failure that must not abort the archive.
type vaultZipSkip struct{ reason string }

func (e *vaultZipSkip) Error() string { return e.reason }

func (z *VaultZip) copyFile(ctx context.Context, zw *zip.Writer, name string, f VaultFile, modified time.Time) error {
	if f.Backend != "" && f.Backend != z.backend {
		return &vaultZipSkip{reason: fmt.Sprintf("stored on a different backend (%s)", f.Backend)}
	}
	rc, err := z.store.Get(ctx, f.StorageKey)
	if err != nil {
		if errors.Is(err, blobstore.ErrNotFound) {
			return &vaultZipSkip{reason: "content missing from storage"}
		}
		return &vaultZipSkip{reason: err.Error()}
	}
	defer rc.Close()

	method := zip.Deflate
	if precompressed(f.ContentType) {
		method = zip.Store
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method, Modified: modified})
	if err != nil {
		return fmt.Errorf("creating zip entry %q: %w", name, err)
	}
	// A read failure mid-copy leaves a truncated entry that can't be
	// rolled back, so it aborts the archive instead of being skipped.
	if _, err := io.Copy(w, rc); err != nil {
		return fmt.Errorf("writing zip entry %q: %w", name, err)
	}
	return nil
}

// precompressed reports content types that gain nothing from deflate.
func precompressed(ct string) bool {
	switch {
	case strings.HasPrefix(ct, "image/") && !strings.HasPrefix(ct, "image/svg") && !strings.HasPrefix(ct, "image/bmp"),
		strings.HasPrefix(ct, "video/"), strings.HasPrefix(ct, "audio/"):
		return true
	}
	switch ct {
	case "application/zip", "application/gzip", "application/x-gzip", "application/x-7z-compressed",
		"application/x-rar-compressed", "application/vnd.rar", "application/x-xz", "application/zstd",
		"application/x-bzip2", "application/pdf":
		return true
	}
	return false
}
