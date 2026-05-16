<script lang="ts">
  import { onMount } from "svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import { Eye, EyeOff, RotateCw, AlertTriangle, Lock, KeyRound } from "lucide-svelte";
  import { keymgrStore } from "@/lib/store/keymgr.svelte";

  // Server-key management panel. Two modes driven by
  // keymgrStore.status.initialized:
  //
  //   - Not initialized: render an opt-in form. Choosing a key
  //     here writes the verifier and unlocks the server in one
  //     step. From this point on every restart requires unlock.
  //
  //   - Initialized: render rotate + lock-now. The full-screen
  //     unlock takeover handles the every-restart unlock flow; this
  //     panel is just the steady-state admin surface.
  //
  // We deliberately do NOT auto-redirect after initialize. Once the
  // verifier is on disk the operator can keep working (the manager
  // is left unlocked by the initialize call). On the NEXT restart
  // the unlock screen will appear.

  // Status refresh on mount so that opening Settings → this panel
  // never shows stale data from an earlier route.
  onMount(() => {
    keymgrStore.refreshStatus();
  });

  const status = $derived(keymgrStore.status);
  const initialized = $derived(status?.initialized === true);
  const busy = $derived(keymgrStore.busy);

  // ─── Initialize form ───────────────────────────────────────
  let initKey = $state('');
  let initConfirm = $state('');
  let showInit = $state(false);

  // ─── Rotate form ───────────────────────────────────────────
  let currentKey = $state('');
  let newKey = $state('');
  let confirmKey = $state('');
  let showCurrent = $state(false);
  let showNew = $state(false);

  // Local validation surface — kept separate from
  // keymgrStore.error so a typo here doesn't override the
  // server-side rejection message.
  let localError = $state<string | null>(null);

  function clearLocal() {
    localError = null;
    keymgrStore.setError(null);
  }

  async function onInitialize(e: Event) {
    e.preventDefault();
    if (!initKey) {
      localError = 'Key is required';
      return;
    }
    if (initKey !== initConfirm) {
      localError = 'Keys do not match';
      return;
    }
    localError = null;
    const ok = await keymgrStore.initialize(initKey);
    if (ok) {
      addToast('Server encryption enabled. Save the key — every restart will require it.', 'success', 8000);
      initKey = '';
      initConfirm = '';
    }
  }

  async function onRotate(e: Event) {
    e.preventDefault();
    if (!currentKey) {
      localError = 'Current key is required';
      return;
    }
    if (!newKey) {
      localError = 'New key is required';
      return;
    }
    if (newKey === currentKey) {
      localError = 'New key must differ from current key';
      return;
    }
    if (newKey !== confirmKey) {
      localError = 'New key and confirmation do not match';
      return;
    }
    localError = null;

    const ok = await keymgrStore.rotate(currentKey, newKey);
    if (ok) {
      addToast('Server key rotated. Save the new key — restarts will require it.', 'success', 6000);
      currentKey = '';
      newKey = '';
      confirmKey = '';
    }
  }

  async function onLockNow() {
    const ok = await keymgrStore.lock();
    if (ok) {
      addToast('Server locked. The next request will redirect to unlock.', 'success', 4000);
    }
  }
</script>

