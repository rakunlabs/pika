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
import { clearCopiedSecret } from './clipboard';
import type { VaultItem } from './api';
import type { VaultItemPayload, TrustedDeviceBlob } from './crypto';
import { apiErrorStatus, apiErrorMessage } from '@/lib/api/client';

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
  // folder is the decrypted folder label (e.g. "Personal", "Work").
  // null means "AEAD failed for this slot" — the row exists but the
  // folder ciphertext was unreadable. Empty string means "no
  // folder" (intentional absence: server returned no
  // encrypted_folder ciphertext at all).
  folder: string | null;
  payload: VaultItemPayload | null;
}

/** Comparable form of a payload: an empty note and a missing note are equal. */
function normalizePayload(p: VaultItemPayload): VaultItemPayload {
  return { fields: p.fields ?? [], notes: p.notes || undefined };
}

function createVaultStore() {
  // Server-side state mirror.
  let status = $state<api.VaultStatus | null>(null);
  let account = $state<crypto.VaultAccountView | null>(null);

  // The freshly generated Secret Key, kept in store state ONLY for
  // the brief window between setup() returning and the user
  // acknowledging the Emergency Kit. We hold it on the store (not
  // local component state) for two reasons:
  //   1. status.initialized flips to true inside setup(), which
  //      would otherwise let the page-level switch unmount the
  //      VaultSetup component mid-flow — local state would be lost
  //      to a remount and the user would see a blank setup form
  //      again with no Secret Key in sight.
  //   2. Routes / hot reloads / accidental refreshes during the
  //      Emergency Kit window all keep their semantics: the kit
  //      stays available until acknowledgeKit() clears it.
  // Cleared by acknowledgeKit() once the user clicks Continue.
  let pendingSecretKey = $state<crypto.SecretKey | null>(null);

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

  // Reactive countdown. `lockDeadline` is the wall-clock ms after
  // which the vault will auto-lock; `nowTick` is bumped once a
  // second by `tickInterval` while the vault is unlocked so that
  // `remainingLockSeconds` re-derives. Both are deliberately
  // separated from `lockTimer` (which is the authoritative one-shot
  // trigger) so the UI can show a countdown without ever risking
  // a lock-vs-display drift.
  let lockDeadline = $state<number>(0);
  let nowTick = $state<number>(Date.now());
  let tickInterval: ReturnType<typeof setInterval> | null = null;

  const DEFAULT_LOCK_SECONDS = 15 * 60;

  // Server-managed vaults have no master password: the server hands
  // the vault key to any signed-in session, so an idle lock would only
  // force a pointless re-fetch. They stay open for the page lifetime.
  function isServerManaged(): boolean {
    return (account?.key_mode ?? status?.key_mode) === 'server';
  }

  function deploymentKeyMode(): api.VaultKeyMode {
    return status?.deployment_key_mode ?? 'user';
  }

  /** The admin switched modes and this vault must be converted to match. */
  function needsConversion(): boolean {
    if (!status?.initialized || !status.key_mode) return false;
    return status.key_mode !== deploymentKeyMode();
  }

  function lockTimeoutSeconds(): number {
    const fromAccount = account?.session_lock_seconds ?? 0;
    return fromAccount > 0 ? fromAccount : DEFAULT_LOCK_SECONDS;
  }

  function resetLockTimer() {
    if (!vaultKey) return;
    if (isServerManaged()) {
      stopLockTimers();
      return;
    }
    if (lockTimer) clearTimeout(lockTimer);
    const ttlMs = lockTimeoutSeconds() * 1000;
    lockDeadline = Date.now() + ttlMs;
    lockTimer = setTimeout(() => {
      lock();
    }, ttlMs);
    // Lazy-start the once-per-second tick. We only run it while the
    // vault is unlocked; lock() stops it.
    if (!tickInterval) {
      nowTick = Date.now();
      tickInterval = setInterval(() => {
        nowTick = Date.now();
      }, 1000);
    }
  }

  /**
   * remainingLockSeconds is the user-visible countdown to the next
   * idle auto-lock. Returns 0 when the vault is locked or the
   * deadline has passed. Re-derives every second via `nowTick`.
   */
  function remainingLockSeconds(): number {
    if (!vaultKey || lockDeadline === 0) return 0;
    const remaining = Math.max(0, lockDeadline - nowTick);
    return Math.ceil(remaining / 1000);
  }

  /**
   * Install global listeners so any keyboard, pointer, wheel, touch or
   * scroll activity resets the auto-lock timer. Listeners use capture
   * because `scroll` doesn't bubble from inner scroll containers.
   * Returns a teardown function — Svelte 5 onMount/onDestroy callers
   * must invoke it on unmount.
   */
  function installActivityWatcher(): () => void {
    // Throttled: wheel/scroll fire at frame rate and each reset writes
    // reactive state (lockDeadline).
    let last = 0;
    const handler = () => {
      const now = Date.now();
      if (now - last < 1000) return;
      last = now;
      resetLockTimer();
    };
    const events = ['keyup', 'mousedown', 'wheel', 'touchstart', 'scroll'] as const;
    const opts: AddEventListenerOptions = { passive: true, capture: true };
    for (const ev of events) document.addEventListener(ev, handler, opts);
    return () => {
      for (const ev of events) document.removeEventListener(ev, handler, opts);
    };
  }

  // ─── Hidden-tab grace period ────────────────────────────────
  //
  // Background: prior to this change, the App-level visibility
  // listener locked the vault the instant `document.hidden` flipped
  // true. That made the vault unusable in real workflows where the
  // user briefly tabs away to copy a TOTP into another window or
  // pastes a password into a native client.
  //
  // The grace period gives the user N seconds to come back. If they
  // do, we cancel the pending lock and restart the activity timer.
  // If they don't, the vault locks exactly as before.
  //
  // The setting is per-DEVICE (localStorage) rather than per-account
  // because "lock when I tab away" is a device-trust question, not
  // a vault-secrets question — the same user might want instant
  // lock on a shared workstation and 5 minutes on their personal
  // laptop. We don't sync it to the server.
  //
  // -1 means "never lock when hidden" — the idle timer still
  //  applies, so a truly forgotten tab still locks eventually.
  //  0  means "lock instantly on hidden" (legacy behavior).
  //  >0 is the grace period in seconds.
  const HIDDEN_GRACE_KEY = 'pika.vault.hidden_grace_seconds';
  const DEFAULT_HIDDEN_GRACE_SECONDS = 60;

  function loadHiddenGraceSeconds(): number {
    try {
      const raw = localStorage.getItem(HIDDEN_GRACE_KEY);
      if (raw === null) return DEFAULT_HIDDEN_GRACE_SECONDS;
      const n = Number.parseInt(raw, 10);
      // -1 (never), 0 (instant), or any positive seconds value.
      if (!Number.isFinite(n)) return DEFAULT_HIDDEN_GRACE_SECONDS;
      if (n < -1) return DEFAULT_HIDDEN_GRACE_SECONDS;
      return n;
    } catch {
      return DEFAULT_HIDDEN_GRACE_SECONDS;
    }
  }

  let hiddenGraceSeconds = $state<number>(loadHiddenGraceSeconds());
  let hiddenLockTimer: ReturnType<typeof setTimeout> | null = null;

  function setHiddenGraceSeconds(value: number): void {
    const clamped = Number.isFinite(value) ? Math.max(-1, Math.floor(value)) : DEFAULT_HIDDEN_GRACE_SECONDS;
    hiddenGraceSeconds = clamped;
    try {
      localStorage.setItem(HIDDEN_GRACE_KEY, String(clamped));
    } catch {
      // localStorage may be unavailable; the in-memory value still
      // takes effect for the current session.
    }
  }

  /**
   * notifyVisibilityChange is called by the App-level
   * visibilitychange listener. When the document becomes hidden we
   * either lock immediately (grace = 0), schedule a delayed lock
   * (grace > 0), or do nothing (grace = -1, "never"). When the
   * document becomes visible again we cancel any pending grace
   * lock and restart the activity timer.
   *
   * Idempotent — safe to call when the vault is already locked or
   * when called twice in a row with the same `hidden` value.
   */
  function notifyVisibilityChange(hidden: boolean): void {
    if (isServerManaged()) return;
    if (hidden) {
      if (!vaultKey) return;
      if (hiddenGraceSeconds < 0) {
        // "Never lock when hidden" — leave the idle timer alone.
        return;
      }
      if (hiddenGraceSeconds === 0) {
        lock();
        return;
      }
      // Schedule a delayed lock. If the user comes back before it
      // fires, notifyVisibilityChange(false) will cancel it.
      if (hiddenLockTimer) clearTimeout(hiddenLockTimer);
      hiddenLockTimer = setTimeout(() => {
        hiddenLockTimer = null;
        lock();
      }, hiddenGraceSeconds * 1000);
    } else {
      if (hiddenLockTimer) {
        clearTimeout(hiddenLockTimer);
        hiddenLockTimer = null;
      }
      // If the vault is still unlocked, give the user a fresh idle
      // window starting from "now" rather than letting the original
      // (pre-hidden) deadline tick down — they just came back, treat
      // the return as activity.
      if (vaultKey) resetLockTimer();
    }
  }

  // ─── Status / Account ───────────────────────────────────────

  async function refreshStatus(): Promise<void> {
    try {
      status = await api.getStatus();
    } catch (err) {
      error = apiErrorMessage(err, 'failed to read vault status');
    }
  }

  async function refreshAccount(): Promise<void> {
    try {
      account = await api.getAccount();
    } catch (err) {
      error = apiErrorMessage(err, 'failed to read vault account');
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
      // CRITICAL: set pendingSecretKey BEFORE refreshStatus(). The
      // refreshStatus call flips status.initialized = true, which
      // triggers the page switch in Vault.svelte to re-evaluate.
      // If pendingSecretKey isn't already populated at that moment,
      // VaultSetup gets unmounted and the user lands on the
      // unlocked layout having never seen their Secret Key.
      pendingSecretKey = result.secretKey;
      await refreshStatus();
      resetLockTimer();
      return result.secretKey;
    } finally {
      loading = false;
    }
  }

  /**
   * Called by VaultSetup once the user has confirmed they saved
   * their Secret Key. Drops the kit reference from store state so
   * the page-level switch can finally advance to the unlocked
   * layout.
   */
  function acknowledgeKit(): void {
    pendingSecretKey = null;
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
      } catch (err) {
        if (apiErrorStatus(err) === 401) {
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
    stopLockTimers();
    crypto.zeroize(vaultKey);
    crypto.zeroize(secretKey);
    vaultKey = null;
    secretKey = null;
    decrypted = new Map();
    void clearCopiedSecret();
  }

  function stopLockTimers(): void {
    if (lockTimer) {
      clearTimeout(lockTimer);
      lockTimer = null;
    }
    if (hiddenLockTimer) {
      // Lock-on-hidden may already have fired; clearing a finished
      // timer is a no-op so this is safe either way.
      clearTimeout(hiddenLockTimer);
      hiddenLockTimer = null;
    }
    if (tickInterval) {
      clearInterval(tickInterval);
      tickInterval = null;
    }
    lockDeadline = 0;
  }

  // ─── Server-managed vaults ──────────────────────────────────

  /** Create a vault whose key is generated and sealed by the server. */
  async function setupServer(sessionLockSeconds = 0): Promise<void> {
    loading = true;
    error = null;
    try {
      const res = await api.setupServer(sessionLockSeconds);
      account = res.account;
      vaultKey = crypto.fromBase64(res.vault_key);
      secretKey = null;
      await refreshStatus();
    } finally {
      loading = false;
    }
  }

  /** Fetch the vault key of a server-managed vault and open it. */
  async function openServerManaged(): Promise<void> {
    loading = true;
    error = null;
    try {
      await crypto.ready();
      if (!account) await refreshAccount();
      const key = await api.getServerKey();
      vaultKey = crypto.fromBase64(key);
      secretKey = null;
    } finally {
      loading = false;
    }
  }

  /**
   * Hand the unlocked vault key to the server so it can be sealed
   * with the server encryption key. Called right after a master-password
   * unlock when the admin has switched the deployment to server mode.
   */
  async function convertToServer(): Promise<void> {
    if (!vaultKey || !secretKey || !account) throw new Error('vault must be unlocked first');
    const userID = account.user_id;
    const skHash = await crypto.hashSecretKey(secretKey);
    account = await api.convertToServer(crypto.toBase64(vaultKey), crypto.toBase64(skHash));
    clearTrustedBlob(userID);
    trustedFailCount = 0;
    crypto.zeroize(secretKey);
    secretKey = null;
    stopLockTimers();
    await refreshStatus();
  }

  /**
   * Re-protect a server-managed vault with a master password. Keeps
   * the existing vault key so items stay readable, and surfaces the new
   * Secret Key through pendingSecretKey like a fresh setup.
   */
  async function convertToUser(
    masterPassword: string,
    preset: crypto.KDFPreset = 'default',
    sessionLockSeconds: number = 900,
  ): Promise<crypto.SecretKey> {
    loading = true;
    error = null;
    try {
      if (!vaultKey) {
        const key = await api.getServerKey();
        vaultKey = crypto.fromBase64(key);
      }
      const result = await crypto.buildSetup(masterPassword, preset, sessionLockSeconds, vaultKey);
      account = await api.convertToUser(result.payload);
      secretKey = result.secretKey.bytes;
      pendingSecretKey = result.secretKey;
      await refreshStatus();
      resetLockTimer();
      return result.secretKey;
    } finally {
      loading = false;
    }
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
      return { item, title: null, tags: [], hostnames: [], folder: '', payload: null };
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
    // Folder: absent ciphertext → empty string ("no folder"); failed
    // decryption → null so the UI can render "unreadable" without
    // accidentally bucketing the item into a real folder.
    let folder: string | null = '';
    if (item.encrypted_folder) {
      folder = await crypto.decryptString(crypto.fromBase64(item.encrypted_folder), vaultKey);
    }
    let payload: VaultItemPayload | null = null;
    if (item.encrypted_payload) {
      payload = await crypto.decryptItemPayload(crypto.fromBase64(item.encrypted_payload), vaultKey);
    }
    return { item, title, tags, hostnames, folder, payload };
  }

  /**
   * Decrypt a base64-encoded payload ciphertext with the live
   * vault key. Used by the editor's "field history" flyout to
   * inspect snapshots from vault_item_versions without ever
   * exposing vaultKey outside the store. Returns null when the
   * vault is locked or the ciphertext fails AEAD (e.g. the
   * snapshot predates the current key after a master-password
   * rotation that didn't re-key history).
   */
  async function decryptPayloadBytes(base64: string): Promise<VaultItemPayload | null> {
    if (!vaultKey || !base64) return null;
    try {
      return await crypto.decryptItemPayload(crypto.fromBase64(base64), vaultKey);
    } catch {
      return null;
    }
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
    // Clear the visible list IMMEDIATELY so the UI doesn't paint the
    // previous filter's items while we wait for the new fetch. The
    // most user-visible symptom of leaving the old list up was the
    // archived/trash tabs briefly showing active items before the
    // server response replaced them — a confusing "are my items
    // showing in the wrong place?" experience even though the
    // server filtered correctly.
    items = [];
    decrypted = new Map();
    try {
      const list = await api.listItems(filter);
      items = list;
      if (vaultKey) {
        const next = new Map<string, DecryptedItem>();
        for (const item of list) {
          next.set(item.id, await decryptItem(item));
        }
        decrypted = next;
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
      folder?: string;
    } = {},
  ): Promise<VaultItem> {
    if (!vaultKey) throw new Error('vault must be unlocked first');
    const tags = extra.tags ?? [];
    const hostnames = extra.urlHostnames ?? [];
    // Folder is encrypted with the same single-string AEAD as the
    // title. A trimmed empty string means "no folder" — we skip the
    // encryption in that case so the server stores nil rather than
    // a ciphertext of an empty string.
    const folder = (extra.folder ?? '').trim();
    const titleBlob = await crypto.encryptString(title, vaultKey);
    const tagsBlob = await crypto.encryptStringList(tags, vaultKey);
    const hostsBlob = await crypto.encryptStringList(hostnames, vaultKey);
    const payloadBlob = await crypto.encryptItemPayload(payload, vaultKey);

    const createReq: api.CreateVaultItemRequest = {
      type,
      encrypted_title: crypto.toBase64(titleBlob),
      encrypted_tags: crypto.toBase64(tagsBlob),
      encrypted_hostnames: crypto.toBase64(hostsBlob),
      encrypted_payload: crypto.toBase64(payloadBlob),
      favorite: extra.favorite,
    };
    if (folder) {
      const folderBlob = await crypto.encryptString(folder, vaultKey);
      createReq.encrypted_folder = crypto.toBase64(folderBlob);
    }
    const item = await api.createItem(createReq);
    items = [item, ...items];
    decrypted.set(item.id, { item, title, tags, hostnames, folder, payload });
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
      // folder mirrors the server's two-input shape: a non-empty
      // string is the new folder name (encrypted on send); an empty
      // string CLEARS the folder (sends clear_folder=true);
      // undefined leaves it as-is.
      folder?: string;
    },
  ): Promise<VaultItem> {
    if (!vaultKey) throw new Error('vault must be unlocked first');
    patch = { ...patch };
    const prior = decrypted.get(id);
    const current = items.find(i => i.id === id) ?? prior?.item;

    // Ciphertexts use a fresh nonce every time, so the server can't tell
    // that a re-encrypted value is unchanged. Drop every field whose
    // plaintext matches what we already hold; otherwise each save would
    // record a new version and a useless history entry.
    if (prior && current) {
      const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
      if (patch.title !== undefined && prior.title !== null && patch.title === prior.title) patch.title = undefined;
      if (patch.tags !== undefined && same(patch.tags, prior.tags)) patch.tags = undefined;
      if (patch.urlHostnames !== undefined && same(patch.urlHostnames, prior.hostnames)) patch.urlHostnames = undefined;
      if (patch.folder !== undefined && patch.folder.trim() === (prior.folder ?? '').trim()) patch.folder = undefined;
      if (patch.payload !== undefined && prior.payload !== null && same(normalizePayload(patch.payload), normalizePayload(prior.payload))) {
        patch.payload = undefined;
      }
      if (patch.type !== undefined && patch.type === current.type) patch.type = undefined;
      if (patch.favorite !== undefined && patch.favorite === !!current.favorite) patch.favorite = undefined;
      if (patch.archived !== undefined && patch.archived === !!current.archived) patch.archived = undefined;
      if (Object.values(patch).every(v => v === undefined)) {
        resetLockTimer();
        return current;
      }
    }

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
    if (patch.folder !== undefined) {
      const trimmed = patch.folder.trim();
      if (trimmed === '') {
        req.clear_folder = true;
      } else {
        const blob = await crypto.encryptString(trimmed, vaultKey);
        req.encrypted_folder = crypto.toBase64(blob);
      }
    }
    if (patch.payload !== undefined) {
      const blob = await crypto.encryptItemPayload(patch.payload, vaultKey);
      req.encrypted_payload = crypto.toBase64(blob);
    }
    if (patch.favorite !== undefined) req.favorite = patch.favorite;
    if (patch.archived !== undefined) req.archived = patch.archived;

    const item = await api.updateItem(id, req);
    items = items.map(i => (i.id === item.id ? item : i));
    decrypted.set(id, {
      item,
      title: patch.title !== undefined ? patch.title : prior?.title ?? null,
      tags: patch.tags !== undefined ? patch.tags : prior?.tags ?? [],
      hostnames: patch.urlHostnames !== undefined ? patch.urlHostnames : prior?.hostnames ?? [],
      folder: patch.folder !== undefined ? patch.folder.trim() : prior?.folder ?? '',
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

  /**
   * Record that the user just used an item (copied a value). Updates
   * last_used_at locally right away; the server write is best-effort.
   */
  async function touchItem(id: string): Promise<void> {
    const now = new Date().toISOString();
    items = items.map(i => (i.id === id ? { ...i, last_used_at: now } : i));
    const d = decrypted.get(id);
    if (d) {
      decrypted.set(id, { ...d, item: { ...d.item, last_used_at: now } });
      decrypted = new Map(decrypted);
    }
    resetLockTimer();
    try {
      await api.touchItem(id);
    } catch {
      // Usage tracking only feeds the "recently used" sort.
    }
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

  /**
   * allFolders returns the union of every decrypted folder label
   * across all loaded items. Empty / null folders are filtered out
   * (those are the "no folder" rows that show up in the All view).
   * Case-preserved so the user sees "Work" rather than "work" in
   * the sidebar, but deduplication is case-insensitive so the same
   * label typed two different ways doesn't appear twice.
   */
  function allFolders(): string[] {
    const seen = new Map<string, string>();
    for (const d of decrypted.values()) {
      const f = (d.folder ?? '').trim();
      if (!f) continue;
      const key = f.toLowerCase();
      if (!seen.has(key)) seen.set(key, f);
    }
    return Array.from(seen.values()).sort((a, b) => a.localeCompare(b));
  }

  return {
    get status() { return status; },
    get account() { return account; },
    get items() { return items; },
    get decrypted() { return decrypted; },
    get loading() { return loading; },
    get error() { return error; },
    get remainingLockSeconds() { return remainingLockSeconds(); },
    get lockTimeoutSeconds() { return lockTimeoutSeconds(); },
    get hiddenGraceSeconds() { return hiddenGraceSeconds; },
    get pendingSecretKey() { return pendingSecretKey; },
    get isServerManaged() { return isServerManaged(); },
    get deploymentKeyMode() { return deploymentKeyMode(); },
    get needsConversion() { return needsConversion(); },
    isUnlocked,
    allTags,
    allFolders,
    acknowledgeKit,
    decryptPayloadBytes,
    refreshStatus,
    refreshAccount,
    setup,
    setupServer,
    openServerManaged,
    convertToServer,
    convertToUser,
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
    touchItem,
    installActivityWatcher,
    notifyVisibilityChange,
    setHiddenGraceSeconds,
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
