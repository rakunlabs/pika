package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/rakunlabs/pika/internal/blobstore"
)

// ─── Settings ─────────────────────────────────────────────────────

const (
	VaultFilesBackendDisabled = ""
	VaultFilesBackendLocal    = "local"
	VaultFilesBackendS3       = "s3"
)

// VaultFilesSettings selects where personal-vault file uploads live.
// File metadata (names, folders, sizes) is always stored in the main
// database; only the bytes go to the configured backend.
type VaultFilesSettings struct {
	Backend string                  `json:"backend"`
	Local   VaultFilesLocalSettings `json:"local"`
	S3      VaultFilesS3Settings    `json:"s3"`
}

type VaultFilesLocalSettings struct {
	// Path is the directory on the server. Relative paths resolve
	// against the process working directory.
	Path string `json:"path"`
}

type VaultFilesS3Settings struct {
	Endpoint     string `json:"endpoint"`
	Region       string `json:"region,omitempty"`
	Bucket       string `json:"bucket"`
	Prefix       string `json:"prefix,omitempty"`
	AccessKeyID  string `json:"access_key_id"`
	UsePathStyle bool   `json:"use_path_style,omitempty"`
	// SecretAccessKey is sealed at rest and never returned by the API.
	SecretAccessKey string `json:"secret_access_key,omitempty"`
	// SecretAccessKeySet is a response-only indicator.
	SecretAccessKeySet bool `json:"secret_access_key_set,omitempty"`
	// ClearSecretAccessKey is a request-only flag that wipes the stored
	// secret instead of keeping it when SecretAccessKey is empty.
	ClearSecretAccessKey bool `json:"clear_secret_access_key,omitempty"`
}

// Enabled reports whether a backend is configured.
func (v *VaultFilesSettings) Enabled() bool {
	return v != nil && v.Backend != VaultFilesBackendDisabled
}

// Normalized trims fields and drops transient flags' side effects.
func (v VaultFilesSettings) Normalized() VaultFilesSettings {
	v.Backend = strings.TrimSpace(v.Backend)
	v.Local.Path = strings.TrimSpace(v.Local.Path)
	v.S3.Endpoint = strings.TrimSpace(v.S3.Endpoint)
	v.S3.Region = strings.TrimSpace(v.S3.Region)
	v.S3.Bucket = strings.TrimSpace(v.S3.Bucket)
	v.S3.Prefix = strings.TrimSpace(v.S3.Prefix)
	v.S3.AccessKeyID = strings.TrimSpace(v.S3.AccessKeyID)
	v.S3.SecretAccessKeySet = false
	return v
}

// preserveSecret implements "leave blank to keep" for the S3 secret.
func (v *VaultFilesSettings) preserveSecret(old *VaultFilesSettings) {
	clear := v.S3.ClearSecretAccessKey
	v.S3.ClearSecretAccessKey = false
	if v.S3.SecretAccessKey != "" || clear || old == nil {
		return
	}
	v.S3.SecretAccessKey = old.S3.SecretAccessKey
}

// Validate checks the backend-specific fields.
func (v VaultFilesSettings) Validate() error {
	switch v.Backend {
	case VaultFilesBackendDisabled:
		return nil
	case VaultFilesBackendLocal:
		if v.Local.Path == "" {
			return fmt.Errorf("vault files: local path is required: %w", ErrBadRequest)
		}
		return nil
	case VaultFilesBackendS3:
		u, err := url.Parse(v.S3.Endpoint)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("vault files: s3 endpoint must be an absolute http(s) URL: %w", ErrBadRequest)
		}
		if v.S3.Bucket == "" {
			return fmt.Errorf("vault files: s3 bucket is required: %w", ErrBadRequest)
		}
		if v.S3.AccessKeyID == "" || v.S3.SecretAccessKey == "" {
			return fmt.Errorf("vault files: s3 access key id and secret access key are required: %w", ErrBadRequest)
		}
		return nil
	default:
		return fmt.Errorf("vault files: unknown backend %q: %w", v.Backend, ErrBadRequest)
	}
}

