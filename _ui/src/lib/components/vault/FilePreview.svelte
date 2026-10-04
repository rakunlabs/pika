<script lang="ts">
  import { X, Download, Pencil, Trash2, FolderOpen, Loader2, File as FileIcon, FileEdit, Code2, FileCode } from "lucide-svelte";
  import * as files from "@/lib/vault/files";
  import type { VaultFile } from "@/lib/vault/files";
  import { fileKind, formatBytes, isMarkdown, previewable } from "@/lib/vault/dropfiles";
  import { renderMarkdown } from "@/lib/vault/markdown";

  interface Props {
    file: VaultFile;
    location: string;
    stats: { count: number; size: number } | null;
    editable: boolean;
    tooLargeToEdit: boolean;
    onClose: () => void;
    onOpen: () => void;
    onEdit: () => void;
    onOpenAsText: () => void;
    onDownload: () => void;
    onRename: () => void;
    onDelete: () => void;
  }
  let { file, location, stats, editable, tooLargeToEdit, onClose, onOpen, onEdit, onOpenAsText, onDownload, onRename, onDelete }: Props = $props();

  const markdown = $derived(isMarkdown(file.name, file.content_type));
  let raw = $state(false);

  const kind = $derived(fileKind(file.name, file.content_type, file.is_dir));
  const url = $derived(files.contentUrl(file.id));

  // Text previews fetch only the first 256 KiB.
  const TEXT_LIMIT = 256 * 1024;
  let text = $state<string | null>(null);
  let textTruncated = $state(false);
  let textError = $state(false);

  $effect(() => {
    if (kind !== "text" && kind !== "code") return;
    const ctrl = new AbortController();
    text = null;
    textError = false;
    fetch(url, { headers: { Range: `bytes=0-${TEXT_LIMIT - 1}` }, signal: ctrl.signal, credentials: "same-origin" })
      .then(async (res) => {
        if (!res.ok && res.status !== 206) throw new Error(String(res.status));
        const buf = await res.arrayBuffer();
        const slice = buf.byteLength > TEXT_LIMIT ? buf.slice(0, TEXT_LIMIT) : buf;
        text = new TextDecoder("utf-8", { fatal: false }).decode(slice);
        textTruncated = file.size > TEXT_LIMIT;
      })
      .catch((err) => {
        if (err?.name !== "AbortError") textError = true;
      });
    return () => ctrl.abort();
  });

  function formatDate(iso: string): string {
    return new Date(iso).toLocaleString();
  }
</script>

<aside
  class="w-[22rem] shrink-0 flex flex-col border-l border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900"
  aria-label="File details"
