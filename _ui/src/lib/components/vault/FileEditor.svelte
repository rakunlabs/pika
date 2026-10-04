<script lang="ts">
  import { onMount } from "svelte";
  import {
    ArrowLeft,
    Save,
    Download,
    Loader2,
    Eye,
    Code2,
    Columns2,
    Pencil,
    AlertTriangle,
    WrapText,
    Check,
    Lock,
    Unlock,
  } from "lucide-svelte";
  import type { Extension } from "@codemirror/state";
  import type { LanguageSupport } from "@codemirror/language";
  import { keymap, EditorView } from "@codemirror/view";
  import AppCodeMirror from "@/lib/editor/AppCodeMirror.svelte";
  import * as files from "@/lib/vault/files";
  import type { VaultFile } from "@/lib/vault/files";
  import { decodeText, EDIT_MAX_BYTES, formatBytes, isMarkdown } from "@/lib/vault/dropfiles";
  import { languageLoader } from "@/lib/vault/languages";
  import { renderMarkdown } from "@/lib/vault/markdown";
  import { addToast } from "@/lib/store/toast.svelte";
  import { confirmDialog } from "@/lib/store/confirm.svelte";
  import { apiErrorMessage, apiErrorStatus } from "@/lib/api/client";
  import { prefsStore } from "@/lib/store/prefs.svelte";

  interface Props {
    file: VaultFile;
    /** Breadcrumb-style location shown under the name. */
    location: string;
    /** Open straight into edit mode (e.g. a freshly created file). */
    startEditing?: boolean;
    /** Opened via "Open as text" on a file not recognised as text. */
    forced?: boolean;
    onClose: () => void;
    onSaved: (f: VaultFile) => void;
  }
  let { file, location, startEditing = false, forced = false, onClose, onSaved }: Props = $props();

  // svelte-ignore state_referenced_locally
  let current = $state<VaultFile>(file);
  const markdown = $derived(isMarkdown(current.name, current.content_type));

  // Markdown files open in rendered preview; everything else opens in
  // the editor. "split" shows source and preview side by side.
  type Mode = "preview" | "edit" | "split";
  // svelte-ignore state_referenced_locally
  let mode = $state<Mode>(markdown && !startEditing ? "preview" : "edit");
  // Raw switch: show markdown source read-only instead of the rendered view.
  let raw = $state(false);
  let wrap = $state<boolean>(prefsStore.editor.line_wrap);

  let loading = $state(true);
  let loadError = $state<string | null>(null);
  let original = $state("");
  let text = $state("");
  let saving = $state(false);
  let savedFlash = $state(false);
  const dirty = $derived(text !== original);

  let lang = $state<LanguageSupport | null>(null);

  // Read-only guards for "open anything as text":
  //  - truncated: only the first EDIT_MAX_BYTES were loaded; saving
  //    would cut the file, so it can never be unlocked.
  //  - binary / lossy: decoding isn't byte-exact; saving would corrupt
  //    the file unless the user explicitly accepts that.
  let truncated = $state(false);
  let binary = $state(false);
  let lossy = $state(false);
  let unlocked = $state(false);
  const locked = $derived(truncated || ((binary || lossy) && !unlocked));

  async function unlockEditing() {
    const ok = await confirmDialog({
      title: binary ? "Edit a binary file as text?" : "Edit with lossy encoding?",
      message: binary
        ? "This file doesn't look like text. Saving it from the text editor will almost certainly corrupt it. Download a copy first if you need the original."
        : "This file isn't valid UTF-8. Invalid bytes are shown as \uFFFD and will be replaced in the file if you save.",
      confirmLabel: "Edit anyway",
      danger: true,
    });
    if (ok) {
      unlocked = true;
      if (mode === "preview" && !markdown) mode = "edit";
    }
  }

  onMount(() => {
    const ctrl = new AbortController();
    const tooBig = current.size > EDIT_MAX_BYTES;
    files
      .readBytes(current.id, { signal: ctrl.signal, maxBytes: tooBig ? EDIT_MAX_BYTES : undefined })
      .then((bytes) => {
        const d = decodeText(bytes);
        truncated = tooBig;
        binary = d.binary;
        lossy = d.lossy;
        original = d.text;
        text = d.text;
      })
      .catch((err) => {
        if (err?.name !== "CanceledError") loadError = apiErrorMessage(err, "Failed to load file");
      })
      .finally(() => (loading = false));
    languageLoader(current.name)?.()
      .then((l) => (lang = l))
      .catch(() => {});
    return () => ctrl.abort();
  });

  async function save() {
    if (!dirty || saving || locked) return;
    saving = true;
    try {
      const updated = await files.writeText(current.id, text, {
        ifUpdatedAt: current.updated_at,
        contentType: current.content_type || files.textContentType(current.name),
      });
      current = updated;
      original = text;
      onSaved(updated);
      savedFlash = true;
      setTimeout(() => (savedFlash = false), 1500);
    } catch (err) {
      if (apiErrorStatus(err) === 409) {
        const overwrite = await confirmDialog({
          title: "File changed on the server",
          message:
            "Someone (maybe you, in another tab) saved this file after you opened it. Overwrite their changes with yours?",
          confirmLabel: "Overwrite",
          danger: true,
        });
        if (overwrite) {
          try {
            const updated = await files.writeText(current.id, text, {
              contentType: current.content_type || files.textContentType(current.name),
            });
            current = updated;
            original = text;
            onSaved(updated);
          } catch (e2) {
            addToast(apiErrorMessage(e2, "Failed to save"), "alert");
          }
        }
      } else {
        addToast(apiErrorMessage(err, "Failed to save"), "alert");
      }
    } finally {
      saving = false;
    }
  }

  async function close() {
    if (dirty) {
      const ok = await confirmDialog({
        title: "Discard unsaved changes?",
        message: `Your edits to ${current.name} haven't been saved.`,
        confirmLabel: "Discard",
        danger: true,
      });
      if (!ok) return;
    }
    onClose();
  }

  function download() {
    const a = document.createElement("a");
    a.href = files.contentUrl(current.id, true);
    a.download = current.name;
    document.body.appendChild(a);
    a.click();
    a.remove();
  }

  // Ctrl/Cmd+S inside CodeMirror.
  const extensions: Extension[] = [
    keymap.of([
      {
        key: "Mod-s",
        preventDefault: true,
        run: () => {
          save();
          return true;
        },
      },
    ]),
  ];
  const readonlyExtensions: Extension[] = [EditorView.editable.of(false)];

  function onWindowKey(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
      e.preventDefault();
      save();
    } else if (e.key === "Escape" && mode === "preview" && !(e.target as HTMLElement).closest("input, textarea, .cm-editor")) {
      close();
    }
  }

  function onBeforeUnload(e: BeforeUnloadEvent) {
    if (dirty) e.preventDefault();
  }

  const html = $derived(markdown ? renderMarkdown(text) : "");
  const lineCount = $derived(text ? text.split("\n").length : 0);

  const segBase = "inline-flex items-center gap-1 px-2.5 py-1 text-xs cursor-pointer";
  const segActive = "bg-accent-50 text-accent-700 dark:bg-accent-900/40 dark:text-accent-300";
  const segIdle = "text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-warm-700";
