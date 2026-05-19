package docker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/blobstore"
	"github.com/rakunlabs/pika/internal/registry/events"
	"github.com/rakunlabs/pika/internal/service"
)

// Local is the hosted-only Docker / OCI registry implementation.
// Reads and writes go entirely against pika storage; no upstream
// pulls happen (remote / pull-through is Faz 5).
//
// The Registry interface's ServeHTTP receives requests whose path
// has had /registries/{ns}/{repo} stripped — so we see "/v2/..."
// directly.
type Local struct {
	namespace string
	name      string
	store     *Store
	signer    TokenSigner

	allowPush bool
	maxUpload int64

	uploads *uploadSessions
	emitter events.Emitter
}

// NewLocalFactory returns the Factory for ("docker", "local").
func NewLocalFactory() registry.Factory {
	return func(_ context.Context, deps registry.Deps, ns string, r *service.RegistryRepository) (registry.Registry, error) {
		fs, err := deps.MountRawFS(r.Mount)
		if err != nil {
			return nil, fmt.Errorf("docker/local %s/%s: mount: %w", ns, r.Name, err)
		}
		blobs, err := deps.MountFor(r.Mount, r.BasePath)
		if err != nil {
			return nil, fmt.Errorf("docker/local %s/%s: blobstore: %w", ns, r.Name, err)
		}
		// Default-signer key is a random per-process value when no
		// pika-wide signer is wired. JWTs issued by this Registry
		// don't survive a restart (clients re-auth on 401), which
		// is acceptable; a future enhancement plumbs the pika
		// encryption key through Deps as the signer source.
		signer := NewStaticSigner(randomKey())

		return &Local{
			namespace: ns,
			name:      r.Name,
			store:     NewStore(fs, blobs, r.BasePath),
			signer:    signer,
			allowPush: r.AllowPush,
			maxUpload: r.MaxUploadSize,
			uploads:   newUploadSessions(),
			emitter:   deps.Emitter,
		}, nil
	}
}

func (l *Local) Namespace() string { return l.namespace }
func (l *Local) Name() string      { return l.name }
func (l *Local) Type() string      { return service.RegistryTypeDocker }
func (l *Local) Kind() string      { return service.RegistryKindLocal }
func (l *Local) Store() *Store     { return l.store }
func (l *Local) Close() error      { return nil }

// PackageDetail implements registry.PackageDetailer.
func (l *Local) PackageDetail(ctx context.Context, name string) (*registry.PackageDetail, error) {
	return buildPackageDetail(ctx, l.store, name)
}

// Stats implements registry.StatsProvider. Walks the on-disk
// repositories and blob store to produce a snapshot count.
func (l *Local) Stats(_ context.Context) (registry.Stats, error) {
	repos, tags, manifests := l.store.CountRepositoriesTagsManifests()
	var (
		blobCount  int
		totalBytes int64
	)
	_ = l.store.Blobs().ListBlobs(func(_ blobstore.Digest, info *blobstore.BlobInfo) error {
		blobCount++
		if info != nil {
			totalBytes += info.Size
		}
		return nil
	})
	return registry.Stats{
		RepositoryCount: repos,
		TagCount:        tags,
		ManifestCount:   manifests,
		BlobCount:       blobCount,
		TotalBytes:      totalBytes,
	}, nil
}

// AllowPush reports whether push operations are enabled.
func (l *Local) AllowPush() bool { return l.allowPush }

// ServeHTTP is the data-mux entry point for /v2/...
func (l *Local) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, ok := classify(r.Method, r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "UNSUPPORTED", "unrecognised docker route: "+r.Method+" "+r.URL.Path)
		return
	}

	switch req.Op {
	case opVersionProbe:
		l.serveVersionProbe(w, r)
	case opToken:
		l.serveToken(w, r)
	case opCatalog:
		l.serveCatalog(w, r)
	case opTagsList:
		l.serveTagsList(w, r, req.Name)
	case opManifest:
		l.serveManifest(w, r, req)
	case opBlob:
		l.serveBlob(w, r, req)
	case opUploadStart:
		l.serveUploadStart(w, r, req.Name)
	case opUploadProgress:
		l.serveUploadProgress(w, r, req)
	case opUploadAppend:
		l.serveUploadAppend(w, r, req)
	case opUploadFinalize:
		l.serveUploadFinalize(w, r, req)
	case opUploadCancel:
		l.serveUploadCancel(w, r, req)
	case opReferrers:
		l.serveReferrers(w, r, req)
	default:
		writeError(w, http.StatusNotFound, "UNSUPPORTED", "operation not implemented")
	}
}

