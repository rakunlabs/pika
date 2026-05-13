// Client-side end-to-end vault crypto. Mirrors the 1Password threat
// model: the server stores opaque ciphertexts + KDF parameters, and
// the master password + per-account Secret Key combine in the browser
// to derive the key that wraps/unwraps the vault key.
//
// Layered keys (matches what server.VaultAccount persists):
//
//   master_password   – user-memorized; never leaves the browser
//   secret_key        – 32 random bytes; generated at Setup; printed
//                       to the Emergency Kit; never leaves the
//                       browser EXCEPT as `secret_key_hash` (a
//                       SHA-256 verifier — see hashSecretKey)
//   account_key       – 32 bytes; Argon2id(master_password, salt) keyed
//                       with secret_key as additional input
//   vault_key         – 32 bytes; random at Setup; wrapped with
//                       account_key for transport / storage; the
//                       wrapped form is what server.WrappedVaultKey
//                       stores
//   item_key          – per-item AEAD key — currently fused with the
//                       vault key (one key for all items); reserved
//                       slot for a future per-item subkey if we want
//                       sharing without exposing the whole vault
//
// Crypto primitives are libsodium (XChaCha20-Poly1305 AEAD, Argon2id).
// Same AEAD as the server's at-rest wrapper (XChaCha20-Poly1305 in
// internal/secret/crypto/chacha.go) so the format is consistent
// across the server ↔ browser boundary.

import sodium from 'libsodium-wrappers-sumo';

// ─── Lifecycle ────────────────────────────────────────────────────

let readyPromise: Promise<typeof sodium> | null = null;

/**
 * Ensure the libsodium WASM module is loaded. Idempotent — every
 * crypto entry point calls this first so callers don't need to.
 */
export async function ready(): Promise<typeof sodium> {
  if (!readyPromise) {
    readyPromise = sodium.ready.then(() => sodium);
  }
  return readyPromise;
}

// ─── Types matching the server wire shape ─────────────────────────

/**
 * VaultKDFParams mirrors service.VaultKDFParams. Memory is in KiB to
 * match the server side; libsodium's crypto_pwhash expects bytes so
 * we multiply at the boundary.
 */
export interface VaultKDFParams {
  algorithm: 'argon2id';
  memory: number; // KiB
  iterations: number;
  parallelism: number;
  salt: string; // base64
}

/**
 * VaultAccountView mirrors service.VaultAccountView. byte slices arrive
 * as base64-encoded strings (Go's encoding/json default for []byte).
 */
export interface VaultAccountView {
  user_id: string;
  kdf: VaultKDFParams;
  wrapped_vault_key: string; // base64
  wrapped_vault_key_version: number;
  recovery_kit_id?: string;
  session_lock_seconds?: number;
  item_count: number;
  created_at: string;
  updated_at: string;
}

/**
 * VaultSetupPayload is the body we POST to /me/vault/setup. Bytes
 * are base64-encoded on the wire — Go's `[]byte` field decoder
 * handles base64 transparently.
 */
export interface VaultSetupPayload {
  secret_key_hash: string;
  kdf: VaultKDFParams;
  wrapped_vault_key: string;
  wrapped_vault_key_version: number;
  session_lock_seconds?: number;
}

// ─── Argon2id parameter presets ──────────────────────────────────

/**
 * KDF preset levels. The Default is the OWASP-recommended starting
 * point for interactive login flows. Strong doubles memory; Fast
 * matches Argon2's `INTERACTIVE` preset for low-end devices.
 *
 * Server-side validateKDF accepts:
 *   memory ∈ [19_456, 1_048_576] (19 MiB to 1 GiB)
 *   iterations ∈ [2, 64]
 *   parallelism ∈ [1, 8]
 *   salt ∈ [16, 64] bytes
 */
export type KDFPreset = 'fast' | 'default' | 'strong';

export function presetParams(preset: KDFPreset): Omit<VaultKDFParams, 'salt'> {
  switch (preset) {
    case 'fast':
      return { algorithm: 'argon2id', memory: 32 * 1024, iterations: 2, parallelism: 1 };
    case 'strong':
      return { algorithm: 'argon2id', memory: 128 * 1024, iterations: 4, parallelism: 1 };
    case 'default':
    default:
      return { algorithm: 'argon2id', memory: 64 * 1024, iterations: 3, parallelism: 1 };
  }
}