</script>

<svelte:window onkeydown={onWindowKey} onbeforeunload={onBeforeUnload} />

<section class="flex-1 min-w-0 flex flex-col bg-white dark:bg-warm-950" aria-label="Edit {current.name}">
  <!-- Toolbar -->
  <div class="flex items-center gap-2 px-3 py-2 border-b border-slate-200 dark:border-warm-700">
    <button
      class="p-1.5 rounded hover:bg-slate-100 dark:hover:bg-warm-700 text-slate-500 dark:text-slate-400 cursor-pointer"
      onclick={close}
      aria-label="Back to files"
      title="Back to files"
    >
      <ArrowLeft size={16} />
    </button>
    <div class="min-w-0 flex-1">
      <div class="flex items-center gap-1.5">
        <span class="text-sm font-semibold truncate">{current.name}</span>
        {#if dirty}
          <span class="w-2 h-2 rounded-full bg-amber-500 shrink-0" title="Unsaved changes" aria-label="Unsaved changes"></span>
        {/if}
      </div>
      <div class="text-[11px] text-slate-500 dark:text-slate-400 truncate">
        {location} · {formatBytes(new Blob([text]).size)} · {lineCount} lines
      </div>
    </div>

    {#if markdown}
      <div class="flex rounded border border-slate-300 dark:border-warm-600 overflow-hidden" role="group" aria-label="View mode">
        <button class="{segBase} {mode === 'edit' ? segActive : segIdle}" onclick={() => (mode = "edit")} aria-pressed={mode === "edit"} title="Edit source">
          <Pencil size={12} /> Edit
        </button>
        <button class="{segBase} border-l border-slate-300 dark:border-warm-600 {mode === 'split' ? segActive : segIdle}" onclick={() => (mode = "split")} aria-pressed={mode === "split"} title="Source and preview side by side">
          <Columns2 size={12} /> Split
        </button>
        <button class="{segBase} border-l border-slate-300 dark:border-warm-600 {mode === 'preview' ? segActive : segIdle}" onclick={() => (mode = "preview")} aria-pressed={mode === "preview"} title="Rendered preview">
          <Eye size={12} /> Preview
        </button>
      </div>
      {#if mode === "preview"}
        <label class="flex items-center gap-1.5 text-xs text-slate-600 dark:text-slate-300 cursor-pointer select-none" title="Show the markdown source instead of the rendered view">
          <span
            class="relative inline-flex h-4 w-7 items-center rounded-full transition-colors {raw ? 'bg-accent-600' : 'bg-slate-300 dark:bg-warm-600'}"
          >
            <input type="checkbox" class="sr-only" bind:checked={raw} />
            <span class="inline-block h-3 w-3 rounded-full bg-white shadow transition-transform {raw ? 'translate-x-3.5' : 'translate-x-0.5'}"></span>
          </span>
          <Code2 size={12} /> Raw
        </label>
      {/if}
    {/if}

    <button
      class="p-1.5 rounded cursor-pointer {wrap ? segActive : 'text-slate-500 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-warm-700'}"
      onclick={() => (wrap = !wrap)}
      aria-pressed={wrap}
      aria-label="Toggle line wrapping"
      title="Line wrapping"
    >
      <WrapText size={14} />
    </button>
    <button
      class="p-1.5 rounded hover:bg-slate-100 dark:hover:bg-warm-700 text-slate-500 dark:text-slate-400 cursor-pointer"
      onclick={download}
      aria-label="Download"
      title="Download"
    >
      <Download size={14} />
    </button>
    <button
      class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
      onclick={save}
      disabled={!dirty || saving || loading || locked}
      title="Save (Ctrl+S)"
    >
      {#if saving}
        <Loader2 size={12} class="animate-spin" />
      {:else if savedFlash}
        <Check size={12} />
      {:else}
        <Save size={12} />
      {/if}
      {savedFlash ? "Saved" : "Save"}
    </button>
  </div>

  {#if !loading && !loadError && (truncated || binary || lossy || forced)}
    <div
      class="flex items-start gap-2 px-4 py-2 text-xs border-b
        {locked
        ? 'bg-amber-50 dark:bg-amber-950/40 border-amber-300 dark:border-amber-700 text-amber-900 dark:text-amber-200'
        : 'bg-blue-50 dark:bg-blue-950/30 border-blue-300 dark:border-blue-700 text-blue-900 dark:text-blue-200'}"
    >
      {#if locked}
        <Lock size={13} class="shrink-0 mt-0.5" />
      {:else}
        <AlertTriangle size={13} class="shrink-0 mt-0.5" />
      {/if}
      <div class="flex-1">
        {#if truncated}
          Showing the first {formatBytes(EDIT_MAX_BYTES)} of {formatBytes(current.size)}. Large files are
          read-only here — download the file to edit it.
        {:else if binary}
          This file looks binary, so it's opened read-only. Saving it as text would corrupt it.
        {:else if lossy}
          This file isn't valid UTF-8; invalid bytes are shown as �. It's read-only so the original bytes are
          preserved.
        {:else}
          Opened as plain text. This file type isn't normally edited in the browser — check the contents
          before saving.
        {/if}
        {#if !locked && (binary || lossy)}
          <strong>Editing is unlocked — saving will rewrite the file.</strong>
        {/if}
      </div>
      {#if !truncated && (binary || lossy) && !unlocked}
        <button
          class="shrink-0 inline-flex items-center gap-1 px-2 py-0.5 rounded border border-amber-400 dark:border-amber-600 hover:bg-amber-100 dark:hover:bg-amber-900/40 cursor-pointer"
          onclick={unlockEditing}
        >
          <Unlock size={11} /> Edit anyway
        </button>
      {/if}
    </div>
  {/if}

  <!-- Body -->
  <div class="flex-1 min-h-0 flex">
    {#if loading}
      <div class="flex-1 flex items-center justify-center">
        <Loader2 size={20} class="animate-spin text-slate-400" />
      </div>
    {:else if loadError}
      <div class="flex-1 flex flex-col items-center justify-center text-center px-6">
        <AlertTriangle size={28} class="mb-3 text-vermilion-600 dark:text-vermilion-400 opacity-70" />
        <div class="text-sm font-medium text-slate-700 dark:text-slate-200 mb-1">Couldn't open this file</div>
        <div class="text-xs text-slate-500 dark:text-slate-400 mb-4">{loadError}</div>
        <button class="px-3 py-1.5 text-xs rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 text-slate-700 dark:text-slate-200 cursor-pointer" onclick={onClose}>
          Back to files
        </button>
      </div>
    {:else}
      {#if mode === "edit" || mode === "split"}
        <div class="min-w-0 h-full {mode === 'split' ? 'w-1/2 border-r border-slate-200 dark:border-warm-700' : 'flex-1'}">
          <AppCodeMirror
            value={text}
            {lang}
            extensions={locked ? readonlyExtensions : extensions}
            readonly={locked}
            lineWrapping={wrap}
            onchange={(v) => (text = v)}
          />
        </div>
      {/if}
      {#if markdown && (mode === "preview" || mode === "split")}
        {#if mode === "preview" && raw}
          <div class="flex-1 min-w-0 h-full">
            <AppCodeMirror value={text} {lang} extensions={readonlyExtensions} readonly lineWrapping={wrap} />
          </div>
        {:else}
          <div class="min-w-0 h-full overflow-y-auto {mode === 'split' ? 'w-1/2' : 'flex-1'}">
            {#if text.trim()}
              <!-- renderMarkdown escapes all input; see lib/vault/markdown.ts -->
              <article class="prose-vault max-w-3xl px-8 py-6 text-sm text-slate-700 dark:text-slate-100">
                {@html html}
              </article>
            {:else}
              <div class="h-full flex flex-col items-center justify-center text-center text-slate-400 dark:text-slate-500 px-6">
                <Pencil size={26} class="mb-2 opacity-40" />
                <div class="text-sm font-medium text-slate-600 dark:text-slate-300 mb-1">Nothing here yet</div>
                <button class="text-xs text-accent-600 dark:text-accent-400 underline cursor-pointer" onclick={() => (mode = "edit")}>
                  Start writing
                </button>
              </div>
            {/if}
          </div>
        {/if}
      {/if}
    {/if}
  </div>
</section>
