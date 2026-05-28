# Configuration

Pika has two layers of configuration:

1. **Process-level config** — supplied at startup via environment variables, a YAML/TOML/JSON file, or one of the supported external sources (Vault, Consul, GCP Parameter/Secret Manager, HTTP). This covers things that must be known before the server is up: the listen port, TLS file paths, the storage path, cluster membership, and the optional at-rest encryption passphrase.
2. **Runtime settings** — stored in the database and edited from the **Settings** pages of the UI. This covers everything else: certificate rotation, HTTP fallback policy, authentication strategies, external resources, hooks, public-port compatibility endpoints, and so on. Changes apply without restarting unless noted.

This page documents the first layer.

## How config is loaded

Process-level config is loaded with [`rakunlabs/chu`](https://github.com/rakunlabs/chu). The configuration name passed to chu is `pika`. On every start, chu walks a fixed list of loaders in order and merges what each one returns into the same `Config` struct — later loaders override fields set by earlier ones:

| # | Loader      | Trigger                                                                            | What it provides                                                                |
| - | ----------- | ---------------------------------------------------------------------------------- | ------------------------------------------------------------------------------- |
| 1 | `default`   | Always runs                                                                        | Struct-tag `default:"…"` values (e.g. `server.port=8080`, `cluster.port=5000`). |
| 2 | `consul`    | `CONSUL_HTTP_ADDR` set                                                             | YAML payload from Consul KV at key `pika` (optionally prefixed).                |
| 3 | `vault`     | `VAULT_ADDR` or `VAULT_AGENT_ADDR` set, plus `VAULT_SECRET_BASE_PATH`              | KVv2 secret at `<base>/pika`.                                                   |
| 4 | `gcpparameter` | `GCP_PROJECT_ID` set                                                            | GCP Parameter Manager parameter named `pika`.                                   |
| 5 | `gcpsecret` | `GCP_PROJECT_ID` set                                                               | GCP Secret Manager secret named `pika` (+ optional shared/generic secrets).     |
| 6 | `http`      | `CONFIG_HTTP_ADDR` set                                                             | `GET {CONFIG_HTTP_ADDR}/pika` returning JSON/YAML/TOML.                         |
| 7 | `file`      | `CONFIG_FILE=/path/to/pika.yaml` set, or `pika.{toml,yaml,yml,json}` in the working directory or `/etc/` | The on-disk config file.                                                        |
| 8 | `env`       | Always runs                                                                        | Process environment variables with the `PIKA_` prefix.                          |

Notes:

- The default order intentionally puts `env` last so that an operator can always override anything else (e.g. force `PIKA_LOG_LEVEL=debug` for a single boot).
- Each external loader auto-skips if its trigger env var is missing, so you only pay for what you use. There is **no** hard dependency on Consul/Vault/GCP libraries at runtime — the clients are only constructed when their env vars are set.
- The `default` tag does not "fill in" zero values after another loader has set the struct field. It only seeds fields nothing else touched.
- Sensitive fields tagged `log:"-"` (`encryption.password`, `cluster.security_key`) are masked from the `loaded configuration` log line printed at startup.

### When to use which source

- **YAML/TOML file** — local dev, baked container images, Kubernetes ConfigMap mounts.
- **`PIKA_*` env vars** — Docker Compose, Kubernetes Deployments, ad-hoc one-shot overrides.
- **Vault / Consul** — central, audited bootstrap config in environments where you already operate one of those.
- **GCP Parameter / Secret Manager** — same idea on GCP. Parameter Manager renders templated payloads server-side; Secret Manager stores opaque blobs and supports a list of shared/generic secrets that get merged in BEFORE the app-specific one.
- **HTTP** — point pika at any internal config server speaking JSON/YAML/TOML.

## Environment variables

The most common variables. All `PIKA_*` env vars use the `PIKA_` prefix and `_` for nesting; the underlying tag is `cfg`, so `server.tls.cert_file` becomes `PIKA_SERVER_TLS_CERT_FILE`.

| Variable                     | Default            | Description                                          |
| ---------------------------- | ------------------ | ---------------------------------------------------- |
| `PIKA_LOG_LEVEL`             | `info`             | `debug`, `info`, `warn`, or `error`.                 |
| `PIKA_SERVER_HOST`           | _(all interfaces)_ | Bind address for the admin server.                   |
| `PIKA_SERVER_PORT`           | `8080`             | Listen port (HTTPS admin UI + authenticated `/data/*`). |
| `PIKA_SERVER_BASE_PATH`      | `/`                | Base URL path — set when running behind a sub-path.  |
| `PIKA_SERVER_TLS_ENABLED`    | `true`             | Enable HTTPS support on the main listener.           |
| `PIKA_SERVER_TLS_CERT_FILE`  | _(auto-managed)_   | PEM certificate path. Empty = managed cert under the storage directory. |
| `PIKA_SERVER_TLS_KEY_FILE`   | _(auto-managed)_   | PEM private key path. Empty = managed key under the storage directory.  |
| `PIKA_STORAGE_BW_PATH`       | `data/pika`        | Embedded BadgerDB directory.                         |
| `PIKA_STORAGE_BW_IN_MEMORY`  | `false`            | Run the BadgerDB backend entirely in memory (tests/CI). |
| `PIKA_ENCRYPTION_PASSWORD`   |                    | Optional at-rest passphrase for auto-unlock/auto-initialize. See [Encryption](./encryption). |
| `PIKA_CLUSTER_ENABLED`       | `false`            | Enable clustering.                                   |
| `PIKA_CLUSTER_DNS_ADDR`      |                    | DNS name resolving to all peer IPs.                  |
| `PIKA_CLUSTER_REPLICAS`      |                    | Number of cluster members (must match reality).      |
| `PIKA_CLUSTER_PORT`          | `5000`             | UDP/QUIC peer port.                                  |
| `PIKA_CLUSTER_SECURITY_KEY`  |                    | Pre-shared key — must match across peers.            |

Loader-control env vars (not prefixed with `PIKA_`, consumed directly by chu):

| Variable                  | Loader        | Description                                                                                          |
| ------------------------- | ------------- | ---------------------------------------------------------------------------------------------------- |
| `CONFIG_FILE`             | `file`        | Absolute path to the config file. Overrides folder/extension search.                                 |
| `CONFIG_ENV_FILE`         | `env`         | Extra `.env`-style file to load before reading the environment.                                      |
| `CONFIG_HTTP_ADDR`        | `http`        | Base URL of an HTTP config server. The loader fetches `GET {addr}/pika`.                             |
| `CONFIG_HTTP_SUFFIX`      | `http`        | Optional path suffix appended after `/pika`.                                                         |
| `CONFIG_HTTP_QUERY`       | `http`        | Optional query string appended to the request.                                                       |
| `CONSUL_HTTP_ADDR`        | `consul`      | Consul HTTP API address. Missing = loader skipped.                                                   |
| `CONSUL_CONFIG_PATH_PREFIX` | `consul`    | KV prefix prepended to the config name. Final key: `<prefix>/pika`.                                  |
| `VAULT_ADDR` / `VAULT_AGENT_ADDR` | `vault` | Vault server address. Missing both = loader skipped.                                                  |
| `VAULT_SECRET_BASE_PATH`  | `vault`       | KVv2 mount path. Required when the loader runs.                                                      |
| `VAULT_ROLE_ID`, `VAULT_ROLE_SECRET` | `vault` | AppRole credentials. Optional — Vault Agent / token auth also work.                                 |
| `VAULT_APPROLE_BASE_PATH` | `vault`       | AppRole login path. Default `auth/approle/login`.                                                    |
| `GCP_PROJECT_ID`          | `gcpparameter`, `gcpsecret` | GCP project id. Missing = both GCP loaders skip.                                       |
| `GCP_PARAMETER_LOCATION`  | `gcpparameter`| Default `global`. Use a region for regional parameters.                                              |
| `GCP_PARAMETER_VERSION`   | `gcpparameter`| Default `latest`.                                                                                    |
| `GCP_SECRET_LOCATION`     | `gcpsecret`   | Default `global`.                                                                                    |
| `GCP_SECRET_VERSION`      | `gcpsecret`   | Default `latest`.                                                                                    |
| `GCP_SECRET_ADDITIONAL`   | `gcpsecret`   | Comma-separated list of shared secret ids loaded BEFORE the app-specific secret, e.g. `generic,keycloak`. |

## Config file

Set `CONFIG_FILE=/path/to/pika.yaml` to load a config file. Without it, the file loader looks for `pika.{toml,yaml,yml,json}` first in the current directory, then in `/etc/`. Env vars still override individual keys after the file is parsed.

A representative full config (all top-level keys correspond to the fields of `internal/config/config.go`):

```yaml
log_level: info

server:
  host: ""
  port: "8080"
  base_path: /
  tls:
    enabled: true
    cert_file: ""    # empty = auto-managed self-signed cert under storage path
    key_file: ""

storage:
  bw:
    enabled: true
    path: data/pika
    in_memory: false

# Optional. When set, pika auto-unlocks (or auto-initializes) the
# at-rest encryption layer on boot. Leave empty to keep the original
# "operator unlocks through the UI on every restart" flow.
# Also readable via PIKA_ENCRYPTION_PASSWORD. See guide/encryption.md.
encryption:
  password: ""

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
Most production deployments only set a handful of these. The Docker Compose example in [Installation](./installation) uses just `PIKA_LOG_LEVEL`. Authentication, login UI, cookie behaviour, token issuance, OAuth2/LDAP/passkey providers, and rate limits are **runtime settings** — configure them under **Settings → Authentication**, not in this file.
:::

## At-rest encryption passphrase

The optional `encryption.password` field (or `PIKA_ENCRYPTION_PASSWORD` env var) controls how the at-rest encryption layer behaves at boot:

- **Empty (default)** — original flow. If a verifier already exists on disk the server starts LOCKED and the operator unlocks through the UI on every restart.
- **Set + server already initialized** — pika auto-unlocks with the supplied passphrase. A wrong value is non-fatal: the server stays LOCKED, the UnlockScreen still asks for the real key, and `/api/v1/info` carries a warning flag so the SPA can call out the bad config value.
- **Set + fresh install** — pika auto-initializes the encryption layer using the supplied passphrase as the at-rest key. Same effect as clicking "Initialize" under **Settings → Server encryption key**.

::: warning
Populating `encryption.password` undoes the "key never lives on disk" property of the default flow. The field is masked from the startup `loaded configuration` log line (`log:"-"`), but it is readable to anyone who can read the config file or `/proc/$pid/environ`. Operators trading manual-unlock UX for at-rest-key-on-disk should accept that trade-off explicitly. See [Encryption](./encryption) for the full lifecycle.
:::

## External config sources

::: info
This section is about loading **pika's own bootstrap config** from Vault/Consul/GCP. It is not the same as **config inheritance**, which is the runtime feature that pulls values into served configs from those same systems. For inheritance see the [Inheritance](./inheritance/) section.
:::

The external loaders are wired in at compile time by importing them with `_ "…"` in `internal/config/config.go`. Each one runs only when its trigger env var is present and silently skips otherwise. The payloads from every active source are merged into the same struct in the order shown in [How config is loaded](#how-config-is-loaded), so you can layer "shared baseline" + "overrides" cleanly.

### Vault

Reads a KVv2 secret whose data fields map onto `Config`.

```sh
export VAULT_ADDR=https://vault.internal:8200
export VAULT_SECRET_BASE_PATH=secret/apps          # KVv2 mount path
# AppRole login (optional — VAULT_TOKEN or Vault Agent also work)
export VAULT_ROLE_ID=...
export VAULT_ROLE_SECRET=...
```

Pika fetches `secret/apps/pika` (KVv2). The fields stored at that path are merged into `Config` — e.g. a Vault secret with `log_level: debug` and `encryption.password: <value>` is enough to drive both.

### Consul

Reads a single KV entry whose value is YAML/JSON.

```sh
export CONSUL_HTTP_ADDR=http://consul.internal:8500
export CONSUL_CONFIG_PATH_PREFIX=config/prod       # optional
```

Pika fetches the KV value at `config/prod/pika` (or just `pika` without the prefix), decodes it as YAML by default, and merges it into `Config`. Standard Consul auth env vars (`CONSUL_HTTP_TOKEN`, mTLS variables) are honoured by the underlying client.

### GCP Parameter Manager

Renders a parameter version and merges the payload.

```sh
export GOOGLE_APPLICATION_CREDENTIALS=/path/to/sa.json   # or workload identity
export GCP_PROJECT_ID=my-project
export GCP_PARAMETER_LOCATION=global       # default; or a region
export GCP_PARAMETER_VERSION=latest        # default
```

The parameter id is the configuration name (`pika`). `/` characters in the name are replaced with `-` because GCP parameter ids don't allow `/`. Parameter Manager renders the payload server-side, so referenced secrets are resolved before pika sees the bytes. Decoded as YAML by default.

### GCP Secret Manager

Accesses one or more secret versions and merges their payloads.

```sh
export GOOGLE_APPLICATION_CREDENTIALS=/path/to/sa.json
export GCP_PROJECT_ID=my-project
export GCP_SECRET_LOCATION=global          # default
export GCP_SECRET_VERSION=latest           # default
# Optional shared secrets loaded BEFORE the app-specific one; later wins.
export GCP_SECRET_ADDITIONAL=generic,keycloak
```

Loads (in order):

1. Each id in `GCP_SECRET_ADDITIONAL` — missing ones are silently skipped. Useful for mounting "common credentials shared across multiple apps".
2. The app-specific secret named `pika`.

If none of these exist the loader skips entirely. Decoded as YAML by default.

### HTTP

Fetches the config from an HTTP endpoint that returns JSON/YAML/TOML.

```sh
export CONFIG_HTTP_ADDR=https://config.internal/v1
export CONFIG_HTTP_SUFFIX=/raw            # optional, appended after /pika
export CONFIG_HTTP_QUERY=env=prod         # optional query string
```

Pika issues `GET ${CONFIG_HTTP_ADDR}/pika${CONFIG_HTTP_SUFFIX}?${CONFIG_HTTP_QUERY}`. `200` is parsed by `Content-Type`; `204` / `404` are treated as "not configured here" and the loader is skipped.

## HTTPS

The main UI/API listener serves HTTPS by default. If no certificate exists at startup, Pika creates a self-signed ECDSA certificate under the storage directory and uses it immediately. Manage the active certificate from **Settings → Certificates**:

- Generate a replacement self-signed certificate with custom DNS/IP SANs.
- Upload a PEM certificate chain and matching private key.
- Allow plaintext HTTP on the same main port when running behind a trusted TLS-terminating proxy.
- Disable HTTPS from the UI only when plaintext HTTP is enabled, to avoid locking yourself out.

Set `server.tls.enabled: false` only when you want the process to be HTTP-only from startup.

## Endpoints

Pika can expose additional listeners that serve configuration data in operator-chosen wire shapes — either a Consul KV-compatible read API or a custom Go-template response. Each endpoint binds its own `host:port`, owns its own auth setting, can run HTTPS using the managed certificate, can run an optional request-check stage, and is configured at runtime from **Settings → Endpoints**.

See [Endpoints](/guide/endpoints) for the wire format, template variables, and authentication options.

## Authentication, sessions, and cookies

Authentication strategies (local, OAuth2, LDAP, passkey, header/proxy), session TTL, cookie behaviour, token issuance, and rate limiting are **runtime settings**, not process-level config — there is no `server.auth` block in `Config`. Configure them under **Settings → Authentication** in the UI, or via `PUT /api/v1/settings` (see [Admin API](/guide/api)). Changes apply without restarting.

## Reverse proxies and base paths

If you serve pika under a sub-path (e.g. `https://example.com/pika/`), set:

```yaml
server:
  base_path: /pika
```

Pika rewrites internal links and SPA routing accordingly. Use a leading slash and no trailing slash; `/pika/` is normalized to `/pika`. If your proxy strips the prefix before forwarding to Pika, leave `server.base_path` unset. Make sure your proxy forwards the original `Host` header.

## CLI

The pika binary takes a single optional flag (`--config`) and otherwise has **no subcommands**. All operational concerns — backups, key rotation, user management — are exposed through the [admin API](/guide/api) and the UI.
