package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"

	"github.com/rakunlabs/pika/internal/registry/blobstore"
)

// pushBlob uploads a blob to the local registry and returns its
// digest. Helper used by GC tests so we can wire up "real" data.
func pushBlob(t *testing.T, l *Local, name string, body []byte) blobstore.Digest {
	t.Helper()
	dgst := blobstore.Digest{Algorithm: "sha256", Hex: hexSha256(body)}

	w := do(l, http.MethodPost, "/v2/"+name+"/blobs/uploads/", nil, map[string]string{
		"Authorization": "Bearer pika_test",
	})
	uuid := w.Header().Get("Docker-Upload-UUID")
	if uuid == "" {
		t.Fatalf("upload init: missing UUID, body %s", w.Body.String())
	}

	w = do(l, http.MethodPatch, "/v2/"+name+"/blobs/uploads/"+uuid,
		bytes.NewReader(body), map[string]string{"Authorization": "Bearer pika_test"})
	if w.Code != http.StatusAccepted {
		t.Fatalf("patch upload: %d", w.Code)
	}

	w = do(l, http.MethodPut, "/v2/"+name+"/blobs/uploads/"+uuid+"?digest="+dgst.String(),
		nil, map[string]string{"Authorization": "Bearer pika_test"})
	if w.Code != http.StatusCreated {
		t.Fatalf("finalize: %d body %s", w.Code, w.Body.String())
	}
	return dgst
}

func hexSha256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestGC_KeepsBlobsReferencedByTag(t *testing.T) {
	l := newDockerLocal(t, true)
	name := "lib/foo"

	configBytes := []byte(`{"architecture":"amd64"}`)
	configDgst := pushBlob(t, l, name, configBytes)

	layerBytes := []byte("layer-payload")
	layerDgst := pushBlob(t, l, name, layerBytes)

	manifest := []byte(`{
		"schemaVersion":2,
		"mediaType":"application/vnd.oci.image.manifest.v1+json",
		"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"` + configDgst.String() + `","size":` + jsonInt(len(configBytes)) + `},
		"layers":[{"mediaType":"application/octet-stream","digest":"` + layerDgst.String() + `","size":` + jsonInt(len(layerBytes)) + `}]
	}`)
	pushManifestBody(t, l, name, "v1.0.0", manifest, "application/vnd.oci.image.manifest.v1+json")

	stats, err := l.GarbageCollect(context.Background(), GCOptions{MinAge: 0})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if stats.SweptBlobs != 0 {
		t.Fatalf("expected zero blob sweeps, got %d (errors: %v)", stats.SweptBlobs, stats.Errors)
	}
	if stats.SweptManifests != 0 {
		t.Fatalf("expected zero manifest sweeps, got %d", stats.SweptManifests)
	}
	if stats.MarkedBlobs < 2 {
		t.Fatalf("expected at least 2 marked blobs (config+layer), got %d", stats.MarkedBlobs)
	}

	// Sanity: both blobs are still readable.
	if _, err := l.Store().Blobs().Stat(configDgst); err != nil {
		t.Errorf("config blob lost: %v", err)
	}
	if _, err := l.Store().Blobs().Stat(layerDgst); err != nil {
		t.Errorf("layer blob lost: %v", err)
	}
}

func TestGC_SweepsOrphanedBlob(t *testing.T) {
	l := newDockerLocal(t, true)
	name := "lib/foo"

	// Push a blob but never reference it from a manifest.
	orphan := pushBlob(t, l, name, []byte("orphan-payload"))

	stats, err := l.GarbageCollect(context.Background(), GCOptions{MinAge: 0})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if stats.SweptBlobs != 1 {
		t.Fatalf("expected 1 swept blob, got %d (errors: %v)", stats.SweptBlobs, stats.Errors)
	}
	if _, err := l.Store().Blobs().Stat(orphan); err == nil {
		t.Fatalf("orphan blob still present after GC")
	}
}

func TestGC_SweepsUnreferencedManifest(t *testing.T) {
	l := newDockerLocal(t, true)
	name := "lib/foo"

	// Push manifest A and tag it.
	manifestA := []byte(`{"schemaVersion":2,"layers":[]}`)
	pushManifestBody(t, l, name, "v1.0.0", manifestA, "application/vnd.oci.image.manifest.v1+json")

	// Push manifest B but don't tag it (digest reference only). It
	// becomes an "orphan" manifest as soon as it lands.
	manifestB := []byte(`{"schemaVersion":2,"_orphan":true,"layers":[]}`)
	digestB := digestOf(manifestB)
	pushManifestBody(t, l, name, digestB, manifestB, "application/vnd.oci.image.manifest.v1+json")

	stats, err := l.GarbageCollect(context.Background(), GCOptions{MinAge: 0})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if stats.SweptManifests < 1 {
		t.Fatalf("expected at least 1 manifest swept, got %d (errors: %v)", stats.SweptManifests, stats.Errors)
	}

	// The orphan B should be gone.
	parsedB, _ := blobstore.ParseDigest(digestB)
	if _, err := l.Store().ReadManifest(name, parsedB); err == nil {
		t.Fatalf("orphan manifest B still present after GC")
	}
}

