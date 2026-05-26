# Package registries & CDN

Pika can act as an artifact registry for package-manager clients and as a package CDN for browser/runtime asset delivery. The two surfaces share the same repository configuration but serve different jobs:

| Surface | URL shape | Used by |
| ------- | --------- | ------- |
| Registry | `/registries/{namespace}/{repo}/...` | `npm install`, `npm publish`, package-manager protocol clients. |
| Package CDN | Proxy listener, for example `/npm/{package[@version]}/{file...}` | Browsers, import maps, bundlers, static websites, and dedicated CDN hostnames. |

The CDN is not a replacement for the NPM registry protocol. `npm install` still talks to `/registries/...`; the CDN extracts individual files from the package tarball and returns cache-friendly HTTP responses.

## Supported registry types

All registry types use the same admin model: **namespace → repository → kind**. The repository `type` selects the package protocol, and the repository `kind` selects where artifacts come from.

| Type | Used for | Typical clients |
| ---- | -------- | --------------- |
| `go` | Go module proxy/cache | `go env GOPROXY=...`, `go get` |
| `npm` | JavaScript packages | `npm`, `pnpm`, `yarn`, Proxy CDN reads |
| `docker` | Docker/OCI images | `docker`, `podman`, `oras` |
| `helm` | Classic Helm chart repositories | `helm repo add`, `helm pull` |
| `maven` | JVM artifacts in Maven layout | Maven, Gradle, sbt |
| `pypi` | Python packages | `pip`, `twine`, Poetry |
| `cargo` | Rust crates with sparse index | `cargo` |

| Kind | Meaning | Notes |
| ---- | ------- | ----- |
| `local` | Store artifacts inside a Pika raw mount. | Enable `allow_push` when clients should publish/push. |
| `remote` | Pull-through cache for an upstream registry. | Requires `url`, cache `mount`, and cache `base_path`. |
| `virtual` | One endpoint that searches multiple sibling repos in order. | Read-only; publish to a concrete local repo. |

Use the **Registries** page in the UI for day-to-day management. Use **Settings → Features → Artifact registry** to hide or re-enable the whole registry surface without deleting saved repositories or stored artifacts.

## NPM topology example

Registry kinds are protocol-neutral, but NPM is the clearest place to see how they work together. A common topology is:

| Repo | Kind | Role |
| ---- | ---- | ---- |
| `npm-local` | `local` | Private packages published by your team. |
| `npmjs` | `remote` | Cached mirror of `https://registry.npmjs.org`. |
| `npm` | `virtual` | One read endpoint that checks private packages first, then npmjs. |

Publish to the local repo. Install and CDN-fetch from the virtual repo.

## Quick setup

### 1. Create a raw mount

Create a writable raw mount under **Settings → Raw Mounts**. For examples below the mount prefix is `packages`.

Any writable backend works: local disk, S3-compatible storage, SFTP, WebDAV, or Vercel Blob. The registry stores package metadata and tarballs under the `base_path` you choose per repo.

### 2. Add NPM repositories

Use **Registries → New repository** in the UI, or replace the registry tree with `PUT /api/v1/registries`. The API call requires a session or token with `registry.admin`.

::: warning
`PUT /api/v1/registries` replaces the whole registry tree. Fetch the current tree first if you are updating an existing deployment.
:::

```json
{
  "namespaces": [
    {
      "name": "default",
      "repositories": [
        {
          "name": "npm-local",
          "type": "npm",
          "kind": "local",
          "mount": "packages",
          "base_path": "npm/local",
          "allow_push": true,
          "cors_origins": ["https://app.example.com"]
        },
        {
          "name": "npmjs",
          "type": "npm",
          "kind": "remote",
          "url": "https://registry.npmjs.org",
          "mount": "packages",
          "base_path": "npm/cache",
          "mutable_ttl": "5m"
        },
        {
          "name": "npm",
          "type": "npm",
          "kind": "virtual",
          "members": ["npm-local", "npmjs"],
          "default_local": "npm-local",
          "cors_origins": ["https://app.example.com"]
        }
      ]
    }
  ]
}
```

The same tree gives you these data-plane endpoints:

```txt
NPM publish target:  http://localhost:8080/registries/default/npm-local/
NPM install target:  http://localhost:8080/registries/default/npm/
CDN proxy target:    http://localhost:9090/npm/lodash@4.17.21/lodash.js
```

### 3. Mint a token

For package-manager clients, mint an API token with registry scopes. A typical developer token needs read access to the virtual repo and write access to the local repo:

```json
[
  { "path": "registry/default/npm/**", "operations": ["read"] },
  { "path": "registry/default/npm-local/**", "operations": ["read", "write"] }
]
```

