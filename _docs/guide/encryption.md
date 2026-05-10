# Encryption

Pika supports envelope encryption for the embedded BadgerDB store. Setting `PIKA_SECRET_ENCRYPTION_KEY` (or `secret.encryption_key` in the config file) enables it. Without it, the database is plain on disk.

## Algorithm

- **Cipher**: XChaCha20-Poly1305 (AEAD).
- **Key size**: 256 bits.
- **Nonce size**: 192 bits.

The provided string is run through SHA-256 to derive the 32-byte key. Any non-empty string works; the exact value doesn't matter, but you must keep it.

::: danger
Pika never persists the encryption key. Lose it and you lose every encrypted record. Store it in a password manager, a sealed env var, or another secrets manager you trust.
:::

## On-disk layout

Encrypted records use the format `[nonce:24][ciphertext+tag]`. Plain (unencrypted) records share the same store and are mixed in transparently — pika tags each record with a flag so it knows whether to decrypt.

## What gets encrypted

When the encryption key is set, pika encrypts:

- Config file content (all versions and variants).
- API tokens and their hashed comparators.
- External-resource credentials (Vault tokens, AWS keys, GCP service-account JSON, etc.).
- Hook target credentials (Kafka SASL passwords, Redis passwords, etc.).

User passwords are independently hashed with bcrypt and are not affected by this setting.

## Key rotation

`POST /api/v1/rotate` accepts a new key, validated against the **admin secret** that you set under **Settings → Admin Secret**. The endpoint:

1. Verifies the supplied admin secret against the stored bcrypt hash.
2. Hashes the new encryption key with SHA-256.
3. Switches the in-memory encryptor.

After rotation, newly-written records use the new key. To re-encrypt existing records, take a backup with the old key and restore it under the new key (the restore path re-encrypts as it writes).

::: warning
On the bw-backed storage, **column-level re-encryption during rotation is not yet implemented**. The recommended migration today is the backup-restore round-trip. Track the related issue on GitHub if you need automated rotation.
:::

## Backups

`GET /api/v1/backup` exports the entire database as a single archive. Backups can themselves be password-protected:

```sh
curl -H "Authorization: Bearer $TOKEN" \
  -o pika-backup.tar.gz \
  "http://localhost:8080/api/v1/backup?encryption_password=correct-horse-battery-staple"
```

The backup envelope is a separate concern from the at-rest encryption: it covers transport / archival, not on-disk storage. Use both for defence in depth.

## Operational checklist

- [ ] Set `PIKA_SECRET_ENCRYPTION_KEY` before importing real secrets.
- [ ] Set the admin secret under **Settings → Admin Secret** so rotation is gated.
- [ ] Take a baseline backup with `encryption_password` set.
- [ ] Document where the encryption key is stored. Include this in your DR plan.
- [ ] If the host disk is shared (e.g. a Kubernetes PVC on a multi-tenant cluster), at-rest encryption gives you defence in depth even with disk encryption enabled.
