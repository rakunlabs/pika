package service

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/pika/internal/external"
)

// Bulk export of an external resource's whole key space into a zip
// archive. This lives behind CapSettingsManage (see the route table in
// internal/server/api/api.go) rather than CapExternalRead: a single
// read exposes one secret, but an export hands the caller every secret
// the backend holds in one file. That is an operator-grade action, so
// it is gated with the operator-grade capability and surfaced only in
// Settings > External Resources.
//
// Only backends where a full walk is both meaningful and cheap enough
// are allowed today — Consul KV and Vault KV. The other backends
// either have no list semantics (HTTP), charge per API call (AWS/GCP/
// Azure), or would need namespace-scoping decisions we don't want to
// bake in yet (Kubernetes).

const (
	// exportMaxEntriesDefault caps how many leaves a single export
	// walks. A misconfigured prefix on a 500k-key Consul cluster
	// should degrade into a truncated archive, not an unbounded
	// request that pins a connection for an hour.
	exportMaxEntriesDefault = 20000

	// exportReadConcurrency is how many Read() calls are in flight at
	// once. Reads dominate the wall-clock time (one HTTP round trip
	// per key), but the zip writer is single-threaded, so we read in
	// batches of this size and write the batch in list order. Keeps
	// peak memory to `exportReadConcurrency` entries.
	exportReadConcurrency = 8
)

// exportableKinds is the allowlist of Provider kinds that support bulk
// export. Extending it is a one-line change once a backend's list/read
// cost profile has been reviewed.
var exportableKinds = map[string]bool{
	"consul": true,
	"vault":  true,
}

// ExternalExportOptions tunes a bulk export.
type ExternalExportOptions struct {
	// Prefix restricts the walk to a subtree. Empty means "whole
	// resource".
	Prefix string
	// MaxEntries caps the number of exported leaves. Zero uses
	// exportMaxEntriesDefault.
	MaxEntries int
}

// ExternalExportStats is the outcome of a bulk export. It is returned
// after the archive has been fully streamed, so callers can log it but
// cannot put it in the response body.
type ExternalExportStats struct {
	// Entries is the number of keys successfully written.
	Entries int
	// Failed is the number of keys/prefixes that errored during the
	// walk. Those are listed inside the archive as _errors.txt.
	Failed int
	// Truncated reports that MaxEntries was hit and the archive does
	// not represent the full key space.
	Truncated bool
}

// CheckExternalExportable validates that the named resource exists and
// its backend supports bulk export, without touching the network.
//
// The API handler calls this *before* it writes any response header:
// once the zip stream starts, the status code is already committed and
// a "backend not supported" error could only be reported as a
// truncated download.
func (s *Service) CheckExternalExportable(ctx context.Context, resourceName string) error {
	provider, err := s.externalProvider(ctx, resourceName)
	if err != nil {
		return err
	}
	return checkExportable(provider)
}

func checkExportable(provider external.Provider) error {
	kind := provider.Kind()
	if !exportableKinds[kind] {
		return fmt.Errorf(
			"bulk export is not supported for %q resources (supported: %s): %w",
			kind, strings.Join(sortedExportableKinds(), ", "), ErrBadRequest,
		)
	}
	caps := provider.Capabilities()
	if !caps.CanList || !caps.CanRead {
		return fmt.Errorf("resource does not support list+read: %w", ErrBadRequest)
	}
	return nil
}

