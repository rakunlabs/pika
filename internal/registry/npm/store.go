package npm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/rakunlabs/pika/internal/rawfs"
)

// Store wraps a rawfs.RawFS scoped to one NPM registry repo. Files
// live under the configured base path; the on-disk layout is
// documented in npm.go's package comment.
//
// Concurrency: every method is safe for concurrent use; rawfs
// backends handle parallel reads, and writes are last-writer-wins
// at the file level. The packument cache is invalidated by writing
// to versions/ — any subsequent CachedPackument call sees an out-of-
// date cache file and rebuilds.
type Store struct {
	fs       rawfs.RawFS
	basePath string
}

// NewStore wraps a rawfs.RawFS as a Store.
func NewStore(fs rawfs.RawFS, basePath string) *Store {
	return &Store{fs: fs, basePath: strings.Trim(basePath, "/")}
}

// RawFS returns the underlying rawfs handle.
func (s *Store) RawFS() rawfs.RawFS { return s.fs }

func (s *Store) join(parts ...string) string {
	if s.basePath != "" {
		parts = append([]string{s.basePath}, parts...)
	}
	return path.Join(parts...)
}

func (s *Store) packageDir(name string) string {
	// Scoped packages: name already carries "@scope/" prefix, so
	// path.Join naturally produces packages/@scope/name.
	return s.join("packages", name)
}

func (s *Store) versionMetaPath(name, version string) string {
	return path.Join(s.packageDir(name), "versions", version+".json")
}

func (s *Store) tarballPath(name, file string) string {
	return path.Join(s.packageDir(name), "tarballs", file)
}

func (s *Store) distTagsPath(name string) string {
	return path.Join(s.packageDir(name), "dist-tags.json")
}

func (s *Store) packumentCachePath(name string) string {
	return path.Join(s.packageDir(name), "packument.json")
}

func (s *Store) readmePath(name string) string {
	return path.Join(s.packageDir(name), "readme.md")
}

// HasPackage returns true when the package has at least one version
// stored.
func (s *Store) HasPackage(name string) (bool, error) {
	if _, err := s.fs.Stat(path.Join(s.packageDir(name), "versions")); err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ListVersions returns every version present under versions/. Sort
// is lexicographic; semver-aware sorting is the consumer's job.
func (s *Store) ListVersions(name string) ([]string, error) {
	dir := path.Join(s.packageDir(name), "versions")
	entries, err := s.fs.ReadDir(dir)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("npm: list versions: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		if !strings.HasSuffix(e.Name, ".json") {
			continue
		}
		v := strings.TrimSuffix(e.Name, ".json")
		if v != "" {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ReadVersionMeta returns the parsed metadata JSON for one version.
// The shape mirrors npm-on-the-wire — Name, Version, Dist, Dependencies
// and the assorted other fields a packument carries per version.
func (s *Store) ReadVersionMeta(name, version string) (map[string]any, error) {
	rc, _, err := s.fs.Open(s.versionMetaPath(name, version))
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("%s@%s: %w", name, version, ErrPackageNotFound)
		}
		return nil, fmt.Errorf("npm: read version meta: %w", err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("npm: read body: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("npm: parse version meta: %w", err)
	}
	return out, nil
}

// WriteVersionMeta persists a per-version metadata blob. Callers
// typically build the map from the publish payload, augmenting with
// the resolved tarball URL.
func (s *Store) WriteVersionMeta(name, version string, meta map[string]any) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("npm: backend read-only")
	}
	body, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("npm: marshal meta: %w", err)
	}
	if err := wfs.Write(s.versionMetaPath(name, version), strings.NewReader(string(body)), int64(len(body))); err != nil {
		return fmt.Errorf("npm: write meta: %w", err)
	}
	// Best-effort cache invalidation.
	_ = wfs.Delete(s.packumentCachePath(name))
	return nil
}

