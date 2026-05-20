package maven

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
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

// Store is a path-keyed Maven repository layout rooted at BasePath.
// Maven already defines a static filesystem contract, so we preserve
// incoming paths exactly after cleaning traversal attempts.
type Store struct {
	fs       rawfs.RawFS
	basePath string
}

func NewStore(fs rawfs.RawFS, basePath string) *Store {
	return &Store{fs: fs, basePath: strings.Trim(basePath, "/")}
}

func (s *Store) RawFS() rawfs.RawFS { return s.fs }

func (s *Store) join(rel string) string {
	rel = cleanRel(rel)
	if s.basePath == "" {
		return rel
	}
	if rel == "" {
		return s.basePath
	}
	return path.Join(s.basePath, rel)
}

func cleanRel(p string) string {
	p = strings.TrimPrefix(p, "/")
	p = path.Clean("/" + p)
	p = strings.TrimPrefix(p, "/")
	if p == "." {
		return ""
	}
	return p
}

func validRel(p string) bool {
	return cleanRel(p) != "" && !strings.Contains(cleanRel(p), "../")
}

func (s *Store) Open(rel string) (rawfs.ReadSeekCloser, *rawfs.FileInfo, error) {
	rc, fi, err := s.fs.Open(s.join(rel))
	if err != nil {
		if isNotFound(err) {
			return nil, nil, registry.ErrPackageNotFound
		}
		return nil, nil, err
	}
	return rc, fi, nil
}

func (s *Store) Write(rel string, body []byte) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("maven: backend read-only")
	}
	return wfs.Write(s.join(rel), bytes.NewReader(body), int64(len(body)))
}

func (s *Store) Delete(rel string) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("maven: backend read-only")
	}
	return wfs.Delete(s.join(rel))
}

type File struct {
	Path    string
	Size    int64
	ModTime time.Time
}

func (s *Store) ListFiles(prefix string) ([]File, error) {
	root := s.join(prefix)
	var out []File
	var walk func(abs, rel string) error
	walk = func(abs, rel string) error {
		entries, err := s.fs.ReadDir(abs)
		if err != nil {
			if isNotFound(err) {
				return nil
			}
			return err
		}
		for _, e := range entries {
			childAbs := path.Join(abs, e.Name)
			childRel := path.Join(rel, e.Name)
			if e.IsDir {
				if err := walk(childAbs, childRel); err != nil {
					return err
				}
				continue
			}
			out = append(out, File{Path: childRel, Size: e.Size})
		}
		return nil
	}
	if err := walk(root, cleanRel(prefix)); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

type Artifact struct {
	GroupID    string   `json:"group_id"`
	ArtifactID string   `json:"artifact_id"`
	Versions   []string `json:"versions"`
}

func (s *Store) ListArtifacts() ([]Artifact, error) {
	files, err := s.ListFiles("")
	if err != nil {
		return nil, err
	}
	versions := map[string]map[string]struct{}{}
	meta := map[string]Artifact{}
	for _, f := range files {
		parts := strings.Split(cleanRel(f.Path), "/")
		if len(parts) < 4 {
			continue
		}
		artifact := parts[len(parts)-3]
		version := parts[len(parts)-2]
		filename := parts[len(parts)-1]
		if !strings.HasPrefix(filename, artifact+"-"+version) && filename != "maven-metadata.xml" {
			continue
		}
		group := strings.Join(parts[:len(parts)-3], ".")
		key := group + ":" + artifact
		if versions[key] == nil {
			versions[key] = map[string]struct{}{}
			meta[key] = Artifact{GroupID: group, ArtifactID: artifact}
		}
		if version != "" && version != "maven-metadata.xml" {
			versions[key][version] = struct{}{}
		}
	}
	out := make([]Artifact, 0, len(meta))
	for key, a := range meta {
		for v := range versions[key] {
			a.Versions = append(a.Versions, v)
		}
		sort.Strings(a.Versions)
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GroupID == out[j].GroupID {
			return out[i].ArtifactID < out[j].ArtifactID
		}
		return out[i].GroupID < out[j].GroupID
	})
	return out, nil
}