func sortedExportableKinds() []string {
	out := make([]string, 0, len(exportableKinds))
	for k := range exportableKinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ExportExternal walks the named resource and streams a zip archive of
// every value it can read into w.
//
// Archive layout mirrors the backend key space 1:1 — the key path is
// the file path inside the zip:
//
//	consul: myapp/db/password        -> myapp/db/password        (raw value bytes)
//	vault:  myapp/db                 -> myapp/db.json            (secret map as JSON)
//
// Consul values are written verbatim when they are plain strings, so a
// re-import (or a diff against a previous export) sees exactly what
// the KV store holds. Values that Consul stored as JSON come back
// through the provider already parsed; those are re-serialised
// indented, which changes formatting but not content.
//
// Failure handling is deliberately partial-tolerant: one unreadable
// key (a Vault path the token can't reach, a prefix ACL denies) must
// not void an otherwise complete export. Failed paths are collected
// and written to _errors.txt at the archive root, and counted in the
// returned stats.
//
// Errors returned from this function mean "nothing usable was
// produced" (bad resource, unsupported backend, root listing failed,
// or the writer broke mid-stream).
func (s *Service) ExportExternal(
	ctx context.Context,
	resourceName string,
	w io.Writer,
	opts ExternalExportOptions,
) (ExternalExportStats, error) {
	provider, err := s.externalProvider(ctx, resourceName)
	if err != nil {
		return ExternalExportStats{}, err
	}
	if err := checkExportable(provider); err != nil {
		return ExternalExportStats{}, err
	}
	return writeExternalExport(ctx, provider, w, opts)
}

// writeExternalExport is the export engine, split from ExportExternal
// so it can be exercised against a stub Provider without standing up a
// Service and its settings store.
func writeExternalExport(
	ctx context.Context,
	provider external.Provider,
	w io.Writer,
	opts ExternalExportOptions,
) (ExternalExportStats, error) {
	var stats ExternalExportStats

	maxEntries := opts.MaxEntries
	if maxEntries <= 0 {
		maxEntries = exportMaxEntriesDefault
	}

	leaves, walkErrs, truncated, err := walkExternalLeaves(ctx, provider, opts.Prefix, maxEntries)
	if err != nil {
		return stats, err
	}
	stats.Truncated = truncated

	kind := provider.Kind()
	naming := newExportNaming(leaves)

	zw := zip.NewWriter(w)
	now := time.Now()

	// Batched concurrent reads: fan out `exportReadConcurrency` reads,
	// then write the batch to the zip in list order. Ordered writing
	// keeps the archive deterministic for the same key space, which
	// makes diffing two exports possible.
	for start := 0; start < len(leaves); start += exportReadConcurrency {
		end := min(start+exportReadConcurrency, len(leaves))
		batch := leaves[start:end]

		type readResult struct {
			entry *external.Entry
			err   error
		}
		results := make([]readResult, len(batch))

		var wg sync.WaitGroup
		for i, p := range batch {
			wg.Add(1)
			go func(i int, p string) {
				defer wg.Done()
				entry, err := provider.Read(ctx, p)
				results[i] = readResult{entry: entry, err: err}
			}(i, p)
		}
		wg.Wait()

		if err := ctx.Err(); err != nil {
			return stats, err
		}

		for i, p := range batch {
			res := results[i]
			if res.err != nil || res.entry == nil {
				msg := "empty response"
				if res.err != nil {
					msg = res.err.Error()
				}
				walkErrs = append(walkErrs, fmt.Sprintf("read %s: %s", p, msg))
				continue
			}

			name, body := exportEntryFile(kind, naming.fileName(p), res.entry)
			hdr := &zip.FileHeader{
				Name:     name,
				Method:   zip.Deflate,
				Modified: now,
			}
			f, err := zw.CreateHeader(hdr)
			if err != nil {
				return stats, fmt.Errorf("creating zip entry %q: %w", name, err)
			}
			if _, err := f.Write(body); err != nil {
				return stats, fmt.Errorf("writing zip entry %q: %w", name, err)
			}
			stats.Entries++
		}
	}

	stats.Failed = len(walkErrs)
	if err := writeExportErrors(zw, walkErrs, truncated, maxEntries, now); err != nil {
		return stats, err
	}

	if err := zw.Close(); err != nil {
		return stats, fmt.Errorf("finalizing zip archive: %w", err)
	}
	return stats, nil
}

// writeExportErrors appends _errors.txt when anything was skipped.
// Without it a partial export is indistinguishable from a complete
// one, which is the worst possible outcome for a backup artifact.
func writeExportErrors(zw *zip.Writer, errs []string, truncated bool, maxEntries int, now time.Time) error {
	if len(errs) == 0 && !truncated {
		return nil
	}

	var b strings.Builder
	if truncated {
		fmt.Fprintf(&b, "TRUNCATED: entry limit of %d reached; this archive is incomplete.\n\n", maxEntries)
	}
	if len(errs) > 0 {
		fmt.Fprintf(&b, "%d path(s) could not be exported:\n\n", len(errs))
		for _, e := range errs {
			b.WriteString(e)
			b.WriteByte('\n')
		}
	}

	f, err := zw.CreateHeader(&zip.FileHeader{
		Name:     "_errors.txt",
		Method:   zip.Deflate,
		Modified: now,
	})
	if err != nil {
		return fmt.Errorf("creating _errors.txt: %w", err)
	}
	if _, err := io.WriteString(f, b.String()); err != nil {
		return fmt.Errorf("writing _errors.txt: %w", err)
	}
	return nil
}

// walkExternalLeaves breadth-first walks the resource under prefix and
// returns every leaf path, the list errors encountered on sub-prefixes,
// and whether the walk was truncated by the entry limit.
//
// Semantics match SearchExternal's traversal: a folder is a listing
// entry with a trailing "/", a failure on the root prefix is fatal, and
// a failure on any sub-prefix is recorded and skipped (permission-
// restricted subtrees are common and must not void the export).
func walkExternalLeaves(
	ctx context.Context,
	provider external.Provider,
	prefix string,
	maxEntries int,
) (leaves []string, errs []string, truncated bool, err error) {
	root := strings.TrimPrefix(prefix, "/")

	frontier := []string{root}
	// Guards against a backend that reports a child equal to its own
	// prefix; without it the walk would spin forever.
	seen := map[string]bool{root: true}

	for len(frontier) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, nil, false, err
		}

		cur := frontier[0]
		frontier = frontier[1:]

		children, listErr := provider.List(ctx, cur)
		if listErr != nil {
			if cur == root {
				return nil, nil, false, fmt.Errorf("listing %q: %w", cur, listErr)
			}
			errs = append(errs, fmt.Sprintf("list %s: %s", cur, listErr.Error()))
			continue
		}

		for _, child := range children {
			full := joinPath(cur, child)
			if strings.HasSuffix(child, "/") {
				if seen[full] {
					continue
				}
				seen[full] = true
				frontier = append(frontier, full)
				continue
			}
			if len(leaves) >= maxEntries {
				return leaves, errs, true, nil
			}
			leaves = append(leaves, full)
		}
	}

	sort.Strings(leaves)
	return leaves, errs, false, nil
}