func TestGC_KeepsReferrersIndexedSubject(t *testing.T) {
	l := newDockerLocal(t, true)
	name := "lib/foo"

	// Push subject manifest (tagged).
	subjectBody := []byte(`{"schemaVersion":2,"layers":[]}`)
	subjectDigest := pushManifestBody(t, l, name, "v1.0.0", subjectBody, "application/vnd.oci.image.manifest.v1+json")

	// Push a cosign signature that points at the subject.
	sigBody := []byte(`{
		"schemaVersion":2,
		"artifactType":"application/vnd.dev.cosign.simplesigning.v1+json",
		"subject":{"digest":"` + subjectDigest + `","size":` + jsonInt(len(subjectBody)) + `}
	}`)
	pushManifestBody(t, l, name, digestOf(sigBody), sigBody, "application/vnd.oci.image.manifest.v1+json")

	// Now untag the subject. Only its referrers index keeps it
	// alive — GC must NOT delete it.
	w := do(l, http.MethodDelete, "/v2/"+name+"/manifests/v1.0.0", nil, map[string]string{
		"Authorization": "Bearer pika_test",
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("untag: %d", w.Code)
	}

	stats, err := l.GarbageCollect(context.Background(), GCOptions{MinAge: 0})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}

	// Subject + signature manifests must remain (subject because
	// it has a referrers index; signature because it references
	// the subject).
	parsedSubject, _ := blobstore.ParseDigest(subjectDigest)
	if _, err := l.Store().ReadManifest(name, parsedSubject); err != nil {
		t.Errorf("subject manifest lost despite referrers index: %v (stats=%+v)", err, stats)
	}
	parsedSig, _ := blobstore.ParseDigest(digestOf(sigBody))
	if _, err := l.Store().ReadManifest(name, parsedSig); err != nil {
		t.Errorf("signature manifest lost: %v", err)
	}
}

func TestGC_GraceWindowProtectsYoung(t *testing.T) {
	l := newDockerLocal(t, true)
	name := "lib/foo"

	// Push a blob that isn't referenced anywhere. With a generous
	// MinAge it must NOT be swept.
	orphan := pushBlob(t, l, name, []byte("young-orphan"))

	stats, err := l.GarbageCollect(context.Background(), GCOptions{MinAge: 3600})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if stats.SweptBlobs != 0 {
		t.Fatalf("expected zero sweeps within grace, got %d", stats.SweptBlobs)
	}
	if stats.SkippedYoung == 0 {
		t.Fatalf("expected at least one young skip")
	}
	if _, err := l.Store().Blobs().Stat(orphan); err != nil {
		t.Errorf("orphan blob lost within grace window: %v", err)
	}
}

func TestGC_RecursesIntoImageIndex(t *testing.T) {
	// Multi-arch: an image index references per-arch manifests,
	// each of which references its own config + layers. GC must
	// keep every blob reachable through that chain.
	l := newDockerLocal(t, true)
	name := "lib/foo"

	// Per-arch manifest with a single layer.
	layer := []byte("arch-amd64-layer")
	layerDgst := pushBlob(t, l, name, layer)
	config := []byte(`{"architecture":"amd64"}`)
	configDgst := pushBlob(t, l, name, config)
	archManifest := []byte(`{
		"schemaVersion":2,
		"mediaType":"application/vnd.oci.image.manifest.v1+json",
		"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"` + configDgst.String() + `","size":` + jsonInt(len(config)) + `},
		"layers":[{"mediaType":"application/octet-stream","digest":"` + layerDgst.String() + `","size":` + jsonInt(len(layer)) + `}]
	}`)
	archDigest := pushManifestBody(t, l, name, digestOf(archManifest), archManifest, "application/vnd.oci.image.manifest.v1+json")

	// Image index pointing at the per-arch manifest, tagged.
	indexManifest := []byte(`{
		"schemaVersion":2,
		"mediaType":"application/vnd.oci.image.index.v1+json",
		"manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"` + archDigest + `","size":` + jsonInt(len(archManifest)) + `,"platform":{"architecture":"amd64","os":"linux"}}]
	}`)
	pushManifestBody(t, l, name, "latest", indexManifest, "application/vnd.oci.image.index.v1+json")

	stats, err := l.GarbageCollect(context.Background(), GCOptions{MinAge: 0})
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if stats.SweptBlobs != 0 {
		t.Fatalf("expected zero sweeps for multi-arch index, got %d (errors: %v)", stats.SweptBlobs, stats.Errors)
	}
	if _, err := l.Store().Blobs().Stat(layerDgst); err != nil {
		t.Errorf("layer blob lost despite multi-arch reference: %v", err)
	}
}
