<script lang="ts">
  import {
    Search,
    Star,
    StarOff,
    Archive,
    ArchiveRestore,
    Trash2,
    RotateCcw,
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
    Folder,
    FolderOpen,
    FolderTree,
    FolderInput,
    FolderMinus,
    ChevronRight,
    ChevronDown,
    ChevronsDownUp,
    ChevronsUpDown,
    X,
    MoreVertical,
    Pencil,
    Check,
    Copy,
    User,
    ExternalLink,
    NotebookPen,
  } from "lucide-svelte";
  import { vaultStore } from "@/lib/vault/store.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import { confirmDialog } from "@/lib/store/confirm.svelte";
  import { apiErrorMessage } from "@/lib/api/client";
  import { copySecret } from "@/lib/vault/clipboard";
  import { setDragChip } from "@/lib/vault/dragchip";
  import { ITEM_DRAG_MIME } from "./VaultSidebar.svelte";
  import { typeLabel, vaultItemAccent } from "@/lib/vault/templates";
  import {
    expiryState,
    itemSubtitle,
    relativeTime,
    searchText,
    secretOf,
    urlOf,
    usernameOf,
    type ExpiryState,
  } from "@/lib/vault/itemSummary";
  import type { VaultItem, VaultItemType, VaultListFilter } from "@/lib/vault/api";

  interface Props {
    selectedId: string | null;
    onSelect: (id: string | null) => void;
    /** Receives the active folder so the new-item dialog can prefill it. */
    onNew: (defaultFolder: string) => void;
    /** Creates an empty note in the active folder and opens it. */
    onNewNote: (folder: string) => void;
    view: "active" | "archived" | "trash";
    favoritesOnly: boolean;
    /** Restrict to one folder (case-insensitive); null = every folder. */
    folder: string | null;
  }
  let { selectedId, onSelect, onNew, onNewNote, view, favoritesOnly, folder }: Props = $props();

  // ─── Preferences ────────────────────────────────────────────────
  type SortKey = "recent" | "name" | "modified";
  const SORT_KEY = "pika.vault.sort";
  const GROUP_KEY = "pika.vault.group";

  function readPref<T extends string>(key: string, allowed: readonly T[], fallback: T): T {
    try {
      const v = localStorage.getItem(key) as T | null;
      return v && allowed.includes(v) ? v : fallback;
    } catch {
      return fallback;
    }
  }
  function writePref(key: string, value: string) {
    try {
      localStorage.setItem(key, value);
    } catch {
      /* private mode / quota: keep in-memory value */
    }
  }

  let sort = $state<SortKey>(readPref(SORT_KEY, ["recent", "name", "modified"] as const, "recent"));
  let groupByFolder = $state(readPref(GROUP_KEY, ["1", "0"] as const, "0") === "1");
  $effect(() => writePref(SORT_KEY, sort));
  $effect(() => writePref(GROUP_KEY, groupByFolder ? "1" : "0"));

  let typeFilter = $state<VaultItemType | "">("");
  let q = $state("");
  let searchEl: HTMLInputElement | undefined = $state();

  const heading = $derived(
    folder !== null
      ? folder
      : view === "trash"
        ? "Trash"
        : view === "archived"
          ? "Archive"
          : favoritesOnly
            ? "Favorites"
            : "All items",
  );

  // ─── Fetch ──────────────────────────────────────────────────────
  // Type filtering runs client-side so the type picker can show every
  // type present in the bucket with its count.
  let firstFetchDone = $state(false);
  $effect(() => {
    const filter: VaultListFilter = {};
    if (favoritesOnly) filter.favorite = true;
    if (view === "archived") filter.archived = "only";
    if (view === "trash") filter.trash = true;
    vaultStore.refreshItems(filter).finally(() => {
      firstFetchDone = true;
    });
  });

  // ─── Per-item derived info ──────────────────────────────────────
  type Info = {
    title: string;
    unreadable: boolean;
    folder: string;
    tags: string[];
    subtitle: string;
    haystack: string;
    expiry: ExpiryState | null;
    user: string;
    secret?: { label: string; value: string };
    url: string;
  };

  const info = $derived.by(() => {
    const out = new Map<string, Info>();
    for (const i of vaultStore.items) {
      const d = vaultStore.decrypted.get(i.id);
      const p = d?.payload ?? null;
      const title = d ? (d.title ?? "") : "";
      const tags = d?.tags ?? [];
      const f = (d?.folder ?? "").trim();
      const secret = secretOf(p);
      out.set(i.id, {
        title,
        unreadable: !!d && d.title === null,
        folder: f,
        tags,
        subtitle: itemSubtitle(i.type, p, title),
        haystack: [title, f, ...tags, ...(d?.hostnames ?? []), typeLabel(i.type), searchText(p)]
          .join("\n")
          .toLowerCase(),
        expiry: expiryState(i.type, p),
        user: usernameOf(p),
        secret: secret ? { label: secret.label || "secret", value: secret.value } : undefined,
        url: urlOf(p),
      });
    }
    return out;
  });

  // Items in the current bucket, before search / type filtering.
  const bucket = $derived.by(() => {
    const onlyFolder = folder !== null ? folder.trim().toLowerCase() : null;
    return vaultStore.items.filter((i) => {
      const inTrash = !!i.deleted_at;
      if (view === "trash") {
        if (!inTrash) return false;
      } else if (inTrash || !!i.archived !== (view === "archived")) {
        return false;
      }
      if (favoritesOnly && !i.favorite) return false;
      if (onlyFolder !== null && (info.get(i.id)?.folder ?? "").toLowerCase() !== onlyFolder) return false;
      return true;
    });
  });

  const typeCounts = $derived.by(() => {
    const m = new Map<VaultItemType, number>();
    for (const i of bucket) m.set(i.type, (m.get(i.type) ?? 0) + 1);
    return Array.from(m.entries()).sort((a, b) => typeLabel(a[0]).localeCompare(typeLabel(b[0])));
  });

  const terms = $derived(
    q
      .trim()
      .toLowerCase()
      .split(/\s+/)
      .filter(Boolean),
  );

  const matched = $derived.by(() => {
    const list = bucket.filter((i) => {
      if (typeFilter && i.type !== typeFilter) return false;
      if (!terms.length) return true;
      const h = info.get(i.id)?.haystack ?? "";
      return terms.every((t) => h.includes(t));
    });
    const title = (i: VaultItem) => info.get(i.id)?.title ?? "";
    const time = (s?: string) => (s ? Date.parse(s) || 0 : 0);
    const byName = (a: VaultItem, b: VaultItem) =>
      title(a).localeCompare(title(b), undefined, { numeric: true, sensitivity: "base" });
    list.sort((a, b) => {
      if (sort === "recent") {
        const d = time(b.last_used_at ?? b.updated_at) - time(a.last_used_at ?? a.updated_at);
        return d || byName(a, b);
      }
      if (sort === "modified") return time(b.updated_at) - time(a.updated_at) || byName(a, b);
      return byName(a, b);
    });
    return list;
  });

  // ─── Grouping ───────────────────────────────────────────────────
  const NONE_KEY = "__none__";
  const canGroup = $derived(folder === null && view !== "trash");
  const grouped = $derived(groupByFolder && canGroup);

  type Group = { key: string; name: string; pseudo: boolean; items: VaultItem[] };
  const groups = $derived.by<Group[]>(() => {
    if (!grouped) return [{ key: "__all__", name: "", pseudo: true, items: matched }];
    const byFolder = new Map<string, Group>();
    const none: VaultItem[] = [];
    for (const i of matched) {
      const f = info.get(i.id)?.folder ?? "";
      if (!f) {
        none.push(i);
        continue;
      }
      const key = f.toLowerCase();
      const g = byFolder.get(key);
      if (g) g.items.push(i);
      else byFolder.set(key, { key, name: f, pseudo: false, items: [i] });
    }
    const out = Array.from(byFolder.values()).sort((a, b) => a.name.localeCompare(b.name));
    if (none.length) out.push({ key: NONE_KEY, name: "No folder", pseudo: true, items: none });
    return out;
  });

  // Collapsed folder groups, persisted per vault account.
  const COLLAPSED_PREFIX = "pika.vault.collapsed.";
  function collapsedKey(): string | null {
    const uid = vaultStore.account?.user_id;
    return uid ? COLLAPSED_PREFIX + uid : null;
  }
  function loadCollapsed(): Set<string> {
    try {
      const k = collapsedKey();
      const arr = k ? JSON.parse(localStorage.getItem(k) ?? "[]") : [];
      return new Set(Array.isArray(arr) ? arr.filter((s) => typeof s === "string") : []);
    } catch {
      return new Set();
    }
  }
  let collapsed = $state<Set<string>>(loadCollapsed());
  $effect(() => {
    if (vaultStore.account?.user_id) collapsed = loadCollapsed();
  });
  function saveCollapsed(next: Set<string>) {
    collapsed = next;
    const k = collapsedKey();
    if (k) writePref(k, JSON.stringify(Array.from(next)));
  }
  function toggleCollapsed(key: string) {
    const next = new Set(collapsed);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    saveCollapsed(next);
  }

  // Visible rows in display order, for keyboard navigation.
  const visibleIds = $derived(
    groups.flatMap((g) => (grouped && collapsed.has(g.key) ? [] : g.items.map((i) => i.id))),
  );

  // ─── Search highlighting ────────────────────────────────────────
  function highlight(text: string): { t: string; m: boolean }[] {
    if (!terms.length || !text) return [{ t: text, m: false }];
    const lower = text.toLowerCase();
    const out: { t: string; m: boolean }[] = [];
    let pos = 0;
    while (pos < text.length) {
      let best = -1;
      let len = 0;
      for (const t of terms) {
        const i = lower.indexOf(t, pos);
        if (i !== -1 && (best === -1 || i < best || (i === best && t.length > len))) {
          best = i;
          len = t.length;
        }
      }
      if (best === -1) break;
      if (best > pos) out.push({ t: text.slice(pos, best), m: false });
      out.push({ t: text.slice(best, best + len), m: true });
      pos = best + len;
    }
    if (pos < text.length) out.push({ t: text.slice(pos), m: false });
    return out;
  }

  // ─── Quick actions ──────────────────────────────────────────────
  let copied = $state<string | null>(null);
  let copiedTimer: ReturnType<typeof setTimeout> | undefined;

  async function quickCopy(item: VaultItem, kind: "user" | "secret", value: string, label: string) {
    try {
      await copySecret(value);
    } catch {
      addToast("Clipboard unavailable", "warn");
      return;
    }
    void vaultStore.touchItem(item.id);
    copied = `${item.id}:${kind}`;
    clearTimeout(copiedTimer);
    copiedTimer = setTimeout(() => (copied = null), 1500);
    addToast(kind === "secret" ? `${label} copied · clears in 30s` : `${label} copied`, "success", 1800);
  }

  function openUrl(item: VaultItem, url: string) {
    window.open(url, "_blank", "noopener,noreferrer");
    void vaultStore.touchItem(item.id);
  }

  async function run(action: () => Promise<unknown>, ok: string, fail: string) {
    closeMenus();
    try {
      await action();
      addToast(ok, "success", 1800);
    } catch (err) {
      addToast(apiErrorMessage(err, fail), "alert");
    }
  }

  function leaveBucket(id: string) {
    if (selectedId === id) onSelect(null);
  }

  const toggleFavorite = (item: VaultItem) =>
    run(
      () => vaultStore.updateItem(item.id, { expected_version: item.version }, { favorite: !item.favorite }),
      item.favorite ? "Removed from favorites" : "Added to favorites",
      "Failed to update item",
    );

  const toggleArchive = (item: VaultItem) =>
    run(
      async () => {
        await vaultStore.updateItem(item.id, { expected_version: item.version }, { archived: !item.archived });
        leaveBucket(item.id);
      },
      item.archived ? "Moved back to items" : "Archived",
      "Failed to update item",
    );

  const trashItem = (item: VaultItem) =>
    run(
      async () => {
        await vaultStore.softDeleteItem(item.id);
        leaveBucket(item.id);
      },
      "Moved to trash",
      "Failed to delete item",
    );

  const restoreItem = (item: VaultItem) =>
    run(
      async () => {
        await vaultStore.restoreItem(item.id);
        leaveBucket(item.id);
      },
      "Restored",
      "Failed to restore item",
    );

  async function purgeItem(item: VaultItem) {
    closeMenus();
    const ok = await confirmDialog({
      title: "Permanently delete this item?",
      message: "This cannot be undone.",
      confirmLabel: "Delete permanently",
      danger: true,
    });
    if (!ok) return;
    await run(
      async () => {
        await vaultStore.purgeItem(item.id);
        leaveBucket(item.id);
      },
      "Deleted permanently",
      "Failed to delete item",
    );
  }

  // ─── Folders: move / rename ─────────────────────────────────────
  type OpenMenu =
    | { kind: "row"; itemId: string }
    | { kind: "header"; folderKey: string }
    | { kind: "rename"; folderKey: string }
    | null;
  let openMenu = $state<OpenMenu>(null);
  let folderDraft = $state("");
  let renameInFlight = $state(false);

  function closeMenus() {
    openMenu = null;
    folderDraft = "";
  }

  function toggleRowMenu(e: MouseEvent, itemId: string) {
    e.stopPropagation();
    const open = openMenu?.kind === "row" && openMenu.itemId === itemId;
    closeMenus();
    if (!open) openMenu = { kind: "row", itemId };
  }

  function toggleHeaderMenu(e: MouseEvent, folderKey: string) {
    e.stopPropagation();
    const open = openMenu?.kind === "header" && openMenu.folderKey === folderKey;
    closeMenus();
    if (!open) openMenu = { kind: "header", folderKey };
  }

  async function moveItemToFolder(item: VaultItem, target: string) {
    target = target.trim();
    if (target.toLowerCase() === (info.get(item.id)?.folder ?? "").toLowerCase()) {
      closeMenus();
      return;
    }
    await run(
      () => vaultStore.updateItem(item.id, { expected_version: item.version }, { folder: target }),
      target ? `Moved to ${target}` : "Removed from folder",
      "Failed to move item",
    );
  }

  // Renaming a folder rewrites every item in it. A failure part-way
  // leaves the folder split, so report exactly how far it got.
  async function renameFolder(group: Group, newName: string) {
    const target = newName.trim();
    if (group.pseudo || !target || target.toLowerCase() === group.name.toLowerCase()) {
      closeMenus();
      return;
    }
    const all = vaultStore.items.filter(
      (i) => (info.get(i.id)?.folder ?? "").toLowerCase() === group.key,
    );
    renameInFlight = true;
    let done = 0;
    try {
      for (const item of all) {
        await vaultStore.updateItem(item.id, { expected_version: item.version }, { folder: target });
        done++;
      }
      addToast(`Renamed to ${target}`, "success", 1800);
    } catch (err) {
      addToast(
        `${apiErrorMessage(err, "Rename failed")} — moved ${done} of ${all.length} items to "${target}".`,
        "alert",
        8000,
      );
    } finally {
      renameInFlight = false;
      closeMenus();
    }
  }

  function moveTargets(item: VaultItem): string[] {
    const current = (info.get(item.id)?.folder ?? "").toLowerCase();
    return vaultStore.allFolders().filter((f) => f.toLowerCase() !== current);
  }

  // ─── Drag & drop ────────────────────────────────────────────────
  let dropGroup = $state<string | null>(null);

  function onRowDragStart(e: DragEvent, item: VaultItem) {
    if (!e.dataTransfer) return;
    const title = info.get(item.id)?.title || "(untitled)";
    e.dataTransfer.setData(ITEM_DRAG_MIME, item.id);
    e.dataTransfer.setData("text/plain", title);
    e.dataTransfer.effectAllowed = "move";
    setDragChip(e.dataTransfer, title, vaultItemAccent(item.type).dot);
  }

  function acceptsItem(e: DragEvent): boolean {
    return Array.from(e.dataTransfer?.types ?? []).includes(ITEM_DRAG_MIME);
  }

  async function onGroupDrop(e: DragEvent, group: Group) {
    e.preventDefault();
    dropGroup = null;
    const id = e.dataTransfer?.getData(ITEM_DRAG_MIME);
    const item = id ? vaultStore.items.find((i) => i.id === id) : undefined;
    if (item) await moveItemToFolder(item, group.pseudo ? "" : group.name);
  }

  // ─── Keyboard ───────────────────────────────────────────────────
  let listEl: HTMLDivElement | undefined = $state();

  function focusRow(id: string) {
    const el = listEl?.querySelector<HTMLElement>(`[data-item-id="${CSS.escape(id)}"]`);
    el?.focus();
    el?.scrollIntoView({ block: "nearest" });
  }

  function step(delta: number | "first" | "last") {
    const ids = visibleIds;
    if (!ids.length) return;
    const cur = selectedId ? ids.indexOf(selectedId) : -1;
    let next: number;
    if (delta === "first") next = 0;
    else if (delta === "last") next = ids.length - 1;
    else next = cur === -1 ? (delta > 0 ? 0 : ids.length - 1) : Math.min(ids.length - 1, Math.max(0, cur + delta));
    onSelect(ids[next]);
    focusRow(ids[next]);
  }

  function onListKeydown(e: KeyboardEvent) {
    if ((e.target as HTMLElement).closest("input, textarea, select, [role='menu']")) return;
    const keys: Record<string, number | "first" | "last"> = {
      ArrowDown: 1,
      ArrowUp: -1,
      j: 1,
      k: -1,
      Home: "first",
      End: "last",
    };
    if (e.key in keys) {
      e.preventDefault();
      step(keys[e.key]);
    }
  }

  function onSearchKeydown(e: KeyboardEvent) {
    if (e.key === "ArrowDown" || e.key === "Enter") {
      e.preventDefault();
      step("first");
    } else if (e.key === "Escape") {
      if (q) q = "";
      else searchEl?.blur();
    }
  }

  function onWindowKeydown(e: KeyboardEvent) {
    if (e.key === "Escape") closeMenus();
    if (e.key !== "/" || e.metaKey || e.ctrlKey || e.altKey) return;
    if ((e.target as HTMLElement).closest("input, textarea, select, [contenteditable]")) return;
    e.preventDefault();
    searchEl?.focus();
    searchEl?.select();
  }

  // ─── Rendering helpers ──────────────────────────────────────────
  const ICONS: Record<VaultItemType, typeof KeyRound> = {
    login: KeyRound,
    card: CreditCard,
    identity: UserSquare2,
    secure_note: FileText,
    ssh_key: Terminal,
    api_credential: Plug,
    database: Database,
    server: Server,
    license: FileBadge,
    tls_cert: ShieldCheck,
  };

  function metaFor(item: VaultItem, i: Info | undefined): string {
    if (sort === "recent") return relativeTime(item.last_used_at ?? item.updated_at);
    if (sort === "modified") return relativeTime(item.updated_at);
    return grouped || folder !== null ? "" : (i?.folder ?? "");
  }

  const filtersActive = $derived(!!q || !!typeFilter || favoritesOnly || folder !== null);

  const quickBtn =
    "p-1 rounded text-slate-400 hover:text-slate-700 dark:hover:text-slate-100 hover:bg-white dark:hover:bg-warm-800 cursor-pointer";
  const menuItem =
    "w-full flex items-center gap-2 px-3 py-1.5 text-left text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer";
  const barSelect =
    "max-w-[11rem] py-0.5 pr-1 text-[11px] rounded bg-white dark:bg-warm-900 text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-100 cursor-pointer focus:outline-none focus:ring-2 focus:ring-accent-500";
