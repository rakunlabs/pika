# Encryption

Pika can encrypt sensitive at-rest data with a server-side master
key. The feature is **opt-in**: a fresh install runs without
encryption until an administrator enables it from Settings →
Server encryption key. Once enabled the key is **never read from
disk or environment** — the operator enters it through the web UI
(or `POST /api/v1/key/unlock`) after every restart, and the server
runs in a "locked" state until that happens.

This model mirrors HashiCorp Vault's seal/unseal pattern, scoped to
Pika's single-key design. The trade-off is a manual unlock step on
every restart in exchange for the master key never sitting on disk
or in `/proc/$pid/environ`.

## Algorithm

- **Cipher**: XChaCha20-Poly1305 (AEAD).
- **Key size**: 256 bits (32 bytes).
- **Nonce size**: 192 bits (24 bytes).
- **KDF**: SHA-256 over the operator-supplied passphrase.

The passphrase you type is run through SHA-256 to derive the 32-byte
cipher key. Any non-empty string works; a long randomly-generated
phrase is recommended.

::: danger
Pika never persists the encryption key. Lose it and you lose every
encrypted record. Store it in a password manager and document its
location in your DR plan.
:::

## Server lifecycle

```
                    ┌──────────────────────────────┐
       fresh install│ uninitialized (encryption    │
                    │   off — runs as plaintext)   │
                    └──────┬───────────────────────┘
                           │  Settings → Enable encryption
                           ▼
                    ┌──────────────┐
                    │   unlocked   │
                    └──────┬───────┘
                           ▼  restart
                    ┌──────────────┐
                    │    locked    │  POST /api/v1/key/unlock
                    └──────┬───────┘  (verifies key, unlocks)
                           ▼
                    ┌──────────────┐
                    │   unlocked   │
                    └──────────────┘
```

A fresh install runs with the encryption layer dormant; the lockgate
middleware is a no-op until a verifier exists on disk. The first
time an administrator opts in (`POST /api/v1/key/initialize`, gated
by `settings.manage`) a verifier ciphertext is written into the
settings table. From that point on, every restart enters the locked
state until an admin authenticates and unlocks. The supplied key is
validated against the verifier (AEAD decrypt + magic-prefix check)
before the server accepts it; a wrong key is rejected with HTTP 403
without entering the unlocked state.

## Wire format

Every encrypted at-rest blob carries a 1-byte format tag followed by
the AEAD ciphertext:

```
[0]      magic byte: 0x01 (single-cipher chacha20-poly1305 envelope)
[1..N]   nonce(24) || sealed_payload || tag(16)
```

The tag exists so future format upgrades (alternate ciphers,
HSM-backed envelopes) can land without breaking on-disk
compatibility — `internal/secret/envelope` dispatches on byte 0.

## What gets encrypted

When the server is unlocked, pika encrypts:

- **Mount credentials** stored in settings: S3 secret keys, FTP/SFTP
  passwords and private keys, WebDAV passwords, Vercel Blob tokens.
- **External-resource credentials**: Vault tokens, Vault AppRole
  secret IDs, AWS secret keys, GCP service-account JSON, Azure
  client secrets, Kubernetes inline kubeconfig.
- **Hook target secrets**: Kafka SASL/SCRAM passwords, Redis
  passwords, NATS tokens & passwords.
- **Server-host private keys**: SFTP host key PEM, FTP TLS key PEM.
- **Verifier record**: the 16-byte randomized known-plaintext used
  to detect a wrong key on unlock.

User passwords are independently hashed with bcrypt and are not
touched by this layer. The personal vault uses end-to-end encryption
in the browser; the server-side key has no access to vault contents.

Operationally-visible fields (mount prefixes, hostnames, port
numbers, share names, public access keys) stay plaintext so they
remain available in audit logs and during the locked state.

## Locked-state behavior

While the server is locked (verifier on disk, no live key):

