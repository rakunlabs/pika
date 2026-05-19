package npm

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"path"
	"strings"
)

// readmeExtractLimit caps how many bytes we'll pull out of one
// README file. Stops a maliciously oversized "README" entry from
// exhausting memory. 1 MiB is comfortable: GitHub's render cap is
// ~512KB and the biggest real-world npm README we surveyed (Vue
// core, dating back years) sits around 60KB.
const readmeExtractLimit = 1 << 20

// readmeCandidateNames is the lowercase-normalized set of filenames
// the extractor accepts as the package README. Order in the slice
// is the priority order: README.md wins over README, etc. The
// tarball walk records the first match seen in the order; if none
// of the higher-priority names appear, the lower-priority one is
// returned.
var readmeCandidateNames = []string{
	"readme.md",
	"readme.markdown",
	"readme.mkd",
	"readme.txt",
	"readme",
}

// extractReadmeFromTarball walks the gzip+tar stream produced by
// `npm pack` and returns the contents of the first README-like
// file it finds. The tarball convention is to wrap every entry in
// a "package/" directory; the function strips that prefix and
// matches against the candidate list. Returns ("", nil) if none of
// the candidates are present.
//
// Errors are returned only for genuinely malformed archives; a
// well-formed tarball that simply has no README is not an error.
func extractReadmeFromTarball(r io.Reader) (string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		// A non-gzip tarball is unusual for npm but not impossible
		// (some tooling emits raw tars). Fall through to tar
		// reader directly on the original reader is not possible
		// because we've consumed bytes — surface the error.
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	// Track the best match found so far (lower index = higher
	// priority). When we find priority 0 ("readme.md") we can
	// stop early.
	bestPriority := len(readmeCandidateNames)
	bestContent := ""
	for {
		hdr, err := tr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := hdr.Name
		// Strip the leading "package/" wrapper. Don't recurse into
		// subdirectories: npm's README lives at the package root.
		if i := strings.Index(name, "/"); i >= 0 {
			rest := name[i+1:]
			if strings.Contains(rest, "/") {
				continue
			}
			name = rest
		}
		lname := strings.ToLower(path.Base(name))
		priority := -1
		for i, cand := range readmeCandidateNames {
			if lname == cand {
				priority = i
				break
			}
		}
		if priority < 0 || priority >= bestPriority {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(tr, readmeExtractLimit))
		if err != nil {
			return "", err
		}
		bestContent = string(body)
		bestPriority = priority
		if priority == 0 {
			break
		}
	}
	return bestContent, nil
}
