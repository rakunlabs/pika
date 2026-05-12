# etcd

Read keys from an etcd cluster.

## Configuration

Under **Settings → External Resources → Add Resource → etcd**:

```text
Type    : etcd
Address : etcd.example.com:2379
Username: (optional)
Password: (optional)
```

- **Address** — etcd gRPC endpoint(s); comma-separate multiple endpoints for failover.
- **Username** / **Password** — used when etcd is configured with authentication.

## Inheritance entry

`path` is the etcd key.

```json
{
  "resource": "etcd",
  "path": "/myapp/config",
  "inject": "settings"
}
```

The value at the key is parsed as JSON / YAML / TOML and merged into the resolved config.

See [Inheritance](./) for the full meaning of `paths` / `inject`.
