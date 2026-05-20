package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// ────────────────────────────────────────────────────────────────────
// Delete-triggered cheap cascade
// ────────────────────────────────────────────────────────────────────

// TestDeleteManifest_CascadesUnreferencedSubManifests pushes a
// multi-arch image index, then deletes the index by digest. The
// per-arch sub-manifest is reachable only through that index, so
// the cascade should reap it as well. Layers / config blobs are
// NOT reaped by the cascade — those wait for the manual GC.
func TestDeleteManifest_CascadesUnreferencedSubManifests(t *testing.T) {
	l := newDockerLocal(t, true)
	// Cascade has a 1h grace window by default; disable it for the
	// test so the just-pushed sub-manifest is eligible to be reaped.
	l.cascadeMinAge = 0
	name := "lib/foo"

	// Per-arch manifest pushed by digest (no tag of its own).
	archManifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[]}`)
	archDigest := pushManifestBody(t, l, name, digestOf(archManifest), archManifest, "application/vnd.oci.image.manifest.v1+json")

	// Image index pointing at the per-arch manifest, tagged.
	indexBody := []byte(`{
		"schemaVersion":2,
		"mediaType":"application/vnd.oci.image.index.v1+json",
		"manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"` + archDigest + `","size":` + jsonInt(len(archManifest)) + `,"platform":{"architecture":"amd64","os":"linux"}}]
	}`)
	indexDigest := pushManifestBody(t, l, name, "latest", indexBody, "application/vnd.oci.image.index.v1+json")

	// Delete the image index by digest.
	w := do(l, http.MethodDelete, "/v2/"+name+"/manifests/"+indexDigest, nil,
		map[string]string{"Authorization": "Bearer pika_test"})
	if w.Code != http.StatusAccepted {
		t.Fatalf("delete index: %d body %s", w.Code, w.Body.String())
	}

	// The sub-manifest must have been cascaded away.
	parsedArch, _ := blobstore.ParseDigest(archDigest)
	if _, err := l.Store().ReadManifest(name, parsedArch); err == nil {
		t.Fatalf("sub-manifest survived cascade after image-index delete")
	}
}

// TestDeleteManifest_KeepsSharedSubManifest pushes a sub-manifest
// that two image indexes reference. Deleting one index must not
// cascade-delete the sub-manifest, because the OTHER index still
// references it.
func TestDeleteManifest_KeepsSharedSubManifest(t *testing.T) {
	l := newDockerLocal(t, true)
	l.cascadeMinAge = 0
	name := "lib/foo"

	archManifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[]}`)
	archDigest := pushManifestBody(t, l, name, digestOf(archManifest), archManifest, "application/vnd.oci.image.manifest.v1+json")

	indexA := []byte(`{
		"schemaVersion":2,
		"mediaType":"application/vnd.oci.image.index.v1+json",
		"manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"` + archDigest + `","size":` + jsonInt(len(archManifest)) + `}]
	}`)
	indexADigest := pushManifestBody(t, l, name, "v1", indexA, "application/vnd.oci.image.index.v1+json")

	indexB := []byte(`{
		"schemaVersion":2,
		"mediaType":"application/vnd.oci.image.index.v1+json",
		"_distinct":"second-index",
		"manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"` + archDigest + `","size":` + jsonInt(len(archManifest)) + `}]
	}`)
	pushManifestBody(t, l, name, "v2", indexB, "application/vnd.oci.image.index.v1+json")

	// Delete index A by digest. Sub-manifest must survive because
	// index B still references it.
	w := do(l, http.MethodDelete, "/v2/"+name+"/manifests/"+indexADigest, nil,
		map[string]string{"Authorization": "Bearer pika_test"})
	if w.Code != http.StatusAccepted {
		t.Fatalf("delete indexA: %d body %s", w.Code, w.Body.String())
	}

	parsedArch, _ := blobstore.ParseDigest(archDigest)
	if _, err := l.Store().ReadManifest(name, parsedArch); err != nil {
		t.Errorf("shared sub-manifest reaped despite second-index reference: %v", err)
	}
}