>
  <div class="flex items-center gap-2 px-4 py-3 border-b border-slate-200 dark:border-warm-700">
    <h3 class="flex-1 min-w-0 text-sm font-semibold truncate" title={file.name}>{file.name}</h3>
    <button
      class="p-1.5 rounded hover:bg-slate-100 dark:hover:bg-warm-700 text-slate-500 dark:text-slate-400 cursor-pointer"
      onclick={onClose}
      aria-label="Close details"
      title="Close"
    >
      <X size={14} />
    </button>
  </div>

  <div class="flex-1 overflow-y-auto p-4 space-y-4">
    {#if !file.is_dir && previewable(kind)}
      <div class="rounded-lg border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-800 overflow-hidden">
        {#if kind === "image"}
          <a href={url} target="_blank" rel="noopener noreferrer" class="block">
            <img src={url} alt={file.name} class="w-full max-h-80 object-contain bg-slate-100 dark:bg-warm-900" />
          </a>
        {:else if kind === "video"}
          <!-- svelte-ignore a11y_media_has_caption -->
          <video src={url} controls preload="metadata" class="w-full max-h-80 bg-black"></video>
        {:else if kind === "audio"}
          <div class="p-3"><audio src={url} controls preload="metadata" class="w-full"></audio></div>
        {:else if kind === "pdf"}
          <iframe src={url} title={file.name} class="w-full h-96 bg-white"></iframe>
        {:else}
          {#if text === null && !textError}
            <div class="h-32 flex items-center justify-center">
              <Loader2 size={16} class="animate-spin text-slate-400" />
            </div>
          {:else if textError}
            <div class="p-3 text-xs text-slate-500 dark:text-slate-400">Preview unavailable.</div>
          {:else}
            {#if markdown}
              <div class="flex items-center justify-end px-2 py-1 border-b border-slate-200 dark:border-warm-700">
                <button
                  class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] uppercase tracking-wider cursor-pointer {raw ? 'bg-accent-50 text-accent-700 dark:bg-accent-900/40 dark:text-accent-300' : 'text-slate-500 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-warm-700'}"
                  onclick={() => (raw = !raw)}
                  aria-pressed={raw}
                  title="Toggle raw markdown"
                >
                  <Code2 size={11} /> Raw
                </button>
              </div>
            {/if}
            {#if markdown && !raw}
              <!-- renderMarkdown escapes all input; see lib/vault/markdown.ts -->
              <div class="prose-vault p-3 text-xs max-h-96 overflow-auto text-slate-700 dark:text-slate-100">
                {@html renderMarkdown(text ?? "")}
              </div>
            {:else}
              <pre class="p-3 text-[11px] leading-relaxed font-mono whitespace-pre-wrap break-all max-h-96 overflow-auto text-slate-700 dark:text-slate-200">{text}</pre>
            {/if}
            {#if textTruncated}
              <div class="px-3 py-1.5 text-[10px] text-slate-400 border-t border-slate-200 dark:border-warm-700">
                Showing the first {formatBytes(TEXT_LIMIT)} — download for the full file.
              </div>
            {/if}
          {/if}
        {/if}
      </div>
    {:else}
      <div class="h-32 rounded-lg border border-dashed border-slate-300 dark:border-warm-600 flex flex-col items-center justify-center text-slate-400 dark:text-slate-500">
        {#if file.is_dir}
          <FolderOpen size={28} class="mb-1.5 opacity-60" />
          <span class="text-xs">Folder</span>
        {:else}
          <FileIcon size={28} class="mb-1.5 opacity-60" />
          <span class="text-xs">No preview for this file type</span>
        {/if}
      </div>
    {/if}

    <dl class="text-xs grid grid-cols-[6rem_1fr] gap-y-2">
      <dt class="text-slate-500 dark:text-slate-400">Location</dt>
      <dd class="text-slate-700 dark:text-slate-200 truncate" title={location}>{location}</dd>
      {#if stats}
        <dt class="text-slate-500 dark:text-slate-400">Contains</dt>
        <dd class="text-slate-700 dark:text-slate-200">{stats.count} file{stats.count === 1 ? "" : "s"} · {formatBytes(stats.size)}</dd>
      {:else}
        <dt class="text-slate-500 dark:text-slate-400">Size</dt>
        <dd class="text-slate-700 dark:text-slate-200 tabular-nums">{formatBytes(file.size)} <span class="text-slate-400">({file.size.toLocaleString()} bytes)</span></dd>
        <dt class="text-slate-500 dark:text-slate-400">Type</dt>
        <dd class="text-slate-700 dark:text-slate-200 font-mono truncate">{file.content_type || "unknown"}</dd>
      {/if}
      <dt class="text-slate-500 dark:text-slate-400">Created</dt>
      <dd class="text-slate-700 dark:text-slate-200">{formatDate(file.created_at)}</dd>
      <dt class="text-slate-500 dark:text-slate-400">Modified</dt>
      <dd class="text-slate-700 dark:text-slate-200">{formatDate(file.updated_at)}</dd>
    </dl>

    {#if tooLargeToEdit}
      <p class="text-xs text-slate-500 dark:text-slate-400">
        This file is larger than 5 MB, so it opens read-only (first 5 MB). Download it to make changes.
      </p>
    {/if}
    {#if !file.is_dir && !editable}
      <button
        class="inline-flex items-center gap-1.5 text-xs text-accent-600 dark:text-accent-400 hover:underline cursor-pointer"
        onclick={onOpenAsText}
      >
        <FileCode size={12} /> Open as text
      </button>
    {/if}
  </div>

  <div class="flex items-center gap-2 px-4 py-3 border-t border-slate-200 dark:border-warm-700">
    {#if file.is_dir}
      <button class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 cursor-pointer" onclick={onOpen}>
        <FolderOpen size={12} /> Open
      </button>
    {:else if editable}
      <button class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 cursor-pointer" onclick={onEdit}>
        <FileEdit size={12} /> Edit
      </button>
      <button
        class="p-1.5 rounded hover:bg-slate-100 dark:hover:bg-warm-700 text-slate-500 dark:text-slate-400 cursor-pointer"
        onclick={onDownload}
        aria-label="Download"
        title="Download"
      >
        <Download size={14} />
      </button>
    {:else}
      <button class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 cursor-pointer" onclick={onDownload}>
        <Download size={12} /> Download
      </button>
    {/if}
    <button class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 text-slate-700 dark:text-slate-200 cursor-pointer" onclick={onRename}>
      <Pencil size={12} /> Rename
    </button>
    <div class="flex-1"></div>
    <button
      class="p-1.5 rounded hover:bg-slate-100 dark:hover:bg-warm-700 text-vermilion-600 dark:text-vermilion-400 cursor-pointer"
      onclick={onDelete}
      aria-label="Delete"
      title="Delete"
    >
      <Trash2 size={14} />
    </button>
  </div>
</aside>
