<script lang="ts">
  import { onMount } from "svelte";
  import {
    Folder,
    FolderPlus,
    FileText,
    FileImage,
    FileVideo,
    FileAudio,
    FileArchive,
    FileCode,
    FileSpreadsheet,
    File as FileIcon,
    Upload,
    FolderUp,
    LayoutGrid,
    List,
    Search,
    X,
    ChevronRight,
    Download,
    Pencil,
    Trash2,
    MoreVertical,
    HardDrive,
    Loader2,
    Check,
    AlertCircle,
    Settings,
    FilePlus,
    ChevronDown,
    FileEdit,
    ListTree,
    RefreshCw,
    SquarePlus,
    SquareMinus,
    ChevronsDownUp,
    ChevronsUpDown,
  } from "lucide-svelte";
  import { link } from "svelte-spa-router";
  import * as files from "@/lib/vault/files";
  import type { VaultFile } from "@/lib/vault/files";
  import {
    collectDropped,
    collectFromInput,
    fileKind,
    formatBytes,
    isFileDrag,
    editable,
    EDIT_MAX_BYTES,
    type FileKind,
    type PickedFile,
  } from "@/lib/vault/dropfiles";
  import { addToast } from "@/lib/store/toast.svelte";
  import { confirmDialog } from "@/lib/store/confirm.svelte";
  import { apiErrorMessage, apiErrorStatus } from "@/lib/api/client";
  import { appStore } from "@/lib/store/store.svelte";
  import FilePreview from "./FilePreview.svelte";
  import FileEditor from "./FileEditor.svelte";
  import { setDragChip } from "@/lib/vault/dragchip";

  const NODE_DRAG_MIME = "application/x-pika-vault-file";
  const VIEW_KEY = "pika.vault.files.view";

  // ─── State ──────────────────────────────────────────────────────
  let loaded = $state(false);
  let enabled = $state(false);
  let backend = $state("");
  let nodes = $state<VaultFile[]>([]);
  let cwd = $state(""); // "" = root
  let selectedId = $state<string | null>(null);
  let q = $state("");
  let view = $state<"grid" | "list" | "tree">(
    (localStorage.getItem(VIEW_KEY) as "grid" | "list" | "tree") || "tree",
  );
  $effect(() => {
    try {
      localStorage.setItem(VIEW_KEY, view);
    } catch {
      /* ignore */
    }
  });

  let refreshing = $state(false);
  async function refresh() {
    refreshing = true;
    try {
      const info = await files.listFiles();
      enabled = info.enabled;
      backend = info.backend ?? "";
      nodes = info.files;
      if (cwd && !nodes.some((n) => n.id === cwd && n.is_dir)) cwd = "";
      if (selectedId && !nodes.some((n) => n.id === selectedId)) selectedId = null;
    } catch (err) {
      addToast(apiErrorMessage(err, "Failed to load files"), "alert");
    } finally {
      loaded = true;
      refreshing = false;
    }
  }
  onMount(refresh);

  // ─── Derived tree views ─────────────────────────────────────────
  const byId = $derived(new Map(nodes.map((n) => [n.id, n])));

  function sortNodes(list: VaultFile[]): VaultFile[] {
    return list.sort((a, b) =>
      a.is_dir !== b.is_dir
        ? a.is_dir
          ? -1
          : 1
        : a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: "base" }),
    );
  }

  const breadcrumbs = $derived.by(() => {
    const out: VaultFile[] = [];
    let cur = cwd ? byId.get(cwd) : undefined;
    while (cur) {
      out.unshift(cur);
      cur = cur.parent_id ? byId.get(cur.parent_id) : undefined;
    }
    return out;
  });

  function pathOf(n: VaultFile): string {
    const parts: string[] = [];
    let cur = n.parent_id ? byId.get(n.parent_id) : undefined;
    while (cur) {
      parts.unshift(cur.name);
      cur = cur.parent_id ? byId.get(cur.parent_id) : undefined;
    }
    return parts.join(" / ");
  }

  const searching = $derived(q.trim().length > 0);
  const visible = $derived.by(() => {
    const needle = q.trim().toLowerCase();
    if (needle) {
      return sortNodes(nodes.filter((n) => n.name.toLowerCase().includes(needle)));
    }
    return sortNodes(nodes.filter((n) => n.parent_id === cwd));
  });

  // ─── Tree view ──────────────────────────────────────────────────
  let expanded = $state<Set<string>>(new Set());

  function toggleExpand(id: string) {
    const next = new Set(expanded);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    expanded = next;
  }

  function expandAll() {
    expanded = new Set(nodes.filter((n) => n.is_dir).map((n) => n.id));
  }

  function collapseAll() {
    expanded = new Set();
  }

  const childrenOf = $derived.by(() => {
    const m = new Map<string, VaultFile[]>();
    for (const n of nodes) {
      const list = m.get(n.parent_id);
      if (list) list.push(n);
      else m.set(n.parent_id, [n]);
    }
    for (const list of m.values()) sortNodes(list);
    return m;
  });

  const treeMode = $derived(view === "tree" && !searching);

  // Rows for the table: flat in list/search mode, depth-first over
  // expanded folders in tree mode.
  const rows = $derived.by(() => {
    if (!treeMode) return visible.map((n) => ({ n, depth: 0 }));
    const out: { n: VaultFile; depth: number }[] = [];
    const walk = (parent: string, depth: number) => {
      for (const n of childrenOf.get(parent) ?? []) {
        out.push({ n, depth });
        if (n.is_dir && expanded.has(n.id)) walk(n.id, depth + 1);
      }
    };
    walk(cwd, 0);
    return out;
  });

  function folderStats(id: string): { count: number; size: number } {
    let count = 0;
    let size = 0;
    const stack = [id];
    while (stack.length) {
      const p = stack.pop()!;
      for (const n of nodes) {
        if (n.parent_id !== p) continue;
        if (n.is_dir) stack.push(n.id);
        else {
          count++;
          size += n.size;
        }
      }
    }
    return { count, size };
  }

  const totals = $derived.by(() => {
    let count = 0;
    let size = 0;
    for (const n of nodes) {
      if (!n.is_dir) {
        count++;
        size += n.size;
      }
    }
    return { count, size };
  });

  const selected = $derived(selectedId ? byId.get(selectedId) : undefined);

  // File currently open in the full-width editor.
  let editingId = $state<string | null>(null);
  let editStartEditing = $state(false);
  let editForced = $state(false);
  const editing = $derived(editingId ? byId.get(editingId) : undefined);

  function canEdit(n: VaultFile): boolean {
    return !n.is_dir && editable(kindOf(n), n.size);
  }

  function openEditor(n: VaultFile, startEditing = false) {
    menuFor = null;
    selectedId = n.id;
    editStartEditing = startEditing;
    editForced = !canEdit(n);
    editingId = n.id;
  }

  function open(n: VaultFile) {
    if (n.is_dir) {
      cwd = n.id;
      q = "";
      selectedId = null;
    } else if (canEdit(n)) {
      openEditor(n);
    } else {
      selectedId = n.id;
    }
  }

  // ─── Icons ──────────────────────────────────────────────────────
  const kindIcon: Record<FileKind, typeof FileIcon> = {
    folder: Folder,
    image: FileImage,
    video: FileVideo,
    audio: FileAudio,
    pdf: FileText,
    text: FileText,
    archive: FileArchive,
    code: FileCode,
    sheet: FileSpreadsheet,
    other: FileIcon,
  };
  const kindTile: Record<FileKind, string> = {
    folder: "bg-accent-50 dark:bg-accent-900/40 text-accent-600 dark:text-accent-300",
    image: "bg-sky-100 dark:bg-sky-950/40 text-sky-700 dark:text-sky-300",
    video: "bg-purple-100 dark:bg-purple-950/40 text-purple-700 dark:text-purple-300",
    audio: "bg-indigo-100 dark:bg-indigo-950/40 text-indigo-700 dark:text-indigo-300",
    pdf: "bg-vermilion-100 dark:bg-vermilion-950/40 text-vermilion-700 dark:text-vermilion-300",
    text: "bg-amber-100 dark:bg-amber-950/40 text-amber-700 dark:text-amber-300",
    archive: "bg-cool-100 dark:bg-cool-950/40 text-cool-700 dark:text-cool-300",
    code: "bg-cyan-100 dark:bg-cyan-950/40 text-cyan-700 dark:text-cyan-300",
    sheet: "bg-emerald-100 dark:bg-emerald-950/40 text-emerald-700 dark:text-emerald-300",
    other: "bg-slate-100 dark:bg-warm-800 text-slate-500 dark:text-slate-400",
  };
  const kindOf = (n: VaultFile) => fileKind(n.name, n.content_type, n.is_dir);

  // ─── Uploads ────────────────────────────────────────────────────
  type Upload = {
    key: number;
    name: string;
    loaded: number;
    total: number;
    status: "queued" | "uploading" | "done" | "error";
    error?: string;
    controller: AbortController;
  };
  let uploads = $state<Upload[]>([]);
  let uploadSeq = 0;
  let pumping = false;
  const queue: { u: Upload; picked: PickedFile; parentId: string }[] = [];
  const CONCURRENCY = 3;

  const activeUploads = $derived(uploads.filter((u) => u.status === "queued" || u.status === "uploading"));
  const uploadProgress = $derived.by(() => {
    let loaded = 0;
    let total = 0;
    for (const u of uploads) {
      loaded += u.status === "done" ? u.total : u.loaded;
      total += u.total;
    }
    return total ? loaded / total : 0;
  });

  function patchUpload(key: number, p: Partial<Upload>) {
    uploads = uploads.map((u) => (u.key === key ? { ...u, ...p } : u));
  }

  async function startUploads(picked: PickedFile[], parentId: string) {
    if (!enabled) {
      addToast("File storage isn't configured yet.", "alert");
      return;
    }
    // Empty dropped folders arrive as markers without a file.
    const dirs = picked.filter((p) => !p.file);
    for (const d of dirs) {
      try {
        await files.createFolder(parentId, d.relDir);
      } catch (err) {
        addToast(apiErrorMessage(err, `Failed to create ${d.relDir}`), "alert");
      }
    }
    const real = picked.filter((p) => p.file);
    if (dirs.length && !real.length) {
      await refresh();
      return;
    }
    for (const p of real) {
      const u: Upload = {
        key: ++uploadSeq,
        name: p.relDir ? `${p.relDir}/${p.file.name}` : p.file.name,
        loaded: 0,
        total: p.file.size,
        status: "queued",
        controller: new AbortController(),
      };
      uploads = [...uploads, u];
      queue.push({ u, picked: p, parentId });
    }
    pump();
  }

  async function pump() {
    if (pumping) return;
    pumping = true;
    try {
      const workers = Array.from({ length: CONCURRENCY }, async () => {
        while (queue.length) {
          const job = queue.shift()!;
          if (job.u.controller.signal.aborted) continue;
          patchUpload(job.u.key, { status: "uploading" });
          try {
            const created = await files.uploadFile(job.picked.file, {
              parentId: job.parentId,
              relDir: job.picked.relDir,
              signal: job.u.controller.signal,
              onProgress: (l) => patchUpload(job.u.key, { loaded: l }),
            });
            patchUpload(job.u.key, { status: "done", loaded: job.u.total });
            nodes = [...nodes.filter((n) => n.id !== created.id), created];
          } catch (err) {
            const aborted = job.u.controller.signal.aborted;
            patchUpload(job.u.key, {
              status: "error",
              error: aborted ? "Cancelled" : apiErrorMessage(err, "Upload failed"),
            });
          }
        }
      });
      await Promise.all(workers);
    } finally {
      pumping = false;
      // Folder nodes created server-side for relDir uploads.
      await refresh();
      const failed = uploads.filter((u) => u.status === "error").length;
      const done = uploads.filter((u) => u.status === "done").length;
      if (done) addToast(`Uploaded ${done} file${done === 1 ? "" : "s"}`, "success", 2500);
      if (failed) addToast(`${failed} upload${failed === 1 ? "" : "s"} failed`, "alert");
      if (!failed) setTimeout(() => {
        if (!activeUploads.length) uploads = [];
      }, 2500);
    }
  }

  function cancelAll() {
    for (const u of uploads) u.controller.abort();
    queue.length = 0;
  }

  let fileInput: HTMLInputElement | undefined = $state();
  let folderInput: HTMLInputElement | undefined = $state();

  function fromInput(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const picked = collectFromInput(input.files);
    input.value = "";
    if (picked.length) startUploads(picked, cwd);
  }

  // ─── Drag & drop ────────────────────────────────────────────────
  // Two kinds of drags: OS files (upload) and in-app nodes (move).
  // dragDepth tracks enter/leave across child elements so the overlay
  // doesn't flicker.
  let dragDepth = $state(0);
  let externalDrag = $state(false);
  let dropFolder = $state<string | null>(null); // folder id or "" for crumb root
  let draggingId = $state<string | null>(null);

  function onZoneEnter(e: DragEvent) {
    if (!isFileDrag(e)) return;
    e.preventDefault();
    dragDepth++;
    externalDrag = true;
  }
  function onZoneLeave(e: DragEvent) {
    if (!isFileDrag(e)) return;
    dragDepth = Math.max(0, dragDepth - 1);
    if (dragDepth === 0) externalDrag = false;
  }
  function onZoneOver(e: DragEvent) {
    if (isFileDrag(e)) {
      e.preventDefault();
      if (e.dataTransfer) e.dataTransfer.dropEffect = "copy";
    }
  }
  function onZoneDrop(e: DragEvent, targetId = cwd) {
    if (!isFileDrag(e)) return;
    e.preventDefault();
    e.stopPropagation();
    dragDepth = 0;
    externalDrag = false;
    dropFolder = null;
    // collectDropped reads DataTransfer synchronously before awaiting.
    collectDropped(e.dataTransfer).then((picked) => {
      if (picked.length) startUploads(picked, targetId);
    });
  }

  function isNodeDrag(e: DragEvent): boolean {
    return Array.from(e.dataTransfer?.types ?? []).includes(NODE_DRAG_MIME);
  }

  function canDropInto(targetId: string): boolean {
    if (!draggingId) return true;
    if (draggingId === targetId) return false;
    // Can't move a folder into its own subtree.
    let cur = targetId ? byId.get(targetId) : undefined;
    while (cur) {
      if (cur.id === draggingId) return false;
      cur = cur.parent_id ? byId.get(cur.parent_id) : undefined;
    }
    return true;
  }

  function folderDragOver(e: DragEvent, targetId: string) {
    if (isFileDrag(e)) {
      e.preventDefault();
      e.stopPropagation();
      if (e.dataTransfer) e.dataTransfer.dropEffect = "copy";
      dropFolder = targetId;
      return;
    }
    if (!isNodeDrag(e) || !canDropInto(targetId)) return;
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
    dropFolder = targetId;
  }

  function folderDragLeave(e: DragEvent, targetId: string) {
    const next = e.relatedTarget as Node | null;
    if (next && (e.currentTarget as HTMLElement).contains(next)) return;
    if (dropFolder === targetId) dropFolder = null;
  }

  async function folderDrop(e: DragEvent, targetId: string) {
    if (isFileDrag(e)) {
      onZoneDrop(e, targetId);
      return;
    }
    e.preventDefault();
    e.stopPropagation();
    dropFolder = null;
    const id = e.dataTransfer?.getData(NODE_DRAG_MIME);
    draggingId = null;
    if (!id) return;
    await moveNode(id, targetId);
  }

  async function moveNode(id: string, parentId: string) {
    const n = byId.get(id);
    if (!n || n.parent_id === parentId || id === parentId) return;
    try {
      const updated = await files.updateFile(id, { parent_id: parentId });
      nodes = nodes.map((x) => (x.id === id ? updated : x));
      const dest = parentId ? byId.get(parentId)?.name : "Files";
      addToast(`Moved ${n.name} to ${dest}`, "success", 2000);
    } catch (err) {
      addToast(apiErrorMessage(err, "Failed to move"), "alert");
    }
  }

  function nodeDragStart(e: DragEvent, n: VaultFile) {
    if (!e.dataTransfer) return;
    draggingId = n.id;
    e.dataTransfer.setData(NODE_DRAG_MIME, n.id);
    e.dataTransfer.effectAllowed = "move";
    setDragChip(e.dataTransfer, n.name, n.is_dir ? "bg-accent-500" : "bg-slate-400");
    // Dragging a file out to the desktop downloads it (Chromium).
    if (!n.is_dir) {
      const url = new URL(files.contentUrl(n.id, true), window.location.href).href;
      e.dataTransfer.setData(
        "DownloadURL",
        `${n.content_type || "application/octet-stream"}:${n.name}:${url}`,
      );
    }
  }

  // ─── Actions ────────────────────────────────────────────────────
  let menuFor = $state<string | null>(null);
  let renaming = $state<string | null>(null);
  let renameDraft = $state("");
  // Inline "create" bar shared by New folder and New file.
  let createKind = $state<"folder" | "file" | null>(null);
  let createName = $state("");
  let newMenuOpen = $state(false);

  function startCreate(kind: "folder" | "file", name = "") {
    newMenuOpen = false;
    createKind = kind;
    createName = name;
  }

  async function submitCreate() {
    const name = createName.trim();
    if (!name || !createKind) return;
    if (createKind === "folder") {
      await createFolder(name);
      return;
    }
    try {
      // "notes/todo.md" creates the folders first.
      const slash = name.lastIndexOf("/");
      let parent = cwd;
      let fileName = name;
      if (slash > 0) {
        const dir = await files.createFolder(cwd, name.slice(0, slash));
        parent = dir.id;
        fileName = name.slice(slash + 1);
      }
      if (!fileName) return;
      const f = await files.createTextFile(parent, fileName);
      createKind = null;
      createName = "";
      await refresh();
      openEditor(f, true);
    } catch (err) {
      addToast(apiErrorMessage(err, "Failed to create file"), "alert");
    }
  }

  async function createFolder(name: string) {
    try {
      const f = await files.createFolder(cwd, name);
      createKind = null;
      createName = "";
      await refresh();
      addToast(`Created ${f.name}`, "success", 2000);
    } catch (err) {
      addToast(apiErrorMessage(err, "Failed to create folder"), "alert");
    }
  }

  function startRename(n: VaultFile) {
    menuFor = null;
    renaming = n.id;
    renameDraft = n.name;
  }

  async function commitRename() {
    const id = renaming;
    const name = renameDraft.trim();
    renaming = null;
    if (!id) return;
    const n = byId.get(id);
    if (!n || !name || name === n.name) return;
    try {
      const updated = await files.updateFile(id, { name });
      nodes = nodes.map((x) => (x.id === id ? updated : x));
    } catch (err) {
      addToast(apiErrorMessage(err, "Failed to rename"), "alert");
    }
  }

  async function remove(n: VaultFile) {
    menuFor = null;
    const stats = n.is_dir ? folderStats(n.id) : null;
    const ok = await confirmDialog({
      title: n.is_dir ? `Delete folder "${n.name}"?` : `Delete "${n.name}"?`,
      message: n.is_dir
        ? `This permanently deletes the folder and everything inside it (${stats!.count} file${stats!.count === 1 ? "" : "s"}, ${formatBytes(stats!.size)}).`
        : "This permanently deletes the file from storage.",
      confirmLabel: "Delete",
      danger: true,
    });
    if (!ok) return;
    try {
      await files.deleteFile(n.id);
      if (selectedId === n.id) selectedId = null;
      await refresh();
    } catch (err) {
      addToast(apiErrorMessage(err, "Failed to delete"), "alert");
    }
  }

  function download(n: VaultFile) {
    menuFor = null;
    const a = document.createElement("a");
    a.href = n.is_dir ? files.zipUrl(n.id) : files.contentUrl(n.id, true);
    a.download = n.is_dir ? `${n.name}.zip` : n.name;
    document.body.appendChild(a);
    a.click();
    a.remove();
  }

  function downloadCurrentZip() {
    const dir = cwd ? byId.get(cwd) : undefined;
    if (dir) {
      download(dir);
      return;
    }
    const a = document.createElement("a");
    a.href = files.zipUrl();
    a.download = "vault-files.zip";
    document.body.appendChild(a);
    a.click();
    a.remove();
  }

  function onKeydown(e: KeyboardEvent) {
    const target = e.target as HTMLElement;
    if (target.closest("input, textarea, select, [contenteditable]")) return;
    if (editingId) return;
    if (!selected) return;
    if (e.key === "Delete") remove(selected);
    else if (e.key === "F2") startRename(selected);
    else if (e.key === "Enter") open(selected);
  }

  function formatDate(iso: string): string {
    const d = new Date(iso);
    return d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
  }

  const canManageSettings = $derived(appStore.hasPermission("settings.manage"));

  const crumbBase =
    "px-1.5 py-0.5 rounded cursor-pointer border border-transparent hover:bg-slate-100 dark:hover:bg-warm-700";
  const crumbDrop =
    "bg-accent-50 dark:bg-accent-900/40 border-dashed !border-accent-400 dark:!border-accent-500";
