# MCP server

Pika speaks the [Model Context Protocol](https://modelcontextprotocol.io), so AI agents (Claude Code, Claude Desktop, Cursor, VS Code, …) can browse, search and edit your configurations directly instead of you copy-pasting them into a chat.

There is nothing to install or run separately. The MCP server is part of the pika binary and is served from the running instance you already have.

```
POST /api/v1/mcp
```

The transport is **streamable HTTP**, the standard remote MCP transport.

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

The MCP endpoint sits on the same authenticated route group as the rest of `/api/v1/*` and enforces the same rules. It is not a side door.

**API token** — the normal case for an agent. A token carries [scopes](./tokens-and-scopes): path globs paired with operations (`read`, `write`, `delete`). Those scopes are enforced per tool call on the exact path being touched, identically to `/data/*` and to the admin API.

**Session cookie** — for a local agent pointed at your own logged-in browser session. Authorization then comes from your capabilities and path patterns.

If a request carries both, the token wins. A narrow token can never inherit a wider browser session.

What that buys you:

- **The tool list is filtered per request.** A token scoped `read` only never sees `set_config`; a token with `write` but not `delete` never sees `delete_config` or `delete_folder`. Agents plan against the tools they are shown, so they do not burn turns retrying calls they will be refused — and a write-only agent is never handed a delete button.
- **Folder listings are filtered too.** A token scoped to `team-b/**` sees `team-b` at the root and nothing else — not even the names of sibling folders. This is stricter than the REST folder endpoint, deliberately: a folder name like `customers/acme-corp` can itself be the sensitive part.
- **Search never leaks.** Out-of-scope hits are dropped silently rather than reported as forbidden, so a search cannot be used to probe for the existence of a path.
- **Writes are attributed.** Version history and [hook](./hooks) events record the token name (or the username for a session), exactly like a UI or REST write.
- **The lock gate applies.** While the server key is [locked](./server-key-management), the endpoint returns `503` like every other `/api/v1/` route.

::: warning Tokens cannot reach external resources
The external backends are gated on the `external.read` / `external.write` capabilities, and a token's [scopes only ever map onto `files.*`](./tokens-and-scopes#tokens-on-the-admin-api) — so the `*_external` tools are simply absent for a token caller. This mirrors the REST side, where `/api/v1/external/*` is unreachable with a token. To let an agent read Vault or AWS secrets you have to give it a session-backed identity holding those capabilities, and you should think hard before you do: those values land in the model's context.
:::

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

Session credentials only — see the warning above.

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
