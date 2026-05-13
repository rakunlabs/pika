<script lang="ts">
  import { onMount } from 'svelte';
  import { Lock, ShieldOff, Loader2 } from 'lucide-svelte';
  import { vaultStore } from '@/lib/vault/store.svelte';
  import { appStore } from '@/lib/store/store.svelte';
  import VaultSetup from '@/lib/components/vault/VaultSetup.svelte';
  import VaultUnlock from '@/lib/components/vault/VaultUnlock.svelte';
  import ItemList from '@/lib/components/vault/ItemList.svelte';
  import ItemEditor from '@/lib/components/vault/ItemEditor.svelte';
  import NewItemDialog from '@/lib/components/vault/NewItemDialog.svelte';

  // Top-level state for the page. The store owns the real data; this
  // component just decides which subview renders.
  let booted = $state(false);
  let selectedId = $state<string | null>(null);
  let showNew = $state(false);

  // NOTE: the idle-lock activity watcher and the on-blur / on-hidden
  // lock hooks are installed at App level so the timer keeps ticking
  // and gets reset on user input regardless of which page is mounted.
  // Within-app navigation does NOT lock the vault anymore — the only
  // automatic locks are the idle timer and the visibility/focus hooks.

  onMount(async () => {
    booted = false;
    await Promise.allSettled([
      vaultStore.refreshStatus(),
      vaultStore.refreshAccount().catch(() => {/* 404 is normal pre-setup */}),
    ]);
    booted = true;
  });

  // When the vault is unlocked for the first time on this view, fetch
  // the items so the list isn't empty after navigation.
  $effect(() => {
    if (booted && vaultStore.isUnlocked() && vaultStore.items.length === 0) {
      vaultStore.refreshItems();
    }
  });

  // Currently-selected item view (with decrypted payload).
  const current = $derived(
    selectedId ? vaultStore.decrypted.get(selectedId) : undefined,
  );

  function onCreated(id: string) {
    showNew = false;
    selectedId = id;
  }

  // Vault availability gate. The /api/v1/info response carries
  // vault_enabled = (s.VaultCoord() != nil). If the server doesn't
  // expose the feature, show a fallback rather than spinning forever.
  const vaultEnabled = $derived(appStore.info?.vault_enabled ?? false);
</script>

<svelte:head>
  <title>Vault · pika</title>
</svelte:head>

<div class="flex flex-col h-full overflow-hidden">
  {#if !booted}
    <div class="flex-1 flex items-center justify-center">
      <Loader2 size={20} class="animate-spin text-slate-400" />
    </div>
  {:else if !vaultEnabled}
    <div class="max-w-md mx-auto py-12 px-4 text-center">
      <ShieldOff size={32} class="mx-auto text-slate-400 mb-3" />
      <h2 class="text-lg font-semibold mb-2">Vault not enabled</h2>
      <p class="text-sm text-slate-600 dark:text-slate-300">
        The personal vault feature isn't configured on this server.
      </p>
    </div>
  {:else if !vaultStore.status?.initialized}
    <VaultSetup onComplete={async () => {
      await vaultStore.refreshItems();
    }} />
  {:else if !vaultStore.isUnlocked()}
    <VaultUnlock onUnlocked={async () => {
      await vaultStore.refreshItems();
    }} />
  {:else}
    <!-- Unlocked: split layout -->
    <div class="flex-1 flex overflow-hidden">
      <ItemList selectedId={selectedId} onSelect={(id) => (selectedId = id)} onNew={() => (showNew = true)} />
      <div class="flex-1 overflow-hidden">
        {#if current}
          {#key current.item.id + ':' + current.item.version}
            <ItemEditor
              item={current.item}
              title={current.title}
              tagsCleartext={current.tags}
              hostnamesCleartext={current.hostnames}
              payload={current.payload}
              onClose={() => (selectedId = null)}
            />
          {/key}
        {:else}
          <div class="h-full flex flex-col items-center justify-center text-slate-400 text-sm gap-2">
            <Lock size={32} class="opacity-30" />
            <div>Select an item or create a new one</div>
          </div>
        {/if}
      </div>
    </div>
  {/if}

  {#if showNew}
    <NewItemDialog onCreated={onCreated} onClose={() => (showNew = false)} />
  {/if}
</div>
