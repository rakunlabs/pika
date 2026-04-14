<img align="left" height="64" src="_ui/public/favicon-192x192.png" />

# pika

General configuration server.

> Highly on development stage, expect breaking changes. Feedback and contributions are very welcome!

## Quick Start

```sh
docker run -d --name pika -v pika:/data -p 8080:8080 ghcr.io/rakunlabs/pika:latest
```

Open `http://localhost:8080` to access the web UI.

## Features

- Multi-format configs (JSON, YAML, TOML)
- Version history with semver constraints
- Variants for environment-specific overrides
- Config inheritance (internal files, Vault, HTTP, Kubernetes, Consul, etcd, AWS, GCP, Azure)
- Token-based access control with glob scopes
- Full-text search across all configs
- Built-in encryption (ChaCha20) with key rotation
- Real-time preview of resolved configs
- Event hooks (HTTP webhooks, Kafka, Redis Pub/Sub, NATS)
- Raw file serving (local, S3, FTP, SFTP, WebDAV, Vercel Blob)
- Inline editor with syntax validation (JSON, YAML, TOML)

## Consuming Configs

Applications fetch resolved configuration from the `/data/*` endpoint using a token:

```
GET /data/<path>
```

### Authentication

Pass the token via the `Authorization` header:

```sh
curl -H "Authorization: Bearer pika_abc123..." http://localhost:8080/data/myapp/config
```

### Query Parameters

| Parameter | Description                          | Example                          |
| --------- | ------------------------------------ | -------------------------------- |
| `version` | Version selector — integer or semver | `?version=3` or `?version=0.2.0` |
| `variant` | Variant name                         | `?variant=prod`                  |
| `format`  | Convert output to a different format | `?format=json`                   |

### Format Conversion

The response is returned in the file's stored format by default. Use `?format=` to convert on the fly:

```sh
# Stored as YAML, returned as JSON
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/data/myapp/config?format=json
```

| Format | Content-Type               |
| ------ | -------------------------- |
| `json` | `application/json`         |
| `yaml` | `application/x-yaml`       |
| `toml` | `application/toml`         |
| other  | `application/octet-stream` |

### Public Port

By default, `/data/*` requires a Bearer token. If you want to serve configs without authentication (e.g., inside a private network), enable the public server via the **Settings > Public Server** page in the UI.

The public port starts a second HTTP server that only exposes `/data/*`, `/raw/*`, and `/healthz` — no admin API, no UI. You can also enable Consul KV compatibility endpoints on it.

```sh
# No token needed on the public port
curl http://localhost:9090/data/myapp/config

# With variant and format
curl "http://localhost:9090/data/myapp/config?variant=prod&format=json"
```

## Versions

Every save creates a new version. Versions can be fetched by integer number or by semver.

### Integer Versions

Each save increments the version number (1, 2, 3, ...). Fetch a specific version:

```sh
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/data/myapp/config?version=3
```

Omit `version` to get the latest.

### Semver Constraints

Versions can be tagged with a semver constraint (e.g., `>= 0.1.0`). When a consumer requests a semver version, pika resolves which version to serve based on constraint boundaries.

#### How It Works

When you save a version with a constraint like `>= 0.1.0`, you're saying "this version is intended for consumers running 0.1.0 or higher." Pika walks the version history and finds the latest version whose constraint is satisfied by the requested semver.

#### Example

Suppose a config has this version history:

| Version | Constraint | Content                       |
| ------- | ---------- | ----------------------------- |
| v1      | _(none)_   | Initial config                |
| v2      | _(none)_   | Minor tweak                   |
| v3      | `>= 0.1.0` | New field added in app 0.1.0  |
| v4      | _(none)_   | Fix typo                      |
| v5      | `>= 0.2.0` | Breaking change for app 0.2.0 |

Consumers request their app version and get the right config:

