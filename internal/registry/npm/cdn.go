package npm

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/common"
	"github.com/rakunlabs/pika/internal/registry/virtualbase"
)

const cdnDefaultMutableTTL = 60 * time.Second

// ParseCDNAssetPath parses jsDelivr-style NPM package file paths:
//
//	lodash@4.17.21/lodash.js
//	@scope/pkg@1.0.0/dist/index.js
//	lodash/lodash.js              (latest)
//	@scope/pkg/dist/index.js      (latest)
//
// The returned Path is rooted inside the package tarball after the
// conventional leading "package/" directory is stripped.
func ParseCDNAssetPath(p string) (registry.CDNAssetRequest, error) {
	p = strings.TrimPrefix(strings.TrimSpace(p), "/")
	if p == "" {
		return registry.CDNAssetRequest{}, fmt.Errorf("cdn npm: package path is required")
	}
	parts := strings.Split(p, "/")
	var pkg, ref string
	var fileParts []string

	if strings.HasPrefix(parts[0], "@") {
		if len(parts) < 3 {
			return registry.CDNAssetRequest{}, fmt.Errorf("cdn npm: scoped package requires @scope/name/file")
		}
		scope, err := url.PathUnescape(parts[0])
		if err != nil {
			return registry.CDNAssetRequest{}, fmt.Errorf("cdn npm: decode scope: %w", err)
		}
		bare, err := url.PathUnescape(parts[1])
		if err != nil {
			return registry.CDNAssetRequest{}, fmt.Errorf("cdn npm: decode package: %w", err)
		}
		if i := strings.LastIndex(bare, "@"); i > 0 {
			ref = bare[i+1:]
			bare = bare[:i]
		}
		pkg = scope + "/" + bare
		fileParts = parts[2:]
	} else {
		if len(parts) < 2 {
			return registry.CDNAssetRequest{}, fmt.Errorf("cdn npm: package file path is required")
		}
		name, err := url.PathUnescape(parts[0])
		if err != nil {
			return registry.CDNAssetRequest{}, fmt.Errorf("cdn npm: decode package: %w", err)
		}
		if i := strings.LastIndex(name, "@"); i > 0 {
			ref = name[i+1:]
			name = name[:i]
		}
		pkg = name
		fileParts = parts[1:]
	}

	if err := ValidatePackageName(pkg); err != nil {
		return registry.CDNAssetRequest{}, err
	}
	assetPath, err := cleanCDNAssetPath(fileParts)
	if err != nil {
		return registry.CDNAssetRequest{}, err
	}
	if ref, err = url.PathUnescape(ref); err != nil {
		return registry.CDNAssetRequest{}, fmt.Errorf("cdn npm: decode version: %w", err)
	}
	return registry.CDNAssetRequest{Package: pkg, Version: ref, Path: assetPath}, nil
}

func cleanCDNAssetPath(parts []string) (string, error) {
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return "", fmt.Errorf("cdn npm: empty file path segment")
		}
		dec, err := url.PathUnescape(part)
		if err != nil {
			return "", fmt.Errorf("cdn npm: decode file path: %w", err)
		}
		if dec == "." || dec == ".." || strings.Contains(dec, "/") {
			return "", fmt.Errorf("cdn npm: invalid file path segment %q", dec)
		}
		clean = append(clean, dec)
	}
	if len(clean) == 0 {
		return "", fmt.Errorf("cdn npm: package file path is required")
	}
	return strings.Join(clean, "/"), nil
}

// ServeCDNAsset implements registry.CDNAssetProvider for Local.
func (l *Local) ServeCDNAsset(w http.ResponseWriter, r *http.Request, asset registry.CDNAssetRequest) {
	serveCDNAssetFromStore(w, r, l.store, asset, 0)
}

// ServeCDNAsset implements registry.CDNAssetProvider for Remote. It
// lazily warms the packument and tarball cache so a first CDN hit can
// populate an otherwise cold remote mirror.
func (rr *Remote) ServeCDNAsset(w http.ResponseWriter, r *http.Request, asset registry.CDNAssetRequest) {
	if refLooksMutable(asset.Version) && !rr.packumentFresh(asset.Package) {
		_, _, _ = rr.sf.Do("packument:"+asset.Package, func() (any, error) {
			return nil, rr.refetchPackument(r, asset.Package)
		})
	}
	resolved, mutable, meta, err := resolveCDNVersionMeta(rr.store, asset.Package, asset.Version)
	if err != nil {
		_, _, _ = rr.sf.Do("packument:"+asset.Package, func() (any, error) {
			return nil, rr.refetchPackument(r, asset.Package)
		})
		resolved, mutable, meta, err = resolveCDNVersionMeta(rr.store, asset.Package, asset.Version)
		if err != nil {
			writeNotFound(w, asset.Package+": "+err.Error())
			return
		}
	}
	file := tarballFilenameFromMeta(meta)
	if file == "" {
		writeNotFound(w, asset.Package+"@"+resolved+": tarball metadata missing")
		return
	}
	if rc, _, err := rr.store.OpenTarball(asset.Package, file); err == nil {
		_ = rc.Close()
	} else {
		_, _, _ = rr.sf.Do("tarball:"+asset.Package+"/"+file, func() (any, error) {
			return nil, rr.refetchTarball(r.Context(), asset.Package, file)
		})
	}
	serveCDNFile(w, r, rr.store, asset.Package, resolved, file, asset.Path, mutable, rr.mutableTTL)
}

