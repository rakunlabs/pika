<script lang="ts">
  import { onMount } from "svelte";
  import {
    Star,
    Archive,
    Trash2,
    Save,
    Check,
    Loader2,
    Pencil,
    Columns2,
    Eye,
    WrapText,
    SlidersHorizontal,
    Folder as FolderIcon,
    FileText,
  } from "lucide-svelte";
  import type { Extension } from "@codemirror/state";
  import type { LanguageSupport } from "@codemirror/language";
  import { keymap } from "@codemirror/view";
  import AppCodeMirror from "@/lib/editor/AppCodeMirror.svelte";
  import { vaultStore } from "@/lib/vault/store.svelte";
  import { languageLoader } from "@/lib/vault/languages";
  import { renderMarkdown } from "@/lib/vault/markdown";
  import { deriveNoteTitle as deriveTitle, relativeTime, UNTITLED_NOTE } from "@/lib/vault/itemSummary";
  import { addToast } from "@/lib/store/toast.svelte";
  import { confirmDialog } from "@/lib/store/confirm.svelte";
  import { apiErrorMessage, apiErrorStatus } from "@/lib/api/client";
  import { prefsStore } from "@/lib/store/prefs.svelte";
  import type { VaultItem } from "@/lib/vault/api";
  import type { VaultItemPayload } from "@/lib/vault/crypto";

  interface Props {
    item: VaultItem;
    title: string | null;
    folder: string | null;
    payload: VaultItemPayload | null;
    onClose: () => void;
    /** Switch to the full item editor (tags, custom fields, history). */
    onOpenDetails: () => void;
    onDirtyChange: (dirty: boolean) => void;
  }
  let { item, title: initialTitle, folder, payload, onClose, onOpenDetails, onDirtyChange }: Props =
    $props();

  // The editor stays mounted across saves (keyed by item id only), so
  // the saved baseline is tracked here rather than re-read from props.
  // svelte-ignore state_referenced_locally
  const initial = { title: initialTitle ?? "", text: payload?.notes ?? "" };
  let savedTitle = $state(initial.title);
  let savedText = $state(initial.text);
  let title = $state(initial.title === UNTITLED_NOTE ? "" : initial.title);
  let text = $state(initial.text);

  const effectiveTitle = $derived(title.trim() || deriveTitle(text) || UNTITLED_NOTE);
  const dirty = $derived(text !== savedText || effectiveTitle !== savedTitle);
  $effect(() => onDirtyChange(dirty));

  type Mode = "edit" | "split" | "preview";
  let mode = $state<Mode>(initial.text.trim() ? "preview" : "edit");
  let wrap = $state(true);
  let saving = $state(false);
  let savedFlash = $state(false);
  let lang = $state<LanguageSupport | null>(null);
  let titleEl: HTMLInputElement | undefined = $state();

  const unreadable = $derived(payload === null);
  const html = $derived(renderMarkdown(text));
  const words = $derived(text.trim() ? text.trim().split(/\s+/).length : 0);
  const extraFields = $derived((payload?.fields ?? []).filter((f) => f.value?.trim()).length);

  onMount(() => {
    languageLoader("note.md")?.()
      .then((l) => (lang = l))
      .catch(() => {});
    if (!initial.text.trim() && !title) titleEl?.focus();
    return () => onDirtyChange(false);
  });

  async function save() {
    if (!dirty || saving || unreadable) return;
    saving = true;
    const nextTitle = effectiveTitle;
    const nextText = text;
    try {
      await vaultStore.updateItem(
        item.id,
        { expected_version: item.version },
        {
          title: nextTitle,
          payload: { fields: payload?.fields ?? [], notes: nextText || undefined },
        },
      );
      savedTitle = nextTitle;
      savedText = nextText;
      savedFlash = true;
      setTimeout(() => (savedFlash = false), 1500);
    } catch (err) {
      addToast(
        apiErrorStatus(err) === 409
          ? "This note was changed elsewhere. Copy your text, then reopen the note to see the latest version."
          : apiErrorMessage(err, "Failed to save note"),
        "alert",
        8000,
      );
    } finally {
      saving = false;
    }
  }

  async function confirmDiscard(): Promise<boolean> {
    if (!dirty) return true;
    return confirmDialog({
      title: "Discard unsaved changes?",
      message: "Your edits to this note haven't been saved.",
      confirmLabel: "Discard",
      danger: true,
    });
  }

  async function act(fn: () => Promise<unknown>, ok: string, fail: string) {
    if (!(await confirmDiscard())) return;
    try {
      await fn();
      addToast(ok, "success", 1500);
      onClose();
    } catch (err) {
      addToast(apiErrorMessage(err, fail), "alert");
    }
  }

  async function toggleFavorite() {
    try {
      await vaultStore.updateItem(item.id, { expected_version: item.version }, { favorite: !item.favorite });
    } catch (err) {
      addToast(apiErrorMessage(err, "Failed to update note"), "alert");
    }
  }

  async function openDetails() {
    if (!(await confirmDiscard())) return;
    onOpenDetails();
  }

  const extensions: Extension[] = [
    keymap.of([
      {
        key: "Mod-s",
        preventDefault: true,
        run: () => {
          void save();
          return true;
        },
      },
    ]),
  ];

  function onWindowKey(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
      e.preventDefault();
      void save();
    }
  }

  function onBeforeUnload(e: BeforeUnloadEvent) {
    if (dirty) e.preventDefault();
  }

  const segBase = "inline-flex items-center gap-1 px-2.5 py-1 text-xs cursor-pointer";
  const segActive = "bg-accent-50 text-accent-700 dark:bg-accent-900/40 dark:text-accent-300";
  const segIdle = "text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-warm-700";
  const iconBtn =
    "p-1.5 rounded hover:bg-slate-100 dark:hover:bg-warm-800 text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 cursor-pointer";