```sh
# App running v0.0.5 — gets v2 (latest before the >= 0.1.0 boundary)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/data/myapp/config?version=0.0.5

# App running v0.1.5 — gets v4 (satisfies >= 0.1.0, latest before >= 0.2.0)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/data/myapp/config?version=0.1.5

# App running v0.2.0 — gets v5 (satisfies >= 0.2.0)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/data/myapp/config?version=0.2.0
```

This lets you evolve configs alongside your application versions without breaking older deployments.

## Variants

Variants are independent copies of a config for different environments or contexts (e.g., `prod`, `staging`, `dev`). Each variant has its own content, version history, and inheritance chain.

### Creating Variants

In the web UI, open a config file and use the **Variants** section in the right panel to add a variant (e.g., `prod`).

Variants are stored as `path@variant` internally (e.g., `myapp/config@prod`).

### Consuming Variants

Use the `variant` query parameter:

```sh
# Base config
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/data/myapp/config

# Production variant
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/data/myapp/config?variant=prod

# Staging variant, converted to JSON
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8080/data/myapp/config?variant=staging&format=json"

# Variant with semver version
curl -H "Authorization: Bearer $TOKEN" "http://localhost:8080/data/myapp/config?variant=prod&version=0.3.0"
```

## Raw Filesystem Serving

Pika can serve files directly from local filesystem directories over HTTP. This is useful for serving static assets, certificates, or any files that don't need versioning or the config management features.

### Configuration

Raw mounts support multiple backend types. Mounts are configured via the **Settings > Raw Mounts** page in the UI.

Supported backend types:

- **Local**: Mount a filesystem directory (also works with FUSE mounts like `s3fs`, `rclone mount`, `sshfs`, `gcsfuse`)
- **S3**: AWS S3, MinIO, Cloudflare R2, DigitalOcean Spaces, or any S3-compatible storage
- **FTP/FTPS**: Connect to a remote FTP/FTPS server
- **SFTP**: Connect to a remote SFTP (SSH) server
- **WebDAV**: Connect to a WebDAV server
- **Vercel Blob**: Serve files from Vercel Blob storage

### API

Files are served at `/raw/{prefix}/{path}`:

```sh
# Read a file (main server — requires Bearer token)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/raw/configs/app.json

# Read a file (public server — no auth)
curl http://localhost:9090/raw/configs/app.json

# Directory listing (returns JSON array)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/raw/configs/

# Upload a file (S3 mounts only — requires write scope)
curl -X PUT -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  --data-binary @app.json \
  http://localhost:8080/raw/assets/app.json

# Delete a file (S3 mounts only — requires delete scope)
curl -X DELETE -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/raw/assets/app.json
```

Directory listings return a JSON array of entries:

```json
[
  { "name": "app.json", "is_dir": false, "size": 1234 },
  { "name": "subdir", "is_dir": true, "size": 0 }
]
```

Token scopes match against `raw/{prefix}/{path}` — for example, a token with scope `raw/**` can access all raw mounts, or `raw/configs/**` for a specific mount. Write operations require `write` permission, delete requires `delete`.

### Web UI — File Browser

When raw mounts are configured, a **Files** link appears in the navigation bar. The file browser provides:

- **Tree navigation** — mount points shown as top-level nodes with backend type badges (Local, S3, FTP), directories expand on click
- **Smart file viewing** — files open with the appropriate viewer based on their extension:
  - **Text/Code** — syntax-highlighted read-only editor for known text formats (JSON, YAML, Go, Python, Markdown, etc.)
  - **Images** — inline preview for PNG, JPG, SVG, WebP, etc.
  - **Video/Audio** — native browser player with controls for MP4, WebM, MP3, WAV, etc.
  - **PDF** — embedded PDF viewer
  - **Binary** — a placeholder with an "Open Anyway" button that shows a hex dump viewer