func (s *Store) Count() (artifacts, versions, files int, bytes int64) {
	arts, _ := s.ListArtifacts()
	artifacts = len(arts)
	for _, a := range arts {
		versions += len(a.Versions)
	}
	all, _ := s.ListFiles("")
	files = len(all)
	for _, f := range all {
		bytes += f.Size
	}
	return
}

func (s *Store) PurgeAll() (int, int64, []error) {
	return s.purge(func(File) bool { return true })
}

func (s *Store) PurgeMutable() (int, int64, []error) {
	return s.purge(func(f File) bool { return isMutablePath(f.Path) })
}

func (s *Store) purge(match func(File) bool) (int, int64, []error) {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return 0, 0, []error{fmt.Errorf("maven: backend read-only")}
	}
	files, err := s.ListFiles("")
	if err != nil {
		return 0, 0, []error{err}
	}
	var count int
	var bytes int64
	var errs []error
	for _, f := range files {
		if !match(f) {
			continue
		}
		if err := wfs.Delete(s.join(f.Path)); err != nil && !isNotFound(err) {
			errs = append(errs, err)
			continue
		}
		count++
		bytes += f.Size
	}
	return count, bytes, errs
}

// Local is a hosted Maven repository. It supports the standard static
// GET/HEAD layout plus PUT/DELETE for deployments.
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
			return nil, fmt.Errorf("maven/local %s/%s: %w", ns, r.Name, err)
		}
		return &Local{namespace: ns, name: r.Name, store: NewStore(fs, r.BasePath), allowPush: r.AllowPush, maxUpload: r.MaxUploadSize, emitter: deps.Emitter}, nil
	}
}

func (l *Local) Namespace() string { return l.namespace }
func (l *Local) Name() string      { return l.name }
func (l *Local) Type() string      { return service.RegistryTypeMaven }
func (l *Local) Kind() string      { return service.RegistryKindLocal }
func (l *Local) Store() *Store     { return l.store }
func (l *Local) Close() error      { return nil }

func (l *Local) Stats(context.Context) (registry.Stats, error) {
	artifacts, versions, files, bytes := l.store.Count()
	return registry.Stats{PackageCount: artifacts, VersionCount: versions, BlobCount: files, TotalBytes: bytes}, nil
}

func (l *Local) PackageDetail(_ context.Context, name string) (*registry.PackageDetail, error) {
	return packageDetail(l.store, name)
}

