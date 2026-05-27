# Endpoints

Pika lets an operator open additional HTTP ports that expose configuration data either directly, from an External resource, or in a shape other tools already understand — so existing clients (consul-template, spring-cloud-config consumers, custom apps) can keep talking to pika without changing their code.

Four modes are supported today:

| Mode     | What it does                                                                                  |
| -------- | --------------------------------------------------------------------------------------------- |
| `static` | Direct `/data/{key}` style config response. No Go template and no compatibility envelope.      |
| `consul` | Read-only Consul KV compatibility shim. Mounts `{base_path}/v1/kv/{key}`.                     |
| `external` | Direct read from one configured External resource. `{base_path}/{path}` maps to provider path. |
| `custom` | User-authored Go-template response modifier. Pika resolves the config, you shape the answer. |

Each endpoint owns its own TCP listener (`host:port`) and its own auth setting. Listeners are reconciled live: every save in the **Settings → Endpoints** panel diffs the desired list against what's running and starts/stops sockets accordingly.

## Request pipeline

Each request flows through three configurable stages, in order:

```
client -> [1. Auth] -> [2. Request rules] -> [3. Mode shim] -> response
```

1. **Auth** — `none`, `bearer_token`, or `static_token` (see below). Failed auth never reaches stage 2 or 3.
2. **Request rules** — optional ordered list of declarative rules that can `allow`, `block`, or modify the request (set/del header, set/del query, rewrite path) before it hits the shim.
3. **Mode shim** — direct config response, External resource read, Consul KV shim, or custom Go-template response. Sees the post-modify request.

## Configuration

Open **Settings → Endpoints**. Click **+ New endpoint** and fill in:

| Field           | Required | Notes                                                                 |
| --------------- | -------- | --------------------------------------------------------------------- |
| Name            | yes      | Operator label. No uniqueness constraint.                             |
| Enabled         | yes      | Off means the configuration is kept but no socket is bound.           |
| Listen host     | yes      | Default `0.0.0.0`. Use `127.0.0.1` to expose only on loopback.        |
| Listen port     | yes      | Any free TCP port. Must not collide with the main pika port.          |
| Base path       | yes      | URL prefix. Default `/`. Must start with `/` and have no trailing `/`.|
| Mode            | yes      | `static`, `consul`, `external`, or `custom`.                           |
| Auth            | yes      | `none`, `bearer_token`, or `static_token`. See below.                  |

Configurations are persisted in the singleton settings document; static tokens are encrypted at rest by the same envelope that seals other secrets in Pika.

## Authentication

| Mode           | Behaviour                                                                                                                                   |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| `none`         | No authentication. The listener serves anyone who can reach it.                                                                              |
| `bearer_token` | Requires `Authorization: Bearer <token>` matching any pika API token with the `files.read` capability. Same tokens used by `/data/*`.      |
| `static_token` | Compares the configured header (default `Authorization`, also accepts `X-Consul-Token` etc.) in constant time against a small list of tokens. |

Static tokens are sealed at rest — when you re-open an endpoint in the UI the field comes back empty; leaving it empty preserves the previously-stored tokens, an explicit empty list clears them.

> **Operator tip.** `none` is the simplest mode and is fine on a private network or behind a fronting reverse proxy that does its own auth. Treat it as "this listener is publicly readable" and design the rest of your network controls accordingly.

## Request rules

The request-rules stage runs after authentication and before the mode shim. It is an ordered list of declarative rules — no templates, no JSON to hand-author. The editor in **Settings → Endpoints** lets you add rows, pick a matcher and an action from dropdowns, and reorder with ↑/↓ buttons.

### Evaluation

Rules are walked top-to-bottom for every request:

- A rule **matches** when every populated entry in its `when` block matches the current request (AND semantics). An empty `when` matches every request.
- `allow` and `block` are **terminal** — they short-circuit evaluation. `block` writes the configured response; `allow` forwards to the shim immediately.
- `set_header`, `del_header`, `set_query`, `del_query`, and `set_path` are **modify** actions — they mutate the request and let evaluation continue with the next rule. The shim sees the post-modify request.
- If no rule terminates, the request falls through to the shim (implicit default-allow). Same convention as a firewall whose tail is an implicit ACCEPT.

A rule with `enabled: false` is skipped entirely, but kept in the list so an operator can flip it on/off without losing the configuration.

### Matchers (`when`)

Pick one matcher per rule. Need two matchers AND-ed? Add two rules — one modify, one terminal — or split into rule chains via stacked modifies.