// ─── Base64 helpers ───────────────────────────────────────────────

/**
 * Encode a Uint8Array to standard base64 (NOT url-safe). Matches Go
 * encoding/json's []byte serialization so we can hand strings
 * straight to fetch().
 */
export function toBase64(b: Uint8Array): string {
  // Use libsodium's encoder for consistency; the standard variant
  // matches Go's encoding/json default.
  return sodium.to_base64(b, sodium.base64_variants.ORIGINAL);
}

/**
 * Decode a base64 string produced by toBase64 (or by Go's encoding/json).
 * Throws on bad padding / alphabet — we don't try to recover, the
 * server invariant guarantees the format.
 */
export function fromBase64(s: string): Uint8Array {
  return sodium.from_base64(s, sodium.base64_variants.ORIGINAL);
}

// ─── Secret key helpers ───────────────────────────────────────────

/**
 * Generate a fresh 32-byte Secret Key. Returned both as raw bytes
 * (kept in memory for the session) and as a human-typeable string
 * (printed on the Emergency Kit; the user pastes it when unlocking
 * on a second device).
 *
 * Format: `<chunks of 6 chars, dash-separated>` over a Crockford-ish
 * alphabet (no I/L/O/U) so the user can re-type it without confusing
 * lookalike characters. 32 bytes → 8 chunks of 6 chars = ~150 bits
 * of usable entropy.
 */
export interface SecretKey {
  bytes: Uint8Array; // 32 raw bytes — kept in session memory
  formatted: string; // human-typable string for the kit
}

const SECRET_KEY_ALPHABET = '23456789ABCDEFGHJKMNPQRSTVWXYZ';

export async function generateSecretKey(): Promise<SecretKey> {
  await ready();
  const bytes = sodium.randombytes_buf(32);
  return { bytes, formatted: formatSecretKey(bytes) };
}

/**
 * Encode 32 random bytes into the human-typeable form. Each output
 * character carries 5 bits of entropy from the alphabet (32 chars),
 * so 32 bytes (256 bits) → 52 characters. We group as 4 + 4 with a
 * single dash and 8 groups, dashes inserted every 6 chars for
 * readability ("XXXX-XX-XXXXXX-..."). The exact grouping is
 * cosmetic; we strip dashes before decoding.
 */
function formatSecretKey(b: Uint8Array): string {
  const chars: string[] = [];
  let buf = 0;
  let bits = 0;
  for (let i = 0; i < b.length; i++) {
    buf = (buf << 8) | b[i];
    bits += 8;
    while (bits >= 5) {
      bits -= 5;
      const idx = (buf >> bits) & 0x1f;
      chars.push(SECRET_KEY_ALPHABET[idx]);
    }
  }
  if (bits > 0) {
    const idx = (buf << (5 - bits)) & 0x1f;
    chars.push(SECRET_KEY_ALPHABET[idx]);
  }
  // Group into 6-char segments separated by dashes.
  const parts: string[] = [];
  for (let i = 0; i < chars.length; i += 6) {
    parts.push(chars.slice(i, i + 6).join(''));
  }
  return parts.join('-');
}

/**
 * Decode a user-typed Secret Key back into 32 bytes. Tolerant of
 * dashes, spaces, and case. Throws on unrecognized characters or
 * wrong length.
 */
export function parseSecretKey(input: string): Uint8Array {
  const cleaned = input.toUpperCase().replace(/[^A-Z0-9]/g, '');
  const out = new Uint8Array(32);
  let buf = 0;
  let bits = 0;
  let outIdx = 0;
  for (const ch of cleaned) {
    const idx = SECRET_KEY_ALPHABET.indexOf(ch);
    if (idx < 0) {
      throw new Error(`Invalid character in Secret Key: ${ch}`);
    }
    buf = (buf << 5) | idx;
    bits += 5;
    if (bits >= 8) {
      bits -= 8;
      if (outIdx >= 32) {
        throw new Error('Secret Key is too long');
      }
      out[outIdx++] = (buf >> bits) & 0xff;
    }
  }
  if (outIdx !== 32) {
    throw new Error(`Secret Key has wrong length (got ${outIdx} bytes, want 32)`);
  }
  return out;
}

