// Package npm implements the NPM registry protocol on top of pika's
// raw filesystem layer. Three Registry kinds are supported:
//
//   - Local — publish-and-serve, no upstream interaction.
//   - Remote — pull-through cache of an upstream registry (e.g.
//     registry.npmjs.org). Tarballs and per-version metadata are
//     persisted on first fetch; mutable endpoints (packument,
//     dist-tags) honour the per-repo MutableTTL.
//   - Virtual — first-hit-wins aggregation of sibling local +
//     remote repos in the same namespace.
//
// Routes (after the manager strips /registries/{ns}/{repo}):
//
//	GET    /{pkg}                              packument JSON
//	GET    /{pkg}/-/{file}.tgz                 tarball
//	PUT    /{pkg}                              publish
//	GET    /-/v1/search?text=                  search results
//	GET    /-/whoami                           current user/token
//	GET    /-/package/{pkg}/dist-tags          all dist-tags
//	PUT    /-/package/{pkg}/dist-tags/{tag}    set dist-tag
//	DELETE /-/package/{pkg}/dist-tags/{tag}    remove dist-tag
//
// Scoped packages
//
// NPM scoped names look like "@scope/name". The URL encoding is
// "/@scope%2Fname" or "/@scope/name" depending on the client; we
// accept both. The on-disk layout uses the raw form
// ("packages/@scope/name") so a single rawfs listing surfaces
// both scoped and unscoped packages uniformly.
//
// Storage layout (under the configured BasePath inside the raw mount):
//
//	{base}/packages/{name}/                   // unscoped
//	  ├── versions/{version}.json             // per-version metadata
//	  ├── tarballs/{file}.tgz                 // raw tarballs
//	  ├── dist-tags.json                      // {latest: "1.2.3", ...}
//	  ├── readme.md                           // latest README (cached)
//	  └── packument.json                      // cached, derived on read
//	{base}/packages/@scope/{name}/...         // scoped (same shape)
package npm

import "errors"

// Errors. Wrapped with service-layer sentinels (ErrNotFound,
// ErrBadRequest) by the HTTP layer so the response codes are
// uniform with the rest of pika.
var (
	ErrPackageNotFound = errors.New("npm: package not found")
	ErrVersionExists   = errors.New("npm: version already published")
	ErrInvalidPackage  = errors.New("npm: invalid package name")
	ErrInvalidVersion  = errors.New("npm: invalid version")
	ErrInvalidPayload  = errors.New("npm: invalid publish payload")
	ErrIntegrityFail   = errors.New("npm: integrity mismatch")
)