- **Tabs** — multiple files can be open simultaneously with right-click context menu (Close, Close Others, Close All)
- **File info panel** — shows file metadata (name, mount, path, size, content type)
- **Download button** — always available in the toolbar to download any file
- **Large file protection** — text files over 5 MB are truncated with a warning; hex viewer limited to 10 MB
- **Write operations** (S3 mounts only):
  - **Upload** — upload files via the tree (upload button on hover)
  - **Create folder** — create new directories
  - **Delete** — delete files (delete button on hover)

### Settings UI

Raw mounts can also be managed from **Settings > Raw Mounts** in the web UI. The form supports all backend types with conditional fields. Changes take effect immediately — no server restart required.

## Hooks

Pika can push event notifications when files or configs change. Hooks are configured via **Settings > Hooks** in the UI.

### Event Types

| Event            | Trigger                         |
| ---------------- | ------------------------------- |
| `file.created`   | File uploaded to a raw mount    |
| `file.updated`   | File overwritten on a raw mount |
| `file.deleted`   | File deleted from a raw mount   |
| `file.renamed`   | File renamed                    |
| `file.copied`    | File copied                     |
| `dir.created`    | Directory created               |
| `config.created` | Config created                  |
| `config.updated` | Config updated                  |
| `config.deleted` | Config deleted                  |
| `*`              | All events                      |

### Targets

Each hook can send events to one or more targets:

- **HTTP Webhook** — POST/PUT to any URL with custom headers
- **Kafka** — Produce messages to Kafka topics (supports TLS, SASL/PLAIN, SASL/SCRAM)
- **Redis Pub/Sub** — Publish to Redis channels (standalone or cluster, with TLS + mTLS)
- **NATS** — Publish to NATS subjects (token or user/password auth)

### Filters

Hooks can be filtered by:
- **Mounts** — restrict to specific raw mount prefixes
- **Path pattern** — glob pattern for matching file paths (e.g., `*.pdf`)

### Custom Payloads

Targets support a Go `text/template` body template for customizing the event payload. Available fields: `.Type`, `.Mount`, `.Path`, `.Size`, `.Protocol`, `.User`, `.Timestamp`.

### TLS Certificate References

TLS certificate file paths in hook targets (Kafka, Redis) support references to files stored in Pika itself:
- `raw://mount/path` — read from a raw mount
- `config://key` — read from the config store
- Plain file paths are also supported

## Configuration

Pika is configured via environment variables (prefixed with `PIKA_`) or a config file.

| Variable                     | Default            | Description                                      |
| ---------------------------- | ------------------ | ------------------------------------------------ |
| `PIKA_SERVER_HOST`           | _(all interfaces)_ | Bind address                                     |
| `PIKA_SERVER_PORT`           | `8080`             | Listen port (admin UI + authenticated data)      |
| `PIKA_SERVER_PUBLIC_PORT`    |                    | Public data port (unauthenticated `/data/*`)     |
| `PIKA_SERVER_BASE_PATH`      | `/`                | Base URL path                                    |
| `PIKA_STORAGE_PATH`          | `data/pika.db`     | SQLite database path                             |
| `PIKA_SECRET_ENCRYPTION_KEY` |                    | Encryption key — setting this enables encryption |
| `PIKA_LOG_LEVEL`             | `info`             | Log level                                        |

### Built-in Authentication

Built-in session-based authentication is always enabled. On first launch, the UI shows a setup screen to create the initial admin account.

Session lifetime and cookie properties can be tuned via the config file or environment variables:

```yaml
server:
  auth:
    session_ttl: 24h
    cookie:
      secure: true
      same_site: lax
```

### Forward Auth