/**
 * SHA-256 verifier of the raw Secret Key. The server stores this so
 * it can reject a bad Secret Key without the cost of a full KDF
 * derivation; it cannot reverse to the Secret Key.
 */
export async function hashSecretKey(secretKey: Uint8Array): Promise<Uint8Array> {
  await ready();
  // crypto.subtle is universally available in browsers; using it
  // (rather than libsodium's crypto_generichash) keeps the choice
  // explicit and standard.
  //
  // TS 6 typings tightened BufferSource to disallow Uint8Array views
  // over a SharedArrayBuffer-typed backing store; libsodium-allocated
  // arrays come back as `Uint8Array<ArrayBufferLike>`. A copy into a
  // fresh ArrayBuffer-typed buffer satisfies the constraint without
  // adding a runtime cost beyond the 32-byte memcpy.
  const view = new Uint8Array(secretKey.length);
  view.set(secretKey);
  const buf = await crypto.subtle.digest('SHA-256', view.buffer);
  return new Uint8Array(buf);
}

// ─── Argon2id derivation ──────────────────────────────────────────

/**
 * Derive the account key from the master password + secret key and
 * the stored KDF parameters. Burns Memory KiB and several hundred
 * milliseconds of CPU — call sparingly (Setup and Unlock paths).
 *
 * The salt input to Argon2id is the SHA-256 of (secret_key || stored_salt).
 * This domain-separates the salt: the stored salt is just per-account
 * randomness, but mixing the secret key in means the same master
 * password on two devices produces different account keys unless the
 * Secret Key matches too. (This is the property 1Password's
 * "Master Password + Secret Key" combination provides.)
 */
export async function deriveAccountKey(
  masterPassword: string,
  secretKey: Uint8Array,
  kdf: VaultKDFParams,
): Promise<Uint8Array> {
  await ready();
  if (kdf.algorithm !== 'argon2id') {
    throw new Error(`unsupported KDF algorithm: ${kdf.algorithm}`);
  }
  const storedSalt = fromBase64(kdf.salt);
  // Mix secret key into the salt input — concatenate then SHA-256
  // down to libsodium's required SALTBYTES (16) length. See
  // hashSecretKey for the rationale behind the explicit buffer copy.
  const combined = new Uint8Array(secretKey.length + storedSalt.length);
  combined.set(secretKey, 0);
  combined.set(storedSalt, secretKey.length);
  const saltDigest = await crypto.subtle.digest('SHA-256', combined.buffer);
  const argonSalt = new Uint8Array(saltDigest).slice(0, sodium.crypto_pwhash_SALTBYTES);

  return sodium.crypto_pwhash(
    32, // 32-byte output (account key)
    masterPassword,
    argonSalt,
    kdf.iterations,
    kdf.memory * 1024, // KiB → bytes for libsodium
    sodium.crypto_pwhash_ALG_ARGON2ID13,
  );
}

// ─── AEAD wrap / unwrap ───────────────────────────────────────────

/**
 * Encrypt `plaintext` with a 32-byte key using XChaCha20-Poly1305.
 * Output format: 24-byte nonce || ciphertext || 16-byte MAC.
 *
 * This is the same on-wire shape libsodium's crypto_secretbox_easy
 * produces, prefixed with the nonce. The server treats this as
 * opaque bytes; only the browser decodes/encodes.
 */
export async function aeadEncrypt(plaintext: Uint8Array, key: Uint8Array): Promise<Uint8Array> {
  await ready();
  const nonce = sodium.randombytes_buf(sodium.crypto_aead_xchacha20poly1305_ietf_NPUBBYTES);
  const ciphertext = sodium.crypto_aead_xchacha20poly1305_ietf_encrypt(
    plaintext,
    null, // additional_data
    null, // secret_nonce (unused in xchacha)
    nonce,
    key,
  );
  const out = new Uint8Array(nonce.length + ciphertext.length);
  out.set(nonce, 0);
  out.set(ciphertext, nonce.length);
  return out;
}