- `GET /healthz`, `GET /api/v1/info`, the auth flow (`/login/*`,
  `/logout`, `/api/v1/me/*`) and the locked-state key endpoints
  (`/api/v1/key/status`, `/api/v1/key/unlock`) keep working.
- Every other request returns `503 Service Unavailable` with header
  `X-Pika-Locked: true` and a JSON body `{"code":"server_locked",...}`.
- `/api/v1/key/unlock` requires both authentication and the
  `settings.manage` capability — an unauthenticated probe gets 401,
  not 503. API automation can call this with a token holding
  `settings.manage` (no UI session needed).
- The web UI catches the header and renders a full-screen unlock
  prompt the moment any background request hits 503.
- `Settings.Get` still returns the row with public fields populated
  but secret slots blank — the auth bootstrap path can read what it
  needs without the master key.
- `Settings.Set` fails closed: a write that would persist secret
  values returns an error rather than storing them in plaintext.

## Key rotation

`POST /api/v1/key/rotate` swaps the live key:

```sh
curl -X POST -H "Cookie: pika_session=$SESSION" \
  -H "Content-Type: application/json" \
  -d '{"current_key":"old-passphrase","new_key":"new-passphrase"}' \
  https://localhost:8080/api/v1/key/rotate
```

Sequence:

1. Validate `current_key` by AEAD-decrypting the on-disk verifier.
2. Read every encrypted-secret blob through the current key into
   memory.
3. Build a new encryptor, re-encrypt the verifier, install the new
   key as the live encryptor.
4. Re-write the secret blobs through the wrapper; they get sealed
   under the new key on the way out.

After rotation the next server restart will require the new key.
Save it before clicking "Rotate".

The legacy `POST /api/v1/rotate` endpoint and the separate
`admin_secret` settings have been retired — `settings.manage` is the
only auth required to drive any key-lifecycle operation.

## Backups

`GET /api/v1/backup` exports the entire database as a single
archive. Backups can themselves be password-protected:

```sh
curl -H "Authorization: Bearer $TOKEN" \
  -o pika-backup.tar.gz \
  "https://localhost:8080/api/v1/backup?encryption_password=correct-horse-battery-staple"
```

The backup envelope is a separate concern from the at-rest
encryption: it covers transport / archival, not on-disk storage.
Use both for defence in depth.

::: warning
The exported backup contains the on-disk ciphertext as-is. To
restore, the target server must be unlocked with the SAME key that
was active when the backup was taken. Without it the encrypted
columns will fail to decrypt after restore.
:::

## Operational checklist

- [ ] On first start, choose a strong server key and store it in a
      password manager BEFORE clicking Initialize.
- [ ] Document the key's location and recovery owner in your DR
      plan — pika cannot recover it for you.
- [ ] Plan for the post-restart unlock step in your operational
      runbook (Kubernetes rolling restarts, OOM auto-restarts,
      etc., all require manual unlock now).
- [ ] Take a baseline backup with `encryption_password` set after
      configuring secrets.
- [ ] If the host disk is shared (e.g. a Kubernetes PVC on a
      multi-tenant cluster), at-rest encryption gives you defence
      in depth even with disk encryption enabled.

## Migration from `PIKA_SECRET_ENCRYPTION_KEY`

The `PIKA_SECRET_ENCRYPTION_KEY` env var (and the equivalent
`secret.encryption_key` config field) is no longer honored. If your
deployment sets it:

1. Restart pika once to log the deprecation warning and force the
   manual-unlock flow.
2. Sign in to the UI as a superadmin.
3. Initialize the server key with the SAME value you previously set
   in the env var. The verifier gets written; existing encrypted
   data stays decryptable.
4. Remove the env var from your deployment manifest.
5. (Optional) Rotate to a new key now that you control the
   lifecycle interactively.

If you started a fresh install on the new model and never set the
env var, you'll see the "Set up server encryption key" form on
first start.
