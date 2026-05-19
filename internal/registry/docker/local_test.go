package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/rawfs/localfs"
	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/blobstore"
	"github.com/rakunlabs/pika/internal/service"
)

// newDockerLocal returns a Local Registry rooted at a fresh temp
// directory with push enabled.
func newDockerLocal(t *testing.T, allowPush bool) *Local {
	t.Helper()
	dir := t.TempDir()
	fs, err := localfs.New(dir)
	if err != nil {
		t.Fatalf("localfs.New: %v", err)
	}
	deps := registry.Deps{
		MountRawFS: func(string) (rawfs.RawFS, error) { return fs, nil },
		MountFor: func(_, basePath string) (blobstore.BlobStore, error) {
			return blobstore.NewRawFSBlobStore(fs, basePath)
		},
	}
	repo := &service.RegistryRepository{
		Name: "docker-local", Type: service.RegistryTypeDocker, Kind: service.RegistryKindLocal,
		Mount: "m", BasePath: "docker", AllowPush: allowPush,
	}
	r, err := NewLocalFactory()(context.Background(), deps, "default", repo)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	return r.(*Local)
}

// do executes one request against the Local handler with the
// registry-prefix header set, returning the recorded response.
func do(l *Local, method, p string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, p, body)
	r.Header.Set("X-Pika-Registry-Prefix", "/registries/default/docker-local")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	l.ServeHTTP(w, r)
	return w
}

func sha256OfBytes(b []byte) blobstore.Digest {
	sum := sha256.Sum256(b)
	return blobstore.Digest{Algorithm: "sha256", Hex: hex.EncodeToString(sum[:])}
}

func TestDockerLocal_Metadata(t *testing.T) {
	l := newDockerLocal(t, true)
	if l.Type() != "docker" || l.Kind() != "local" {
		t.Errorf("type=%s kind=%s", l.Type(), l.Kind())
	}
	if !l.AllowPush() {
		t.Errorf("push should be enabled")
	}
}

func TestDockerLocal_VersionProbeUnauthenticated(t *testing.T) {
	l := newDockerLocal(t, true)
	w := do(l, http.MethodGet, "/v2/", nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if w.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("WWW-Authenticate missing")
	}
}