/**
 * Decrypt a buffer produced by aeadEncrypt. Throws when the MAC fails
 * or the input is truncated. Browser-side callers should catch and
 * surface as "wrong password" — never log the underlying message.
 */
export async function aeadDecrypt(blob: Uint8Array, key: Uint8Array): Promise<Uint8Array> {
  await ready();
  const nbytes = sodium.crypto_aead_xchacha20poly1305_ietf_NPUBBYTES;
  if (blob.length < nbytes + 16) {
    throw new Error('ciphertext too short');
  }
  const nonce = blob.slice(0, nbytes);
  const ciphertext = blob.slice(nbytes);
  return sodium.crypto_aead_xchacha20poly1305_ietf_decrypt(
    null, // secret_nonce
    ciphertext,
    null, // additional_data
    nonce,
    key,
  );
}

// ─── Vault key wrap / unwrap ──────────────────────────────────────

/**
 * Generate a fresh 32-byte vault key. Called at Setup only.
 */
export async function generateVaultKey(): Promise<Uint8Array> {
  await ready();
  return sodium.randombytes_buf(32);
}

/**
 * Wrap a vault key under the account key. The output is the
 * server-side `wrapped_vault_key` blob.
 */
export async function wrapVaultKey(vaultKey: Uint8Array, accountKey: Uint8Array): Promise<Uint8Array> {
  return aeadEncrypt(vaultKey, accountKey);
}

/**
 * Unwrap a vault key. Returns null when the MAC fails (wrong account
 * key, typically wrong master password). The SPA renders this as
 * "incorrect password" without leaking why.
 */
export async function unwrapVaultKey(wrapped: Uint8Array, accountKey: Uint8Array): Promise<Uint8Array | null> {
  try {
    return await aeadDecrypt(wrapped, accountKey);
  } catch {
    return null;
  }
}

// ─── Item payload encrypt / decrypt ───────────────────────────────

/**
 * VaultItemPayload is the structured form the SPA produces and the
 * server never sees in cleartext. Adding new fields here doesn't
 * touch the schema — the server stores the whole thing as opaque
 * bytes.
 *
 * `fields` is a flat array of typed fields the editor renders;
 * a per-type "template" in the SPA decides the default field set
 * for new items but the runtime shape is identical regardless.
 *
 * Sensitive values are masked by default in the UI — `sensitive`
 * is a hint, not a security boundary. Anyone with the decrypted
 * payload sees every byte.
 */
export interface VaultItemField {
  id: string;
  type:
    | 'text'
    | 'password'
    | 'email'
    | 'phone'
    | 'url'
    | 'username'
    | 'totp'
    | 'date'
    | 'month_year'
    | 'cvv'
    | 'card_number'
    | 'pin'
    | 'address'
    | 'ssh_private_key'
    | 'ssh_public_key'
    | 'api_key'
    | 'secret_token'
    | 'hostname'
    | 'port'
    | 'connection_string';
  label: string;
  value: string;
  sensitive?: boolean;

  // TOTP-specific (only meaningful when type === 'totp'):
  // - When stored, `value` is the otpauth:// URL or a bare base32 secret.
  // - `period`, `digits`, `algorithm` allow override of the defaults
  //   (30s / 6 / SHA-1) when an issuer requires non-standard params.
  totp_period?: number;
  totp_digits?: number;
  totp_algorithm?: 'SHA1' | 'SHA256' | 'SHA512';
}

export interface VaultItemPayload {
  fields: VaultItemField[];
  notes?: string;
}

/**
 * Encrypt a structured payload to the byte form the server stores.
 * JSON-serialize then AEAD-encrypt. The textual JSON intermediate is
 * harmless — it never leaves the browser before encryption.
 */
export async function encryptItemPayload(payload: VaultItemPayload, vaultKey: Uint8Array): Promise<Uint8Array> {
  const json = new TextEncoder().encode(JSON.stringify(payload));
  return aeadEncrypt(json, vaultKey);
}

/**
 * Decrypt a server-returned item payload. Returns null on AEAD
 * failure (corrupt blob, wrong key) so the SPA can surface an item-
 * level error without blowing up the list view.
 */
