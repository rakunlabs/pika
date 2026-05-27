# Hooks

Hooks fire when files or configs change. Pika logs every emitted event as one structured server log line by default; this can be disabled under **Settings → Hooks**. Hook delivery rules are not required for basic event observability. Pika also ships with four delivery sinks — HTTP webhook, Kafka, Redis Pub/Sub, and NATS — plus a configurable `log` sink for custom log messages. Hooks are managed under **Settings → Hooks**.

## Event types

| Event              | Trigger                              |
| ------------------ | ------------------------------------ |
| `file.created`     | File uploaded to a raw mount.        |
| `file.updated`     | File overwritten on a raw mount.     |
| `file.deleted`     | File deleted from a raw mount.       |
| `file.renamed`     | File renamed.                        |
| `file.copied`      | File copied (possibly cross-mount).  |
| `dir.created`      | Directory created on a raw mount.    |
| `config.created`   | Config file created.                 |
| `config.updated`   | Config file saved (new version).     |
| `config.deleted`   | Config file deleted.                 |
| `*`                | Match all events.                    |

## Default payload

All sinks receive a JSON-encoded `Event`:

```json
{
  "type": "file.created",
  "timestamp": "2026-05-07T12:34:56.789Z",
  "hook": "my-hook-name",
  "mount": "uploads",
  "path": "documents/report.pdf",
  "size": 12345,
  "protocol": "http",
  "user": "alice",
  "old_path": "",
  "dst_mount": "",
  "dst_path": "",
  "config_key": "",
  "config_version": 0,
  "variant": ""
}
```

Field meanings:

- `protocol` — how the change was made: `http` (admin API) or `internal` (cluster sync, hooks, etc.). Public-port endpoints are read-only and never produce write events.
- `user` — pika username, or empty for internal/system actions.
- `old_path`, `dst_*` — populated for `file.renamed` and `file.copied` events.
- `config_key`, `config_version`, `variant` — populated for `config.*` events.

## Filters

Each hook can be filtered before it fires:

- **Event types** — pick one or more of the events above (or `*`).
- **Mounts** — restrict to specific raw mount prefixes.
- **Path pattern** — `filepath.Match` glob (e.g. `*.pdf`, `firmware/v*/*.bin`).

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

Kafka and Redis TLS fields (cert, key, CA) accept three forms:

- A plain filesystem path (`/etc/ssl/kafka.pem`).
- A pika raw-mount reference: `raw://certs/kafka.pem`.
- A pika config reference: `config://certificates/kafka` — the file content is used as-is.

This lets you rotate TLS material via the UI without touching the host filesystem.
