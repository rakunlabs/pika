# Personal Vault

The personal vault stores per-user secrets — logins, payment cards, SSH keys, TOTP seeds, and so on — encrypted **end-to-end in the browser**. The pika server never sees the unencrypted contents. Not even an administrator with full database access can read another user's items.

This is a different feature from the *secrets-by-reference* model pika uses for configs. Configs that inherit from external resources (Vault, AWS Secrets Manager, etc.) are visible to anyone with the right token; the personal vault is yours alone.

## At a glance

| Concept | Where it lives |
|---|---|
| Master password | In your head — never sent over the wire |
| Secret Key | Generated at Setup; printed on the Emergency Kit; never sent |
| Account key | Derived in-browser from Master Password + Secret Key via Argon2id |
| Vault key | Random 32 bytes; encrypted with account key; only the wrapped form is stored |
| Item title, tags, URL hostnames, payload | `XChaCha20-Poly1305` ciphertext keyed by the vault key |
| Item type, favorite/archived flags, timestamps | Cleartext on the server (used for list filtering and sort) |

The server stores opaque blobs, the KDF parameters, the wrapped vault key, the item type (fixed enum of 10 values), and a few lifecycle flags. Everything content-bearing is opaque.

## Threat model

The vault is designed to keep an attacker who steals a full pika database backup — including the encrypted blobs and the KDF salt — out of the encrypted contents. To read the vault they would need to additionally:

1. Know (or guess) your master password, **and**
2. Possess your Secret Key (32 random bytes from the Emergency Kit).

Brute-forcing Argon2id with the default parameters costs hundreds of milliseconds per guess on commodity hardware; a long master password combined with the Secret Key is well outside any feasible offline attack window.

What the server admin **can** see:

- The item *type* (login / card / identity / …) — a fixed 10-value enum used for icons and the type filter.
- Lifecycle flags: `favorite`, `archived`, `deleted_at`, `last_used_at`, `created_at`, `updated_at`.
- Item counts and the version number on each row.
- The audit log of vault operations (when hooks are configured).

What the server admin **cannot** see:

- Item titles. Encrypted with the per-user vault key before they leave the browser.
- Tags. Same — encrypted as a list before transit.
- URL hostnames. Same. (A future autofill feature will introduce a blind index alongside; today they are fully opaque.)
- Passwords, TOTP seeds, card numbers, SSH private keys, or any other field value.
- The notes attached to an item.
- The master password or Secret Key.

## Initial setup

1. Navigate to **Vault** in the navbar after logging in.
2. Choose a master password (8 characters minimum; longer is much better — 4–5 random words from a wordlist is the recommended baseline).
3. Pick a KDF preset:
   - **Fast** (32 MiB, 2 iterations) — older devices, mobile.
   - **Default** (64 MiB, 3 iterations) — modern laptop / desktop.
   - **Strong** (128 MiB, 4 iterations) — power user / paranoid.
4. The server generates the Emergency Kit immediately. **Save it now** — the Secret Key is shown exactly once.

The kit can be printed, downloaded as HTML, or copied to a password-manager-of-last-resort. Anyone with both the kit and your master password can read your vault, so keep them separate (master password in your head; kit in a fireproof safe or a deposit box).

## Server-managed encryption (optional)

Admins can turn off per-user encryption in **Settings → Features → Personal vault** by unchecking **Require a master password for each vault**. This requires the server encryption key (**Settings → Server encryption key**) to be initialized and unlocked.

On new deployments where the server encryption key is set up, this option is off by default. Deployments that already have master-password vaults keep requiring a master password until an admin changes the setting.

When it is off:

- Each user's vault key is sealed with the server encryption key instead of their master password. Users open **Vault** directly — no master password, Secret Key, Emergency Kit or auto-lock.
- Items are still encrypted at rest with the vault key, but **anyone holding the server key can read them**. Choose this mode only when that trade-off is acceptable.
- While the server key is locked, server-managed vaults can't be opened (`503`).
- Rotating the server key re-seals every server-managed vault key.

Switching modes never re-encrypts items; only the protection of the vault key changes:

- **Master password → server:** an existing vault is converted the next time its owner unlocks it with their master password and Secret Key.
- **Server → master password:** the next time a user opens their vault, they choose a master password and receive a new Secret Key for the same vault.

## Unlocking on another device

To unlock the vault on a different browser or after the auto-lock fired:

1. Sign in to pika normally (the session is independent of the vault unlock).
2. Open **Vault**.
3. Enter your master password and Secret Key.

The Secret Key field accepts dashes, spaces, and either case — the formatting on the printed kit is for readability only.

## Item types

| Type | Default fields |
|---|---|
| Login | Username, password, website |
| Card | Cardholder, number, expiry, CVV, PIN |
| Identity | Name, email, phone, address |
| Secure note | Just the notes |
| SSH key | Public key, private key, passphrase, fingerprint |
| API credential | Endpoint, API key, API secret |
| Database | Host, port, database, username, password, connection string |
| Server | Hostname, IP, port, username, password |
| Software license | Product, version, license key, support email |
| TLS certificate | Certificate, private key, CA chain, fingerprint, expiry |

