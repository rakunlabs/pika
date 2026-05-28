# Hooks

Hooks fire when files or configs change. Pika logs every emitted event as one structured server log line by default; this can be disabled under **Settings → Hooks**. Hook delivery rules are not required for basic event observability. Pika also ships with four delivery sinks — HTTP webhook, Kafka, Redis Pub/Sub, and NATS — plus a configurable `log` sink for custom log messages. Hooks are managed under **Settings → Hooks**.

## Event types

Currently emitted by pika:

| Event                  | Trigger                                                     |
| ---------------------- | ----------------------------------------------------------- |
| `config.created`       | Config file created via the admin API.                      |
| `config.deleted`       | Config file deleted via the admin API.                      |
| `vault.item.created`   | Personal-vault item created.                                |
| `vault.item.updated`   | Personal-vault item updated.                                |
| `vault.item.deleted`   | Personal-vault item soft-deleted.                           |
| `vault.unlock.failed`  | Personal-vault unlock attempt failed (wrong master password). |
| `*`                    | Match all events.                                           |

::: info Vault event payloads
Vault events carry only the item id, type, and the calling user. The encrypted payload is **never** included — the server cannot read it (the personal vault is end-to-end encrypted client-side).
:::

::: info Reserved event types
`file.*`, `dir.*`, `config.updated`, and `registry.*` are declared in the event type constants but no longer emitted in this build — the raw filesystem and artifact-registry features have been extracted out of pika. Hook filters can still reference these strings; they simply won't fire.
:::

## Default payload

All sinks receive a JSON-encoded `Event`:

```json
{
  "type": "config.created",
  "timestamp": "2026-05-07T12:34:56.789Z",
  "hook": "my-hook-name",
  "config_key": "myapp/config",
  "config_version": 3,
  "variant": "",
  "user": "alice",
  "protocol": "http"
}
```

Field meanings:

- `protocol` — how the change was made: `http` (admin API) or `internal` (cluster sync, etc.). Public-port Endpoints are read-only and never produce write events.
- `user` — pika username, or empty for internal/system actions.
- `config_key`, `config_version`, `variant` — populated for `config.*` events.
- For `vault.item.*` events, `path` carries the item id and `user` is the owning user. The body of the vault item is intentionally **not** included.

## Filters

Each hook can be filtered before it fires:

- **Event types** — pick one or more of the events above (or `*`).
- **Path pattern** — `filepath.Match` glob (e.g. `myapp/*`, `tenants/*/secrets`).

A hook only fires when all filters match.

## Targets

A single hook can dispatch to multiple targets. Each target has its own settings.

### HTTP webhook

```text
Method : POST | PUT
URL    : https://example.com/pika-events
Headers: { "X-Pika-Token": "..." }
Timeout: 30s
```

Pika sends the rendered payload as the request body and `User-Agent: pika/<version>`. Non-2xx responses are retried with exponential backoff (configurable per target).

### Kafka

```text
Brokers       : kafka-1:9092, kafka-2:9092
Topic         : pika.events
Key template  : {{.Mount}}/{{.Path}}    (default — overridable)
TLS           : disabled | enabled (cert / key / CA)
SASL          : PLAIN | SCRAM-SHA-256 | SCRAM-SHA-512
```

TLS material can come from disk paths, inline PEM, or [pika references](#pika-references-in-tls-fields).

### Redis Pub/Sub

```text
Mode      : standalone | cluster
Address(es): redis:6379
Channel   : pika.events
Password  : ...
DB        : 0
TLS       : optional with mTLS support
```

### NATS

```text
URL    : nats://nats:4222
Subject: pika.events
Auth   : token | username/password
```

### log

Emits an `slog` line on the pika server. Useful while developing a payload template:

```text
Level  : info | warn | ...
Message: hook fired: {{.Type}} {{.Mount}}/{{.Path}}
Fields : { "user": "{{.User}}", "size": {{.Size}} }
```

## Custom payloads

Each target can define a `body_template` (or `key_template` / `message` / `fields` for non-HTTP sinks) using Go's [`text/template`](https://pkg.go.dev/text/template). Available fields:

| Field             | Type   |
| ----------------- | ------ |
| `.Type`           | string |
| `.Timestamp`      | RFC3339 string |
| `.Hook`           | string |
| `.Mount`          | string |
| `.Path`           | string |
| `.Size`           | int    |
| `.Protocol`       | string |
| `.User`           | string |
| `.OldPath`        | string |
| `.DstMount`       | string |
| `.DstPath`        | string |
| `.ConfigKey`      | string |
| `.ConfigVersion`  | int    |
| `.Variant`        | string |

Example HTTP body template:

```text
{
  "channel": "#ops",
  "text": ":package: `{{.User}}` uploaded `{{.Path}}` ({{.Size}} bytes) to `{{.Mount}}`"
}
```

## Pika references in TLS fields

Kafka and Redis TLS fields (cert, key, CA) accept two forms:

- A plain filesystem path (`/etc/ssl/kafka.pem`).
- A pika config reference: `config://certificates/kafka` — the file content is read live from pika storage each time the target connects.

This lets you rotate TLS material via the UI without touching the host filesystem. Legacy `raw://` references from earlier pika versions decode but resolve to inline PEM text; they will fail with a parse error if the path no longer matches a real file.
