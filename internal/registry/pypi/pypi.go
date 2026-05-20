package pypi

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
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

func normalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	return normalizeRE.ReplaceAllString(name, "-")
}

var normalizeRE = regexp.MustCompile(`[-_.]+`)
var filenameVersionRE = regexp.MustCompile(`^(.+?)-([0-9][A-Za-z0-9.!+_]*)(?:-|$)`)

func InferNameVersion(filename string) (string, string) {
	base := path.Base(filename)
	for _, suffix := range []string{".tar.gz", ".whl", ".zip", ".tar.bz2", ".tgz"} {
		base = strings.TrimSuffix(base, suffix)
	}
	if m := filenameVersionRE.FindStringSubmatch(base); len(m) == 3 {
		return normalizeName(m[1]), m[2]
	}
	return "", ""
}

func (s *Store) ListVersions(name string) ([]string, error) {
	files, err := s.ListPackageFiles(name)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	for _, f := range files {
		_, v := InferNameVersion(f.Name)
		if v != "" {
			seen[v] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out, nil
}

func (s *Store) packagePath(name, filename string) string {
	return s.join("packages", normalizeName(name), path.Base(filename))
}

func (s *Store) remotePath(encoded string) string { return s.join("remote", encoded) }
func (s *Store) simplePath(name string) string    { return s.join("simple", normalizeName(name)+".html") }

func (s *Store) WritePackage(name, filename string, body []byte) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("pypi: backend read-only")
	}
	return wfs.Write(s.packagePath(name, filename), bytes.NewReader(body), int64(len(body)))
}

func (s *Store) OpenPackage(name, filename string) (rawfs.ReadSeekCloser, *rawfs.FileInfo, error) {
	rc, fi, err := s.fs.Open(s.packagePath(name, filename))
	if err != nil {
		return nil, nil, err
	}
	return rc, fi, nil
}

func (s *Store) ListPackages() ([]string, error) {
	entries, err := s.fs.ReadDir(s.join("packages"))
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir {
			out = append(out, e.Name)
		}
	}
	sort.Strings(out)
	return out, nil
}

type File struct {
	Name string
	Size int64
}

func (s *Store) ListPackageFiles(name string) ([]File, error) {
	entries, err := s.fs.ReadDir(s.join("packages", normalizeName(name)))
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []File
	for _, e := range entries {
		if !e.IsDir {
			out = append(out, File{Name: e.Name, Size: e.Size})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Store) Count() (packages, versions, files int, bytes int64) {
	names, _ := s.ListPackages()
	packages = len(names)
	for _, name := range names {
		seen := map[string]struct{}{}
		fs, _ := s.ListPackageFiles(name)
		files += len(fs)
		for _, f := range fs {
			_, v := InferNameVersion(f.Name)
			if v != "" {
				seen[v] = struct{}{}
			}
			bytes += f.Size
		}
		versions += len(seen)
	}
	return
}

func (s *Store) WriteRemote(encoded string, body []byte) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("pypi: backend read-only")
	}
	return wfs.Write(s.remotePath(encoded), bytes.NewReader(body), int64(len(body)))
}

func (s *Store) OpenRemote(encoded string) (rawfs.ReadSeekCloser, *rawfs.FileInfo, error) {
	return s.fs.Open(s.remotePath(encoded))
}

func (s *Store) WriteSimple(name string, body []byte) error {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return fmt.Errorf("pypi: backend read-only")
	}
	return wfs.Write(s.simplePath(name), bytes.NewReader(body), int64(len(body)))
}

func (s *Store) OpenSimple(name string) (rawfs.ReadSeekCloser, *rawfs.FileInfo, error) {
	return s.fs.Open(s.simplePath(name))
}

func (s *Store) PurgeAll() (int, int64, []error)     { return s.purge("") }
func (s *Store) PurgeMutable() (int, int64, []error) { return s.purge("simple") }