You can add, remove, reorder, or change the type of any field on any item — the type list is the starting template, not a constraint.

### TOTP

Items can hold a TOTP field — either an `otpauth://totp/...` URL (copied from a QR code) or a bare base32 secret. The 6-digit code is computed in the browser, displayed in the editor with a countdown timer, and copies to clipboard with one click.

The TOTP feature in the vault is distinct from pika's *own login 2FA* — that one is stored under **Settings → Account Security** and gates your pika session. The vault TOTP is for *other services* (your bank, your GitHub account, your VPN, ...).

## Auto-lock

The vault key lives only in the browser tab's memory while the vault is unlocked. It is zeroized when:

- You leave the `/vault` route in the SPA.
- The auto-lock timer fires (default 15 minutes; configurable at Setup time or via the gear menu).
- You explicitly click Lock.
- You log out of pika.

After a lock, returning to `/vault` shows the unlock screen again. Active editing is preserved only as long as the tab is alive; refresh = unlock.

## Master password rotation

Settings → Vault → Change master password runs the full Argon2id re-wrap **locally** with the new password and posts the new wrapped vault key. Items are **not** re-encrypted (the vault key is unchanged); the operation is constant-time regardless of vault size.

The Secret Key is preserved. To rotate the Secret Key you would need to re-wrap every item, which is a larger surgery — see the Reset path below for now; a proper "rotate Secret Key" flow is planned.

## Reset / lost master password

If you lose your master password, your vault is unrecoverable. There is no admin reset that preserves the items — that would defeat the threat model. What an admin **can** do is delete the user account, which cascades to wipe the vault entirely.

A user can also reset their own vault from **Settings → Vault → Delete vault**. This destroys every item irreversibly. You can then run Setup again with a fresh master password and Secret Key.

## Files

The vault also has a **Files** area (left navigation → Files) where you can keep any binary or text file — documents, key files, archives, binaries. Drag files or whole folders from your desktop onto the page (folder structure is preserved), drag items between folders or onto breadcrumbs to move them, and preview images, video, audio, PDFs and text in the side pane.

Text files (Markdown, plain text, JSON/YAML/TOML, shell, code, `.env`, …) up to 5 MB open in a built-in editor with syntax highlighting: double-click a file or use **Edit**. Save with **Ctrl/⌘+S**; if the file changed since you opened it you are asked before overwriting. Markdown files open in a rendered preview (headings, lists, task lists, tables, code blocks; raw HTML is never rendered) with **Edit / Split / Preview** modes and a **Raw** switch to see the source. **New → Folder / Markdown file / Text file** creates items in the current folder; a name like `notes/todo.md` creates the folders too.

Any other file can be opened with **Open as text** (row menu or the details pane). Files that look binary or aren't valid UTF-8 open read-only, because saving them as text would change their bytes; **Edit anyway** unlocks editing after a warning. Files over 5 MB show only their first 5 MB, read-only.

::: warning Files are not end-to-end encrypted
Unlike vault items, file contents and names are stored as-is on the server-side storage backend. Protect the backend accordingly: keep S3 buckets private, use an `https` endpoint, and enable server-side encryption on the bucket.
:::

An administrator chooses the backend under **Settings → Vault Storage**:

| Backend | Notes |
|---|---|
| Disabled (default) | The Files area is shown but uploads are refused. |
| Local disk | A directory on the server, created with owner-only permissions. It is **not** included in Pika's database backup — back it up separately. |
| S3 bucket | Any S3-compatible service (AWS S3, MinIO, Cloudflare R2, …). Supports a key prefix and path-style URLs. The secret access key is sealed with the server encryption key, so the key must be initialized and unlocked before it can be saved. **Test connection** probes unsaved values. |

Objects are written under `vault/<user id>/<file id>` (after the optional S3 prefix). Names and folders live in Pika's database, so renaming and moving never touch the backend. Switching backends does not migrate existing files: they stay listed but return `409` on download until you switch back.

Uploads are streamed straight to the backend without buffering, so there is no size limit by default. Set `PIKA_SERVER_LIMITS_VAULT_FILE_BODY_MB` to cap them. Deleting a user or resetting a vault also deletes that user's files.

## Sharing

Sharing between users is **not implemented** in this release. Each vault is private to a single user. Cross-user share links with expiry are on the roadmap (Dalga 3).

## Hook events

Vault operations emit the following event types (in addition to the existing `config.*` and `file.*`):

| Event | Trigger |
|---|---|
| `vault.item.created` | New item written |
| `vault.item.updated` | Existing item updated |
| `vault.item.deleted` | Item hard-deleted (purge from trash) |
| `vault.unlock.failed` | Bad Secret Key on `/me/vault/unlock-check` |

Events carry the calling user and item id but **never** the encrypted payload. Wire them to a log sink for an audit trail; the server-side state itself is intentionally minimal.