| Matcher          | Fires when …                                                       |
| ---------------- | ------------------------------------------------------------------ |
| `any request`    | Always. Useful as a default-deny tail (`when=any, then=block`).     |
| `method`         | Request method equals the configured value (case insensitive).      |
| `path equals`    | URL path equals the configured string exactly.                      |
| `path prefix`    | URL path starts with the configured string.                         |
| `header equals`  | The named header's first value equals the configured value.        |
| `header present` | The named header is set and has a non-empty value.                 |
| `header absent`  | The named header is missing or only has empty values.              |
| `query equals`   | The named query parameter's first value equals the configured value.|
| `query present`  | The named query parameter is set and non-empty.                    |
| `query absent`   | The named query parameter is missing or empty.                     |

Header names are case-insensitive in the matcher (canonical form is used internally), so `X-Tenant` and `x-tenant` behave the same. Header values are case-sensitive.

### Actions (`then`)

| Action       | Effect                                                                                                 |
| ------------ | ------------------------------------------------------------------------------------------------------ |
| `allow`      | Forward to the shim immediately. Stops evaluation.                                                    |
| `block`      | Reply with the configured status (default 403), body, and content-type. Stops evaluation.             |
| `set_header` | Set a request header (overwrites existing values). Continues evaluation.                              |
| `del_header` | Remove a request header. Continues evaluation.                                                        |
| `set_query`  | Set a query parameter. Continues evaluation.                                                          |
| `del_query`  | Remove a query parameter. Continues evaluation.                                                       |
| `set_path`   | Replace the whole URL path with a literal value. Must start with `/`. Not regex/capture based. Continues evaluation. |
| `replace_path` | Regex-replace the URL path. Uses Go regexp syntax and `$1` / `$name` capture replacements. Continues evaluation. |

`set_path` is intentionally simple: it does not run a regex and it does not preserve an unmatched suffix. If a rule matches `/legacy/app`, then `set_path = /myapp/config` means the shim receives exactly `/myapp/config`.

Use `replace_path` when you want capture groups. The regex runs against the whole current path, and the replacement becomes the new path. If the replacement does not start with `/`, Pika prefixes it.

### Example recipes

**Require a tenant header on every request**

| # | enabled | when                       | then                                       |
| - | ------- | -------------------------- | ------------------------------------------ |
| 1 | yes     | header absent: `X-Tenant`  | block — status 401, body "missing tenant"  |

**Force every request onto the prod variant**

| # | enabled | when | then                                  |
| - | ------- | ---- | ------------------------------------- |
| 1 | yes     | any  | set_query — `variant` = `prod`        |

**Rewrite a legacy path**

| # | enabled | when                              | then                                           |
| - | ------- | --------------------------------- | ---------------------------------------------- |
| 1 | yes     | path equals: `/v1/kv/legacy`      | set_path — `/v1/kv/myapp/config`               |

This is a full replacement: a request for `/v1/kv/legacy` reaches the shim as `/v1/kv/myapp/config`. It is not `/v1/kv/<regex>` rewriting; dynamic capture groups are not supported by this action.

**Regex rewrite a legacy prefix**

| # | enabled | when                         | then                                                                  |
| - | ------- | ---------------------------- | --------------------------------------------------------------------- |
| 1 | yes     | path starts with: `/legacy/` | replace_path — pattern `^/legacy/(.*)$`, replacement `/$1`             |

This keeps the suffix dynamically. A request for `/legacy/myapp/config` reaches the shim as `/myapp/config`. In static config-data mode with `base_path=/`, that resolves the same key as `/data/myapp/config`.

**Admin override + default-deny**

| # | enabled | when                              | then              |
| - | ------- | --------------------------------- | ----------------- |
| 1 | yes     | header equals: `X-Admin` = `yes`  | allow             |
| 2 | yes     | any                               | block — status 403 |

Rule 1 lets admin requests through; rule 2 catches everything else.

## Mode: Static config data

Static mode is the plain `/data/{key}` equivalent for Endpoints. The path tail after the endpoint's base path is resolved as a pika config key, then the resolved bytes are written directly with the same content-type and format conversion rules as `/data/*`. It does not run a Go template and does not wrap the response in a Consul envelope.

```
GET {base_path}/{key} [?variant=] [?version=] [?format=]
```

For example, with `base_path=/cfg`, this request:

```sh
curl http://localhost:9090/cfg/myapp/config
```

behaves like:

```sh
curl http://localhost:8080/data/myapp/config
```

### Query parameters