func (l *Local) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		serveFile(w, r, l.store, r.URL.Path)
	case http.MethodPut:
		l.put(w, r)
	case http.MethodDelete:
		if !l.allowPush {
			http.Error(w, "delete disabled", http.StatusMethodNotAllowed)
			return
		}
		if err := l.store.Delete(r.URL.Path); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (l *Local) put(w http.ResponseWriter, r *http.Request) {
	if !l.allowPush {
		http.Error(w, "push disabled", http.StatusMethodNotAllowed)
		return
	}
	if !validRel(r.URL.Path) {
		http.Error(w, "invalid maven path", http.StatusBadRequest)
		return
	}
	max := l.maxUpload
	if max == 0 {
		max = 512 * 1024 * 1024
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if int64(len(body)) > max {
		http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
		return
	}
	if err := l.store.Write(r.URL.Path, body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	events.EmitSafe(l.emitter, hook.Event{Type: hook.EventRegistryPublished, Mount: l.namespace, Path: l.name + "/" + cleanRel(r.URL.Path), Protocol: "registry-maven", Size: int64(len(body))})
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
		b, err := upstream.BuildRemote(deps, "maven/remote", ns, r, 5*time.Minute)
		if err != nil {
			return nil, err
		}
		return &Remote{namespace: ns, name: r.Name, store: NewStore(b.FS, b.BasePath), client: b.Client, mutableTTL: b.MutableTTL}, nil
	}
}

func (rr *Remote) Namespace() string { return rr.namespace }
func (rr *Remote) Name() string      { return rr.name }
func (rr *Remote) Type() string      { return service.RegistryTypeMaven }
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
	return upstream.Probe(ctx, rr.client, "/"), nil
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
	rel := cleanRel(r.URL.Path)
	if rel == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if rc, fi, err := rr.store.Open(rel); err == nil {
		fresh := !isMutablePath(rel) || rr.mutableTTL <= 0 || time.Since(fi.ModTime) < rr.mutableTTL
		if fresh {
			defer rc.Close()
			writeFileResponse(w, r, rel, rc, fi)
			return
		}
		rc.Close()
	}
	resp, err := rr.client.Get(r.Context(), "/"+rel)
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
	_ = rr.store.Write(rel, body)
	if resp.ContentType != "" {
		w.Header().Set("Content-Type", resp.ContentType)
	} else {
		setContentType(w, rel)
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

type Virtual struct{ *virtualbase.Base }

func NewVirtualFactory(resolver virtualbase.Resolver) registry.Factory {
	return func(_ context.Context, _ registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		if len(r.Members) == 0 {
			return nil, fmt.Errorf("maven/virtual %s/%s: members required", ns, r.Name)
		}
		return &Virtual{Base: virtualbase.New(ns, r.Name, r.Members, resolver)}, nil
	}
}

func (v *Virtual) Type() string { return service.RegistryTypeMaven }
func (v *Virtual) PackageDetail(ctx context.Context, name string) (*registry.PackageDetail, error) {
	return v.DelegatePackageDetail(ctx, name)
}
func (v *Virtual) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !v.ServeFirstHit(w, r) {
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func serveFile(w http.ResponseWriter, r *http.Request, s *Store, rel string) {
	rc, fi, err := s.Open(rel)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer rc.Close()
	writeFileResponse(w, r, rel, rc, fi)
}

func writeFileResponse(w http.ResponseWriter, r *http.Request, rel string, rc io.Reader, fi *rawfs.FileInfo) {
	setContentType(w, rel)
	if fi != nil && fi.Size >= 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fi.Size))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, rc)
	}
}

func setContentType(w http.ResponseWriter, rel string) {
	if ct := mime.TypeByExtension(path.Ext(rel)); ct != "" {
		w.Header().Set("Content-Type", ct)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
}

func isMutablePath(p string) bool {
	base := path.Base(p)
	return base == "maven-metadata.xml" || strings.HasPrefix(base, "maven-metadata.xml.")
}

func packageDetail(s *Store, name string) (*registry.PackageDetail, error) {
	group, artifact := splitName(name)
	if group == "" || artifact == "" {
		return nil, registry.ErrPackageNotFound
	}
	arts, err := s.ListArtifacts()
	if err != nil {
		return nil, err
	}
	for _, a := range arts {
		if a.GroupID != group || a.ArtifactID != artifact {
			continue
		}
		detail := &registry.MavenArtifactDetail{GroupID: group, ArtifactID: artifact}
		for _, v := range a.Versions {
			detail.Versions = append(detail.Versions, registry.MavenVersionDetail{
				Version: v,
				JarSize: fileSize(s, group, artifact, v, ".jar"),
				PomSize: fileSize(s, group, artifact, v, ".pom"),
			})
		}
		if len(detail.Versions) > 0 {
			detail.LatestVersion = detail.Versions[len(detail.Versions)-1].Version
		}
		return &registry.PackageDetail{Type: service.RegistryTypeMaven, Name: group + ":" + artifact, Maven: detail}, nil
	}
	return nil, registry.ErrPackageNotFound
}

func splitName(name string) (string, string) {
	if strings.Contains(name, ":") {
		parts := strings.SplitN(name, ":", 2)
		return parts[0], parts[1]
	}
	parts := strings.Split(cleanRel(name), "/")
	if len(parts) < 2 {
		return "", ""
	}
	return strings.Join(parts[:len(parts)-1], "."), parts[len(parts)-1]
}

func fileSize(s *Store, group, artifact, version, ext string) int64 {
	rel := strings.ReplaceAll(group, ".", "/") + "/" + artifact + "/" + version + "/" + artifact + "-" + version + ext
	fi, err := s.fs.Stat(s.join(rel))
	if err != nil {
		return 0
	}
	return fi.Size
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "not found") || strings.Contains(low, "no such file") || strings.Contains(low, "does not exist")
}
