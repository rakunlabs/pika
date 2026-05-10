# Inheritance

A pika config file can pull values from elsewhere and merge them into the resolved output that consumers see. The merging happens at **read time**, so the source is always the live remote value — no caching, no copy-paste.

Sources fall into three categories:

1. **Internal files** — another pika config (or a specific variant of it).
2. **Raw mounts** — a file from one of your configured [raw mounts](./raw-files).
3. **External resources** — Vault, Kubernetes Secrets, Consul, etcd, AWS Secrets Manager / SSM, GCP Secret Manager, Azure Key Vault, or plain HTTP.

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

Each external resource has a **name** that you choose. That name is what `resource:` references.

### HTTP

Generic HTTP fetcher. Supports basic auth, bearer tokens, OAuth2 (client credentials, password, etc.), retries, and custom headers — all configurable via the resource form.

```text
Type: HTTP
URL : https://example.com/configs/{path}
Auth: bearer / basic / oauth2 / none
```

The inheritance entry then specifies a `path` that's substituted into the URL.

### Vault

[HashiCorp Vault](https://www.vaultproject.io/) KV secrets.

```text
Type     : Vault
Address  : https://vault.example.com
Mount    : secret
Auth     : token   OR   AppRole
Token    : (when auth=token)
RoleID   : (when auth=AppRole)
SecretID : (when auth=AppRole)
```

Inheritance: `path` is the secret path under the configured mount, e.g. `myapp/db`.

### Kubernetes

Reads `Secret` and `ConfigMap` objects directly from the Kubernetes API. See [Kubernetes external resource](./kubernetes-external) for the full breakdown — service-account / RBAC setup, the three authentication modes (in-cluster, kubeconfig path, inline kubeconfig), and inheritance examples.

```text
Type: Kubernetes
Auth: in-cluster   |   kubeconfig path   |   paste kubeconfig
```

Inheritance: `path` is `<namespace>/<kind>/<name>`, e.g. `default/secret/db-credentials`.

### Consul

[Consul](https://www.consul.io/) KV.

```text
Type   : Consul
Address: https://consul.example.com:8500
Token  : (optional ACL token)
```

Inheritance: `path` is the KV key.

### etcd

```text
Type    : etcd
Address : etcd.example.com:2379
Username: (optional)
Password: (optional)
```

Inheritance: `path` is the etcd key.

### AWS

AWS Secrets Manager or SSM Parameter Store.

```text
Type      : AWS
Region    : eu-west-1
AccessKey : ...
SecretKey : ...
Service   : secretsmanager   |   ssm
```

Inheritance: `path` is the secret name (Secrets Manager) or the parameter name (SSM).

### GCP

GCP Secret Manager.

```text
Type               : GCP
ServiceAccountJSON : { "type": "service_account", ... }   (full JSON key, pasted)
```

Inheritance: `path` is the secret name. Pika resolves the latest version automatically.

### Azure

Azure Key Vault, authenticated via an AAD client credentials flow.

```text
Type        : Azure
VaultURL    : https://my-vault.vault.azure.net/
TenantID    : ...
ClientID    : ...
ClientSecret: ...
```

Inheritance: `path` is the secret name.

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
  http://localhost:8080/api/v1/render/myapp/config
```
