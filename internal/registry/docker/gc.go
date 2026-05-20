package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/pika/internal/hook"
	"github.com/rakunlabs/pika/internal/rawfs"
	"github.com/rakunlabs/pika/internal/registry/blobstore"
	"github.com/rakunlabs/pika/internal/registry/events"
)

// Garbage collection for Docker / OCI registries.
//
// Why this exists
//
// Pushing an image writes one blob per layer + one blob per config
// + one manifest. Deleting a tag removes the tag pointer but
// leaves the manifest and its referenced blobs intact — the spec
// doesn't define cascade-on-tag-delete semantics. Over time, an
// active registry accumulates unreferenced blobs and manifests
// ("garbage") that consume storage without serving any pull.
//
// Two-tier cleanup model
//
// Pika does NOT run a background scheduler. Cleanup happens in two
// places:
//
//   1. DELETE-TRIGGERED CHEAP CASCADE (docker/local.go deleteManifest):
//      When a manifest is deleted by digest we immediately drop
//      the manifest file + its .media sidecar (these are never
//      shared with other manifests) and remove the manifest's own
//      orphan referrers index file. If the deleted manifest was an
//      image index (multi-arch), we also cascade-delete each
//      sub-manifest IF no other tag in the same repo points at it
//      AND the sub-manifest is past the grace window. We never
//      touch layer / config blobs during cascade — those are
//      genuinely shareable and require the full mark walk to
//      reclaim safely. The cascade is therefore O(tags in this
//      repo), not O(global storage).
//
//   2. MANUAL MARK-AND-SWEEP (this file, runGC):
//      Operator-triggered full pass. Builds the live set across
//      every repo, sweeps unreferenced blobs + manifests, and
//      prunes abandoned upload tmp files (BlobStore that
//      implements AbandonedUploadPruner). Supports DryRun=true
//      for the "estimated garbage" UI surface — same code path,
//      just skips the os.Remove calls.
//
// The classic mark-and-sweep:
//
//   1. MARK phase: walk every repository, every tag → manifest, and
//      every manifest → blob references. Build the live set of
//      blob digests.
//
//   2. SWEEP phase: list every blob in the BlobStore. Drop the ones
//      not in the live set. Manifests not referenced by any tag
//      AND not referenced by any referrers index are also dropped.
//
// Concurrency note
//
// A concurrent push during a GC pass risks deleting a blob that's
// part of a manifest being written. The standard mitigation is a
// time-based grace period: blobs younger than (typically) 1 hour
// are never deleted, on the assumption that any in-flight push
// completes within that window. Pika's GC accepts a MinAge
// parameter for the same reason. The same grace window is honoured
// by the delete-triggered cascade.
//
// What we don't do (yet)
//
//   - Distributed coordination: in a multi-node pika deployment,
//     two nodes running GC simultaneously can race. The mitigation
//     is "run GC on the leader only" — left to the operator for
//     this MVP.
//   - Block reads during sweep: we don't pause traffic. The grace
//     period covers the race window in practice.

// GCStats summarises the result of one GC pass for the UI / logs.
//
// DryRun is faithfully reflected so the response shape is
// unambiguous: when true, the counters describe what WOULD have
// been reclaimed; when false, what was actually deleted.
type GCStats struct {
	// MarkedBlobs is the number of blobs reachable from at least
	// one manifest in the registry. Useful as a sanity check
	// alongside SweptBlobs.
	MarkedBlobs int `json:"marked_blobs"`
	// SweptBlobs counts blob deletions performed by this pass
	// (or would-be deletions when DryRun is set).
	SweptBlobs int `json:"swept_blobs"`
	// SweptBytes is the total size of swept blobs in bytes.
	// Counted from BlobInfo.Size at mark time; backends that don't
	// surface size leave this at 0.
	SweptBytes int64 `json:"swept_bytes"`
	// SweptManifests counts manifest deletions (unreferenced
	// manifests that no tag and no referrer pointed at). Manifest
	// file sizes are not counted into SweptBytes — they're small
	// JSON documents next to multi-MB layer blobs.
	SweptManifests int `json:"swept_manifests"`
	// SkippedYoung counts blobs / manifests skipped because they
	// were within the MinAge grace window.
	SkippedYoung int `json:"skipped_young"`
	// AbandonedUploadsRemoved is the count of upload tmp files
	// pruned in the same pass when the underlying BlobStore
	// implements AbandonedUploadPruner. Zero when the backend
	// doesn't expose the interface.
	AbandonedUploadsRemoved int `json:"abandoned_uploads_removed"`
	// AbandonedUploadsBytes is the total reclaimable size from
	// pruned upload tmp files in bytes.
	AbandonedUploadsBytes int64 `json:"abandoned_uploads_bytes"`
	// DryRun mirrors the option that produced these stats so the
	// caller can distinguish "estimate" from "did it".
	DryRun bool `json:"dry_run"`
	// Errors records any per-item failures the pass encountered.
	// A failure here is non-fatal — the pass keeps going so a
	// single broken file doesn't stop the rest.
	Errors []string `json:"errors,omitempty"`
}

