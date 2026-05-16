# Tokens & scopes

API tokens authenticate non-human consumers against `/data/*`, `/raw/*`, and the admin API. Each token has a list of **scopes** (for path-based read/write/delete) and an optional list of **capabilities** (for admin operations).

## Token format

```
pika_<64-hex-characters>
```

Tokens are minted under **Settings → Tokens**. They're shown once at creation time and stored hashed afterwards — copy them immediately.

Pass tokens via the `Authorization` header:

```sh
curl -H "Authorization: Bearer pika_..." http://localhost:8080/data/myapp/config
```

Pika does not accept `?token=` query parameters or `X-API-Key` headers.

## Scopes

A scope is `{ path, operations }`:

```json
{
  "path": "myapp/**",
  "operations": ["read", "write"]
}
```

A request is allowed if **any** scope on the token matches the requested path with the requested operation.

### Path matching

Scope paths use a small custom glob — segments are split on `/`:

| Pattern         | Matches                                | Doesn't match                    |
| --------------- | -------------------------------------- | -------------------------------- |
| `myapp/config`  | `myapp/config`                         | `myapp/config/sub`, `myapp/other` |
| `myapp/*`       | `myapp/foo`, `myapp/bar`               | `myapp/foo/bar`                  |
| `myapp/**`      | `myapp/a`, `myapp/a/b/c`               | `other/a`                        |
| `**` or `*` (alone) | everything                         | —                                |
| `raw/configs/**` | `raw/configs/app.json`, `raw/configs/team-a/x` | `raw/other/x`             |

The matcher is intentionally simpler than `filepath.Match` or doublestar — there are no character classes or single-char wildcards. Use `*` for "exactly one segment" and `**` for "zero or more segments".

::: tip
Scopes apply to both `/data/{path}` and `/raw/{prefix}/{path}`. To grant access to raw mounts, prefix the scope path with `raw/`.
:::

### Operations

| Operation | Endpoints                                                  |
| --------- | ---------------------------------------------------------- |
| `read`    | `GET /data/...`, `GET /raw/...`, `HEAD /raw/...`           |
| `write`   | `PUT /raw/...` (uploads on raw mounts that support it)     |
| `delete`  | `DELETE /raw/...`                                          |
| `*`       | All of the above                                           |

The config store itself (`/data/*`) is currently read-only over HTTP — writes go through `/api/v1/file/...` which uses **capabilities**, not scopes. So `write`/`delete` operations on a scope are only meaningful for raw mounts.

### Examples

#### Read-only access to a single app

```json
[
  { "path": "myapp/**", "operations": ["read"] }
]
```

#### Read-only across all apps + write on uploads

```json
[
  { "path": "**",                "operations": ["read"] },
  { "path": "raw/uploads/**",    "operations": ["read", "write"] }
]
```

#### Per-tenant isolation

Mint one token per tenant with a scope like:

```json
[
  { "path": "tenants/{tenant-id}/**",       "operations": ["read"] },
  { "path": "raw/tenant-files/{tenant-id}/**", "operations": ["read", "write", "delete"] }
]
```

#### Wide-open (admin-style) token

```json
[
  { "path": "**", "operations": ["*"] }
]
```

Use sparingly — a token with `**` and `*` is a master key for `/data/*` and `/raw/*`.

## Capabilities (admin operations)

Tokens used against the admin API (`/api/v1/...`) check **capabilities**, not scopes. Set them when creating the token. The keys are the same as for [user permissions](/guide/authentication#capabilities):

| Key                  | Grants                                                                       |
| -------------------- | ---------------------------------------------------------------------------- |
| `files.read`         | View configurations, versions, variants, render, search, convert.            |
| `files.write`        | Create, update, delete configurations and variants.                          |
| `raw.read`           | Browse and download raw mount contents.                                      |
| `raw.write`          | Upload, delete, rename, copy, move raw mount contents.                       |
| `settings.manage`    | View and modify server settings, backup/restore, server encryption-key lifecycle. |
| `tokens.manage`      | Create, edit, revoke API access tokens.                                      |
| `users.manage`       | Create, edit, delete, kick users (built-in auth only).                       |
| `permissions.manage` | Define permission bundles and assign them (built-in auth only).              |

A token with no capabilities can still consume `/data/*` and `/raw/*` (subject to its scopes); it just can't call any admin endpoints.

## Rotation and revocation

- **Rotation** — there's no "rotate" action: mint a new token, switch consumers over, delete the old one.
- **Revocation** — delete the token under **Settings → Tokens** or via `DELETE /api/v1/tokens/{id}`. The change is effective immediately on the local node and propagates to the rest of the cluster within `cluster.sync_interval`.

## Audit

Token use shows up in pika's structured logs under `auth.token=<token-id>`. Combine with your existing log pipeline for an audit trail.
