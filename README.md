<img align="left" height="78" src="_ui/public/favicon-192x192.png" />

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

## Configuration

Pika is configured via environment variables (prefixed with `PIKA_`) or a config file.

| Variable                     | Default            | Description                                  |
| ---------------------------- | ------------------ | -------------------------------------------- |
| `PIKA_SERVER_HOST`           | _(all interfaces)_ | Bind address                                 |
| `PIKA_SERVER_PORT`           | `8080`             | Listen port (admin UI + authenticated data)  |
| `PIKA_SERVER_PUBLIC_PORT`    |                    | Public data port (unauthenticated `/data/*`) |
| `PIKA_SERVER_BASE_PATH`      | `/`                | Base URL path                                |
| `PIKA_STORAGE_PATH`          | `pika.db`          | SQLite database path                         |
| `PIKA_SECRET_ENABLED`        | `false`            | Enable value encryption                      |
| `PIKA_SECRET_ENCRYPTION_KEY` |                    | Encryption key (any string)                  |
| `PIKA_LOG_LEVEL`             | `info`             | Log level                                    |

### Built-in Authentication

```yaml
server:
  auth:
    cookie_secret: "your-secret-key"
    session_ttl: 24h
    seed_user:
      username: admin
      password: changeme
```

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
        path: /data/config.yaml
        value: |
          server:
            port: "8080"
            public_port: "9090"
            auth:
              cookie_secret: my-real-secret
              seed_user:
                username: admin
                password: my-real-password
          storage:
            path: /data/pika.db
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