// GCOptions tunes the pass.
type GCOptions struct {
	// MinAge is the grace window in seconds. Blobs / manifests
	// younger than time.Now() - MinAge are never deleted,
	// protecting in-flight pushes from sweeping their own
	// dependencies.
	//
	// Zero disables the grace check (use only for tests).
	MinAge int64

	// AbandonedUploadMaxAge is the grace window in seconds for
	// the upload tmp prune (separate from MinAge because
	// abandoned uploads are interrupted client state rather than
	// referenced data and benefit from a tighter default).
	//
	// Zero disables the upload prune entirely. The Docker local factory
	// applies the repo policy default when the operator doesn't override.
	AbandonedUploadMaxAge int64

	// DryRun reports estimated reclaimable garbage without
	// touching the filesystem. Stats fields are populated as if
	// the destructive pass ran, but no Delete / Remove calls are
	// issued. Used by the GET .../gc/estimate endpoint.
	DryRun bool
}

// runGC executes one mark-and-sweep pass against a Docker Store.
// Returns stats on success; errors only when the pass can't run
// (e.g. backend read-only).
//
// When opt.DryRun is true, the pass produces accurate counters
// without invoking any destructive backend call — used by the
// "estimated garbage" UI surface.
func runGC(ctx context.Context, s *Store, opt GCOptions) (*GCStats, error) {
	wfs, ok := s.fs.(rawfs.WritableRawFS)
	if !ok {
		return nil, errors.New("docker gc: backend is read-only")
	}

	live := newLiveSet()
	stats := &GCStats{DryRun: opt.DryRun}

	// ── MARK PHASE ─────────────────────────────────────────────────
	repos, err := s.ListRepositories()
	if err != nil {
		return nil, fmt.Errorf("gc: list repos: %w", err)
	}
	for _, name := range repos {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		if err := markRepo(s, name, live, stats); err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("mark %s: %v", name, err))
		}
	}
	stats.MarkedBlobs = len(live.blobs)

	// ── SWEEP PHASE ─────────────────────────────────────────────────
	now := nowSeconds()
	// Sweep blobs.
	err = s.Blobs().ListBlobs(func(d blobstore.Digest, info *blobstore.BlobInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if live.hasBlob(d.String()) {
			return nil
		}
		if opt.MinAge > 0 && info != nil && !info.ModTime.IsZero() {
			ageSec := now - info.ModTime.Unix()
			if ageSec < opt.MinAge {
				stats.SkippedYoung++
				return nil
			}
		}
		size := int64(0)
		if info != nil {
			size = info.Size
		}
		if !opt.DryRun {
			if err := s.Blobs().Delete(d); err != nil {
				stats.Errors = append(stats.Errors, fmt.Sprintf("sweep blob %s: %v", d, err))
				return nil
			}
		}
		stats.SweptBlobs++
		stats.SweptBytes += size
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		stats.Errors = append(stats.Errors, fmt.Sprintf("blob walk: %v", err))
	}

	// Sweep manifests that no tag and no referrers index pointed at.
	for _, name := range repos {
		swept, errs := sweepUnreferencedManifests(s, wfs, name, live, opt, now)
		stats.SweptManifests += swept
		stats.Errors = append(stats.Errors, errs...)
	}

	// ── ABANDONED UPLOAD PRUNE ─────────────────────────────────────
	// Folded into the same admin pass so one button reclaims
	// everything reclaimable. Only runs when the BlobStore supports
	// it and the operator didn't disable the prune by zeroing the
	// max-age.
	if opt.AbandonedUploadMaxAge > 0 {
		if pruner, ok := s.Blobs().(blobstore.AbandonedUploadPruner); ok {
			maxAge := time.Duration(opt.AbandonedUploadMaxAge) * time.Second
			n, bytes, err := pruner.PruneAbandonedUploads(maxAge, opt.DryRun)
			if err != nil {
				stats.Errors = append(stats.Errors, fmt.Sprintf("abandoned uploads: %v", err))
			}
			stats.AbandonedUploadsRemoved = n
			stats.AbandonedUploadsBytes = bytes
		}
	}

	return stats, nil
}