func (s *Store) purge(prefix string) (int, int64, []error) {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return 0, 0, []error{fmt.Errorf("pypi: backend read-only")}
	}
	root := s.join(prefix)
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
	walk(root)
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
			return nil, fmt.Errorf("pypi/local %s/%s: %w", ns, r.Name, err)
		}
		return &Local{namespace: ns, name: r.Name, store: NewStore(fs, r.BasePath), allowPush: r.AllowPush, maxUpload: r.MaxUploadSize, emitter: deps.Emitter}, nil
	}
}

func (l *Local) Namespace() string { return l.namespace }
func (l *Local) Name() string      { return l.name }
func (l *Local) Type() string      { return service.RegistryTypePyPI }
func (l *Local) Kind() string      { return service.RegistryKindLocal }
func (l *Local) Store() *Store     { return l.store }
func (l *Local) Close() error      { return nil }

func (l *Local) Stats(context.Context) (registry.Stats, error) {
	packages, versions, files, bytes := l.store.Count()
	return registry.Stats{PackageCount: packages, VersionCount: versions, BlobCount: files, TotalBytes: bytes}, nil
}
func (l *Local) PackageDetail(_ context.Context, name string) (*registry.PackageDetail, error) {
	return packageDetail(l.store, name)
}

func (l *Local) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) && (p == "/simple" || p == ""):
		l.serveSimpleRoot(w, r)
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.HasPrefix(p, "/simple/"):
		l.serveSimplePackage(w, r, strings.TrimPrefix(p, "/simple/"))
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.HasPrefix(p, "/packages/"):
		l.servePackageFile(w, r)
	case r.Method == http.MethodPost && (p == "" || p == "/legacy"):
		l.publishMultipart(w, r)
	case r.Method == http.MethodPut && strings.HasPrefix(p, "/packages/"):
		l.publishPut(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(p, "/packages/"):
		l.deleteFile(w, r)
	default:
		http.Error(w, "no pypi route", http.StatusNotFound)
	}
}

func (l *Local) serveSimpleRoot(w http.ResponseWriter, r *http.Request) {
	names, _ := l.store.ListPackages()
	var b strings.Builder
	b.WriteString("<!doctype html><html><body>\n")
	for _, name := range names {
		fmt.Fprintf(&b, `<a href="%s/">%s</a><br/>`+"\n", html.EscapeString(name), html.EscapeString(name))
	}
	b.WriteString("</body></html>\n")
	writeHTML(w, r, []byte(b.String()))
}

func (l *Local) serveSimplePackage(w http.ResponseWriter, r *http.Request, name string) {
	name = normalizeName(name)
	files, _ := l.store.ListPackageFiles(name)
	if len(files) == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	prefix := strings.TrimRight(r.Header.Get("X-Pika-Registry-Prefix"), "/")
	var b strings.Builder
	b.WriteString("<!doctype html><html><body>\n")
	for _, f := range files {
		href := prefix + "/packages/" + url.PathEscape(name) + "/" + url.PathEscape(f.Name)
		fmt.Fprintf(&b, `<a href="%s">%s</a><br/>`+"\n", html.EscapeString(href), html.EscapeString(f.Name))
	}
	b.WriteString("</body></html>\n")
	writeHTML(w, r, []byte(b.String()))
}

func (l *Local) servePackageFile(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/packages/"), "/")
	if len(parts) < 2 {
		http.Error(w, "expected /packages/{name}/{filename}", http.StatusBadRequest)
		return
	}
	rc, fi, err := l.store.OpenPackage(parts[0], parts[1])
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer rc.Close()
	writeFile(w, r, parts[1], rc, fi)
}