export async function decryptItemPayload(blob: Uint8Array, vaultKey: Uint8Array): Promise<VaultItemPayload | null> {
  try {
    const bytes = await aeadDecrypt(blob, vaultKey);
    const text = new TextDecoder().decode(bytes);
    const data = JSON.parse(text);
    if (!data || typeof data !== 'object' || !Array.isArray(data.fields)) {
      return null;
    }
    return data as VaultItemPayload;
  } catch {
    return null;
  }
}

// ─── String / list ciphertext helpers ─────────────────────────────

/**
 * Encrypt a UTF-8 string and return the on-wire byte blob. Used for
 * the per-item title field, which is fully E2E (server only sees
 * ciphertext). Empty input still produces a ≥ 40-byte ciphertext
 * because XChaCha20-Poly1305 prepends a 24-byte nonce and appends a
 * 16-byte tag.
 */
export async function encryptString(plain: string, key: Uint8Array): Promise<Uint8Array> {
  return aeadEncrypt(new TextEncoder().encode(plain), key);
}

/**
 * Decrypt a string ciphertext. Returns null on AEAD failure (corrupt
 * blob, wrong key) so the caller can render an "unreadable" badge
 * inline rather than crashing the whole list view.
 */
export async function decryptString(blob: Uint8Array, key: Uint8Array): Promise<string | null> {
  try {
    const bytes = await aeadDecrypt(blob, key);
    return new TextDecoder().decode(bytes);
  } catch {
    return null;
  }
}

/**
 * Encrypt a string array (e.g. tags or URL hostnames). JSON-encodes
 * the array first so the wire format is stable across array sizes
 * and characters; AEAD then frames it. Empty arrays still produce a
 * valid ciphertext — the server stores the bytes verbatim regardless.
 */
export async function encryptStringList(arr: string[], key: Uint8Array): Promise<Uint8Array> {
  return aeadEncrypt(new TextEncoder().encode(JSON.stringify(arr)), key);
}

/**
 * Decrypt a string-array ciphertext. Returns null on AEAD failure or
 * when the decrypted bytes are not a JSON array of strings. Used by
 * the item list to recover tags and hostnames before in-memory
 * filtering.
 */
export async function decryptStringList(blob: Uint8Array, key: Uint8Array): Promise<string[] | null> {
  try {
    const bytes = await aeadDecrypt(blob, key);
    const text = new TextDecoder().decode(bytes);
    const data = JSON.parse(text);
    if (!Array.isArray(data)) return null;
    return data.filter((x): x is string => typeof x === 'string');
  } catch {
    return null;
  }
}

// ─── Setup helper ────────────────────────────────────────────────

/**
 * Build the complete VaultSetupPayload from a master password and
 * preset choice. Returns the secret key (must be shown to the user
 * once) and the payload to POST to /me/vault/setup.
 *
 * The vault_key is generated fresh, wrapped, and discarded from the
 * returned object — the caller must keep the unwrapped vault_key in
 * memory if it wants to seed items immediately after Setup.
 */
export interface SetupResult {
  payload: VaultSetupPayload;
  secretKey: SecretKey;
  vaultKey: Uint8Array; // returned so caller can populate the live session
}

export async function buildSetup(
  masterPassword: string,
  preset: KDFPreset = 'default',
  sessionLockSeconds: number = 900,
): Promise<SetupResult> {
  await ready();
  if (masterPassword.length < 8) {
    throw new Error('Master password must be at least 8 characters');
  }
  const params = presetParams(preset);
  const salt = sodium.randombytes_buf(sodium.crypto_pwhash_SALTBYTES);
  const kdf: VaultKDFParams = { ...params, salt: toBase64(salt) };

  const secretKey = await generateSecretKey();
  const accountKey = await deriveAccountKey(masterPassword, secretKey.bytes, kdf);
  const vaultKey = await generateVaultKey();
  const wrapped = await wrapVaultKey(vaultKey, accountKey);
  const skHash = await hashSecretKey(secretKey.bytes);

  return {
    payload: {
      secret_key_hash: toBase64(skHash),
      kdf,
      wrapped_vault_key: toBase64(wrapped),
      wrapped_vault_key_version: 1,
      session_lock_seconds: sessionLockSeconds,
    },
    secretKey,
    vaultKey,
  };
}