// liveSet holds the digests reachable from any tag or referrers
// index. Two slots — blobs and manifests — because the sweep
// phase treats them differently (manifest delete also removes
// the .media sidecar).
type liveSet struct {
	mu        sync.Mutex
	blobs     map[string]struct{}
	manifests map[string]map[string]struct{} // {repoName: {digest: {}}}
}

func newLiveSet() *liveSet {
	return &liveSet{
		blobs:     make(map[string]struct{}),
		manifests: make(map[string]map[string]struct{}),
	}
}

func (ls *liveSet) addBlob(digest string)      { ls.blobs[digest] = struct{}{} }
func (ls *liveSet) hasBlob(digest string) bool { _, ok := ls.blobs[digest]; return ok }
func (ls *liveSet) addManifest(name, digest string) {
	if ls.manifests[name] == nil {
		ls.manifests[name] = make(map[string]struct{})
	}
	ls.manifests[name][digest] = struct{}{}
}
func (ls *liveSet) hasManifest(name, digest string) bool {
	m, ok := ls.manifests[name]
	if !ok {
		return false
	}
	_, ok = m[digest]
	return ok
}

// markRepo walks one repository: every tag points at a manifest;
// each manifest references blobs (config + layers) and may carry a
// subject pointing at another manifest. We mark every digest we
// encounter as live.
func markRepo(s *Store, name string, live *liveSet, stats *GCStats) error {
	tags, err := s.ListTags(name)
	if err != nil {
		return err
	}
	for _, tag := range tags {
		dgst, err := s.ReadTag(name, tag)
		if err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("read tag %s/%s: %v", name, tag, err))
			continue
		}
		if err := markManifestRecursive(s, name, dgst.String(), live); err != nil {
			stats.Errors = append(stats.Errors, fmt.Sprintf("mark manifest %s@%s: %v", name, dgst, err))
		}
	}

	// Also mark every manifest referenced from a referrers index
	// (subject-target relationships). The referrers themselves are
	// the manifests indexed; their *subject* manifest may not have
	// any tag pointing at it but we still need to keep it alive
	// while its referrers exist.
	refDir := path.Join(s.repoDir(name), "_referrers")
	if entries, err := s.fs.ReadDir(refDir); err == nil {
		for _, e := range entries {
			if e.IsDir || !strings.HasSuffix(e.Name, ".json") {
				continue
			}
			subjectDigest := strings.TrimSuffix(e.Name, ".json")
			// The subject digest itself is a manifest we must
			// preserve. We don't recursively mark the subject's
			// blobs here — a subject manifest with no tag and only
			// referrers is unusual but legal (cosign-only state).
			live.addManifest(name, subjectDigest)
			live.addBlob(subjectDigest)

			// Also walk the referrers index to keep the referrer
			// manifests themselves alive (they reference the
			// subject; their own blobs were already marked when
			// they were inserted into the index).
			rc, _, err := s.fs.Open(path.Join(refDir, e.Name))
			if err != nil {
				continue
			}
			var idx ociImageIndex
			body, _ := readAll(rc)
			rc.Close()
			if err := json.Unmarshal(body, &idx); err != nil {
				continue
			}
			for _, m := range idx.Manifests {
				live.addManifest(name, m.Digest)
				if err := markManifestRecursive(s, name, m.Digest, live); err != nil {
					stats.Errors = append(stats.Errors, fmt.Sprintf("mark referrer %s@%s: %v", name, m.Digest, err))
				}
			}
		}
	}
	return nil
}