</script>

<svelte:window onclick={closeMenus} onkeydown={onWindowKeydown} />

<div
  class="flex flex-col h-full w-full border-r border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-900"
>
  <!-- Header -->
  <div class="px-3 pt-3 pb-2 space-y-2 border-b border-slate-200 dark:border-warm-700">
    <div class="flex items-center gap-2">
      <h2 class="min-w-0 truncate text-base font-semibold text-slate-800 dark:text-slate-100">
        {heading}
      </h2>
      <span class="text-[11px] tabular-nums text-slate-400 dark:text-slate-500">
        {matched.length}{matched.length !== bucket.length ? ` / ${bucket.length}` : ""}
      </span>
      <div class="flex-1"></div>
      {#if canGroup}
        <button
          class="p-1.5 rounded cursor-pointer {groupByFolder
            ? 'bg-accent-50 text-accent-700 dark:bg-accent-900/40 dark:text-accent-300'
            : 'text-slate-500 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-warm-700'}"
          onclick={() => (groupByFolder = !groupByFolder)}
          title={groupByFolder ? "Show as one list" : "Group by folder"}
          aria-label="Group by folder"
          aria-pressed={groupByFolder}
        >
          <FolderTree size={14} />
        </button>
      {/if}
      {#if view === "active"}
        <div class="flex rounded overflow-hidden">
          <button
            onclick={() => onNew(folder ?? "")}
            class="inline-flex items-center gap-1 px-2.5 py-1.5 text-xs bg-accent-600 text-white font-medium hover:bg-accent-700 cursor-pointer"
            title="New item"
          >
            <Plus size={12} /> New
          </button>
          <button
            onclick={() => onNewNote(folder ?? "")}
            class="inline-flex items-center px-2 py-1.5 text-xs bg-accent-600 text-white hover:bg-accent-700 border-l border-accent-500 cursor-pointer"
            title="New note"
            aria-label="New note"
          >
            <NotebookPen size={13} />
          </button>
        </div>
      {/if}
    </div>

    <div class="relative">
      <Search
        size={14}
        class="absolute top-1/2 left-2.5 -translate-y-1/2 text-slate-400 pointer-events-none"
      />
      <input
        bind:this={searchEl}
        type="text"
        bind:value={q}
        onkeydown={onSearchKeydown}
        placeholder="Search name, user, site, tag…"
        aria-label="Search items"
        class="w-full pl-8 pr-8 py-1.5 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
      />
      {#if q}
        <button
          onclick={() => {
            q = "";
            searchEl?.focus();
          }}
          class="absolute top-1/2 right-1.5 -translate-y-1/2 p-0.5 rounded hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer"
          title="Clear search"
          aria-label="Clear search"
        >
          <X size={11} class="text-slate-400" />
        </button>
      {:else}
        <kbd
          class="absolute top-1/2 right-2 -translate-y-1/2 px-1 text-[10px] font-mono rounded border border-slate-200 dark:border-warm-700 text-slate-400 pointer-events-none"
          title="Press / to search">/</kbd
        >
      {/if}
    </div>

    <div class="flex items-center gap-2">
      <select bind:value={typeFilter} aria-label="Filter by type" class={barSelect}>
        <option value="">All types</option>
        {#each typeCounts as [t, n] (t)}
          <option value={t}>{typeLabel(t)} ({n})</option>
        {/each}
        {#if typeFilter && !typeCounts.some(([t]) => t === typeFilter)}
          <option value={typeFilter}>{typeLabel(typeFilter)} (0)</option>
        {/if}
      </select>
      <div class="flex-1"></div>
      {#if grouped && groups.length > 1}
        <button
          class="p-0.5 rounded text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 cursor-pointer"
          onclick={() => saveCollapsed(new Set())}
          title="Expand all folders"
          aria-label="Expand all folders"
        >
          <ChevronsUpDown size={13} />
        </button>
        <button
          class="p-0.5 rounded text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 cursor-pointer"
          onclick={() => saveCollapsed(new Set(groups.map((g) => g.key)))}
          title="Collapse all folders"
          aria-label="Collapse all folders"
        >
          <ChevronsDownUp size={13} />
        </button>
      {/if}
      <select bind:value={sort} aria-label="Sort items" class={barSelect}>
        <option value="recent">Recently used</option>
        <option value="modified">Recently changed</option>
        <option value="name">Name</option>
      </select>
    </div>
  </div>

  <!-- List -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="flex-1 overflow-y-auto" bind:this={listEl} onkeydown={onListKeydown}>
    {#if !firstFetchDone && matched.length === 0}
      <div class="py-12 text-center animate-pulse text-xs text-slate-400">Loading items…</div>
    {:else if matched.length === 0}
      <div class="flex flex-col items-center justify-center py-12 px-6 text-center text-slate-400 dark:text-slate-500">
        {#if filtersActive && bucket.length > 0}
          <Search size={28} class="mb-3 opacity-40" />
          <div class="text-sm font-medium text-slate-600 dark:text-slate-300 mb-1">No items match</div>
          <div class="text-xs mb-3">Search covers names, usernames, sites, tags and other non-secret fields.</div>
          <button
            class="text-xs text-accent-700 dark:text-accent-300 hover:underline cursor-pointer"
            onclick={() => {
              q = "";
              typeFilter = "";
            }}>Clear filters</button
          >
        {:else if view === "trash"}
          <Trash2 size={28} class="mb-3 opacity-40" />
          <div class="text-sm font-medium text-slate-600 dark:text-slate-300 mb-1">Trash is empty</div>
          <div class="text-xs">Deleted items stay here until you restore or permanently remove them.</div>
        {:else if view === "archived"}
          <Archive size={28} class="mb-3 opacity-40" />
          <div class="text-sm font-medium text-slate-600 dark:text-slate-300 mb-1">Nothing archived</div>
          <div class="text-xs">Archived items stay in your vault but out of the main list.</div>
        {:else if favoritesOnly}
          <Star size={28} class="mb-3 opacity-40" />
          <div class="text-sm font-medium text-slate-600 dark:text-slate-300 mb-1">No favorites yet</div>
          <div class="text-xs">Star an item from its menu to pin it here.</div>
        {:else if folder !== null}
          <Folder size={28} class="mb-3 opacity-40" />
          <div class="text-sm font-medium text-slate-600 dark:text-slate-300 mb-1">This folder is empty</div>
          <div class="text-xs">Drag items onto the folder in the sidebar to file them.</div>
        {:else}
          <KeyRound size={28} class="mb-3 opacity-40" />
          <div class="text-sm font-medium text-slate-600 dark:text-slate-300 mb-1">Your vault is empty</div>
          <div class="text-xs mb-4">
            Add your first password, key, or note. Everything is encrypted in your browser before it
            reaches the server.
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
      {#each groups as group (group.key)}
        {@const isCollapsed = grouped && collapsed.has(group.key)}
        {#if grouped}
          {@render groupHeader(group, isCollapsed)}
        {/if}
        {#if !isCollapsed}
          <div role="list">
            {#each group.items as item (item.id)}
              {@render row(item)}
            {/each}
          </div>
        {/if}
      {/each}
    {/if}
  </div>
</div>

{#snippet groupHeader(group: Group, isCollapsed: boolean)}
  {@const isRenaming = openMenu?.kind === "rename" && openMenu.folderKey === group.key}
  {@const menuOpen = openMenu?.kind === "header" && openMenu.folderKey === group.key}
  {#if isRenaming}
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <form
      class="sticky top-0 z-10 flex items-center gap-1.5 px-2.5 py-1.5 bg-slate-100 dark:bg-warm-800 border-b border-slate-200 dark:border-warm-700"
      onclick={(e) => e.stopPropagation()}
      onsubmit={(e) => {
        e.preventDefault();
        renameFolder(group, folderDraft);
      }}
    >
      <FolderOpen size={13} class="shrink-0 text-accent-600 dark:text-accent-400" />
      <!-- svelte-ignore a11y_autofocus -->
      <input
        type="text"
        bind:value={folderDraft}
        placeholder={group.name}
        disabled={renameInFlight}
        autofocus
        aria-label="New folder name"
        onkeydown={(e) => {
          if (e.key === "Escape") closeMenus();
        }}
        class="flex-1 min-w-0 px-1.5 py-0.5 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500"
      />
      <button
        type="submit"
        disabled={renameInFlight || !folderDraft.trim()}
        class="p-1 rounded text-accent-600 dark:text-accent-400 hover:bg-white dark:hover:bg-warm-700 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
        title="Save"
        aria-label="Save folder name"
      >
        <Check size={12} />
      </button>
      <button
        type="button"
        onclick={closeMenus}
        disabled={renameInFlight}
        class="p-1 rounded text-slate-500 hover:bg-white dark:hover:bg-warm-700 cursor-pointer"
        title="Cancel"
        aria-label="Cancel rename"
      >
        <X size={12} />
      </button>
    </form>
  {:else}
    <div
      role="group"
      aria-label="Folder {group.name}"
      class="sticky top-0 z-10 flex items-center group/folder border-y border-transparent
        {dropGroup === group.key
        ? 'bg-accent-50 dark:bg-accent-900/40 border-dashed !border-accent-400 dark:!border-accent-500'
        : 'bg-slate-50 dark:bg-warm-800 !border-b-slate-200 dark:!border-b-warm-700 hover:bg-slate-100 dark:hover:bg-warm-700'}"
      ondragover={(e) => {
        if (view !== "active" || !acceptsItem(e)) return;
        e.preventDefault();
        if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
        dropGroup = group.key;
      }}
      ondragleave={(e) => {
        const next = e.relatedTarget as Node | null;
        if (next && (e.currentTarget as HTMLElement).contains(next)) return;
        if (dropGroup === group.key) dropGroup = null;
      }}
      ondrop={(e) => onGroupDrop(e, group)}
    >
      <button
        class="flex-1 min-w-0 flex items-center gap-1.5 px-2.5 py-1.5 text-xs cursor-pointer"
        onclick={() => toggleCollapsed(group.key)}
        aria-expanded={!isCollapsed}
      >
        {#if isCollapsed}
          <ChevronRight size={12} class="shrink-0 text-slate-400" />
        {:else}
          <ChevronDown size={12} class="shrink-0 text-slate-400" />
        {/if}
        {#if group.pseudo}
          <FolderMinus size={13} class="shrink-0 text-slate-400" />
        {:else}
          <Folder size={13} class="shrink-0 text-accent-600 dark:text-accent-400" />
        {/if}
        <span
          class="flex-1 min-w-0 text-left truncate font-medium {group.pseudo
            ? 'text-slate-500 dark:text-slate-400'
            : 'text-slate-700 dark:text-slate-200'}"
        >
          {group.name}
        </span>
        <span class="text-[10px] tabular-nums text-slate-400">{group.items.length}</span>
      </button>
      {#if !group.pseudo}
        <div class="relative shrink-0">
          <button
            type="button"
            onclick={(e) => toggleHeaderMenu(e, group.key)}
            class="p-1 mr-1 rounded text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 hover:bg-white dark:hover:bg-warm-800 cursor-pointer opacity-0 group-hover/folder:opacity-100 focus:opacity-100 {menuOpen
              ? 'opacity-100'
              : ''}"
            aria-label="Folder actions"
            aria-haspopup="menu"
            aria-expanded={menuOpen}
          >
            <MoreVertical size={12} />
          </button>
          {#if menuOpen}
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
                class={menuItem}
                onclick={() => {
                  openMenu = { kind: "rename", folderKey: group.key };
                  folderDraft = group.name;
                }}
              >
                <Pencil size={11} class="shrink-0 text-slate-400" /> Rename folder
              </button>
            </div>
          {/if}
        </div>
      {/if}
    </div>
  {/if}
{/snippet}

{#snippet row(item: VaultItem)}
  {@const i = info.get(item.id)}
  {@const Icon = ICONS[item.type] ?? FileText}
  {@const accent = vaultItemAccent(item.type)}
  {@const isSel = selectedId === item.id}
  {@const menuOpen = openMenu?.kind === "row" && openMenu.itemId === item.id}
  {@const meta = metaFor(item, i)}
  {@const subtitle = i?.subtitle || i?.tags.join(", ") || typeLabel(item.type)}
  <div
    role="listitem"
    draggable={view === "active"}
    ondragstart={(e) => onRowDragStart(e, item)}
    ondragend={() => (dropGroup = null)}
    class="relative flex items-center border-l-2 group/row
      {isSel
      ? 'bg-accent-50 border-accent-500 dark:bg-accent-900/40'
      : 'border-transparent hover:bg-slate-50 dark:hover:bg-warm-800'}"
  >
    <button
      data-item-id={item.id}
      class="flex-1 min-w-0 flex items-center gap-2.5 pl-2.5 pr-1 py-2 text-left cursor-pointer focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-500"
      onclick={() => onSelect(item.id)}
      aria-current={isSel ? "true" : undefined}
    >
      <div class="shrink-0 w-8 h-8 rounded-md flex items-center justify-center {accent.tile}">
        <Icon size={16} />
      </div>
      <div class="flex-1 min-w-0">
        <div class="flex items-center gap-1.5 min-w-0">
          <span
            class="truncate text-sm font-medium {i?.unreadable
              ? 'italic text-vermilion-600 dark:text-vermilion-400'
              : isSel
                ? 'text-accent-700 dark:text-accent-300'
                : 'text-slate-800 dark:text-slate-100'}"
          >
            {#if i?.unreadable}
              (unreadable)
            {:else}
              {#each highlight(i?.title || "(untitled)") as seg, n (n)}
                {#if seg.m}<mark class="bg-amber-200/70 dark:bg-amber-500/30 text-inherit rounded-sm">{seg.t}</mark
                  >{:else}{seg.t}{/if}
              {/each}
            {/if}
          </span>
          {#if item.favorite}
            <Star size={11} fill="currentColor" class="shrink-0 text-amber-500" aria-label="Favorite" />
          {/if}
          {#if i?.expiry === "expired"}
            <span
              class="shrink-0 px-1 rounded text-[10px] font-medium bg-vermilion-100 text-vermilion-700 dark:bg-vermilion-950/40 dark:text-vermilion-300"
              >Expired</span
            >
          {:else if i?.expiry === "soon"}
            <span
              class="shrink-0 px-1 rounded text-[10px] font-medium bg-amber-100 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300"
              >Expires soon</span
            >
          {/if}
        </div>
        <div class="truncate text-xs text-slate-500 dark:text-slate-400">{subtitle}</div>
      </div>
    </button>

    <div class="shrink-0 flex items-center gap-0.5 pr-1.5">
      {#if meta}
        <span
          class="px-1 text-[10px] tabular-nums text-slate-400 dark:text-slate-500 whitespace-nowrap max-w-[6rem] truncate
            {menuOpen ? 'hidden' : 'group-hover/row:hidden group-focus-within/row:hidden'}"
        >
          {meta}
        </span>
      {/if}
      <div class="{menuOpen ? 'flex' : 'hidden group-hover/row:flex group-focus-within/row:flex'} items-center gap-0.5">
        {#if view !== "trash"}
          {#if i?.user}
            <button
              class={quickBtn}
              onclick={() => quickCopy(item, "user", i.user, "Username")}
              title="Copy username"
              aria-label="Copy username"
            >
              {#if copied === `${item.id}:user`}
                <Check size={13} class="text-accent-600 dark:text-accent-400" />
              {:else}
                <User size={13} />
              {/if}
            </button>
          {/if}
          {#if i?.secret}
            {@const label = i.secret.label}
            <button
              class={quickBtn}
              onclick={() => quickCopy(item, "secret", i.secret!.value, label)}
              title="Copy {label.toLowerCase()}"
              aria-label="Copy {label.toLowerCase()}"
            >
              {#if copied === `${item.id}:secret`}
                <Check size={13} class="text-accent-600 dark:text-accent-400" />
              {:else}
                <Copy size={13} />
              {/if}
            </button>
          {/if}
          {#if i?.url}
            <button
              class={quickBtn}
              onclick={() => openUrl(item, i.url)}
              title="Open {i.url}"
              aria-label="Open website"
            >
              <ExternalLink size={13} />
            </button>
          {/if}
        {/if}
        <div class="relative">
          <button
            type="button"
            class="{quickBtn} {menuOpen ? 'bg-white dark:bg-warm-800 text-slate-700 dark:text-slate-100' : ''}"
            onclick={(e) => toggleRowMenu(e, item.id)}
            aria-label="Item actions"
            aria-haspopup="menu"
            aria-expanded={menuOpen}
          >
            <MoreVertical size={13} />
          </button>
          {#if menuOpen}
            {@render rowMenu(item, i)}
          {/if}
        </div>
      </div>
    </div>
  </div>
{/snippet}

{#snippet rowMenu(item: VaultItem, i: Info | undefined)}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div
    role="menu"
    tabindex="-1"
    onclick={(e) => e.stopPropagation()}
    class="absolute right-0 top-full z-30 mt-0.5 w-56 rounded-md border border-slate-200 dark:border-warm-600 bg-white dark:bg-warm-800 shadow-lg py-1 text-xs"
  >
    {#if view === "trash"}
      <button type="button" role="menuitem" class={menuItem} onclick={() => restoreItem(item)}>
        <RotateCcw size={12} class="shrink-0 text-slate-400" /> Restore
      </button>
      <div class="my-1 border-t border-slate-100 dark:border-warm-700"></div>
      <button
        type="button"
        role="menuitem"
        class="{menuItem} !text-vermilion-600 dark:!text-vermilion-400"
        onclick={() => purgeItem(item)}
      >
        <Trash2 size={12} class="shrink-0" /> Delete permanently
      </button>
    {:else}
      <button type="button" role="menuitem" class={menuItem} onclick={() => toggleFavorite(item)}>
        {#if item.favorite}
          <StarOff size={12} class="shrink-0 text-slate-400" /> Remove from favorites
        {:else}
          <Star size={12} class="shrink-0 text-slate-400" /> Add to favorites
        {/if}
      </button>
      <button type="button" role="menuitem" class={menuItem} onclick={() => toggleArchive(item)}>
        {#if item.archived}
          <ArchiveRestore size={12} class="shrink-0 text-slate-400" /> Unarchive
        {:else}
          <Archive size={12} class="shrink-0 text-slate-400" /> Archive
        {/if}
      </button>

      <div class="my-1 border-t border-slate-100 dark:border-warm-700"></div>
      <div class="px-3 py-1 text-[10px] uppercase tracking-wider text-slate-400 flex items-center gap-1.5">
        <FolderInput size={11} /> Move to folder
      </div>
      <div class="max-h-48 overflow-y-auto">
        {#if i?.folder}
          <button type="button" role="menuitem" class={menuItem} onclick={() => moveItemToFolder(item, "")}>
            <FolderMinus size={12} class="shrink-0 text-slate-400" />
            <span class="text-slate-500 dark:text-slate-400">No folder</span>
          </button>
        {/if}
        {#each moveTargets(item) as f (f)}
          <button type="button" role="menuitem" class={menuItem} onclick={() => moveItemToFolder(item, f)}>
            <Folder size={12} class="shrink-0 text-accent-600 dark:text-accent-400" />
            <span class="truncate flex-1">{f}</span>
          </button>
        {/each}
      </div>
      <form
        class="px-2 py-1 flex items-center gap-1"
        onsubmit={(e) => {
          e.preventDefault();
          if (folderDraft.trim()) moveItemToFolder(item, folderDraft);
        }}
      >
        <Plus size={11} class="shrink-0 text-slate-400" />
        <input
          type="text"
          bind:value={folderDraft}
          placeholder="New folder…"
          aria-label="New folder name"
          class="flex-1 min-w-0 px-1.5 py-0.5 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500"
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

      <div class="my-1 border-t border-slate-100 dark:border-warm-700"></div>
      <button
        type="button"
        role="menuitem"
        class="{menuItem} !text-vermilion-600 dark:!text-vermilion-400"
        onclick={() => trashItem(item)}
      >
        <Trash2 size={12} class="shrink-0" /> Move to trash
      </button>
    {/if}
  </div>
{/snippet}
