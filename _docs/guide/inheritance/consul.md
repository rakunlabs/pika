# Consul

Read keys from a [Consul](https://www.consul.io/) KV store.

## Configuration

Under **Settings → External Resources → Add Resource → Consul**:

```text
Type   : Consul
Address: https://consul.example.com:8500
Token  : (optional ACL token)
```

- **Address** — Consul HTTP API endpoint.
- **Token** — ACL token, required if Consul has ACLs enabled. Scope it to read-only on the keys pika will touch.

## Inheritance entry

`path` is the KV key.

```json
{
  "resource": "consul",
  "path": "myapp/config",
  "inject": "settings"
}
```

The value at `myapp/config` is parsed as JSON / YAML / TOML and merged under `settings` in the resolved config.

See [Inheritance](./) for the full meaning of `paths` / `inject`.