// ─── Unlock helper ───────────────────────────────────────────────

/**
 * Given the stored account view + the user-typed password and Secret
 * Key, run the full unwrap chain and return the live vault key. The
 * Secret Key bytes can be supplied either as the raw 32 bytes (from
 * the parseSecretKey path) or implicitly via the SecretKey type.
 *
 * Returns null when the password is wrong (the only observable
 * failure mode — the Secret Key mismatch path is short-circuited by
 * the server's /unlock-check endpoint before we get here).
 */
export async function unlockVault(
  account: VaultAccountView,
  masterPassword: string,
  secretKey: Uint8Array,
): Promise<Uint8Array | null> {
  await ready();
  const accountKey = await deriveAccountKey(masterPassword, secretKey, account.kdf);
  const wrapped = fromBase64(account.wrapped_vault_key);
  return unwrapVaultKey(wrapped, accountKey);
}

// ─── Rotation helper ─────────────────────────────────────────────

/**
 * Re-wrap the existing vault key under a new master password.
 * Vault items are NOT re-encrypted — only the wrapper changes — so
 * this is O(1) regardless of vault size.
 *
 * Returns a VaultSetupPayload suitable to POST to
 * /me/vault/rotate-password. The Secret Key is unchanged: the
 * caller passes the existing raw bytes (or re-parses the formatted
 * form).
 */
export async function buildRotatePayload(
  newMasterPassword: string,
  secretKey: Uint8Array,
  vaultKey: Uint8Array,
  preset: KDFPreset = 'default',
  sessionLockSeconds?: number,
): Promise<VaultSetupPayload> {
  await ready();
  if (newMasterPassword.length < 8) {
    throw new Error('Master password must be at least 8 characters');
  }
  const params = presetParams(preset);
  const salt = sodium.randombytes_buf(sodium.crypto_pwhash_SALTBYTES);
  const kdf: VaultKDFParams = { ...params, salt: toBase64(salt) };
  const accountKey = await deriveAccountKey(newMasterPassword, secretKey, kdf);
  const wrapped = await wrapVaultKey(vaultKey, accountKey);
  const skHash = await hashSecretKey(secretKey);

  return {
    secret_key_hash: toBase64(skHash),
    kdf,
    wrapped_vault_key: toBase64(wrapped),
    wrapped_vault_key_version: 1,
    session_lock_seconds: sessionLockSeconds ?? 0,
  };
}

// ─── Zeroize helper ──────────────────────────────────────────────

/**
 * Best-effort wipe of a Uint8Array. JS engines may keep copies in
 * the JIT'd code path — there's no way to guarantee a key never
 * appears in process memory after this call. Treat as defense-in-
 * depth: it reduces the window during which a memory dump (e.g. via
 * a heap snapshot debugger) recovers the live key.
 */
export function zeroize(b: Uint8Array | null | undefined): void {
  if (b) {
    b.fill(0);
  }
}

// ─── Trusted-device sealing ───────────────────────────────────────
//
// "Trust this device" lets a user reduce the unlock prompt to a
// single master-password input by caching the Secret Key, encrypted
// with a key derived from the master password alone, in localStorage.
//
// Security tradeoff (intentional, opt-in):
//   - The original 1Password model requires the Secret Key as a
//     physically-held second factor on every unlock. Trusting a
//     device weakens this: an attacker with both the blob AND the
//     master password can recover the Secret Key offline.
//   - The blob is wrapped under Argon2id with the SAME cost
//     parameters as the account KDF, so the brute-force cost
//     against the blob equals the brute-force cost against a
//     server DB dump. There is no security regression vs. the
//     server-side threat model — only against the "browser is
//     compromised" threat.
//   - 3 consecutive failed unlock attempts against the blob result
//     in automatic untrust (the store enforces this; see
//     `trustedFailCount` in store.svelte.ts).

