# Consuming data

This page describes the read-only `/data/*` endpoint that applications use to fetch resolved configuration. For the full management API, see [Admin API](./api).

## Endpoint

```
GET /data/{path}
```

`{path}` is the full file path (e.g. `myapp/workers/config`). The response body is the resolved file content — base file + variant overrides + inheritance, all merged.

## Authentication

Pass the token via the `Authorization` header:

```sh
curl -H "Authorization: Bearer pika_abc123..." \
  https://localhost:8080/data/myapp/config
```

The token must have a scope that covers the requested path with the `read` operation. See [Tokens & scopes](./tokens-and-scopes).

::: tip Endpoints
For client tools that don't speak Bearer auth — or that already speak a different config protocol — open an **Endpoint** (Settings → Endpoints). Each endpoint binds its own `host:port` and serves pika's data in either Consul KV shape or a custom Go-template response, with an optional request-check stage in between. See [Endpoints](./compat).
:::

## Query parameters

| Parameter | Description                                       | Example                          |
| --------- | ------------------------------------------------- | -------------------------------- |
| `version` | Version selector — integer or semver constraint.  | `?version=3` or `?version=0.2.0` |
| `variant` | Variant name.                                     | `?variant=prod`                  |
| `format`  | Convert output to a different format on the fly.  | `?format=json`                   |

### Versions

```sh
# Latest version
curl -H "Authorization: Bearer $TOKEN" \
  https://localhost:8080/data/myapp/config

# Pinned to integer version
curl -H "Authorization: Bearer $TOKEN" \
  "https://localhost:8080/data/myapp/config?version=3"

# Pinned to semver — pika picks the latest version satisfying ?version=
curl -H "Authorization: Bearer $TOKEN" \
  "https://localhost:8080/data/myapp/config?version=0.2.0"
```

See [Versions & variants](/guide/versions-variants) for how semver constraints are resolved.

### Variants

```sh
# Base
curl -H "Authorization: Bearer $TOKEN" \
  https://localhost:8080/data/myapp/config

# Production variant
curl -H "Authorization: Bearer $TOKEN" \
  "https://localhost:8080/data/myapp/config?variant=prod"

# Combine variant + semver + format
curl -H "Authorization: Bearer $TOKEN" \
  "https://localhost:8080/data/myapp/config?variant=staging&version=0.3.0&format=json"
```

### Format conversion

By default the response is in the file's stored format. Override with `?format=`:

| Format | `Content-Type`             |
| ------ | -------------------------- |
| `json` | `application/json`         |
| `yaml` | `application/x-yaml`       |
| `toml` | `application/toml`         |
| other  | `application/octet-stream` |

## Status codes

| Code | Meaning                                                                         |
| ---- | ------------------------------------------------------------------------------- |
| 200  | OK — body contains the resolved config.                                         |
| 401  | No token / invalid token. Endpoints with `auth=none` skip this. |
| 403  | Token doesn't have a scope covering the path with `read`.                       |
| 404  | Path doesn't exist (or variant/version not found).                              |
| 502  | An external inheritance source failed.                                          |

## Caching

Pika does **not** add `Cache-Control` headers — caching is the consumer's responsibility. Most callers should fetch on startup and on a long interval (e.g. once a minute). For configs that change rarely, an `ETag` header is provided so you can issue conditional requests:

```sh
curl -I -H "Authorization: Bearer $TOKEN" \
  https://localhost:8080/data/myapp/config
# HTTP/1.1 200 OK
# ETag: "v3"

curl -H "Authorization: Bearer $TOKEN" \
  -H 'If-None-Match: "v3"' \
  https://localhost:8080/data/myapp/config
# HTTP/1.1 304 Not Modified
```

## Endpoints

Pika can also expose its data through operator-defined endpoints — either a Consul KV-shaped reader or a custom Go-template modifier, with an optional per-request inspection stage. Each endpoint binds its own `host:port`. See [Endpoints](./compat).