| Parameter | Description                                                     |
| --------- | --------------------------------------------------------------- |
| `variant` | Pika variant name (e.g. `prod`, `staging`).                     |
| `version` | Integer or semver, same semantics as `/data/*`.                 |
| `format`  | Convert output to a different format (`json`, `yaml`, `toml`). |

### Response

The body is the resolved config file bytes. `Content-Type` is chosen from the resolved or converted format:

| Format | `Content-Type`             |
| ------ | -------------------------- |
| JSON   | `application/json`         |
| YAML   | `application/x-yaml`       |
| TOML   | `application/toml`         |
| Other  | `application/octet-stream` |

### Status codes

| Code | Meaning                                                 |
| ---- | ------------------------------------------------------- |
| 200  | OK.                                                    |
| 400  | Bad request — invalid format conversion or config error.|
| 401  | Auth missing or invalid (when auth mode is not `none`). |
| 404  | Key (or variant / version) not found.                   |
| 405  | Non-GET request.                                        |

Request rules still run before the static response. For example, you can require `X-Tenant`, force `?variant=prod`, or rewrite a legacy path before the config key is resolved.

## Mode: External resource

External mode reads directly from one configured External resource. It does not go through `/data/*`, config inheritance, or file rendering. The path tail after the endpoint's base path is passed to the selected provider as its resource-specific path.

```
GET {base_path}/{path} [?version=]
```

For example, with `base_path=/vault` and `resource=prod-vault`:

```sh
curl http://localhost:9090/vault/apps/api/db
```

behaves like an admin External read of resource `prod-vault` at path `apps/api/db`.

### Resource selection

Pick the External resource in **Settings → Endpoints → Mode → External resource**. The dropdown lists the resources already configured in **Settings → External resources**.

### Response

The endpoint returns the provider entry's raw bytes and content-type when available. For structured providers such as Vault, Kubernetes, Consul, etcd, AWS, GCP, and Azure, that is typically JSON. For HTTP external resources, it is the HTTP response body returned by the configured upstream.

### Query parameters

| Parameter | Description                                                                 |
| --------- | --------------------------------------------------------------------------- |
| `version` | Read a provider-specific historical version when the backend supports it.   |

If the selected backend does not support versions, `?version=` returns a bad request instead of falling back silently.

Request rules still run first, so you can rewrite `/legacy/(.*)` to `/$1`, force a tenant header, or block disallowed paths before the external provider sees the path.

## Mode: Consul KV

```
GET {base_path}/v1/kv/{key} [?raw] [?variant=] [?version=] [?format=]
```

`{key}` is a pika config path (e.g. `myapp/config`). The shim also accepts the bare `/v1/kv/...` prefix in addition to the configured base path so clients that don't include the prefix still work.

### Query parameters

| Parameter | Description                                                            |
| --------- | ---------------------------------------------------------------------- |
| `raw`     | Return raw bytes instead of the Consul JSON envelope.                  |
| `variant` | Pika variant name (e.g. `prod`, `staging`).                            |
| `version` | Integer or semver, same semantics as `/data/*`.                        |
| `format`  | Convert output to a different format (`json`, `yaml`, `toml`).        |

### Default response

A JSON array with one Consul-shaped entry. `Value` is base64-encoded, matching Consul's behaviour:

```json
[
  {
    "CreateIndex": 0,
    "ModifyIndex": 0,
    "LockIndex": 0,
    "Key": "myapp/config",
    "Flags": 0,
    "Value": "ZGF0YWJhc2U6CiAgaG9zdDogbG9jYWxob3N0Cg==",
    "Session": ""
  }
]
```

### `?raw` response

Returns the raw config bytes with the appropriate `Content-Type`:

| Format       | `Content-Type`             |
| ------------ | -------------------------- |
| JSON         | `application/json`         |
| YAML         | `application/x-yaml`       |
| TOML         | `application/toml`         |
| Other        | `application/octet-stream` |

```sh
# Raw bytes — looks just like Consul KV
curl http://localhost:9090/consul/v1/kv/myapp/config?raw

# Raw bytes, converted to JSON, prod variant
curl "http://localhost:9090/consul/v1/kv/myapp/config?raw&variant=prod&format=json"
```

### Status codes

| Code | Meaning                                                |
| ---- | ------------------------------------------------------ |
| 200  | OK.                                                    |
| 400  | Bad request — typically a malformed query parameter.   |
| 401  | Auth missing or invalid (when auth mode is not `none`).|
| 404  | Key (or variant / version) not found. Empty body.      |

The 404-with-empty-body behaviour matches Consul KV — a number of clients depend on it.

