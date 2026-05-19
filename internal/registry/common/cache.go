package common

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Cache-related HTTP helpers used by every protocol head.
//
// Two patterns we need uniformly:
//
//  1. Mutable metadata (NPM packument, Go @v/list, Docker tag→manifest).
//     Send `ETag` + short Cache-Control; honour If-None-Match on
//     subsequent fetches to send 304s. Reduces bandwidth on hot
//     packages without losing freshness.
//
//  2. Immutable artifacts (Go .zip / .mod by version, NPM tarball,
//     Docker blob by digest). Send `Cache-Control: public, max-age=
//     31536000, immutable`. Clients (and any in-front CDN) can cache
//     forever — the URL identifies the content, so a different bytes
//     must come from a different URL.

// EtagFor computes a strong ETag from a content fingerprint. The
// fingerprint is typically a digest the protocol head already has on
// hand (sha256 of a tarball, sha256 of a serialised packument JSON).
// We re-hash inside to keep the ETag opaque to clients — they should
// never parse it.
//
// Returns a quoted form ready to be set as the ETag header value
// (RFC 7232 §2.3 mandates the quotes).
func EtagFor(fingerprint string) string {
	if fingerprint == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(fingerprint))
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// SetMutableCache marks a response as mutable-and-revalidatable. The
// client (and any in-between cache) is told to revalidate before
// reuse, which combined with the ETag turns most repeat requests
// into cheap 304 round-trips.
//
// maxAge of 0 falls back to a 60-second floor — fully disabling
// client caching costs more than it's worth (npm clients refresh
// the packument on every install).
func SetMutableCache(w http.ResponseWriter, etag string, maxAge time.Duration) {
	if etag != "" {
		w.Header().Set("ETag", etag)
	}
	seconds := int(maxAge.Seconds())
	if seconds <= 0 {
		seconds = 60
	}
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(seconds)+", must-revalidate")
}

// SetImmutableCache marks a response as content-addressed and
// therefore eligible for indefinite caching. Used for Go zip
// downloads, NPM tarballs, Docker blob fetches by digest.
func SetImmutableCache(w http.ResponseWriter, etag string) {
	if etag != "" {
		w.Header().Set("ETag", etag)
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
}

// MatchIfNoneMatch returns true when the request's If-None-Match
// header matches the supplied etag. Used by protocol heads to short-
// circuit a write with a 304 status.
//
// The comparison is per RFC 7232: weak comparator, multiple comma-
// separated entries allowed, "*" matches anything. Pika never emits
// weak ETags so a strong comparison would also work — but the weak
// form is the safer default.
func MatchIfNoneMatch(r *http.Request, etag string) bool {
	if etag == "" {
		return false
	}
	header := r.Header.Get("If-None-Match")
	if header == "" {
		return false
	}
	if strings.TrimSpace(header) == "*" {
		return true
	}
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		// Drop the "W/" weak prefix from the candidate for comparison.
		candidate = strings.TrimPrefix(candidate, "W/")
		etagCmp := strings.TrimPrefix(etag, "W/")
		if candidate == etagCmp {
			return true
		}
	}
	return false
}