// ─── /v2/ — version probe ─────────────────────────────────────────

// serveVersionProbe handles GET /v2/. Unauthenticated probe → 401
// + WWW-Authenticate. Authenticated probe → 200 with empty body.
//
// "Authenticated" here means: a valid bearer JWT, OR a pika token
// passed as Bearer/Basic. The latter lets test scripts and the UI
// skip the token-exchange dance.
func (l *Local) serveVersionProbe(w http.ResponseWriter, r *http.Request) {
	if l.authenticated(r) {
		w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
		w.WriteHeader(http.StatusOK)
		return
	}
	challenge(w, r, "authentication required")
}

// authenticated reports whether the request carries credentials we
// accept: a valid Docker bearer JWT, OR a pika token (Bearer/Basic).
// The entry handler in api/registry.go has already validated the
// pika token for any operation that requires capability; this
// helper just lets us complete the docker challenge dance.
func (l *Local) authenticated(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	switch {
	case strings.HasPrefix(auth, "Bearer "):
		// Try our JWT first; on failure fall through to "treat as
		// pika token" because the entry handler may have already
		// validated it.
		token := strings.TrimPrefix(auth, "Bearer ")
		if _, err := verifyToken(l.signer, token); err == nil {
			return true
		}
		// If the bearer looks like a pika token (starts with
		// "pika_"), assume the entry handler validated it — the
		// data-mux dispatch already enforces the capability check
		// before we reach here.
		return strings.HasPrefix(token, "pika_")
	case strings.HasPrefix(auth, "Basic "):
		return true
	}
	return false
}

// ─── /v2/token — bearer token issuance ────────────────────────────

