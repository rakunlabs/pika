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