<div>
  <div class="mb-4">
    <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">Server encryption key</h2>
    <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
      Manage the at-rest encryption key that protects sensitive settings (mount credentials, hook secrets, external-resource creds).
    </p>
  </div>

  <div class="space-y-4">
    {#if !initialized}
      <!-- Initialize panel — only visible when the verifier has
           never been written. Choosing a key here flips the system
           into "encryption on" mode permanently; every restart from
           this point on will require unlock. -->
      <div class="p-5 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm">
        <h3 class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-2 flex items-center gap-1.5">
          <KeyRound size={14} class="text-accent-600 dark:text-accent-400" /> Enable at-rest encryption
        </h3>
        <p class="text-xs text-slate-500 dark:text-slate-400 mb-3">
          Pika is currently running without at-rest encryption. Choose a master key below to turn it on.
          After enabling, sensitive settings (mount credentials, hook secrets, external-resource creds) will be encrypted on disk,
          and every server restart will require this key to bring Pika online.
        </p>

        <div class="mb-4 p-3 bg-amber-50 dark:bg-amber-950/40 border border-amber-200 dark:border-amber-800 rounded-md">
          <p class="text-xs text-amber-800 dark:text-amber-200 leading-relaxed m-0 flex items-start gap-2">
            <AlertTriangle size={13} class="shrink-0 mt-0.5" />
            <span>
              Save this key in a password manager <strong>before</strong> clicking Enable.
              Losing it makes encrypted data unrecoverable — there is no reset.
            </span>
          </p>
        </div>

        <form onsubmit={onInitialize} class="space-y-3">
          <div>
            <label for="init-key" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">
              Master key
            </label>
            <div class="relative">
              <input
                id="init-key"
                type={showInit ? 'text' : 'password'}
                bind:value={initKey}
                oninput={clearLocal}
                autocomplete="new-password"
                disabled={busy}
                class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10 disabled:opacity-50"
              />
              <button
                type="button"
                onclick={() => (showInit = !showInit)}
                tabindex="-1"
                class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 dark:text-slate-500 bg-transparent border-none cursor-pointer hover:text-slate-600 dark:hover:text-slate-300"
                title={showInit ? 'Hide' : 'Show'}
                aria-label={showInit ? 'Hide key' : 'Show key'}
              >
                {#if showInit}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
              </button>
            </div>
          </div>

          <div>
            <label for="init-confirm" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">
              Confirm master key
            </label>
            <input
              id="init-confirm"
              type={showInit ? 'text' : 'password'}
              bind:value={initConfirm}
              oninput={clearLocal}
              autocomplete="new-password"
              disabled={busy}
              class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10 disabled:opacity-50"
            />
          </div>

          {#if localError || keymgrStore.error}
            <div class="p-2.5 rounded border border-red-300 dark:border-red-700 bg-red-50 dark:bg-red-950/40 text-xs text-red-700 dark:text-red-300">
              {localError || keymgrStore.error}
            </div>
          {/if}

          <button
            type="submit"
            disabled={busy || !initKey || !initConfirm}
            class="flex items-center justify-center gap-2 w-full px-4 py-2.5 text-sm font-medium text-white rounded-md cursor-pointer transition-colors disabled:opacity-50 disabled:cursor-not-allowed
              {busy ? 'bg-amber-500' : 'bg-accent-600 hover:bg-accent-700'}"
          >
            <KeyRound size={14} />
            {busy ? 'Enabling...' : 'Enable encryption'}
          </button>
        </form>
      </div>
    {:else}
    <!-- Rotate panel -->
    <div class="p-5 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm">
      <h3 class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-2 flex items-center gap-1.5">
        <RotateCw size={14} /> Rotate key
      </h3>
      <p class="text-xs text-slate-500 dark:text-slate-400 mb-4">
        Re-encrypts every at-rest secret with the new key.
        After rotation, the next server restart will require the new key to unlock.
      </p>

      <div class="mb-4 p-3 bg-amber-50 dark:bg-amber-950/40 border border-amber-200 dark:border-amber-800 rounded-md">
        <p class="text-xs text-amber-800 dark:text-amber-200 leading-relaxed m-0 flex items-start gap-2">
          <AlertTriangle size={13} class="shrink-0 mt-0.5" />
          <span>
            Save the new key in your password manager <strong>before</strong> rotating.
            If a restart happens after rotation and you don't have the new key, the server can't be unlocked.
          </span>
        </p>
      </div>

      <form onsubmit={onRotate} class="space-y-3">
        <!-- Current -->
        <div>
          <label for="rotate-current" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">
            Current key
          </label>
          <div class="relative">
            <input
              id="rotate-current"
              type={showCurrent ? 'text' : 'password'}
              bind:value={currentKey}
              oninput={clearLocal}
              autocomplete="current-password"
              disabled={busy}
              class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10 disabled:opacity-50"
            />
            <button
              type="button"
              onclick={() => (showCurrent = !showCurrent)}
              tabindex="-1"
              class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 dark:text-slate-500 bg-transparent border-none cursor-pointer hover:text-slate-600 dark:hover:text-slate-300"
              title={showCurrent ? 'Hide' : 'Show'}
              aria-label={showCurrent ? 'Hide current key' : 'Show current key'}
            >
              {#if showCurrent}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
            </button>
          </div>
        </div>

        <!-- New -->
        <div>
          <label for="rotate-new" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">
            New key
          </label>
          <div class="relative">
            <input
              id="rotate-new"
              type={showNew ? 'text' : 'password'}
              bind:value={newKey}
              oninput={clearLocal}
              autocomplete="new-password"
              disabled={busy}
              class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10 disabled:opacity-50"
            />
            <button
              type="button"
              onclick={() => (showNew = !showNew)}
              tabindex="-1"
              class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 dark:text-slate-500 bg-transparent border-none cursor-pointer hover:text-slate-600 dark:hover:text-slate-300"
              title={showNew ? 'Hide' : 'Show'}
              aria-label={showNew ? 'Hide new key' : 'Show new key'}
            >
              {#if showNew}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
            </button>
          </div>
        </div>

        <!-- Confirm -->
        <div>
          <label for="rotate-confirm" class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">
            Confirm new key
          </label>
          <input
            id="rotate-confirm"
            type={showNew ? 'text' : 'password'}
            bind:value={confirmKey}
            oninput={clearLocal}
            autocomplete="new-password"
            disabled={busy}
            class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10 disabled:opacity-50"
          />
        </div>

        {#if localError || keymgrStore.error}
          <div class="p-2.5 rounded border border-red-300 dark:border-red-700 bg-red-50 dark:bg-red-950/40 text-xs text-red-700 dark:text-red-300">
            {localError || keymgrStore.error}
          </div>
        {/if}

        <button
          type="submit"
          disabled={busy || !currentKey || !newKey || !confirmKey}
          class="flex items-center justify-center gap-2 w-full px-4 py-2.5 text-sm font-medium text-white rounded-md cursor-pointer transition-colors disabled:opacity-50 disabled:cursor-not-allowed
            {busy ? 'bg-amber-500' : 'bg-vermilion-500 hover:bg-vermilion-600'}"
        >
          <RotateCw size={14} class={busy ? 'animate-spin' : ''} />
          {busy ? 'Rotating...' : 'Rotate server key'}
        </button>
      </form>
    </div>

    <!-- Lock now panel — explicit "step away" action that forces
         the next request through the unlock flow without restarting
         the process. Useful for rehearsals and operator handovers. -->
    <div class="p-5 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm">
      <h3 class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-2 flex items-center gap-1.5">
        <Lock size={14} /> Lock the server now
      </h3>
      <p class="text-xs text-slate-500 dark:text-slate-400 mb-3">
        Clears the live encryption key from memory. Every other user (including you) will be redirected to the unlock screen until someone enters the key again.
      </p>
      <button
        type="button"
        onclick={onLockNow}
        disabled={busy}
        class="flex items-center justify-center gap-2 px-4 py-2 text-sm font-medium text-slate-700 dark:text-slate-100 bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 rounded-md cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
      >
        <Lock size={13} /> Lock server
      </button>
    </div>
    {/if}
  </div>
</div>
