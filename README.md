<img align="left" height="64" src="_ui/public/favicon-192x192.png" />

# pika

[![License](https://img.shields.io/github/license/rakunlabs/pika?color=blue&style=flat-square)](https://raw.githubusercontent.com/rakunlabs/pika/main/LICENSE)
[![Coverage](https://img.shields.io/sonar/coverage/rakunlabs_pika?logo=sonarcloud&server=https%3A%2F%2Fsonarcloud.io&style=flat-square)](https://sonarcloud.io/summary/overall?id=rakunlabs_pika)
[![GitHub Workflow Status](https://img.shields.io/github/actions/workflow/status/rakunlabs/pika/test.yml?branch=main&logo=github&style=flat-square&label=ci)](https://github.com/rakunlabs/pika/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/rakunlabs/pika?style=flat-square)](https://goreportcard.com/report/github.com/rakunlabs/pika)
[![Web](https://img.shields.io/badge/web-document-blueviolet?style=flat-square)](https://rakunlabs.github.io/pika/)

General configuration server, secrets manager, and personal vault with a beautiful web UI and a powerful API.

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
- Built-in encryption (XChaCha20-Poly1305) with key rotation
- Real-time preview of resolved configs
- Event hooks (HTTP webhooks, Kafka, Redis Pub/Sub, NATS)
- Inline editor with syntax validation (JSON, YAML, TOML)
- Personal vault (client-side end-to-end encrypted passwords, TOTP, SSH keys, …)
- Built-in MCP server so AI agents can browse and edit configs under your existing token permissions

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

## Hooks

Pika can push event notifications when files or configs change. Hooks are configured via **Settings > Hooks** in the UI.

### Event Types

| Event            | Trigger          |
| ---------------- | ---------------- |
| `config.created` | Config created   |
| `config.updated` | Config updated   |
| `config.deleted` | Config deleted   |
| `*`              | All events       |

### Targets

Each hook can send events to one or more targets:

- **HTTP Webhook** — POST/PUT to any URL with custom headers
- **Kafka** — Produce messages to Kafka topics (supports TLS, SASL/PLAIN, SASL/SCRAM)
- **Redis Pub/Sub** — Publish to Redis channels (standalone or cluster, with TLS + mTLS)
- **NATS** — Publish to NATS subjects (token or user/password auth)

### Filters

Hooks can be filtered by:
- **Path pattern** — glob pattern for matching config paths (e.g., `apps/*`)

### Custom Payloads

Targets support a Go `text/template` body template for customizing the event payload. Available fields: `.Type`, `.Path`, `.Size`, `.User`, `.Timestamp`.

### TLS Certificate References

TLS certificate file paths in hook targets (Kafka, Redis) support references to files stored in Pika itself:
- `config://key` — read from the config store
- Plain file paths are also supported

## MCP Server

Pika serves a [Model Context Protocol](https://modelcontextprotocol.io) endpoint so AI agents can search, read and edit configurations without you pasting them into a chat. It is part of the binary — nothing extra to run.

```
POST /api/v1/mcp
```

Connect with an API token, for example with Claude Code:

```sh
claude mcp add --transport http pika http://localhost:8080/api/v1/mcp \
  --header "Authorization: Bearer pika_abc123..."
```

The endpoint reuses pika's existing authorization rather than adding a parallel one. It accepts the same two credentials as `/data/*` — an API token (authorized by its path scopes and operations) or a UI session cookie (authorized by capabilities and path patterns) — and enforces them per tool call. The tool list is filtered to what the caller may actually do, folder listings hide out-of-scope entries, search never leaks a path you cannot read, and writes are attributed in version history.

So a token scoped `read` on `team-a/**` yields an agent with a read-only tool set that cannot see, search or even list anything outside that subtree.

Tools cover config search, folder browsing, reading stored source vs. fully resolved values, version and variant history, writes and deletes, plus the same operations against configured external backends. See [docs/guide/mcp.md](_docs/guide/mcp.md).

## Configuration

Pika is configured via environment variables (prefixed with `PIKA_`) or a config file.

| Variable                     | Default            | Description                                      |
| ---------------------------- | ------------------ | ------------------------------------------------ |
| `PIKA_SERVER_HOST`           | _(all interfaces)_ | Bind address                                     |
| `PIKA_SERVER_PORT`           | `8080`             | Listen port (admin UI + authenticated data)      |
| `PIKA_SERVER_BASE_PATH`      | `/`                | Base URL path                                    |
| `PIKA_STORAGE_BW_PATH`       | `data/pika`        | BadgerDB data directory                          |
| `PIKA_LOG_LEVEL`             | `info`             | Log level                                        |

> The at-rest encryption master key is supplied through the web UI
> (or `POST /api/v1/key/unlock`) on every restart — it is not
> stored on disk or in the environment. See
> [docs/guide/encryption.md](_docs/guide/encryption.md).

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

### External Authentication Strategies

Beyond built-in local accounts, pika supports OAuth2/OIDC providers and header-based forward authentication (e.g., with Turna, Authelia, Authentik). All strategies are configured and toggled at runtime from **Settings > Authentication** in the UI — no server restart required. The middleware is hot-swapped via an [ada Slot](https://rakunlabs.github.io/ada/guide/runtime-reload.html).

When any external strategy is enabled, pika uses a **session-first** approach:

1. If the request has a valid local session cookie, the user is authenticated immediately (external strategies are skipped).
2. If no session cookie is present, pika delegates authentication to the configured strategy (OAuth2 or forward-auth headers).
3. If neither succeeds, the request gets a 401.

This means local login always works — even when external auth is active. The `/api/v1/info` endpoint is always accessible so the SPA can boot and show the local login page regardless of external auth redirects.

OAuth2/OIDC providers read identity claims from the configured or discovered UserInfo endpoint when available, then revoke the upstream access token best-effort after pika issues its own session. Unknown OAuth2 identities fail closed by default; enable **Auto-create users** per provider to create external-only pika users at first login while still reusing existing links or verified-email matches first.

#### External Permissions

Under external authentication, pika can translate provider-supplied groups (or OAuth2 scopes) into its own capability keys. This is configured at runtime in **Settings > Authentication > Permissions** in the UI, and persisted in the database alongside other settings. The pika-native capability keys are:

| Key                  | Grants                                                                      |
| -------------------- | --------------------------------------------------------------------------- |
| `files.read`         | View configurations, versions, variants, render, search, convert            |
| `files.write`        | Create, update, delete configurations and variants                          |
| `external.read`      | Browse, search, read entries from configured external resources             |
| `external.write`     | Create, update, delete entries on configured external resources             |
| `settings.manage`    | View and modify server settings, backup/restore, server encryption-key lifecycle |
| `tokens.manage`      | Create, edit, revoke API access tokens                                      |
| `users.manage`       | Create, edit, delete, kick users (built-in auth only)                       |
| `permissions.manage` | Define permission bundles and assign them (built-in auth only)              |

Example mapping: if your gateway emits `X-Groups: pika-editor,auditors` for a user, a mapping like

```
pika-editor  →  files.read, files.write, external.read
auditors     →  files.read, tokens.manage
```

grants that user the union of both sets. A user in multiple groups gets the union; unknown groups are ignored. Users not in the Superadmins allowlist and without any matching group are **denied** any restricted action (403).

When external permissions are **disabled** (the default), externally-authenticated users have no permission checks applied — pika trusts the gateway/IdP completely. Enable enforcement from the **Permissions** section once the mapping is in place.

The groups header name and value separator are configurable. If your gateway uses `X-Pika-Roles` with `|` separators, set both fields in the Permissions form; pika also accepts repeated header lines as a single concatenated list. The same role/scope mapping applies to OAuth2 token claims.

Externally-authenticated users are identified by provider + subject and can be linked to local `users` rows by verified-email auto-linking or OAuth2 auto-create. User and Permission bundle management in the UI remains available only under built-in auth.

## Kubernetes Deployment

Kustomize manifests are provided in [`ci/kubernetes/`](ci/kubernetes/). They include a Namespace, ServiceAccount, ConfigMap, Secret (cluster pre-shared key), StatefulSet (3 replicas with per-pod PVC via `volumeClaimTemplates`), a ClusterIP Service for HTTP traffic, and a headless Service for cluster peer discovery. Ingress is intentionally not included — bring your own (Ingress, Gateway API HTTPRoute, etc.) and point it at the `pika` Service on port 8080.

### Quick Deploy

Apply directly from the repository:

```sh
kubectl apply -k https://github.com/rakunlabs/pika/ci/kubernetes
```

Pin to a specific version:

```sh
kubectl apply -k "https://github.com/rakunlabs/pika/ci/kubernetes?ref=v0.1.0"
```

> **Before deploying**, change the placeholder `security_key` in [`secret.yaml`](ci/kubernetes/secret.yaml) to a real random value (e.g. `openssl rand -base64 48`). All replicas must share the same key.

### Clustering Notes

The default manifests deploy a 3-replica cluster (see [Clustering](#clustering)). If you change `spec.replicas` on the StatefulSet, also update `cluster.replicas` in the ConfigMap — both must match for the quorum math.

### External Secrets Operator

If you use [External Secrets Operator](https://external-secrets.io), [`ci/kubernetes/examples/`](ci/kubernetes/examples/) contains examples for pulling Pika-stored configs and TLS material into Kubernetes Secrets via a `SecretStore` / `ExternalSecret` pair.

### Customizing with a Remote Base

Create your own `kustomization.yaml` that references the upstream manifests and overrides what you need:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - https://github.com/rakunlabs/pika/ci/kubernetes?ref=main

images:
  - name: ghcr.io/rakunlabs/pika
    newTag: v0.3.0

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
            bw:
              path: /data/pika
          cluster:
            enabled: true
            dns_addr: pika-cluster.pika.svc.cluster.local
            replicas: 3
            port: 5000
  - target:
      kind: Secret
      name: pika-cluster
    patch: |
      - op: replace
        path: /stringData/security_key
        value: "your-long-random-string-here"
```

Then apply:

```sh
kubectl apply -k .
```

The public port (9090) is exposed on the `pika` Service alongside the admin port — accessible within the cluster at `pika.pika.svc.cluster.local:9090`. By default it has no external Ingress, so traffic is cluster-internal only; route it externally with your own Ingress/HTTPRoute if you want to expose `/data/*` to consumers outside the cluster.

## Clustering

Pika supports a 3+ node cluster for high availability. Reads are served locally on every node from the replicated [bw](https://github.com/rakunlabs/bw) store; writes are routed to the elected leader and the diff is pushed to every follower before the leader replies. Peer discovery and leader election use [alan](https://github.com/rakunlabs/alan) over QUIC.

Enable clustering in the config file:

```yaml
cluster:
  enabled: true
  dns_addr: pika-cluster.pika.svc.cluster.local  # resolves to all peer IPs
  replicas: 3                                    # quorum size; tolerates losing 1
  port: 5000                                     # UDP/QUIC peer port
  security_key: "long-random-string"             # pre-shared key
```

For a local 3-node demo, see [`example/cluster/`](example/cluster/). For Kubernetes, the manifests in [`ci/kubernetes/`](ci/kubernetes/) ship with a 3-replica StatefulSet + headless Service preconfigured for clustering.