// markManifestRecursive reads a manifest and marks every blob it
// references, plus the manifest body itself (manifests are stored
// as content-addressable JSON files; their digest is also a
// "blob" from the sweep's perspective).
//
// Multi-arch image indexes are recognised by their media type:
// we descend into the referenced sub-manifests so a multi-arch
// image keeps its per-arch manifests alive.
func markManifestRecursive(s *Store, name, manifestDigest string, live *liveSet) error {
	if live.hasManifest(name, manifestDigest) {
		return nil
	}
	dgst, err := blobstore.ParseDigest(manifestDigest)
	if err != nil {
		return err
	}
	live.addManifest(name, manifestDigest)
	live.addBlob(manifestDigest)

	rec, err := s.ReadManifest(name, dgst)
	if err != nil {
		return err
	}

	// Parse the manifest body. We accept both schema-v2 manifests
	// (config + layers) and image indexes (manifests).
	var parsed map[string]any
	if err := json.Unmarshal(rec.Body, &parsed); err != nil {
		return nil
	}
	// Single-manifest case: mark config + each layer digest.
	if cfg, ok := parsed["config"].(map[string]any); ok {
		if d, ok := cfg["digest"].(string); ok && d != "" {
			live.addBlob(d)
		}
	}
	if layers, ok := parsed["layers"].([]any); ok {
		for _, layer := range layers {
			if l, ok := layer.(map[string]any); ok {
				if d, ok := l["digest"].(string); ok && d != "" {
					live.addBlob(d)
				}
			}
		}
	}
	// Multi-arch / image index case: each entry in "manifests"
	// references another manifest by digest. Recurse.
	if manifests, ok := parsed["manifests"].([]any); ok {
		for _, m := range manifests {
			if mm, ok := m.(map[string]any); ok {
				if d, ok := mm["digest"].(string); ok && d != "" {
					if err := markManifestRecursive(s, name, d, live); err != nil {
						// Non-fatal: continue marking siblings.
						continue
					}
				}
			}
		}
	}
	// Subject (OCI v1.1): keep the referenced manifest alive too.
	if subj, ok := parsed["subject"].(map[string]any); ok {
		if d, ok := subj["digest"].(string); ok && d != "" {
			if err := markManifestRecursive(s, name, d, live); err != nil {
				return nil
			}
		}
	}
	return nil
}

// sweepUnreferencedManifests walks repositories/{name}/manifests/
// and deletes manifest files whose digest is not in the live set.
// The accompanying .media sidecar is also removed.
//
// Honours opt.DryRun: when set, the manifest count is updated but
// no Delete is issued.
func sweepUnreferencedManifests(s *Store, wfs rawfs.WritableRawFS, name string, live *liveSet, opt GCOptions, now int64) (int, []string) {
	dir := path.Join(s.repoDir(name), "manifests")
	entries, err := s.fs.ReadDir(dir)
	if err != nil {
		return 0, nil // empty / missing — nothing to sweep
	}
	count := 0
	var errs []string
	for _, e := range entries {
		if e.IsDir || strings.HasSuffix(e.Name, ".media") {
			continue
		}
		if !strings.HasSuffix(e.Name, ".json") {
			continue
		}
		digestStr := strings.TrimSuffix(e.Name, ".json")
		if live.hasManifest(name, digestStr) {
			continue
		}
		// Grace period.
		if opt.MinAge > 0 {
			fi, err := s.fs.Stat(path.Join(dir, e.Name))
			if err == nil && !fi.ModTime.IsZero() {
				ageSec := now - fi.ModTime.Unix()
				if ageSec < opt.MinAge {
					continue
				}
			}
		}
		if opt.DryRun {
			count++
			continue
		}
		if err := wfs.Delete(path.Join(dir, e.Name)); err != nil {
			errs = append(errs, fmt.Sprintf("delete manifest %s/%s: %v", name, digestStr, err))
			continue
		}
		_ = wfs.Delete(path.Join(dir, e.Name+".media"))
		count++
	}
	return count, errs
}

