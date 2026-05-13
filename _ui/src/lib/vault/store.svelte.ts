// Reactive vault state. Holds the unlocked vault key + decrypted
// items in memory for the session and auto-locks after the
// per-account TTL has elapsed without UI activity.
//
// Unlocked material lives ONLY on this store — never in localStorage,
// never serialized into a Svelte URL deeplink. A page refresh wipes
// it; the SPA prompts to unlock again.
//
// EXCEPTION: when the user opts in to "trust this device", the
// Secret Key is sealed under a key derived from the master password
// alone and persisted to localStorage. See crypto.sealTrustedDeviceBlob
// for the format and the security tradeoff. The master password
// itself is never persisted in any form.
//
// Activity is tracked at the global keyup/mousedown level by the
// /vault page; idle => the store zeroizes the key and toggles the
// UI back to the unlock screen.

import { onMount } from 'svelte';

import * as api from './api';
import * as crypto from './crypto';
import type { VaultItem } from './api';
import type { VaultItemPayload, TrustedDeviceBlob } from './crypto';

// ─── Trusted-device storage ──────────────────────────────────────

// localStorage key prefix; suffixed with the account's user_id so
// switching users in the same browser doesn't collide.
const TRUSTED_DEVICE_PREFIX = 'pika.vault.trust.';

// Three consecutive failures against a trusted blob auto-untrust the
// device (defends against shoulder-surfing brute-force).
const TRUSTED_DEVICE_FAIL_LIMIT = 3;

function trustedStorageKey(userID: string): string {
  return TRUSTED_DEVICE_PREFIX + userID;
}

function readTrustedBlob(userID: string): TrustedDeviceBlob | null {
  try {
    const raw = localStorage.getItem(trustedStorageKey(userID));
    if (!raw) return null;
    const parsed = JSON.parse(raw) as TrustedDeviceBlob;
    // Best-effort validation. If anything looks off we treat the
    // blob as absent rather than throwing — a malformed blob just
    // means the user falls back to the secret-key prompt.
    if (
      !parsed ||
      parsed.version !== 1 ||
      typeof parsed.account_id !== 'string' ||
      typeof parsed.sealed !== 'string' ||
      !parsed.kdf ||
      parsed.account_id !== userID
    ) {
      return null;
    }
    return parsed;
  } catch {
    return null;
  }
}

function writeTrustedBlob(blob: TrustedDeviceBlob): void {
  localStorage.setItem(trustedStorageKey(blob.account_id), JSON.stringify(blob));
}

function clearTrustedBlob(userID: string): void {
  localStorage.removeItem(trustedStorageKey(userID));
}

/**
 * DecryptedItem caches everything the SPA needs to render a single
 * row WITHOUT re-running AEAD: the original server row, plus the
 * decrypted title / tag list / hostname list / payload. `null` on
 * any of the four fields means "AEAD failed for this slot" — the
 * item is still navigable, but the UI renders an "unreadable" tile
 * instead of leaking the ciphertext or crashing the list.
 */
interface DecryptedItem {
  item: VaultItem;
  title: string | null;
  tags: string[];
  hostnames: string[];
  payload: VaultItemPayload | null;
}