Pika supports forward authentication (e.g., with Turna, Authelia, Authentik). Forward-auth is configured and toggled at runtime from **Settings > Forward Auth** in the UI — no server restart required. The middleware is hot-swapped via an [ada Slot](https://rakunlabs.github.io/ada/guide/runtime-reload.html).

When enabled, pika uses a **session-first** strategy:

1. If the request has a valid local session cookie, the user is authenticated immediately (forward-auth is skipped).
2. If no session cookie is present and forward-auth is enabled, pika delegates authentication to the configured external auth service.
3. If neither succeeds, the request gets a 401.

This means local login always works — even when forward-auth is active. The `/api/v1/info` endpoint is always accessible so the SPA can boot and show the local login page regardless of forward-auth redirects.

#### Forward-Auth Permissions

Under forward auth, pika can translate external group names into its own capability keys. This is configured at runtime in **Settings > Forward Auth > Permissions** tab in the UI, and persisted in the database alongside other settings. The pika-native capability keys are:

| Key                    | Grants                                                                          |
| ---------------------- | ------------------------------------------------------------------------------- |
| `files.read`           | View configurations, versions, variants, render, search, convert                |
| `files.write`          | Create, update, delete configurations and variants                              |
| `raw.read`             | Browse and download raw mount contents                                          |
| `raw.write`            | Upload, delete, rename, copy, move raw mount contents                           |
| `settings.manage`      | View and modify server settings, admin secret, backup/restore, key rotation    |
| `tokens.manage`        | Create, edit, revoke API access tokens                                          |
| `users.manage`         | Create, edit, delete, kick users (built-in auth only)                           |
| `permissions.manage`   | Define permission bundles and assign them (built-in auth only)                  |

Example mapping: if your gateway emits `X-Groups: pika-editor,auditors` for a user, a mapping like

```
pika-editor  →  files.read, files.write, raw.read
auditors     →  files.read, raw.read, tokens.manage
```

grants that user the union of both sets. A user in multiple groups gets the union; unknown groups are ignored. Users not in the Superadmins allowlist and without any matching group are **denied** any restricted action (403).

When external permissions are **disabled** (the default), forward-auth users have no permission checks applied — pika trusts the gateway completely. Enable enforcement from the **Permissions** tab once the mapping is in place.

The groups header name and value separator are configurable. If your gateway uses `X-Pika-Roles` with `|` separators, set both fields in the Permissions form; pika also accepts repeated header lines as a single concatenated list.

Forward-auth users are identified purely by username — pika does not create rows in its `users` table for them. User and Permission bundle management in the UI remains available only under built-in auth.

## Kubernetes Deployment

Kustomize manifests are provided in [`ci/kubernetes/`](ci/kubernetes/). They include a Deployment, Service, ConfigMap (with seed user and public port), PVC, ServiceAccount, and a Gateway API HTTPRoute for `pika.example.com`.

### Quick Deploy

Apply directly from the repository:

```sh
kubectl apply -k https://github.com/rakunlabs/pika/ci/kubernetes
```

Pin to a specific version:

```sh
kubectl apply -k "https://github.com/rakunlabs/pika/ci/kubernetes?ref=v0.1.0"
```

### Customizing with a Remote Base

Create your own `kustomization.yaml` that references the upstream manifests and overrides what you need:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - https://github.com/rakunlabs/pika/ci/kubernetes?ref=main

images:
  - name: ghcr.io/rakunlabs/pika
    newTag: v0.1.0

patches:
  - target:
      kind: ConfigMap
      name: pika
    patch: |
      - op: replace
        path: /data/pika.yaml
        value: |
          server:
            port: "8080"
            auth:
              session_ttl: 24h
              cookie:
                secure: true
                same_site: lax
          storage:
            sqlite:
              dsn: file:/data/pika.db?cache=shared
  - target:
      kind: HTTPRoute
      name: pika
    patch: |
      - op: replace
        path: /spec/hostnames/0
        value: pika.mydomain.com
```

Then apply:

```sh
kubectl apply -k .
```

The public port (9090) is only exposed as a ClusterIP service — accessible within the cluster at `pika.pika.svc.cluster.local:9090`, not through the gateway.
