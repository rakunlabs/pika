package cargo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/events"
	"github.com/rakunlabs/pika/internal/registry/upstream"
	"github.com/rakunlabs/pika/internal/registry/virtualbase"
	"github.com/rakunlabs/pika/internal/service"
)

type Store struct {
	fs       rawfs.RawFS
	basePath string
}

func NewStore(fs rawfs.RawFS, basePath string) *Store {
	return &Store{fs: fs, basePath: strings.Trim(basePath, "/")}
}

func (s *Store) RawFS() rawfs.RawFS { return s.fs }

func (s *Store) join(parts ...string) string {
	cleaned := make([]string, 0, len(parts)+1)
	if s.basePath != "" {
		cleaned = append(cleaned, s.basePath)
	}
	for _, p := range parts {
		p = strings.Trim(p, "/")
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	return path.Join(cleaned...)
}

func norm(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func indexPath(name string) string {
	n := norm(name)
	switch len(n) {
	case 0:
		return ""
	case 1:
		return path.Join("1", n)
	case 2:
		return path.Join("2", n)
	case 3:
		return path.Join("3", n[:1], n)
	default:
		return path.Join(n[:2], n[2:4], n)
	}
}

func (s *Store) cratePath(name, version string) string {
	name = norm(name)
	return s.join("crates", name, version, fmt.Sprintf("%s-%s.crate", name, version))
}

func (s *Store) indexFilePath(idx string) string { return s.join("index", idx) }

func (s *Store) WriteCrate(name, version string, body []byte) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("cargo: backend read-only")
	}
	if err := wfs.Write(s.cratePath(name, version), bytes.NewReader(body), int64(len(body))); err != nil {
		return err
	}
	return s.upsertIndex(name, version, body)
}

func (s *Store) OpenCrate(name, version string) (rawfs.ReadSeekCloser, *rawfs.FileInfo, error) {
	return s.fs.Open(s.cratePath(name, version))
}

func (s *Store) ReadIndexByName(name string) ([]indexEntry, error) {
	return s.ReadIndex(indexPath(name))
}

func (s *Store) ReadIndex(idx string) ([]indexEntry, error) {
	rc, _, err := s.fs.Open(s.indexFilePath(idx))
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	var out []indexEntry
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ent indexEntry
		if json.Unmarshal([]byte(line), &ent) == nil && ent.Name != "" {
			out = append(out, ent)
		}
	}
	return out, nil
}

func (s *Store) WriteIndex(idx string, body []byte) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("cargo: backend read-only")
	}
	return wfs.Write(s.indexFilePath(idx), bytes.NewReader(body), int64(len(body)))
}

func (s *Store) upsertIndex(name, version string, body []byte) error {
	idx := indexPath(name)
	entries, _ := s.ReadIndex(idx)
	sum := sha256.Sum256(body)
	next := indexEntry{Name: norm(name), Version: version, CKSum: hex.EncodeToString(sum[:]), Deps: []any{}, Features: map[string][]string{}, Links: nil}
	replaced := false
	for i := range entries {
		if entries[i].Version == version {
			entries[i] = next
			replaced = true
			break
		}
	}
	if !replaced {
		entries = append(entries, next)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Version < entries[j].Version })
	var b strings.Builder
	for _, ent := range entries {
		line, _ := json.Marshal(ent)
		b.Write(line)
		b.WriteByte('\n')
	}
	return s.WriteIndex(idx, []byte(b.String()))
}

type indexEntry struct {
	Name     string              `json:"name"`
	Version  string              `json:"vers"`
	Deps     []any               `json:"deps"`
	CKSum    string              `json:"cksum"`
	Features map[string][]string `json:"features"`
	Yanked   bool                `json:"yanked"`
	Links    *string             `json:"links"`
}

