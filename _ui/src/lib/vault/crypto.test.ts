import { beforeAll, describe, expect, it } from 'vitest';
import {
  buildRotatePayload,
  buildSetup,
  decryptItemPayload,
  decryptString,
  encryptItemPayload,
  encryptString,
  fromBase64,
  generateSecretKey,
  generateVaultKey,
  parseSecretKey,
  ready,
  toBase64,
  unlockVault,
  zeroize,
  type VaultAccountView,
  type VaultItemPayload,
  type VaultSetupPayload,
} from './crypto';

// Argon2id at the real presets (32–128 MiB) is slow; tests use the
// 'fast' preset, which still exercises the full derivation path.
const PRESET = 'fast' as const;

function accountFrom(payload: VaultSetupPayload): VaultAccountView {
  return {
    user_id: 'u1',
    kdf: payload.kdf,
    wrapped_vault_key: payload.wrapped_vault_key,
    wrapped_vault_key_version: payload.wrapped_vault_key_version,
    item_count: 0,
    created_at: '',
    updated_at: '',
  };
}

const samplePayload: VaultItemPayload = {
  fields: [
    { id: 'f1', type: 'username', label: 'User', value: 'alice' },
    { id: 'f2', type: 'password', label: 'Password', value: 'p@ss — ünïcode ✓', sensitive: true },
  ],
  notes: '# notes',
};

beforeAll(async () => {
  await ready();
});

describe('base64 helpers', () => {
  it('round-trips arbitrary bytes', () => {
    const b = new Uint8Array([0, 1, 2, 250, 255, 128]);
    expect(fromBase64(toBase64(b))).toEqual(b);
  });
});

describe('secret key', () => {
  it('formats and parses back to the same 32 bytes', async () => {
    const sk = await generateSecretKey();
    expect(sk.bytes).toHaveLength(32);
    expect(parseSecretKey(sk.formatted)).toEqual(sk.bytes);
    // Lowercase + no dashes + confusable letters are tolerated.
    const sloppy = sk.formatted.toLowerCase().replace(/-/g, '').replace(/1/g, 'l').replace(/0/g, 'o');
    expect(parseSecretKey(sloppy)).toEqual(sk.bytes);
  });

  it('rejects truncated keys', async () => {
    const sk = await generateSecretKey();
    expect(() => parseSecretKey(sk.formatted.slice(0, -4))).toThrow(/too short/);
    expect(() => parseSecretKey('')).toThrow(/empty/);
  });
});

describe('item payload encryption', () => {
  it('round-trips a payload', async () => {
    const key = await generateVaultKey();
    const blob = await encryptItemPayload(samplePayload, key);
    expect(await decryptItemPayload(blob, key)).toEqual(samplePayload);
  });

  it('uses a fresh nonce each time', async () => {
    const key = await generateVaultKey();
    const a = await encryptItemPayload(samplePayload, key);
    const b = await encryptItemPayload(samplePayload, key);
    expect(toBase64(a)).not.toBe(toBase64(b));
  });

  it('returns null for the wrong key or tampered ciphertext', async () => {
    const key = await generateVaultKey();
    const other = await generateVaultKey();
    const blob = await encryptItemPayload(samplePayload, key);
    expect(await decryptItemPayload(blob, other)).toBeNull();
    const tampered = blob.slice();
    tampered[tampered.length - 1] ^= 0xff;
    expect(await decryptItemPayload(tampered, key)).toBeNull();
  });

  it('round-trips strings', async () => {
    const key = await generateVaultKey();
    expect(await decryptString(await encryptString('hello', key), key)).toBe('hello');
  });
});

describe('setup / unlock / rotate', () => {
  it('unlocks with the right password + secret key and fails otherwise', async () => {
    const setup = await buildSetup('correct horse battery', PRESET);
    const account = accountFrom(setup.payload);

    const vk = await unlockVault(account, 'correct horse battery', setup.secretKey.bytes);
    expect(vk).not.toBeNull();
    expect(vk).toEqual(setup.vaultKey);

    expect(await unlockVault(account, 'wrong password!', setup.secretKey.bytes)).toBeNull();

    const otherSk = await generateSecretKey();
    expect(await unlockVault(account, 'correct horse battery', otherSk.bytes)).toBeNull();
  });

  it('returns a live (non-zeroized) vault key from setup and unlock', async () => {
    const setup = await buildSetup('correct horse battery', PRESET);
    expect(setup.vaultKey.some((b) => b !== 0)).toBe(true);
    const vk = await unlockVault(accountFrom(setup.payload), 'correct horse battery', setup.secretKey.bytes);
    expect(vk!.some((b) => b !== 0)).toBe(true);
    // Items encrypted with the setup key decrypt with the unlocked key.
    const blob = await encryptItemPayload(samplePayload, setup.vaultKey);
    expect(await decryptItemPayload(blob, vk!)).toEqual(samplePayload);
  });

  it('does not zeroize caller-owned inputs', async () => {
    const setup = await buildSetup('correct horse battery', PRESET);
    const sk = setup.secretKey.bytes.slice();
    const vk = setup.vaultKey.slice();
    await unlockVault(accountFrom(setup.payload), 'correct horse battery', setup.secretKey.bytes);
    await buildRotatePayload('another password', setup.secretKey.bytes, setup.vaultKey, PRESET);
    expect(setup.secretKey.bytes).toEqual(sk);
    expect(setup.vaultKey).toEqual(vk);
  });

  it('rotation re-wraps the same vault key under the new password', async () => {
    const setup = await buildSetup('old password 123', PRESET);
    const rotated = await buildRotatePayload(
      'new password 456',
      setup.secretKey.bytes,
      setup.vaultKey,
      PRESET,
      600,
    );
    expect(rotated.session_lock_seconds).toBe(600);
    expect(rotated.secret_key_hash).toBe(setup.payload.secret_key_hash);
    expect(rotated.kdf.salt).not.toBe(setup.payload.kdf.salt);

    const account = accountFrom(rotated);
    expect(await unlockVault(account, 'old password 123', setup.secretKey.bytes)).toBeNull();
    expect(await unlockVault(account, 'new password 456', setup.secretKey.bytes)).toEqual(setup.vaultKey);
  });

  it('enforces the minimum password length', async () => {
    await expect(buildSetup('short', PRESET)).rejects.toThrow(/at least 8/);
    const vk = await generateVaultKey();
    const sk = await generateSecretKey();
    await expect(buildRotatePayload('short', sk.bytes, vk, PRESET)).rejects.toThrow(/at least 8/);
  });
});

describe('zeroize', () => {
  it('wipes buffers and tolerates null', () => {
    const b = new Uint8Array([1, 2, 3]);
    zeroize(b);
    expect([...b]).toEqual([0, 0, 0]);
    expect(() => zeroize(null)).not.toThrow();
    expect(() => zeroize(undefined)).not.toThrow();
  });
});
