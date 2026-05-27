<script lang="ts">
  import {
    Search,
    Star,
    Archive,
    Trash2,
    KeyRound,
    CreditCard,
    UserSquare2,
    FileText,
    Terminal,
    Plug,
    Database,
    Server,
    FileBadge,
    ShieldCheck,
    Plus,
    Lock,
    Clock,
    Folder,
    FolderOpen,
    Inbox,
    ChevronRight,
    ChevronDown,
    X,
    MoreVertical,
    FolderInput,
    FolderMinus,
    Pencil,
    Check,
  } from "lucide-svelte";
  import { vaultStore } from "@/lib/vault/store.svelte";
  import { typeLabel, vaultItemAccent } from "@/lib/vault/templates";
  import type {
    VaultItem,
    VaultItemType,
    VaultListFilter,
  } from "@/lib/vault/api";

  interface Props {
    selectedId: string | null;
    onSelect: (id: string | null) => void;
    /** onNew receives the currently active folder so the new-item
     *  dialog can default the folder field. Empty string = no
     *  folder context. */
    onNew: (defaultFolder: string) => void;
  }
  let { selectedId, onSelect, onNew }: Props = $props();

  // View tab. The server-evaluable filters (type, favorite, archived,
  // trash) live here; the rest run client-side because tags / titles
  // / folders are encrypted at rest.
  let view = $state<"active" | "archived" | "trash">("active");
  let typeFilter = $state<VaultItemType | "">("");
  let q = $state("");
  let favoritesOnly = $state(false);

  // Sentinel keys for the two pseudo-buckets in the grouped view.
  // Real folder names are stored verbatim. We keep them constants
  // so the persisted collapsed-state map and the render loop share
  // a single source of truth.
  const NONE_KEY = "__none__";

  // Tracks whether we've completed at least one fetch in this mount.
  // `loading` flips for any operation (create / update / delete) so
  // we use this flag for the initial-load placeholder only.
  let firstFetchDone = $state(false);

  // Re-fetch when SERVER-EVALUABLE filters change.
  $effect(() => {
    const filter: VaultListFilter = {};
    if (typeFilter) filter.type = typeFilter;
    if (favoritesOnly) filter.favorite = true;
    if (view === "archived") filter.archived = "only";
    if (view === "trash") filter.trash = true;
    vaultStore.refreshItems(filter).finally(() => {
      firstFetchDone = true;
    });
  });

  // ─── Collapsed-state persistence ────────────────────────────────
  //
  // The set of currently-collapsed folder keys is persisted to
  // localStorage so refreshes and lock/unlock cycles don't blow
  // away the user's chosen layout. Keyed by the user_id of the
  // active vault account; switching accounts in the same browser
  // gets a fresh state.
  //
  // The default is "everything expanded" — if a folder isn't in
  // the set, it's open. That matches 1Password's behavior where
  // first-time users see every group at once.
  const COLLAPSED_PREFIX = "pika.vault.collapsed.";

  function storageKey(): string | null {
    const uid = vaultStore.account?.user_id;
    return uid ? COLLAPSED_PREFIX + uid : null;
  }

  function loadCollapsed(): Set<string> {
    try {
      const key = storageKey();
      if (!key) return new Set();
      const raw = localStorage.getItem(key);
      if (!raw) return new Set();
      const arr = JSON.parse(raw);
      return Array.isArray(arr)
        ? new Set(arr.filter((s) => typeof s === "string"))
        : new Set();
    } catch {
      return new Set();
    }
  }

  let collapsed = $state<Set<string>>(loadCollapsed());

  // Re-load when the vault account becomes available (after unlock).
  $effect(() => {
    const uid = vaultStore.account?.user_id;
    if (uid) collapsed = loadCollapsed();
  });

  function toggleCollapsed(key: string) {
    const next = new Set(collapsed);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    collapsed = next;
    try {
      const k = storageKey();
      if (k) localStorage.setItem(k, JSON.stringify(Array.from(next)));
    } catch {
      // localStorage may be unavailable (private mode, quota); the
      // in-memory state still works for this session.
    }
  }

  function expandAll() {
    collapsed = new Set();
    try {
      const k = storageKey();
      if (k) localStorage.removeItem(k);
    } catch {
      /* ignore */
    }
  }
  function collapseAll(keys: string[]) {
    collapsed = new Set(keys);
    try {
      const k = storageKey();
      if (k) localStorage.setItem(k, JSON.stringify(keys));
    } catch {
      /* ignore */
    }
  }

  // ─── Helpers (decrypted lookups) ────────────────────────────────

  function iconFor(type: VaultItemType) {
    switch (type) {
      case "login":
        return KeyRound;
      case "card":
        return CreditCard;
      case "identity":
        return UserSquare2;
      case "secure_note":
        return FileText;
      case "ssh_key":
        return Terminal;
      case "api_credential":
        return Plug;
      case "database":
        return Database;
      case "server":
        return Server;
      case "license":
        return FileBadge;
      case "tls_cert":
        return ShieldCheck;
      default:
        return FileText;
    }
  }

  function decryptedTitle(item: VaultItem): string {
    const d = vaultStore.decrypted.get(item.id);
    if (!d) return "";
    if (d.title === null) return "(unreadable)";
    return d.title;
  }
  function decryptedTags(item: VaultItem): string[] {
    return vaultStore.decrypted.get(item.id)?.tags ?? [];
  }
  function decryptedFolder(item: VaultItem): string {
    // Treat AEAD-failed folder as "no folder" so the row still
    // appears in some bucket rather than vanishing entirely.
    const f = vaultStore.decrypted.get(item.id)?.folder;
    return (f ?? "").trim();
  }

  // ─── Grouped derived view ───────────────────────────────────────
  //
  // One pass over vaultStore.items applies:
  //  - view bucket (active/archived/trash)
  //  - type / favorite / search
  // and groups the survivors by their decrypted folder name (with a
  // (No folder) bucket). The result is the actual render structure;
  // we don't filter to a single folder anymore — the user sees every
  // bucket as a collapsible header.
  //
  // Sort within each bucket: favorites first, then by title.
  type Group = {
    /** Stable key for collapsed-state map. NONE_KEY for no-folder bucket. */
    key: string;
    /** Display name. For NONE_KEY this is "(No folder)". */
    name: string;
    /** True for the pseudo "no folder" bucket. */
    pseudo: boolean;
    items: VaultItem[];
  };

  const grouped = $derived.by<{ groups: Group[]; totalMatched: number }>(() => {
    const needle = q.trim().toLowerCase();
    const byFolder = new Map<string, { display: string; items: VaultItem[] }>();
    const none: VaultItem[] = [];
    let totalMatched = 0;

    for (const i of vaultStore.items) {
      const inTrash = !!i.deleted_at;
      const isArchived = !!i.archived;
      if (view === "trash") {
        if (!inTrash) continue;
      } else if (view === "archived") {
        if (inTrash || !isArchived) continue;
      } else {
        if (inTrash || isArchived) continue;
      }
      if (favoritesOnly && !i.favorite) continue;
      if (typeFilter && i.type !== typeFilter) continue;
      if (needle) {
        const t = decryptedTitle(i).toLowerCase();
        const tags = decryptedTags(i).map((x) => x.toLowerCase());
        const folder = decryptedFolder(i).toLowerCase();
        const matched =
          t.includes(needle) ||
          tags.some((x) => x.includes(needle)) ||
          folder.includes(needle);
        if (!matched) continue;
      }
      totalMatched++;

      const f = decryptedFolder(i);
      if (!f) {
        none.push(i);
        continue;
      }
      const key = f.toLowerCase();
      const bucket = byFolder.get(key);
      if (bucket) bucket.items.push(i);
      else byFolder.set(key, { display: f, items: [i] });
    }

    const cmp = (a: VaultItem, b: VaultItem): number => {
      if ((a.favorite ?? false) !== (b.favorite ?? false))
        return a.favorite ? -1 : 1;
      return decryptedTitle(a).localeCompare(decryptedTitle(b));
    };

    const groups: Group[] = [];

    // Real folders first, alphabetically.
    const folderKeys = Array.from(byFolder.keys()).sort((a, b) =>
      byFolder.get(a)!.display.localeCompare(byFolder.get(b)!.display),
    );
    for (const k of folderKeys) {
      const b = byFolder.get(k)!;
      groups.push({
        key: k,
        name: b.display,
        pseudo: false,
        items: b.items.sort(cmp),
      });
    }
    // No-folder bucket last (so the visual order matches "labeled
    // stuff first, the catch-all bin at the bottom"). Hidden when
    // empty so empty vaults don't show an awkward "(No folder) 0".
    if (none.length > 0) {
      groups.push({
        key: NONE_KEY,
        name: "(No folder)",
        pseudo: true,
        items: none.sort(cmp),
      });
    }

    return { groups, totalMatched };
  });

  // All group keys, used by the Collapse All button.
  const allGroupKeys = $derived(grouped.groups.map((g) => g.key));

  // ─── Lock countdown footer ──────────────────────────────────────

  function formatCountdown(total: number): string {
    if (total <= 0) return "0:00";
    const m = Math.floor(total / 60);
    const s = total % 60;
    return `${m}:${s.toString().padStart(2, "0")}`;
  }

  // ─── Item row helpers ───────────────────────────────────────────

  /** Tag chips rendered inline on each row. We cap the visible
   *  count so a single tag-happy item doesn't blow out the row;
   *  the rest collapse into "+N more". */
  const TAGS_PER_ROW = 2;
  function tagsForRow(item: VaultItem): { visible: string[]; extra: number } {
    const all = decryptedTags(item);
    if (all.length <= TAGS_PER_ROW) return { visible: all, extra: 0 };
    return {
      visible: all.slice(0, TAGS_PER_ROW),
      extra: all.length - TAGS_PER_ROW,
    };
  }

  // ─── Move-to-folder & rename-folder UX ───────────────────────────
  //
  // Folders are just an encrypted string slot per item; the UI lets
  // the user reorganize WITHOUT opening the full editor:
  //
  //  - Per-row kebab → "Move to folder…" popover with the existing
  //    folder list, plus "(No folder)" and an inline "New folder"
  //    creator. Each pick fires a single updateItem({ folder }) call.
  //
  //  - Per-folder-header kebab → inline "Rename folder" input. A
  //    rename loops through every item in that bucket and issues
  //    one updateItem({ folder: newName }) per item. We do them
  //    sequentially to avoid hammering the server with N parallel
  //    encryption requests; the bucket is small in practice.
  //
  // Both surfaces use the same `openMenu` token state so opening
  // one auto-closes the other. A `svelte:window onclick` at the
  // root closes everything when the user clicks outside.
  type OpenMenu =
    | { kind: "row"; itemId: string }
    | { kind: "header"; folderKey: string }
    | { kind: "rename"; folderKey: string }
    | null;
  let openMenu = $state<OpenMenu>(null);

  // The currently-typed name for either (a) the inline "New folder"
  // input inside a row's move popover or (b) the inline rename input
  // on a folder header. Re-used — only one popover is open at a
  // time so a single string suffices.
  let folderDraft = $state("");

  // Tracks an in-flight bulk rename so we can disable the input and
  // show a small spinner — important because rename = N round-trips
  // and the user shouldn't be able to fire it twice.
  let renameInFlight = $state(false);

  function closeMenus() {
    openMenu = null;
    folderDraft = "";
  }

  function openRowMenu(e: MouseEvent, itemId: string) {
    e.stopPropagation();
    if (openMenu?.kind === "row" && openMenu.itemId === itemId) {
      closeMenus();
    } else {
      openMenu = { kind: "row", itemId };
      folderDraft = "";
    }
  }

  function openHeaderMenu(e: MouseEvent, folderKey: string) {
    e.stopPropagation();
    if (openMenu?.kind === "header" && openMenu.folderKey === folderKey) {
      closeMenus();
    } else {
      openMenu = { kind: "header", folderKey };
      folderDraft = "";
    }
  }

  function startRename(folderKey: string, currentName: string) {
    openMenu = { kind: "rename", folderKey };
    folderDraft = currentName;
  }

  /**
   * Move a single item to `folder`. Empty string clears the folder.
   * No-op when the target is identical to the current value (avoids
   * a useless server round-trip + version bump).
   */
  async function moveItemToFolder(item: VaultItem, folder: string) {
    const target = folder.trim();
    const current = decryptedFolder(item);
    if (target === current) {
      closeMenus();
      return;
    }
    try {
      await vaultStore.updateItem(
        item.id,
        { expected_version: item.version },
        { folder: target },
      );
    } catch (err) {
      // Surface to the console; the store also exposes `error` but
      // we don't have a toast system in the sidebar. The list will
      // simply fail to reflect the change.
      console.error("moveItemToFolder failed", err);
    } finally {
      closeMenus();
    }
  }

  /**
   * Rename every item currently in `folderKey` to `newName`. An
   * empty `newName` clears the folder for all items in the bucket
   * (effectively merging them into the (No folder) bucket).
   *
   * Sequential on purpose — vault items each carry their own
   * version + ciphertext so a parallel firehose would burn CPU on
   * Argon2/AEAD with no real wall-clock benefit for typical
   * folders (< 50 items).
   */
  async function renameFolder(folderKey: string, newName: string) {
    const target = newName.trim();
    const bucket = grouped.groups.find((g) => g.key === folderKey);
    if (!bucket || bucket.pseudo) {
      closeMenus();
      return;
    }
    if (target.toLowerCase() === bucket.name.toLowerCase()) {
      closeMenus();
      return;
    }
    renameInFlight = true;
    try {
      for (const item of bucket.items) {
        await vaultStore.updateItem(
          item.id,
          { expected_version: item.version },
          { folder: target },
        );
      }
    } catch (err) {
      console.error("renameFolder failed", err);
    } finally {
      renameInFlight = false;
      closeMenus();
    }
  }

  // Available folder labels for the move popover. We exclude the
  // item's own current folder from the list (showing it would just
  // be a no-op) but always offer "(No folder)" so the user can
  // unfile an item in one click.
  function moveTargets(item: VaultItem): string[] {
    const current = decryptedFolder(item).toLowerCase();
    return vaultStore.allFolders().filter((f) => f.toLowerCase() !== current);
  }