func (s *Store) ListCrates() ([]string, error) {
	var out []string
	var walk func(abs string) error
	walk = func(abs string) error {
		entries, err := s.fs.ReadDir(abs)
		if err != nil {
			if isNotFound(err) {
				return nil
			}
			return err
		}
		for _, e := range entries {
			child := path.Join(abs, e.Name)
			if e.IsDir {
				if err := walk(child); err != nil {
					return err
				}
				continue
			}
			rc, _, err := s.fs.Open(child)
			if err != nil {
				continue
			}
			body, _ := io.ReadAll(rc)
			rc.Close()
			for _, line := range strings.Split(string(body), "\n") {
				var ent indexEntry
				if json.Unmarshal([]byte(line), &ent) == nil && ent.Name != "" {
					out = append(out, ent.Name)
					break
				}
			}
		}
		return nil
	}
	if err := walk(s.join("index")); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func (s *Store) ListVersions(name string) ([]string, error) {
	entries, err := s.ReadIndexByName(name)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, ent := range entries {
		out = append(out, ent.Version)
	}
	sort.Strings(out)
	return out, nil
}

func (s *Store) Count() (crates, versions, files int, bytes int64) {
	names, _ := s.ListCrates()
	crates = len(names)
	for _, name := range names {
		ents, _ := s.ReadIndexByName(name)
		versions += len(ents)
		for _, ent := range ents {
			fi, err := s.fs.Stat(s.cratePath(name, ent.Version))
			if err == nil {
				files++
				bytes += fi.Size
			}
		}
	}
	return
}

func (s *Store) PurgeAll() (int, int64, []error)     { return s.purge("") }
func (s *Store) PurgeMutable() (int, int64, []error) { return s.purge("index") }

func (s *Store) purge(prefix string) (int, int64, []error) {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return 0, 0, []error{fmt.Errorf("cargo: backend read-only")}
	}
	var count int
	var bytes int64
	var errs []error
	var walk func(abs string)
	walk = func(abs string) {
		entries, err := s.fs.ReadDir(abs)
		if err != nil {
			if !isNotFound(err) {
				errs = append(errs, err)
			}
			return
		}
		for _, e := range entries {
			child := path.Join(abs, e.Name)
			if e.IsDir {
				walk(child)
				continue
			}
			if err := wfs.Delete(child); err != nil && !isNotFound(err) {
				errs = append(errs, err)
				continue
			}
			count++
			bytes += e.Size
		}
	}
	walk(s.join(prefix))
	return count, bytes, errs
}

type Local struct {
	namespace string
	name      string
	store     *Store
	allowPush bool
	maxUpload int64
	emitter   events.Emitter
}

func NewLocalFactory() registry.Factory {
	return func(_ context.Context, deps registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		fs, err := deps.MountRawFS(r.Mount)
		if err != nil {
			return nil, fmt.Errorf("cargo/local %s/%s: %w", ns, r.Name, err)
		}
		return &Local{namespace: ns, name: r.Name, store: NewStore(fs, r.BasePath), allowPush: r.AllowPush, maxUpload: r.MaxUploadSize, emitter: deps.Emitter}, nil
	}
}

func (l *Local) Namespace() string { return l.namespace }
func (l *Local) Name() string      { return l.name }
func (l *Local) Type() string      { return service.RegistryTypeCargo }
func (l *Local) Kind() string      { return service.RegistryKindLocal }
func (l *Local) Store() *Store     { return l.store }
func (l *Local) Close() error      { return nil }

func (l *Local) Stats(context.Context) (registry.Stats, error) {
	crates, versions, files, bytes := l.store.Count()
	return registry.Stats{PackageCount: crates, VersionCount: versions, BlobCount: files, TotalBytes: bytes}, nil
}
func (l *Local) PackageDetail(_ context.Context, name string) (*registry.PackageDetail, error) {
	return packageDetail(l.store, name)
}

func (l *Local) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/config.json":
		serveConfig(w, r)
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.HasPrefix(r.URL.Path, "/api/v1/crates/"):
		l.serveDownload(w, r)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/crates/"):
		l.publishPut(w, r)
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		l.serveIndex(w, r)
	default:
		http.Error(w, "no cargo route", http.StatusNotFound)
	}
}

