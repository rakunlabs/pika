# Tokens & scopes

API tokens authenticate non-human consumers against `/data/*` (resolved configs) and the admin API (`/api/v1/*`). Each token has a list of **scopes** (path-based read/write/delete on data plane) and an optional list of **capabilities** (named permissions for admin operations).

## Token format

```
pika_<64-hex-characters>
```

Tokens are minted under **Settings → Tokens**. They're shown once at creation time and stored hashed afterwards — copy them immediately.

Pass tokens via the `Authorization` header:

```sh
curl -H "Authorization: Bearer pika_..." https://localhost:8080/data/myapp/config
```

Pika does not accept `?token=` query parameters or `X-API-Key` headers.

## Scopes

A scope is `{ path, operations }`:

```json
{
  "path": "myapp/**",
  "operations": ["read"]
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

The matcher is intentionally simpler than `filepath.Match` or doublestar — there are no character classes or single-char wildcards. Use `*` for "exactly one segment" and `**` for "zero or more segments".

::: tip
Scopes apply to `/data/{path}` on the admin port and to `static` / `consul` / `custom` Endpoints when their auth is set to `bearer_token`. `external` Endpoints translate the URL into the underlying provider path before scope matching, so a scope of `myapp/**` covers `GET {endpoint}/myapp/...`.
:::

### Operations

| Operation | Effect on `/data/*`                              |
| --------- | ------------------------------------------------ |
| `read`    | `GET /data/...` succeeds.                        |
| `write`   | Reserved. The HTTP data plane is read-only — config writes go through `/api/v1/file/...` and are gated by **capabilities**, not scopes. |
| `delete`  | Reserved (same reason).                          |
| `*`       | All of the above.                                |

A token without any matching `read` scope gets `403 Forbidden` from `/data/*`.

### Examples

#### Read-only access to a single app

```json
[
  { "path": "myapp/**", "operations": ["read"] }
]
```

#### Read-only across all apps

```json
[
  { "path": "**", "operations": ["read"] }
]
```

#### Per-tenant isolation

Mint one token per tenant with a scope like:

```json
[
  { "path": "tenants/{tenant-id}/**", "operations": ["read"] }
]
```

#### Wide-open (admin-style) token

```json
[
  { "path": "**", "operations": ["*"] }
]
```

Use sparingly — `**` + `*` is a master key for `/data/*`.

## Capabilities (admin operations)

Tokens used against the admin API (`/api/v1/...`) check **capabilities**, not scopes. Set them when creating the token. The keys are the same as for [user permissions](/guide/authentication#capabilities) — `internal/service/capabilities.go` is the source of truth:

| Key                  | Grants                                                                       |
| -------------------- | ---------------------------------------------------------------------------- |
| `files.read`         | View folders, files, versions, variants, render and search configurations.    |
| `files.write`        | Create, update and delete folders and configuration files.                    |
| `external.read`      | Browse, search and read entries from configured external resources (Vault, Consul, etcd, AWS, Azure, GCP, Kubernetes, HTTP, ...). |
| `external.write`     | Create, update and delete entries on configured external resources.           |
| `settings.manage`    | View and modify server settings, run backup/restore, drive the server encryption-key lifecycle (initialize / unlock / lock / rotate). |
| `tokens.manage`      | Create, edit and revoke API access tokens.                                    |
| `users.manage`       | Create, edit, delete and kick users (built-in auth only).                     |
| `permissions.manage` | Define permission bundles and assign them to users.                           |

A token with no capabilities can still consume `/data/*` (subject to its scopes); it just can't call any admin endpoint.

::: info Superadmin
A "superadmin" user holds every key in the list above implicitly. The forward-auth / OAuth2 / LDAP **Superadmins** allowlist promotes matching identities to the same status. There is no separate `*` capability — superadmin is a user attribute, not a token attribute.
:::

## Rotation and revocation

- **Rotation** — there's no "rotate" action: mint a new token, switch consumers over, delete the old one.
- **Revocation** — delete the token under **Settings → Tokens** or via `DELETE /api/v1/tokens/{id}`. The change is effective immediately on the local node and propagates to the rest of the cluster within `cluster.sync_interval`.

## Audit

Token use shows up in pika's structured logs under `auth.token=<token-id>`. Combine with your existing log pipeline for an audit trail.
