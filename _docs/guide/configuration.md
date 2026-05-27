# Configuration

Pika has two layers of configuration:

1. **Process-level config** — supplied at startup via environment variables or a YAML file. This covers things that must be known before the HTTP server is up: the listen port, the storage path, the cluster membership, and the encryption key.
2. **Runtime settings** — stored in the database and edited from the **Settings** pages of the UI. This covers everything else: authentication strategies, external resources, hooks, public-port compatibility endpoints, and so on. Changes apply without restarting.

This page documents the first layer.

## Environment variables

The most common variables. All env vars use the `PIKA_` prefix and `_` for nesting.

| Variable                     | Default            | Description                                          |
| ---------------------------- | ------------------ | ---------------------------------------------------- |
| `PIKA_SERVER_HOST`           | _(all interfaces)_ | Bind address for the admin server.                   |
| `PIKA_SERVER_PORT`           | `8080`             | Listen port (admin UI + authenticated `/data/*`).    |
| `PIKA_SERVER_BASE_PATH`      | `/`                | Base URL path — set when running behind a sub-path.  |
| `PIKA_STORAGE_BW_PATH`       | `data/pika`        | Embedded BadgerDB directory.                         |
| `PIKA_LOG_LEVEL`             | `info`             | `debug`, `info`, `warn`, or `error`.                 |
| `PIKA_CLUSTER_ENABLED`       | `false`            | Enable clustering.                                   |
| `PIKA_CLUSTER_DNS_ADDR`      |                    | DNS name resolving to all peer IPs.                  |
| `PIKA_CLUSTER_REPLICAS`      |                    | Number of cluster members (must match the realisty). |
| `PIKA_CLUSTER_PORT`          | `5000`             | UDP/QUIC peer port.                                  |
| `PIKA_CLUSTER_SECURITY_KEY`  |                    | Pre-shared key — must match across peers.            |

## YAML config file

Pass `--config /path/to/pika.yaml` (or the `CONFIG_FILE` env var when using `make run`) to load a YAML file. Env vars still override individual keys.

A representative full config:

```yaml
log_level: info

server:
  host: ""
  port: "8080"
  base_path: /
  auth:
    session_ttl: 24h
    cookie:
      name: pika_session
      domain: ""
      path: /
      secure: true        # set true behind HTTPS
      same_site: lax      # lax | strict | none

storage:
  bw:
    path: /data/pika

# At-rest encryption (XChaCha20-Poly1305) is always on. The master
# key is supplied through the web UI on every restart — there is
# no `secret.encryption_key` field. See guide/encryption.md.

cluster:
  enabled: false
  dns_addr: pika-cluster.pika.svc.cluster.local
  bind_addr: 0.0.0.0
  port: 5000
  replicas: 3
  refresh_interval: 30s
  heartbeat_interval: 5s
  heartbeat_timeout: 30s
  security_key: "long-random-pre-shared-key"
  lock_key: pika-leader
  sync_interval: 5m
  prefix: pika
  forward_timeout: 30s

# OpenTelemetry — see github.com/rakunlabs/tell for the full schema.
telemetry:
  service:
    name: pika
```

::: tip
Most production deployments only set a handful of these. The Docker Compose example in [Installation](./installation) uses just `PIKA_LOG_LEVEL`. The at-rest encryption key is entered through the UI after every start; see [Encryption](./encryption).
:::

## Endpoints

Pika can expose additional HTTP listeners that serve configuration data in operator-chosen wire shapes — either a Consul KV-compatible read API or a custom Go-template response. Each endpoint binds its own `host:port`, owns its own auth setting, can run an optional request-check stage, and is configured at runtime from **Settings → Endpoints**.

See [Endpoints](/reference/compat) for the wire format, template variables, and authentication options.

## Sessions and cookies

Built-in session-based authentication is always enabled. Tune cookie behaviour under `server.auth` in the config file:

```yaml
server:
  auth:
    session_ttl: 24h
    cookie:
      secure: true         # required when serving over HTTPS
      same_site: lax       # use 'none' if your UI is on a different origin
```

## Reverse proxies and base paths

If you serve pika under a sub-path (e.g. `https://example.com/pika/`), set:

```yaml
server:
  base_path: /pika
```

Pika rewrites internal links and SPA routing accordingly. Make sure your proxy forwards the original `Host` header.

## CLI

The pika binary takes a single optional flag (`--config`) and otherwise has **no subcommands**. All operational concerns — backups, key rotation, user management — are exposed through the [admin API](/reference/api) and the UI.
