// Package helm implements the classic Helm chart repository
// protocol on top of pika's raw filesystem layer.
//
// Protocol surface (read):
//
//	GET /index.yaml                       → repo manifest
//	GET /{chart}-{version}.tgz            → chart tarball
//
// Protocol surface (write — ChartMuseum compatibility):
//
//	POST /api/charts                      → multipart upload of a
//	                                        chart tarball
//	DELETE /api/charts/{chart}/{version}  → remove one chart version
//	PUT /api/prov/{chart}-{version}.tgz   → provenance file (signed)
//	GET /api/charts                       → JSON catalogue (Pika
//	                                        adds this for the
//	                                        admin UI; ChartMuseum
//	                                        also exposes it)
//
// Pure raw-PUT publishes are also accepted at:
//
//	PUT /{chart}-{version}.tgz            → identical to ChartMuseum
//	                                        upload, single-file form
//
// Three Registry kinds are supported:
//
//   - Local — manual upload + ChartMuseum publish.
//   - Remote — pull-through proxy of an upstream Helm repo.
//   - Virtual — ordered aggregation of sibling Local + Remote
//     repos exposed under one URL.
//
// On-disk layout (under the configured BasePath inside the raw
// mount):
//
//	{base}/charts/{chart}/{chart}-{version}.tgz   chart tarballs
//	{base}/charts/{chart}/Chart.yaml.{version}    parsed Chart.yaml cache
//	{base}/charts/{chart}/readme.md               extracted README
//	{base}/index.yaml                             cached merged index
//
// The per-chart cached Chart.yaml lets the package detail endpoint
// return appVersion / description / keywords without re-opening
// the tarball on every request; it's an O(1) read off disk.
package helm

import "errors"

// ErrChartNotFound is the package sentinel for "no such chart /
// version". Callers wrap with the registry layer's ErrPackageNotFound
// (or service.ErrNotFound at the HTTP boundary).
var ErrChartNotFound = errors.New("helm: chart not found")

// ErrInvalidName is the sentinel for syntactically invalid chart
// names or versions. The validator is conservative — see name.go.
var ErrInvalidName = errors.New("helm: invalid chart name or version")

// ErrInvalidChart is returned when an uploaded tarball does not
// look like a Helm chart (missing Chart.yaml, malformed tar, …).
var ErrInvalidChart = errors.New("helm: invalid chart archive")