// Open builds the blob store for these settings.
func (v VaultFilesSettings) Open() (blobstore.Store, error) {
	switch v.Backend {
	case VaultFilesBackendLocal:
		return blobstore.NewLocal(v.Local.Path)
	case VaultFilesBackendS3:
		return blobstore.NewS3(blobstore.S3Config{
			Endpoint:        v.S3.Endpoint,
			Region:          v.S3.Region,
			Bucket:          v.S3.Bucket,
			Prefix:          v.S3.Prefix,
			AccessKeyID:     v.S3.AccessKeyID,
			SecretAccessKey: v.S3.SecretAccessKey,
			UsePathStyle:    v.S3.UsePathStyle,
		})
	default:
		return nil, ErrVaultFilesDisabled
	}
}

// MaskSecrets replaces the stored secret with the "is set" indicator.
// Operates on the per-request copy returned by Settings().
func (v *VaultFilesSettings) MaskSecrets() {
	if v == nil {
		return
	}
	v.S3.SecretAccessKeySet = v.S3.SecretAccessKey != ""
	v.S3.SecretAccessKey = ""
}

// ─── Model ────────────────────────────────────────────────────────

var (
	ErrVaultFilesDisabled = errors.New("vault file storage is not configured")
	// ErrVaultFileBackendMismatch is returned when a file was written to
	// a different backend than the one currently configured.
	ErrVaultFileBackendMismatch = errors.New("file belongs to a different storage backend")
)

