# Compatibility endpoints

Pika can expose its config store through endpoints shaped like other tools, so existing consumers don't need to change their code.

Today, only **Consul KV** is implemented. The framework is set up to add more shims (etcd, etc.) as needed.

## Consul KV

When enabled, pika serves the Consul KV read API at `/consul/v1/kv/*`. The base path is configurable.

### Enabling

Under **Settings → Compatibility → Consul KV**, set:

| Field        | Default      | Description                                                  |
| ------------ | ------------ | ------------------------------------------------------------ |
| `enabled`    | `false`      | Master switch.                                               |
| `base_path`  | `/consul`    | URL prefix. The actual route becomes `<base_path>/v1/kv/*`. |

The endpoint is mounted on the **public port** (so it's reachable without a Bearer token in trusted networks). It's not currently exposed on the authenticated admin port.

### Endpoint

```
GET /consul/v1/kv/{key}
```

`{key}` is a pika config path (e.g. `myapp/config`).

### Query parameters

| Parameter | Pika ext.? | Description                                                  |
| --------- | ---------- | ------------------------------------------------------------ |
| `raw`     | no         | Return raw bytes instead of the Consul JSON envelope.        |
| `variant` | yes        | Pika variant name.                                           |
| `version` | yes        | Integer or semver (same semantics as `/data/*`).             |
| `format`  | yes        | Convert output to a different format (`json`/`yaml`/`toml`). |

### Default response

A JSON array with one Consul-shaped entry. The `Value` is base64-encoded, matching Consul's behaviour:

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
| 404  | Key (or variant / version) not found. Empty body.      |

The 404-with-empty-body behaviour matches Consul KV — a number of clients depend on it.

### Limitations

- **Read-only.** The compat shim does not implement `PUT /v1/kv/...` or `DELETE /v1/kv/...`. Use `/api/v1/file/...` for writes.
- **No ACL token check.** The endpoint sits on the public port; if you need authentication, put a reverse proxy in front and require its own header.
- **No watch / blocking queries.** The `?wait=` and `?index=` query parameters are accepted but ignored. Long-poll consumers fall back to immediate responses.
- **No transactions.** `PUT /v1/txn` is not implemented.

For most read-only library use (e.g. consul-template, app frameworks that pull config from Consul KV at startup), this is enough.
