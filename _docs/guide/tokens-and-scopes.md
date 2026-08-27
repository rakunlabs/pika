# Tokens & scopes

API tokens authenticate non-human consumers against `/data/*` (resolved configs), the configuration endpoints of the admin API (`/api/v1/*`), and the [MCP endpoint](./mcp). A token carries one thing: a list of **scopes**, each pairing a path glob with a set of operations.

## Token format

```
pika_<64-hex-characters>
```

Tokens are minted under **Settings > Access Tokens**. They're shown once at creation time and stored hashed afterwards — copy them immediately.

![Access token form with a read-only scope](/screenshots/pika-token-scopes.png)

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

| Operation | Grants                                                                                          |
| --------- | ----------------------------------------------------------------------------------------------- |
| `read`    | `GET /data/...`, and reading configurations through `/api/v1/*` and MCP.                         |
| `write`   | Creating and updating configurations through `/api/v1/file/...`, `/api/v1/folder/...` and MCP.   |
| `delete`  | Deleting configurations and folders.                                                            |
| `*`       | All of the above.                                                                               |

A token without any matching `read` scope gets `403 Forbidden` from `/data/*`.

The data plane itself is read-only — there is no `POST /data/...` — so `write` and `delete` only ever take effect on the admin API and MCP.

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

Use sparingly — `**` + `*` is a master key for every configuration in the server.

## Tokens on the admin API

Users are authorized by named [capabilities](/guide/authentication#capabilities); tokens are authorized by scopes. When a token calls `/api/v1/*`, pika projects its scopes onto the capability vocabulary the routes check:

| Token operation | Derived capability | Restricted to             |
| --------------- | ------------------ | ------------------------- |
| `read`          | `files.read`       | the paths granting `read` |
| `write`         | `files.write`      | the paths granting `write` |
| `delete`        | `files.write`      | the paths granting `delete` |

So a token scoped `{ "path": "team-a/**", "operations": ["read"] }` can list and read configurations under `team-a/`, and nothing else.

::: warning Tokens cannot administer the server
The derivation stops at `files.*`. There is no scope that yields `settings.manage`, `tokens.manage`, `users.manage`, `permissions.manage`, `external.read` or `external.write`, so those endpoints return `403` for every token regardless of its scopes. Server administration, external secret backends and token management require a logged-in user.

This is the ceiling on a leaked token: configuration data within its paths, never the server itself.
:::

::: info Superadmin
Superadmin is a user attribute, not a token attribute. A superadmin user holds every capability implicitly; the forward-auth / OAuth2 **Superadmins** allowlist promotes matching identities to the same status. A token named after a superadmin gets nothing from that — token identities are resolved from their scopes before the allowlist is ever consulted.
:::

## Rotation and revocation

- **Rotation** — there's no "rotate" action: mint a new token, switch consumers over, delete the old one.
- **Revocation** — delete the token under **Settings → Tokens** or via `DELETE /api/v1/tokens/{id}`. The change is effective immediately on the local node and propagates to the rest of the cluster within `cluster.sync_interval`.

## Audit

Token use shows up in pika's structured logs under `auth.token=<token-id>`. Combine with your existing log pipeline for an audit trail.