// exportNaming resolves key paths to collision-free archive paths.
//
// Consul (and Vault KV v1) allow a key to be both a value and a folder
// — "app" and "app/db" can coexist. A zip that contains both a file
// "app" and a directory "app/" cannot be extracted on any filesystem,
// so keys that double as a directory get a ".value" suffix.
type exportNaming struct {
	dirs  map[string]bool
	taken map[string]bool
}

func newExportNaming(leaves []string) *exportNaming {
	n := &exportNaming{
		dirs:  make(map[string]bool),
		taken: make(map[string]bool),
	}
	for _, leaf := range leaves {
		clean := sanitizeExportPath(leaf)
		for {
			dir := path.Dir(clean)
			if dir == "." || dir == "/" || dir == "" {
				break
			}
			n.dirs[dir] = true
			clean = dir
		}
	}
	return n
}

// fileName returns the archive-relative path (without any per-backend
// extension) for a key path.
func (n *exportNaming) fileName(keyPath string) string {
	name := sanitizeExportPath(keyPath)
	if n.dirs[name] {
		name += ".value"
	}
	for i := 2; n.taken[name]; i++ {
		name = fmt.Sprintf("%s.%d", name, i)
	}
	n.taken[name] = true
	return name
}

// sanitizeExportPath makes a backend key safe to use as a zip entry
// name: no absolute paths, no "..", no backslashes (which Windows
// extractors treat as separators), no empty segments.
func sanitizeExportPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "_")
	p = path.Clean("/" + strings.TrimPrefix(p, "/"))
	p = strings.TrimPrefix(p, "/")
	if p == "" || p == "." {
		p = "_root"
	}
	return p
}

// exportEntryFile decides the final archive entry name and body for a
// single read entry.
//
//   - Vault entries are secret maps with arbitrary fields, so they are
//     always written as indented JSON under "<key>.json".
//   - Consul entries hold a single opaque value. When the provider
//     handed back the plain-string shape ({"value": "..."} — i.e. the
//     stored bytes were not JSON) we write those bytes verbatim under
//     the key path so the archive is a faithful mirror of the KV tree.
//     Values Consul stored as JSON come back parsed and are re-emitted
//     as indented JSON under the same key path.
func exportEntryFile(kind, baseName string, entry *external.Entry) (string, []byte) {
	if kind == "consul" {
		if v, ok := entry.Data["value"]; ok && len(entry.Data) == 1 {
			if s, ok := v.(string); ok {
				return baseName, []byte(s)
			}
		}
		return baseName, marshalExportJSON(entry)
	}
	return baseName + ".json", marshalExportJSON(entry)
}

// marshalExportJSON renders an entry as indented JSON, falling back to
// the provider's raw bytes if the structured map can't be marshalled
// (shouldn't happen — every provider builds Data from decoded JSON —
// but an export must never lose a value to a formatting error).
func marshalExportJSON(entry *external.Entry) []byte {
	b, err := json.MarshalIndent(entry.Data, "", "  ")
	if err == nil {
		return append(b, '\n')
	}
	if len(entry.Raw) > 0 {
		return entry.Raw
	}
	return []byte("{}\n")
}