### Limitations

- **Read-only.** No `PUT` / `DELETE` / `/v1/txn`. Use `/api/v1/file/...` from the admin port for writes.
- **No watch / blocking queries.** `?wait=` / `?index=` are accepted but ignored. Long-poll consumers fall back to immediate responses.
- **No ACL endpoints.** Use the endpoint's own `auth` setting instead.

For most read-only library use (consul-template, frameworks that pull config from Consul KV at startup), this is enough.

## Mode: Custom Go template

Custom mode lets you author a Go `text/template` that produces the response body — useful when the wire shape isn't Consul KV (etcd, Spring Cloud Config, a custom in-house contract, etc.).

### Request shape

```
GET {base_path}/{key} [?raw] [?variant=] [?version=] [?format=]
```

The path tail after `base_path` becomes the template's `.Key` value. The query parameters above pass through to the template as `.Variant`, `.Version`, `.Raw`, `.Format`.

### Template variables

The template runs against a curated input map:

| Variable          | Type    | Description                                                  |
| ----------------- | ------- | ------------------------------------------------------------ |
| `.Key`            | string  | Path tail (the resolved pika key).                           |
| `.Variant`        | string  | Variant query param, or `""`.                                |
| `.Version`        | string  | Version query param, or `""`.                                |
| `.Raw`            | bool    | True when `?raw` is present.                                 |
| `.Format`         | string  | Requested format (after `?format=` override).                |
| `.Data`           | []byte  | Resolved configuration bytes.                                |
| `.DataString`     | string  | String view of `.Data`.                                      |
| `.DataB64`        | string  | Base64-encoded `.Data` — handy for Consul-style envelopes.  |
| `.Found`          | bool    | `false` when the underlying key was not found.               |
| `.ResolvedFormat` | string  | Format pika actually produced for `.Data`.                   |
| `.Now`            | time.Time | Current server time (UTC).                                 |

The funcmap is curated: `b64`, `b64dec`, `upper`, `lower`, `trim`, `join`, `split`, `rfc3339`, `unix`. Filesystem, environment, and command-execution helpers are **not** exposed.

### Response

The template output becomes the HTTP body. `Content-Type` and the status returned when the key is missing are configured per endpoint:

| Field                  | Default                       | Notes                                                       |
| ---------------------- | ----------------------------- | ----------------------------------------------------------- |
| `content_type`         | `application/octet-stream`    | Sent verbatim as the response `Content-Type`.               |
| `status_on_missing`    | `404`                         | HTTP status when `.Found` is false.                         |
| `allow_format_override`| `false`                       | When true, the client may pass `?format=` to convert data.  |

### Example: Consul KV envelope (verbatim of the built-in shim)

```text
[
  {
    "Key": "{{ .Key }}",
    "Value": "{{ .DataB64 }}",
    "CreateIndex": 0,
    "ModifyIndex": 0,
    "LockIndex": 0,
    "Flags": 0,
    "Session": ""
  }
]
```

### Example: Spring Cloud Config-style flat YAML

```text
{{ if .Found -}}
{{ .DataString }}
{{- else -}}
# config '{{ .Key }}' not found
{{- end }}
```

Set `content_type` to `application/x-yaml` and `status_on_missing` to `200` if the client expects a 200 with a marker comment instead of a 404.

### Limitations

- **Read-only.** GET only; non-GET requests return 405.
- **Template is stateless.** No sessions, no per-request storage. Use pika's variants/versions to model environment differences.
- **No streaming.** The whole body is rendered into memory before write. Large configs are fine; multi-gigabyte payloads are not.

## Diagnostics

The Settings → Endpoints panel shows a live status badge per entry (`running` / `bind failed` / `disabled` / `pending`) sourced from `GET /api/v1/public-endpoints/status`. A click on the **▶** button on any row opens a probe: it issues a synthetic GET through the same handler the live listener serves and returns status + headers + body. The probe panel includes a "Request headers" editor so you can supply auth tokens, tenant headers matched by your request rules, or anything else the live chain inspects — useful for validating a rule list end-to-end before pointing real clients at it.

```text
GET /api/v1/public-endpoints/status              # list every endpoint + runtime state
POST /api/v1/public-endpoints/{id}/test          # probe a single endpoint (admin auth)
```

The probe endpoint constructs the same handler chain (auth + request rules + shim) the live listener uses. If you've set `auth.mode = "bearer_token"` or `"static_token"`, add the same request header in the probe panel's **Request headers** editor, or pass it in the API body as `headers`.
