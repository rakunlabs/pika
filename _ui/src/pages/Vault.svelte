<script lang="ts">
  import { onMount, untrack } from "svelte";
  import { Lock, ShieldOff, Loader2 } from "lucide-svelte";
  import { vaultStore } from "@/lib/vault/store.svelte";
  import { appStore } from "@/lib/store/store.svelte";
  import VaultSetup from "@/lib/components/vault/VaultSetup.svelte";
  import VaultUnlock from "@/lib/components/vault/VaultUnlock.svelte";
  import ItemList from "@/lib/components/vault/ItemList.svelte";
  import ItemEditor from "@/lib/components/vault/ItemEditor.svelte";
  import NewItemDialog from "@/lib/components/vault/NewItemDialog.svelte";
  import VaultSidebar, {
    type VaultNav,
  } from "@/lib/components/vault/VaultSidebar.svelte";
  import FileBrowser from "@/lib/components/vault/FileBrowser.svelte";
  import ResizablePanel from "@/lib/components/config/ResizablePanel.svelte";
  import NoteEditor from "@/lib/components/vault/NoteEditor.svelte";
  import { UNTITLED_NOTE } from "@/lib/vault/itemSummary";
  import { confirmDialog } from "@/lib/store/confirm.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import { apiErrorMessage } from "@/lib/api/client";

  // Top-level state for the page. The store owns the real data; this
  // component just decides which subview renders.
  let booted = $state(false);
  let selectedId = $state<string | null>(null);
  let showNew = $state(false);
  // ItemList tells us which folder is currently active in the
  // sidebar so the new-item dialog can pre-fill that folder. Empty
  // string = no folder context.
  let newItemDefaultFolder = $state("");
  // Session-only, like the other resizable panes.
  let sidebarWidth = $state(224);
  let listWidth = $state(352);

  // Left-nav selection: an item bucket (all / favorites / folder /
  // archive / trash) or the file browser.
  let nav = $state<VaultNav>({
    kind: "items",
    view: "active",
    favorites: false,
    folder: null,
  });

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
      vaultStore.refreshAccount().catch(() => {
        /* 404 is normal pre-setup */
      }),
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

  // Notes open in the writing-focused NoteEditor. "Details" swaps in
  // the full ItemEditor (tags, extra fields, history) for that note.
  let detailsFor = $state<string | null>(null);
  let noteDirty = $state(false);
  const showNoteEditor = $derived(
    !!current &&
      current.item.type === "secure_note" &&
      !current.item.deleted_at &&
      detailsFor !== current.item.id,
  );

  async function select(id: string | null) {
    if (id === selectedId) return;
    if (noteDirty) {
      const ok = await confirmDialog({
        title: "Discard unsaved changes?",
        message: "Your edits to this note haven't been saved.",
        confirmLabel: "Discard",
        danger: true,
      });
      if (!ok) return;
    }
    noteDirty = false;
    detailsFor = null;
    selectedId = id;
  }

  async function navigate(next: VaultNav) {
    await select(null);
    if (selectedId === null) nav = next;
  }

  let creatingNote = false;
  async function newNote(folder: string) {
    if (creatingNote) return;
    creatingNote = true;
    try {
      const item = await vaultStore.createItem(
        "secure_note",
        UNTITLED_NOTE,
        { fields: [], notes: "" },
        { folder: folder || undefined },
      );
      await select(item.id);
    } catch (err) {
      addToast(apiErrorMessage(err, "Failed to create note"), "alert");
    } finally {
      creatingNote = false;
    }
  }

  function onCreated(id: string) {
    showNew = false;
    void select(id);
  }

  // Vault availability gate. The /api/v1/info response carries
  // vault_enabled = (s.VaultCoord() != nil). If the server doesn't
  // expose the feature, show a fallback rather than spinning forever.
  const vaultEnabled = $derived(appStore.info?.vault_enabled ?? false);

  // Server-managed vaults (admin turned per-user encryption off) open
  // without a master password: create or fetch the key automatically.
  let serverOpenErr = $state<string | null>(null);
  let serverOpening = $state(false);

  async function openServerVault(create: boolean) {
    if (serverOpening) return;
    serverOpening = true;
    serverOpenErr = null;
    try {
      if (create) await vaultStore.setupServer();
      else await vaultStore.openServerManaged();
    } catch (err) {
      serverOpenErr = apiErrorMessage(err, "Could not open the vault");
    } finally {
      serverOpening = false;
    }
  }

  const status = $derived(vaultStore.status);
  const autoOpen = $derived.by(() => {
    if (!booted || !vaultEnabled || !status || vaultStore.pendingSecretKey) return null;
    if (vaultStore.isUnlocked()) return null;
    if (!status.initialized) return status.deployment_key_mode === "server" ? "create" : null;
    if (status.key_mode === "server" && status.deployment_key_mode === "server") return "open";
    return null;
  });

  $effect(() => {
    const mode = autoOpen;
    if (mode && !serverOpenErr) untrack(() => void openServerVault(mode === "create"));
  });

  // A server-managed vault whose deployment went back to per-user
  // encryption must be given a master password before it can be used.
  const convertToUser = $derived(
    !!status?.initialized && status.key_mode === "server" && status.deployment_key_mode === "user",
  );

  // A master-password vault whose deployment switched to server keys is
  // unlocked once more, then handed to the server.
  const convertToServer = $derived(
    !!status?.initialized && status.key_mode === "user" && status.deployment_key_mode === "server",
  );

  async function afterUnlock() {
    if (convertToServer) {
      try {
        await vaultStore.convertToServer();
        addToast("Vault moved to server-managed encryption", "success", 3000);
      } catch (err) {
        addToast(apiErrorMessage(err, "Could not convert the vault"), "alert");
        vaultStore.lock();
        return;
      }
    }
    await vaultStore.refreshItems();
  }
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
  {:else if serverOpenErr}
    <div class="max-w-md mx-auto py-12 px-4">
      <div
        class="bg-white dark:bg-warm-800 rounded-lg border border-slate-200 dark:border-warm-700 p-6 shadow-sm dark:shadow-none text-center"
      >
        <ShieldOff size={28} class="mx-auto text-slate-400 mb-3" />
        <h2 class="text-base font-semibold mb-2">Vault unavailable</h2>
        <p class="text-sm text-slate-600 dark:text-slate-300 mb-4">{serverOpenErr}</p>
        <button
          type="button"
          onclick={() => {
            serverOpenErr = null;
          }}
          class="px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 cursor-pointer"
        >
          Try again
        </button>
      </div>
    </div>
  {:else if autoOpen || serverOpening}
    <div class="flex-1 flex items-center justify-center">
      <Loader2 size={20} class="animate-spin text-slate-400" />
    </div>
  {:else if !vaultStore.status?.initialized || vaultStore.pendingSecretKey || convertToUser}
    <div class="flex-1 min-h-0 overflow-y-auto">
      <VaultSetup
        convert={convertToUser}
        onComplete={async () => {
          await vaultStore.refreshItems();
        }}
      />
    </div>
  {:else if !vaultStore.isUnlocked()}
    <div class="flex-1 min-h-0 overflow-y-auto">
      <VaultUnlock
        convertToServer={convertToServer}
        onUnlocked={afterUnlock}
      />
    </div>
  {:else}
    <!-- Unlocked: sidebar nav + list + detail -->
    <div class="flex-1 flex overflow-hidden">
      <ResizablePanel
        width={sidebarWidth}
        minWidth={180}
        maxWidth={420}
        side="left"
        onResize={(w) => (sidebarWidth = w)}
      >
        <VaultSidebar {nav} onNavigate={navigate} />
      </ResizablePanel>
      {#if nav.kind === "files"}
        <FileBrowser />
      {:else}
        <ResizablePanel
          width={listWidth}
          minWidth={280}
          maxWidth={640}
          side="left"
          onResize={(w) => (listWidth = w)}
        >
          <ItemList
            {selectedId}
            view={nav.view}
            favoritesOnly={nav.favorites}
            folder={nav.folder}
            onSelect={(id) => void select(id)}
            onNew={(folder) => {
              newItemDefaultFolder = folder;
              showNew = true;
            }}
            onNewNote={newNote}
          />
        </ResizablePanel>
        <!-- Right pane. When an ItemEditor is mounted it provides its
             own `bg-white dark:bg-warm-950` surface; the empty state
             needs the same backdrop. -->
        <div class="flex-1 min-w-0 overflow-hidden bg-white dark:bg-warm-950">
          {#if current && showNoteEditor}
            {#key current.item.id}
              <NoteEditor
                item={current.item}
                title={current.title}
                folder={current.folder}
                payload={current.payload}
                onClose={() => void select(null)}
                onOpenDetails={() => {
                  noteDirty = false;
                  detailsFor = current.item.id;
                }}
                onDirtyChange={(d) => (noteDirty = d)}
              />
            {/key}
          {:else if current}
            {#key current.item.id + ":" + current.item.version}
              <ItemEditor
                item={current.item}
                title={current.title}
                tagsCleartext={current.tags}
                hostnamesCleartext={current.hostnames}
                folderCleartext={current.folder}
                payload={current.payload}
                onClose={() => void select(null)}
                onBackToNote={detailsFor === current.item.id ? () => (detailsFor = null) : undefined}
              />
            {/key}
          {:else}
            <div
              class="h-full flex flex-col items-center justify-center text-center px-6"
            >
              <div
                class="w-16 h-16 rounded-full bg-slate-100 dark:bg-warm-900 flex items-center justify-center mb-4"
              >
                <Lock size={28} class="text-slate-400 opacity-70" />
              </div>
              <div
                class="text-sm font-medium text-slate-700 dark:text-slate-200 mb-1"
              >
                Select an item to view it
              </div>
              <div class="text-xs text-slate-500 dark:text-slate-400 max-w-sm">
                Items are decrypted in your browser when you open them. Drag an
                item onto a folder to file it.
              </div>
            </div>
          {/if}
        </div>
      {/if}
    </div>
  {/if}

  {#if showNew}
    <NewItemDialog
      defaultFolder={newItemDefaultFolder}
      {onCreated}
      onClose={() => (showNew = false)}
    />
  {/if}
</div>