func TestDockerLocal_VersionProbeAuthenticated(t *testing.T) {
	l := newDockerLocal(t, true)
	w := do(l, http.MethodGet, "/v2/", nil, map[string]string{
		"Authorization": "Bearer pika_test",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Docker-Distribution-API-Version") != "registry/2.0" {
		t.Fatalf("API version header missing")
	}
}

func TestDockerLocal_TokenIssue(t *testing.T) {
	l := newDockerLocal(t, true)
	w := do(l, http.MethodGet, "/v2/token?service=pika&scope=repository:lib/foo:pull", nil, map[string]string{
		"Authorization": "Basic " + base64BasicAuth("user", "pika_test"),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if tok, _ := resp["token"].(string); tok == "" {
		t.Fatalf("token missing in response: %v", resp)
	}
}

func TestDockerLocal_PushPullCycle(t *testing.T) {
	l := newDockerLocal(t, true)
	name := "lib/foo"

	// Layer blob
	layer := []byte("layer-bytes")
	layerDgst := sha256OfBytes(layer)

	// Step 1: POST /v2/{name}/blobs/uploads/
	w := do(l, http.MethodPost, "/v2/"+name+"/blobs/uploads/", nil, map[string]string{
		"Authorization": "Bearer pika_test",
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("upload init status %d body %s", w.Code, w.Body.String())
	}
	location := w.Header().Get("Location")
	uuid := w.Header().Get("Docker-Upload-UUID")
	if uuid == "" || location == "" {
		t.Fatalf("missing upload headers: location=%q uuid=%q", location, uuid)
	}

	// Step 2: PATCH the layer bytes.
	w = do(l, http.MethodPatch, "/v2/"+name+"/blobs/uploads/"+uuid,
		bytes.NewReader(layer), map[string]string{
			"Authorization": "Bearer pika_test",
		})
	if w.Code != http.StatusAccepted {
		t.Fatalf("patch status %d body %s", w.Code, w.Body.String())
	}

	// Step 3: PUT finalise.
	w = do(l, http.MethodPut, "/v2/"+name+"/blobs/uploads/"+uuid+"?digest="+layerDgst.String(),
		nil, map[string]string{
			"Authorization": "Bearer pika_test",
		})
	if w.Code != http.StatusCreated {
		t.Fatalf("finalize status %d body %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Docker-Content-Digest") != layerDgst.String() {
		t.Fatalf("docker-content-digest %q want %q",
			w.Header().Get("Docker-Content-Digest"), layerDgst.String())
	}

	// Step 4: HEAD /v2/{name}/blobs/{digest} — sanity-check the
	// blob materialised.
	w = do(l, http.MethodHead, "/v2/"+name+"/blobs/"+layerDgst.String(), nil, map[string]string{
		"Authorization": "Bearer pika_test",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("blob HEAD status %d", w.Code)
	}

	// Step 5: PUT manifest tagged "latest".
	manifest := map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.docker.distribution.manifest.v2+json",
		"config":        map[string]any{"digest": layerDgst.String()},
		"layers":        []any{map[string]any{"digest": layerDgst.String()}},
	}
	manifestBody, _ := json.Marshal(manifest)
	w = do(l, http.MethodPut, "/v2/"+name+"/manifests/latest",
		bytes.NewReader(manifestBody), map[string]string{
			"Authorization": "Bearer pika_test",
			"Content-Type":  "application/vnd.docker.distribution.manifest.v2+json",
		})
	if w.Code != http.StatusCreated {
		t.Fatalf("manifest put status %d body %s", w.Code, w.Body.String())
	}
	manifestDgst := w.Header().Get("Docker-Content-Digest")
	if manifestDgst == "" {
		t.Fatalf("missing manifest digest in response")
	}

	// Step 6: GET manifest by tag.
	w = do(l, http.MethodGet, "/v2/"+name+"/manifests/latest", nil, map[string]string{
		"Authorization": "Bearer pika_test",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("manifest get by tag: status %d", w.Code)
	}
	if !bytes.Equal(w.Body.Bytes(), manifestBody) {
		t.Fatalf("manifest body mismatch")
	}

	// Step 7: GET manifest by digest.
	w = do(l, http.MethodGet, "/v2/"+name+"/manifests/"+manifestDgst, nil, map[string]string{
		"Authorization": "Bearer pika_test",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("manifest get by digest: status %d", w.Code)
	}

	// Step 8: GET tags/list.
	w = do(l, http.MethodGet, "/v2/"+name+"/tags/list", nil, map[string]string{
		"Authorization": "Bearer pika_test",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("tags/list status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"latest"`) {
		t.Fatalf("tags body %q", w.Body.String())
	}

	// Step 9: GET _catalog.
	w = do(l, http.MethodGet, "/v2/_catalog", nil, map[string]string{
		"Authorization": "Bearer pika_test",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("catalog status %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), name) {
		t.Fatalf("catalog body %q", w.Body.String())
	}

	// Step 10: GET blob payload.
	w = do(l, http.MethodGet, "/v2/"+name+"/blobs/"+layerDgst.String(), nil, map[string]string{
		"Authorization": "Bearer pika_test",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("blob get status %d", w.Code)
	}
	if !bytes.Equal(w.Body.Bytes(), layer) {
		t.Fatalf("blob body mismatch")
	}
}

func TestDockerLocal_UploadFinalizeDigestMismatch(t *testing.T) {
	l := newDockerLocal(t, true)
	name := "lib/foo"

	w := do(l, http.MethodPost, "/v2/"+name+"/blobs/uploads/", nil, map[string]string{
		"Authorization": "Bearer pika_test",
	})
	uuid := w.Header().Get("Docker-Upload-UUID")

	w = do(l, http.MethodPatch, "/v2/"+name+"/blobs/uploads/"+uuid,
		bytes.NewReader([]byte("actual-bytes")), map[string]string{
			"Authorization": "Bearer pika_test",
		})
	if w.Code != http.StatusAccepted {
		t.Fatalf("patch %d", w.Code)
	}

	bogus := sha256OfBytes([]byte("different-bytes"))
	w = do(l, http.MethodPut, "/v2/"+name+"/blobs/uploads/"+uuid+"?digest="+bogus.String(),
		nil, map[string]string{
			"Authorization": "Bearer pika_test",
		})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on digest mismatch, got %d body %s", w.Code, w.Body.String())
	}
}

func TestDockerLocal_DeleteManifest(t *testing.T) {
	l := newDockerLocal(t, true)
	name := "lib/foo"

	// Push a minimal manifest.
	manifest := []byte(`{"schemaVersion":2,"layers":[]}`)
	w := do(l, http.MethodPut, "/v2/"+name+"/manifests/latest", bytes.NewReader(manifest),
		map[string]string{"Authorization": "Bearer pika_test",
			"Content-Type": "application/vnd.docker.distribution.manifest.v2+json"})
	if w.Code != http.StatusCreated {
		t.Fatalf("put manifest %d", w.Code)
	}
	digest := w.Header().Get("Docker-Content-Digest")

	w = do(l, http.MethodDelete, "/v2/"+name+"/manifests/"+digest, nil,
		map[string]string{"Authorization": "Bearer pika_test"})
	if w.Code != http.StatusAccepted {
		t.Fatalf("delete manifest %d", w.Code)
	}

	w = do(l, http.MethodGet, "/v2/"+name+"/manifests/"+digest, nil,
		map[string]string{"Authorization": "Bearer pika_test"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("post-delete get: status %d", w.Code)
	}
}

func TestDockerLocal_RejectsPushWhenDisabled(t *testing.T) {
	l := newDockerLocal(t, false)
	w := do(l, http.MethodPost, "/v2/lib/foo/blobs/uploads/", nil,
		map[string]string{"Authorization": "Bearer pika_test"})
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestDockerLocal_UnknownBlob(t *testing.T) {
	l := newDockerLocal(t, true)
	dgst := sha256OfBytes([]byte("ghost"))
	w := do(l, http.MethodGet, "/v2/lib/foo/blobs/"+dgst.String(), nil,
		map[string]string{"Authorization": "Bearer pika_test"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestDockerLocal_BogusPath(t *testing.T) {
	l := newDockerLocal(t, true)
	w := do(l, http.MethodGet, "/v2/", nil, nil)
	// Already covered (challenge); ensure /not-v2 is 404.
	w = do(l, http.MethodGet, "/garbage", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d", w.Code)
	}
}

// base64BasicAuth produces a Basic auth credential string for the
// "Authorization" header.
func base64BasicAuth(user, pass string) string {
	enc := []byte(user + ":" + pass)
	return base64Std(enc)
}

func base64Std(b []byte) string {
	// Inline because the std encoding helper is only needed by one
	// test helper.
	return string(b64Encode(b))
}

func b64Encode(b []byte) []byte {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	out := make([]byte, ((len(b)+2)/3)*4)
	i := 0
	for j := 0; j < len(b); j += 3 {
		var v uint32
		switch len(b) - j {
		case 1:
			v = uint32(b[j]) << 16
		case 2:
			v = uint32(b[j])<<16 | uint32(b[j+1])<<8
		default:
			v = uint32(b[j])<<16 | uint32(b[j+1])<<8 | uint32(b[j+2])
		}
		out[i] = alphabet[(v>>18)&0x3F]
		out[i+1] = alphabet[(v>>12)&0x3F]
		out[i+2] = '='
		out[i+3] = '='
		if len(b)-j >= 2 {
			out[i+2] = alphabet[(v>>6)&0x3F]
		}
		if len(b)-j >= 3 {
			out[i+3] = alphabet[v&0x3F]
		}
		i += 4
	}
	return out
}

func TestParsePathClassifications(t *testing.T) {
	cases := []struct {
		method string
		path   string
		op     dockerOp
		ok     bool
	}{
		{http.MethodGet, "/v2/", opVersionProbe, true},
		{http.MethodGet, "/v2/_catalog", opCatalog, true},
		{http.MethodGet, "/v2/lib/foo/tags/list", opTagsList, true},
		{http.MethodGet, "/v2/lib/foo/manifests/latest", opManifest, true},
		{http.MethodHead, "/v2/lib/foo/manifests/sha256:" + strings.Repeat("a", 64), opManifest, true},
		{http.MethodGet, "/v2/lib/foo/blobs/sha256:" + strings.Repeat("a", 64), opBlob, true},
		{http.MethodPost, "/v2/lib/foo/blobs/uploads/", opUploadStart, true},
		{http.MethodPatch, "/v2/lib/foo/blobs/uploads/abc123", opUploadAppend, true},
		{http.MethodPut, "/v2/lib/foo/blobs/uploads/abc123", opUploadFinalize, true},
		{http.MethodDelete, "/v2/lib/foo/blobs/uploads/abc123", opUploadCancel, true},
		{http.MethodGet, "/v2/lib/foo/blobs/uploads/abc123", opUploadProgress, true},
		{http.MethodGet, "/v2/lib/foo/referrers/sha256:" + strings.Repeat("b", 64), opReferrers, true},
		{http.MethodGet, "/v2/no/such/op/garbage", opUnknown, false},
		{http.MethodGet, "/notv2", opUnknown, false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s %s", tc.method, tc.path), func(t *testing.T) {
			got, ok := classify(tc.method, tc.path)
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v", ok, tc.ok)
			}
			if ok && got.Op != tc.op {
				t.Fatalf("op=%v want %v", got.Op, tc.op)
			}
		})
	}
}

func TestNameValidation(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"library/nginx", false},
		{"foo", false},
		{"my-org/my-image", false},
		{"a/b/c", false},
		{"", true},
		{"UPPER", true},
		{"trailing/", true},
		{"/leading", true},
		{"with space", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			err := ValidateRepoName(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