// VersionMetaExists is a cheap probe without opening the file.
func (s *Store) VersionMetaExists(name, version string) (bool, error) {
	_, err := s.fs.Stat(s.versionMetaPath(name, version))
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// DeleteVersion removes one published package version, its tarball,
// derived packument/readme caches, and any dist-tags that pointed to
// the removed version. The package directory may remain on backends
// without directory removal; ListPackages ignores empty version dirs.
func (s *Store) DeleteVersion(name, version string) error {
	if err := ValidatePackageName(name); err != nil {
		return err
	}
	if strings.TrimSpace(version) == "" {
		return fmt.Errorf("npm: empty version: %w", ErrInvalidVersion)
	}
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("npm: backend read-only")
	}

	meta, err := s.ReadVersionMeta(name, version)
	if err != nil {
		if errors.Is(err, ErrPackageNotFound) {
			return err
		}
		return fmt.Errorf("npm: read version before delete: %w", err)
	}

	if err := wfs.Delete(s.versionMetaPath(name, version)); err != nil {
		if isNotFound(err) {
			return fmt.Errorf("%s@%s: %w", name, version, ErrPackageNotFound)
		}
		return fmt.Errorf("npm: delete version meta: %w", err)
	}
	if file := tarballFilenameFromMeta(meta); file != "" {
		_ = wfs.Delete(s.tarballPath(name, file))
	}

	if tags, err := s.ReadDistTags(name); err == nil && len(tags) > 0 {
		changed := false
		for tag, taggedVersion := range tags {
			if taggedVersion == version {
				delete(tags, tag)
				changed = true
			}
		}
		if changed {
			_ = s.WriteDistTags(name, tags)
		}
	}
	_ = wfs.Delete(s.packumentCachePath(name))
	_ = wfs.Delete(s.readmePath(name))
	return nil
}

// OpenTarball streams a tarball file by name. file is the filename
// portion of the URL ("lodash-4.17.21.tgz"); the store doesn't
// re-derive it from name+version because npm publish payloads
// include the filename explicitly and pika preserves it verbatim.
func (s *Store) OpenTarball(name, file string) (rawfs.ReadSeekCloser, *rawfs.FileInfo, error) {
	rc, fi, err := s.fs.Open(s.tarballPath(name, file))
	if err != nil {
		if isNotFound(err) {
			return nil, nil, fmt.Errorf("%s/%s: %w", name, file, ErrPackageNotFound)
		}
		return nil, nil, err
	}
	return rc, fi, nil
}

// WriteTarball persists a tarball under tarballs/{file}.
func (s *Store) WriteTarball(name, file string, r io.Reader, size int64) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("npm: backend read-only")
	}
	if err := wfs.Write(s.tarballPath(name, file), r, size); err != nil {
		return fmt.Errorf("npm: write tarball: %w", err)
	}
	return nil
}

// ReadDistTags returns the current dist-tags map. Missing file is
// treated as an empty map (no dist-tags configured yet).
func (s *Store) ReadDistTags(name string) (map[string]string, error) {
	rc, _, err := s.fs.Open(s.distTagsPath(name))
	if err != nil {
		if isNotFound(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	var out map[string]string
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("npm: parse dist-tags: %w", err)
	}
	if out == nil {
		out = map[string]string{}
	}
	return out, nil
}

// WriteDistTags overwrites the dist-tags map.
func (s *Store) WriteDistTags(name string, tags map[string]string) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("npm: backend read-only")
	}
	body, err := json.Marshal(tags)
	if err != nil {
		return err
	}
	if err := wfs.Write(s.distTagsPath(name), strings.NewReader(string(body)), int64(len(body))); err != nil {
		return err
	}
	_ = wfs.Delete(s.packumentCachePath(name))
	return nil
}

// ReadCachedPackument returns the previously-cached packument bytes,
// or (nil, false) if no cached copy exists. The packument is a
// derivative document; callers typically rebuild on cache miss
// (handler.go's BuildPackument).
func (s *Store) ReadCachedPackument(name string) ([]byte, bool, error) {
	rc, _, err := s.fs.Open(s.packumentCachePath(name))
	if err != nil {
		if isNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return nil, false, err
	}
	return body, true, nil
}

// WriteCachedPackument persists the rebuilt packument so subsequent
// reads avoid the rebuild cost. Best-effort: failure here just means
// the next reader rebuilds again.
func (s *Store) WriteCachedPackument(name string, body []byte) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return nil
	}
	return wfs.Write(s.packumentCachePath(name), strings.NewReader(string(body)), int64(len(body)))
}

// WriteReadme persists the per-package README (the long-form one
// from the publish payload). The cached file lets the UI render
// markdown without re-parsing every version's metadata.
func (s *Store) WriteReadme(name, content string) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return nil
	}
	return wfs.Write(s.readmePath(name), strings.NewReader(content), int64(len(content)))
}

