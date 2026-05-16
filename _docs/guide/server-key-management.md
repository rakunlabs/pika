# Server key management

This guide walks through every operational scenario for the
server-side at-rest encryption key. For the cryptographic
background see [Encryption](./encryption); this page is the
runbook.

## First start (fresh install)

At-rest encryption is **opt-in**. A new install runs without it
until an administrator turns it on.

1. Start pika. The process logs `server started; encryption not
   yet enabled` and serves every request normally.
2. Open the web UI and complete the standard first-run admin
   account setup.
3. Configure users, mounts, hooks, externals as needed — these all
   work in plaintext-at-rest mode.
4. When you're ready to turn on encryption, navigate to
   **Settings → Server encryption key → Enable encryption**.
5. Choose a strong passphrase. Save it in your password manager
   BEFORE clicking Enable — there is no recovery path.
6. Confirm the passphrase. The form posts to
   `POST /api/v1/key/initialize` (authenticated, requires
   `settings.manage`); the server writes a verifier record into
   the settings table, re-seals existing sensitive settings under
   the new key, and stays unlocked for the rest of the process
   lifetime.

From here, every subsequent restart enters the locked state.

### CLI / curl variant

```sh
curl -X POST -H "Cookie: pika_session=$SESSION" \
  -H "Content-Type: application/json" \
  -d '{"key":"correct-horse-battery-staple"}' \
  http://localhost:8080/api/v1/key/initialize
```

The endpoint requires an authenticated session OR a token carrying
`settings.manage`. `$SESSION` is the cookie value from
`/login/password`.

## Every-restart unlock

Pika starts in the **locked** state on every restart. Until
unlocked:

- `/api/v1/info`, `/healthz`, `/login/*`, `/logout`, `/api/v1/me/*`,
  and `/api/v1/key/*` continue to work.
- Every other request returns `503` with `X-Pika-Locked: true`.
- The web UI's axios layer detects the header and renders the
  unlock screen automatically.

Steps:

1. Open the web UI. The unlock screen appears.
2. Paste the server key.
3. Click **Unlock**. The form posts to `POST /api/v1/key/unlock`.
4. Wrong key → 403 with a "wrong key" toast; the verifier protects
   against silent corruption.

CLI variant:

```sh
curl -X POST -H "Cookie: pika_session=$SESSION" \
  -H "Content-Type: application/json" \
  -d '{"key":"correct-horse-battery-staple"}' \
  http://localhost:8080/api/v1/key/unlock
```

## Status check

Always available, no auth required:

```sh
curl http://localhost:8080/api/v1/key/status
```

Response:

```json
{"initialized": true, "unlocked": false}
```

- `initialized: false` → the verifier record doesn't exist yet
  (fresh install).
- `unlocked: false` → server is locked; an admin must unlock.
- Both `true` → server is online and serving normal traffic.

## Rotation

When and why to rotate:

- Suspected key compromise.
- Periodic rotation per your security policy.
- Operator handover.

Procedure (web UI):

1. Sign in as a superadmin.
2. **Settings → Security → Server encryption key**.
3. Enter the current key, the new key, and confirm the new key.
4. Save the new key in your password manager BEFORE clicking
   Rotate.
5. Click **Rotate server key**. The flow:
   1. Server validates the current key against the verifier.
   2. Reads every encrypted blob through the current key.
   3. Re-encrypts the verifier with the new key and installs the
      new key as live.
   4. Re-writes the secret blobs through the new key.
6. The next server restart will require the NEW key.

CLI variant:

```sh
curl -X POST -H "Cookie: pika_session=$SESSION" \
  -H "Content-Type: application/json" \
  -d '{"current_key":"old","new_key":"new"}' \
  http://localhost:8080/api/v1/key/rotate
```

### Failure modes during rotation

If step 4 (re-write secrets) fails after step 3 (verifier swap),
the server is in a partial state: the verifier matches the new key
but a settings row may still hold old-key ciphertext. Recovery:

- Retry rotation using the NEW key as both `current_key` and the
  re-typed new key. The verifier accepts the new key, and the
  re-write step runs again. This is safe: the settings wrapper
  re-extracts secrets every time, so re-running puts everything on
  the new key.

If the recovery rotation also fails:

- Restart the server, unlock with the new key, and inspect the
  settings row. Any secret slot that decrypts as garbage was lost
  in the half-rotation; restore from a recent backup taken under
  the new key (see Backups in [Encryption](./encryption)).

## Manual lock

You can lock the server without restarting it. Useful for:

- Rotation rehearsals on a non-production environment.
- "I'm stepping away from this terminal" if you're worried about a
  shoulder-surfer.
- Forcing every active user back through unlock when an operator
  leaves the company.

Web UI: **Settings → Security → Server encryption key → Lock the
server now**.

CLI:

```sh
curl -X POST -H "Cookie: pika_session=$SESSION" \
  http://localhost:8080/api/v1/key/lock
```

After lock, every non-allowlisted request returns 503; the web
UI's axios interceptor flips immediately to the unlock screen for
all open browser tabs the next time they make any request.

## Disaster scenarios

### Lost key, no backup

Encrypted columns are unrecoverable. To restore service you must:

1. Wipe the database (`PIKA_STORAGE_BW_PATH` directory).
2. Start fresh. The Initialize flow will appear.
3. Re-create users, mounts, hooks, externals from scratch.

User accounts and sessions are NOT in the encrypted set, but they
live in the same Badger directory and a wipe loses them too.

### Lost key, have a backup

1. Locate a backup taken when you still had the key.
2. Restore the backup into a fresh server.
3. Unlock with the key that was active when the backup was taken.
4. Once unlocked, rotate to a new key you control.

If you don't have the original key for the backup either, the
backup is also unrecoverable — the export is the on-disk
ciphertext as-is. Plan accordingly: backup-and-key-archive should
live in the same secure store.

### Operator handover

1. Outgoing operator initiates rotation, picks the new key
   themselves, hands off through the org's normal credential-
   transfer process.
2. Incoming operator confirms unlock works with the new key on
   the next restart cycle (e.g. roll one pod in a multi-replica
   deployment).
3. Outgoing operator's password manager entry for the old key can
   be deleted.
