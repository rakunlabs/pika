# Admin API

The admin API lives under `/api/v1/*` and is consumed by the web UI. You can also call it programmatically — either with a session cookie (after logging in) or with an API token that holds the right capability.

This page is a high-level map. For the consumer-facing read endpoint, see [Consuming data](./consuming-data).

## Authentication

Two equivalent options:

- **Session cookie** — set automatically after you log in to the UI. Useful when scripting against your own browser session.
- **Bearer token** — `Authorization: Bearer pika_...`. The token must have a capability that covers the requested operation.

::: tip
Token capabilities for admin operations are different from path scopes used on `/data/*`. A token holds a list of capabilities (`files.read`, `settings.manage`, …). The list is set when the token is created.
:::

## Endpoints

| Method                  | Path                                  | Capability             | Purpose                                                              |
| ----------------------- | ------------------------------------- | ---------------------- | -------------------------------------------------------------------- |
| `GET`                   | `/api/v1/info`                        | _(public)_             | Server metadata, current user, capabilities, raw mounts.             |
| `GET`                   | `/healthz`                            | _(public)_             | Health probe.                                                        |
| `GET`,`POST`,`PATCH`,`DELETE` | `/api/v1/users[/...]`             | `users.manage`         | User CRUD.                                                            |
| `POST`                  | `/api/v1/users-kick/{username}`       | `users.manage`         | Force-logout a user (invalidates their sessions).                    |
| `GET`,`POST`,`PATCH`,`DELETE` | `/api/v1/permissions[/...]`       | `permissions.manage`   | Permission bundle CRUD.                                              |
| `GET`,`PUT`             | `/api/v1/user-permissions/{username}` | `permissions.manage`   | Assign permission bundles to a user.                                 |
| `GET`,`POST`,`DELETE`   | `/api/v1/folder[/...]`                | `files.read`/`write`   | Folder navigation, create, delete.                                   |
| `GET`,`POST`,`DELETE`   | `/api/v1/file/{path}`                 | `files.read`/`write`   | Config file CRUD. Supports `?variant=` and `?version=`.              |
| `GET`,`PATCH`           | `/api/v1/versions/{path}[/{n}]`       | `files.read`/`write`   | List versions; set / clear semver constraint on a specific version.  |
| `GET`                   | `/api/v1/variants/{path}`             | `files.read`           | List variants of a file.                                             |
| `POST`                  | `/api/v1/render/{path}`               | `files.read`           | Resolve inheritance / variants — returns the merged document.        |
| `GET`,`POST`,`DELETE`,`PATCH` | `/api/v1/tokens[/{id}]`           | `tokens.manage`        | API token CRUD.                                                       |
| `POST`                  | `/api/v1/convert`                     | `files.read`           | Convert content between JSON / YAML / TOML.                          |
| `GET`                   | `/api/v1/search?q=...`                | `files.read`           | Full-text search across configs (Server-Sent Events stream).         |
| `GET`                   | `/api/v1/key/status`                  | _(public)_             | Server encryption-key state (initialized / unlocked).                |
| `POST`                  | `/api/v1/key/initialize`              | `settings.manage`      | Opt in to at-rest encryption (first-time setup of the server key).   |
| `POST`                  | `/api/v1/key/unlock`                  | `settings.manage`      | Unlock the server after a restart by supplying the master key.       |
| `POST`                  | `/api/v1/key/lock`                    | `settings.manage`      | Manually re-lock the server without restarting.                      |
| `POST`                  | `/api/v1/key/rotate`                  | `settings.manage`      | Rotate the server encryption key (current → new).                    |
| `POST`                  | `/api/v1/tls-generate`                | `settings.manage`      | Generate a TLS certificate / key pair.                               |
| `POST`                  | `/api/v1/ssh-keygen`                  | `settings.manage`      | Generate an SSH key pair.                                            |
| `GET`,`POST`            | `/api/v1/settings`                    | `settings.manage`      | Read / patch the entire settings document.                           |
| `GET`                   | `/api/v1/cluster/status`              | `settings.manage`      | Runtime cluster role, quorum, leader and visible alan peers.         |
| `GET`,`POST`            | `/api/v1/backup[/info]`               | `settings.manage`      | Export / inspect / import a full backup archive.                     |
| `GET`,`PUT`,`DELETE`    | `/api/v1/raw/{prefix}/{path}`         | `raw.read`/`write`     | Raw FS browsing (UI, session-auth side).                             |
| `POST`                  | `/api/v1/raw-mkdir/{prefix}/{path}`   | `raw.write`            | Create a folder on a raw mount.                                      |
| `POST`                  | `/api/v1/raw-rename`                  | `raw.write`            | Rename a file on a raw mount.                                        |
| `POST`                  | `/api/v1/raw-copy`                    | `raw.write`            | Copy a file (possibly cross-mount).                                  |
| `POST`                  | `/api/v1/raw-move`                    | `raw.write`            | Move a file (possibly cross-mount).                                  |
| `GET`,`PUT`             | `/api/v1/registries`                  | `registry.read`/`registry.admin` | Read or replace the namespace/repository tree.                      |
| `GET`                   | `/api/v1/registries/{type}/{ns}/{repo}/stats` | `registry.read` | Registry storage/package statistics.                                |
| `POST`                  | `/api/v1/registries/{type}/{ns}/{repo}/purge` | `registry.admin` | Purge remote registry cache.                                        |
| `POST`                  | `/api/v1/registries/{type}/{ns}/{repo}/test-upstream` | `registry.admin` | Probe a remote registry upstream.                                  |
| `GET`                   | `/api/v1/external/{name}/paths`       | `settings.manage`      | Browse paths in a configured external resource (Vault, Consul, …).   |
| `GET`                   | `/api/v1/user-sync/status`            | `settings.manage`      | Last-run report for every configured user-sync source.               |
| `POST`                  | `/api/v1/user-sync/run/{id}`          | `settings.manage`      | Trigger a one-shot user-sync run.                                    |
| `POST`                  | `/api/v1/user-sync/test/{id}`         | `settings.manage`      | Dry-run a user-sync source — no writes performed.                    |

