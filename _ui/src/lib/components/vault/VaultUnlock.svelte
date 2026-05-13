<script lang="ts">
  import { Lock, Loader2, KeyRound, AlertCircle, ShieldCheck } from 'lucide-svelte';
  import { onMount } from 'svelte';
  import { vaultStore } from '@/lib/vault/store.svelte';
  import { parseSecretKey } from '@/lib/vault/crypto';
  import { addToast } from '@/lib/store/toast.svelte';

  interface Props {
    onUnlocked?: () => void;
  }
  let { onUnlocked }: Props = $props();

  // The form has two modes:
  //   trusted   — a localStorage trust blob exists for this account;
  //               only the master password is needed. The store
  //               unseals the Secret Key from the blob.
  //   untrusted — first time on this device (or trust was revoked);
  //               both fields are required, plus an opt-in checkbox
  //               to seed the trust blob on a successful unlock.
  let password = $state('');
  let secretKeyRaw = $state('');
  let trustChecked = $state(false);
  let busy = $state(false);
  let err = $state('');

  // `mode` is derived from the store, but we snapshot it locally so
  // it doesn't flip mid-submit (e.g. when the store auto-untrusts
  // on the 3rd failed attempt — we want the error to render in the
  // mode the user just submitted from).
  let mode = $state<'trusted' | 'untrusted'>('untrusted');

  function refreshMode() {
    mode = vaultStore.isDeviceTrusted() ? 'trusted' : 'untrusted';
  }

  onMount(async () => {
    if (!vaultStore.account) {
      await vaultStore.refreshAccount();
    }
    refreshMode();
  });

  // Manual "use Secret Key instead" path. Clears the blob so the
  // store falls through to the explicit-input path on submit.
  function useSecretKeyInstead() {
    vaultStore.untrustDevice();
    refreshMode();
    err = '';
    // Move focus to the secret-key textarea once it renders. The
    // password input keeps whatever the user already typed.
    setTimeout(() => {
      const el = document.getElementById('unlock-sk') as HTMLTextAreaElement | null;
      el?.focus();
    }, 0);
  }

  async function submit(e: Event) {
    e.preventDefault();
    if (busy) return;
    err = '';
    busy = true;
    try {
      if (mode === 'trusted') {
        // Store reads the trust blob, unseals with the master
        // password, and proceeds. Returns false on a wrong password
        // OR after the 3-strike auto-untrust fires.
        const ok = await vaultStore.unlock(password);
        if (!ok) {
          const stillTrusted = vaultStore.isDeviceTrusted();
          if (!stillTrusted) {
            // 3-strike untrust fired (or the stored blob was
            // server-rejected as stale). Flip to the full form and
            // tell the user.
            mode = 'untrusted';
            err = 'Too many failed attempts — device trust removed. Please enter your Secret Key.';
          } else {
            err = 'Wrong master password';
          }
          return;
        }
      } else {
        let sk: Uint8Array;
        try {
          sk = parseSecretKey(secretKeyRaw);
        } catch (e: any) {
          err = e?.message ?? 'Invalid Secret Key format';
          return;
        }
        const ok = await vaultStore.unlock(password, sk);
        if (!ok) {
          err = 'Wrong password or Secret Key';
          return;
        }
        if (trustChecked) {
          try {
            await vaultStore.trustDevice(password);
            addToast('This device is now trusted', 'success', 2500);
          } catch {
            // Non-fatal: unlock succeeded; just couldn't persist
            // the trust blob (quota, private mode, etc).
            addToast('Could not save device trust', 'alert', 3000);
          }
        }
      }
      password = '';
      secretKeyRaw = '';
      trustChecked = false;
      addToast('Vault unlocked', 'success', 2000);
      onUnlocked?.();
    } catch (e: any) {
      err = e?.response?.data?.message ?? e?.message ?? 'Unlock failed';
    } finally {
      busy = false;
    }
  }
</script>

<div class="max-w-md mx-auto py-12 px-4">
  <div class="bg-white dark:bg-warm-900 rounded-lg border border-slate-200 dark:border-warm-700 p-6">
    <div class="flex items-center gap-2 mb-2">
      <Lock size={18} class="text-accent-600" />
      <h2 class="text-lg font-semibold">Unlock your vault</h2>
    </div>
    {#if mode === 'trusted'}
      <p class="text-sm text-slate-600 dark:text-slate-300 mb-2 flex items-center gap-1.5">
        <ShieldCheck size={14} class="text-green-600 dark:text-green-500" />
        This device is trusted — only your master password is needed.
      </p>
    {:else}
      <p class="text-sm text-slate-600 dark:text-slate-300 mb-6">
        Enter your master password and Secret Key (from your Emergency Kit).
      </p>
    {/if}

    <form onsubmit={submit} class="space-y-4">
      <div>
        <label class="block text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400 mb-1" for="unlock-pw">
          Master password
        </label>
        <!-- svelte-ignore a11y_autofocus -->
        <input
          id="unlock-pw"
          type="password"
          autofocus
          bind:value={password}
          required
          autocomplete="current-password"
          class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-950 focus:outline-none focus:ring-2 focus:ring-accent-500"
        />
      </div>

      {#if mode === 'untrusted'}
        <div>
          <label class="block text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400 mb-1" for="unlock-sk">
            <span class="inline-flex items-center gap-1.5">
              <KeyRound size={12} /> Secret Key
            </span>
          </label>
          <textarea
            id="unlock-sk"
            bind:value={secretKeyRaw}
            rows="3"
            required
            spellcheck="false"
            autocomplete="off"
            placeholder="Paste your Secret Key here"
            class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-950 focus:outline-none focus:ring-2 focus:ring-accent-500"
          ></textarea>
          <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">
            Dashes, spaces, and case are ignored.
          </p>
        </div>

        <label class="flex items-start gap-2 text-xs text-slate-600 dark:text-slate-300 cursor-pointer select-none">
          <input
            type="checkbox"
            bind:checked={trustChecked}
            class="mt-0.5 cursor-pointer accent-accent-600"
          />
          <span>
            <span class="font-medium text-slate-700 dark:text-slate-200">Trust this device.</span>
            Skip the Secret Key prompt on future unlocks from this browser.
            The Secret Key is stored locally, encrypted with your master password
            (Argon2id). Three wrong passwords automatically revoke trust.
          </span>
        </label>
      {/if}

      {#if err}
        <div class="flex items-start gap-2 text-sm text-red-700 dark:text-red-400">
          <AlertCircle size={14} class="mt-0.5 shrink-0" />
          <span>{err}</span>
        </div>
      {/if}

      <button
        type="submit"
        disabled={busy}
        class="w-full flex items-center justify-center gap-2 px-4 py-2 text-sm rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
      >
        {#if busy}<Loader2 size={14} class="animate-spin" />{/if}
        {busy && mode === 'trusted' ? 'Unlocking…' : 'Unlock'}
      </button>

      {#if mode === 'trusted'}
        <button
          type="button"
          onclick={useSecretKeyInstead}
          class="w-full text-xs text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 underline cursor-pointer"
        >
          Use Secret Key instead (revoke trust on this device)
        </button>
      {/if}
    </form>
  </div>
</div>
