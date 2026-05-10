# Authentication

Pika has two distinct authentication mechanisms:

1. **Sessions** for human users in the web UI (cookie-based).
2. **API tokens** for programmatic consumers reading `/data/*` and `/raw/*`.

In addition, pika can plug external identity providers in front of the session login: OAuth2/OIDC, LDAP, or a header-based forward-auth gateway.

## Built-in users

Built-in session-based auth is always on. The first time you start the server, the UI shows a setup screen to create the initial admin account. After that, sign in and create more users from **Settings → Users**.

Each user belongs to one or more **permission bundles** that define which capabilities they hold. Bundles are managed under **Settings → Permissions**.

## Capabilities

Users (and externally-authenticated identities) are checked against pika's capability keys:

| Key                  | Grants                                                                       |
| -------------------- | ---------------------------------------------------------------------------- |
| `files.read`         | View configurations, versions, variants, render, search, convert.            |
| `files.write`        | Create, update, delete configurations and variants.                          |
| `raw.read`           | Browse and download raw mount contents.                                      |
| `raw.write`          | Upload, delete, rename, copy, move raw mount contents.                       |
| `settings.manage`    | View and modify server settings, admin secret, backup/restore, key rotation. |
| `tokens.manage`      | Create, edit, revoke API access tokens.                                      |
| `users.manage`       | Create, edit, delete, kick users (built-in auth only).                       |
| `permissions.manage` | Define permission bundles and assign them (built-in auth only).              |

A bundle can also have an associated **path pattern** that scopes the file/raw permissions. For example, a bundle with `files.read` and pattern `team-a/**` lets the user read everything under `team-a/` but nothing else.

## API tokens

Tokens are the recommended way to authenticate non-human consumers. Mint them under **Settings → Tokens**.

A token has the format `pika_<64hex>` and one or more **scopes**. Each scope grants specific operations on a glob-matched path:

```json
{
  "path": "myapp/**",
  "operations": ["read", "write", "delete"]
}
```

Pass the token as a `Bearer` token:

```sh
curl -H "Authorization: Bearer pika_..." \
  http://localhost:8080/data/myapp/config
```

Token scope syntax and matching rules are documented in detail under [Tokens & scopes](/reference/tokens-and-scopes).

## External authentication

Pika supports three external strategies, all configurable from **Settings → Authentication** at runtime:

| Strategy        | What it does                                                                                                      |
| --------------- | ----------------------------------------------------------------------------------------------------------------- |
| **OAuth2 / OIDC** | Standard authorization-code flow with any compatible provider (Keycloak, Auth0, Okta, Azure AD, Google, …).       |
| **LDAP**        | Bind against an LDAP server. Optionally syncs users on a schedule.                                                |
| **Forward-auth** | Trust headers set by an upstream gateway (Turna, Authelia, Authentik, oauth2-proxy, …). Pika sees `X-User`, etc. |

When any external strategy is enabled, pika uses a **session-first** approach:

1. If the request has a valid local session cookie, the user is authenticated immediately.
2. If no session cookie is present, pika delegates to the configured external strategy.
3. If neither succeeds, the request gets a 401.

Local login always works — even when external auth is on. The `/api/v1/info` endpoint is always public so the SPA can boot and show the login screen regardless of the external strategy's redirect behaviour.

::: tip
Strategies are hot-swapped via an [ada Slot](https://rakunlabs.github.io/ada/guide/runtime-reload.html). Toggling them on/off, changing the OIDC client secret, or adjusting LDAP filters does not require a restart.
:::

## External permissions

Externally-authenticated users don't get rows in the local `users` table. By default they have **no enforced permission checks** — pika trusts the upstream IdP / gateway.

Enable enforcement under **Settings → Authentication → Permissions**, then map external groups (or OAuth2 scopes) to pika capabilities:

```text
pika-editor  →  files.read, files.write, raw.read
auditors     →  files.read, raw.read, tokens.manage
```

If your gateway emits `X-Groups: pika-editor,auditors` for a user, the user gets the **union** of both sets. Unknown groups are ignored. Users not in the Superadmins allowlist and without any matching group are denied any restricted action (403).

The header name and value separator are configurable. Pika also accepts repeated header lines as a single concatenated list. The same role/scope mapping applies to OAuth2 (token claims) and LDAP (group membership).

## User sync (LDAP)

For LDAP, pika can periodically reconcile local user metadata against the directory:

- Browse the configured `UserBaseDN` with `UserFilter`.
- For each entry, create-or-update a local user (idempotent).
- Apply group → permission mappings from the source's `GroupPermissions`.
- Reconcile missing users per the source's `OnMissing` policy (`disable` by default, or `ignore`).

User sync is configured under **Settings → Authentication → User sync**. You can also trigger a one-shot run via:

```sh
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/v1/user-sync/run/<source-id>
```

…and inspect the last result with `GET /api/v1/user-sync/status`.