// ServeCDNAsset implements registry.CDNAssetProvider for Virtual by
// trying member registries in order. First successful member wins,
// matching Virtual's tarball lookup semantics.
func (v *Virtual) ServeCDNAsset(w http.ResponseWriter, r *http.Request, asset registry.CDNAssetRequest) {
	served := false
	v.ForEachMember(func(mem registry.Registry) bool {
		provider, ok := mem.(registry.CDNAssetProvider)
		if !ok {
			return false
		}
		rec := httptest.NewRecorder()
		provider.ServeCDNAsset(rec, r, asset)
		if rec.Code >= 200 && rec.Code < 300 {
			virtualbase.CopyHeaders(w.Header(), rec.Header())
			w.WriteHeader(rec.Code)
			_, _ = w.Write(rec.Body.Bytes())
			served = true
			return true
		}
		return false
	})
	if !served {
		writeNotFound(w, asset.Package+": CDN asset not found in virtual members")
	}
}

func serveCDNAssetFromStore(w http.ResponseWriter, r *http.Request, s *Store, asset registry.CDNAssetRequest, ttl time.Duration) {
	resolved, mutable, meta, err := resolveCDNVersionMeta(s, asset.Package, asset.Version)
	if err != nil {
		writeNotFound(w, asset.Package+": "+err.Error())
		return
	}
	file := tarballFilenameFromMeta(meta)
	if file == "" {
		writeNotFound(w, asset.Package+"@"+resolved+": tarball metadata missing")
		return
	}
	serveCDNFile(w, r, s, asset.Package, resolved, file, asset.Path, mutable, ttl)
}

func resolveCDNVersionMeta(s *Store, name, ref string) (version string, mutable bool, meta map[string]any, err error) {
	if ref == "" {
		ref = "latest"
		mutable = true
	}
	if tags, tagErr := s.ReadDistTags(name); tagErr == nil {
		if tagged := tags[ref]; tagged != "" {
			version = tagged
			mutable = true
		}
	}
	if version == "" {
		version = ref
	}
	if version != "" {
		if meta, err := s.ReadVersionMeta(name, version); err == nil {
			return version, mutable, meta, nil
		}
	}
	if body, ok, _ := s.ReadCachedPackument(name); ok && len(body) > 0 {
		if version, mutable, meta, err = resolveCDNVersionMetaFromPackument(body, ref, mutable); err == nil {
			return version, mutable, meta, nil
		}
	}
	if ref == "latest" {
		versions, listErr := s.ListVersions(name)
		if listErr == nil && len(versions) > 0 {
			version = versions[len(versions)-1]
			if meta, err := s.ReadVersionMeta(name, version); err == nil {
				return version, true, meta, nil
			}
		}
	}
	return "", mutable, nil, fmt.Errorf("version %q not found", ref)
}

func resolveCDNVersionMetaFromPackument(body []byte, ref string, mutable bool) (string, bool, map[string]any, error) {
	var pkg map[string]any
	if err := json.Unmarshal(body, &pkg); err != nil {
		return "", mutable, nil, err
	}
	version := ref
	if tags, ok := pkg["dist-tags"].(map[string]any); ok {
		if tagged, ok := tags[ref].(string); ok && tagged != "" {
			version = tagged
			mutable = true
		}
	}
	versions, ok := pkg["versions"].(map[string]any)
	if !ok {
		return "", mutable, nil, fmt.Errorf("packument has no versions")
	}
	vm, ok := versions[version].(map[string]any)
	if !ok {
		return "", mutable, nil, fmt.Errorf("version %q not found", ref)
	}
	return version, mutable, vm, nil
}

func serveCDNFile(w http.ResponseWriter, r *http.Request, s *Store, name, version, tarballFile, assetPath string, mutable bool, ttl time.Duration) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "CDN assets are read-only")
		return
	}
	rc, _, err := s.OpenTarball(name, tarballFile)
	if err != nil {
		writeNotFound(w, name+"/"+tarballFile+": "+err.Error())
		return
	}
	defer rc.Close()

	gz, err := gzip.NewReader(rc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open tarball: "+err.Error())
		return
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				writeNotFound(w, name+"@"+version+"/"+assetPath+": file not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "read tarball: "+err.Error())
			return
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		entry := cleanTarEntryName(hdr.Name)
		if entry != assetPath {
			continue
		}
		writeCDNFileResponse(w, r, tr, name, version, tarballFile, assetPath, hdr.Size, mutable, ttl)
		return
	}
}

func cleanTarEntryName(name string) string {
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	name = strings.TrimPrefix(name, "package/")
	return name
}

func writeCDNFileResponse(w http.ResponseWriter, r *http.Request, body io.Reader, name, version, tarballFile, assetPath string, size int64, mutable bool, ttl time.Duration) {
	etag := common.EtagFor(name + "@" + version + "/" + assetPath + "/" + tarballFile + fmt.Sprintf("/%d", size))
	if etag != "" {
		w.Header().Set("ETag", etag)
	}
	if common.MatchIfNoneMatch(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if mutable {
		if ttl <= 0 {
			ttl = cdnDefaultMutableTTL
		}
		common.SetMutableCache(w, etag, ttl)
	} else {
		common.SetImmutableCache(w, etag)
	}

	contentType := mime.TypeByExtension(path.Ext(assetPath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, body)
}

func refLooksMutable(ref string) bool {
	return ref == "" || ref == "latest"
}
