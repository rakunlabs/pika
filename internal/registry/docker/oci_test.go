package docker

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"
)

// pushManifestBody is a helper that pushes a manifest body and
// returns the resulting digest string. Fails the test on non-201.
func pushManifestBody(t *testing.T, l *Local, name, ref string, body []byte, mediaType string) string {
	t.Helper()
	if mediaType == "" {
		mediaType = "application/vnd.oci.image.manifest.v1+json"
	}
	w := do(l, http.MethodPut, "/v2/"+name+"/manifests/"+ref, bytes.NewReader(body),
		map[string]string{
			"Authorization": "Bearer pika_test",
			"Content-Type":  mediaType,
		})
	if w.Code != http.StatusCreated {
		t.Fatalf("push manifest %s status %d body %s", ref, w.Code, w.Body.String())
	}
	return w.Header().Get("Docker-Content-Digest")
}

// digestOf computes the sha256 digest of a manifest body in the
// canonical "sha256:hex" form, as it would appear on the wire.
func digestOf(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestOCI_InspectManifest(t *testing.T) {
	cases := []struct {
		name         string
		body         string
		wantArtType  string
		wantSubject  string
	}{
		{
			"plain image",
			`{"schemaVersion":2,"config":{"mediaType":"application/vnd.oci.image.config.v1+json"}}`,
			"",
			"",
		},
		{
			"helm chart via config",
			`{"schemaVersion":2,"config":{"mediaType":"application/vnd.cncf.helm.config.v1+json"}}`,
			"application/vnd.cncf.helm.config.v1+json",
			"",
		},
		{
			"explicit artifactType",
			`{"schemaVersion":2,"artifactType":"application/spdx+json"}`,
			"application/spdx+json",
			"",
		},
		{
			"with subject",
			`{"schemaVersion":2,"artifactType":"application/vnd.dev.cosign.simplesigning.v1+json","subject":{"digest":"sha256:abc","size":123}}`,
			"application/vnd.dev.cosign.simplesigning.v1+json",
			"sha256:abc",
		},
		{
			"docker image config",
			`{"schemaVersion":2,"config":{"mediaType":"application/vnd.docker.container.image.v1+json"}}`,
			"",
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			insp := inspectManifest([]byte(tc.body))
			if insp == nil {
				t.Fatal("inspect returned nil")
			}
			if got := insp.effectiveArtifactType(); got != tc.wantArtType {
				t.Errorf("artifactType = %q, want %q", got, tc.wantArtType)
			}
			subj := ""
			if insp.Subject != nil {
				subj = insp.Subject.Digest
			}
			if subj != tc.wantSubject {
				t.Errorf("subject = %q, want %q", subj, tc.wantSubject)
			}
		})
	}
}

func TestOCI_ReferrersEmpty(t *testing.T) {
	l := newDockerLocal(t, true)
	w := do(l, http.MethodGet, "/v2/lib/foo/referrers/sha256:"+hex.EncodeToString(make([]byte, 32)), nil,
		map[string]string{"Authorization": "Bearer pika_test"})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var idx ociImageIndex
	_ = json.Unmarshal(w.Body.Bytes(), &idx)
	if idx.SchemaVersion != 2 {
		t.Errorf("schemaVersion=%d", idx.SchemaVersion)
	}
	if len(idx.Manifests) != 0 {
		t.Errorf("expected empty manifests, got %d", len(idx.Manifests))
	}
}

