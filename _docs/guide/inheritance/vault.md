# Vault

Read KV secrets from [HashiCorp Vault](https://www.vaultproject.io/) and merge them into the resolved config.

## Configuration

Under **Settings → External Resources → Add Resource → Vault**:

```text
Type     : Vault
Address  : https://vault.example.com
Mount    : secret
Auth     : token   OR   AppRole
Token    : (when auth=token)
RoleID   : (when auth=AppRole)
SecretID : (when auth=AppRole)
```

- **Address** — the Vault server URL.
- **Mount** — the KV mount to read from (typically `secret`).
- **Auth** — `token` for a static Vault token, or `AppRole` for a `RoleID` + `SecretID` pair. AppRole is the recommended production setup because it rotates cleanly.

Both KV v1 and KV v2 are supported; pika detects the engine version from the mount and adjusts the API path automatically.

## Inheritance entry

`path` is the secret path under the configured mount.

```json
{
  "resource": "vault",
  "path": "myapp/db",
  "paths": ["password"],
  "inject": "database.password"
}
```

If the Vault secret returns `{ "password": "hunter2", "host": "..." }`, the resolved config gains `{ "database": { "password": "hunter2" } }`.

See [Inheritance](./) for the full meaning of `paths` / `inject`.