## API reference

All endpoints live under `/api/v1/me/vault/*` and require an authenticated session — no capability gate, since every user owns their own vault.

| Method | Path | Purpose |
|---|---|---|
| GET | `/me/vault/status` | Lightweight check: initialized? item count? |
| GET | `/me/vault/account` | KDF params + wrapped vault key |
| POST | `/me/vault/setup` | First-time initialization (409 on re-init) |
| POST | `/me/vault/setup-server` | Create a server-managed vault (server key mode only) |
| GET | `/me/vault/server-key` | Raw vault key of a server-managed vault (`503` while the server key is locked) |
| POST | `/me/vault/convert-to-server` | Hand an unlocked master-password vault to the server key |
| POST | `/me/vault/convert-to-user` | Re-protect a server-managed vault with a master password |
| POST | `/me/vault/unlock-check` | Rate-limited Secret Key verifier |
| POST | `/me/vault/rotate-password` | Re-wrap the vault key with a new master password |
| POST | `/me/vault/recovery-kit` | Regenerate the kit ID |
| PUT | `/me/vault/session-lock` | Update the auto-lock TTL |
| DELETE | `/me/vault` | Wipe everything (items + history + account) |
| GET | `/me/vault/items` | List items with filter params |
| POST | `/me/vault/items` | Create item |
| GET | `/me/vault/items/{id}` | Get item with encrypted payload |
| PUT | `/me/vault/items/{id}` | Update item (optimistic concurrency on `expected_version`) |
| DELETE | `/me/vault/items/{id}` | Soft-delete (move to trash); `?purge=true` for hard-delete |
| POST | `/me/vault/items-restore/{id}` | Restore from trash |
| POST | `/me/vault/items-use/{id}` | Bump `last_used_at` for "recently used" sorting |
| GET | `/me/vault/items-versions/{id}` | Item edit history (newest first) |
| GET | `/me/vault/files` | Storage status plus every file and folder node |
| POST | `/me/vault/files-folder` | Create a folder (`{parent_id, name}`; `/` in name creates nested folders) |
| PUT | `/me/vault/files-upload?name=&parent_id=&path=&replace=` | Stream the raw request body as a file; `path` creates relative folders on demand |
| GET | `/me/vault/files-content/{id}` | Download (supports `Range`); `?download=1` forces attachment |
| PUT | `/me/vault/files-content/{id}?if_updated_at=` | Replace the file's content with the raw body; `409` if it changed since `if_updated_at` |
| PATCH | `/me/vault/files/{id}` | Rename and/or move (`{name, parent_id}`) |
| DELETE | `/me/vault/files/{id}` | Delete a file, or a folder recursively |

The list filter parameters:

| Param | Values |
|---|---|
| `type` | One of the known item types (`login`, `card`, ...) |
| `favorite` | `1` for favorites only |
| `archived` | `include` to add archived to the active list; `only` for archive-only |
| `trash` | `1` for trash-only |

There is no `q` or `tag` server-side filter — title, tags and URL hostnames are stored as ciphertext. The SPA decrypts the listing in memory and runs free-text + tag filters on the decrypted view. For typical personal vaults (under a few thousand items) the round-trip is sub-second.

### Searching the vault

After unlock, the SPA fetches the full item list once. Subsequent search keystrokes filter the in-memory decrypted set without hitting the network. Tag chips are populated from the union of every loaded item's tag list.

Two consequences:

- Unlock time grows roughly linearly with item count. For 1000 items the title/tags/hostnames decrypt typically runs in ~30 ms on a modern laptop.
- Filtering is not persisted server-side. If you reload the page you lose your search; that's by design — there's no way for the server to remember a query against ciphertext anyway.

## What's not in this release

These appear in the long-term roadmap but were intentionally deferred:

- **Sharing between users** (with expiring links / per-recipient public keys).
- **Browser extension and autofill.** A planned blind index on URL hostnames will allow server-side equality matching for the autofill path without leaking the hostname value.
- **CLI integration** (`pika vault list/get/run -- mycmd`).
- **Watchtower-style audits** (reused-password detection, HIBP breach lookup, certificate expiry warnings).
- **Per-item attachments** (e.g. PDFs scanned alongside a license).
- **Passkey PRF unlock** as a master-password alternative on supported authenticators.

## Schema migration notes

The vault is currently at storage schema **v2**. Earlier development builds used **v1**, which stored item titles, tags and URL hostnames in cleartext on the server.

When the pika binary boots against an older Badger directory whose vault tables are still at v1, the storage layer detects the version mismatch on first open and wipes all three vault buckets (`vault_accounts`, `vault_items`, `vault_item_versions`) before bw registers them under the v2 schema. A `vault: legacy schema detected` warning is logged when this happens.

Because the cleartext-to-ciphertext transition can only be performed client-side (the server doesn't hold the vault key), no automatic data migration is possible. The personal-vault feature has not yet shipped in a release, so this only affects developer workstations and CI environments — production deployments will start fresh at v2.
