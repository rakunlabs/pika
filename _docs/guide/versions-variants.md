# Versions & variants

Pika stores history. Every save bumps a version number; you can also branch the same path into independent **variants** for different environments.

## Versions

Versions are integers, starting at `1` and incrementing on every save. They're immutable — once written, a version's content never changes (unless you delete it explicitly).

### Fetching by integer

```sh
# Latest
curl -H "Authorization: Bearer $TOKEN" \
  https://localhost:8080/data/myapp/config

# Specific version
curl -H "Authorization: Bearer $TOKEN" \
  https://localhost:8080/data/myapp/config?version=3
```

### Listing versions

The admin API exposes the full history:

```sh
curl -H "Authorization: Bearer $TOKEN" \
  https://localhost:8080/api/v1/versions/myapp/config
```

Each entry includes the version number, timestamp, author, and any semver constraint attached to it.

## Semver constraints

Each version can carry a **semver constraint** (e.g. `>= 0.1.0`) that says: "this version is meant for consumers running 0.1.0 or higher." When a consumer requests a config with `?version=` set to a semver string, pika walks the history and picks the latest version whose constraint is satisfied.

### Example

Suppose `myapp/config` has this history:

| Version | Constraint | Notes                            |
| ------- | ---------- | -------------------------------- |
| v1      | _(none)_   | Initial config.                  |
| v2      | _(none)_   | Minor tweak.                     |
| v3      | `>= 0.1.0` | New field added in app 0.1.0.    |
| v4      | _(none)_   | Fix typo.                        |
| v5      | `>= 0.2.0` | Breaking change for app 0.2.0.   |

Consumers ask for the config that matches their version:

```sh
# App running v0.0.5 → gets v2 (latest before the >= 0.1.0 boundary)
curl -H "Authorization: Bearer $TOKEN" \
  "https://localhost:8080/data/myapp/config?version=0.0.5"

# App running v0.1.5 → gets v4 (satisfies >= 0.1.0, before >= 0.2.0)
curl -H "Authorization: Bearer $TOKEN" \
  "https://localhost:8080/data/myapp/config?version=0.1.5"

# App running v0.2.0 → gets v5
curl -H "Authorization: Bearer $TOKEN" \
  "https://localhost:8080/data/myapp/config?version=0.2.0"
```

This lets you evolve configs alongside application versions without breaking older deployments. Add a constraint when shipping a config that requires a newer app version; older clients keep getting the last compatible version.

### Setting constraints

Constraints can be set when saving a new version (in the UI editor) or attached retroactively via:

```sh
curl -X PATCH \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"constraint": ">= 0.1.0"}' \
  https://localhost:8080/api/v1/versions/myapp/config/3
```

## Variants

A **variant** is an independent parallel history of the same file. The base file and each variant have their own content, version history, and inheritance chain — they share only the path.

### When to use variants

- Per-environment configs: `prod`, `staging`, `dev`.
- Per-region configs: `us-east`, `eu-west`.
- A/B variants of the same service config.

If your variants are mostly the same with a few overrides, [inheritance](./inheritance/) is usually a better fit — define a base and let variants inherit from it.

### Creating a variant

In the UI, open the file and use the **Variants** section in the right panel. Internally a variant is stored as `path@variant` (e.g. `myapp/config@prod`).

### Reading a variant

```sh
# Base config
curl -H "Authorization: Bearer $TOKEN" \
  https://localhost:8080/data/myapp/config

# Production variant
curl -H "Authorization: Bearer $TOKEN" \
  "https://localhost:8080/data/myapp/config?variant=prod"

# Staging variant, JSON output, app version 0.3.0
curl -H "Authorization: Bearer $TOKEN" \
  "https://localhost:8080/data/myapp/config?variant=staging&version=0.3.0&format=json"
```

### Listing variants

```sh
curl -H "Authorization: Bearer $TOKEN" \
  https://localhost:8080/api/v1/variants/myapp/config
```

## Format conversion

Regardless of how a file is stored, you can request it in any supported format:

```sh
# Stored as YAML, served as JSON
curl -H "Authorization: Bearer $TOKEN" \
  "https://localhost:8080/data/myapp/config?format=json"
```

Supported `format` values: `json`, `yaml`, `toml`. The `Content-Type` header reflects the requested format.