If the same token also edits registry settings through the admin API, add the `registry.admin` capability. Normal install/publish/CDN traffic uses scopes, not admin capabilities.

### 4. Configure npm

Set `PIKA_TOKEN` in the shell, then use the virtual repo for installs. Include auth for the local repo too if the same checkout publishes packages:

```ini
# .npmrc
registry=http://localhost:8080/registries/default/npm/
//localhost:8080/registries/default/npm/:_authToken=${PIKA_TOKEN}
//localhost:8080/registries/default/npm-local/:_authToken=${PIKA_TOKEN}
always-auth=true
```

Publish private packages to the local repo:

```sh
export PIKA_TOKEN=pika_...

npm publish --registry=http://localhost:8080/registries/default/npm-local/
```

For scoped private packages, keep the install registry virtual and publish the scope to the local repo:

```ini
registry=http://localhost:8080/registries/default/npm/
@acme:registry=http://localhost:8080/registries/default/npm-local/
//localhost:8080/registries/default/npm/:_authToken=${PIKA_TOKEN}
//localhost:8080/registries/default/npm-local/:_authToken=${PIKA_TOKEN}
always-auth=true
```

### 5. Publish the package CDN through Pika Proxy

Use **Proxy → New CDN proxy** to create a listener that terminates in a **Package CDN resource** handler. The generated graph does not depend on the built-in `/cdn/npm/...` route; it serves jsDelivr-style package paths directly from the selected NPM repository.

The default generated handler config is:

```json
{
  "namespace": "default",
  "repository": "npm",
  "strip_prefix": "/npm"
}
```

Enable the proxy server, then fetch assets from the proxy port or from the hostname you route to that listener:

```sh
curl http://localhost:9090/npm/lodash@4.17.21/lodash.js

curl http://localhost:9090/npm/@acme/button@1.2.0/dist/index.js
```

If the version is omitted, Pika resolves the package's `latest` dist-tag:

```txt
/npm/lodash/lodash.js
/npm/@acme/button/dist/index.js
```

Explicit versions are served with long-lived immutable cache headers. `latest` and other dist-tag requests use revalidating cache headers because the tag can move.

Use a custom proxy graph when you want a different public path or a dedicated CDN hostname such as `https://cdn.example.com/assets/...`.

Create a proxy server under **Proxy**, route a path such as `/assets/*`, and terminate it with a **Package CDN resource** handler:

```json
{
  "namespace": "default",
  "repository": "npm",
  "strip_prefix": "/assets"
}
```

Requests now map like this:

```txt
https://cdn.example.com/assets/lodash@4.17.21/lodash.js
  → default/npm package CDN
  → package lodash, version 4.17.21, file lodash.js

https://cdn.example.com/assets/@acme/button@1.2.0/dist/index.js
  → default/npm package CDN
  → package @acme/button, version 1.2.0, file dist/index.js
```

Proxy CDN resources are public by default because they are usually used by browsers. To protect a proxy-published CDN path, either set `require_token` on the handler or place an `auth-bearer` middleware before it:

```json
{
  "namespace": "default",
  "repository": "npm",
  "strip_prefix": "/assets",
  "require_token": true
}
```

When `require_token` is enabled, the token must have read scope for the selected repo, for example `registry/default/npm/**`.

The built-in `/cdn/npm/{namespace}/{repo}/...` endpoint remains available for authenticated direct reads, but public CDN exposure should go through Proxy.

## Remote cache behavior

Remote NPM repos fetch from upstream lazily:

- First `npm install lodash` through the virtual repo warms the packument and tarball cache.
- First CDN request for a remote package can also warm the needed packument and tarball.
- `mutable_ttl` controls how long mutable metadata such as `latest` is reused before Pika checks upstream again.
- Tarballs and explicit-version CDN assets are treated as immutable once cached.

If upstream metadata changes and you need to refresh immediately, use the registry cache purge action in the UI or `POST /api/v1/registries/{type}/{namespace}/{repo}/purge`.

## Common failures

| Symptom | Likely cause | Fix |
| ------- | ------------ | --- |
| `401` from `/registries/...` | Missing token or npm did not send `_authToken`. | Check `.npmrc` host/path and `always-auth=true`. |
| `403` from install or CDN | Token scope does not match `registry/{namespace}/{repo}/...`. | Add a read scope such as `registry/default/npm/**`. |
| `405` while publishing to a virtual repo | Virtual repos are read-only. | Publish to `npm-local`, install from `npm`. |
| CDN file returns `404` but package exists | The file path is not inside the package tarball. | Run `npm pack --dry-run` locally or inspect package contents. |
| Browser blocks CDN response | CORS origin is not allowed on the repo. | Add the site origin to `cors_origins` on the repo used by CDN. |