func (l *Local) serveIndex(w http.ResponseWriter, r *http.Request) {
	idx := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	ents, err := l.store.ReadIndex(idx)
	if err != nil || len(ents) == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	rc, fi, err := l.store.fs.Open(l.store.indexFilePath(idx))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer rc.Close()
	writeFile(w, r, "text/plain; charset=utf-8", rc, fi)
}

func (l *Local) serveDownload(w http.ResponseWriter, r *http.Request) {
	name, version, ok := parseDownload(r.URL.Path)
	if !ok {
		http.Error(w, "bad cargo download path", http.StatusBadRequest)
		return
	}
	rc, fi, err := l.store.OpenCrate(name, version)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer rc.Close()
	writeFile(w, r, "application/x-tar", rc, fi)
}

func (l *Local) publishPut(w http.ResponseWriter, r *http.Request) {
	if !l.allowPush {
		http.Error(w, "push disabled", http.StatusMethodNotAllowed)
		return
	}
	name, version, ok := parseDownload(r.URL.Path)
	if !ok {
		http.Error(w, "expected /api/v1/crates/{crate}/{version}/download", http.StatusBadRequest)
		return
	}
	max := l.maxUpload
	if max == 0 {
		max = 512 * 1024 * 1024
	}
	body, err := readLimited(r.Body, max)
	if err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	if err := l.store.WriteCrate(name, version, body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	events.EmitSafe(l.emitter, hook.Event{Type: hook.EventRegistryPublished, Mount: l.namespace, Path: l.name + "/" + norm(name) + "@" + version, Protocol: "registry-cargo", Size: int64(len(body))})
	w.WriteHeader(http.StatusCreated)
}

type Remote struct {
	namespace  string
	name       string
	store      *Store
	client     *upstream.Client
	mutableTTL time.Duration
}

func NewRemoteFactory() registry.Factory {
	return func(_ context.Context, deps registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		b, err := upstream.BuildRemote(deps, "cargo/remote", ns, r, 5*time.Minute)
		if err != nil {
			return nil, err
		}
		return &Remote{namespace: ns, name: r.Name, store: NewStore(b.FS, b.BasePath), client: b.Client, mutableTTL: b.MutableTTL}, nil
	}
}

func (rr *Remote) Namespace() string { return rr.namespace }
func (rr *Remote) Name() string      { return rr.name }
func (rr *Remote) Type() string      { return service.RegistryTypeCargo }
func (rr *Remote) Kind() string      { return service.RegistryKindRemote }
func (rr *Remote) Store() *Store     { return rr.store }
func (rr *Remote) Close() error {
	if rr.client != nil {
		return rr.client.Close()
	}
	return nil
}
func (rr *Remote) Stats(ctx context.Context) (registry.Stats, error) {
	return (&Local{store: rr.store}).Stats(ctx)
}
func (rr *Remote) PackageDetail(_ context.Context, name string) (*registry.PackageDetail, error) {
	return packageDetail(rr.store, name)
}
func (rr *Remote) ProbeUpstream(ctx context.Context) (registry.UpstreamHealth, error) {
	return upstream.Probe(ctx, rr.client, "/config.json"), nil
}
func (rr *Remote) PurgeCache(_ context.Context, opts registry.PurgeOptions) (registry.PurgeStats, error) {
	count, bytes, errs := rr.store.PurgeMutable()
	if opts.All {
		count, bytes, errs = rr.store.PurgeAll()
	}
	out := registry.PurgeStats{PurgedFiles: count, PurgedBytes: bytes}
	for _, err := range errs {
		out.Errors = append(out.Errors, err.Error())
	}
	return out, nil
}

func (rr *Remote) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "remote registry is read-only", http.StatusMethodNotAllowed)
		return
	}
	switch {
	case r.URL.Path == "/config.json":
		serveConfig(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/crates/"):
		rr.serveRemoteDownload(w, r)
	default:
		rr.serveRemoteIndex(w, r)
	}
}

