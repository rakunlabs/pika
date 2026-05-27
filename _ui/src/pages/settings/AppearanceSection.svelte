<script lang="ts">
  import { RotateCcw, Save, Undo2 } from "lucide-svelte";

  import { prefsStore, type EditorThemeKey } from "@/lib/store/prefs.svelte";
  import {
    EDITOR_THEMES,
    EDITOR_FONT_FAMILIES,
    EDITOR_FONT_SIZE_MIN,
    EDITOR_FONT_SIZE_MAX,
  } from "@/lib/editor/themes";
  import AppCodeMirror from "@/lib/editor/AppCodeMirror.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import { json } from "@codemirror/lang-json";

  // Preview content shown live next to the editor preferences so the
  // user can see font, size and theme changes without leaving the page.
  const PREVIEW_VALUE = `{
 "service": "pika",
 "version": 1,
 "features": {
 "auth": true,
 "rate_limit": 100
 }
}`;

  // All setters below are LOCAL ONLY — they don't hit the network.
  // Persisting to the backend happens once, when the user clicks "Save"
  // (handleSave below). This keeps DB write traffic to one PUT per
  // intentional commit instead of one per slider tick.
  function setEditorTheme(theme: EditorThemeKey) {
    prefsStore.updatePreferences({ editor: { theme } });
  }
  function setEditorFontSize(value: number) {
    prefsStore.updatePreferences({ editor: { font_size: value } });
  }
  function setEditorFontFamily(value: string) {
    prefsStore.updatePreferences({ editor: { font_family: value } });
  }
  function setEditorLineWrap(value: boolean) {
    prefsStore.updatePreferences({ editor: { line_wrap: value } });
  }

  async function handleSave() {
    try {
      await prefsStore.savePreferences();
      addToast("Appearance preferences saved", "success");
    } catch (err: any) {
      const msg = err?.response?.data?.message ?? err?.message ?? String(err);
      addToast(`Failed to save: ${msg}`, "alert");
    }
  }

  function handleRevert() {
    prefsStore.revertPreferences();
  }

  async function resetAll() {
    if (
      !confirm(
        "Reset all appearance preferences to defaults? This will be saved immediately.",
      )
    )
      return;
    try {
      await prefsStore.resetPreferences();
      addToast("Appearance preferences reset to defaults", "success");
    } catch (err: any) {
      addToast(`Failed to reset: ${err?.message ?? err}`, "alert");
    }
  }
</script>