// ReadReadme returns the cached README content, or "" if absent.
func (s *Store) ReadReadme(name string) (string, error) {
	rc, _, err := s.fs.Open(s.readmePath(name))
	if err != nil {
		if isNotFound(err) {
			return "", nil
		}
		return "", err
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// HasReadme is a cheap existence probe for the cached README. Used
// by the package detail endpoint to set HasReadme without paying
// the cost of reading the file body.
func (s *Store) HasReadme(name string) bool {
	_, err := s.fs.Stat(s.readmePath(name))
	return err == nil
}

// TarballSize returns the size in bytes of a tarball file, or 0
// when missing. Mirrors goproxy.Store.VersionSize: the detail
// endpoint surfaces sizes without forcing an open.
func (s *Store) TarballSize(name, file string) int64 {
	fi, err := s.fs.Stat(s.tarballPath(name, file))
	if err != nil {
		return 0
	}
	return fi.Size
}

// LazyExtractReadme reads the tarball for (name, version), unpacks
// the gzip+tar, and tries to extract a README file. The extracted
// content is cached via WriteReadme so subsequent calls are cheap.
//
// Returns ("", nil) when the tarball is missing or contains no
// README. The caller is responsible for choosing which version's
// tarball to look at (typically the latest).
//
// Recognized README filenames (case-insensitive): README.md,
// README.markdown, README. The first match wins. Tarballs use the
// "package/" prefix per npm convention; the function strips that
// prefix when matching.
func (s *Store) LazyExtractReadme(name, file string) (string, error) {
	rc, _, err := s.fs.Open(s.tarballPath(name, file))
	if err != nil {
		if isNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("npm: open tarball: %w", err)
	}
	defer rc.Close()
	content, err := extractReadmeFromTarball(rc)
	if err != nil {
		return "", err
	}
	if content == "" {
		return "", nil
	}
	// Cache the extracted README so subsequent reads bypass the
	// tar walk. Failure here is non-fatal — the caller still gets
	// the content; next call just re-extracts.
	_ = s.WriteReadme(name, content)
	return content, nil
}

// PackageDirExists reports whether the package directory exists.
// Used by ListVersions callers that want to distinguish "no
// versions yet" from "package does not exist".
func (s *Store) PackageDirExists(name string) bool {
	_, err := s.fs.Stat(s.packageDir(name))
	return err == nil
}

// ListPackages walks the packages/ tree and returns every package
// name (canonical "@scope/name" or "name") with at least one
// version stored. Used by search + the admin UI's package browser.
func (s *Store) ListPackages() ([]string, error) {
	root := s.join("packages")
	out, err := walkPackages(s.fs, root, "")
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// walkPackages recurses one level deep into "@scope/" subdirs and
// looks for versions/ leaves marking package directories.
func walkPackages(fs rawfs.RawFS, dir, prefix string) ([]string, error) {
	entries, err := fs.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir {
			continue
		}
		// Detect a package leaf: has a "versions" subdir.
		vDir := path.Join(dir, e.Name, "versions")
		if hasVersionMetaFiles(fs, vDir) {
			pkg := e.Name
			if prefix != "" {
				pkg = prefix + "/" + e.Name
			}
			out = append(out, pkg)
			continue
		}
		// Otherwise, only descend one level (npm scopes are flat).
		if prefix != "" {
			continue
		}
		sub, err := walkPackages(fs, path.Join(dir, e.Name), e.Name)
		if err != nil {
			continue
		}
		out = append(out, sub...)
	}
	return out, nil
}

func hasVersionMetaFiles(fs rawfs.RawFS, dir string) bool {
	entries, err := fs.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir && strings.HasSuffix(e.Name, ".json") {
			return true
		}
	}
	return false
}

// CountPackagesVersionsBytes walks the packages tree and reports
// (#packages, sum of versions across all packages, total bytes).
// Both Local and Remote share the helper so the stats endpoint has
// uniform semantics.
func (s *Store) CountPackagesVersionsBytes() (packages int, versions int, bytes int64) {
	walkNPMPackages(s.fs, s.join("packages"), "", func(name string) {
		packages++
		// versions/ may not exist on a Remote repo that only
		// cached the packument — ListVersions handles the
		// missing-dir case.
		vs, _ := s.ListVersions(name)
		versions += len(vs)
	})
	// Total bytes: walk the packages/ tree end-to-end. This
	// includes packument caches, tarballs, dist-tags and version
	// metadata — the operator wants "total disk used by this
	// repo".
	walkBytes(s.fs, s.join("packages"), &bytes)
	return
}

// walkBytes is a depth-first byte tally helper. Errors are
// swallowed — partial accounting beats a hard failure.
func walkBytes(fs rawfs.RawFS, root string, total *int64) {
	entries, err := fs.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		full := path.Join(root, e.Name)
		if e.IsDir {
			walkBytes(fs, full, total)
			continue
		}
		if fi, err := fs.Stat(full); err == nil {
			*total += fi.Size
		}
	}
}

