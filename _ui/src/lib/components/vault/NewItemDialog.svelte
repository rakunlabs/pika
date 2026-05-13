<script lang="ts">
  import { X, KeyRound, CreditCard, UserSquare2, FileText, Terminal, Plug, Database, Server, FileBadge, ShieldCheck } from 'lucide-svelte';
  import { vaultStore } from '@/lib/vault/store.svelte';
  import { templateFor, typeLabel, extractHostnames } from '@/lib/vault/templates';
  import type { VaultItemType } from '@/lib/vault/api';
  import { addToast } from '@/lib/store/toast.svelte';

  interface Props {
    onCreated: (id: string) => void;
    onClose: () => void;
  }
  let { onCreated, onClose }: Props = $props();

  let step = $state<'pick' | 'name'>('pick');
  let chosenType = $state<VaultItemType | null>(null);
  let title = $state('');
  let busy = $state(false);

  // Order matters here — it's the layout the SPA renders. Server-side
  // KnownVaultItemTypes is the source of truth for what's accepted; if
  // it gets a new entry, mirror it here.
  const types: { t: VaultItemType; icon: typeof KeyRound; desc: string }[] = [
    { t: 'login', icon: KeyRound, desc: 'Username, password, website' },
    { t: 'card', icon: CreditCard, desc: 'Credit card number, CVV, PIN' },
    { t: 'identity', icon: UserSquare2, desc: 'Name, address, phone' },
    { t: 'secure_note', icon: FileText, desc: 'Free-form note' },
    { t: 'ssh_key', icon: Terminal, desc: 'SSH public/private key pair' },
    { t: 'api_credential', icon: Plug, desc: 'API key and secret' },
    { t: 'database', icon: Database, desc: 'DB host, port, credentials' },
    { t: 'server', icon: Server, desc: 'Hostname, SSH credentials' },
    { t: 'license', icon: FileBadge, desc: 'Software license key' },
    { t: 'tls_cert', icon: ShieldCheck, desc: 'Certificate and private key' },
  ];

  function pickType(t: VaultItemType) {
    chosenType = t;
    step = 'name';
  }

  async function create() {
    if (!chosenType || busy) return;
    const cleanTitle = title.trim();
    if (!cleanTitle) {
      addToast('Title is required', 'alert');
      return;
    }
    busy = true;
    try {
      const payload = templateFor(chosenType);
      const hostnames = extractHostnames(payload);
      const item = await vaultStore.createItem(chosenType, cleanTitle, payload, {
        urlHostnames: hostnames,
      });
      onCreated(item.id);
    } catch (e: any) {
      addToast(e?.response?.data?.message ?? e?.message ?? 'Create failed', 'alert');
    } finally {
      busy = false;
    }
  }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
<div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40" onclick={onClose} role="dialog" tabindex="-1" aria-modal="true" onkeydown={(e) => { if (e.key === 'Escape') onClose(); }}>
  <!-- Inner panel: stopPropagation is for click bubbling only. -->
  <!-- aria-modal lives on the backdrop; the inner wrapper just opts -->
  <!-- out of the click bubble. -->
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions a11y_click_events_have_key_events -->
  <div class="bg-white dark:bg-warm-900 rounded-lg shadow-xl w-[640px] max-h-[90vh] flex flex-col" onclick={(e) => e.stopPropagation()} role="document">
    <div class="flex items-center justify-between px-4 py-3 border-b border-slate-200 dark:border-warm-700">
      <h2 class="text-sm font-semibold">
        {#if step === 'pick'}Choose item type{:else}Name your {chosenType ? typeLabel(chosenType).toLowerCase() : 'item'}{/if}
      </h2>
      <button onclick={onClose} class="text-slate-400 hover:text-slate-600 cursor-pointer" aria-label="Close">
        <X size={16} />
      </button>
    </div>

    <div class="overflow-y-auto p-4">
      {#if step === 'pick'}
        <div class="grid grid-cols-2 gap-2">
          {#each types as { t, icon: Icon, desc } (t)}
            <button
              onclick={() => pickType(t)}
              class="flex items-start gap-3 p-3 rounded border border-slate-200 dark:border-warm-700 hover:bg-slate-50 dark:hover:bg-warm-800 text-left cursor-pointer"
            >
              <Icon size={20} class="shrink-0 mt-0.5 text-accent-600" />
              <div class="flex-1 min-w-0">
                <div class="text-sm font-medium">{typeLabel(t)}</div>
                <div class="text-xs text-slate-500 dark:text-slate-400">{desc}</div>
              </div>
            </button>
          {/each}
        </div>
      {:else if step === 'name'}
        <form onsubmit={(e) => { e.preventDefault(); create(); }} class="space-y-3">
          <p class="text-sm text-slate-600 dark:text-slate-300">
            The title is encrypted before it reaches the server, so it
            can be as descriptive as you like.
          </p>
          <input
            type="text"
            bind:value={title}
            required
            placeholder="e.g. GitHub, Production DB, ..."
            class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-950 focus:outline-none focus:ring-2 focus:ring-accent-500"
          />
          <div class="flex gap-2 justify-end">
            <button
              type="button"
              onclick={() => { step = 'pick'; chosenType = null; }}
              class="px-3 py-1.5 text-xs rounded hover:bg-slate-100 dark:hover:bg-warm-800 cursor-pointer"
            >Back</button>
            <button
              type="submit"
              disabled={busy || !title.trim()}
              class="px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
            >Create</button>
          </div>
        </form>
      {/if}
    </div>
  </div>
</div>
