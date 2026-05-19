package docker

import (
	"context"
	"errors"
	"testing"

	"github.com/rakunlabs/pika/internal/registry"
	"github.com/rakunlabs/pika/internal/registry/blobstore"
)

// TestInspectManifestBytes_ImageManifest_ParsesLayers verifies the
// inspector pulls config digest + layer list out of a standard
// OCI image manifest.
func TestInspectManifestBytes_ImageManifest_ParsesLayers(t *testing.T) {
	body := []byte(`{
		"schemaVersion": 2,
		"mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": {
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"digest": "sha256:abcd",
			"size": 100
		},
		"layers": [
			{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:l1","size":1000},
			{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:l2","size":2000}
		]
	}`)
	insp := InspectManifestBytes(body)
	if insp == nil {
		t.Fatal("nil inspection")
	}
	if insp.ConfigDigest != "sha256:abcd" {
		t.Errorf("config_digest=%q", insp.ConfigDigest)
	}
	if len(insp.Layers) != 2 {
		t.Fatalf("layers=%d", len(insp.Layers))
	}
	if insp.ImageSize != 3000 {
		t.Errorf("image_size=%d want 3000", insp.ImageSize)
	}
	// Generic image config — artifactType should remain empty.
	if insp.ArtifactType != "" {
		t.Errorf("unexpected artifact_type=%q", insp.ArtifactType)
	}
}

// TestInspectManifestBytes_HelmArtifact_DerivesArtifactType
// verifies a Helm OCI manifest (config mediaType = helm chart
// config) surfaces as a non-empty artifact type even when
// `artifactType` is omitted.
func TestInspectManifestBytes_HelmArtifact_DerivesArtifactType(t *testing.T) {
	body := []byte(`{
		"schemaVersion": 2,
		"mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": {
			"mediaType": "application/vnd.cncf.helm.config.v1+json",
			"digest": "sha256:cfg"
		},
		"layers": [
			{"mediaType":"application/vnd.cncf.helm.chart.content.v1.tar+gzip","digest":"sha256:chart","size":500}
		]
	}`)
	insp := InspectManifestBytes(body)
	if insp == nil {
		t.Fatal("nil inspection")
	}
	if insp.ArtifactType != "application/vnd.cncf.helm.config.v1+json" {
		t.Errorf("artifact_type=%q", insp.ArtifactType)
	}
}

// TestInspectManifestBytes_ManifestList_ParsesPlatforms covers
// multi-arch image indexes.
func TestInspectManifestBytes_ManifestList_ParsesPlatforms(t *testing.T) {
	body := []byte(`{
		"schemaVersion": 2,
		"mediaType": "application/vnd.oci.image.index.v1+json",
		"manifests": [
			{"digest":"sha256:linux-amd64","size":100,"platform":{"os":"linux","architecture":"amd64"}},
			{"digest":"sha256:linux-arm64","size":120,"platform":{"os":"linux","architecture":"arm64"}}
		]
	}`)
	insp := InspectManifestBytes(body)
	if insp == nil {
		t.Fatal("nil inspection")
	}
	if len(insp.Platforms) != 2 {
		t.Fatalf("platforms=%d", len(insp.Platforms))
	}
	if insp.Platforms[0].OS != "linux" || insp.Platforms[0].Architecture != "amd64" {
		t.Errorf("platform[0]=%+v", insp.Platforms[0])
	}
}

// TestDockerLocal_PackageDetail_EmptyImage verifies the
// PackageNotFound sentinel for an image that doesn't exist.
func TestDockerLocal_PackageDetail_EmptyImage(t *testing.T) {
	l := newDockerLocal(t, true)
	_, err := l.PackageDetail(context.Background(), "ghost-image")
	if !errors.Is(err, registry.ErrPackageNotFound) {
		t.Errorf("err=%v want ErrPackageNotFound", err)
	}
}

// TestDockerLocal_PackageDetail_TagWithLayers writes a manifest +
// tag directly via the store API and confirms the detail builder
// surfaces digest + manifest_size + image_size + layers.
func TestDockerLocal_PackageDetail_TagWithLayers(t *testing.T) {
	l := newDockerLocal(t, true)
	body := []byte(`{
		"schemaVersion": 2,
		"mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": {"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:cfg","size":50},
		"layers": [
			{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:l1","size":1024}
		]
	}`)
	dgst := sha256OfBytes(body)
	if err := l.store.WriteManifest("myapp", dgst, body,
		"application/vnd.oci.image.manifest.v1+json"); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	if err := l.store.SetTag("myapp", "v1.0.0", dgst); err != nil {
		t.Fatalf("SetTag: %v", err)
	}
	det, err := l.PackageDetail(context.Background(), "myapp")
	if err != nil {
		t.Fatalf("PackageDetail: %v", err)
	}
	if det.Docker == nil || len(det.Docker.Tags) != 1 {
		t.Fatalf("tags: %+v", det.Docker)
	}
	row := det.Docker.Tags[0]
	if row.Tag != "v1.0.0" {
		t.Errorf("tag=%q", row.Tag)
	}
	if row.Digest != dgst.String() {
		t.Errorf("digest=%q want %q", row.Digest, dgst.String())
	}
	if row.ManifestSize != int64(len(body)) {
		t.Errorf("manifest_size=%d want %d", row.ManifestSize, len(body))
	}
	if row.ImageSize != 1024 {
		t.Errorf("image_size=%d want 1024", row.ImageSize)
	}
	if len(row.Layers) != 1 {
		t.Errorf("layers=%d", len(row.Layers))
	}
	if row.ConfigDigest != "sha256:cfg" {
		t.Errorf("config_digest=%q", row.ConfigDigest)
	}
}

// _ keep blobstore import used by sha256OfBytes from the existing
// test helper.
var _ = blobstore.Digest{}