// serveToken handles GET /v2/token?service=&scope=. The client
// presents Basic auth (pika token in the password slot); we
// validate the token via the pika-wide auth path (which has
// already happened at the entry handler if the call reached here),
// then mint a JWT carrying the scope.
//
// For the MVP we trust the entry handler's auth gating: any
// request that lands here has already been verified by the
// data-mux entry. We just produce a token bound to the requested
// scope and a short expiry.
func (l *Local) serveToken(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	scope := q.Get("scope")
	if scope == "" {
		scope = "repository:" + l.name + ":pull"
	}

	subject, _, _ := r.BasicAuth()
	if subject == "" {
		subject = "anonymous"
	}

	token, err := issueToken(l.signer, subject, scope)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TOKEN_FAILURE", err.Error())
		return
	}
	resp := map[string]any{
		"token":        token,
		"access_token": token,
		"expires_in":   int(tokenLifetime.Seconds()),
		"issued_at":    timeNowRFC3339(),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// ─── /v2/_catalog ─────────────────────────────────────────────────

func (l *Local) serveCatalog(w http.ResponseWriter, r *http.Request) {
	repos, err := l.store.ListRepositories()
	if err != nil {
		mapError(w, err)
		return
	}
	type catalogResp struct {
		Repositories []string `json:"repositories"`
	}
	body, _ := json.Marshal(catalogResp{Repositories: repos})
	writeOK(w, "application/json", body)
}

// ─── /v2/{name}/tags/list ─────────────────────────────────────────

func (l *Local) serveTagsList(w http.ResponseWriter, r *http.Request, name string) {
	tags, err := l.store.ListTags(name)
	if err != nil {
		mapError(w, err)
		return
	}
	type tagsResp struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	body, _ := json.Marshal(tagsResp{Name: name, Tags: tags})
	writeOK(w, "application/json", body)
}

// ─── /v2/{name}/manifests/{ref} ───────────────────────────────────

func (l *Local) serveManifest(w http.ResponseWriter, r *http.Request, req parsedRequest) {
	switch r.Method {
	case http.MethodHead, http.MethodGet:
		l.readManifest(w, r, req)
	case http.MethodPut:
		l.putManifest(w, r, req)
	case http.MethodDelete:
		l.deleteManifest(w, r, req)
	default:
		writeError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
	}
}

func (l *Local) readManifest(w http.ResponseWriter, r *http.Request, req parsedRequest) {
	// Resolve to a digest if the ref was a tag.
	dgst := req.Digest
	if dgst.IsZero() {
		d, err := l.store.ReadTag(req.Name, req.Ref)
		if err != nil {
			mapError(w, err)
			return
		}
		dgst = d
	}
	rec, err := l.store.ReadManifest(req.Name, dgst)
	if err != nil {
		mapError(w, err)
		return
	}
	w.Header().Set("Content-Type", rec.ContentType)
	w.Header().Set("Docker-Content-Digest", rec.Digest.String())
	w.Header().Set("Content-Length", strconv.Itoa(len(rec.Body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(rec.Body)
}

func (l *Local) putManifest(w http.ResponseWriter, r *http.Request, req parsedRequest) {
	if !l.allowPush {
		writeError(w, http.StatusMethodNotAllowed, "DENIED", "push disabled")
		return
	}
	max := l.maxUpload
	if max == 0 {
		max = 4 * 1024 * 1024 // 4 MiB ceiling for manifest bodies
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "MANIFEST_INVALID", "read body: "+err.Error())
		return
	}
	if int64(len(body)) > max {
		writeError(w, http.StatusRequestEntityTooLarge, "MANIFEST_INVALID",
			fmt.Sprintf("manifest exceeds %d bytes", max))
		return
	}

	// Compute sha256 digest of the body — manifests are content-
	// addressable. Use the blobstore hashing primitive for
	// consistency.
	h, _ := blobstore.NewHasher("sha256")
	_, _ = h.Write(body)
	dgst := blobstore.DigestFromHash("sha256", h)

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/vnd.docker.distribution.manifest.v2+json"
	}

	if err := l.store.WriteManifest(req.Name, dgst, body, contentType); err != nil {
		mapError(w, err)
		return
	}

	// If the ref was a tag, point it at this digest.
	if !IsDigestReference(req.Ref) && req.Ref != "" {
		if err := l.store.SetTag(req.Name, req.Ref, dgst); err != nil {
			mapError(w, err)
			return
		}
	}

	// OCI v1.1 referrers index maintenance. If this manifest has a
	// `subject` descriptor, register us as a referrer of that
	// subject so /v2/{name}/referrers/{subject} surfaces this push.
	// Tolerate unparseable bodies — referrers indexing is a best-
	// effort feature; the manifest itself is still pushable.
	if insp := inspectManifest(body); insp != nil && insp.Subject != nil && insp.Subject.Digest != "" {
		desc := manifestDescriptor{
			MediaType:    contentType,
			ArtifactType: insp.effectiveArtifactType(),
			Digest:       dgst.String(),
			Size:         int64(len(body)),
			Annotations:  insp.Annotations,
		}
		if err := l.store.AddReferrer(req.Name, insp.Subject.Digest, desc); err != nil {
			// Non-fatal: log via the response header so curious
			// operators can spot the failure without breaking
			// pushes.
			w.Header().Set("OCI-Subject-Index-Warning", err.Error())
		}
		// Spec: when the manifest carries a subject, response MUST
		// include the OCI-Subject header so clients know we
		// processed it.
		w.Header().Set("OCI-Subject", insp.Subject.Digest)
	}

	// Emit registry.published. Path encodes "{repo}/{image}:{tag-or-digest}"
	// so operators see a single greppable identifier per push.
	subject := req.Name + "@" + dgst.String()
	if !IsDigestReference(req.Ref) && req.Ref != "" {
		subject = req.Name + ":" + req.Ref
	}
	events.EmitSafe(l.emitter, hook.Event{
		Type:     hook.EventRegistryPublished,
		Mount:    l.namespace,
		Path:     l.name + "/" + subject,
		Protocol: "registry-docker",
		Size:     int64(len(body)),
	})

	prefix := r.Header.Get("X-Pika-Registry-Prefix")
	w.Header().Set("Location", prefix+"/v2/"+req.Name+"/manifests/"+dgst.String())
	w.Header().Set("Docker-Content-Digest", dgst.String())
	w.WriteHeader(http.StatusCreated)
}

func (l *Local) deleteManifest(w http.ResponseWriter, r *http.Request, req parsedRequest) {
	if !l.allowPush {
		writeError(w, http.StatusMethodNotAllowed, "DENIED", "delete disabled")
		return
	}
	dgst := req.Digest
	if dgst.IsZero() {
		// Tag delete: drop the pointer, leave the manifest blob.
		if err := l.store.DeleteTag(req.Name, req.Ref); err != nil {
			mapError(w, err)
			return
		}
		events.EmitSafe(l.emitter, hook.Event{
			Type:     hook.EventRegistryDeleted,
			Mount:    l.namespace,
			Path:     l.name + "/" + req.Name + ":" + req.Ref,
			Protocol: "registry-docker",
		})
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Manifest delete by digest: also drop ourselves from the
	// referrers index of our subject (if we had one). Read the
	// manifest body first to recover the subject.digest — silent
	// when the manifest is already missing or unparseable, because
	// the post-conditions don't depend on the cleanup succeeding.
	if rec, err := l.store.ReadManifest(req.Name, dgst); err == nil {
		if insp := inspectManifest(rec.Body); insp != nil && insp.Subject != nil && insp.Subject.Digest != "" {
			_ = l.store.RemoveReferrer(req.Name, insp.Subject.Digest, dgst.String())
		}
	}

	if err := l.store.DeleteManifest(req.Name, dgst); err != nil {
		mapError(w, err)
		return
	}
	events.EmitSafe(l.emitter, hook.Event{
		Type:     hook.EventRegistryDeleted,
		Mount:    l.namespace,
		Path:     l.name + "/" + req.Name + "@" + dgst.String(),
		Protocol: "registry-docker",
	})
	w.WriteHeader(http.StatusAccepted)
}

// serveReferrers implements the OCI v1.1 referrers API:
// GET /v2/{name}/referrers/{digest}[?artifactType=...]
//
// Returns an OCI Image Index document listing every manifest whose
// `subject.digest` matches {digest}. The optional artifactType
// query parameter filters the result; when set, the response also
// includes the `OCI-Filters-Applied: artifactType` header per spec.
func (l *Local) serveReferrers(w http.ResponseWriter, r *http.Request, req parsedRequest) {
	idx, err := l.store.ReadReferrers(req.Name, req.Digest.String())
	if err != nil {
		mapError(w, err)
		return
	}
	filter := r.URL.Query().Get("artifactType")
	if filter != "" {
		filtered := idx.Manifests[:0]
		for _, m := range idx.Manifests {
			if m.ArtifactType == filter {
				filtered = append(filtered, m)
			}
		}
		idx.Manifests = filtered
		w.Header().Set("OCI-Filters-Applied", "artifactType")
	}
	body, err := json.Marshal(idx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// ─── /v2/{name}/blobs/{digest} ────────────────────────────────────

func (l *Local) serveBlob(w http.ResponseWriter, r *http.Request, req parsedRequest) {
	switch r.Method {
	case http.MethodHead, http.MethodGet:
		l.readBlob(w, r, req)
	case http.MethodDelete:
		if !l.allowPush {
			writeError(w, http.StatusMethodNotAllowed, "DENIED", "delete disabled")
			return
		}
		if err := l.store.Blobs().Delete(req.Digest); err != nil {
			if errors.Is(err, blobstore.ErrNotFound) {
				writeError(w, http.StatusNotFound, "BLOB_UNKNOWN", err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "UNKNOWN", err.Error())
			return
		}
		w.WriteHeader(http.StatusAccepted)
	default:
		writeError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
	}
}

func (l *Local) readBlob(w http.ResponseWriter, r *http.Request, req parsedRequest) {
	rc, info, err := l.store.Blobs().Get(req.Digest)
	if err != nil {
		if errors.Is(err, blobstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "BLOB_UNKNOWN", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Docker-Content-Digest", req.Digest.String())
	if info != nil && info.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, rc)
}

// ─── /v2/{name}/blobs/uploads/... ─────────────────────────────────

// serveUploadStart handles POST /v2/{name}/blobs/uploads/.
//
// Two flows are supported:
//
//   - Plain init: return 202 with a Location pointing at the new
//     upload UUID; the client then PATCHes chunks and PUTs the
//     final digest.
//   - "Monolithic" PUT alongside POST (rare): we don't optimise
//     for it, just treat as a regular start.
func (l *Local) serveUploadStart(w http.ResponseWriter, r *http.Request, name string) {
	if !l.allowPush {
		writeError(w, http.StatusMethodNotAllowed, "DENIED", "push disabled")
		return
	}
	sess, err := l.store.Blobs().StartUpload()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	uuid := newUploadUUID()
	l.uploads.put(uuid, name, sess.ID())

	prefix := r.Header.Get("X-Pika-Registry-Prefix")
	location := prefix + "/v2/" + name + "/blobs/uploads/" + uuid
	w.Header().Set("Location", location)
	w.Header().Set("Docker-Upload-UUID", uuid)
	w.Header().Set("Range", "0-0")
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusAccepted)
}

// serveUploadAppend handles PATCH /v2/{name}/blobs/uploads/{uuid}.
func (l *Local) serveUploadAppend(w http.ResponseWriter, r *http.Request, req parsedRequest) {
	if !l.allowPush {
		writeError(w, http.StatusMethodNotAllowed, "DENIED", "push disabled")
		return
	}
	entry, ok := l.uploads.get(req.UploadID)
	if !ok || entry.RepoName != req.Name {
		writeError(w, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "session not found")
		return
	}
	sess, err := l.store.Blobs().ResumeUpload(entry.BlobSession)
	if err != nil {
		writeError(w, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", err.Error())
		return
	}
	n, err := io.Copy(sess, r.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	prefix := r.Header.Get("X-Pika-Registry-Prefix")
	location := prefix + "/v2/" + req.Name + "/blobs/uploads/" + req.UploadID
	w.Header().Set("Location", location)
	w.Header().Set("Docker-Upload-UUID", req.UploadID)
	w.Header().Set("Range", fmt.Sprintf("0-%d", sess.Offset()-1))
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusAccepted)
	_ = n
}

// serveUploadFinalize handles PUT /v2/{name}/blobs/uploads/{uuid}?digest=...
// The body (if any) is appended as a final chunk before the commit.
func (l *Local) serveUploadFinalize(w http.ResponseWriter, r *http.Request, req parsedRequest) {
	if !l.allowPush {
		writeError(w, http.StatusMethodNotAllowed, "DENIED", "push disabled")
		return
	}
	entry, ok := l.uploads.get(req.UploadID)
	if !ok || entry.RepoName != req.Name {
		writeError(w, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "session not found")
		return
	}
	sess, err := l.store.Blobs().ResumeUpload(entry.BlobSession)
	if err != nil {
		writeError(w, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", err.Error())
		return
	}
	digestParam := r.URL.Query().Get("digest")
	if digestParam == "" {
		writeError(w, http.StatusBadRequest, "DIGEST_INVALID", "digest query param required")
		return
	}
	dgst, err := blobstore.ParseDigest(digestParam)
	if err != nil {
		writeError(w, http.StatusBadRequest, "DIGEST_INVALID", err.Error())
		return
	}

	// Drain any final body bytes.
	if r.Body != nil {
		if _, err := io.Copy(sess, r.Body); err != nil {
			writeError(w, http.StatusInternalServerError, "UNKNOWN", err.Error())
			return
		}
	}

	final, err := sess.Commit(dgst)
	if err != nil {
		if errors.Is(err, blobstore.ErrDigestMismatch) {
			writeError(w, http.StatusBadRequest, "DIGEST_INVALID",
				fmt.Sprintf("expected %s, got %s", dgst, final))
			return
		}
		writeError(w, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	l.uploads.delete(req.UploadID)

	prefix := r.Header.Get("X-Pika-Registry-Prefix")
	w.Header().Set("Location", prefix+"/v2/"+req.Name+"/blobs/"+final.String())
	w.Header().Set("Docker-Content-Digest", final.String())
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusCreated)
}

// serveUploadProgress handles GET /v2/{name}/blobs/uploads/{uuid}.
// Returns the current offset.
func (l *Local) serveUploadProgress(w http.ResponseWriter, r *http.Request, req parsedRequest) {
	entry, ok := l.uploads.get(req.UploadID)
	if !ok || entry.RepoName != req.Name {
		writeError(w, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "session not found")
		return
	}
	sess, err := l.store.Blobs().ResumeUpload(entry.BlobSession)
	if err != nil {
		writeError(w, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", err.Error())
		return
	}
	w.Header().Set("Range", fmt.Sprintf("0-%d", sess.Offset()-1))
	w.Header().Set("Docker-Upload-UUID", req.UploadID)
	w.WriteHeader(http.StatusNoContent)
}

// serveUploadCancel handles DELETE /v2/{name}/blobs/uploads/{uuid}.
func (l *Local) serveUploadCancel(w http.ResponseWriter, r *http.Request, req parsedRequest) {
	entry, ok := l.uploads.get(req.UploadID)
	if !ok || entry.RepoName != req.Name {
		writeError(w, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "session not found")
		return
	}
	sess, err := l.store.Blobs().ResumeUpload(entry.BlobSession)
	if err == nil {
		_ = sess.Cancel()
	}
	l.uploads.delete(req.UploadID)
	w.WriteHeader(http.StatusNoContent)
}

// ─── helpers ───────────────────────────────────────────────────────

// newUploadUUID is the docker-side opaque session identifier we
// hand to clients. Distinct from the BlobStore session ID — the
// docker layer's mapping is held in uploadSessions.
func newUploadUUID() string {
	buf := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "fallback-id"
	}
	return hex.EncodeToString(buf)
}

// timeNowRFC3339 returns the current time in RFC3339, used in
// token issuance responses.
func timeNowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// randomKey generates a 32-byte secret used for HMAC signing.
func randomKey() []byte {
	buf := make([]byte, 32)
	_, _ = io.ReadFull(rand.Reader, buf)
	return buf
}

// _ kept across optional refactors.
var _ = strings.HasPrefix