</script>

<svelte:window onkeydown={onWindowKey} onbeforeunload={onBeforeUnload} />

<section class="h-full flex flex-col bg-white dark:bg-warm-950" aria-label="Note">
  <!-- Header -->
  <div
    class="flex items-center gap-3 px-4 py-2.5 border-b border-slate-200 dark:border-warm-700 border-l-4 border-l-amber-500 dark:border-l-amber-400"
  >
    <div class="shrink-0 w-9 h-9 rounded-md flex items-center justify-center bg-amber-100 dark:bg-amber-950/40 text-amber-700 dark:text-amber-300">
      <FileText size={18} />
    </div>
    <div class="flex-1 min-w-0">
      <input
        bind:this={titleEl}
        bind:value={title}
        placeholder={deriveTitle(text) || UNTITLED_NOTE}
        aria-label="Note title"
        onkeydown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            mode = mode === "preview" ? "edit" : mode;
          }
        }}
        class="w-full -ml-1 px-1 py-0.5 text-base font-semibold rounded bg-transparent text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 hover:bg-slate-50 dark:hover:bg-warm-900 focus:outline-none focus:ring-2 focus:ring-accent-500"
      />
      <div class="flex items-center gap-1.5 text-[11px] text-slate-500 dark:text-slate-400 min-w-0">
        {#if folder}
          <FolderIcon size={10} class="shrink-0 text-accent-600 dark:text-accent-400" />
          <span class="truncate">{folder}</span>
          <span class="text-slate-300 dark:text-warm-700">·</span>
        {/if}
        <span class="tabular-nums whitespace-nowrap">{words} word{words === 1 ? "" : "s"}</span>
        <span class="text-slate-300 dark:text-warm-700">·</span>
        {#if dirty}
          <span class="text-amber-600 dark:text-amber-400 whitespace-nowrap">Unsaved changes</span>
        {:else}
          <span class="whitespace-nowrap">Saved {relativeTime(item.updated_at)}</span>
        {/if}
      </div>
    </div>

    <div class="flex rounded border border-slate-300 dark:border-warm-600 overflow-hidden shrink-0" role="group" aria-label="View mode">
      <button class="{segBase} {mode === 'edit' ? segActive : segIdle}" onclick={() => (mode = "edit")} aria-pressed={mode === "edit"} title="Write">
        <Pencil size={12} /> Write
      </button>
      <button class="{segBase} border-l border-slate-300 dark:border-warm-600 {mode === 'split' ? segActive : segIdle}" onclick={() => (mode = "split")} aria-pressed={mode === "split"} title="Write and preview side by side">
        <Columns2 size={12} />
      </button>
      <button class="{segBase} border-l border-slate-300 dark:border-warm-600 {mode === 'preview' ? segActive : segIdle}" onclick={() => (mode = "preview")} aria-pressed={mode === "preview"} title="Preview">
        <Eye size={12} /> Read
      </button>
    </div>

    <div class="flex items-center gap-0.5 shrink-0">
      {#if mode !== "preview"}
        <button
          class="p-1.5 rounded cursor-pointer {wrap ? segActive : 'text-slate-400 hover:bg-slate-100 dark:hover:bg-warm-800'}"
          onclick={() => (wrap = !wrap)}
          aria-pressed={wrap}
          aria-label="Toggle line wrapping"
          title="Line wrapping"
        >
          <WrapText size={14} />
        </button>
      {/if}
      <button class={iconBtn} onclick={toggleFavorite} title={item.favorite ? "Remove from favorites" : "Add to favorites"} aria-pressed={!!item.favorite}>
        <Star size={15} fill={item.favorite ? "currentColor" : "none"} class={item.favorite ? "text-amber-500" : ""} />
      </button>
      <button
        class={iconBtn}
        onclick={() => act(() => vaultStore.updateItem(item.id, { expected_version: item.version }, { archived: !item.archived }), item.archived ? "Unarchived" : "Archived", "Failed to update note")}
        title={item.archived ? "Unarchive" : "Archive"}
      >
        <Archive size={14} class={item.archived ? "text-amber-600 dark:text-amber-400" : ""} />
      </button>
      <button class={iconBtn} onclick={() => act(() => vaultStore.softDeleteItem(item.id), "Moved to trash", "Failed to delete note")} title="Move to trash">
        <Trash2 size={14} />
      </button>
      <button class={iconBtn} onclick={openDetails} title="Details: tags, extra fields, history">
        <SlidersHorizontal size={14} />
      </button>
      <div class="w-px h-5 bg-slate-200 dark:bg-warm-700 mx-1"></div>
      <button
        class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
        onclick={save}
        disabled={!dirty || saving || unreadable}
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
  </div>

  {#if unreadable}
    <div class="m-4 bg-red-50 dark:bg-red-950/30 border border-red-300 dark:border-red-700 rounded p-3 text-sm text-red-700 dark:text-red-300">
      This note could not be decrypted. It may have been encrypted under a different vault key. You can still
      delete it.
    </div>
  {:else}
    {#if extraFields > 0}
      <div class="flex items-center gap-2 px-4 py-1.5 text-xs border-b border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900 text-slate-600 dark:text-slate-300">
        This note also has {extraFields} extra field{extraFields === 1 ? "" : "s"}.
        <button class="text-accent-700 dark:text-accent-300 hover:underline cursor-pointer" onclick={openDetails}>Show details</button>
      </div>
    {/if}

    <div class="flex-1 min-h-0 flex">
      {#if mode !== "preview"}
        <div class="min-w-0 h-full {mode === 'split' ? 'w-1/2 border-r border-slate-200 dark:border-warm-700' : 'flex-1'}">
          <AppCodeMirror
            value={text}
            {lang}
            {extensions}
            lineWrapping={wrap}
            onchange={(v) => (text = v)}
          />
        </div>
      {/if}
      {#if mode !== "edit"}
        <div class="min-w-0 h-full overflow-y-auto {mode === 'split' ? 'w-1/2' : 'flex-1'}">
          {#if text.trim()}
            <!-- renderMarkdown escapes all input; see lib/vault/markdown.ts -->
            <article
              class="prose-vault max-w-3xl px-8 py-6 text-sm text-slate-700 dark:text-slate-100"
              style="font-size: {Math.max(13, prefsStore.editor.font_size + 1)}px"
            >
              {@html html}
            </article>
          {:else}
            <div class="h-full flex flex-col items-center justify-center text-center text-slate-400 dark:text-slate-500 px-6">
              <Pencil size={26} class="mb-2 opacity-40" />
              <div class="text-sm font-medium text-slate-600 dark:text-slate-300 mb-1">This note is empty</div>
              <button class="text-xs text-accent-600 dark:text-accent-400 underline cursor-pointer" onclick={() => (mode = "edit")}>
                Start writing
              </button>
            </div>
          {/if}
        </div>
      {/if}
    </div>
  {/if}
</section>
