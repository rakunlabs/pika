# Admin API

The admin API lives under `/api/v1/*` and is consumed by the web UI. You can also call it programmatically — either with a session cookie (after logging in) or with an API token that holds the right capability.

This page is a high-level map. For the consumer-facing read endpoint, see [Consuming data](./consuming-data).

## Authentication

Two equivalent options:

- **Session cookie** — set automatically after you log in to the UI. Useful when scripting against your own browser session.
- **Bearer token** — `Authorization: Bearer pika_...`. The token must hold a capability that covers the requested operation.

::: tip
Token capabilities for admin operations are different from path scopes used on `/data/*`. A token holds a list of capabilities (`files.read`, `settings.manage`, …). The list is set when the token is created.
:::

## Discovery

| Method | Path                 | Capability | Purpose                                                                                                  |
| ------ | -------------------- | ---------- | -------------------------------------------------------------------------------------------------------- |
| `GET`  | `/healthz`           | _(public)_ | Liveness probe. Returns `OK`.                                                                            |
| `GET`  | `/api/v1/info`       | _(public)_ | Server metadata, version, current user identity, effective capabilities, encryption-config warning flag. |
| `GET`  | `/api/v1/key/status` | _(public)_ | Server-key state (initialized / locked / unlocked). Allowed while the server is locked.                  |

`/api/v1/info` is the discovery endpoint — the UI calls it on every load to decide which sections to render, and scripts can use it to feature-detect.

## Configuration data plane

These operate on the persisted config tree.

| Method               | Path                            | Capability                | Purpose                                                                  |
| -------------------- | ------------------------------- | ------------------------- | ------------------------------------------------------------------------ |
| `GET`                | `/api/v1/folder`, `/folder/*`   | `files.read`              | List folders / contents.                                                 |
| `POST`               | `/api/v1/folder/*`              | `files.write`             | Create a folder.                                                         |
| `DELETE`             | `/api/v1/folder/*`              | `files.write`             | Delete a folder.                                                         |
| `GET`                | `/api/v1/file/*`                | `files.read`              | Read a config file. Supports `?variant=` and `?version=`.                |
| `POST`               | `/api/v1/file/*`                | `files.write`             | Create or update a config file (creates a new version).                  |
| `DELETE`             | `/api/v1/file/*`                | `files.write`             | Delete a config file.                                                    |
| `GET`                | `/api/v1/versions/*`            | `files.read`              | List versions of a file.                                                 |
| `PATCH`              | `/api/v1/versions/*`            | `files.write`             | Set / clear the semver tag on a specific version.                        |
| `GET`                | `/api/v1/variants/*`            | `files.read`              | List the variants of a file.                                             |
| `POST`               | `/api/v1/render/*`              | `files.read`              | Resolve a file (inheritance + variant + version) and return the merged document. |
| `POST`               | `/api/v1/convert`               | `files.read`              | Convert content between JSON / YAML / TOML.                              |
| `GET`                | `/api/v1/search?q=...`          | `files.read`              | Full-text search across configs (Server-Sent Events stream).             |

## Consumer endpoints

Reachable on the same admin port. To expose configuration data on a separate port (no Bearer required, custom shape, optional request-rule pre-stage), configure an [Endpoint](./endpoints) instead.

| Method | Path           | Capability    | Purpose                                                  |
| ------ | -------------- | ------------- | -------------------------------------------------------- |
| `GET`  | `/data/*`      | `files.read` _(scope-gated)_ | Resolved config — see [Consuming data](./consuming-data). |

## External resources

Browse and operate on configured external backends (Vault, Consul, etcd, AWS, Azure, GCP, Kubernetes, HTTP). All endpoints are gated on `external.*` because exposing them returns or mutates third-party secrets.

| Method | Path                                  | Capability       | Purpose                                                              |
| ------ | ------------------------------------- | ---------------- | -------------------------------------------------------------------- |
| `GET`  | `/api/v1/external/resources`          | `external.read`  | List configured external resources.                                  |
| `GET`  | `/api/v1/external/{name}/paths`       | `external.read`  | Browse the namespace of one resource.                                |
| `POST` | `/api/v1/external/{name}/test`        | `external.read`  | Probe connectivity / credentials for one resource.                   |
| `POST` | `/api/v1/external/{name}/read`        | `external.read`  | Read one entry by path (body carries the path; `*` wildcard not viable in middle-segment routing). |
| `POST` | `/api/v1/external/{name}/write`       | `external.write` | Create / update one entry.                                           |
| `POST` | `/api/v1/external/{name}/delete`      | `external.write` | Delete one entry.                                                    |
| `POST` | `/api/v1/external/{name}/versions`    | `external.read`  | List historical versions (KV backends that support it).              |
| `POST` | `/api/v1/external/{name}/version`     | `external.read`  | Read a specific version of an entry.                                 |
| `GET`  | `/api/v1/external/{name}/search`      | `external.read`  | Search within the resource.                                          |

## Users, permissions, tokens