func (rr *Remote) serveRemoteIndex(w http.ResponseWriter, r *http.Request) {
	idx := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if idx == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if rc, fi, err := rr.store.fs.Open(rr.store.indexFilePath(idx)); err == nil && (rr.mutableTTL <= 0 || time.Since(fi.ModTime) < rr.mutableTTL) {
		defer rc.Close()
		writeFile(w, r, "text/plain; charset=utf-8", rc, fi)
		return
	}
	resp, err := rr.client.Get(r.Context(), "/"+idx)
	if err != nil {
		if errors.Is(err, upstream.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "read upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	_ = rr.store.WriteIndex(idx, body)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func (rr *Remote) serveRemoteDownload(w http.ResponseWriter, r *http.Request) {
	name, version, ok := parseDownload(r.URL.Path)
	if !ok {
		http.Error(w, "bad cargo download path", http.StatusBadRequest)
		return
	}
	if rc, fi, err := rr.store.OpenCrate(name, version); err == nil {
		defer rc.Close()
		writeFile(w, r, "application/x-tar", rc, fi)
		return
	}
	crateURL := fmt.Sprintf("https://static.crates.io/crates/%s/%s-%s.crate", norm(name), norm(name), version)
	resp, err := rr.client.Get(r.Context(), crateURL)
	if err != nil {
		http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "read upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	_ = rr.store.WriteCrate(name, version, body)
	w.Header().Set("Content-Type", "application/x-tar")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

type Virtual struct{ *virtualbase.Base }

func NewVirtualFactory(resolver virtualbase.Resolver) registry.Factory {
	return func(_ context.Context, _ registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		if len(r.Members) == 0 {
			return nil, fmt.Errorf("cargo/virtual %s/%s: members required", ns, r.Name)
		}
		return &Virtual{Base: virtualbase.New(ns, r.Name, r.Members, resolver)}, nil
	}
}

func (v *Virtual) Type() string { return service.RegistryTypeCargo }
func (v *Virtual) PackageDetail(ctx context.Context, name string) (*registry.PackageDetail, error) {
	return v.DelegatePackageDetail(ctx, name)
}
func (v *Virtual) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !v.ServeFirstHit(w, r) {
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func serveConfig(w http.ResponseWriter, r *http.Request) {
	prefix := strings.TrimRight(r.Header.Get("X-Pika-Registry-Prefix"), "/")
	body, _ := json.Marshal(map[string]string{
		"dl":  prefix + "/api/v1/crates/{crate}/{version}/download",
		"api": prefix,
	})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func parseDownload(p string) (string, string, bool) {
	parts := strings.Split(strings.TrimPrefix(p, "/api/v1/crates/"), "/")
	if len(parts) != 3 || parts[2] != "download" || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func packageDetail(s *Store, name string) (*registry.PackageDetail, error) {
	ents, _ := s.ReadIndexByName(name)
	if len(ents) == 0 {
		return nil, registry.ErrPackageNotFound
	}
	detail := &registry.CargoCrateDetail{}
	for _, ent := range ents {
		row := registry.CargoVersionDetail{Version: ent.Version, Yanked: ent.Yanked, CKSum: ent.CKSum}
		if fi, err := s.fs.Stat(s.cratePath(ent.Name, ent.Version)); err == nil {
			row.Size = fi.Size
		}
		detail.Versions = append(detail.Versions, row)
	}
	if len(detail.Versions) > 0 {
		detail.LatestVersion = detail.Versions[len(detail.Versions)-1].Version
	}
	return &registry.PackageDetail{Type: service.RegistryTypeCargo, Name: norm(name), Cargo: detail}, nil
}

func writeFile(w http.ResponseWriter, r *http.Request, contentType string, rc io.Reader, fi *rawfs.FileInfo) {
	w.Header().Set("Content-Type", contentType)
	if fi != nil {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fi.Size))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, rc)
	}
}

func readLimited(r io.Reader, max int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("upload too large")
	}
	return body, nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "not found") || strings.Contains(low, "no such file") || strings.Contains(low, "does not exist")
}
