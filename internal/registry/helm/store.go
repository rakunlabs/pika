package helm

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/rakunlabs/pika/internal/rawfs"
)

// Store wraps a rawfs.RawFS scoped to one Helm repo. Mirrors the
// shape of npm.Store / goproxy.Store: thin path-keyed wrappers
// around rawfs primitives.
type Store struct {
	fs       rawfs.RawFS
	basePath string
}

func NewStore(fs rawfs.RawFS, basePath string) *Store {
	return &Store{fs: fs, basePath: strings.Trim(basePath, "/")}
}

func (s *Store) RawFS() rawfs.RawFS { return s.fs }

func (s *Store) join(parts ...string) string {
	if s.basePath != "" {
		parts = append([]string{s.basePath}, parts...)
	}
	return path.Join(parts...)
}

func (s *Store) chartDir(name string) string {
	return s.join("charts", name)
}

func (s *Store) tarballPath(name, version string) string {
	return path.Join(s.chartDir(name), TarballFilename(name, version))
}

func (s *Store) metadataPath(name, version string) string {
	return path.Join(s.chartDir(name), "meta-"+version+".json")
}

func (s *Store) readmePath(name string) string {
	return path.Join(s.chartDir(name), "readme.md")
}

func (s *Store) indexPath() string {
	return s.join("index.yaml")
}

// WriteChart persists a tarball + parsed Chart.yaml + extracted
// README in one go. Idempotent — overwriting an existing version
// is allowed (callers gate on a pre-check when they want a
// "already published" error).
func (s *Store) WriteChart(extraction *ChartExtraction, body []byte) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("helm: backend read-only")
	}
	name := extraction.Metadata.Name
	ver := extraction.Metadata.Version
	if err := wfs.Write(s.tarballPath(name, ver), strings.NewReader(string(body)), int64(len(body))); err != nil {
		return fmt.Errorf("helm: write tarball: %w", err)
	}
	metaBody, err := json.Marshal(extraction.Metadata)
	if err != nil {
		return fmt.Errorf("helm: marshal meta: %w", err)
	}
	if err := wfs.Write(s.metadataPath(name, ver), strings.NewReader(string(metaBody)), int64(len(metaBody))); err != nil {
		return fmt.Errorf("helm: write meta: %w", err)
	}
	if extraction.Readme != "" {
		_ = wfs.Write(s.readmePath(name), strings.NewReader(extraction.Readme), int64(len(extraction.Readme)))
	}
	// Bust the cached index — next index.yaml request rebuilds.
	_ = wfs.Delete(s.indexPath())
	return nil
}

// OpenTarball streams the chart tarball for (name, version).
func (s *Store) OpenTarball(name, version string) (rawfs.ReadSeekCloser, *rawfs.FileInfo, error) {
	rc, fi, err := s.fs.Open(s.tarballPath(name, version))
	if err != nil {
		if isNotFound(err) {
			return nil, nil, fmt.Errorf("%s-%s.tgz: %w", name, version, ErrChartNotFound)
		}
		return nil, nil, err
	}
	return rc, fi, nil
}

// ReadMetadata returns the parsed Chart.yaml for one version.
func (s *Store) ReadMetadata(name, version string) (*ChartMetadata, error) {
	rc, _, err := s.fs.Open(s.metadataPath(name, version))
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("%s-%s: %w", name, version, ErrChartNotFound)
		}
		return nil, err
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	var meta ChartMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, fmt.Errorf("helm: parse meta: %w", err)
	}
	return &meta, nil
}