// PurgeMutable deletes every cached packument across the repo.
// Mutable in NPM-land means the packument document (the per-package
// JSON listing all versions + dist-tags). Tarballs themselves are
// content-addressed by sha512 integrity and are kept. Used by
// Remote registries to force a re-fetch on the next read.
//
// Walks the packages/ tree directly instead of routing through
// ListPackages — a Remote repo never persists per-version metadata,
// so the "has versions/" leaf detector that ListPackages uses
// would miss every cached packument. This walker keys on the
// presence of the packument.json file itself.
func (s *Store) PurgeMutable() (int, int64, []error) {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return 0, 0, []error{fmt.Errorf("npm: backend is read-only")}
	}
	var (
		count int
		bytes int64
		errs  []error
	)
	walkNPMPackages(s.fs, s.join("packages"), "", func(name string) {
		p := s.packumentCachePath(name)
		if fi, err := s.fs.Stat(p); err == nil {
			if delErr := wfs.Delete(p); delErr == nil {
				count++
				bytes += fi.Size
			} else if !isNotFound(delErr) {
				errs = append(errs, fmt.Errorf("delete %s: %w", p, delErr))
			}
		}
	})
	return count, bytes, errs
}

// walkNPMPackages descends "packages/" looking for any directory
// that holds a packument cache, dist-tags.json or versions/ subdir.
// Calls fn(canonicalName) once per package. Includes Remote repos
// that only have a packument.json (no versions/ leaf).
func walkNPMPackages(fs rawfs.RawFS, dir, prefix string, fn func(name string)) {
	entries, err := fs.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir {
			continue
		}
		// Is this a package leaf? Check for any of the three
		// canonical markers we leave behind.
		pkgDir := path.Join(dir, e.Name)
		isLeaf := false
		for _, marker := range []string{"packument.json", "dist-tags.json", "versions"} {
			if _, err := fs.Stat(path.Join(pkgDir, marker)); err == nil {
				isLeaf = true
				break
			}
		}
		if isLeaf {
			name := e.Name
			if prefix != "" {
				name = prefix + "/" + e.Name
			}
			fn(name)
			continue
		}
		// Descend one level for @scope/.
		if prefix == "" {
			walkNPMPackages(fs, pkgDir, e.Name, fn)
		}
	}
}

// PurgeAll wipes the entire NPM cache: every packument, version
// metadata file, tarball and dist-tags JSON under {base}/packages/.
// Use only on Remote registries; on Local, this would delete the
// publisher's own artifacts.
func (s *Store) PurgeAll() (int, int64, []error) {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return 0, 0, []error{fmt.Errorf("npm: backend is read-only")}
	}
	var (
		count int
		bytes int64
		errs  []error
	)
	walkNPMPackages(s.fs, s.join("packages"), "", func(name string) {
		count, bytes = purgePackageDir(s, wfs, name, count, bytes, &errs)
	})
	return count, bytes, errs
}

func purgePackageDir(s *Store, wfs rawfs.WritableRawFS, name string, count int, bytes int64, errs *[]error) (int, int64) {
	dir := s.packageDir(name)
	// Top-level packument cache + dist-tags.
	for _, p := range []string{s.packumentCachePath(name), s.distTagsPath(name)} {
		if fi, err := s.fs.Stat(p); err == nil {
			if delErr := wfs.Delete(p); delErr == nil {
				count++
				bytes += fi.Size
			}
		}
	}
	// versions/*.json
	for _, sub := range []string{"versions", "tarballs"} {
		d := path.Join(dir, sub)
		entries, err := s.fs.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir {
				continue
			}
			p := path.Join(d, e.Name)
			fi, statErr := s.fs.Stat(p)
			if statErr != nil {
				continue
			}
			if delErr := wfs.Delete(p); delErr == nil {
				count++
				bytes += fi.Size
			} else if !isNotFound(delErr) {
				*errs = append(*errs, fmt.Errorf("delete %s: %w", p, delErr))
			}
		}
	}
	return count, bytes
}

// isNotFound — same shape as the goproxy store's helper.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "not found") ||
		strings.Contains(low, "no such file") ||
		strings.Contains(low, "does not exist")
}
