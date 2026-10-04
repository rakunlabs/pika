<script lang="ts" module>
  export type VaultNav =
    | { kind: "items"; view: "active" | "archived" | "trash"; favorites: boolean; folder: string | null }
    | { kind: "files" };

  export const ITEM_DRAG_MIME = "application/x-pika-vault-item";

  export function navKey(n: VaultNav): string {
    if (n.kind === "files") return "files";
    if (n.folder !== null) return "folder:" + n.folder.toLowerCase();
    if (n.favorites) return "favorites";
    return n.view;
  }
</script>

<script lang="ts">
  import {
    Inbox,
    Star,
    Archive,
    Trash2,
    HardDrive,
    Folder,
    FolderOpen,
    Lock,
    Clock,
    ShieldCheck,
  } from "lucide-svelte";
  import { vaultStore } from "@/lib/vault/store.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import { apiErrorMessage } from "@/lib/api/client";

  interface Props {
    nav: VaultNav;
    onNavigate: (nav: VaultNav) => void;
  }
  let { nav, onNavigate }: Props = $props();

  const current = $derived(navKey(nav));

  // Folder labels only exist on items, so derive them from the active
  // list. Remember the last set seen while browsing active items so the
  // section doesn't flicker when switching to Archive / Trash / Files.
  let folders = $state<string[]>([]);
  $effect(() => {
    if (nav.kind === "items" && nav.view === "active" && !nav.favorites) {
      const next = vaultStore.allFolders();
      if (next.length || !vaultStore.loading) folders = next;
    }
  });

  const primary = [
    { key: "active", label: "All items", icon: Inbox, nav: { kind: "items", view: "active", favorites: false, folder: null } },
    { key: "favorites", label: "Favorites", icon: Star, nav: { kind: "items", view: "active", favorites: true, folder: null } },
    { key: "files", label: "Files", icon: HardDrive, nav: { kind: "files" } },
  ] as const;

  const secondary = [
    { key: "archived", label: "Archive", icon: Archive, nav: { kind: "items", view: "archived", favorites: false, folder: null } },
    { key: "trash", label: "Trash", icon: Trash2, nav: { kind: "items", view: "trash", favorites: false, folder: null } },
  ] as const;

  // ─── Drop items onto folders ────────────────────────────────────
  let dropTarget = $state<string | null>(null);

  function acceptsItem(e: DragEvent): boolean {
    return Array.from(e.dataTransfer?.types ?? []).includes(ITEM_DRAG_MIME);
  }

  async function dropOnFolder(e: DragEvent, folder: string) {
    e.preventDefault();
    dropTarget = null;
    const id = e.dataTransfer?.getData(ITEM_DRAG_MIME);
    if (!id) return;
    const item = vaultStore.items.find((i) => i.id === id);
    if (!item) return;
    const currentFolder = (vaultStore.decrypted.get(id)?.folder ?? "").trim();
    if (currentFolder.toLowerCase() === folder.toLowerCase()) return;
    try {
      await vaultStore.updateItem(id, { expected_version: item.version }, { folder });
      addToast(folder ? `Moved to ${folder}` : "Removed from folder", "success", 2000);
    } catch (err) {
      addToast(apiErrorMessage(err, "Failed to move item"), "alert");
    }
  }

  function formatCountdown(total: number): string {
    if (total <= 0) return "0:00";
    const m = Math.floor(total / 60);
    const s = total % 60;
    return `${m}:${s.toString().padStart(2, "0")}`;
  }

  const rowBase =
    "w-full flex items-center gap-2 px-2.5 py-1.5 rounded text-sm cursor-pointer border border-transparent transition-colors";
  const rowActive =
    "bg-accent-50 text-accent-700 border-accent-200 dark:bg-accent-900/40 dark:text-accent-300 dark:border-accent-700";
  const rowIdle =
    "text-slate-600 dark:text-warm-200 hover:bg-slate-100 dark:hover:bg-warm-700 hover:text-slate-800 dark:hover:text-white";
  const rowDrop =
    "bg-accent-50 dark:bg-accent-900/40 border-dashed border-accent-400 dark:border-accent-500 text-accent-700 dark:text-accent-300";
</script>

