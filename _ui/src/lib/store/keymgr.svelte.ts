// Server-key (at-rest encryption) lifecycle UI state.
//
// The Pika server starts in a "locked" state on every boot — the
// at-rest key is no longer read from config; an admin enters it
// through the UI (or POST /api/v1/key/unlock). Until that happens
// every non-allowlisted request returns 503 with header
// `X-Pika-Locked: true`.
//
// This store owns three things:
//
//   1. The current server-key status (Initialized + Unlocked) used
//      by App.svelte to decide between the unlock screen and the
//      normal app shell.
//   2. The mutation calls (initialize / unlock / lock / rotate) that
//      the UnlockScreen + Settings pages dispatch.
//   3. An axios response interceptor that watches for the
//      X-Pika-Locked header on background requests so a server that
//      gets locked WHILE the user is browsing (e.g. another admin
//      called /lock) immediately surfaces the unlock screen rather
//      than letting the user stare at a broken page full of 503s.
//
// The store deliberately does NOT do polling. The status is
// re-fetched on (a) initial app boot, (b) any 503 with the locked
// header, and (c) explicit user action (after unlock/lock/rotate).
// Polling would add request volume without buying anything — the
// header signal is sufficient and lower-latency.

import axios from 'axios';

export interface KeyStatus {
  initialized: boolean;
  unlocked: boolean;
}

function createKeymgrStore() {
  // null = not yet fetched (App.svelte shows a loader); otherwise
  // the latest server-reported state.
  let status = $state<KeyStatus | null>(null);

  // True while a status request is in flight. The UnlockScreen uses
  // this to disable the form during the round-trip after a user
  // clicks "Unlock" (the unlock call itself sets `busy` instead, see
  // below).
  let loading = $state(false);

  // True while a mutation (initialize / unlock / lock / rotate) is
  // in flight. Separate from `loading` so the unlock form's button
  // spinner doesn't conflict with the boot-time status fetch.
  let busy = $state(false);

  // Last error message from a mutation. The unlock screen renders
  // it inline; clearing it is the caller's responsibility (call
  // setError(null) before retrying so the message disappears as
  // soon as the user starts typing).
  let error = $state<string | null>(null);

  function setError(msg: string | null) {
    error = msg;
  }

  /**
   * Fetch /api/v1/key/status. The endpoint is on the lockgate
   * allowlist so this works whether the server is locked or not.
   * Network errors leave the previous status in place — App.svelte's
   * boot path retries on the next loadInfo().
   */
  async function refreshStatus(): Promise<void> {
    loading = true;
    try {
      const res = await axios.get<KeyStatus>('/api/v1/key/status');
      status = res.data;
    } catch (err) {
      // Don't blow away a known-good prior status on a transient
      // failure. If the call genuinely fails, App.svelte's loader
      // stays up; the user can refresh.
      console.warn('key status fetch failed', err);
    } finally {
      loading = false;
    }
  }

  /**
   * Set the very first server key. Server returns 409 if a verifier
   * already exists (the SPA should fall back to the unlock form on
   * that signal). Sets the status to {initialized:true, unlocked:true}
   * on success without re-querying since the server's response
   * implies that exact state.
   */
  async function initialize(key: string): Promise<boolean> {
    setError(null);
    busy = true;
    try {
      await axios.post('/api/v1/key/initialize', { key });
      status = { initialized: true, unlocked: true };
      return true;
    } catch (err: any) {
      const msg = err?.response?.data?.message ?? err?.message ?? 'Initialize failed';
      setError(msg);
      // 409 means "already initialized" — refresh status so the UI
      // can switch to the unlock form on the next render.
      if (err?.response?.status === 409) {
        await refreshStatus();
      }
      return false;
    } finally {
      busy = false;
    }
  }

  /**
   * Unlock with the supplied key. Wrong key → 403 → returns false
   * with `error` populated. Network or 5xx → throws so the UI can
   * distinguish "your key was wrong" from "the server is down".
   */
  async function unlock(key: string): Promise<boolean> {
    setError(null);
    busy = true;
    try {
      await axios.post('/api/v1/key/unlock', { key });
      status = { initialized: true, unlocked: true };
      return true;
    } catch (err: any) {
      const code = err?.response?.status;
      // 400 / 401 / 403 are all "user-facing rejection" cases — the
      // server told us why and we should show that to the user
      // without further alarm.
      if (code === 400 || code === 401 || code === 403) {
        const msg = err?.response?.data?.message ?? 'Wrong key';
        setError(msg);
        return false;
      }
      // Anything else is unexpected — surface it but don't pretend
      // the unlock succeeded.
      const msg = err?.response?.data?.message ?? err?.message ?? 'Unlock failed';
      setError(msg);
      return false;
    } finally {
      busy = false;
    }
  }

  /**
   * Manual lock — admin-only (server enforces CapSettingsManage).
   * After success the next interceptor 503 will re-show the unlock
   * screen.
   */
  async function lock(): Promise<boolean> {
    setError(null);
    busy = true;
    try {
      await axios.post('/api/v1/key/lock');
      if (status) status = { ...status, unlocked: false };
      return true;
    } catch (err: any) {
      setError(err?.response?.data?.message ?? 'Lock failed');
      return false;
    } finally {
      busy = false;
    }
  }

  /**
   * Rotate the server key. Validates `current` against the on-disk
   * verifier before swapping. Wrong current → 403, returns false.
   */
  async function rotate(current: string, next: string): Promise<boolean> {
    setError(null);
    busy = true;
    try {
      await axios.post('/api/v1/key/rotate', {
        current_key: current,
        new_key: next,
      });
      // Server stays unlocked under the new key.
      status = { initialized: true, unlocked: true };
      return true;
    } catch (err: any) {
      setError(err?.response?.data?.message ?? 'Rotate failed');
      return false;
    } finally {
      busy = false;
    }
  }

  // Axios response interceptor: any background request that comes
  // back with X-Pika-Locked: true means the server is locked from
  // under us. Update the store and let App.svelte's reactive switch
  // handle the rest. We deliberately don't surface a toast here —
  // the unlock screen IS the surface.
  //
  // Skip the status endpoint itself; otherwise we'd recurse on every
  // refresh (which always carries the header when locked).
  axios.interceptors.response.use(
    (response) => response,
    (error) => {
      const headers = error?.response?.headers;
      const url: string = error?.config?.url ?? '';
      const isLocked = headers && (headers['x-pika-locked'] === 'true' || headers['X-Pika-Locked'] === 'true');
      if (isLocked && !url.includes('/api/v1/key/')) {
        if (status?.unlocked !== false) {
          status = { initialized: status?.initialized ?? true, unlocked: false };
        }
      }
      return Promise.reject(error);
    },
  );

  return {
    get status() { return status; },
    get loading() { return loading; },
    get busy() { return busy; },
    get error() { return error; },
    setError,
    refreshStatus,
    initialize,
    unlock,
    lock,
    rotate,
  };
}

export const keymgrStore = createKeymgrStore();
