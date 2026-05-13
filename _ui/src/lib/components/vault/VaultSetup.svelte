<script lang="ts">
  import { Lock, AlertTriangle, Loader2 } from 'lucide-svelte';
  import { vaultStore } from '@/lib/vault/store.svelte';
  import { estimateStrength } from '@/lib/vault/generator';
  import type { KDFPreset, SecretKey } from '@/lib/vault/crypto';
  import { appStore } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import EmergencyKit from './EmergencyKit.svelte';

  interface Props {
    onComplete?: () => void;
  }
  let { onComplete }: Props = $props();

  let password = $state('');
  let confirm = $state('');
  let preset = $state<KDFPreset>('default');
  let lockMinutes = $state(15);
  let busy = $state(false);
  let err = $state('');
  let secretKey = $state<SecretKey | null>(null);

  // Strength estimator runs synchronously every keystroke; cheap.
  const strength = $derived(estimateStrength(password));
  const mismatch = $derived(confirm.length > 0 && confirm !== password);
  const passwordValid = $derived(password.length >= 8 && !mismatch);

  const strengthColors: Record<string, string> = {
    terrible: 'bg-red-500',
    weak: 'bg-orange-500',
    fair: 'bg-yellow-500',
    strong: 'bg-emerald-500',
    very_strong: 'bg-emerald-600',
  };

  async function submit(e: Event) {
    e.preventDefault();
    if (!passwordValid || busy) return;
    busy = true;
    err = '';
    try {
      const sk = await vaultStore.setup(password, preset, lockMinutes * 60);
      secretKey = sk;
      // Wipe the password from local state — the live vault key is
      // already in vaultStore. The user must save the Secret Key
      // before clicking Continue.
      password = '';
      confirm = '';
    } catch (e: any) {
      err = e?.response?.data?.message ?? e?.message ?? 'Setup failed';
      addToast(err, 'alert');
    } finally {
      busy = false;
    }
  }

  function acknowledgeKit() {
    secretKey = null;
    onComplete?.();
  }
</script>

<div class="h-full overflow-y-auto">
  <div class="max-w-xl mx-auto py-8 px-4">
  {#if secretKey}
    <div class="bg-white dark:bg-warm-900 rounded-lg border border-slate-200 dark:border-warm-700 p-6">
      <h2 class="text-lg font-semibold mb-4 flex items-center gap-2">
        <Lock size={18} class="text-accent-600" />
        Your Emergency Kit
      </h2>
      <EmergencyKit
        username={appStore.identity?.subject ?? appStore.info?.user ?? 'user'}
        secretKey={secretKey.formatted}
        kitID={vaultStore.account?.recovery_kit_id ?? ''}
        onAcknowledge={acknowledgeKit}
      />
    </div>
  {:else}
    <div class="bg-white dark:bg-warm-900 rounded-lg border border-slate-200 dark:border-warm-700 p-6">
      <h2 class="text-lg font-semibold mb-2 flex items-center gap-2">
        <Lock size={18} class="text-accent-600" />
        Create your personal vault
      </h2>
      <p class="text-sm text-slate-600 dark:text-slate-300 mb-6">
        Your vault stores passwords and other secrets end-to-end encrypted in your browser.
        The server never sees the unencrypted contents — not even an admin can read them.
      </p>

      <div class="bg-blue-50 dark:bg-blue-950/30 border border-blue-300 dark:border-blue-700 rounded p-3 text-sm mb-6 flex gap-2">
        <AlertTriangle size={16} class="text-blue-700 dark:text-blue-300 shrink-0 mt-0.5" />
        <div class="text-blue-900 dark:text-blue-200">
          Every item — title, tags, URLs, passwords, notes — is encrypted in your browser
          before it reaches the server. Search and filtering happen locally after unlock.
          The server can only see the item type (Login / Card / …) and lifecycle flags
          (favorite / archive / trash) needed to render the list.
        </div>
      </div>

      <form onsubmit={submit} class="space-y-4">
        <div>
          <label class="block text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400 mb-1" for="vault-pw">
            Master password
          </label>
          <input
            id="vault-pw"
            type="password"
            bind:value={password}
            required
            minlength="8"
            autocomplete="new-password"
            class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-950 focus:outline-none focus:ring-2 focus:ring-accent-500"
            placeholder="At least 8 characters; longer is much better"
          />
          {#if password}
            <div class="mt-2 flex items-center gap-2">
              <div class="flex-1 h-1.5 bg-slate-200 dark:bg-warm-800 rounded overflow-hidden">
                <div
                  class="h-full transition-all {strengthColors[strength.label]}"
                  style="width: {(strength.score + 1) * 20}%"
                ></div>
              </div>
              <span class="text-xs capitalize text-slate-500 dark:text-slate-400">
                {strength.label.replace('_', ' ')}
              </span>
            </div>
          {/if}
        </div>

        <div>
          <label class="block text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400 mb-1" for="vault-confirm">
            Confirm master password
          </label>
          <input
            id="vault-confirm"
            type="password"
            bind:value={confirm}
            required
            autocomplete="new-password"
            class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-950 focus:outline-none focus:ring-2 focus:ring-accent-500"
          />
          {#if mismatch}
            <p class="mt-1 text-xs text-red-600 dark:text-red-400">Passwords don't match</p>
          {/if}
        </div>

        <div>
          <label class="block text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400 mb-1" for="vault-preset">
            Key derivation preset
          </label>
          <select
            id="vault-preset"
            bind:value={preset}
            class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-950 focus:outline-none focus:ring-2 focus:ring-accent-500"
          >
            <option value="fast">Fast — 32 MiB, 2 iters (older / mobile)</option>
            <option value="default">Default — 64 MiB, 3 iters (recommended)</option>
            <option value="strong">Strong — 128 MiB, 4 iters (modern desktop)</option>
          </select>
          <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">
            Stronger means each unlock takes longer in the browser. The setting is
            persisted with the vault; you can rotate it later.
          </p>
        </div>

        <div>
          <label class="block text-xs font-medium uppercase tracking-wide text-slate-500 dark:text-slate-400 mb-1" for="vault-lock">
            Auto-lock after (minutes of inactivity)
          </label>
          <input
            id="vault-lock"
            type="number"
            min="1"
            max="1440"
            bind:value={lockMinutes}
            class="w-32 px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-950 focus:outline-none focus:ring-2 focus:ring-accent-500"
          />
        </div>

        {#if err}
          <div class="text-sm text-red-600 dark:text-red-400">{err}</div>
        {/if}

        <button
          type="submit"
          disabled={!passwordValid || busy}
          class="w-full flex items-center justify-center gap-2 px-4 py-2 text-sm rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
        >
          {#if busy}<Loader2 size={14} class="animate-spin" />{/if}
          Create vault
        </button>
      </form>
    </div>
  {/if}
  </div>
</div>