<div>
  <div class="mb-4 flex items-start justify-between gap-4">
    <div>
      <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">
        Appearance
      </h2>
      <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
        Personal display preferences stored against your user — they follow you
        across devices.
      </p>
    </div>
    <div class="flex items-center gap-2">
      <button
        type="button"
        class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-slate-600 dark:text-slate-300 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md hover:bg-slate-50 dark:hover:bg-warm-700 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
        onclick={handleRevert}
        disabled={!prefsStore.dirty || prefsStore.saving}
        title="Discard unsaved changes and revert to the last saved state"
      >
        <Undo2 size={12} />
        Revert
      </button>
      <button
        type="button"
        class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-slate-600 dark:text-slate-300 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md hover:bg-slate-50 dark:hover:bg-warm-700 cursor-pointer"
        onclick={resetAll}
        title="Reset all appearance settings to their defaults (saves immediately)"
      >
        <RotateCcw size={12} />
        Reset
      </button>
      <button
        type="button"
        class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-white bg-accent-600 hover:bg-accent-700 rounded-md cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
        onclick={handleSave}
        disabled={!prefsStore.dirty || prefsStore.saving}
        title="Save changes to the server"
      >
        <Save size={12} />
        {prefsStore.saving ? "Saving…" : "Save"}
      </button>
    </div>
  </div>

  <!--
 Note: the application light/dark theme is intentionally NOT shown
 here. It is a purely client-local, per-browser preference toggled
 from the login screen's theme switcher (top-right) — see
 Login.svelte. Keeping that choice out of the server-side preference
 document means it can't accidentally fight the user's per-device
 expectations (e.g. their work laptop on dark, phone on light).
 -->

  <!-- Editor preferences -->
  <section
    class="p-5 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm mb-4"
  >
    <h3 class="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-1">
      Editor
    </h3>
    <p class="text-xs text-slate-500 dark:text-slate-400 mb-3">
      Theme, font and behavior of the code editor. The preview below updates as
      you change settings.
    </p>

    <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
      <!-- Theme -->
      <label class="flex flex-col gap-1">
        <span class="text-xs font-medium text-slate-600 dark:text-slate-300"
          >Theme</span
        >
        <select
          class="px-2 py-1.5 text-sm border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 rounded-md cursor-pointer"
          value={prefsStore.editor.theme}
          onchange={(e) =>
            setEditorTheme(
              (e.currentTarget as HTMLSelectElement).value as EditorThemeKey,
            )}
        >
          <optgroup label="Light">
            {#each EDITOR_THEMES.filter((t) => !t.isDark) as t}
              <option value={t.key}>{t.label}</option>
            {/each}
          </optgroup>
          <optgroup label="Dark">
            {#each EDITOR_THEMES.filter((t) => t.isDark) as t}
              <option value={t.key}>{t.label}</option>
            {/each}
          </optgroup>
        </select>
      </label>

      <!-- Font family -->
      <label class="flex flex-col gap-1">
        <span class="text-xs font-medium text-slate-600 dark:text-slate-300"
          >Font family</span
        >
        <select
          class="px-2 py-1.5 text-sm border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 rounded-md cursor-pointer"
          value={prefsStore.editor.font_family}
          onchange={(e) =>
            setEditorFontFamily((e.currentTarget as HTMLSelectElement).value)}
        >
          {#each EDITOR_FONT_FAMILIES as ff}
            <option value={ff.value}>{ff.label}</option>
          {/each}
        </select>
      </label>

      <!-- Font size -->
      <label class="flex flex-col gap-1">
        <span class="text-xs font-medium text-slate-600 dark:text-slate-300">
          Font size: <span class="font-mono text-slate-800 dark:text-slate-100"
            >{prefsStore.editor.font_size}px</span
          >
        </span>
        <input
          type="range"
          min={EDITOR_FONT_SIZE_MIN}
          max={EDITOR_FONT_SIZE_MAX}
          step="1"
          value={prefsStore.editor.font_size}
          oninput={(e) =>
            setEditorFontSize(
              parseInt((e.currentTarget as HTMLInputElement).value, 10),
            )}
          class="cursor-pointer"
        />
      </label>

      <!-- Line wrap -->
      <label class="flex items-center gap-2 mt-auto pt-1">
        <input
          type="checkbox"
          checked={prefsStore.editor.line_wrap}
          onchange={(e) =>
            setEditorLineWrap((e.currentTarget as HTMLInputElement).checked)}
          class="cursor-pointer"
        />
        <span class="text-sm text-slate-700 dark:text-slate-200"
          >Wrap long lines</span
        >
      </label>
    </div>

    <!-- Live preview. Height scales with the chosen font size so a
         48px setting doesn't squash the preview into a single visible
         line, but is capped so the section never dominates the page.
         The wrapper also forwards an explicit scrollbar — CodeMirror's
         internal `.cm-scroller` is what actually scrolls long content
         vertically and horizontally; we just give it the room to do
         so and a min-height floor for the small-font end. -->
    <div class="mt-4">
      <span
        class="block text-xs font-medium text-slate-600 dark:text-slate-300 mb-1"
        >Preview</span
      >
      <div
        class="border border-slate-200 dark:border-warm-700 rounded-md overflow-auto bg-white dark:bg-warm-900"
        style="height: {Math.min(
          420,
          Math.max(176, prefsStore.editor.font_size * 9),
        )}px"
      >
        <AppCodeMirror value={PREVIEW_VALUE} lang={json()} readonly={true} />
      </div>
    </div>
  </section>

  <!--
 Panel widths used to be exposed here as sliders but were removed
 intentionally: those values are session-only now. Dragging the
 resize handles inside the app updates the layout immediately and
 that state lives in-memory until the page is reloaded — not worth
 a DB write per drag, and not worth a "Save" button to commit.
 -->

  {#if prefsStore.dirty}
    <!-- Unsaved-changes banner — placed at the bottom of the panel so
 it sits right above the user's natural reading flow (sections
 first, then the prompt) instead of pushing the form down. -->
    <div
      class="mt-4 px-3 py-2 text-xs text-amber-800 dark:text-amber-300 bg-amber-50 dark:bg-amber-900/30 border border-amber-200 dark:border-amber-800 rounded-md"
    >
      You have unsaved appearance changes. Click <strong>Save</strong> to persist
      them.
    </div>
  {/if}
</div>