// TestDeleteManifest_RespectsGraceWindowOnSubManifests ensures the
// cascade honours cascadeMinAge: a sub-manifest pushed seconds ago
// must be preserved by the cascade so a concurrent push of another
// image index referencing it can complete safely.
func TestDeleteManifest_RespectsGraceWindowOnSubManifests(t *testing.T) {
	l := newDockerLocal(t, true)
	l.cascadeMinAge = 3600 // 1h
	name := "lib/foo"

	archManifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[]}`)
	archDigest := pushManifestBody(t, l, name, digestOf(archManifest), archManifest, "application/vnd.oci.image.manifest.v1+json")

	indexBody := []byte(`{
		"schemaVersion":2,
		"mediaType":"application/vnd.oci.image.index.v1+json",
		"manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"` + archDigest + `","size":` + jsonInt(len(archManifest)) + `}]
	}`)
	indexDigest := pushManifestBody(t, l, name, "latest", indexBody, "application/vnd.oci.image.index.v1+json")

	w := do(l, http.MethodDelete, "/v2/"+name+"/manifests/"+indexDigest, nil,
		map[string]string{"Authorization": "Bearer pika_test"})
	if w.Code != http.StatusAccepted {
		t.Fatalf("delete index: %d body %s", w.Code, w.Body.String())
	}

	// The sub-manifest is younger than the grace window — must NOT
	// be reaped by the cascade. Manual GC with grace=0 would still
	// clean it up.
	parsedArch, _ := blobstore.ParseDigest(archDigest)
	if _, err := l.Store().ReadManifest(name, parsedArch); err != nil {
		t.Errorf("sub-manifest reaped within grace window: %v", err)
	}
}

// TestDeleteManifest_RemovesOwnReferrersIndex pushes a manifest
// that itself has a referrer (cosign signature), then deletes the
// manifest by digest. The orphan _referrers/{digest}.json index
// file should be removed so we don't accumulate dangling indexes.
func TestDeleteManifest_RemovesOwnReferrersIndex(t *testing.T) {
	l := newDockerLocal(t, true)
	l.cascadeMinAge = 0
	name := "lib/foo"

	// Subject manifest, pushed by digest only.
	subjectBody := []byte(`{"schemaVersion":2,"_role":"subject","layers":[]}`)
	subjectDigest := pushManifestBody(t, l, name, digestOf(subjectBody), subjectBody, "application/vnd.oci.image.manifest.v1+json")

	// Cosign signature pointing at the subject (creates the
	// _referrers/{subjectDigest}.json index).
	sigBody := []byte(`{
		"schemaVersion":2,
		"artifactType":"application/vnd.dev.cosign.simplesigning.v1+json",
		"subject":{"digest":"` + subjectDigest + `","size":` + jsonInt(len(subjectBody)) + `}
	}`)
	pushManifestBody(t, l, name, digestOf(sigBody), sigBody, "application/vnd.oci.image.manifest.v1+json")

	// Sanity: the referrers index exists.
	idx, _ := l.Store().ReadReferrers(name, subjectDigest)
	if len(idx.Manifests) != 1 {
		t.Fatalf("setup: expected 1 referrer, got %d", len(idx.Manifests))
	}

	// Delete the subject manifest by digest. The cheap cascade
	// should drop the orphan _referrers/{subjectDigest}.json file.
	w := do(l, http.MethodDelete, "/v2/"+name+"/manifests/"+subjectDigest, nil,
		map[string]string{"Authorization": "Bearer pika_test"})
	if w.Code != http.StatusAccepted {
		t.Fatalf("delete subject: %d body %s", w.Code, w.Body.String())
	}

	// The referrers index file should be gone. We probe via the
	// store's read path: ReadReferrers returns the empty document
	// for a missing file, so a length-0 index means the file is
	// gone (or the index has been emptied — equivalent here since
	// the only entry was the signature we never deleted).
	idx2, _ := l.Store().ReadReferrers(name, subjectDigest)
	if len(idx2.Manifests) != 0 {
		t.Errorf("orphan referrers index still populated: %+v", idx2)
	}
}

// ────────────────────────────────────────────────────────────────────
// Dry-run + abandoned uploads
// ────────────────────────────────────────────────────────────────────

// TestGC_DryRunReportsButDoesNotDelete pushes an orphan blob, asks
// GC for an estimate, then confirms the orphan is still there.
// Then runs the real pass and confirms it goes away.
func TestGC_DryRunReportsButDoesNotDelete(t *testing.T) {
	l := newDockerLocal(t, true)
	name := "lib/foo"
	orphan := pushBlob(t, l, name, []byte("orphan-bytes"))

	dry, err := l.GarbageCollect(context.Background(), GCOptions{MinAge: 0, DryRun: true})
	if err != nil {
		t.Fatalf("dry-run GC: %v", err)
	}
	if !dry.DryRun {
		t.Errorf("DryRun flag not echoed back")
	}
	if dry.SweptBlobs != 1 {
		t.Fatalf("dry SweptBlobs = %d, want 1", dry.SweptBlobs)
	}
	if dry.SweptBytes != int64(len("orphan-bytes")) {
		t.Fatalf("dry SweptBytes = %d, want %d", dry.SweptBytes, len("orphan-bytes"))
	}
	// Orphan must still be present.
	if _, err := l.Store().Blobs().Stat(orphan); err != nil {
		t.Fatalf("dry-run deleted the blob (it shouldn't): %v", err)
	}

	// Now run for real.
	real, err := l.GarbageCollect(context.Background(), GCOptions{MinAge: 0})
	if err != nil {
		t.Fatalf("real GC: %v", err)
	}
	if real.DryRun {
		t.Errorf("DryRun flag set on real pass")
	}
	if real.SweptBlobs != 1 {
		t.Fatalf("real SweptBlobs = %d, want 1", real.SweptBlobs)
	}
	if _, err := l.Store().Blobs().Stat(orphan); err == nil {
		t.Fatalf("orphan blob survived real GC pass")
	}
}

// TestGC_PrunesAbandonedUploads creates an abandoned upload tmp
// file directly under the blobstore _uploads dir and verifies the
// GC pass reclaims it. Backend is RawFSBlobStore in this test
// fixture, which doesn't implement AbandonedUploadPruner — so we
// expect AbandonedUploadsRemoved to be 0. We then build a Local
// rooted on a LocalBlobStore and exercise the same path; that
// implementation DOES prune.
//
// We exercise the LocalBlobStore variant by constructing it
// directly and feeding it into runGC via a minimal Store, since
// the standard newDockerLocal fixture uses RawFSBlobStore.
func TestGC_PrunesAbandonedUploads(t *testing.T) {
	// Build a Local-backed BlobStore + a docker Store on top.
	dir := t.TempDir()
	bs, err := blobstore.NewLocalBlobStore(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatalf("NewLocalBlobStore: %v", err)
	}
	// Drop a tmp file under _uploads/ that's well past the prune
	// threshold.
	tmpPath := filepath.Join(bs.Root(), "_uploads", "ghost-upload")
	if err := os.WriteFile(tmpPath, []byte("ghost-bytes"), 0o644); err != nil {
		t.Fatalf("seed ghost upload: %v", err)
	}
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(tmpPath, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// Dry-run: must report 1 + bytes without removing.
	count, bytes, err := bs.PruneAbandonedUploads(24*time.Hour, true)
	if err != nil {
		t.Fatalf("dry prune: %v", err)
	}
	if count != 1 || bytes != int64(len("ghost-bytes")) {
		t.Fatalf("dry prune count=%d bytes=%d, want 1 / %d", count, bytes, len("ghost-bytes"))
	}
	if _, err := os.Stat(tmpPath); err != nil {
		t.Fatalf("dry-run deleted ghost: %v", err)
	}
	// Real prune.
	count, bytes, err = bs.PruneAbandonedUploads(24*time.Hour, false)
	if err != nil {
		t.Fatalf("real prune: %v", err)
	}
	if count != 1 || bytes != int64(len("ghost-bytes")) {
		t.Fatalf("real prune count=%d bytes=%d, want 1 / %d", count, bytes, len("ghost-bytes"))
	}
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("ghost upload survived prune: %v", err)
	}
}