<nav
  class="flex flex-col h-full w-full border-r border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-800"
  aria-label="Vault sections"
>
  <div class="px-3 pt-3 pb-2 flex items-center gap-2">
    <div
      class="w-7 h-7 rounded-md bg-accent-600 text-white flex items-center justify-center"
    >
      <ShieldCheck size={15} />
    </div>
    <div class="min-w-0">
      <div class="text-sm font-semibold leading-tight">Vault</div>
      <div class="text-[11px] text-slate-500 dark:text-slate-400 leading-tight">
        {vaultStore.status?.item_count ?? 0} items
      </div>
    </div>
  </div>

  <div class="flex-1 overflow-y-auto px-2 pb-2 space-y-4">
    <div class="space-y-0.5">
      {#each primary as entry (entry.key)}
        {@const Icon = entry.icon}
        <button
          class="{rowBase} {current === entry.key ? rowActive : rowIdle}"
          aria-current={current === entry.key ? "page" : undefined}
          onclick={() => onNavigate(entry.nav as VaultNav)}
        >
          <Icon size={15} class="shrink-0" />
          <span class="flex-1 text-left truncate">{entry.label}</span>
        </button>
      {/each}
    </div>

    {#if folders.length > 0}
      <div>
        <div
          class="px-2.5 mb-1 text-[10px] font-medium uppercase tracking-wider text-slate-400 dark:text-slate-500"
        >
          Folders
        </div>
        <div class="space-y-0.5">
          {#each folders as f (f)}
            {@const key = "folder:" + f.toLowerCase()}
            {@const active = current === key}
            <button
              class="{rowBase} {dropTarget === key ? rowDrop : active ? rowActive : rowIdle}"
              aria-current={active ? "page" : undefined}
              title="Drop items here to move them into {f}"
              onclick={() =>
                onNavigate({ kind: "items", view: "active", favorites: false, folder: f })}
              ondragover={(e) => {
                if (!acceptsItem(e)) return;
                e.preventDefault();
                if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
                dropTarget = key;
              }}
              ondragleave={() => {
                if (dropTarget === key) dropTarget = null;
              }}
              ondrop={(e) => dropOnFolder(e, f)}
            >
              {#if active}
                <FolderOpen size={15} class="shrink-0" />
              {:else}
                <Folder size={15} class="shrink-0 text-accent-600 dark:text-accent-400" />
              {/if}
              <span class="flex-1 text-left truncate">{f}</span>
            </button>
          {/each}
        </div>
      </div>
    {/if}

    <div class="space-y-0.5 pt-2 border-t border-slate-200 dark:border-warm-700">
      {#each secondary as entry (entry.key)}
        {@const Icon = entry.icon}
        <button
          class="{rowBase} {current === entry.key ? rowActive : rowIdle}"
          aria-current={current === entry.key ? "page" : undefined}
          onclick={() => onNavigate(entry.nav as VaultNav)}
        >
          <Icon size={15} class="shrink-0" />
          <span class="flex-1 text-left truncate">{entry.label}</span>
        </button>
      {/each}
    </div>
  </div>

  {#if vaultStore.isServerManaged}
    <div
      class="flex items-center gap-2 px-3 py-2 border-t border-slate-200 dark:border-warm-700 text-[11px] text-slate-500 dark:text-slate-400"
      title="Vault keys are protected by the server encryption key"
    >
      <ShieldCheck size={12} class="shrink-0" />
      <span class="flex-1">Server-managed encryption</span>
    </div>
  {:else}
  <div
    class="flex items-center gap-2 px-3 py-2 border-t border-slate-200 dark:border-warm-700 text-[11px] text-slate-500 dark:text-slate-400"
  >
    <Clock size={12} class="shrink-0" />
    <span class="flex-1 tabular-nums" title="Vault auto-locks after this time without activity">
      Locks in {formatCountdown(vaultStore.remainingLockSeconds)}
    </span>
    <button
      onclick={() => vaultStore.lock()}
      class="flex items-center gap-1 px-2 py-1 rounded hover:bg-slate-100 dark:hover:bg-warm-700 text-slate-600 dark:text-slate-300 cursor-pointer"
      title="Lock the vault now"
    >
      <Lock size={11} /> Lock
    </button>
  </div>
  {/if}
</nav>
