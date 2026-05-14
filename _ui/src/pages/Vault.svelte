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
  // ItemList tells us which folder is currently active in the
  // sidebar so the new-item dialog can pre-fill that folder. Empty
  // string = no folder context.
  let newItemDefaultFolder = $state('');

  // The Emergency Kit pin lives on the store now (vaultStore.pendingSecretKey).
  // Setting it on the store BEFORE refreshStatus() flips initialized=true
  // closes the race that previously unmounted VaultSetup before the
  // kit screen rendered. See store.svelte.ts:setup() for details.

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

  // NOTE: We do NOT trigger an items refresh from here. ItemList's
  // own $effect (ItemList.svelte) issues refreshItems(filter) on
  // mount and whenever its server-evaluable filters change.
  //
  // A previous version of this block called refreshItems() whenever
  // items.length === 0 — but after the refreshItems change that now
  // clears `items` synchronously before the network round-trip, an
  // empty result (legitimately empty archive/trash, or any boot
  // before the user has items) kept the condition true and
  // re-triggered the effect, hitting Svelte's
  // effect_update_depth_exceeded guard. ItemList alone is the
  // single owner of "when to fetch the item list" — keep it that
  // way.

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

<!-- The vault page acts as its own application surface. We give the
     outer wrapper the same page-level background (`slate-100` /
     `warm-900`) the App shell uses so the area surrounding
     full-bleed children (VaultSetup card, VaultUnlock card) has a
     proper backdrop. Without this, dark mode rendered the empty
     gutters around the cards in light grey because nothing in the
     tree painted a dark surface there. -->
<div class="flex flex-col h-full overflow-hidden bg-slate-100 dark:bg-warm-900">
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
  {:else if !vaultStore.status?.initialized || vaultStore.pendingSecretKey}
    <VaultSetup
      onComplete={async () => {
        await vaultStore.refreshItems();
      }}
    />
  {:else if !vaultStore.isUnlocked()}
    <VaultUnlock onUnlocked={async () => {
      await vaultStore.refreshItems();
    }} />
  {:else}
    <!-- Unlocked: split layout -->
    <div class="flex-1 flex overflow-hidden">
      <ItemList
        selectedId={selectedId}
        onSelect={(id) => (selectedId = id)}
        onNew={(folder) => { newItemDefaultFolder = folder; showNew = true; }}
      />
      <!-- Right pane. When an ItemEditor is mounted it provides its
           own `bg-white dark:bg-warm-950` surface; when nothing is
           selected, this empty-state surface needs the same dark
           backdrop so the right half doesn't blast white into the
           dark UI. -->
      <div class="flex-1 overflow-hidden bg-white dark:bg-warm-950">
        {#if current}
          {#key current.item.id + ':' + current.item.version}
            <ItemEditor
              item={current.item}
              title={current.title}
              tagsCleartext={current.tags}
              hostnamesCleartext={current.hostnames}
              folderCleartext={current.folder}
              payload={current.payload}
              onClose={() => (selectedId = null)}
            />
          {/key}
        {:else}
          <!-- Right-pane empty state. Picked up from the same
               visual vocabulary as the list's empty states so the
               vault feels like one coherent surface even when
               nothing is selected. -->
          <div class="h-full flex flex-col items-center justify-center text-center px-6">
            <div class="w-16 h-16 rounded-full bg-slate-100 dark:bg-warm-900 flex items-center justify-center mb-4">
              <Lock size={28} class="text-slate-400 opacity-70" />
            </div>
            <div class="text-sm font-medium text-slate-700 dark:text-slate-200 mb-1">
              Select an item to view it
            </div>
            <div class="text-xs text-slate-500 dark:text-slate-400 max-w-sm">
              Items are decrypted in your browser when you open them.
              The server only sees opaque ciphertext.
            </div>
          </div>
        {/if}
      </div>
    </div>
  {/if}

  {#if showNew}
    <NewItemDialog
      defaultFolder={newItemDefaultFolder}
      onCreated={onCreated}
      onClose={() => (showNew = false)}
    />
  {/if}
</div>