// GarbageCollect runs a GC pass against this Local registry.
// Exported so the admin API can trigger a sweep on demand. Emits
// a registry.gc_completed event on success so operators can wire a
// notification webhook to the cleanup run.
//
// Dry-run passes do NOT emit the event — they're estimation
// queries, not destructive actions.
func (l *Local) GarbageCollect(ctx context.Context, opt GCOptions) (*GCStats, error) {
	stats, err := runGC(ctx, l.store, opt)
	if err == nil && stats != nil && !opt.DryRun {
		events.EmitSafe(l.emitter, hook.Event{
			Type:     hook.EventRegistryGCCompleted,
			Mount:    l.namespace,
			Path:     l.name,
			Protocol: "registry-docker",
			Size:     stats.SweptBytes + stats.AbandonedUploadsBytes,
		})
	}
	return stats, err
}

// markRepoScoped builds a single-repo live set for the
// delete-triggered cheap cascade in docker/local.go. It walks every
// tag in `name`, follows each manifest's references (config, layers,
// sub-manifests for multi-arch, subject for OCI v1.1), and returns
// the resulting set of manifest+blob digests considered live within
// the scope of `name` only.
//
// The excludeManifest digest is treated as "about to be deleted":
// the walker skips it entirely (does not mark it nor any of its
// transitive references). This is what makes the cascade safe — we
// can ask "is sub-manifest X still reachable from any OTHER tag in
// this repo?" by walking with X's parent excluded.
//
// We also include manifests referenced from referrers indexes
// (cosign signatures, SBOMs) so attestations keep their subject
// alive across cascade. The excludeManifest entry is removed from
// any referrer index walk as well.
//
// Errors are silently absorbed: a manifest that fails to parse just
// doesn't contribute references to the live set. This matches
// markRepo's tolerant behaviour and avoids blocking a delete on
// transient read errors.
func markRepoScoped(s *Store, name, excludeManifest string) *liveSet {
	live := newLiveSet()
	// Walk tags.
	tags, err := s.ListTags(name)
	if err == nil {
		for _, tag := range tags {
			dgst, err := s.ReadTag(name, tag)
			if err != nil {
				continue
			}
			if dgst.String() == excludeManifest {
				// This tag points at the manifest being deleted;
				// don't follow it. (The tag pointer itself will
				// be invalidated by the surrounding delete or by
				// the tag-vs-digest semantics of the request.)
				continue
			}
			_ = markManifestRecursive(s, name, dgst.String(), live)
		}
	}
	// Walk referrers indexes too, mirroring markRepo's logic but
	// skipping the excluded subject. Referrers keep otherwise
	// orphan manifests alive (cosign-only state).
	refDir := path.Join(s.repoDir(name), "_referrers")
	if entries, err := s.fs.ReadDir(refDir); err == nil {
		for _, e := range entries {
			if e.IsDir || !strings.HasSuffix(e.Name, ".json") {
				continue
			}
			subjectDigest := strings.TrimSuffix(e.Name, ".json")
			if subjectDigest == excludeManifest {
				continue
			}
			live.addManifest(name, subjectDigest)
			live.addBlob(subjectDigest)
			rc, _, err := s.fs.Open(path.Join(refDir, e.Name))
			if err != nil {
				continue
			}
			var idx ociImageIndex
			body, _ := readAll(rc)
			rc.Close()
			if err := json.Unmarshal(body, &idx); err != nil {
				continue
			}
			for _, m := range idx.Manifests {
				if m.Digest == excludeManifest {
					continue
				}
				live.addManifest(name, m.Digest)
				_ = markManifestRecursive(s, name, m.Digest, live)
			}
		}
	}
	return live
}

// nowSeconds returns the current Unix time. Wrapped so tests can
// override the clock (they don't today; the function is a placeholder
// for a future test-time override).
var nowSeconds = func() int64 {
	return time.Now().Unix()
}

// readAll consumes an io.Reader entirely.
func readAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}