| Method                         | Path                                  | Capability             | Purpose                                                              |
| ------------------------------ | ------------------------------------- | ---------------------- | -------------------------------------------------------------------- |
| `GET`,`POST`,`PATCH`,`DELETE`  | `/api/v1/users[/*]`                   | `users.manage`         | User CRUD.                                                            |
| `POST`                         | `/api/v1/users-kick/*`                | `users.manage`         | Force-logout a user (invalidates their sessions).                    |
| `DELETE`                       | `/api/v1/users-totp/*`                | `users.manage`         | Admin reset of a user's TOTP enrolment.                               |
| `GET`,`POST`,`PATCH`,`DELETE`  | `/api/v1/permissions[/*]`             | `permissions.manage`   | Permission bundle CRUD.                                              |
| `GET`,`PUT`                    | `/api/v1/user-permissions/*`          | `permissions.manage`   | Read / replace a user's bundle assignments.                          |
| `GET`,`POST`,`DELETE`,`PATCH`  | `/api/v1/tokens[/*]`                  | `tokens.manage`        | API token CRUD.                                                       |

## Server administration

| Method        | Path                                          | Capability        | Purpose                                                       |
| ------------- | --------------------------------------------- | ----------------- | ------------------------------------------------------------- |
| `GET`,`POST`  | `/api/v1/settings`                            | `settings.manage` | Read / patch the entire settings document.                    |
| `POST`        | `/api/v1/key/initialize`                      | `settings.manage` | Opt in to at-rest encryption (first-time setup).              |
| `POST`        | `/api/v1/key/unlock`                          | `settings.manage` | Unlock the server after a restart by supplying the master key. |
| `POST`        | `/api/v1/key/lock`                            | `settings.manage` | Manually re-lock the server without restarting.               |
| `POST`        | `/api/v1/key/rotate`                          | `settings.manage` | Rotate the server encryption key (current → new).             |
| `GET`         | `/api/v1/tls/status`                          | `settings.manage` | Inspect active HTTPS policy and certificate expiry.           |
| `POST`        | `/api/v1/tls/self-signed`                     | `settings.manage` | Generate and activate a managed self-signed certificate.      |
| `PUT`         | `/api/v1/tls/manual`                          | `settings.manage` | Upload and activate a PEM certificate / key pair.             |
| `POST`        | `/api/v1/tls-generate`                        | `settings.manage` | Generate a TLS certificate / key pair without activating it.  |
| `POST`        | `/api/v1/ssh-keygen`                          | `settings.manage` | Generate an SSH key pair (used by external resources that require key-based auth, e.g. Git over SSH). |
| `GET`         | `/api/v1/cluster/status`                      | `settings.manage` | Runtime cluster role, quorum, leader, visible alan peers.     |
| `GET`         | `/api/v1/public-endpoints/status`             | `settings.manage` | Runtime state of each configured Endpoint listener.           |
| `POST`        | `/api/v1/public-endpoints/test-rules`         | `settings.manage` | Dry-run draft Endpoint request rules and return a trace.      |
| `POST`        | `/api/v1/public-endpoints/{id}/test`          | `settings.manage` | Synthetic probe against a saved Endpoint.                     |
| `GET`,`POST`  | `/api/v1/backup[/info]`                       | `settings.manage` | Export / inspect / import a full backup archive.              |

## Per-user endpoints

Reserved under `/api/v1/me/*`. Every authenticated user can read and modify their own resources; no extra capability is required.

| Group               | Routes                                                                                                                              |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| **Preferences**     | `GET / PUT / DELETE /api/v1/me/preferences`                                                                                         |
| **Passkeys**        | `POST /me/passkeys/begin`, `/finish`; `GET /me/passkeys`; `PATCH / DELETE /me/passkeys/*`                                           |
| **TOTP**            | `GET /me/totp`; `POST /me/totp/begin`, `/finish`, `/recovery-codes`; `DELETE /me/totp`                                              |
| **Personal vault**  | `GET /me/vault/status`, `/account`; `POST /me/vault/setup`, `/unlock-check`, `/rotate-password`, `/recovery-kit`; `PUT /me/vault/session-lock`; `DELETE /me/vault` |
| **Vault items**     | `GET / POST /me/vault/items`; `GET / PUT / DELETE /me/vault/items/*`; `POST /me/vault/items-restore/*`, `/items-use/*`; `GET /me/vault/items-versions/*` |

## Authentication endpoints

Provided by the [ada](https://rakunlabs.github.io/ada/) auth manager — paths are stable but the strategies behind them are configured at runtime under **Settings → Authentication**.

| Path                  | Purpose                                                                                  |
| --------------------- | ---------------------------------------------------------------------------------------- |
| `GET  /login/info`    | Public — discover enabled strategies and login UI metadata. Allowed while locked.        |
| `GET  /login/me`      | Current session — username, capabilities, MFA state. Returns 401 when not signed in.     |
| `POST /login/pass/{strategy}` | Password-style strategies such as `local`. Rate-limited by `login_guard`.|
| `GET  /login/{strategy}` | OAuth2 / OIDC redirect start.                                                         |
| `POST /login/register/{strategy}` | Self-registration (when enabled by the strategy).                              |
| `POST /logout`        | Invalidates the current session.                                                         |

## Conventions

- All write endpoints accept JSON request bodies and return JSON responses.
- 4xx responses include a `{ "message": "..." }` body.
- Long-running operations (search, backup export) stream over Server-Sent Events.
- When the server is locked, all endpoints except the discovery group and `/login/*` / `/api/v1/key/{status,unlock}` return `503 Service Unavailable` with `X-Pika-Locked: true`. See [Encryption](./encryption).
