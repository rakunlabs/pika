// Package docker implements the OCI Distribution Spec v1.1 / Docker
// Registry HTTP API v2 on top of pika's BlobStore + rawfs layers.
//
// What's supported in this phase
//
//   - GET /v2/                              — version probe with bearer challenge
//   - GET /v2/token                         — bearer token issuance
//   - HEAD/GET /v2/{name}/manifests/{ref}   — manifest by tag or digest
//   - PUT /v2/{name}/manifests/{ref}        — push manifest
//   - DELETE /v2/{name}/manifests/{ref}     — delete manifest
//   - HEAD/GET /v2/{name}/blobs/{digest}    — blob fetch
//   - DELETE /v2/{name}/blobs/{digest}      — blob delete
//   - POST /v2/{name}/blobs/uploads/        — start upload (returns Location)
//   - PATCH /v2/{name}/blobs/uploads/{uuid} — chunked upload append
//   - PUT /v2/{name}/blobs/uploads/{uuid}?digest=... — finalise upload
//   - GET /v2/{name}/blobs/uploads/{uuid}   — upload progress
//   - GET /v2/{name}/tags/list              — tag list with pagination
//   - GET /v2/_catalog                      — repo catalog
//
// # Auth challenge model
//
// Docker clients first poke /v2/ unauthenticated. We reply 401 with
// a WWW-Authenticate header pointing at our token endpoint. The
// client then exchanges its credentials (pika token, via Basic auth)
// for a short-lived bearer JWT and carries that on subsequent
// requests. The JWT carries the scope ("repository:foo/bar:pull,push")
// that gates which operations the holder may perform.
//
// Storage layout (under each repo's configured BasePath inside a
// raw mount):
//
//	blobs/sha256/{first2}/{full-digest}              CAS, via BlobStore
//	repositories/{name}/manifests/{digest}.json      manifest body
//	repositories/{name}/tags/{tag}                   pointer to digest
//	repositories/{name}/_uploads/{uuid}              in-flight upload sess
//	repositories/{name}/_referrers/{digest}.json     OCI 1.1 referrers (Faz 4)
//
// Note: the upload UUID space is local to the Registry; cross-repo
// upload session resumption is not supported (matching the spec's
// per-repository upload session contract).
package docker

import "errors"

// Sentinel errors used inside the package. The HTTP layer maps them
// to OCI-style error codes (BLOB_UNKNOWN, MANIFEST_UNKNOWN, ...) via
// httpError helpers in handler.go.
var (
	ErrBlobUnknown      = errors.New("docker: blob unknown")
	ErrManifestUnknown  = errors.New("docker: manifest unknown")
	ErrTagUnknown       = errors.New("docker: tag unknown")
	ErrTagImmutable     = errors.New("docker: tag immutable")
	ErrNameInvalid      = errors.New("docker: invalid name")
	ErrDigestInvalid    = errors.New("docker: invalid digest")
	ErrUploadUnknown    = errors.New("docker: upload session unknown")
	ErrUnsupportedMedia = errors.New("docker: unsupported media type")
	ErrSizeInvalid      = errors.New("docker: size invalid")
)
