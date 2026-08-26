# Authentication

Pika has two distinct authentication mechanisms:

1. **Sessions** for human users in the web UI (cookie-based).
2. **API tokens** for programmatic consumers reading `/data/*` and calling `/api/v1/*`.

In addition, pika can plug external identity providers in front of the session login: OAuth2/OIDC or a header-based forward-auth gateway.

## Built-in users

Built-in session-based auth is always on. The first time you start the server, the UI shows a setup screen to create the initial admin account. After that, sign in and create more users from **Settings → Users**.

Each user belongs to one or more **permission bundles** that define which capabilities they hold. Bundles are managed under **Settings → Permissions**.

## Capabilities

Users (and externally-authenticated identities) are checked against pika's capability keys (`internal/service/capabilities.go` is the source of truth):

| Key                  | Grants                                                                       |
| -------------------- | ---------------------------------------------------------------------------- |
| `files.read`         | View folders, files, versions, variants, render and search configurations.    |
| `files.write`        | Create, update and delete folders and configuration files.                    |
| `external.read`      | Browse, search and read entries from configured external resources (Vault, Consul, etcd, AWS, Azure, GCP, Kubernetes, HTTP, ...). |
| `external.write`     | Create, update and delete entries on configured external resources.           |
| `settings.manage`    | View and modify server settings, backup/restore, server encryption-key lifecycle. |
| `tokens.manage`      | Create, edit, revoke API access tokens.                                      |
| `users.manage`       | Create, edit, delete, kick users (built-in auth only).                       |
| `permissions.manage` | Define permission bundles and assign them (built-in auth only).              |

A bundle can also have an associated **path pattern** that scopes the file/external permissions. For example, a bundle with `files.read` and pattern `team-a/**` lets the user read everything under `team-a/` but nothing else.

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
  https://localhost:8080/data/myapp/config
```

Token scope syntax and matching rules are documented in detail under [Tokens & scopes](/guide/tokens-and-scopes).

## External authentication

Pika supports two external strategies, both configurable from **Settings → Authentication** at runtime:

| Strategy        | What it does                                                                                                      |
| --------------- | ----------------------------------------------------------------------------------------------------------------- |
| **OAuth2 / OIDC** | Standard authorization-code flow with any compatible provider (Keycloak, Auth0, Okta, Azure AD, Google, …).       |
| **Forward-auth** | Trust headers set by an upstream gateway (Turna, Authelia, Authentik, oauth2-proxy, …). Pika sees `X-User`, etc. |

When any external strategy is enabled, pika uses a **session-first** approach:

1. If the request has a valid local session cookie, the user is authenticated immediately.
2. If no session cookie is present, pika delegates to the configured external strategy.
3. If neither succeeds, the request gets a 401.

Local login always works — even when external auth is on. The `/api/v1/info` endpoint is always public so the SPA can boot and show the login screen regardless of the external strategy's redirect behaviour.

For OAuth2/OIDC providers, configure the provider's Authorization URL and Token URL explicitly. Configure UserInfo URL when the provider exposes one; pika then reads identity claims from that endpoint with the upstream access token. Without UserInfo URL, pika falls back to token claims. The upstream access token is only used for that check and is revoked best-effort afterwards. pika then issues its own session token.

OAuth2 providers are fail-closed by default: an incoming `(provider, subject)` must already be linked to a pika user, or match an existing verified email when email auto-linking is enabled. Enable **Auto-create users** on a provider if unknown identities should create external-only pika users at first login. Username selection for those users is `preferred_username`, then email local-part, then a subject-based fallback.

::: tip
Strategies are hot-swapped via an [ada Slot](https://rakunlabs.github.io/ada/guide/runtime-reload.html). Toggling them on/off or changing the OIDC client secret does not require a restart.
:::

## External permissions

Externally-authenticated users can be linked to local `users` rows by verified email auto-linking or OAuth2 auto-create. If no row is linked, they have **no enforced permission checks** beyond declarative role/scope mappings and the superadmin allowlist.

Enable enforcement under **Settings → Authentication → Permissions**, then map external groups (or OAuth2 scopes) to pika capabilities:

```text
pika-editor  →  files.read, files.write, external.read
auditors     →  files.read, external.read, tokens.manage
```

If your gateway emits `X-Groups: pika-editor,auditors` for a user, the user gets the **union** of both sets. Unknown groups are ignored. Users not in the Superadmins allowlist and without any matching group are denied any restricted action (403).

The header name and value separator are configurable. Pika also accepts repeated header lines as a single concatenated list. The same role/scope mapping applies to OAuth2 token claims.