func (l *Local) publishMultipart(w http.ResponseWriter, r *http.Request) {
	if !l.allowPush {
		http.Error(w, "push disabled", http.StatusMethodNotAllowed)
		return
	}
	max := l.maxUpload
	if max == 0 {
		max = 512 * 1024 * 1024
	}
	if err := r.ParseMultipartForm(max); err != nil {
		http.Error(w, "parse multipart: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, hdr, err := r.FormFile("content")
	if err != nil {
		http.Error(w, "missing content file", http.StatusBadRequest)
		return
	}
	defer file.Close()
	body, err := readLimited(file, max)
	if err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	name := r.FormValue("name")
	if name == "" {
		name, _ = InferNameVersion(hdr.Filename)
	}
	if name == "" {
		http.Error(w, "missing package name", http.StatusBadRequest)
		return
	}
	l.finishPublish(w, normalizeName(name), hdr.Filename, body)
}

func (l *Local) publishPut(w http.ResponseWriter, r *http.Request) {
	if !l.allowPush {
		http.Error(w, "push disabled", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/packages/"), "/")
	var name, filename string
	if len(parts) >= 2 {
		name, filename = parts[0], parts[1]
	} else if len(parts) == 1 {
		filename = parts[0]
		name, _ = InferNameVersion(filename)
	}
	if name == "" || filename == "" {
		http.Error(w, "expected /packages/{name}/{filename}", http.StatusBadRequest)
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
	l.finishPublish(w, normalizeName(name), filename, body)
}

func (l *Local) finishPublish(w http.ResponseWriter, name, filename string, body []byte) {
	if err := l.store.WritePackage(name, filename, body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	events.EmitSafe(l.emitter, hook.Event{Type: hook.EventRegistryPublished, Mount: l.namespace, Path: l.name + "/" + name + "/" + filename, Protocol: "registry-pypi", Size: int64(len(body))})
	w.WriteHeader(http.StatusCreated)
}

func (l *Local) deleteFile(w http.ResponseWriter, r *http.Request) {
	if !l.allowPush {
		http.Error(w, "delete disabled", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/packages/"), "/")
	if len(parts) < 2 {
		http.Error(w, "expected /packages/{name}/{filename}", http.StatusBadRequest)
		return
	}
	wfs, ok := l.store.fs.(rawfs.WritableRawFS)
	if !ok {
		http.Error(w, "backend read-only", http.StatusInternalServerError)
		return
	}
	if err := wfs.Delete(l.store.packagePath(parts[0], parts[1])); err != nil && !isNotFound(err) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		b, err := upstream.BuildRemote(deps, "pypi/remote", ns, r, 5*time.Minute)
		if err != nil {
			return nil, err
		}
		return &Remote{namespace: ns, name: r.Name, store: NewStore(b.FS, b.BasePath), client: b.Client, mutableTTL: b.MutableTTL}, nil
	}
}

func (rr *Remote) Namespace() string { return rr.namespace }
func (rr *Remote) Name() string      { return rr.name }
func (rr *Remote) Type() string      { return service.RegistryTypePyPI }
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
	return upstream.Probe(ctx, rr.client, "/simple/"), nil
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
	p := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case p == "/simple" || p == "":
		rr.serveRemoteSimple(w, r, "")
	case strings.HasPrefix(p, "/simple/"):
		rr.serveRemoteSimple(w, r, strings.TrimPrefix(p, "/simple/"))
	case strings.HasPrefix(p, "/_remote/"):
		rr.serveRemoteFile(w, r, strings.TrimPrefix(p, "/_remote/"))
	default:
		http.Error(w, "no pypi route", http.StatusNotFound)
	}
}

func (rr *Remote) serveRemoteSimple(w http.ResponseWriter, r *http.Request, name string) {
	cacheName := name
	if cacheName == "" {
		cacheName = "_root"
	}
	if rc, fi, err := rr.store.OpenSimple(cacheName); err == nil && (rr.mutableTTL <= 0 || time.Since(fi.ModTime) < rr.mutableTTL) {
		defer rc.Close()
		body, _ := io.ReadAll(rc)
		writeHTML(w, r, rr.rewriteSimple(r, body, name))
		return
	}
	upath := "/simple/"
	if name != "" {
		upath += url.PathEscape(normalizeName(name)) + "/"
	}
	resp, err := rr.client.Get(r.Context(), upath)
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
	_ = rr.store.WriteSimple(cacheName, body)
	writeHTML(w, r, rr.rewriteSimple(r, body, name))
}

func (rr *Remote) rewriteSimple(r *http.Request, body []byte, name string) []byte {
	prefix := strings.TrimRight(r.Header.Get("X-Pika-Registry-Prefix"), "/")
	base, _ := url.Parse(rr.client.BaseURL() + "/simple/")
	if name != "" {
		base, _ = url.Parse(rr.client.BaseURL() + "/simple/" + normalizeName(name) + "/")
	}
	re := regexp.MustCompile(`href=["']([^"']+)["']`)
	return re.ReplaceAllFunc(body, func(m []byte) []byte {
		parts := re.FindSubmatch(m)
		if len(parts) != 2 {
			return m
		}
		raw := html.UnescapeString(string(parts[1]))
		u, err := url.Parse(raw)
		if err != nil {
			return m
		}
		abs := base.ResolveReference(u)
		frag := abs.Fragment
		abs.Fragment = ""
		encoded := base64.RawURLEncoding.EncodeToString([]byte(abs.String()))
		href := prefix + "/_remote/" + encoded
		if frag != "" {
			href += "#" + frag
		}
		return []byte(`href="` + html.EscapeString(href) + `"`)
	})
}

func (rr *Remote) serveRemoteFile(w http.ResponseWriter, r *http.Request, encoded string) {
	if rc, fi, err := rr.store.OpenRemote(encoded); err == nil {
		defer rc.Close()
		writeFile(w, r, encoded, rc, fi)
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		http.Error(w, "bad remote file key", http.StatusBadRequest)
		return
	}
	resp, err := rr.client.Get(r.Context(), string(raw))
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
	_ = rr.store.WriteRemote(encoded, body)
	if resp.ContentType != "" {
		w.Header().Set("Content-Type", resp.ContentType)
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
			return nil, fmt.Errorf("pypi/virtual %s/%s: members required", ns, r.Name)
		}
		return &Virtual{Base: virtualbase.New(ns, r.Name, r.Members, resolver)}, nil
	}
}

func (v *Virtual) Type() string { return service.RegistryTypePyPI }
func (v *Virtual) PackageDetail(ctx context.Context, name string) (*registry.PackageDetail, error) {
	return v.DelegatePackageDetail(ctx, name)
}
func (v *Virtual) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !v.ServeFirstHit(w, r) {
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func packageDetail(s *Store, name string) (*registry.PackageDetail, error) {
	name = normalizeName(name)
	files, _ := s.ListPackageFiles(name)
	if len(files) == 0 {
		return nil, registry.ErrPackageNotFound
	}
	byVersion := map[string]*registry.PyPIVersionDetail{}
	for _, f := range files {
		_, v := InferNameVersion(f.Name)
		if v == "" {
			v = "unknown"
		}
		row := byVersion[v]
		if row == nil {
			row = &registry.PyPIVersionDetail{Version: v}
			byVersion[v] = row
		}
		row.Files = append(row.Files, f.Name)
		row.FileSize += f.Size
	}
	versions := make([]string, 0, len(byVersion))
	for v := range byVersion {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	detail := &registry.PyPIPackageDetail{}
	for _, v := range versions {
		detail.Versions = append(detail.Versions, *byVersion[v])
	}
	if len(versions) > 0 {
		detail.LatestVersion = versions[len(versions)-1]
	}
	return &registry.PackageDetail{Type: service.RegistryTypePyPI, Name: name, PyPI: detail}, nil
}

func writeHTML(w http.ResponseWriter, r *http.Request, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func writeFile(w http.ResponseWriter, r *http.Request, filename string, rc io.Reader, fi *rawfs.FileInfo) {
	if ct := mime.TypeByExtension(path.Ext(filename)); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
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