## Public consumer endpoints

These are reachable on both the admin port (with token) and the public port (without):

| Method         | Path                  | Notes                                     |
| -------------- | --------------------- | ----------------------------------------- |
| `GET`          | `/data/{path}`        | Resolved config — see [Consuming data](./consuming-data). |
| `GET`,`PUT`,`DELETE` | `/raw/{prefix}/{path}` | Raw filesystem browsing — see [Raw file serving](/guide/raw-files). |
| `GET`          | `/healthz`            | Health probe.                             |

These data-plane endpoints are reachable on the admin port and require a token or UI session unless you publish them through a proxy graph:

| Method         | Path                  | Notes                                     |
| -------------- | --------------------- | ----------------------------------------- |
| `GET`,`HEAD`,`POST`,`PUT`,`PATCH`,`DELETE` | `/registries/{namespace}/{repo}/...` | Package-manager registry traffic. See [Package registries & CDN](/guide/package-registries). |
| `GET`,`HEAD`,`OPTIONS` | `/cdn/npm/{namespace}/{repo}/{package[@version]}/{file...}` | Authenticated direct NPM package CDN reads. Prefer a Proxy CDN listener for public CDN paths. |

## Conventions

- All write endpoints accept JSON request bodies and return JSON responses.
- 4xx responses include a `{ "error": "..." }` body.
- Long-running operations (search, backup) stream over Server-Sent Events.

## Discovering endpoints

`GET /api/v1/info` returns a description of the running server: version, configured features, the current user's capabilities, and the list of raw mounts. The web UI uses it on every load to decide which sections to render — you can use the same response to feature-detect when scripting.
