<img align="left" height="64" src="_ui/public/favicon-192x192.png" />

# pika

General configuration server.

> Highly on development stage, expect breaking changes. Feedback and contributions are very welcome!

## Quick Start

```sh
docker run -d --name pika -p 8080:8080 ghcr.io/rakunlabs/pika:latest
```

Open `http://localhost:8080` to access the web UI.

## Features

- Multi-format configs (JSON, YAML, TOML)
- Version history with semver constraints
- Variants for environment-specific overrides
- Config inheritance (internal files, Vault, HTTP)
- Token-based access control with glob scopes
- Full-text search across all configs
- Built-in encryption (ChaCha20) with key rotation
- Real-time preview of resolved configs

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

By default, `/data/*` requires a Bearer token. If you want to serve configs without authentication (e.g., inside a private network), configure a public port:

```yaml
server:
  port: "8080"         # Admin UI + authenticated /data/*
  public_port: "9090"  # Unauthenticated /data/* and /healthz only
```

```sh
# No token needed on the public port
curl http://localhost:9090/data/myapp/config

# With variant and format
curl "http://localhost:9090/data/myapp/config?variant=prod&format=json"
```

The public port only exposes `/data/*` and `/healthz` — no admin API, no UI.

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

## Inheritance

Configs can inherit from other sources. Inherited values act as defaults — the current config's values always take precedence.

### Internal Inheritance

Inherit from another pika config:

```yaml
# In the file's metadata (inherits):
- source: shared/defaults
```

### External Inheritance

Inherit from Vault, HTTP, or Kubernetes sources (configure in Settings):

```yaml
- resource: my-vault
  path: myapp/secrets

- resource: my-api
  path: /config/defaults

- resource: my-k8s
  path: production/secret/db-credentials

- resource: my-k8s
  path: default/configmap/app-config
```

### Kubernetes Secrets & ConfigMaps

Pika can inherit from Kubernetes Secrets and ConfigMaps. Configure a Kubernetes external resource in Settings with an optional kubeconfig path (leave empty for in-cluster auth).

Path format: `namespace/type/name`

| Path                              | Reads                                            |
| --------------------------------- | ------------------------------------------------ |
| `default/secret/db-creds`         | Secret "db-creds" in namespace "default"         |
| `production/configmap/app-config` | ConfigMap "app-config" in namespace "production" |

Secret values are automatically base64-decoded. ConfigMap values are returned as-is.

Example — inject database credentials from a Kubernetes Secret:

```yaml
- resource: k8s-prod
  path: production/secret/db-credentials
  paths: ["username", "password"]
  inject: database.auth
```

### Selective Inheritance

Pull only specific fields and inject them at a target path:

```yaml
- source: shared/database
  paths: ["host", "port"]      # Only these fields
  inject: database.connection   # Place under this key
```

## Raw Filesystem Serving

Pika can serve files directly from local filesystem directories over HTTP. This is useful for serving static assets, certificates, or any files that don't need versioning or the config management features.

### Configuration

Raw mounts support three backend types: **local** (filesystem), **S3** (compatible), and **FTP/FTPS**. Mounts can be configured via config file, environment variables, or the Settings UI.

#### Local Directory

```yaml
server:
  raw:
    - prefix: configs
      type: local        # default, can be omitted
      path: /opt/configs
```

#### S3-Compatible Storage

```yaml
server:
  raw:
    - prefix: assets
      type: s3
      s3:
        bucket: my-assets
        region: us-east-1
        endpoint: ""              # leave empty for AWS S3
        access_key: AKIA...
        secret_key: wJal...
        prefix: ""                # optional key prefix within bucket
        path_style: false         # set true for MinIO
        secure: true              # use HTTPS
```

Works with AWS S3, MinIO, Cloudflare R2, DigitalOcean Spaces, and any S3-compatible storage.

#### FTP/FTPS

```yaml
server:
  raw:
    - prefix: legacy
      type: ftp
      ftp:
        host: ftp.example.com:21
        username: admin
        password: secret
        tls: false                # set true for FTPS
        base_path: /data          # remote directory root
```

#### FUSE Mounts

FUSE mounts (e.g., `s3fs`, `rclone mount`, `sshfs`, `gcsfuse`) appear as normal directories on the host. Use `type: local` with the FUSE mount path:

```yaml
server:
  raw:
    - prefix: remote-bucket
      type: local
      path: /mnt/s3-fuse          # FUSE mount point
```

#### Environment Variables

```sh
PIKA_SERVER_RAW_0_PREFIX=configs
PIKA_SERVER_RAW_0_TYPE=local
PIKA_SERVER_RAW_0_PATH=/opt/configs
PIKA_SERVER_RAW_1_PREFIX=assets
PIKA_SERVER_RAW_1_TYPE=s3
PIKA_SERVER_RAW_1_S3_BUCKET=my-assets
PIKA_SERVER_RAW_1_S3_REGION=us-east-1
PIKA_SERVER_RAW_1_S3_ACCESS_KEY=AKIA...
PIKA_SERVER_RAW_1_S3_SECRET_KEY=wJal...
```

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

Raw mounts can also be managed from **Settings > Raw Mounts** in the web UI. The form supports all three backend types with conditional fields:

- **Local**: directory path input
- **S3**: bucket, region, endpoint, credentials, key prefix, path-style toggle
- **FTP**: host, credentials, base path, TLS toggle

Changes take effect immediately — no server restart required.

## Configuration

Pika is configured via environment variables (prefixed with `PIKA_`) or a config file.

| Variable                     | Default            | Description                                  |
| ---------------------------- | ------------------ | -------------------------------------------- |
| `PIKA_SERVER_HOST`           | _(all interfaces)_ | Bind address                                 |
| `PIKA_SERVER_PORT`           | `8080`             | Listen port (admin UI + authenticated data)  |
| `PIKA_SERVER_PUBLIC_PORT`    |                    | Public data port (unauthenticated `/data/*`) |
| `PIKA_SERVER_BASE_PATH`      | `/`                | Base URL path                                |
| `PIKA_STORAGE_PATH`          | `pika.db`          | SQLite database path                         |
| `PIKA_SECRET_ENCRYPTION_KEY` |                    | Encryption key — setting this enables encryption |
| `PIKA_SERVER_RAW_N_PREFIX`   |                    | URL prefix for raw mount N (e.g., `configs`) |
| `PIKA_SERVER_RAW_N_TYPE`     | `local`            | Backend type: `local`, `s3`, `ftp`           |
| `PIKA_SERVER_RAW_N_PATH`     |                    | Local directory for raw mount N              |
| `PIKA_SERVER_RAW_N_S3_*`     |                    | S3 config: `BUCKET`, `REGION`, `ENDPOINT`, etc. |
| `PIKA_SERVER_RAW_N_FTP_*`    |                    | FTP config: `HOST`, `USERNAME`, `PASSWORD`, etc. |
| `PIKA_LOG_LEVEL`             | `info`             | Log level                                    |

### Built-in Authentication

```yaml
server:
  auth:
    session_ttl: 24h
```

On first launch, the UI will show a setup screen to create your initial admin account.

### Forward Auth

Pika supports forward authentication (e.g., with Turna, Authelia, Authentik):

```yaml
server:
  forward_auth:
    address: http://authelia:9091/api/verify
    request_headers:
      - Cookie
    response_headers:
      - X-User
```

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
            public_port: "9090"
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
