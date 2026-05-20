package docker

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/registry/blobstore"
)

// Store wraps the rawfs handle + blobstore for one Docker registry
// repo. Manifests, tag pointers and (later) referrers indexes live
// on the rawfs directly (path-keyed); blob bytes go through the
// BlobStore so multipart-on-S3, atomic rename on local, etc. are
// uniform across backends.
//
// Concurrency: every method is safe for concurrent use. The
// rawfs and BlobStore primitives both serialize their own access.
type Store struct {
	fs       rawfs.RawFS
	blobs    blobstore.BlobStore
	basePath string
}

// NewStore wraps a rawfs + BlobStore in a Docker store. basePath is
// the prefix under the rawfs inside which manifests/ and
// repositories/ live; the BlobStore is expected to be rooted at the
// same basePath (so its blobs/ directory is a sibling).
func NewStore(fs rawfs.RawFS, blobs blobstore.BlobStore, basePath string) *Store {
	return &Store{
		fs:       fs,
		blobs:    blobs,
		basePath: strings.Trim(basePath, "/"),
	}
}

// RawFS returns the underlying rawfs handle.
func (s *Store) RawFS() rawfs.RawFS { return s.fs }

// CountRepositoriesTagsManifests walks the repositories/ tree once
// and returns (#repos, #tags across all repos, #manifest-by-digest
// files). Used by the stats endpoint; cheap because manifest files
// are small and the tree is flat (one repo = one tags/ + one
// manifests/ subdir).
func (s *Store) CountRepositoriesTagsManifests() (repos int, tags int, manifests int) {
	names, _ := s.ListRepositories()
	repos = len(names)
	for _, name := range names {
		tagsDir := path.Join(s.repoDir(name), "tags")
		if entries, err := s.fs.ReadDir(tagsDir); err == nil {
			for _, e := range entries {
				if !e.IsDir {
					tags++
				}
			}
		}
		manifestsDir := path.Join(s.repoDir(name), "manifests")
		if entries, err := s.fs.ReadDir(manifestsDir); err == nil {
			for _, e := range entries {
				if !e.IsDir {
					manifests++
				}
			}
		}
	}
	return
}

// Blobs returns the underlying BlobStore.
func (s *Store) Blobs() blobstore.BlobStore { return s.blobs }

func (s *Store) join(parts ...string) string {
	if s.basePath != "" {
		parts = append([]string{s.basePath}, parts...)
	}
	return path.Join(parts...)
}

func (s *Store) repoDir(name string) string {
	return s.join("repositories", name)
}

func (s *Store) manifestPath(name string, dgst blobstore.Digest) string {
	return path.Join(s.repoDir(name), "manifests", dgst.String()+".json")
}

func (s *Store) tagPath(name, tag string) string {
	return path.Join(s.repoDir(name), "tags", tag)
}

func (s *Store) uploadPath(name, uuid string) string {
	return path.Join(s.repoDir(name), "_uploads", uuid)
}

// referrersIndexPath is where the per-subject-digest referrers index
// file lives. One file per subject digest keeps the index O(1)
// read/write rather than scanning every manifest on demand.
func (s *Store) referrersIndexPath(name, subjectDigest string) string {
	return path.Join(s.repoDir(name), "_referrers", subjectDigest+".json")
}

// ─── Manifests ─────────────────────────────────────────────────────

// ManifestRecord pairs a manifest body with its content type. We
// store both so HEAD/GET can serve the right Content-Type without
// re-sniffing the bytes.
type ManifestRecord struct {
	Body        []byte
	ContentType string
	Digest      blobstore.Digest
}

// WriteManifest persists a manifest body keyed by its digest. The
// content type is stored alongside via a "{digest}.media" sidecar
// so we can recover it on GET. We could embed the media type in
// the manifest body itself (JSON peek), but the sidecar is cheaper
// and protocol-agnostic (works for OCI image manifest, image index,
// Helm OCI artifact, anything else with a custom mediaType).
func (s *Store) WriteManifest(name string, dgst blobstore.Digest, body []byte, contentType string) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("docker: backend read-only")
	}
	if err := wfs.Write(s.manifestPath(name, dgst), strings.NewReader(string(body)), int64(len(body))); err != nil {
		return fmt.Errorf("docker: write manifest: %w", err)
	}
	mediaPath := s.manifestPath(name, dgst) + ".media"
	if err := wfs.Write(mediaPath, strings.NewReader(contentType), int64(len(contentType))); err != nil {
		// Non-fatal: the manifest is still readable; the next GET
		// will fall back to a default content type.
		_ = err
	}
	return nil
}

