package docker

import (
	"encoding/json"
)

// OCI artifact type recognition and referrers index maintenance.
//
// OCI Distribution Spec v1.1 introduced two key fields on manifests:
//
//   - artifactType: a media type indicating what kind of artifact
//     the manifest represents. Examples:
//       application/vnd.cncf.helm.config.v1+json     → Helm chart
//       application/vnd.dev.cosign.simplesigning.v1+json → Cosign signature
//       application/vnd.in-toto+json                 → SLSA attestation
//       application/spdx+json                        → SBOM
//
//   - subject: a descriptor pointing at another manifest the current
//     one refers to. E.g. a cosign signature manifest carries a
//     subject pointing at the image manifest it signs; an SBOM
//     manifest's subject points at the image it describes.
//
// The /v2/{name}/referrers/{digest} endpoint returns every manifest
// whose subject.digest matches {digest}. Clients (cosign verify,
// SBOM tooling, Helm) use this to discover related artifacts
// without having to scan the entire repo.
//
// Spec: https://github.com/opencontainers/distribution-spec/blob/main/spec.md#listing-referrers
//
// Index maintenance strategy
//
// We persist a per-(repo, subject-digest) referrers index file at
// `repositories/{name}/_referrers/{subject-digest}.json`. The file
// holds an OCI Image Index document whose `manifests` array lists
// the referrers. The index is updated on every manifest write:
// when a manifest with a `subject` is pushed, we append its
// descriptor to the corresponding index file; when a manifest is
// deleted, we drop it from the index.
//
// Why a derived index instead of scanning manifests on demand
//
// Scan-on-read would require walking every manifest file under
// repositories/{name}/manifests/ on each /referrers/ request,
// parsing each, comparing subject.digest. That's O(manifests) per
// request and gets expensive fast for repos with thousands of
// images. The materialised index is O(1) read + O(1) write.

// manifestDescriptor is the small subset of OCI Image Manifest fields
// we extract to keep referrers indexes compact.
type manifestDescriptor struct {
	MediaType    string            `json:"mediaType"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Digest       string            `json:"digest"`
	Size         int64             `json:"size"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

// inspectedManifest holds the fields we care about when parsing a
// pushed manifest body.
type inspectedManifest struct {
	MediaType    string            `json:"mediaType,omitempty"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Config       *configDescriptor `json:"config,omitempty"`
	Subject      *subjectDescriptor `json:"subject,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

// configDescriptor matches the manifest.config block (OCI image
// manifest). We use config.mediaType as a fallback artifactType
// when the manifest itself doesn't carry one (older Helm 3 charts,
// for example, encode the type via config.mediaType).
type configDescriptor struct {
	MediaType string `json:"mediaType,omitempty"`
}

// subjectDescriptor matches manifest.subject in OCI v1.1.
type subjectDescriptor struct {
	MediaType string `json:"mediaType,omitempty"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size,omitempty"`
}

// inspectManifest parses the subset of fields we care about from a
// raw manifest body. Returns (nil, nil) when the body isn't valid
// JSON — callers treat absent inspection as "no subject, no
// artifactType" rather than failing the push.
func inspectManifest(body []byte) *inspectedManifest {
	var m inspectedManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil
	}
	return &m
}

// ArtifactTypeOf is a small public wrapper around inspectManifest +
// effectiveArtifactType for use by the admin API. Returns "" when
// the body is unparseable or carries no distinguishing artifact
// type (plain container image).
func ArtifactTypeOf(manifestBody []byte) string {
	insp := inspectManifest(manifestBody)
	if insp == nil {
		return ""
	}
	return insp.effectiveArtifactType()
}

// effectiveArtifactType returns the most specific artifact type we
// can derive from the parsed manifest. Preference:
//
//   1. manifest.artifactType (OCI v1.1 explicit field).
//   2. manifest.config.mediaType when it's not the generic image
//      config type (i.e. the manifest is repurposed as an artifact
//      carrier).
//
// Returns "" when no artifact type is detectable. The generic
// "application/vnd.oci.image.config.v1+json" and the docker image
// config type are explicitly NOT surfaced as artifact types —
// those are just plain container images.
func (m *inspectedManifest) effectiveArtifactType() string {
	if m.ArtifactType != "" {
		return m.ArtifactType
	}
	if m.Config != nil && m.Config.MediaType != "" {
		switch m.Config.MediaType {
		case "application/vnd.oci.image.config.v1+json",
			"application/vnd.docker.container.image.v1+json":
			return ""
		}
		return m.Config.MediaType
	}
	return ""
}

// ociImageIndex is the OCI Image Index document shape — also the
// shape of the /v2/{name}/referrers/{digest} response.
type ociImageIndex struct {
	SchemaVersion int                  `json:"schemaVersion"`
	MediaType     string               `json:"mediaType"`
	Manifests     []manifestDescriptor `json:"manifests"`
}

// newEmptyReferrersIndex returns the empty index document we send
// when no referrers exist for a digest.
func newEmptyReferrersIndex() ociImageIndex {
	return ociImageIndex{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.index.v1+json",
		Manifests:     []manifestDescriptor{},
	}
}