</script>

<svelte:window
  onclick={() => {
    menuFor = null;
    newMenuOpen = false;
  }}
  onkeydown={onKeydown}
/>

<input bind:this={fileInput} type="file" multiple class="hidden" onchange={fromInput} />
<input
  bind:this={folderInput}
  type="file"
  multiple
  webkitdirectory
  class="hidden"
  onchange={fromInput}
/>

<div class="flex-1 flex overflow-hidden">
  {#if editing}
    {#key editing.id}
      <FileEditor
        file={editing}
        location={pathOf(editing) || "All files"}
        startEditing={editStartEditing}
        forced={editForced}
        onClose={() => (editingId = null)}
        onSaved={(f) => (nodes = nodes.map((x) => (x.id === f.id ? f : x)))}
      />
    {/key}
  {:else}
  <!-- Main browser surface -->
  <section
    class="relative flex-1 min-w-0 flex flex-col bg-white dark:bg-warm-950"
    aria-label="Files"
    ondragenter={onZoneEnter}
    ondragleave={onZoneLeave}
    ondragover={onZoneOver}
    ondrop={(e) => onZoneDrop(e)}
  >
    <!-- Toolbar -->
    <div class="px-4 pt-3 pb-2.5 border-b border-slate-200 dark:border-warm-700 space-y-2.5">
      <div class="flex items-center gap-2">
        <h2 class="text-base font-semibold text-slate-800 dark:text-slate-100">Files</h2>
        {#if enabled}
          <span
            class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] uppercase tracking-wider bg-slate-100 dark:bg-warm-800 text-slate-500 dark:text-slate-400"
            title="Storage backend"
          >
            <HardDrive size={10} />
            {backend === "s3" ? "S3" : "Local disk"}
          </span>
        {/if}
        <span class="text-[11px] text-slate-400 dark:text-slate-500 tabular-nums">
          {totals.count} file{totals.count === 1 ? "" : "s"} · {formatBytes(totals.size)}
        </span>
        <div class="flex-1"></div>
        <button
          class="p-1.5 rounded text-slate-500 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-warm-700 hover:text-slate-800 dark:hover:text-slate-100 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
          onclick={refresh}
          disabled={refreshing}
          title="Refresh"
          aria-label="Refresh files"
        >
          <RefreshCw size={14} class={refreshing ? "animate-spin" : ""} />
        </button>
        <div class="relative w-56">
          <Search size={13} class="absolute top-1/2 left-2.5 -translate-y-1/2 text-slate-400 pointer-events-none" />
          <input
            type="text"
            bind:value={q}
            placeholder="Search all files…"
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
        <div class="flex rounded border border-slate-300 dark:border-warm-600 overflow-hidden">
          <button
            class="p-1.5 cursor-pointer {view === 'grid' ? 'bg-accent-50 text-accent-700 dark:bg-accent-900/40 dark:text-accent-300' : 'text-slate-500 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-warm-700'}"
            onclick={() => (view = "grid")}
            title="Grid view"
            aria-label="Grid view"
            aria-pressed={view === "grid"}
          >
            <LayoutGrid size={14} />
          </button>
          <button
            class="p-1.5 cursor-pointer border-l border-slate-300 dark:border-warm-600 {view === 'list' ? 'bg-accent-50 text-accent-700 dark:bg-accent-900/40 dark:text-accent-300' : 'text-slate-500 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-warm-700'}"
            onclick={() => (view = "list")}
            title="List view"
            aria-label="List view"
            aria-pressed={view === "list"}
          >
            <List size={14} />
          </button>
          <button
            class="p-1.5 cursor-pointer border-l border-slate-300 dark:border-warm-600 {view === 'tree' ? 'bg-accent-50 text-accent-700 dark:bg-accent-900/40 dark:text-accent-300' : 'text-slate-500 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-warm-700'}"
            onclick={() => (view = "tree")}
            title="Tree view"
            aria-label="Tree view"
            aria-pressed={view === "tree"}
          >
            <ListTree size={14} />
          </button>
        </div>
      </div>

      <div class="flex items-center gap-2">
        <!-- Breadcrumbs: each crumb is also a drop target. -->
        <nav class="flex-1 min-w-0 flex items-center gap-0.5 text-sm overflow-x-auto" aria-label="Breadcrumb">
          {#if searching}
            <span class="px-1.5 text-slate-500 dark:text-slate-400">
              Search results for “{q.trim()}”
            </span>
          {:else}
            <button
              class="{crumbBase} {dropFolder === '' ? crumbDrop : ''} {cwd === '' ? 'font-medium text-slate-800 dark:text-slate-100' : 'text-slate-500 dark:text-slate-400'}"
              onclick={() => {
                cwd = "";
                selectedId = null;
              }}
              ondragover={(e) => folderDragOver(e, "")}
              ondragleave={(e) => folderDragLeave(e, "")}
              ondrop={(e) => folderDrop(e, "")}
            >
              All files
            </button>
            {#each breadcrumbs as crumb (crumb.id)}
              <ChevronRight size={13} class="shrink-0 text-slate-300 dark:text-warm-600" />
              <button
                class="{crumbBase} truncate max-w-[12rem] {dropFolder === crumb.id ? crumbDrop : ''} {cwd === crumb.id ? 'font-medium text-slate-800 dark:text-slate-100' : 'text-slate-500 dark:text-slate-400'}"
                onclick={() => {
                  cwd = crumb.id;
                  selectedId = null;
                }}
                ondragover={(e) => folderDragOver(e, crumb.id)}
                ondragleave={(e) => folderDragLeave(e, crumb.id)}
                ondrop={(e) => folderDrop(e, crumb.id)}
              >
                {crumb.name}
              </button>
            {/each}
          {/if}
        </nav>
        {#if treeMode}
          <button
            class="p-1.5 rounded text-slate-500 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-warm-700 hover:text-slate-800 dark:hover:text-slate-100 cursor-pointer"
            onclick={expandAll}
            title="Expand all"
            aria-label="Expand all folders"
          >
            <ChevronsUpDown size={14} />
          </button>
          <button
            class="p-1.5 rounded text-slate-500 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-warm-700 hover:text-slate-800 dark:hover:text-slate-100 cursor-pointer"
            onclick={collapseAll}
            title="Collapse all"
            aria-label="Collapse all folders"
          >
            <ChevronsDownUp size={14} />
          </button>
        {/if}
        <div class="relative">
          <button
            class="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 text-slate-700 dark:text-slate-200 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
            disabled={!enabled}
            aria-haspopup="menu"
            aria-expanded={newMenuOpen}
            onclick={(e) => {
              e.stopPropagation();
              newMenuOpen = !newMenuOpen;
            }}
          >
            <FilePlus size={13} /> New <ChevronDown size={11} class="opacity-60" />
          </button>
          {#if newMenuOpen}
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <div
              role="menu"
              tabindex="-1"
              onclick={(e) => e.stopPropagation()}
              class="absolute right-0 top-full z-30 mt-1 w-48 rounded-md border border-slate-200 dark:border-warm-600 bg-white dark:bg-warm-800 shadow-lg py-1 text-xs"
            >
              <button role="menuitem" class="w-full flex items-center gap-2 px-3 py-1.5 text-left text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer" onclick={() => startCreate("folder")}>
                <FolderPlus size={12} class="text-accent-600 dark:text-accent-400" /> Folder
              </button>
              <div class="my-1 border-t border-slate-100 dark:border-warm-700"></div>
              <button role="menuitem" class="w-full flex items-center gap-2 px-3 py-1.5 text-left text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer" onclick={() => startCreate("file", "untitled.md")}>
                <FileText size={12} class="text-amber-600 dark:text-amber-400" /> Markdown file
              </button>
              <button role="menuitem" class="w-full flex items-center gap-2 px-3 py-1.5 text-left text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer" onclick={() => startCreate("file", "untitled.txt")}>
                <FileText size={12} class="text-slate-400" /> Text file
              </button>
              <button role="menuitem" class="w-full flex items-center gap-2 px-3 py-1.5 text-left text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer" onclick={() => startCreate("file", "")}>
                <FileCode size={12} class="text-cyan-600 dark:text-cyan-400" /> Other file…
              </button>
            </div>
          {/if}
        </div>
        <button
          class="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 text-slate-700 dark:text-slate-200 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
          disabled={!enabled || totals.count === 0}
          onclick={downloadCurrentZip}
          title={cwd ? "Download this folder as a zip" : "Download all files as a zip"}
        >
          <Download size={13} /> Zip
        </button>
        <button
          class="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 text-slate-700 dark:text-slate-200 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
          disabled={!enabled}
          onclick={() => folderInput?.click()}
        >
          <FolderUp size={13} /> Upload folder
        </button>
        <button
          class="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
          disabled={!enabled}
          onclick={() => fileInput?.click()}
        >
          <Upload size={13} /> Upload
        </button>
      </div>

      {#if createKind}
        <form
          class="flex items-center gap-2"
          onsubmit={(e) => {
            e.preventDefault();
            submitCreate();
          }}
        >
          {#if createKind === "folder"}
            <FolderPlus size={14} class="text-accent-600 dark:text-accent-400 shrink-0" />
          {:else}
            <FilePlus size={14} class="text-accent-600 dark:text-accent-400 shrink-0" />
          {/if}
          <!-- svelte-ignore a11y_autofocus -->
          <input
            type="text"
            bind:value={createName}
            autofocus
            onfocus={(e) => {
              // Select the base name so typing replaces "untitled".
              const el = e.currentTarget as HTMLInputElement;
              const dot = el.value.lastIndexOf(".");
              el.setSelectionRange(0, dot > 0 ? dot : el.value.length);
            }}
            placeholder={createKind === "folder"
              ? "Folder name (use / for nested folders)"
              : "File name, e.g. notes.md or config/app.yaml"}
            onkeydown={(e) => {
              if (e.key === "Escape") createKind = null;
            }}
            class="flex-1 px-2.5 py-1.5 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
          />
          <button
            type="button"
            class="px-3 py-1.5 text-xs rounded hover:bg-slate-100 dark:hover:bg-warm-700 text-slate-700 dark:text-slate-200 cursor-pointer"
            onclick={() => (createKind = null)}
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={!createName.trim()}
            class="px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
          >
            {createKind === "folder" ? "Create folder" : "Create & edit"}
          </button>
        </form>
      {/if}
    </div>

    <!-- Body -->
    <div class="flex-1 overflow-y-auto">
      {#if !loaded}
        <div class="h-full flex items-center justify-center">
          <Loader2 size={20} class="animate-spin text-slate-400" />
        </div>
      {:else if !enabled && nodes.length === 0}
        <div class="h-full flex flex-col items-center justify-center text-center px-6 text-slate-400 dark:text-slate-500">
          <HardDrive size={30} class="mb-3 opacity-40" />
          <div class="text-sm font-medium text-slate-600 dark:text-slate-300 mb-1">
            File storage isn't set up
          </div>
          <div class="text-xs max-w-sm mb-4">
            An administrator needs to choose where vault files are stored — a folder on the
            server or an S3-compatible bucket.
          </div>
          {#if canManageSettings}
            <a
              href="/settings/vault_files"
              use:link
              class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 cursor-pointer"
            >
              <Settings size={12} /> Configure storage
            </a>
          {/if}
        </div>
      {:else if visible.length === 0}
        <div class="h-full min-h-[18rem] flex flex-col items-center justify-center text-center px-6 text-slate-400 dark:text-slate-500">
          {#if searching}
            <Search size={28} class="mb-3 opacity-40" />
            <div class="text-sm font-medium text-slate-600 dark:text-slate-300 mb-1">No files match</div>
            <div class="text-xs">Search covers file and folder names in every folder.</div>
          {:else}
            <div class="w-16 h-16 rounded-full bg-slate-100 dark:bg-warm-900 flex items-center justify-center mb-4">
              <Upload size={26} class="opacity-60" />
            </div>
            <div class="text-sm font-medium text-slate-600 dark:text-slate-300 mb-1">
              {cwd ? "This folder is empty" : "No files yet"}
            </div>
            <div class="text-xs max-w-sm mb-4">
              Drag files or whole folders here, or use the upload buttons. Any file type works —
              documents, keys, archives, binaries.
            </div>
            <div class="flex gap-2">
              <button
                class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 cursor-pointer disabled:opacity-40"
                disabled={!enabled}
                onclick={() => fileInput?.click()}
              >
                <Upload size={12} /> Upload files
              </button>
              <button
                class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 text-slate-700 dark:text-slate-200 cursor-pointer disabled:opacity-40"
                disabled={!enabled}
                onclick={() => startCreate("file", "untitled.md")}
              >
                <FilePlus size={12} /> New note
              </button>
            </div>
          {/if}
        </div>
      {:else if view === "grid"}
        <div class="p-4 grid gap-3 grid-cols-[repeat(auto-fill,minmax(9.5rem,1fr))]" role="list">
          {#each visible as n (n.id)}
            {@const kind = kindOf(n)}
            {@const Icon = kindIcon[kind]}
            {@const isSel = selectedId === n.id}
            {@const isDrop = n.is_dir && dropFolder === n.id}
            <div
              role="listitem"
              draggable={renaming !== n.id}
              ondragstart={(e) => nodeDragStart(e, n)}
              ondragend={() => {
                draggingId = null;
                dropFolder = null;
              }}
              ondragover={n.is_dir ? (e) => folderDragOver(e, n.id) : undefined}
              ondragleave={n.is_dir ? (e) => folderDragLeave(e, n.id) : undefined}
              ondrop={n.is_dir ? (e) => folderDrop(e, n.id) : undefined}
              class="group relative rounded-lg border transition-colors
                {isDrop
                ? 'border-dashed border-accent-400 dark:border-accent-500 bg-accent-50 dark:bg-accent-900/40'
                : isSel
                  ? 'border-accent-300 dark:border-accent-700 bg-accent-50 dark:bg-accent-900/40'
                  : 'border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-900 hover:border-slate-300 dark:hover:border-warm-600'}
                {draggingId === n.id ? 'opacity-50' : ''}"
            >
              <button
                class="w-full p-3 pt-4 flex flex-col items-center text-center cursor-pointer"
                onclick={() => (selectedId = n.id)}
                ondblclick={() => open(n)}
                title={searching ? pathOf(n) || "All files" : n.name}
              >
                {#if kind === "image"}
                  <div class="w-full h-20 mb-2 rounded-md overflow-hidden bg-slate-100 dark:bg-warm-800 flex items-center justify-center">
                    <img src={files.contentUrl(n.id)} alt="" loading="lazy" class="max-w-full max-h-full object-contain" draggable="false" />
                  </div>
                {:else}
                  <div class="w-14 h-14 mb-3 mt-1 rounded-xl flex items-center justify-center {kindTile[kind]}">
                    <Icon size={26} />
                  </div>
                {/if}
                {#if renaming === n.id}
                  <!-- svelte-ignore a11y_autofocus -->
                  <input
                    type="text"
                    bind:value={renameDraft}
                    autofocus
                    onclick={(e) => e.stopPropagation()}
                    onkeydown={(e) => {
                      if (e.key === "Enter") commitRename();
                      if (e.key === "Escape") renaming = null;
                    }}
                    onblur={commitRename}
                    class="w-full px-1.5 py-0.5 text-xs text-center rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500"
                  />
                {:else}
                  <div class="w-full text-xs font-medium truncate text-slate-700 dark:text-slate-200">{n.name}</div>
                {/if}
                <div class="mt-0.5 text-[10px] text-slate-400 dark:text-slate-500 tabular-nums">
                  {#if n.is_dir}
                    {folderStats(n.id).count} items
                  {:else}
                    {formatBytes(n.size)}
                  {/if}
                </div>
              </button>
              <button
                class="absolute top-1.5 right-1.5 p-1 rounded text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer opacity-0 group-hover:opacity-100 focus:opacity-100 {menuFor === n.id ? 'opacity-100' : ''}"
                onclick={(e) => {
                  e.stopPropagation();
                  menuFor = menuFor === n.id ? null : n.id;
                }}
                aria-label="Actions for {n.name}"
                title="Actions"
              >
                <MoreVertical size={13} />
              </button>
              {#if menuFor === n.id}
                {@render actionMenu(n)}
              {/if}
            </div>
          {/each}
        </div>
      {:else}
        <table class="w-full text-sm">
          <thead class="sticky top-0 bg-white dark:bg-warm-950 z-10">
            <tr class="text-[10px] uppercase tracking-wider text-slate-400 dark:text-slate-500 border-b border-slate-200 dark:border-warm-700">
              <th class="text-left font-medium px-4 py-2">Name</th>
              {#if searching}<th class="text-left font-medium px-2 py-2">Location</th>{/if}
              <th class="text-right font-medium px-2 py-2 w-24">Size</th>
              <th class="text-left font-medium px-2 py-2 w-32">Modified</th>
              <th class="w-10"></th>
            </tr>
          </thead>
          <tbody>
            {#each rows as { n, depth } (n.id)}
              {@const kind = kindOf(n)}
              {@const Icon = kindIcon[kind]}
              {@const isSel = selectedId === n.id}
              {@const isDrop = n.is_dir && dropFolder === n.id}
              {@const isOpen = expanded.has(n.id)}
              <tr
                draggable={renaming !== n.id}
                ondragstart={(e) => nodeDragStart(e, n)}
                ondragend={() => {
                  draggingId = null;
                  dropFolder = null;
                }}
                ondragover={n.is_dir ? (e) => folderDragOver(e, n.id) : undefined}
                ondragleave={n.is_dir ? (e) => folderDragLeave(e, n.id) : undefined}
                ondrop={n.is_dir ? (e) => folderDrop(e, n.id) : undefined}
                onclick={() => (selectedId = n.id)}
                ondblclick={() => (treeMode && n.is_dir ? toggleExpand(n.id) : open(n))}
                class="group border-b border-slate-100 dark:border-warm-800 cursor-pointer
                  {isDrop
                  ? 'bg-accent-50 dark:bg-accent-900/40 outline-1 outline-dashed outline-accent-400'
                  : isSel
                    ? 'bg-accent-50 dark:bg-accent-900/40'
                    : 'hover:bg-slate-50 dark:hover:bg-warm-900'}
                  {draggingId === n.id ? 'opacity-50' : ''}"
              >
                <td class="px-4 py-1.5">
                  <div class="flex items-center gap-2.5 min-w-0" style={treeMode ? `padding-left: ${depth * 20}px` : ""}>
                    {#if treeMode}
                      {#if n.is_dir}
                        <button
                          class="shrink-0 -mr-1 p-0.5 rounded text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer"
                          onclick={(e) => {
                            e.stopPropagation();
                            toggleExpand(n.id);
                          }}
                          ondblclick={(e) => e.stopPropagation()}
                          aria-label="{isOpen ? 'Collapse' : 'Expand'} {n.name}"
                          aria-expanded={isOpen}
                          title={isOpen ? "Collapse" : "Expand"}
                        >
                          {#if isOpen}
                            <SquareMinus size={14} />
                          {:else}
                            <SquarePlus size={14} />
                          {/if}
                        </button>
                      {:else}
                        <span class="shrink-0 w-[18px] -mr-1"></span>
                      {/if}
                    {/if}
                    <div class="shrink-0 w-7 h-7 rounded-md flex items-center justify-center {kindTile[kind]}">
                      <Icon size={14} />
                    </div>
                    {#if renaming === n.id}
                      <!-- svelte-ignore a11y_autofocus -->
                      <input
                        type="text"
                        bind:value={renameDraft}
                        autofocus
                        onclick={(e) => e.stopPropagation()}
                        onkeydown={(e) => {
                          if (e.key === "Enter") commitRename();
                          if (e.key === "Escape") renaming = null;
                        }}
                        onblur={commitRename}
                        class="flex-1 px-1.5 py-0.5 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500"
                      />
                    {:else}
                      <span class="truncate text-slate-700 dark:text-slate-200 {isSel ? 'text-accent-700 dark:text-accent-300 font-medium' : ''}">{n.name}</span>
                    {/if}
                  </div>
                </td>
                {#if searching}
                  <td class="px-2 py-1.5 text-xs text-slate-500 dark:text-slate-400 truncate max-w-[12rem]">{pathOf(n) || "All files"}</td>
                {/if}
                <td class="px-2 py-1.5 text-right text-xs text-slate-500 dark:text-slate-400 tabular-nums">
                  {n.is_dir ? `${folderStats(n.id).count} items` : formatBytes(n.size)}
                </td>
                <td class="px-2 py-1.5 text-xs text-slate-500 dark:text-slate-400">{formatDate(n.updated_at)}</td>
                <td class="px-2 py-1.5 relative">
                  <button
                    class="p-1 rounded text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer opacity-0 group-hover:opacity-100 focus:opacity-100 {menuFor === n.id ? 'opacity-100' : ''}"
                    onclick={(e) => {
                      e.stopPropagation();
                      menuFor = menuFor === n.id ? null : n.id;
                    }}
                    aria-label="Actions for {n.name}"
                    title="Actions"
                  >
                    <MoreVertical size={13} />
                  </button>
                  {#if menuFor === n.id}
                    {@render actionMenu(n)}
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
    </div>

    <!-- External drag overlay -->
    {#if externalDrag && dropFolder === null}
      <div class="pointer-events-none absolute inset-2 rounded-xl border-2 border-dashed border-accent-400 dark:border-accent-500 bg-accent-50/80 dark:bg-accent-900/40 flex items-center justify-center">
        <div class="flex flex-col items-center text-accent-700 dark:text-accent-300">
          <Upload size={30} class="mb-2" />
          <div class="text-sm font-medium">
            Drop to upload to {breadcrumbs.at(-1)?.name ?? "All files"}
          </div>
          <div class="text-xs opacity-80">Folders keep their structure</div>
        </div>
      </div>
    {/if}

    <!-- Upload tray -->
    {#if uploads.length > 0}
      <div class="absolute bottom-3 right-3 w-80 rounded-lg border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-800 shadow-lg overflow-hidden">
        <div class="flex items-center gap-2 px-3 py-2 border-b border-slate-200 dark:border-warm-700">
          {#if activeUploads.length}
            <Loader2 size={13} class="animate-spin text-accent-600 dark:text-accent-400" />
            <span class="flex-1 text-xs font-medium">
              Uploading {activeUploads.length} file{activeUploads.length === 1 ? "" : "s"} · {Math.round(uploadProgress * 100)}%
            </span>
            <button class="text-[11px] text-slate-500 hover:text-vermilion-600 dark:hover:text-vermilion-400 cursor-pointer" onclick={cancelAll}>
              Cancel
            </button>
          {:else}
            <Check size={13} class="text-emerald-600 dark:text-emerald-400" />
            <span class="flex-1 text-xs font-medium">Uploads finished</span>
            <button
              class="p-0.5 rounded hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer"
              onclick={() => (uploads = [])}
              aria-label="Dismiss"
              title="Dismiss"
            >
              <X size={12} class="text-slate-400" />
            </button>
          {/if}
        </div>
        <div class="h-1 bg-slate-100 dark:bg-warm-900">
          <div class="h-full bg-accent-500 transition-all" style="width: {Math.round(uploadProgress * 100)}%"></div>
        </div>
        <ul class="max-h-48 overflow-y-auto text-xs divide-y divide-slate-100 dark:divide-warm-700">
          {#each uploads as u (u.key)}
            <li class="flex items-center gap-2 px-3 py-1.5">
              {#if u.status === "done"}
                <Check size={12} class="shrink-0 text-emerald-600 dark:text-emerald-400" />
              {:else if u.status === "error"}
                <AlertCircle size={12} class="shrink-0 text-vermilion-600 dark:text-vermilion-400" />
              {:else if u.status === "uploading"}
                <Loader2 size={12} class="shrink-0 animate-spin text-accent-600 dark:text-accent-400" />
              {:else}
                <span class="w-3 h-3 shrink-0 rounded-full border border-slate-300 dark:border-warm-600"></span>
              {/if}
              <span class="flex-1 min-w-0 truncate text-slate-700 dark:text-slate-200" title={u.error ?? u.name}>{u.name}</span>
              <span class="shrink-0 tabular-nums text-[10px] {u.status === 'error' ? 'text-vermilion-600 dark:text-vermilion-400' : 'text-slate-400'}">
                {#if u.status === "error"}
                  {u.error}
                {:else if u.status === "uploading"}
                  {u.total ? Math.round((u.loaded / u.total) * 100) : 0}%
                {:else}
                  {formatBytes(u.total)}
                {/if}
              </span>
            </li>
          {/each}
        </ul>
      </div>
    {/if}
  </section>

  <!-- Detail / preview pane -->
  {#if selected}
    {#key selected.id + selected.updated_at}
      <FilePreview
        file={selected}
        location={pathOf(selected) || "All files"}
        stats={selected.is_dir ? folderStats(selected.id) : null}
        editable={canEdit(selected)}
        tooLargeToEdit={!selected.is_dir && editable(kindOf(selected), 0) && selected.size > EDIT_MAX_BYTES}
        onClose={() => (selectedId = null)}
        onOpen={() => open(selected)}
        onEdit={() => openEditor(selected, true)}
        onOpenAsText={() => openEditor(selected, true)}
        onDownload={() => download(selected)}
        onRename={() => startRename(selected)}
        onDelete={() => remove(selected)}
      />
    {/key}
  {/if}
  {/if}
</div>

{#snippet actionMenu(n: VaultFile)}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div
    role="menu"
    tabindex="-1"
    onclick={(e) => e.stopPropagation()}
    class="absolute right-1.5 top-8 z-30 min-w-[10rem] rounded-md border border-slate-200 dark:border-warm-600 bg-white dark:bg-warm-800 shadow-lg py-1 text-xs text-left"
  >
    {#if n.is_dir}
      <button role="menuitem" class="w-full flex items-center gap-2 px-3 py-1.5 text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer" onclick={() => { menuFor = null; open(n); }}>
        <Folder size={12} class="text-slate-400" /> Open
      </button>
      <button role="menuitem" class="w-full flex items-center gap-2 px-3 py-1.5 text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer" onclick={() => download(n)}>
        <FileArchive size={12} class="text-slate-400" /> Download as zip
      </button>
    {:else}
      {#if canEdit(n)}
        <button role="menuitem" class="w-full flex items-center gap-2 px-3 py-1.5 text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer" onclick={() => openEditor(n, true)}>
          <FileEdit size={12} class="text-slate-400" /> Edit
        </button>
      {:else}
        <button role="menuitem" class="w-full flex items-center gap-2 px-3 py-1.5 text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer" onclick={() => openEditor(n, true)}>
          <FileCode size={12} class="text-slate-400" /> Open as text
        </button>
      {/if}
      <button role="menuitem" class="w-full flex items-center gap-2 px-3 py-1.5 text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer" onclick={() => download(n)}>
        <Download size={12} class="text-slate-400" /> Download
      </button>
    {/if}
    <button role="menuitem" class="w-full flex items-center gap-2 px-3 py-1.5 text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer" onclick={() => startRename(n)}>
      <Pencil size={12} class="text-slate-400" /> Rename
    </button>
    {#if n.parent_id}
      <button role="menuitem" class="w-full flex items-center gap-2 px-3 py-1.5 text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer" onclick={() => { menuFor = null; moveNode(n.id, byId.get(n.parent_id)?.parent_id ?? ""); }}>
        <FolderUp size={12} class="text-slate-400" /> Move up one level
      </button>
    {/if}
    <div class="my-1 border-t border-slate-100 dark:border-warm-700"></div>
    <button role="menuitem" class="w-full flex items-center gap-2 px-3 py-1.5 text-vermilion-600 dark:text-vermilion-400 hover:bg-slate-100 dark:hover:bg-warm-700 cursor-pointer" onclick={() => remove(n)}>
      <Trash2 size={12} /> Delete
    </button>
  </div>
{/snippet}