// ReadManifest returns the manifest body + content-type for the
// given digest. Returns ErrManifestUnknown when missing.
func (s *Store) ReadManifest(name string, dgst blobstore.Digest) (*ManifestRecord, error) {
	rc, _, err := s.fs.Open(s.manifestPath(name, dgst))
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("%s@%s: %w", name, dgst, ErrManifestUnknown)
		}
		return nil, fmt.Errorf("docker: read manifest: %w", err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("docker: read manifest body: %w", err)
	}
	ct := "application/vnd.docker.distribution.manifest.v2+json"
	if mrc, _, err := s.fs.Open(s.manifestPath(name, dgst) + ".media"); err == nil {
		if m, err := io.ReadAll(mrc); err == nil && len(m) > 0 {
			ct = strings.TrimSpace(string(m))
		}
		mrc.Close()
	}
	return &ManifestRecord{Body: body, ContentType: ct, Digest: dgst}, nil
}

// DeleteManifest removes a manifest body + media sidecar.
// Idempotent on the sidecar (missing-is-not-an-error).
func (s *Store) DeleteManifest(name string, dgst blobstore.Digest) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("docker: backend read-only")
	}
	if err := wfs.Delete(s.manifestPath(name, dgst)); err != nil {
		if isNotFound(err) {
			return fmt.Errorf("%s@%s: %w", name, dgst, ErrManifestUnknown)
		}
		return fmt.Errorf("docker: delete manifest: %w", err)
	}
	_ = wfs.Delete(s.manifestPath(name, dgst) + ".media")
	return nil
}

// ─── Manifest layer / platform inspection ─────────────────────────

// LayerInfo describes one layer inside an image manifest.
type LayerInfo struct {
	Digest    string `json:"digest"`
	Size      int64  `json:"size,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

// PlatformInfo describes one descriptor inside an image index
// (manifest list) — used for multi-arch images.
type PlatformInfo struct {
	OS           string `json:"os,omitempty"`
	Architecture string `json:"architecture,omitempty"`
	Variant      string `json:"variant,omitempty"`
	Digest       string `json:"digest"`
	Size         int64  `json:"size,omitempty"`
}

// ManifestInspection is the parsed shape of a manifest body for
// the package-detail UI. ConfigDigest / Layers are populated for
// image manifests; Platforms for image indexes (multi-arch).
type ManifestInspection struct {
	MediaType    string
	ArtifactType string
	ConfigDigest string
	ImageSize    int64
	Layers       []LayerInfo
	Platforms    []PlatformInfo
}

// inspectManifestDetail is the shared parser used by the detail
// endpoint. The shape is intentionally lax (every field optional)
// so we don't reject manifests that omit pieces a strict OCI
// validator would flag.
type inspectManifestDetail struct {
	MediaType    string                `json:"mediaType,omitempty"`
	ArtifactType string                `json:"artifactType,omitempty"`
	Config       *manifestConfig       `json:"config,omitempty"`
	Layers       []manifestLayer       `json:"layers,omitempty"`
	Manifests    []manifestListEntry   `json:"manifests,omitempty"`
}

type manifestConfig struct {
	Digest    string `json:"digest,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
}

