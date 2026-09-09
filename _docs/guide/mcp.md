# MCP server

Pika speaks the [Model Context Protocol](https://modelcontextprotocol.io), so AI agents (Claude Code, Claude Desktop, Cursor, VS Code, …) can browse, search and edit your configurations directly instead of you copy-pasting them into a chat.

There is nothing to install or run separately. The MCP server is part of the pika binary and is served from the running instance you already have.

```
POST /api/v1/mcp
```

The transport is **streamable HTTP**, the standard remote MCP transport. The path above is the default; it can be changed in **Settings → MCP**.

## Endpoint and reverse-proxy mode

In **Settings → MCP**, choose an **Endpoint path**, such as `/mcp` or `/agents/mcp`. Pika adds `server.base_path` automatically. Reserved API, login and asset paths cannot be selected. Saving applies immediately, including on subsequent requests from existing clients; update the client URL when changing the path.

By default, Pika authenticates MCP callers with the same token/session mechanism as the API. If a reverse proxy handles authentication, enable **Disable Pika authentication for MCP**, then add explicit access scopes:

- **Source:** Pika configs or a configured external resource, such as `production-vault`.
- **Path pattern:** `team-a/**`, an exact key, or `**` for all paths in that source.
- **Operations:** `read`, `write`, and/or `delete`.

Every request in proxy mode uses these shared endpoint scopes, even if it includes a bearer token or session cookie. The tool preview shows which tools clients will see. A config-only scope grants no external access; an external-only scope grants no config access. Read-only grants hide write/delete tools entirely, and previously known tool names cannot bypass a revoked grant.

The proxy's **`X-User`** header is used only as an audit author (for example, in config version history and hooks). If absent or blank, the author is `mcp-proxy`. It never resolves a Pika user or grants that user's permissions. Have the authenticating proxy set this header to its verified username.

For example, the settings API accepts this full MCP configuration:

```json
{
  "action": "set",
  "mcp": {
    "endpoint": "/mcp",
    "auth_disabled": true,
    "scopes": [
      { "path": "apps/**", "operations": ["read"] },
      { "resource": "production-vault", "path": "team-a/**", "operations": ["read"] }
    ]
  }
}
```

Connect the client to the resulting URL without a Pika bearer header; supply whatever authentication your proxy requires. The server-key lock still blocks MCP, including custom endpoint paths.

## Connecting a client

Create an API token in **Settings → Access Tokens** scoped to what you want the agent to reach, then point your client at the endpoint.

### Claude Code

```sh
claude mcp add --transport http pika https://pika.example.com/api/v1/mcp \
  --header "Authorization: Bearer pika_abc123..."
```

### Client config file

Most other clients use a JSON config of this shape:

```json
{
  "mcpServers": {
    "pika": {
      "type": "http",
      "url": "https://pika.example.com/api/v1/mcp",
      "headers": {
        "Authorization": "Bearer pika_abc123..."
      }
    }
  }
}
```

::: tip
If your client cannot send custom headers, put a proxy in front that injects the `Authorization` header, or use pika behind a forward-auth gateway.
:::

## Permissions

By default, the MCP endpoint uses the same authentication and permission resolution as `/api/v1/*`. Proxy mode substitutes the explicit endpoint scopes described above.

**API token** — the normal case for an agent. A token carries [scopes](./tokens-and-scopes): path globs paired with operations (`read`, `write`, `delete`). Those scopes are enforced per tool call on the exact path being touched, identically to `/data/*` and to the admin API.

**Session cookie** — for a local agent pointed at your own logged-in browser session. Authorization then comes from your capabilities and path patterns.

With Pika authentication enabled, if a request carries both, the token wins. A narrow token can never inherit a wider browser session. Proxy mode always uses its configured endpoint scopes.

What that buys you:

- **The tool list is filtered per request.** A token scoped `read` only never sees `set_config`; a token with `write` but not `delete` never sees `delete_config` or `delete_folder`. Agents plan against the tools they are shown, so they do not burn turns retrying calls they will be refused — and a write-only agent is never handed a delete button.
- **Folder listings are filtered too.** A token scoped to `team-b/**` sees `team-b` at the root and nothing else — not even the names of sibling folders. This is stricter than the REST folder endpoint, deliberately: a folder name like `customers/acme-corp` can itself be the sensitive part.
- **Search never leaks.** Out-of-scope hits are dropped silently rather than reported as forbidden, so a search cannot be used to probe for the existence of a path.
- **Writes are attributed.** Version history and [hook](./hooks) events record the token name (or the username for a session), exactly like a UI or REST write.
- **The lock gate applies.** While the server key is [locked](./server-key-management), the endpoint returns `503` like every other `/api/v1/` route.

**External resources:** tokens and proxy-mode scopes can grant external access with an explicit `resource` name. These scopes map to `external.read` / `external.write`, while retaining the exact resource/path/operation pairing. Resource listings, path browsing, search, reads, version reads, writes and deletes are scoped; a write grant does not imply delete. Config scopes without `resource` never grant external access. External values returned by MCP become part of the model's context.

## Tools

### Configurations

| Tool                  | Token operation | Capability    | Purpose                                                                                            |
| --------------------- | --------------- | ------------- | -------------------------------------------------------------------------------------------------- |
| `search_configs`      | `read`          | `files.read`  | Find configs by path or by content across the whole tree.                                          |
| `list_folder`         | `read`          | `files.read`  | List subfolders and configs one level at a time.                                                   |
| `get_config`          | `read`          | `files.read`  | Read a config's stored source and metadata — the thing you edit.                                   |
| `get_resolved_config` | `read`          | `files.read`  | Read the effective value after inheritance, templating and format conversion — what an app receives. |
| `list_versions`       | `read`          | `files.read`  | Version history with author, timestamp and semver constraint.                                      |
| `list_variants`       | `read`          | `files.read`  | Variant keys defined for a config.                                                                 |
| `set_config`          | `write`         | `files.write` | Create a config or save a new version.                                                             |
| `delete_config`       | `delete`        | `files.write` | Delete a config, one version of it, or a variant.                                                  |
| `delete_folder`       | `delete`        | `files.write` | Delete a folder and everything under it.                                                           |

### External resources

Available to sessions with the listed capabilities, or to tokens/proxy scopes granting the corresponding operation on an explicit external resource. Listings and search omit inaccessible resources and paths.

| Tool                      | Capability       | Purpose                                                                |
| ------------------------- | ---------------- | ---------------------------------------------------------------------- |
| `list_external_resources` | `external.read`  | List configured backends and what each one supports.                   |
| `list_external_paths`     | `external.read`  | List entries under a prefix.                                           |
| `search_external`         | `external.read`  | Find entries by path or by stored value.                               |
| `read_external`           | `external.read`  | Read one entry, optionally at a historical version.                    |
| `write_external`          | `external.write` | Create or replace an entry.                                            |
| `delete_external`         | `external.write` | Delete an entry.                                                       |

## Behaviour worth knowing

**`get_config` vs `get_resolved_config`.** The first returns the source text as stored, including unrendered templates and an unmerged inheritance list. The second returns the final document a consuming application would get from `/data/*`. Agents are told to use the first for editing and the second for answering "what is service X actually running with".

**Writes replace content wholesale.** There is no partial patch. An agent editing a config reads it first and sends back the complete new text.

**Metadata is preserved.** `set_config` keeps the existing description, format, inheritance list and template flag unless the call explicitly overrides them, so an agent changing one value cannot silently drop a config's inheritance. Passing an empty `inherits` array is the way to clear it deliberately.

**Format is inferred.** For a new config, the format comes from the path extension (`.yaml`, `.json`, `.toml`); anything else is stored as `raw`.

**Concurrency.** `set_config` accepts `expected_version`. If the config has moved on since the agent read it, the write fails instead of clobbering the change.

**Search is capped.** `search_configs` and `search_external` default to 50 hits and cap at 200. Both walk unindexed, so narrow the query rather than raising the limit.

## Clustering

In a [clustered](./clustering) deployment MCP calls are `POST`, so they are forwarded to the leader like any other write. Reads served this way are always current; the cost is one extra hop on follower nodes.
