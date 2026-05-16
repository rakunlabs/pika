<script lang="ts">
  import { Lock, Loader2, Eye, EyeOff } from 'lucide-svelte';
  import { keymgrStore } from '@/lib/store/keymgr.svelte';
  import { appStore } from '@/lib/store/store.svelte';

  // Full-screen takeover shown when the server's at-rest encryption
  // key is initialized (verifier on disk) but locked (no live key
  // in memory). Pre-condition is enforced by App.svelte — this
  // component is only mounted when serverLocked is true.
  //
  // We never render the "initialize" flow here anymore: setting up
  // the server key for the first time is an explicit opt-in from
  // Settings, not a forced takeover on a brand-new install. That
  // matches the legacy "encryption is opt-in" UX while still
  // forcing a manual unlock on every restart once it IS turned on.

  const busy = $derived(keymgrStore.busy);
  const error = $derived(keymgrStore.error);

  // Form state. Cleared on successful submit so a back-button or
  // tab-switch later doesn't leave the key sitting in the input.
  let key = $state('');
  let showKey = $state(false);

  // Local validation surface. We don't push these through
  // keymgrStore.error because the store's error field is reserved
  // for server-side responses; mixing the two would let a stale
  // local mismatch warning override a real "wrong key" toast.
  let localError = $state<string | null>(null);

  function clearError() {
    localError = null;
    keymgrStore.setError(null);
  }

  async function onSubmit(e: Event) {
    e.preventDefault();
    if (!key) {
      localError = 'Key is required';
      return;
    }
    localError = null;

    const ok = await keymgrStore.unlock(key);
    if (ok) {
      key = '';
      // After unlock, refresh the rest of the app state — the
      // user's session is intact but `loadInfo` may have failed
      // earlier with 503; re-fetch so the navbar / capabilities
      // populate.
      await appStore.loadInfo();
    }
  }

  // Sign the user out of the SPA without unlocking. Useful when an
  // operator realizes they're on the wrong account before the unlock
  // succeeds.
  async function onLogout() {
    await appStore.logout();
  }
</script>

<!-- Full-viewport overlay so no app chrome leaks through. We use a
     warm-tinted dark background to visually separate this from the
     normal app shell — the user should immediately see "this is a
     different mode, the server isn't fully running". -->
<div class="fixed inset-0 z-50 flex items-center justify-center bg-slate-100 dark:bg-warm-900 p-4 overflow-y-auto">
  <div class="w-full max-w-md">
    <!-- Branding row -->
    <div class="flex items-center justify-center gap-2 mb-6 text-slate-700 dark:text-slate-200">
      <Lock size={20} />
      <span class="text-lg font-semibold">Pika</span>
    </div>

    <div class="bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg p-6 shadow-sm">
      <!-- Unlock mode: verifier exists on disk; the operator is
           returning after a restart. Server validates the key
           against the verifier ciphertext. -->
      <h1 class="text-base font-semibold text-slate-800 dark:text-slate-100 mb-1 flex items-center gap-2">
        <Lock size={16} class="text-accent-600 dark:text-accent-400" />
        Server is locked
      </h1>
      <p class="text-xs text-slate-500 dark:text-slate-400 mb-4">
        Enter the server encryption key to bring Pika online. The key is held in memory only — every restart will require this step.
      </p>

      <form onsubmit={onSubmit} class="space-y-3">
        <div>
          <label class="block text-[10px] font-medium uppercase tracking-wider text-slate-500 mb-1" for="key-input">
            Server key
          </label>
          <div class="relative">
            <!-- svelte-ignore a11y_autofocus -->
            <input
              id="key-input"
              type={showKey ? 'text' : 'password'}
              bind:value={key}
              oninput={clearError}
              autocomplete="current-password"
              autofocus
              disabled={busy}
              class="w-full pl-3 pr-10 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500 disabled:opacity-50"
              placeholder="Enter server key"
            />
            <button
              type="button"
              onclick={() => (showKey = !showKey)}
              class="absolute top-1/2 right-2 -translate-y-1/2 p-1 rounded text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 cursor-pointer"
              aria-label={showKey ? 'Hide key' : 'Show key'}
              tabindex="-1"
            >
              {#if showKey}<EyeOff size={13} />{:else}<Eye size={13} />{/if}
            </button>
          </div>
        </div>

        {#if localError || error}
          <div class="p-2.5 rounded border border-red-300 dark:border-red-700 bg-red-50 dark:bg-red-950/40 text-xs text-red-700 dark:text-red-300">
            {localError || error}
          </div>
        {/if}

        <button
          type="submit"
          disabled={busy || !key}
          class="w-full flex items-center justify-center gap-2 px-3 py-2 text-sm rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
        >
          {#if busy}
            <Loader2 size={14} class="animate-spin" />
            Working...
          {:else}
            Unlock
          {/if}
        </button>
      </form>

      <!-- Bottom row: identity + escape hatch. The user is logged in
           (they cleared /login already to reach this screen) so we
           expose a logout in case they realize they need a different
           admin account. -->
      <div class="mt-4 pt-3 border-t border-slate-100 dark:border-warm-700 flex items-center justify-between text-[11px] text-slate-500 dark:text-slate-400">
        <span class="truncate">
          Signed in as
          <span class="font-medium text-slate-600 dark:text-slate-300">{appStore.identity?.subject ?? 'unknown'}</span>
        </span>
        <button
          type="button"
          onclick={onLogout}
          class="hover:text-slate-700 dark:hover:text-slate-200 underline cursor-pointer"
        >
          Sign out
        </button>
      </div>
    </div>

    <p class="text-center text-[10px] text-slate-400 dark:text-slate-500 mt-4">
      The server cannot decrypt sensitive data until you unlock it.
    </p>
  </div>
</div>