type manifestLayer struct {
	Digest    string `json:"digest"`
	Size      int64  `json:"size,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
}

type manifestListEntry struct {
	Digest    string                  `json:"digest"`
	Size      int64                   `json:"size,omitempty"`
	MediaType string                  `json:"mediaType,omitempty"`
	Platform  *manifestListEntryPlat  `json:"platform,omitempty"`
}

type manifestListEntryPlat struct {
	OS           string `json:"os,omitempty"`
	Architecture string `json:"architecture,omitempty"`
	Variant      string `json:"variant,omitempty"`
}

// InspectManifestBytes parses a manifest body and returns a
// detail-oriented view: layer breakdown for image manifests,
// platform descriptors for image indexes. Returns nil for bodies
// that don't parse as JSON.
func InspectManifestBytes(body []byte) *ManifestInspection {
	var m inspectManifestDetail
	if err := json.Unmarshal(body, &m); err != nil {
		return nil
	}
	out := &ManifestInspection{
		MediaType: m.MediaType,
		ArtifactType: m.ArtifactType,
	}
	if m.Config != nil {
		out.ConfigDigest = m.Config.Digest
		// Fall back to the config mediaType as artifact type when
		// the manifest didn't carry an explicit artifactType and
		// the config is not a generic image config.
		if out.ArtifactType == "" && m.Config.MediaType != "" {
			switch m.Config.MediaType {
			case "application/vnd.oci.image.config.v1+json",
				"application/vnd.docker.container.image.v1+json":
				// generic; ignore
			default:
				out.ArtifactType = m.Config.MediaType
			}
		}
	}
	if len(m.Layers) > 0 {
		out.Layers = make([]LayerInfo, 0, len(m.Layers))
		for _, l := range m.Layers {
			out.Layers = append(out.Layers, LayerInfo{
				Digest:    l.Digest,
				Size:      l.Size,
				MediaType: l.MediaType,
			})
			out.ImageSize += l.Size
		}
	}
	if len(m.Manifests) > 0 {
		out.Platforms = make([]PlatformInfo, 0, len(m.Manifests))
		for _, e := range m.Manifests {
			p := PlatformInfo{
				Digest: e.Digest,
				Size:   e.Size,
			}
			if e.Platform != nil {
				p.OS = e.Platform.OS
				p.Architecture = e.Platform.Architecture
				p.Variant = e.Platform.Variant
			}
			out.Platforms = append(out.Platforms, p)
		}
	}
	return out
}

// ManifestModTime returns the modification time (unix seconds) of
// a manifest blob, or 0 when the file does not exist. Used by the
// detail UI to surface "pushed at" timestamps.
func (s *Store) ManifestModTime(name string, dgst blobstore.Digest) int64 {
	fi, err := s.fs.Stat(s.manifestPath(name, dgst))
	if err != nil {
		return 0
	}
	return fi.ModTime.Unix()
}

// ─── Tags ──────────────────────────────────────────────────────────

// SetTag points a tag at a manifest digest.
func (s *Store) SetTag(name, tag string, dgst blobstore.Digest) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("docker: backend read-only")
	}
	body := dgst.String()
	return wfs.Write(s.tagPath(name, tag), strings.NewReader(body), int64(len(body)))
}

// ReadTag resolves a tag to its manifest digest. Returns
// ErrTagUnknown when the tag doesn't exist.
func (s *Store) ReadTag(name, tag string) (blobstore.Digest, error) {
	rc, _, err := s.fs.Open(s.tagPath(name, tag))
	if err != nil {
		if isNotFound(err) {
			return blobstore.Digest{}, fmt.Errorf("%s:%s: %w", name, tag, ErrTagUnknown)
		}
		return blobstore.Digest{}, fmt.Errorf("docker: read tag: %w", err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return blobstore.Digest{}, fmt.Errorf("docker: read tag body: %w", err)
	}
	return blobstore.ParseDigest(strings.TrimSpace(string(body)))
}

// DeleteTag removes a tag pointer.
func (s *Store) DeleteTag(name, tag string) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("docker: backend read-only")
	}
	if err := wfs.Delete(s.tagPath(name, tag)); err != nil {
		if isNotFound(err) {
			return fmt.Errorf("%s:%s: %w", name, tag, ErrTagUnknown)
		}
		return err
	}
	return nil
}

// ListTags returns every tag for a repo, sorted ascending. Returns
// nil (not error) when the repo doesn't exist or has no tags.
func (s *Store) ListTags(name string) ([]string, error) {
	dir := path.Join(s.repoDir(name), "tags")
	entries, err := s.fs.ReadDir(dir)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("docker: list tags: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		if e.Name != "" {
			out = append(out, e.Name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ─── Upload session persistence ────────────────────────────────────
//
// Upload sessions in this MVP live on the rawfs as
// {repo}/_uploads/{uuid} files: a small JSON document holding the
// inner BlobStore upload session ID, so we can resume across
// restarts when the rawfs is durable. Bytes themselves stream
// through the BlobStore.

// uploadRecord links a Docker upload UUID to the underlying
// BlobStore session ID. We persist this so a Docker client that
// restarts mid-push can resume against the same blob session
// (matching the spec's chunked upload semantics).
type uploadRecord struct {
	Name     string `json:"name"`
	BlobSess string `json:"blob_session"`
}

// ─── Referrers (OCI v1.1) ──────────────────────────────────────────

// ReadReferrers returns the cached referrers index for a subject
// digest. Returns the empty index (not nil, not error) when no
// referrers have been recorded for the subject yet — the spec's
// 200 OK response with an empty manifests array.
func (s *Store) ReadReferrers(name, subjectDigest string) (ociImageIndex, error) {
	rc, _, err := s.fs.Open(s.referrersIndexPath(name, subjectDigest))
	if err != nil {
		if isNotFound(err) {
			return newEmptyReferrersIndex(), nil
		}
		return ociImageIndex{}, fmt.Errorf("docker: read referrers: %w", err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return ociImageIndex{}, err
	}
	var idx ociImageIndex
	if err := json.Unmarshal(body, &idx); err != nil {
		return newEmptyReferrersIndex(), nil
	}
	if idx.Manifests == nil {
		idx.Manifests = []manifestDescriptor{}
	}
	return idx, nil
}

// AddReferrer adds (or updates, when the digest already exists)
// a descriptor to the subject's referrers index. Idempotent on
// re-add of the same digest.
func (s *Store) AddReferrer(name, subjectDigest string, desc manifestDescriptor) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("docker: backend read-only")
	}
	idx, _ := s.ReadReferrers(name, subjectDigest)
	// Dedupe: replace any existing entry with the same digest.
	for i, existing := range idx.Manifests {
		if existing.Digest == desc.Digest {
			idx.Manifests[i] = desc
			return s.writeReferrers(wfs, name, subjectDigest, idx)
		}
	}
	idx.Manifests = append(idx.Manifests, desc)
	return s.writeReferrers(wfs, name, subjectDigest, idx)
}

// RemoveReferrer drops a referrer descriptor from a subject's
// index. Idempotent — missing entries are not an error.
func (s *Store) RemoveReferrer(name, subjectDigest, referrerDigest string) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("docker: backend read-only")
	}
	idx, err := s.ReadReferrers(name, subjectDigest)
	if err != nil {
		return err
	}
	out := idx.Manifests[:0]
	for _, m := range idx.Manifests {
		if m.Digest != referrerDigest {
			out = append(out, m)
		}
	}
	idx.Manifests = out
	if len(idx.Manifests) == 0 {
		// Drop the empty index file so the directory listing stays
		// clean. ReadReferrers returns the empty document for
		// missing files, so this is observationally equivalent.
		_ = wfs.Delete(s.referrersIndexPath(name, subjectDigest))
		return nil
	}
	return s.writeReferrers(wfs, name, subjectDigest, idx)
}

// DeleteReferrersIndex removes the referrers index file for a
// subject digest. Used by the delete-triggered cheap cascade when
// the subject manifest itself is being deleted: with the subject
// gone, every referrer descriptor in the index is dangling, and
// the index file is meaningless on its own. Idempotent — missing
// is not an error.
func (s *Store) DeleteReferrersIndex(name, subjectDigest string) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("docker: backend read-only")
	}
	if err := wfs.Delete(s.referrersIndexPath(name, subjectDigest)); err != nil {
		if isNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

// writeReferrers persists the index document.
func (s *Store) writeReferrers(wfs rawfs.WritableRawFS, name, subjectDigest string, idx ociImageIndex) error {
	body, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	return wfs.Write(s.referrersIndexPath(name, subjectDigest),
		strings.NewReader(string(body)), int64(len(body)))
}

// ─── Catalog ───────────────────────────────────────────────────────

// ListRepositories walks repositories/ and returns every repo name.
// The walk handles nested repo names ("namespace/imagename") by
// detecting a "tags" subdirectory as the leaf marker — same
// convention used by ListTags.
func (s *Store) ListRepositories() ([]string, error) {
	root := s.join("repositories")
	out, err := walkRepos(s.fs, root, "")
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func walkRepos(fs rawfs.RawFS, dir, prefix string) ([]string, error) {
	entries, err := fs.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir {
			continue
		}
		name := e.Name
		if prefix != "" {
			name = prefix + "/" + e.Name
		}
		// Leaf detection: has a "tags" or "manifests" subdir.
		if _, err := fs.Stat(path.Join(dir, e.Name, "tags")); err == nil {
			out = append(out, name)
			continue
		}
		if _, err := fs.Stat(path.Join(dir, e.Name, "manifests")); err == nil {
			out = append(out, name)
			continue
		}
		sub, err := walkRepos(fs, path.Join(dir, e.Name), name)
		if err != nil {
			continue
		}
		out = append(out, sub...)
	}
	return out, nil
}

// isNotFound — same pattern as goproxy/npm stores.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "not found") ||
		strings.Contains(low, "no such file") ||
		strings.Contains(low, "does not exist")
}

// _ kept to satisfy potential refactor that drops the uploadRecord
// type before the upload handler grows.
var _ = uploadRecord{}