// ListVersions enumerates every version present for a chart.
// Returns nil (not error) when the chart directory does not exist.
func (s *Store) ListVersions(name string) ([]string, error) {
	dir := s.chartDir(name)
	entries, err := s.fs.ReadDir(dir)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("helm: list versions: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		if !strings.HasPrefix(e.Name, "meta-") || !strings.HasSuffix(e.Name, ".json") {
			continue
		}
		v := strings.TrimPrefix(e.Name, "meta-")
		v = strings.TrimSuffix(v, ".json")
		if v != "" {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ListCharts walks the charts/ tree and returns every chart name
// with at least one published version.
func (s *Store) ListCharts() ([]string, error) {
	dir := s.join("charts")
	entries, err := s.fs.ReadDir(dir)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir {
			continue
		}
		// Confirm at least one meta-*.json — drop empty
		// directories left over after a deletion.
		sub, _ := s.fs.ReadDir(path.Join(dir, e.Name))
		for _, x := range sub {
			if !x.IsDir && strings.HasPrefix(x.Name, "meta-") && strings.HasSuffix(x.Name, ".json") {
				out = append(out, e.Name)
				break
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// ReadReadme returns the cached README content (extracted from
// the latest published tarball), or "" if absent.
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

// HasReadme is a cheap existence probe — used by the package
// detail endpoint to populate HasReadme without a body read.
func (s *Store) HasReadme(name string) bool {
	_, err := s.fs.Stat(s.readmePath(name))
	return err == nil
}

// TarballSize returns the file size in bytes of a chart tarball,
// or 0 when missing.
func (s *Store) TarballSize(name, version string) int64 {
	fi, err := s.fs.Stat(s.tarballPath(name, version))
	if err != nil {
		return 0
	}
	return fi.Size
}

// TarballModTime returns the modification time of the tarball as
// an RFC3339 string, or "" when missing.
func (s *Store) TarballModTime(name, version string) string {
	fi, err := s.fs.Stat(s.tarballPath(name, version))
	if err != nil {
		return ""
	}
	return fi.ModTime.UTC().Format(time.RFC3339)
}

// DeleteVersion removes a chart version's tarball + metadata
// sidecar. Bust the index cache so the next index.yaml rebuild
// reflects the removal.
func (s *Store) DeleteVersion(name, version string) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("helm: backend read-only")
	}
	tarballErr := wfs.Delete(s.tarballPath(name, version))
	_ = wfs.Delete(s.metadataPath(name, version))
	_ = wfs.Delete(s.indexPath())
	if tarballErr != nil && !isNotFound(tarballErr) {
		return tarballErr
	}
	return nil
}

// CountChartsVersionsBytes walks the on-disk tree once for the
// stats endpoint. Mirrors goproxy/npm convention.
func (s *Store) CountChartsVersionsBytes() (charts, versions int, bytes int64) {
	names, _ := s.ListCharts()
	charts = len(names)
	for _, name := range names {
		vs, _ := s.ListVersions(name)
		versions += len(vs)
		for _, v := range vs {
			bytes += s.TarballSize(name, v)
		}
	}
	return
}

// PurgeMutable deletes the cached index.yaml — same semantics as
// goproxy.Store.PurgeMutable (drop the derived index, keep the
// immutable artefacts). Used by Remote repos to force a re-fetch
// of the upstream index on the next read.
func (s *Store) PurgeMutable() (int, int64, []error) {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return 0, 0, []error{fmt.Errorf("helm: backend is read-only")}
	}
	var (
		count int
		bytes int64
		errs  []error
	)
	if fi, err := s.fs.Stat(s.indexPath()); err == nil {
		if delErr := wfs.Delete(s.indexPath()); delErr == nil {
			count++
			bytes += fi.Size
		} else if !isNotFound(delErr) {
			errs = append(errs, delErr)
		}
	}
	return count, bytes, errs
}

// PurgeAll wipes the entire chart cache: every tarball, sidecar,
// readme, and the cached index. Only safe on Remote repos.
func (s *Store) PurgeAll() (int, int64, []error) {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return 0, 0, []error{fmt.Errorf("helm: backend is read-only")}
	}
	var (
		count int
		bytes int64
		errs  []error
	)
	// Drop the cached index first.
	if fi, err := s.fs.Stat(s.indexPath()); err == nil {
		if delErr := wfs.Delete(s.indexPath()); delErr == nil {
			count++
			bytes += fi.Size
		}
	}
	// Walk charts/* and delete every leaf file.
	charts, _ := s.ListCharts()
	for _, name := range charts {
		dir := s.chartDir(name)
		entries, err := s.fs.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir {
				continue
			}
			p := path.Join(dir, e.Name)
			fi, statErr := s.fs.Stat(p)
			if statErr != nil {
				continue
			}
			if delErr := wfs.Delete(p); delErr == nil {
				count++
				bytes += fi.Size
			} else if !isNotFound(delErr) {
				errs = append(errs, fmt.Errorf("delete %s: %w", p, delErr))
			}
		}
	}
	return count, bytes, errs
}

// isNotFound matches the sentinel pattern used across pika's
// registry stores.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "not found") ||
		strings.Contains(low, "no such file") ||
		strings.Contains(low, "does not exist")
}
