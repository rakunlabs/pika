package docker

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/rakunlabs/pika/internal/registry/blobstore"
)

// Request parsing and shared HTTP helpers for the Docker registry.
//
// The dispatcher (used by Local.ServeHTTP) classifies the URL into
// one of the protocol operations, then routes to the operation
// handler. Auth gating happens before classification.

// dockerOp is the discriminator for the operations we recognise.
type dockerOp int

const (
	opUnknown dockerOp = iota
	opVersionProbe
	opToken
	opManifest
	opBlob
	opUploadStart
	opUploadProgress
	opUploadAppend
	opUploadFinalize
	opUploadCancel
	opTagsList
	opCatalog
	opReferrers
)

// parsedRequest holds the parsed components of a docker /v2/ URL.
type parsedRequest struct {
	Op       dockerOp
	Name     string
	Ref      string             // manifest ref (tag or digest)
	Digest   blobstore.Digest   // blob/manifest digest when applicable
	UploadID string
}

// classify parses one /v2/... URL + HTTP method into a parsedRequest.
// Returns ok=false for shapes we don't recognise.
func classify(method, p string) (parsedRequest, bool) {
	if !strings.HasPrefix(p, "/v2") {
		return parsedRequest{}, false
	}

	// /v2/ and /v2 → version probe.
	if p == "/v2" || p == "/v2/" {
		return parsedRequest{Op: opVersionProbe}, true
	}

	// /v2/token → token endpoint.
	if p == "/v2/token" {
		return parsedRequest{Op: opToken}, true
	}

	// /v2/_catalog → catalog.
	if p == "/v2/_catalog" {
		return parsedRequest{Op: opCatalog}, true
	}

	// Everything else is "/v2/{name}/{...}". {name} may contain
	// slashes (e.g. "lib/nginx"), so we split off the operation
	// marker first by looking for known suffix anchors.

	body := strings.TrimPrefix(p, "/v2/")

	// /v2/{name}/blobs/uploads/[{uuid}]
	if i := strings.Index(body, "/blobs/uploads"); i > 0 {
		name := body[:i]
		rest := body[i+len("/blobs/uploads"):]
		if err := ValidateRepoName(name); err != nil {
			return parsedRequest{}, false
		}
		switch {
		case rest == "" || rest == "/":
			if method == http.MethodPost {
				return parsedRequest{Op: opUploadStart, Name: name}, true
			}
		case strings.HasPrefix(rest, "/"):
			uuid := rest[1:]
			if uuid == "" {
				return parsedRequest{}, false
			}
			switch method {
			case http.MethodGet:
				return parsedRequest{Op: opUploadProgress, Name: name, UploadID: uuid}, true
			case http.MethodPatch:
				return parsedRequest{Op: opUploadAppend, Name: name, UploadID: uuid}, true
			case http.MethodPut:
				return parsedRequest{Op: opUploadFinalize, Name: name, UploadID: uuid}, true
			case http.MethodDelete:
				return parsedRequest{Op: opUploadCancel, Name: name, UploadID: uuid}, true
			}
		}
		return parsedRequest{}, false
	}

	// /v2/{name}/blobs/{digest}
	if i := strings.LastIndex(body, "/blobs/"); i > 0 {
		name := body[:i]
		ref := body[i+len("/blobs/"):]
		if err := ValidateRepoName(name); err != nil {
			return parsedRequest{}, false
		}
		dgst, err := blobstore.ParseDigest(ref)
		if err != nil {
			return parsedRequest{}, false
		}
		return parsedRequest{Op: opBlob, Name: name, Digest: dgst}, true
	}

	// /v2/{name}/manifests/{ref}
	if i := strings.LastIndex(body, "/manifests/"); i > 0 {
		name := body[:i]
		ref := body[i+len("/manifests/"):]
		if err := ValidateRepoName(name); err != nil {
			return parsedRequest{}, false
		}
		// ref can be a digest or a tag.
		if IsDigestReference(ref) {
			d, err := blobstore.ParseDigest(ref)
			if err != nil {
				return parsedRequest{}, false
			}
			return parsedRequest{Op: opManifest, Name: name, Ref: ref, Digest: d}, true
		}
		if err := ValidateTag(ref); err != nil {
			return parsedRequest{}, false
		}
		return parsedRequest{Op: opManifest, Name: name, Ref: ref}, true
	}

	// /v2/{name}/tags/list
	if strings.HasSuffix(body, "/tags/list") {
		name := strings.TrimSuffix(body, "/tags/list")
		if err := ValidateRepoName(name); err != nil {
			return parsedRequest{}, false
		}
		return parsedRequest{Op: opTagsList, Name: name}, true
	}

	// /v2/{name}/referrers/{digest}  (OCI 1.1, Faz 4)
	if i := strings.LastIndex(body, "/referrers/"); i > 0 {
		name := body[:i]
		ref := body[i+len("/referrers/"):]
		if err := ValidateRepoName(name); err != nil {
			return parsedRequest{}, false
		}
		dgst, err := blobstore.ParseDigest(ref)
		if err != nil {
			return parsedRequest{}, false
		}
		return parsedRequest{Op: opReferrers, Name: name, Digest: dgst}, true
	}

	return parsedRequest{}, false
}

// ─── HTTP error envelope ───────────────────────────────────────────
//
// OCI distribution error response shape:
//
//	{"errors":[{"code":"NAME_INVALID","message":"...","detail":...}]}
//
// We use a compact helper for each error to keep the response
// shape consistent.

type ociError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type ociErrorEnvelope struct {
	Errors []ociError `json:"errors"`
}

// writeError emits an OCI-style error response with the given status.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"errors":[{"code":%q,"message":%q}]}`, code, message)
}

// mapError translates a domain error into an HTTP response.
func mapError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrBlobUnknown):
		writeError(w, http.StatusNotFound, "BLOB_UNKNOWN", err.Error())
	case errors.Is(err, ErrManifestUnknown):
		writeError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", err.Error())
	case errors.Is(err, ErrTagUnknown):
		writeError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", err.Error())
	case errors.Is(err, ErrNameInvalid):
		writeError(w, http.StatusBadRequest, "NAME_INVALID", err.Error())
	case errors.Is(err, ErrDigestInvalid):
		writeError(w, http.StatusBadRequest, "DIGEST_INVALID", err.Error())
	case errors.Is(err, ErrUploadUnknown):
		writeError(w, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", err.Error())
	case errors.Is(err, ErrUnsupportedMedia):
		writeError(w, http.StatusUnsupportedMediaType, "MANIFEST_INVALID", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "UNKNOWN", err.Error())
	}
}

// writeOK writes a 200 with optional headers.
func writeOK(w http.ResponseWriter, contentType string, body []byte) {
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if body != nil {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	}
	w.WriteHeader(http.StatusOK)
	if body != nil {
		_, _ = w.Write(body)
	}
}
