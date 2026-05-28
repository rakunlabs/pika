# Inheritance

A pika config file can pull values from elsewhere and merge them into the resolved output that consumers see. The merging happens at **read time**, so the source is always the live remote value — no caching, no copy-paste.

Sources fall into two categories:

1. **Internal files** — another pika config (or a specific variant of it).
2. **External resources** — Vault, Kubernetes Secrets, Consul, etcd, AWS Secrets Manager / SSM, GCP Secret Manager, Azure Key Vault, or plain HTTP.

External resources are configured once under **Settings → External Resources** and then referenced by name from any file's inheritance chain.

## Inheritance entry shape

Each entry on a file is a small JSON object. The top-level key tells pika where to look:

```json
{
  "resource": "my-vault",
  "path": "myapp/database",
  "paths": ["password", "host"],
  "inject": "database.auth"
}
```

| Field      | Required          | Description                                                                                                       |
| ---------- | ----------------- | ----------------------------------------------------------------------------------------------------------------- |
| `resource` | (one of 3)        | Name of an external resource defined under **Settings**. Use this for Vault, Kubernetes, Consul, etc.             |
| `source`   | (one of 3)        | Path to another pika config file. Append `@variant` to inherit from a specific variant.                           |
| `mount`    | (one of 3)        | Prefix of a raw mount. Combined with `path`, picks a specific file out of the mount.                              |
| `path`     | yes (most cases)  | The resource-specific path: a Vault secret path, an etcd key, an S3 object key, etc.                              |
| `paths`    | no                | Pick only specific keys out of the loaded data. JSON-pointer-like paths.                                          |
| `inject`   | no                | Nest the inherited data under this key in the resolved output (e.g. `database.auth`).                             |

You manage inheritance entries from the **Inherits** section of a file in the UI. Order matters — later entries override earlier ones, and the file's own content overrides everything.

## External resources

Each external resource has a **name** that you choose. That name is what `resource:` references. Pika supports the following backends — each has its own page with auth, configuration, and inheritance examples:

- [HTTP](./http) — generic HTTP fetcher with basic / bearer / OAuth2 auth.
- [Vault](./vault) — HashiCorp Vault KV secrets.
- [Kubernetes](./kubernetes) — `Secret` and `ConfigMap` objects from the Kubernetes API.
- [Consul](./consul) — Consul KV.
- [etcd](./etcd) — etcd keys.
- [AWS](./aws) — Secrets Manager or SSM Parameter Store.
- [GCP](./gcp) — Secret Manager.
- [Azure](./azure) — Key Vault.

## Examples

### Pull a database password out of Vault

Define an external resource named `vault` pointing at your Vault server, then on `myapp/config` add an inheritance entry:

```json
{
  "resource": "vault",
  "path": "myapp/db",
  "paths": ["password"],
  "inject": "database.password"
}
```

If the Vault secret returns `{ "password": "hunter2", "host": "..." }`, the resolved config is merged with `{ "database": { "password": "hunter2" } }`.

### Inherit from another pika file

A `prod` variant that starts from the base config and only overrides a few fields:

```json
{ "source": "myapp/config" }
```

Or inherit from a specific variant:

```json
{ "source": "myapp/config@staging" }
```

### Combine a Kubernetes secret with a JSON file from S3

```json
[
  { "mount": "shared-configs", "path": "common.json" },
  { "resource": "k8s",         "path": "default/secret/myapp", "inject": "secrets" }
]
```

The base config's hand-edited fields override both inherited sources, so you can drop a single key into the file to change behaviour without touching either source.

## Preview before saving

The **Render** panel in the UI shows the fully resolved output of a file with all inheritance applied. Use it to verify the chain produces the expected merged document before consumers fetch it.

The same data is available programmatically via:

```sh
curl -X POST -H "Authorization: Bearer $TOKEN" \
  https://localhost:8080/api/v1/render/myapp/config
```