/**
 * On-disk format for a trusted-device blob. Persisted to
 * localStorage under `pika.vault.trust.${user_id}`. Versioned so we
 * can evolve the format without breaking existing trusts (a
 * mismatched / unknown version causes the blob to be ignored —
 * effectively untrusting the device).
 */
export interface TrustedDeviceBlob {
  version: 1;
  /** Vault account user_id; mismatch detection (e.g. account reset). */
  account_id: string;
  /** KDF used to derive the device key. Includes its own per-device salt. */
  kdf: VaultKDFParams;
  /** AEAD-encrypted (nonce||ct||tag) raw Secret Key, base64-encoded. */
  sealed: string;
  /** ISO-8601 — shown in Settings, also useful when debugging. */
  created_at: string;
}

/**
 * Derive a device-only key from the master password and a per-device
 * salt. Unlike deriveAccountKey, the Secret Key is NOT mixed into
 * the salt — that would be circular, since we're trying to recover
 * the Secret Key from the encrypted blob.
 *
 * The KDF parameter struct must carry a salt of exactly
 * crypto_pwhash_SALTBYTES (16) bytes; sealTrustedDeviceBlob
 * guarantees this when it generates the blob.
 */
export async function deriveDeviceKey(
  masterPassword: string,
  kdf: VaultKDFParams,
): Promise<Uint8Array> {
  await ready();
  if (kdf.algorithm !== 'argon2id') {
    throw new Error(`unsupported KDF algorithm: ${kdf.algorithm}`);
  }
  const salt = fromBase64(kdf.salt);
  if (salt.length !== sodium.crypto_pwhash_SALTBYTES) {
    throw new Error(
      `invalid device salt length: got ${salt.length}, want ${sodium.crypto_pwhash_SALTBYTES}`,
    );
  }
  return sodium.crypto_pwhash(
    32,
    masterPassword,
    salt,
    kdf.iterations,
    kdf.memory * 1024,
    sodium.crypto_pwhash_ALG_ARGON2ID13,
  );
}

/**
 * Build a fresh TrustedDeviceBlob. Generates a 16-byte per-device
 * salt, derives the device key from the master password under the
 * supplied cost parameters, and AEAD-seals the Secret Key.
 *
 * The caller passes the account's KDF parameters (so cost matches
 * the account) but only the `algorithm`, `memory`, `iterations`,
 * and `parallelism` fields are reused — the `salt` is replaced with
 * fresh randomness.
 */
export async function sealTrustedDeviceBlob(
  secretKey: Uint8Array,
  masterPassword: string,
  accountKDF: VaultKDFParams,
  accountID: string,
): Promise<TrustedDeviceBlob> {
  await ready();
  const salt = sodium.randombytes_buf(sodium.crypto_pwhash_SALTBYTES);
  const kdf: VaultKDFParams = {
    algorithm: 'argon2id',
    memory: accountKDF.memory,
    iterations: accountKDF.iterations,
    parallelism: accountKDF.parallelism,
    salt: toBase64(salt),
  };
  const deviceKey = await deriveDeviceKey(masterPassword, kdf);
  try {
    const sealed = await aeadEncrypt(secretKey, deviceKey);
    return {
      version: 1,
      account_id: accountID,
      kdf,
      sealed: toBase64(sealed),
      created_at: new Date().toISOString(),
    };
  } finally {
    zeroize(deviceKey);
  }
}

/**
 * Unseal a TrustedDeviceBlob back to the raw Secret Key bytes.
 * Returns null on any failure (unknown version, wrong master
 * password, corrupt blob) so the caller can fold every failure into
 * a single "wrong password" UI message — distinguishing them would
 * leak information to a shoulder-surfer.
 */
export async function openTrustedDeviceBlob(
  blob: TrustedDeviceBlob,
  masterPassword: string,
): Promise<Uint8Array | null> {
  await ready();
  if (blob.version !== 1) return null;
  let deviceKey: Uint8Array | null = null;
  try {
    deviceKey = await deriveDeviceKey(masterPassword, blob.kdf);
    const sealed = fromBase64(blob.sealed);
    return await aeadDecrypt(sealed, deviceKey);
  } catch {
    return null;
  } finally {
    if (deviceKey) zeroize(deviceKey);
  }
}