func TestOCI_PushSubjectThenReferrer(t *testing.T) {
	l := newDockerLocal(t, true)
	name := "lib/foo"

	// 1) Push a subject (plain image) manifest.
	subjectBody := []byte(`{"schemaVersion":2,"config":{"mediaType":"application/vnd.oci.image.config.v1+json"},"layers":[]}`)
	subjectDigest := pushManifestBody(t, l, name, "v1.0.0", subjectBody, "application/vnd.oci.image.manifest.v1+json")

	// 2) Push a cosign signature manifest that points at the subject.
	sigBody := []byte(`{
		"schemaVersion":2,
		"artifactType":"application/vnd.dev.cosign.simplesigning.v1+json",
		"config":{"mediaType":"application/vnd.oci.image.config.v1+json"},
		"layers":[],
		"subject":{
			"mediaType":"application/vnd.oci.image.manifest.v1+json",
			"digest":"` + subjectDigest + `",
			"size":` + jsonInt(len(subjectBody)) + `
		}
	}`)
	sigDigest := pushManifestBody(t, l, name, digestOf(sigBody), sigBody, "application/vnd.oci.image.manifest.v1+json")

	// The push response should carry OCI-Subject header.
	// (We verified the status; the helper doesn't return headers.)
	// Instead, query the referrers endpoint.

	// 3) GET /v2/{name}/referrers/{subject-digest}
	w := do(l, http.MethodGet, "/v2/"+name+"/referrers/"+subjectDigest, nil,
		map[string]string{"Authorization": "Bearer pika_test"})
	if w.Code != http.StatusOK {
		t.Fatalf("referrers status %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/vnd.oci.image.index.v1+json" {
		t.Errorf("content-type %q", ct)
	}
	var idx ociImageIndex
	if err := json.Unmarshal(w.Body.Bytes(), &idx); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(idx.Manifests) != 1 {
		t.Fatalf("expected 1 referrer, got %d (%+v)", len(idx.Manifests), idx.Manifests)
	}
	if idx.Manifests[0].Digest != sigDigest {
		t.Errorf("referrer digest %q, want %q", idx.Manifests[0].Digest, sigDigest)
	}
	if idx.Manifests[0].ArtifactType != "application/vnd.dev.cosign.simplesigning.v1+json" {
		t.Errorf("artifactType %q", idx.Manifests[0].ArtifactType)
	}
}

func TestOCI_ReferrersArtifactTypeFilter(t *testing.T) {
	l := newDockerLocal(t, true)
	name := "lib/foo"

	subjectBody := []byte(`{"schemaVersion":2,"config":{"mediaType":"application/vnd.oci.image.config.v1+json"},"layers":[]}`)
	subjectDigest := pushManifestBody(t, l, name, "v1.0.0", subjectBody, "application/vnd.oci.image.manifest.v1+json")

	// Push two referrers with different artifact types.
	cosignBody := []byte(`{
		"schemaVersion":2,
		"artifactType":"application/vnd.dev.cosign.simplesigning.v1+json",
		"config":{"mediaType":"application/vnd.oci.image.config.v1+json"},
		"subject":{"digest":"` + subjectDigest + `","size":` + jsonInt(len(subjectBody)) + `}
	}`)
	sbomBody := []byte(`{
		"schemaVersion":2,
		"artifactType":"application/spdx+json",
		"config":{"mediaType":"application/vnd.oci.image.config.v1+json"},
		"subject":{"digest":"` + subjectDigest + `","size":` + jsonInt(len(subjectBody)) + `}
	}`)
	pushManifestBody(t, l, name, digestOf(cosignBody), cosignBody, "application/vnd.oci.image.manifest.v1+json")
	pushManifestBody(t, l, name, digestOf(sbomBody), sbomBody, "application/vnd.oci.image.manifest.v1+json")

	// Unfiltered: 2 referrers.
	w := do(l, http.MethodGet, "/v2/"+name+"/referrers/"+subjectDigest, nil,
		map[string]string{"Authorization": "Bearer pika_test"})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var idx ociImageIndex
	_ = json.Unmarshal(w.Body.Bytes(), &idx)
	if len(idx.Manifests) != 2 {
		t.Fatalf("expected 2 referrers, got %d", len(idx.Manifests))
	}

	// Filtered to cosign only. "+" in artifactType media types must
	// be percent-encoded in URL query string so it survives form-
	// encoding's "+" → " " mapping.
	w = do(l, http.MethodGet, "/v2/"+name+"/referrers/"+subjectDigest+"?artifactType=application/vnd.dev.cosign.simplesigning.v1%2Bjson",
		nil, map[string]string{"Authorization": "Bearer pika_test"})
	if w.Code != http.StatusOK {
		t.Fatalf("filtered status %d", w.Code)
	}
	if w.Header().Get("OCI-Filters-Applied") != "artifactType" {
		t.Errorf("OCI-Filters-Applied header missing")
	}
	_ = json.Unmarshal(w.Body.Bytes(), &idx)
	if len(idx.Manifests) != 1 {
		t.Fatalf("expected 1 cosign referrer, got %d", len(idx.Manifests))
	}
	if idx.Manifests[0].ArtifactType != "application/vnd.dev.cosign.simplesigning.v1+json" {
		t.Errorf("artifactType %q", idx.Manifests[0].ArtifactType)
	}
}

func TestOCI_ReferrerCleanupOnDelete(t *testing.T) {
	l := newDockerLocal(t, true)
	name := "lib/foo"

	subjectBody := []byte(`{"schemaVersion":2,"layers":[]}`)
	subjectDigest := pushManifestBody(t, l, name, "v1.0.0", subjectBody, "application/vnd.oci.image.manifest.v1+json")

	sigBody := []byte(`{
		"schemaVersion":2,
		"artifactType":"application/vnd.dev.cosign.simplesigning.v1+json",
		"subject":{"digest":"` + subjectDigest + `","size":` + jsonInt(len(subjectBody)) + `}
	}`)
	sigDigest := pushManifestBody(t, l, name, digestOf(sigBody), sigBody, "application/vnd.oci.image.manifest.v1+json")

	// Sanity: referrer indexed.
	w := do(l, http.MethodGet, "/v2/"+name+"/referrers/"+subjectDigest, nil,
		map[string]string{"Authorization": "Bearer pika_test"})
	var idx ociImageIndex
	_ = json.Unmarshal(w.Body.Bytes(), &idx)
	if len(idx.Manifests) != 1 {
		t.Fatalf("setup failed: %d referrers", len(idx.Manifests))
	}

	// Delete the referrer manifest.
	w = do(l, http.MethodDelete, "/v2/"+name+"/manifests/"+sigDigest, nil,
		map[string]string{"Authorization": "Bearer pika_test"})
	if w.Code != http.StatusAccepted {
		t.Fatalf("delete status %d", w.Code)
	}

	// Index now empty.
	w = do(l, http.MethodGet, "/v2/"+name+"/referrers/"+subjectDigest, nil,
		map[string]string{"Authorization": "Bearer pika_test"})
	_ = json.Unmarshal(w.Body.Bytes(), &idx)
	if len(idx.Manifests) != 0 {
		t.Fatalf("expected 0 referrers after delete, got %d", len(idx.Manifests))
	}
}

func TestOCI_HelmChartArtifactType(t *testing.T) {
	// Helm charts use config.mediaType (not artifactType) to mark
	// themselves. Confirm we surface the right type on referrers.
	l := newDockerLocal(t, true)
	name := "charts"

	subjectBody := []byte(`{"schemaVersion":2}`)
	subjectDigest := pushManifestBody(t, l, name, "v1.0.0", subjectBody, "application/vnd.oci.image.manifest.v1+json")

	helmBody := []byte(`{
		"schemaVersion":2,
		"config":{"mediaType":"application/vnd.cncf.helm.config.v1+json"},
		"subject":{"digest":"` + subjectDigest + `","size":` + jsonInt(len(subjectBody)) + `}
	}`)
	pushManifestBody(t, l, name, digestOf(helmBody), helmBody, "application/vnd.oci.image.manifest.v1+json")

	w := do(l, http.MethodGet, "/v2/"+name+"/referrers/"+subjectDigest, nil,
		map[string]string{"Authorization": "Bearer pika_test"})
	var idx ociImageIndex
	_ = json.Unmarshal(w.Body.Bytes(), &idx)
	if len(idx.Manifests) != 1 {
		t.Fatalf("expected 1 referrer, got %d", len(idx.Manifests))
	}
	if idx.Manifests[0].ArtifactType != "application/vnd.cncf.helm.config.v1+json" {
		t.Errorf("expected helm artifactType, got %q", idx.Manifests[0].ArtifactType)
	}
}

func TestOCI_PushManifestWithSubjectSetsHeader(t *testing.T) {
	l := newDockerLocal(t, true)
	name := "lib/foo"

	subjectDigest := "sha256:" + hex.EncodeToString(make([]byte, 32))
	body := []byte(`{
		"schemaVersion":2,
		"artifactType":"application/spdx+json",
		"subject":{"digest":"` + subjectDigest + `","size":100}
	}`)
	r := do(l, http.MethodPut, "/v2/"+name+"/manifests/"+digestOf(body), bytes.NewReader(body),
		map[string]string{
			"Authorization": "Bearer pika_test",
			"Content-Type":  "application/vnd.oci.image.manifest.v1+json",
		})
	if r.Code != http.StatusCreated {
		t.Fatalf("status %d", r.Code)
	}
	if got := r.Header().Get("OCI-Subject"); got != subjectDigest {
		t.Errorf("OCI-Subject header = %q, want %q", got, subjectDigest)
	}
}

// jsonInt is a tiny helper used in inline-built JSON fixtures so
// the size field renders as a bare number, not a quoted string.
func jsonInt(n int) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	x := n
	if n < 0 {
		out = append(out, '-')
		x = -n
	}
	var digits [20]byte
	i := len(digits)
	for x > 0 {
		i--
		digits[i] = byte('0' + (x % 10))
		x /= 10
	}
	out = append(out, digits[i:]...)
	return string(out)
}
