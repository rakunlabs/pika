package helm

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/goccy/go-yaml"
)

// ChartMetadata is the subset of Chart.yaml fields pika surfaces.
// The full schema is at https://helm.sh/docs/topics/charts/#charts-and-versioning.
// We capture the operator-relevant bits — enough to populate the
// admin UI's detail panel and the auto-generated index.yaml — and
// pass everything else through verbatim via Raw.
type ChartMetadata struct {
	Name        string   `yaml:"name" json:"name"`
	Version     string   `yaml:"version" json:"version"`
	AppVersion  string   `yaml:"appVersion,omitempty" json:"app_version,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Icon        string   `yaml:"icon,omitempty" json:"icon,omitempty"`
	Keywords    []string `yaml:"keywords,omitempty" json:"keywords,omitempty"`
	Home        string   `yaml:"home,omitempty" json:"home,omitempty"`
	Sources     []string `yaml:"sources,omitempty" json:"sources,omitempty"`
	Type        string   `yaml:"type,omitempty" json:"type,omitempty"` // "application" | "library"
	// Maintainers is intentionally typed as []any so the YAML
	// shape (name/email/url) round-trips into JSON without
	// strictness drift.
	Maintainers []any `yaml:"maintainers,omitempty" json:"maintainers,omitempty"`
	// Dependencies is a similarly loose []any so subchart pinning
	// shapes ({name, version, repository, alias, …}) preserve.
	Dependencies []any `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	// Annotations is the free-form key/value bag Helm 3 exposes.
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

// ChartExtraction is the post-publish view of a chart tarball
// alongside the bytes-on-disk metadata pika needs to maintain its
// index. Returned by ExtractChart.
type ChartExtraction struct {
	Metadata ChartMetadata
	// README content extracted from the tarball, if present.
	Readme string
	// Digest is the sha256 of the tarball body (Helm's index.yaml
	// uses this as the per-version `digest` field).
	Digest string
	// Size is the tarball size in bytes (`urls` references in
	// index.yaml don't carry size but the admin UI wants it).
	Size int64
}

// ExtractChart parses a Helm chart tarball: gunzip + tar walk,
// pull Chart.yaml from the chart's root directory, parse its YAML,
// also pull README* from the same directory.
//
// Helm convention: the tarball wraps every entry in a single
// top-level directory named after the chart ("mychart/Chart.yaml",
// "mychart/values.yaml", "mychart/README.md", …). The extractor
// tolerates multiple top-level directories — picks the first one
// that contains Chart.yaml.
//
// Returns ErrInvalidChart for tarballs that don't carry a parseable
// Chart.yaml. The size + digest are computed alongside the walk
// so a single read of the input stream produces every artefact.
//
// The reader must support being fully consumed (no seeking
// required); typical callers pass a bytes.Reader over the in-memory
// upload body.
func ExtractChart(body []byte) (*ChartExtraction, error) {
	// Compute digest first — cheap, lets us return early when the
	// payload is obviously broken.
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])

	gz, err := gzip.NewReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("%w: gzip: %v", ErrInvalidChart, err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	var chartYAML []byte
	var readme string
	for {
		hdr, err := tr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("%w: tar: %v", ErrInvalidChart, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := baseName(hdr.Name)
		depth := strings.Count(strings.TrimSuffix(hdr.Name, "/"), "/")
		if depth != 1 {
			// We only look one level deep: the chart root.
			continue
		}
		switch strings.ToLower(base) {
		case "chart.yaml":
			chartYAML, err = io.ReadAll(io.LimitReader(tr, 256*1024))
			if err != nil {
				return nil, fmt.Errorf("%w: read chart.yaml: %v", ErrInvalidChart, err)
			}
		case "readme.md", "readme.markdown", "readme":
			b, err := io.ReadAll(io.LimitReader(tr, 1<<20))
			if err == nil && readme == "" {
				readme = string(b)
			}
		}
	}
	if len(chartYAML) == 0 {
		return nil, fmt.Errorf("%w: missing Chart.yaml", ErrInvalidChart)
	}
	var meta ChartMetadata
	if err := yaml.Unmarshal(chartYAML, &meta); err != nil {
		return nil, fmt.Errorf("%w: parse chart.yaml: %v", ErrInvalidChart, err)
	}
	if meta.Name == "" || meta.Version == "" {
		return nil, fmt.Errorf("%w: chart.yaml missing name or version", ErrInvalidChart)
	}
	return &ChartExtraction{
		Metadata: meta,
		Readme:   readme,
		Digest:   digest,
		Size:     int64(len(body)),
	}, nil
}

// baseName returns the last path segment of `p`, stripping any
// trailing slash. Helm tarballs use forward slashes regardless of
// host OS, so we don't go through path/filepath.
func baseName(p string) string {
	p = strings.TrimSuffix(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// TarballFilename returns the on-disk filename for a chart version,
// matching Helm's "{name}-{version}.tgz" convention. The output is
// safe for filesystem use: name and version have already passed
// ValidateChartName / ValidateChartVersion at the call site.
func TarballFilename(name, version string) string {
	return name + "-" + version + ".tgz"
}