function createVaultStore() {
  // Server-side state mirror.
  let status = $state<api.VaultStatus | null>(null);
  let account = $state<crypto.VaultAccountView | null>(null);

  // Client-only session material. `vaultKey` being non-null is the
  // single source of truth for "vault is unlocked". `secretKey` is
  // kept too so master-password rotation can re-wrap without
  // prompting the user for the kit a second time during the same
  // session.
  let vaultKey = $state<Uint8Array | null>(null);
  let secretKey = $state<Uint8Array | null>(null);

  let items = $state<VaultItem[]>([]);
  let decrypted = $state<Map<string, DecryptedItem>>(new Map());
  let loading = $state(false);
  let error = $state<string | null>(null);

  // Auto-lock plumbing. The timer is reset on every activity
  // notification; when it fires, lock() is called and the SPA
  // re-renders the unlock screen.
  let lockTimer: ReturnType<typeof setTimeout> | null = null;

  const DEFAULT_LOCK_SECONDS = 15 * 60;

  function lockTimeoutSeconds(): number {
    const fromAccount = account?.session_lock_seconds ?? 0;
    return fromAccount > 0 ? fromAccount : DEFAULT_LOCK_SECONDS;
  }

  function resetLockTimer() {
    if (!vaultKey) return;
    if (lockTimer) clearTimeout(lockTimer);
    lockTimer = setTimeout(() => {
      lock();
    }, lockTimeoutSeconds() * 1000);
  }

  /**
   * Install global listeners so any keyup/mousedown resets the
   * auto-lock timer. Returns a teardown function — Svelte 5
   * onMount/onDestroy callers must invoke it on unmount.
   */
  function installActivityWatcher(): () => void {
    const handler = () => resetLockTimer();
    document.addEventListener('keyup', handler, { passive: true });
    document.addEventListener('mousedown', handler, { passive: true });
    return () => {
      document.removeEventListener('keyup', handler);
      document.removeEventListener('mousedown', handler);
    };
  }

  // ─── Status / Account ───────────────────────────────────────

  async function refreshStatus(): Promise<void> {
    try {
      status = await api.getStatus();
    } catch (err: any) {
      error = err?.message ?? 'failed to read vault status';
    }
  }

  async function refreshAccount(): Promise<void> {
    try {
      account = await api.getAccount();
    } catch (err: any) {
      error = err?.message ?? 'failed to read vault account';
    }
  }

  // ─── Setup / Unlock / Lock ──────────────────────────────────

  /**
   * Set up a brand-new vault. Returns the SecretKey so the UI can
   * render the Emergency Kit before the page leaves Setup.
   */
  async function setup(
    masterPassword: string,
    preset: crypto.KDFPreset = 'default',
    sessionLockSeconds: number = 900,
  ): Promise<crypto.SecretKey> {
    loading = true;
    error = null;
    try {
      const result = await crypto.buildSetup(masterPassword, preset, sessionLockSeconds);
      const acc = await api.setup(result.payload);
      account = acc;
      vaultKey = result.vaultKey;
      secretKey = result.secretKey.bytes;
      await refreshStatus();
      resetLockTimer();
      return result.secretKey;
    } finally {
      loading = false;
    }
  }

  /**
   * Unlock an existing vault. Returns true on success, false on a
   * wrong-password failure. Throws for transport-level errors
   * (network, 5xx) so the caller can surface them distinctly.
   *
   * `secretKeyInput` is optional: when omitted, the store attempts
   * to recover the Secret Key from a trusted-device blob in
   * localStorage. If no blob exists or the master password fails to
   * unseal it, the call returns false and the caller is expected to
   * fall back to the full master-password + Secret Key form.
   *
   * Trusted-device unlocks run two Argon2id passes (one to unseal
   * the blob, one to derive the account key) — visibly slower than
   * the full path. The caller should display a spinner.
   */
  async function unlock(masterPassword: string, secretKeyInput?: Uint8Array): Promise<boolean> {
    loading = true;
    error = null;
    try {
      await crypto.ready();
      // Ensure we have the account view; user_id is needed for the
      // trusted-blob lookup AND for unwrapping.
      if (!account) {
        await refreshAccount();
      }
      if (!account) {
        error = 'vault not initialized';
        return false;
      }
      const userID = account.user_id;

      // Resolve the effective secret key. Explicit input wins; only
      // fall back to the trusted-device blob when the caller didn't
      // supply one.
      let sk: Uint8Array | null = secretKeyInput ?? null;
      let cameFromTrustedBlob = false;
      if (!sk) {
        const blob = readTrustedBlob(userID);
        if (!blob) {
          // No way forward: caller must collect the Secret Key.
          return false;
        }
        sk = await crypto.openTrustedDeviceBlob(blob, masterPassword);
        if (!sk) {
          // Wrong master password (or corrupt blob). Count the
          // failure and untrust the device after the configured
          // strike limit so a shoulder-surfer can't brute-force the
          // master password against the local blob indefinitely.
          trustedFailCount += 1;
          if (trustedFailCount >= TRUSTED_DEVICE_FAIL_LIMIT) {
            clearTrustedBlob(userID);
            trustedFailCount = 0;
          }
          return false;
        }
        cameFromTrustedBlob = true;
      }

      // Server-side rate-limited check. Returns a 401 on a wrong
      // Secret Key; treat as "wrong password" because the user can't
      // easily distinguish and the SPA's UX folds both into one
      // "Try again" toast. When the blob successfully decrypted but
      // the server rejects the hash, the stored Secret Key is stale
      // (e.g. account reset on another device) — drop the blob so
      // the next attempt prompts for a fresh Secret Key.
      const skHash = await crypto.hashSecretKey(sk);
      try {
        await api.unlockCheck(crypto.toBase64(skHash));
      } catch (err: any) {
        if (err?.response?.status === 401) {
          if (cameFromTrustedBlob) {
            clearTrustedBlob(userID);
            trustedFailCount = 0;
          }
          return false;
        }
        throw err;
      }

      const key = await crypto.unlockVault(account, masterPassword, sk);
      if (!key) {
        // AEAD failure on the wrapped vault key → wrong master
        // password. For trusted-blob unlocks this is the same
        // shoulder-surfing case as a wrong-blob-unseal: count it.
        if (cameFromTrustedBlob) {
          trustedFailCount += 1;
          if (trustedFailCount >= TRUSTED_DEVICE_FAIL_LIMIT) {
            clearTrustedBlob(userID);
            trustedFailCount = 0;
          }
        }
        return false;
      }
      vaultKey = key;
      secretKey = sk;
      trustedFailCount = 0;
      resetLockTimer();
      return true;
    } finally {
      loading = false;
    }
  }

  // ─── Trusted-device opt-in ──────────────────────────────────

  // Tracks consecutive failed unlock attempts against a trusted
  // blob. Reset to 0 on success. When it hits TRUSTED_DEVICE_FAIL_LIMIT
  // the blob is removed; the user must re-paste their Secret Key to
  // re-trust. Lives in memory only — a refresh resets the count,
  // which is fine: the attack window the counter defends against is
  // shoulder-surfing in a single session.
  let trustedFailCount = 0;

  /**
   * Persist the live Secret Key as a localStorage blob encrypted
   * with a device key derived from the master password. The vault
   * must already be unlocked (we read the live `secretKey` from
   * memory) and the caller must supply the master password again —
   * we deliberately don't cache the master password in the store,
   * even transiently.
   */
  async function trustDevice(masterPassword: string): Promise<void> {
    if (!secretKey || !account) {
      throw new Error('vault must be unlocked first');
    }
    const blob = await crypto.sealTrustedDeviceBlob(
      secretKey,
      masterPassword,
      account.kdf,
      account.user_id,
    );
    writeTrustedBlob(blob);
    trustedFailCount = 0;
  }

  /**
   * Drop the trusted-device blob for the current account. Safe to
   * call when no blob exists. Does NOT lock the live session — only
   * removes the cached Secret Key from localStorage so the next
   * fresh unlock will require the full Secret Key prompt.
   */
  function untrustDevice(): void {
    const userID = account?.user_id;
    if (!userID) return;
    clearTrustedBlob(userID);
    trustedFailCount = 0;
  }

  /**
   * True when a trusted-device blob exists for the current account.
   * Cheap (localStorage read + JSON parse); fine to call from a
   * reactive `$derived` context.
   */
  function isDeviceTrusted(): boolean {
    const userID = account?.user_id;
    if (!userID) return false;
    return readTrustedBlob(userID) !== null;
  }

  /**
   * Metadata about the current trust blob, or null when untrusted.
   * The UI surfaces `created_at` so the user can see when they last
   * trusted this device.
   */
  function trustedDeviceInfo(): { created_at: string } | null {
    const userID = account?.user_id;
    if (!userID) return null;
    const blob = readTrustedBlob(userID);
    return blob ? { created_at: blob.created_at } : null;
  }

  /**
   * Zeroize the unlocked material. Items remain in memory but their
   * decrypted payloads are dropped — the UI re-prompts for unlock
   * before showing any field value again.
   */
  function lock(): void {
    if (lockTimer) {
      clearTimeout(lockTimer);
      lockTimer = null;
    }
    crypto.zeroize(vaultKey);
    crypto.zeroize(secretKey);
    vaultKey = null;
    secretKey = null;
    decrypted = new Map();
  }

  /**
   * Reset the entire vault server-side and zeroize the local key.
   * Destructive — the caller is responsible for the confirmation
   * step before invoking.
   */
  async function reset(): Promise<number> {
    loading = true;
    error = null;
    try {
      // Drop the trust blob BEFORE we clear `account`, since the
      // localStorage key is keyed off the user_id we'd otherwise
      // lose access to.
      const userID = account?.user_id;
      const n = await api.resetVault();
      lock();
      if (userID) clearTrustedBlob(userID);
      account = null;
      items = [];
      await refreshStatus();
      return n;
    } finally {
      loading = false;
    }
  }

  async function rotateMasterPassword(
    newPassword: string,
    preset: crypto.KDFPreset = 'default',
  ): Promise<void> {
    if (!vaultKey || !secretKey) throw new Error('vault must be unlocked first');
    loading = true;
    error = null;
    try {
      const payload = await crypto.buildRotatePayload(newPassword, secretKey, vaultKey, preset);
      const acc = await api.rotatePassword(payload);
      account = acc;
      // If this device was trusted under the OLD master password,
      // re-seal the Secret Key under the NEW master password so
      // the next unlock here still works without a Secret Key
      // prompt. If we skipped this step the blob would AEAD-fail
      // and the user would hit the 3-strike auto-untrust path.
      if (readTrustedBlob(account.user_id)) {
        const blob = await crypto.sealTrustedDeviceBlob(
          secretKey,
          newPassword,
          account.kdf,
          account.user_id,
        );
        writeTrustedBlob(blob);
        trustedFailCount = 0;
      }
      resetLockTimer();
    } finally {
      loading = false;
    }
  }

  // ─── Items ──────────────────────────────────────────────────

  /**
   * Decrypt the four ciphertext slots on a server-returned item.
   * Individual AEAD failures fall through to null / empty so the
   * SPA can still render an "unreadable" tile rather than crashing
   * the list view.
   *
   * Caller must hold vaultKey; we short-circuit when locked.
   */
  async function decryptItem(item: VaultItem): Promise<DecryptedItem> {
    if (!vaultKey) {
      return { item, title: null, tags: [], hostnames: [], payload: null };
    }
    let title: string | null = null;
    if (item.encrypted_title) {
      title = await crypto.decryptString(crypto.fromBase64(item.encrypted_title), vaultKey);
    }
    let tags: string[] = [];
    if (item.encrypted_tags) {
      tags = (await crypto.decryptStringList(crypto.fromBase64(item.encrypted_tags), vaultKey)) ?? [];
    }
    let hostnames: string[] = [];
    if (item.encrypted_hostnames) {
      hostnames = (await crypto.decryptStringList(crypto.fromBase64(item.encrypted_hostnames), vaultKey)) ?? [];
    }
    let payload: VaultItemPayload | null = null;
    if (item.encrypted_payload) {
      payload = await crypto.decryptItemPayload(crypto.fromBase64(item.encrypted_payload), vaultKey);
    }
    return { item, title, tags, hostnames, payload };
  }

  /**
   * Fetch the current list of items and (best-effort) decrypt each
   * one. Individual decryption failures don't abort the load —
   * they show as an "unreadable" tile in the UI so the user can
   * still inspect / delete the corrupt entry.
   */
  async function refreshItems(filter: api.VaultListFilter = {}): Promise<void> {
    loading = true;
    error = null;
    try {
      const list = await api.listItems(filter);
      items = list;
      // Drop stale decrypted entries so the GC can reclaim them.
      decrypted = new Map();
      if (vaultKey) {
        for (const item of list) {
          decrypted.set(item.id, await decryptItem(item));
        }
        // Trigger reactivity since Map mutation doesn't reassign.
        decrypted = new Map(decrypted);
      }
      resetLockTimer();
    } finally {
      loading = false;
    }
  }

  /**
   * Create an item. The caller supplies plaintext metadata (title,
   * tags, hostnames) and the decrypted payload; the store performs
   * every AEAD operation locally before posting.
   */
  async function createItem(
    type: api.VaultItemType,
    title: string,
    payload: VaultItemPayload,
    extra: {
      tags?: string[];
      urlHostnames?: string[];
      favorite?: boolean;
    } = {},
  ): Promise<VaultItem> {
    if (!vaultKey) throw new Error('vault must be unlocked first');
    const tags = extra.tags ?? [];
    const hostnames = extra.urlHostnames ?? [];
    const titleBlob = await crypto.encryptString(title, vaultKey);
    const tagsBlob = await crypto.encryptStringList(tags, vaultKey);
    const hostsBlob = await crypto.encryptStringList(hostnames, vaultKey);
    const payloadBlob = await crypto.encryptItemPayload(payload, vaultKey);

    const item = await api.createItem({
      type,
      encrypted_title: crypto.toBase64(titleBlob),
      encrypted_tags: crypto.toBase64(tagsBlob),
      encrypted_hostnames: crypto.toBase64(hostsBlob),
      encrypted_payload: crypto.toBase64(payloadBlob),
      favorite: extra.favorite,
    });
    items = [item, ...items];
    decrypted.set(item.id, { item, title, tags, hostnames, payload });
    decrypted = new Map(decrypted);
    resetLockTimer();
    return item;
  }

  /**
   * Update an item. Only fields present in `patch` are re-encrypted
   * and sent; unspecified fields stay as they were on the server.
   * The decrypted cache is updated in parallel so the UI reflects
   * the change immediately without a re-fetch.
   */
  async function updateItem(
    id: string,
    base: { expected_version: number },
    patch: {
      title?: string;
      type?: api.VaultItemType;
      payload?: VaultItemPayload;
      tags?: string[];
      urlHostnames?: string[];
      favorite?: boolean;
      archived?: boolean;
    },
  ): Promise<VaultItem> {
    if (!vaultKey) throw new Error('vault must be unlocked first');
    const req: api.UpdateVaultItemRequest = { expected_version: base.expected_version };
    if (patch.type !== undefined) req.type = patch.type;
    if (patch.title !== undefined) {
      const blob = await crypto.encryptString(patch.title, vaultKey);
      req.encrypted_title = crypto.toBase64(blob);
    }
    if (patch.tags !== undefined) {
      const blob = await crypto.encryptStringList(patch.tags, vaultKey);
      req.encrypted_tags = crypto.toBase64(blob);
    }
    if (patch.urlHostnames !== undefined) {
      const blob = await crypto.encryptStringList(patch.urlHostnames, vaultKey);
      req.encrypted_hostnames = crypto.toBase64(blob);
    }
    if (patch.payload !== undefined) {
      const blob = await crypto.encryptItemPayload(patch.payload, vaultKey);
      req.encrypted_payload = crypto.toBase64(blob);
    }
    if (patch.favorite !== undefined) req.favorite = patch.favorite;
    if (patch.archived !== undefined) req.archived = patch.archived;

    const item = await api.updateItem(id, req);
    items = items.map(i => (i.id === item.id ? item : i));
    const prior = decrypted.get(id);
    decrypted.set(id, {
      item,
      title: patch.title !== undefined ? patch.title : prior?.title ?? null,
      tags: patch.tags !== undefined ? patch.tags : prior?.tags ?? [],
      hostnames: patch.urlHostnames !== undefined ? patch.urlHostnames : prior?.hostnames ?? [],
      payload: patch.payload !== undefined ? patch.payload : prior?.payload ?? null,
    });
    decrypted = new Map(decrypted);
    resetLockTimer();
    return item;
  }

  async function softDeleteItem(id: string): Promise<void> {
    await api.softDeleteItem(id);
    items = items.filter(i => i.id !== id);
    decrypted.delete(id);
    decrypted = new Map(decrypted);
  }

  async function purgeItem(id: string): Promise<void> {
    await api.purgeItem(id);
    items = items.filter(i => i.id !== id);
    decrypted.delete(id);
    decrypted = new Map(decrypted);
  }

  async function restoreItem(id: string): Promise<VaultItem> {
    const item = await api.restoreItem(id);
    items = [item, ...items.filter(i => i.id !== id)];
    return item;
  }

  function isUnlocked(): boolean {
    return vaultKey !== null;
  }

  /**
   * allTags returns the union of every decrypted tag across all
   * loaded items, lower-cased and deduplicated. Used by the list
   * UI's tag filter chip/autocomplete since the server no longer
   * holds cleartext tags.
   */
  function allTags(): string[] {
    const seen = new Set<string>();
    for (const d of decrypted.values()) {
      for (const t of d.tags) {
        const tt = t.trim();
        if (tt) seen.add(tt);
      }
    }
    return Array.from(seen).sort((a, b) => a.localeCompare(b));
  }

  return {
    get status() { return status; },
    get account() { return account; },
    get items() { return items; },
    get decrypted() { return decrypted; },
    get loading() { return loading; },
    get error() { return error; },
    isUnlocked,
    allTags,
    refreshStatus,
    refreshAccount,
    setup,
    unlock,
    lock,
    reset,
    rotateMasterPassword,
    refreshItems,
    createItem,
    updateItem,
    softDeleteItem,
    purgeItem,
    restoreItem,
    installActivityWatcher,
    trustDevice,
    untrustDevice,
    isDeviceTrusted,
    trustedDeviceInfo,
  };
}

export const vaultStore = createVaultStore();

/**
 * useVaultActivityWatcher hooks the global activity watcher onto a
 * component lifecycle. Call from the root vault component's onMount;
 * the returned teardown is invoked automatically by Svelte's
 * onDestroy when the component unmounts.
 */
export function useVaultActivityWatcher(): void {
  onMount(() => vaultStore.installActivityWatcher());
}