// VaultFile is a node in a user's vault file tree: either a folder
// (IsDir) or a file whose bytes live in the blob store under
// StorageKey. Keys are immutable and id-based, so rename and move are
// metadata-only operations.
type VaultFile struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	ParentID    string    `json:"parent_id"`
	Name        string    `json:"name"`
	IsDir       bool      `json:"is_dir"`
	Size        int64     `json:"size"`
	ContentType string    `json:"content_type,omitempty"`
	StorageKey  string    `json:"-"`
	Backend     string    `json:"backend,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// VaultFileStorage persists VaultFile metadata.
type VaultFileStorage interface {
	Create(ctx context.Context, f *VaultFile) error
	Get(ctx context.Context, userID, id string) (*VaultFile, error)
	ListByUser(ctx context.Context, userID string) ([]VaultFile, error)
	ListChildren(ctx context.Context, userID, parentID string) ([]VaultFile, error)
	Update(ctx context.Context, f *VaultFile) error
	Delete(ctx context.Context, userID, id string) error
	DeleteAllByUser(ctx context.Context, userID string) error
}

// ─── Service ──────────────────────────────────────────────────────

const vaultFileNameMax = 255

// vaultTreeMu serializes tree mutations (folder creation, placement,
// rename/move). Badger transactions don't detect phantom inserts, so
// two concurrent uploads into the same new folder path could otherwise
// both create it. Writes always run on one node (cluster followers
// forward them to the leader), so a process-local lock is sufficient.
var vaultTreeMu sync.Mutex

func (s *Service) vaultTreeTx(ctx context.Context, fn func(ctx context.Context, tx Storage) error) error {
	vaultTreeMu.Lock()
	defer vaultTreeMu.Unlock()
	return s.store.Tx(ctx, fn)
}

// VaultFilesInfo is returned with the listing so the SPA knows whether
// uploads are possible.
type VaultFilesInfo struct {
	Enabled bool        `json:"enabled"`
	Backend string      `json:"backend,omitempty"`
	Files   []VaultFile `json:"files"`
}

func (s *Service) vaultFilesSettings(ctx context.Context) (*VaultFilesSettings, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	if !settings.VaultFiles.Enabled() {
		return nil, ErrVaultFilesDisabled
	}
	return settings.VaultFiles, nil
}

func (s *Service) vaultFilesStore(ctx context.Context) (blobstore.Store, string, error) {
	cfg, err := s.vaultFilesSettings(ctx)
	if err != nil {
		return nil, "", err
	}
	store, err := cfg.Open()
	if err != nil {
		return nil, "", fmt.Errorf("vault files: storage misconfigured: %w", err)
	}
	return store, cfg.Backend, nil
}

// TestVaultFilesSettings probes a (possibly unsaved) configuration. An
// empty S3 secret falls back to the stored one.
func (s *Service) TestVaultFilesSettings(ctx context.Context, cfg VaultFilesSettings) error {
	cfg = cfg.Normalized()
	if settings, err := s.Settings(ctx); err == nil {
		cfg.preserveSecret(settings.VaultFiles)
	}
	if cfg.Backend == VaultFilesBackendDisabled {
		return fmt.Errorf("select a storage backend first: %w", ErrBadRequest)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	store, err := cfg.Open()
	if err != nil {
		return fmt.Errorf("%w: %w", err, ErrBadRequest)
	}
	return store.Check(ctx)
}

// ListVaultFiles returns every node the user owns plus storage status.
func (s *Service) ListVaultFiles(ctx context.Context, userID string) (*VaultFilesInfo, error) {
	out := &VaultFilesInfo{Files: []VaultFile{}}
	if cfg, err := s.vaultFilesSettings(ctx); err == nil {
		out.Enabled = true
		out.Backend = cfg.Backend
	} else if !errors.Is(err, ErrVaultFilesDisabled) {
		return nil, err
	}
	files, err := s.store.VaultFiles().ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if files != nil {
		out.Files = files
	}
	return out, nil
}

// GetVaultFile returns a single node owned by the user.
func (s *Service) GetVaultFile(ctx context.Context, userID, id string) (*VaultFile, error) {
	return s.store.VaultFiles().Get(ctx, userID, id)
}

func validateVaultFileName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "" || name == "." || name == "..":
		return "", fmt.Errorf("invalid name: %w", ErrBadRequest)
	case len(name) > vaultFileNameMax:
		return "", fmt.Errorf("name is too long: %w", ErrBadRequest)
	case !utf8.ValidString(name):
		return "", fmt.Errorf("name must be valid UTF-8: %w", ErrBadRequest)
	case strings.ContainsAny(name, "/\\"):
		return "", fmt.Errorf("name must not contain slashes: %w", ErrBadRequest)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("name contains control characters: %w", ErrBadRequest)
		}
	}
	return name, nil
}

// splitVaultRelPath splits "a/b/c" into validated segments.
func splitVaultRelPath(p string) ([]string, error) {
	p = strings.Trim(strings.ReplaceAll(p, "\\", "/"), "/")
	if p == "" {
		return nil, nil
	}
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		name, err := validateVaultFileName(part)
		if err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, nil
}

// checkParent verifies parentID ("" = root) is a folder owned by user.
func checkVaultParent(ctx context.Context, tx Storage, userID, parentID string) error {
	if parentID == "" {
		return nil
	}
	p, err := tx.VaultFiles().Get(ctx, userID, parentID)
	if err != nil {
		return err
	}
	if !p.IsDir {
		return fmt.Errorf("parent is not a folder: %w", ErrBadRequest)
	}
	return nil
}

func findChild(children []VaultFile, name string) *VaultFile {
	for i := range children {
		if strings.EqualFold(children[i].Name, name) {
			return &children[i]
		}
	}
	return nil
}

// ensureVaultFolderPath walks/creates segments below parentID and
// returns the id of the deepest folder. Must run inside a tx.
func ensureVaultFolderPath(ctx context.Context, tx Storage, userID, parentID string, segments []string) (string, error) {
	cur := parentID
	now := time.Now().UTC()
	for _, seg := range segments {
		children, err := tx.VaultFiles().ListChildren(ctx, userID, cur)
		if err != nil {
			return "", err
		}
		if existing := findChild(children, seg); existing != nil {
			if !existing.IsDir {
				return "", fmt.Errorf("%q exists and is not a folder: %w", seg, ErrConflict)
			}
			cur = existing.ID
			continue
		}
		f := &VaultFile{
			ID: newItemID(), UserID: userID, ParentID: cur, Name: seg, IsDir: true,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.VaultFiles().Create(ctx, f); err != nil {
			return "", err
		}
		cur = f.ID
	}
	return cur, nil
}

// uniqueVaultName returns name, or "name (n).ext" if taken.
func uniqueVaultName(children []VaultFile, name string) string {
	if findChild(children, name) == nil {
		return name
	}
	base, ext := name, ""
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		base, ext = name[:i], name[i:]
	}
	for n := 1; ; n++ {
		cand := fmt.Sprintf("%s (%d)%s", base, n, ext)
		if findChild(children, cand) == nil {
			return cand
		}
	}
}

// CreateVaultFolder creates a folder (and any missing intermediate
// folders when name contains slashes).
func (s *Service) CreateVaultFolder(ctx context.Context, userID, parentID, name string) (*VaultFile, error) {
	segments, err := splitVaultRelPath(name)
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("folder name is required: %w", ErrBadRequest)
	}
	var id string
	err = s.vaultTreeTx(ctx, func(ctx context.Context, tx Storage) error {
		if err := checkVaultParent(ctx, tx, userID, parentID); err != nil {
			return err
		}
		id, err = ensureVaultFolderPath(ctx, tx, userID, parentID, segments)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.store.VaultFiles().Get(ctx, userID, id)
}

// VaultUploadRequest describes a streamed upload.
type VaultUploadRequest struct {
	ParentID string
	// RelDir is an optional "a/b" folder path below ParentID that is
	// created on demand (folder drag-and-drop).
	RelDir      string
	Name        string
	Size        int64
	ContentType string
	// Replace overwrites an existing file with the same name instead of
	// auto-renaming the upload.
	Replace bool
	Body    io.Reader
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// UploadVaultFile streams the body to the blob store and records it.
func (s *Service) UploadVaultFile(ctx context.Context, userID string, req VaultUploadRequest) (*VaultFile, error) {
	name, err := validateVaultFileName(req.Name)
	if err != nil {
		return nil, err
	}
	segments, err := splitVaultRelPath(req.RelDir)
	if err != nil {
		return nil, err
	}
	if req.Size < 0 {
		return nil, fmt.Errorf("upload size (Content-Length) is required: %w", ErrBadRequest)
	}
	store, backend, err := s.vaultFilesStore(ctx)
	if err != nil {
		return nil, err
	}

	// Resolve the target folder first so a bad parent fails fast,
	// before any bytes are transferred.
	var parentID string
	if err := s.vaultTreeTx(ctx, func(ctx context.Context, tx Storage) error {
		if err := checkVaultParent(ctx, tx, userID, req.ParentID); err != nil {
			return err
		}
		parentID, err = ensureVaultFolderPath(ctx, tx, userID, req.ParentID, segments)
		return err
	}); err != nil {
		return nil, err
	}

	id := newItemID()
	key := "vault/" + userID + "/" + id
	contentType := strings.TrimSpace(req.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	cr := &countingReader{r: req.Body}
	if err := store.Put(ctx, key, cr, req.Size, contentType); err != nil {
		_ = store.Delete(context.WithoutCancel(ctx), key)
		return nil, fmt.Errorf("store file: %w", err)
	}
	if cr.n != req.Size {
		_ = store.Delete(context.WithoutCancel(ctx), key)
		return nil, fmt.Errorf("upload truncated (%d of %d bytes): %w", cr.n, req.Size, ErrBadRequest)
	}

	now := time.Now().UTC()
	file := &VaultFile{
		ID: id, UserID: userID, ParentID: parentID, Name: name,
		Size: req.Size, ContentType: contentType, StorageKey: key, Backend: backend,
		CreatedAt: now, UpdatedAt: now,
	}
	var replaced *VaultFile
	err = s.vaultTreeTx(ctx, func(ctx context.Context, tx Storage) error {
		children, err := tx.VaultFiles().ListChildren(ctx, userID, parentID)
		if err != nil {
			return err
		}
		if existing := findChild(children, name); existing != nil {
			if req.Replace && !existing.IsDir {
				cp := *existing
				replaced = &cp
				if err := tx.VaultFiles().Delete(ctx, userID, existing.ID); err != nil {
					return err
				}
			} else {
				file.Name = uniqueVaultName(children, name)
			}
		}
		return tx.VaultFiles().Create(ctx, file)
	})
	if err != nil {
		_ = store.Delete(context.WithoutCancel(ctx), key)
		return nil, err
	}
	if replaced != nil {
		s.deleteVaultBlobs(ctx, []VaultFile{*replaced})
	}
	return file, nil
}

// OpenVaultFile returns the file metadata and a reader for its bytes.
func (s *Service) OpenVaultFile(ctx context.Context, userID, id string) (*VaultFile, io.ReadCloser, error) {
	f, err := s.store.VaultFiles().Get(ctx, userID, id)
	if err != nil {
		return nil, nil, err
	}
	if f.IsDir {
		return nil, nil, fmt.Errorf("cannot download a folder: %w", ErrBadRequest)
	}
	store, backend, err := s.vaultFilesStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	if f.Backend != "" && f.Backend != backend {
		return nil, nil, fmt.Errorf("%w (%s): %w", ErrVaultFileBackendMismatch, f.Backend, ErrConflict)
	}
	rc, err := store.Get(ctx, f.StorageKey)
	if err != nil {
		if errors.Is(err, blobstore.ErrNotFound) {
			return nil, nil, fmt.Errorf("file content missing from storage: %w", ErrNotFound)
		}
		return nil, nil, err
	}
	return f, rc, nil
}

// VaultContentRequest replaces the bytes of an existing file (in-browser
// text editor saves).
type VaultContentRequest struct {
	// ExpectedUpdatedAt is the optimistic-concurrency token: the save is
	// rejected with ErrConflict when the file changed since it was
	// loaded. Zero skips the check.
	ExpectedUpdatedAt time.Time
	Size              int64
	ContentType       string
	Body              io.Reader
}

// WriteVaultFileContent stores new content under a fresh blob key and
// swaps the metadata pointer, so a concurrent or failed save never
// corrupts the current version. The previous blob is removed after the
// swap.
func (s *Service) WriteVaultFileContent(ctx context.Context, userID, id string, req VaultContentRequest) (*VaultFile, error) {
	if req.Size < 0 {
		return nil, fmt.Errorf("content size (Content-Length) is required: %w", ErrBadRequest)
	}
	cur, err := s.store.VaultFiles().Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if cur.IsDir {
		return nil, fmt.Errorf("cannot write content to a folder: %w", ErrBadRequest)
	}
	if !req.ExpectedUpdatedAt.IsZero() && !cur.UpdatedAt.Equal(req.ExpectedUpdatedAt) {
		return nil, fmt.Errorf("file was modified since it was opened: %w", ErrConflict)
	}
	store, backend, err := s.vaultFilesStore(ctx)
	if err != nil {
		return nil, err
	}

	key := "vault/" + userID + "/" + id + "." + newItemID()[:12]
	contentType := strings.TrimSpace(req.ContentType)
	if contentType == "" {
		contentType = cur.ContentType
	}
	cr := &countingReader{r: req.Body}
	if err := store.Put(ctx, key, cr, req.Size, contentType); err != nil {
		_ = store.Delete(context.WithoutCancel(ctx), key)
		return nil, fmt.Errorf("store file: %w", err)
	}
	if cr.n != req.Size {
		_ = store.Delete(context.WithoutCancel(ctx), key)
		return nil, fmt.Errorf("upload truncated (%d of %d bytes): %w", cr.n, req.Size, ErrBadRequest)
	}

	var old VaultFile
	var out *VaultFile
	err = s.vaultTreeTx(ctx, func(ctx context.Context, tx Storage) error {
		f, err := tx.VaultFiles().Get(ctx, userID, id)
		if err != nil {
			return err
		}
		if !req.ExpectedUpdatedAt.IsZero() && !f.UpdatedAt.Equal(req.ExpectedUpdatedAt) {
			return fmt.Errorf("file was modified since it was opened: %w", ErrConflict)
		}
		old = *f
		f.StorageKey, f.Backend = key, backend
		f.Size, f.ContentType = req.Size, contentType
		f.UpdatedAt = time.Now().UTC()
		if err := tx.VaultFiles().Update(ctx, f); err != nil {
			return err
		}
		out = f
		return nil
	})
	if err != nil {
		_ = store.Delete(context.WithoutCancel(ctx), key)
		return nil, err
	}
	s.deleteVaultBlobs(ctx, []VaultFile{old})
	return out, nil
}

// VaultFilePatch renames and/or moves a node. Nil fields are untouched;
// ParentID "" (non-nil) moves to the root.
type VaultFilePatch struct {
	Name     *string `json:"name,omitempty"`
	ParentID *string `json:"parent_id,omitempty"`
}

// UpdateVaultFile renames/moves a node. Moving a folder into itself or
// one of its descendants is rejected.
func (s *Service) UpdateVaultFile(ctx context.Context, userID, id string, patch VaultFilePatch) (*VaultFile, error) {
	var out *VaultFile
	err := s.vaultTreeTx(ctx, func(ctx context.Context, tx Storage) error {
		f, err := tx.VaultFiles().Get(ctx, userID, id)
		if err != nil {
			return err
		}
		name, parent := f.Name, f.ParentID
		if patch.Name != nil {
			if name, err = validateVaultFileName(*patch.Name); err != nil {
				return err
			}
		}
		if patch.ParentID != nil {
			parent = *patch.ParentID
		}
		if parent != f.ParentID {
			if err := checkVaultParent(ctx, tx, userID, parent); err != nil {
				return err
			}
			if f.IsDir {
				// Walk up from the destination; hitting f means a cycle.
				for cur := parent; cur != ""; {
					if cur == f.ID {
						return fmt.Errorf("cannot move a folder into itself: %w", ErrBadRequest)
					}
					p, err := tx.VaultFiles().Get(ctx, userID, cur)
					if err != nil {
						return err
					}
					cur = p.ParentID
				}
			}
		}
		if name == f.Name && parent == f.ParentID {
			out = f
			return nil
		}
		children, err := tx.VaultFiles().ListChildren(ctx, userID, parent)
		if err != nil {
			return err
		}
		for _, c := range children {
			if c.ID != f.ID && strings.EqualFold(c.Name, name) {
				return fmt.Errorf("%q already exists in the destination: %w", name, ErrConflict)
			}
		}
		f.Name, f.ParentID, f.UpdatedAt = name, parent, time.Now().UTC()
		if err := tx.VaultFiles().Update(ctx, f); err != nil {
			return err
		}
		out = f
		return nil
	})
	return out, err
}

// DeleteVaultFile deletes a node; folders are deleted recursively.
// Returns the number of nodes removed.
func (s *Service) DeleteVaultFile(ctx context.Context, userID, id string) (int, error) {
	var removed []VaultFile
	err := s.vaultTreeTx(ctx, func(ctx context.Context, tx Storage) error {
		root, err := tx.VaultFiles().Get(ctx, userID, id)
		if err != nil {
			return err
		}
		all, err := tx.VaultFiles().ListByUser(ctx, userID)
		if err != nil {
			return err
		}
		byParent := make(map[string][]VaultFile)
		for _, f := range all {
			byParent[f.ParentID] = append(byParent[f.ParentID], f)
		}
		stack := []VaultFile{*root}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			removed = append(removed, n)
			if n.IsDir {
				stack = append(stack, byParent[n.ID]...)
			}
		}
		for _, f := range removed {
			if err := tx.VaultFiles().Delete(ctx, userID, f.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	s.deleteVaultBlobs(ctx, removed)
	return len(removed), nil
}

// deleteVaultBlobs removes blob content best-effort. Metadata is the
// source of truth, so a failure here only leaves an orphaned object.
func (s *Service) deleteVaultBlobs(ctx context.Context, files []VaultFile) {
	ctx = context.WithoutCancel(ctx)
	store, backend, err := s.vaultFilesStore(ctx)
	if err != nil {
		return
	}
	for _, f := range files {
		if f.IsDir || f.StorageKey == "" || (f.Backend != "" && f.Backend != backend) {
			continue
		}
		_ = store.Delete(ctx, f.StorageKey)
	}
}

// PurgeUserVaultFiles drops every file node and blob for a user. Used
// by vault reset and user deletion.
func (s *Service) PurgeUserVaultFiles(ctx context.Context, userID string) error {
	files, err := s.store.VaultFiles().ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.store.VaultFiles().DeleteAllByUser(ctx, userID); err != nil {
		return err
	}
	s.deleteVaultBlobs(ctx, files)
	return nil
}
