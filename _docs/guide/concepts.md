# Concepts

A few short definitions before the rest of the guide makes sense.

## Folders and files

The config store is a tree. Internal nodes are **folders**; leaves are **config files**. Paths use forward slashes:

```
myapp/
├── config         (file — JSON)
├── secrets        (file — YAML)
└── workers/
    ├── config     (file — TOML)
    └── secrets    (file — YAML)
```

A path like `myapp/workers/config` uniquely identifies a file. The same path is what consumers use against `/data/myapp/workers/config`.

Folders are created implicitly when you save a file inside them.

## Files

A **file** is a single content blob with:

- a **format** (`json`, `yaml`, or `toml`) that determines storage and validation,
- a **version history** — every save creates a new version,
- zero or more **variants** — independent copies of the file for different environments,
- zero or more **inheritance entries** — references to other configs or external sources that get merged in at read time.

Files behave like documents, not like key-value pairs. You edit the whole document, get back the whole document.

## Versions

Every save bumps the version number (`1`, `2`, `3`, …). Old versions are kept and can be fetched directly:

```sh
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/data/myapp/config?version=3
```

Versions can also be **tagged with a semver constraint** (e.g. `>= 0.1.0`) so consumers can request a config that matches their own application version. See [Versions & variants](./versions-variants) for the details.

## Variants

A **variant** is a parallel version history of the same file scoped to a specific name — `prod`, `staging`, `dev`, or anything you choose. Variants share the path but not the content. They're stored internally as `path@variant`.

```sh
# Base
GET /data/myapp/config

# Production variant
GET /data/myapp/config?variant=prod
```

Each variant has its own version history and can have its own inheritance chain.

## Inheritance

Files can declare **inheritance entries** that pull data from elsewhere and merge it into the resolved output. Sources include:

- another pika file (or one of its variants),
- a [raw mount](./raw-files) (local disk, S3, FTP, …),
- an [external resource](./inheritance/) (Vault, Kubernetes, Consul, etcd, AWS, GCP, Azure, plain HTTP).

Merging happens at **read time**, not at save time. The stored file content is always your hand-edited document; pika resolves the inheritance chain when a consumer asks for it.

## Tokens and scopes

API tokens authenticate non-human consumers. Each token has 1+ **scopes**, where a scope is a `path` glob plus a list of allowed operations (`read`, `write`, `delete`).

```json
{
  "path": "myapp/**",
  "operations": ["read"]
}
```

See [Tokens & scopes](/reference/tokens-and-scopes) for the matcher rules.

## Raw mounts

Pika can mount external storage backends and serve them as plain files over HTTP — independent of the config store. A mount has a **prefix** (e.g. `uploads`) and a backend type (`local`, `s3`, `ftp`, `sftp`, `webdav`, `vercelblob`). Files in a mount are reachable at `/raw/{prefix}/{path}`.

See [Raw file serving](./raw-files).

## Hooks

A **hook** is a subscription to events. When a file or config changes, pika dispatches a JSON event to one or more **targets** (HTTP webhook, Kafka, Redis Pub/Sub, NATS, log). See [Hooks](./hooks).

## Settings

Almost everything beyond ports and storage paths is stored in the database and edited from the UI under **Settings**. That includes auth strategies, raw mounts, external resources, hooks, the public port, the compat endpoints, and user-sync schedules. The settings document itself is fetched and patched via `GET`/`POST /api/v1/settings`.