</script>

<svelte:window
  onclick={closeMenus}
  onkeydown={(e) => {
    if (e.key === "Escape") closeMenus();
  }}
/>

<!-- Sidebar surface mirrors Settings.svelte's sidebar tier
     (`bg-slate-50 dark:bg-warm-800`) so navigating from Settings
     into Vault feels like the same shell. Previously we used
     warm-950 here to read as a "deep app canvas" against the
     warm-900 page bg, but that diverged from Settings and the
     contrast against the editor (`warm-950`) was actually
     stronger when sidebar = warm-800. -->
<div
  class="flex flex-col h-full w-80 border-r border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-800"
>
  <!-- View tabs. Tab buttons sit on the warm-800 sidebar; the
       hover surface goes one step lighter (warm-700) to match
       Settings.svelte's pattern. Active tab keeps the solid
       accent-600 fill since it's a high-emphasis "current view"
       indicator rather than a soft nav highlight. -->
  <div class="flex border-b border-slate-200 dark:border-warm-700 text-xs">
    <button
      class="flex-1 py-2 cursor-pointer flex items-center justify-center gap-1.5
        {view === 'active'
        ? 'bg-accent-600 text-white'
        : 'text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700'}"
      onclick={() => (view = "active")}
    >
      <Inbox size={12} /> Items
    </button>
    <button
      class="flex-1 py-2 cursor-pointer flex items-center justify-center gap-1.5
        {view === 'archived'
        ? 'bg-accent-600 text-white'
        : 'text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700'}"
      onclick={() => (view = "archived")}
    >
      <Archive size={12} /> Archive
    </button>
    <button
      class="flex-1 py-2 cursor-pointer flex items-center justify-center gap-1.5
        {view === 'trash'
        ? 'bg-accent-600 text-white'
        : 'text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700'}"
      onclick={() => (view = "trash")}
    >
      <Trash2 size={12} /> Trash
    </button>
  </div>

  <!-- Search + filters -->
  <div class="p-2.5 space-y-2 border-b border-slate-200 dark:border-warm-700">
    <div class="relative">
      <Search
        size={14}
        class="absolute top-1/2 left-2.5 -translate-y-1/2 text-slate-400 pointer-events-none"
      />
      <input
        type="text"
        bind:value={q}
        placeholder="Search title, tag, folder..."
        class="w-full pl-8 pr-7 py-1.5 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
      />
      {#if q}
        <button
          onclick={() => (q = "")}
          class="absolute top-1/2 right-1.5 -translate-y-1/2 p-0.5 rounded hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer"
          title="Clear search"
          aria-label="Clear search"
        >
          <X size={11} class="text-slate-400" />
        </button>
      {/if}
    </div>

    <div class="flex items-center gap-1.5">
      <select
        bind:value={typeFilter}
        class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-1 focus:ring-accent-500"
      >
        <option value="">All types</option>
        <option value="login">Login</option>
        <option value="card">Card</option>
        <option value="identity">Identity</option>
        <option value="secure_note">Note</option>
        <option value="ssh_key">SSH key</option>
        <option value="api_credential">API</option>
        <option value="database">Database</option>
        <option value="server">Server</option>
        <option value="license">License</option>
        <option value="tls_cert">TLS</option>
      </select>
      <button
        onclick={() => (favoritesOnly = !favoritesOnly)}
        class="p-1.5 rounded border cursor-pointer {favoritesOnly
          ? 'bg-amber-50 dark:bg-amber-950/40 border-amber-300 dark:border-amber-700 text-amber-700 dark:text-amber-300'
          : 'border-slate-300 dark:border-warm-600 hover:bg-slate-100 dark:hover:bg-warm-700 text-slate-400'}"
        title="Favorites only"
        aria-pressed={favoritesOnly}
      >
        <Star size={13} fill={favoritesOnly ? "currentColor" : "none"} />
      </button>
    </div>

    {#if view === "active"}
      <button
        onclick={() => onNew("")}
        class="w-full flex items-center justify-center gap-1.5 px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 cursor-pointer"
      >
        <Plus size={12} /> New item
      </button>
    {/if}
  </div>

  <!-- Group control strip (expand/collapse all) — only shown when
       there's more than one group to act on, otherwise it's noise. -->
  {#if view === "active" && grouped.groups.length > 1}
    <div
      class="flex items-center justify-between px-3 py-1 border-b border-slate-100 dark:border-warm-800 text-[10px] uppercase tracking-wider text-slate-400"
    >
      <span class="tabular-nums"
        >{grouped.totalMatched} item{grouped.totalMatched === 1
          ? ""
          : "s"}</span
      >
      <div class="flex items-center gap-2">
        <button
          onclick={expandAll}
          class="hover:text-slate-700 dark:hover:text-slate-200 cursor-pointer"
          >Expand all</button
        >
        <span class="text-slate-300 dark:text-warm-700">·</span>
        <button
          onclick={() => collapseAll(allGroupKeys)}
          class="hover:text-slate-700 dark:hover:text-slate-200 cursor-pointer"
          >Collapse all</button
        >
      </div>
    </div>
  {/if}

  <!-- Grouped list. Each folder is its own collapsible accordion
       header; clicking the header toggles its body. Items render
       with type icon, decrypted title, type label, and a couple of
       tag chips if present. -->
  <div class="flex-1 overflow-y-auto">
    {#if !firstFetchDone && grouped.totalMatched === 0}
      <div
        class="flex flex-col items-center justify-center py-12 px-4 text-center"
      >
        <div class="animate-pulse text-xs text-slate-400">Loading items…</div>
      </div>
    {:else if grouped.totalMatched === 0}
      <!-- Empty states: distinct text per bucket so the user gets a
           concrete next step rather than a generic "no items". -->
      <div
        class="flex flex-col items-center justify-center py-12 px-6 text-center text-slate-400 dark:text-slate-500"
      >
        {#if view === "trash"}
          <Trash2 size={28} class="mb-3 opacity-40" />
          <div
            class="text-sm font-medium text-slate-600 dark:text-slate-300 mb-1"
          >
            Trash is empty
          </div>
          <div class="text-xs">
            Soft-deleted items appear here for restore or permanent removal.
          </div>
        {:else if view === "archived"}
          <Archive size={28} class="mb-3 opacity-40" />
          <div
            class="text-sm font-medium text-slate-600 dark:text-slate-300 mb-1"
          >
            Nothing archived
          </div>
          <div class="text-xs">
            Archived items stay in your vault but out of the active list.
          </div>
        {:else if q || typeFilter || favoritesOnly}
          <Search size={28} class="mb-3 opacity-40" />
          <div
            class="text-sm font-medium text-slate-600 dark:text-slate-300 mb-1"
          >
            No items match
          </div>
          <div class="text-xs">
            Try a different search, type, or clear the favorites filter.
          </div>
        {:else}
          <KeyRound size={28} class="mb-3 opacity-40" />
          <div
            class="text-sm font-medium text-slate-600 dark:text-slate-300 mb-1"
          >
            Your vault is empty
          </div>
          <div class="text-xs mb-4">
            Add your first password, key, or note — everything is encrypted in
            your browser before it reaches the server.
          </div>
          <button
            onclick={() => onNew("")}
            class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 cursor-pointer"
          >
            <Plus size={12} /> Create your first item
          </button>
        {/if}
      </div>
    {:else}
      {#each grouped.groups as group (group.key)}
        {@const isCollapsed = collapsed.has(group.key)}
        {@const isRenaming =
          openMenu?.kind === "rename" && openMenu.folderKey === group.key}
        {@const isHeaderMenuOpen =
          openMenu?.kind === "header" && openMenu.folderKey === group.key}
        <div
          class="border-b border-slate-100 dark:border-warm-800 last:border-b-0"
        >
          <!-- Folder header. The chevron + folder icon + name + count
               pattern matches what 1Password and Bitwarden use in
               their grouped views. Click anywhere on the row to
               toggle. The kebab on the right (only on real folders,
               not the (No folder) pseudo-bucket) opens a small
               header menu with "Rename" — there's no "Delete folder"
               because folders aren't first-class entities; deleting
               every item in a bucket is what removes it. -->
          {#if isRenaming}
            <!-- Inline rename form replaces the header row entirely
                 so the user has the full width to type. Submitting
                 fires renameFolder() which updates every item in the
                 bucket. We swallow click events so they don't bubble
                 to svelte:window's closeMenus. -->
            <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <form
              class="flex items-center gap-1.5 px-2.5 py-1.5 bg-slate-100 dark:bg-warm-700"
              onclick={(e) => e.stopPropagation()}
              onsubmit={(e) => {
                e.preventDefault();
                renameFolder(group.key, folderDraft);
              }}
            >
              <FolderOpen
                size={13}
                class="shrink-0 text-accent-600 dark:text-accent-400"
              />
              <!-- svelte-ignore a11y_autofocus -->
              <input
                type="text"
                bind:value={folderDraft}
                placeholder={group.name}
                disabled={renameInFlight}
                autofocus
                onkeydown={(e) => {
                  if (e.key === "Escape") closeMenus();
                }}
                class="flex-1 min-w-0 px-1.5 py-0.5 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-1 focus:ring-accent-500"
              />
              <button
                type="submit"
                disabled={renameInFlight || !folderDraft.trim()}
                class="p-1 rounded text-accent-600 dark:text-accent-400 hover:bg-white dark:hover:bg-warm-800 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                title="Save"
                aria-label="Save folder name"
              >
                <Check size={12} />
              </button>
              <button
                type="button"
                onclick={closeMenus}
                disabled={renameInFlight}
                class="p-1 rounded text-slate-500 hover:bg-white dark:hover:bg-warm-800 cursor-pointer"
                title="Cancel"
                aria-label="Cancel rename"
              >
                <X size={12} />
              </button>
            </form>
          {:else}
            <div
              class="relative flex items-center hover:bg-slate-100 dark:hover:bg-warm-700 group/folder"
            >
              <button
                class="flex-1 min-w-0 flex items-center gap-1.5 px-2.5 py-2 text-xs cursor-pointer"
                onclick={() => toggleCollapsed(group.key)}
                aria-expanded={!isCollapsed}
                aria-controls="folder-body-{group.key}"
              >
                {#if isCollapsed}
                  <ChevronRight size={12} class="shrink-0 text-slate-400" />
                {:else}
                  <ChevronDown size={12} class="shrink-0 text-slate-400" />
                {/if}
                {#if group.pseudo}
                  <Folder size={13} class="shrink-0 opacity-50" />
                {:else if isCollapsed}
                  <Folder
                    size={13}
                    class="shrink-0 text-accent-600 dark:text-accent-400"
                  />
                {:else}
                  <FolderOpen
                    size={13}
                    class="shrink-0 text-accent-600 dark:text-accent-400"
                  />
                {/if}
                <span
                  class="flex-1 min-w-0 text-left font-medium uppercase tracking-wider text-[11px] text-slate-600 dark:text-slate-300 truncate {group.pseudo
                    ? 'italic font-normal text-slate-500'
                    : ''}"
                >
                  {group.name}
                </span>
                <span class="text-[10px] tabular-nums text-slate-400"
                  >{group.items.length}</span
                >
              </button>

              <!-- Header kebab. Hidden on the (No folder) pseudo
                   bucket since there's nothing to rename — that
                   bucket is a derived view, not a stored entity. -->
              {#if !group.pseudo}
                <div class="relative shrink-0">
                  <button
                    type="button"
                    onclick={(e) => openHeaderMenu(e, group.key)}
                    class="p-1 mr-1 rounded text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 hover:bg-white dark:hover:bg-warm-800 cursor-pointer opacity-0 group-hover/folder:opacity-100 focus:opacity-100 {isHeaderMenuOpen
                      ? 'opacity-100 bg-white dark:bg-warm-800'
                      : ''}"
                    aria-label="Folder actions"
                    aria-haspopup="menu"
                    aria-expanded={isHeaderMenuOpen}
                  >
                    <MoreVertical size={12} />
                  </button>
                  {#if isHeaderMenuOpen}
                    <!-- svelte-ignore a11y_click_events_have_key_events -->
                    <div
                      role="menu"
                      tabindex="-1"
                      onclick={(e) => e.stopPropagation()}
                      class="absolute right-0 top-full z-20 mt-0.5 min-w-[10rem] rounded-md border border-slate-200 dark:border-warm-600 bg-white dark:bg-warm-800 shadow-lg py-1 text-xs"
                    >
                      <button
                        type="button"
                        role="menuitem"
                        onclick={() => startRename(group.key, group.name)}
                        class="w-full flex items-center gap-2 px-3 py-1.5 text-left text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer"
                      >
                        <Pencil size={11} class="shrink-0 text-slate-400" />
                        Rename folder
                      </button>
                    </div>
                  {/if}
                </div>
              {/if}
            </div>
          {/if}

          {#if !isCollapsed}
            <div id="folder-body-{group.key}">
              {#each group.items as item (item.id)}
                {@const Icon = iconFor(item.type)}
                {@const titleText = decryptedTitle(item)}
                {@const tags = tagsForRow(item)}
                {@const accent = vaultItemAccent(item.type)}
                {@const isRowMenuOpen =
                  openMenu?.kind === "row" && openMenu.itemId === item.id}
                {@const targets = isRowMenuOpen ? moveTargets(item) : []}
                {@const currentFolder = decryptedFolder(item)}
                <!-- Row container. We wrap the click-to-select button
                     and a sibling kebab button together so the kebab
                     can sit on top without nesting interactive
                     elements. The kebab fades in on row hover. -->
                <div
                  class="relative flex items-stretch border-l-2 group/row
                  {selectedId === item.id
                    ? 'bg-accent-50 border-accent-500 dark:bg-accent-900/40'
                    : 'border-transparent hover:bg-slate-100 dark:hover:bg-warm-700'}"
                >
                  <!-- Selected row uses the same teal-tint formula as
                       Settings.svelte's active nav entry. -->
                  <button
                    class="flex-1 min-w-0 flex items-start gap-2.5 pl-7 pr-1 py-2 text-left text-sm cursor-pointer
                      {selectedId === item.id
                      ? 'text-accent-700 dark:text-accent-300'
                      : 'text-slate-700 dark:text-slate-200'}"
                    onclick={() => onSelect(item.id)}
                    aria-current={selectedId === item.id ? "true" : undefined}
                  >
                    <!-- Type-colored tile. The stem is supplied by
                         vaultItemAccent() per the table in
                         DESIGN_SYSTEM.md §11. -->
                    <div
                      class="shrink-0 mt-0.5 w-8 h-8 rounded-md flex items-center justify-center {accent.tile}"
                    >
                      <Icon size={16} />
                    </div>

                    <div class="flex-1 min-w-0">
                      <!-- First line: title + favorite badge -->
                      <div class="flex items-center gap-1.5">
                        <span
                          class="truncate font-medium {titleText ===
                          '(unreadable)'
                            ? 'italic text-red-500'
                            : ''}"
                        >
                          {titleText || "(untitled)"}
                        </span>
                        {#if item.favorite}
                          <Star
                            size={11}
                            fill="currentColor"
                            class="shrink-0 text-amber-500"
                          />
                        {/if}
                      </div>
                      <!-- Second line: type label + tag chips. -->
                      <div class="flex items-center gap-1.5 mt-0.5 min-w-0">
                        <span
                          class="shrink-0 text-[10px] uppercase tracking-wider text-slate-400"
                        >
                          {typeLabel(item.type)}
                        </span>
                        {#if tags.visible.length > 0}
                          <span class="text-slate-300 dark:text-warm-700"
                            >·</span
                          >
                          <div
                            class="flex items-center gap-1 min-w-0 overflow-hidden"
                          >
                            {#each tags.visible as tag (tag)}
                              <span
                                class="shrink-0 px-1.5 py-px text-[10px] rounded bg-slate-100 dark:bg-warm-800 text-slate-600 dark:text-slate-300 truncate max-w-[6rem]"
                              >
                                {tag}
                              </span>
                            {/each}
                            {#if tags.extra > 0}
                              <span class="shrink-0 text-[10px] text-slate-400"
                                >+{tags.extra}</span
                              >
                            {/if}
                          </div>
                        {/if}
                      </div>
                    </div>
                  </button>

                  <!-- Per-row kebab — only "Move to folder" for now,
                       but the popover is structured so additional
                       actions (Favorite, Archive…) can slot in
                       above the divider later. We hide the kebab
                       in the trash view because moving a soft-
                       deleted item between folders is meaningless. -->
                  {#if view !== "trash"}
                    <div class="relative flex items-center shrink-0 pr-1.5">
                      <button
                        type="button"
                        onclick={(e) => openRowMenu(e, item.id)}
                        class="p-1 rounded text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 hover:bg-white dark:hover:bg-warm-800 cursor-pointer opacity-0 group-hover/row:opacity-100 focus:opacity-100 {isRowMenuOpen
                          ? 'opacity-100 bg-white dark:bg-warm-800'
                          : ''}"
                        aria-label="Item actions"
                        aria-haspopup="menu"
                        aria-expanded={isRowMenuOpen}
                      >
                        <MoreVertical size={13} />
                      </button>
                      {#if isRowMenuOpen}
                        <!-- Move-to-folder popover. We list every
                             other folder + the (No folder) option
                             + an inline "New folder…" creator. The
                             checkmark on the current folder gives
                             the user feedback about where the item
                             currently lives without needing a
                             separate label. -->
                        <!-- svelte-ignore a11y_click_events_have_key_events -->
                        <div
                          role="menu"
                          tabindex="-1"
                          onclick={(e) => e.stopPropagation()}
                          class="absolute right-0 top-full z-20 mt-0.5 w-56 rounded-md border border-slate-200 dark:border-warm-600 bg-white dark:bg-warm-800 shadow-lg py-1 text-xs"
                        >
                          <div
                            class="px-3 py-1 text-[10px] uppercase tracking-wider text-slate-400 flex items-center gap-1.5"
                          >
                            <FolderInput size={11} /> Move to folder
                          </div>
                          <div class="max-h-60 overflow-y-auto">
                            <!-- (No folder) — always available unless
                                 the item is already there. -->
                            {#if currentFolder !== ""}
                              <button
                                type="button"
                                role="menuitem"
                                onclick={() => moveItemToFolder(item, "")}
                                class="w-full flex items-center gap-2 px-3 py-1.5 text-left text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer"
                              >
                                <FolderMinus
                                  size={11}
                                  class="shrink-0 text-slate-400"
                                />
                                <span class="italic text-slate-500"
                                  >(No folder)</span
                                >
                              </button>
                            {/if}
                            {#each targets as f (f)}
                              <button
                                type="button"
                                role="menuitem"
                                onclick={() => moveItemToFolder(item, f)}
                                class="w-full flex items-center gap-2 px-3 py-1.5 text-left text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer"
                              >
                                <Folder
                                  size={11}
                                  class="shrink-0 text-accent-600 dark:text-accent-400"
                                />
                                <span class="truncate flex-1">{f}</span>
                              </button>
                            {/each}
                            {#if currentFolder !== "" || targets.length > 0}
                              <div
                                class="my-1 border-t border-slate-100 dark:border-warm-700"
                              ></div>
                            {/if}
                            <!-- Inline "new folder" creator. We bind
                                 the same `folderDraft` state used by
                                 rename — only one popover is open at
                                 a time so the field can't collide. -->
                            <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
                            <!-- svelte-ignore a11y_click_events_have_key_events -->
                            <form
                              class="px-2 py-1 flex items-center gap-1"
                              onclick={(e) => e.stopPropagation()}
                              onsubmit={(e) => {
                                e.preventDefault();
                                if (folderDraft.trim())
                                  moveItemToFolder(item, folderDraft);
                              }}
                            >
                              <Plus size={11} class="shrink-0 text-slate-400" />
                              <input
                                type="text"
                                bind:value={folderDraft}
                                placeholder="New folder…"
                                class="flex-1 min-w-0 px-1.5 py-0.5 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-1 focus:ring-accent-500"
                                onkeydown={(e) => {
                                  if (e.key === "Escape") closeMenus();
                                }}
                              />
                              <button
                                type="submit"
                                disabled={!folderDraft.trim()}
                                class="p-0.5 rounded text-accent-600 dark:text-accent-400 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                                title="Create and move"
                                aria-label="Create folder and move item"
                              >
                                <Check size={11} />
                              </button>
                            </form>
                          </div>
                        </div>
                      {/if}
                    </div>
                  {/if}
                </div>
              {/each}
            </div>
          {/if}
        </div>
      {/each}
    {/if}
  </div>

  <!-- Idle-lock status footer. Countdown re-derives every second
       from the store; the Lock button gives the user an explicit
       escape hatch when stepping away. -->
  <div
    class="flex items-center gap-2 px-3 py-2 border-t border-slate-200 dark:border-warm-700 text-[11px] text-slate-500 dark:text-slate-400"
  >
    <Clock size={12} class="shrink-0" />
    <span
      class="flex-1 tabular-nums"
      title="Vault auto-locks after this time without activity"
    >
      Locks in {formatCountdown(vaultStore.remainingLockSeconds)}
    </span>
    <button
      onclick={() => vaultStore.lock()}
      class="flex items-center gap-1 px-2 py-1 rounded hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer"
      title="Lock the vault now"
    >
      <Lock size={11} /> Lock
    </button>
  </div>
</div>
