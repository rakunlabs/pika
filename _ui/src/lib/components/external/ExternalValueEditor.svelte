<script lang="ts">
  // ExternalValueEditor renders a single value from the External
  // resource browser inside a CodeMirror surface.
  //
  // It owns three things that the Configuration editor also owns,
  // re-implemented here at smaller scope:
  //
  //   1. Language detection/selection — `lang='auto'` inspects the
  //      value and picks JSON or YAML; the user can override via the
  //      format selector in the toolbar (TEXT / JSON / YAML).
  //   2. Beautify — pretty-prints JSON (and trims YAML/TEXT).
  //   3. Copy to clipboard.
  //
  // Visual chrome mirrors Editor.svelte:386-511 (bg-[#1e1e1e] /
  // bg-[#252526] toolbar, brand-500 format pill) so this surface
  // feels like part of the same product.

  import { untrack } from "svelte";
  import AppCodeMirror from "@/lib/editor/AppCodeMirror.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import { json, jsonParseLinter } from "@codemirror/lang-json";
  import { yaml } from "@codemirror/lang-yaml";
  import type { LanguageSupport } from "@codemirror/language";
  import { linter, lintGutter, type Diagnostic } from "@codemirror/lint";
  import type { Extension } from "@codemirror/state";
  import { Copy, Check, Sparkles, ChevronDown } from "lucide-svelte";
  import jsYaml from "js-yaml";

  type LangChoice = "auto" | "json" | "yaml" | "text";

  interface Props {
    value: string;
    readonly?: boolean;
    onchange?: (v: string) => void;
    /** Syntax highlighting hint:
     *    'auto' — inspect the value (JSON > YAML > text)
     *    'json' / 'yaml' — force the matching language
     *    'text' / 'none' — render as plain text, no highlighting */
    lang?: LangChoice | "none";
    /** Placeholder shown inside the editor when the value is empty. */
    placeholder?: string;
    /** Optional title rendered on the left of the toolbar. */
    title?: string;
    /** When true, render a format selector dropdown on the right side
     *  of the toolbar so the user can switch between auto / JSON /
     *  YAML / text. Pairs with a Beautify button that pretty-prints
     *  the current content according to the chosen format. Default
     *  off: most readonly displays don't need format controls because
     *  auto-detect already nails it. */
    showFormatControls?: boolean;
    /** Two-way: surfaces the current syntax-validation error (or null)
     *  for the effective language so a parent can gate Save on it, the
     *  way the Configurations editor does. Updated reactively as the
     *  user types or switches format. */
    lintError?: string | null;
  }

  let {
    value = $bindable(),
    readonly = false,
    onchange,
    lang = "auto",
    placeholder = "",
    title,
    showFormatControls = false,
    lintError = $bindable(null),
  }: Props = $props();

  // Internal lang state: starts from the prop's initial value and
  // then tracks user overrides via the format selector. We use
  // `untrack` so Svelte's compiler knows we intentionally only read
  // the prop once at mount — subsequent prop changes don't override
  // a user choice (if the parent flips between resources, a remount
  // via {#key} reseeds this correctly).
  let chosenLang = $state<LangChoice>(
    untrack(() => (lang === "none" ? "text" : (lang as LangChoice))),
  );

  // Effective lang for the editor extension — folds 'auto' into a
  // concrete language by inspecting the value.
  const effectiveLang = $derived.by((): "json" | "yaml" | "text" => {
    if (chosenLang !== "auto") return chosenLang;

    const trimmed = value.trim();
    if (!trimmed) return "text";

    if (trimmed[0] === "{" || trimmed[0] === "[") {
      try {
        JSON.parse(trimmed);
        return "json";
      } catch {
        /* fall through to YAML probe */
      }
    }

    if (/^[\w.-]+\s*:(\s|$)/m.test(trimmed)) {
      try {
        const parsed = jsYaml.load(trimmed);
        if (parsed !== null && typeof parsed === "object") {
          return "yaml";
        }
      } catch {
        /* not YAML */
      }
    }

    return "text";
  });

  const languageExtension = $derived.by((): LanguageSupport | undefined => {
    switch (effectiveLang) {
      case "json":
        return json();
      case "yaml":
        return yaml();
      default:
        return undefined;
    }
  });

  // ── Linting ───────────────────────────────────────────────────────
  // Mirrors the Configuration editor (Editor.svelte): JSON uses
  // CodeMirror's built-in jsonParseLinter; YAML is validated with
  // js-yaml, surfacing the parse error at its reported mark. A
  // lintGutter() paints the error markers in the gutter. The extension
  // set is keyed on the effective language so switching formats (via
  // the toolbar selector) re-derives the right linter.
  const LINT_DELAY = 500; // ms debounce, matches Editor.svelte

  const yamlLinter = linter(
    (view) => {
      const doc = view.state.doc;
      const content = doc.toString();
      if (!content.trim()) return [];

      try {
        jsYaml.load(content);
        return [];
      } catch (e: any) {
        const diagnostics: Diagnostic[] = [];
        if (e?.mark) {
          const line = Math.min(e.mark.line, doc.lines - 1);
          const lineObj = doc.line(line + 1);
          const from =
            lineObj.from + Math.min(e.mark.column || 0, lineObj.length);
          const to = Math.min(from + 1, doc.length);
          diagnostics.push({
            from,
            to,
            severity: "error",
            message: e.reason || "YAML syntax error",
          });
        } else {
          diagnostics.push({
            from: 0,
            to: Math.min(1, doc.length),
            severity: "error",
            message: e?.message || "YAML syntax error",
          });
        }
        return diagnostics;
      }
    },
    { delay: LINT_DELAY },
  );

  const lintExtensions = $derived.by((): Extension[] => {
    switch (effectiveLang) {
      case "json":
        return [lintGutter(), linter(jsonParseLinter(), { delay: LINT_DELAY })];
      case "yaml":
        return [lintGutter(), yamlLinter];
      default:
        return [];
    }
  });

  // Surface the current validation status to the parent so it can gate
  // Save on it. Mirrors validateContent() in the Configurations editor:
  // JSON via JSON.parse, YAML via js-yaml; plain text never errors. This
  // is the same parse the inline linter performs, lifted to a value the
  // parent can read (the linter only paints the gutter).
  const currentLintError = $derived.by((): string | null => {
    const trimmed = value.trim();
    if (!trimmed) return null;
    try {
      switch (effectiveLang) {
        case "json":
          JSON.parse(value);
          return null;
        case "yaml":
          jsYaml.load(value);
          return null;
        default:
          return null;
      }
    } catch (e: any) {
      return e?.reason || e?.message || `Invalid ${effectiveLang.toUpperCase()}`;
    }
  });

  $effect(() => {
    lintError = currentLintError;
  });

  const formatLabel = $derived(effectiveLang.toUpperCase());

  // ── Copy ──────────────────────────────────────────────────────────
  let copied = $state(false);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(value);
      copied = true;
      clearTimeout(copyTimer);
      copyTimer = setTimeout(() => {
        copied = false;
      }, 1500);
    } catch (err) {
      console.error("Failed to copy value:", err);
      addToast("Failed to copy", "alert");
    }
  }

  // ── Beautify ──────────────────────────────────────────────────────
  // Mirrors Editor.svelte:101-144 — pretty-prints based on the
  // effective lang. JSON is the most useful case (re-indents and
  // sorts keys back to canonical form); YAML/TEXT do whitespace
  // tidying so a beautify always feels like it did something.
  function handleBeautify() {
    if (readonly) return;
    const next = beautify(value, effectiveLang);
    if (next === null) {
      addToast(
        `Could not beautify — check ${effectiveLang.toUpperCase()} syntax`,
        "alert",
      );
      return;
    }
    if (next === value) {
      addToast("Already formatted", "info");
      return;
    }
    value = next;
    onchange?.(next);
    addToast("Formatted", "success");
  }

  function beautify(
    content: string,
    fmt: "json" | "yaml" | "text",
  ): string | null {
    try {
      if (fmt === "json") {
        return JSON.stringify(JSON.parse(content), null, 2);
      }
      if (fmt === "yaml") {
        // js-yaml's dump normalizes whitespace, key order in YAML
        // mappings, and string quoting — closer to a true pretty-
        // printer than the trim-only fallback in Editor.svelte.
        return jsYaml.dump(jsYaml.load(content), { indent: 2, lineWidth: 100 });
      }
      // TEXT: collapse triple+ blank lines, trim trailing whitespace.
      return (
        content
          .split("\n")
          .map((l) => l.trimEnd())
          .join("\n")
          .replace(/\n{3,}/g, "\n\n")
          .trim() + "\n"
      );
    } catch {
      return null;
    }
  }

  // ── Format selector dropdown ──────────────────────────────────────
  let formatMenuOpen = $state(false);
  let formatMenuEl: HTMLDivElement | undefined = $state();

  function closeFormatMenu(e?: MouseEvent) {
    if (e && formatMenuEl?.contains(e.target as Node)) return;
    formatMenuOpen = false;
  }

  $effect(() => {
    if (!formatMenuOpen) return;
    document.addEventListener("click", closeFormatMenu);
    return () => document.removeEventListener("click", closeFormatMenu);
  });

  function pickLang(l: LangChoice) {
    chosenLang = l;
    formatMenuOpen = false;
  }
</script>

<!--
  Outer wrapper carries the dark IDE chrome that Editor.svelte uses
  (bg-[#1e1e1e]). The choice to render as VS Code dark regardless of
  the user's light-mode app theme is consistent with the Configuration
  page: a "code surface" reads as a code surface even when the rest
  of the app is light.

  No outer border or rounded corners — the parent in External.svelte
  places us edge-to-edge for a true full-pane look.
-->
<div class="flex flex-col h-full overflow-hidden bg-[#1e1e1e]">
  <!-- Toolbar — left: format pill + optional title; right: optional
       Beautify, optional format dropdown, always-on Copy. Toolbar
       metrics match Editor.svelte:389 (px-3 py-1.5). -->
  <div
    class="flex items-center justify-between px-3 py-1.5 bg-[#252526] border-b border-[#3c3c3c] text-xs shrink-0"
  >
    <div class="flex items-center gap-2 min-w-0">
      <span
        class="px-1.5 py-0.5 rounded text-[10px] font-semibold shrink-0 text-white
               {effectiveLang === 'text' ? 'bg-slate-500' : 'bg-brand-500'}"
      >
        {formatLabel}
      </span>
      {#if title}
        <span class="text-gray-600 dark:text-slate-300 shrink-0">|</span>
        <span
          class="text-gray-300 font-mono overflow-hidden text-ellipsis whitespace-nowrap min-w-0"
          {title}
        >
          {title}
        </span>
      {/if}
    </div>
    <div class="flex items-center gap-1.5 shrink-0">
      {#if showFormatControls && !readonly}
        <!-- Beautify. Disabled when the content can't currently be
             beautified (handled inside handleBeautify by toast). -->
        <button
          type="button"
          class="flex items-center gap-1 px-2 py-0.5 bg-transparent border border-[#3c3c3c] rounded text-[11px] text-gray-400 hover:bg-[#333] hover:text-gray-100 cursor-pointer transition-colors"
          onclick={handleBeautify}
          title="Beautify"
          aria-label="Beautify"
        >
          <Sparkles size={12} />
          <span>Beautify</span>
        </button>

        <!-- Format selector. Tiny popup so the toolbar stays compact;
             clicking outside dismisses it (effect above). The pill on
             the left side of the toolbar always reflects the
             effective lang, so the dropdown is showing the *override*
             choice, with 'auto' being the default. -->
        <div class="relative" bind:this={formatMenuEl}>
          <button
            type="button"
            class="flex items-center gap-1 px-2 py-0.5 bg-transparent border border-[#3c3c3c] rounded text-[11px] text-gray-400 hover:bg-[#333] hover:text-gray-100 cursor-pointer transition-colors"
            onclick={() => (formatMenuOpen = !formatMenuOpen)}
            title="Choose format"
            aria-haspopup="true"
            aria-expanded={formatMenuOpen}
          >
            <span class="font-mono uppercase"
              >{chosenLang === "auto" ? "Auto" : chosenLang}</span
            >
            <ChevronDown size={11} />
          </button>
          {#if formatMenuOpen}
            <div
              class="absolute right-0 top-full mt-1 z-10 min-w-[90px] bg-[#252526] border border-[#3c3c3c] rounded shadow-lg overflow-hidden"
            >
              {#each [{ value: "auto", label: "Auto-detect" }, { value: "json", label: "JSON" }, { value: "yaml", label: "YAML" }, { value: "text", label: "Plain text" }] as opt (opt.value)}
                <button
                  type="button"
                  class="w-full text-left px-2.5 py-1 text-[11px] cursor-pointer transition-colors
                         {chosenLang === opt.value
                    ? 'bg-brand-500 text-white'
                    : 'text-gray-300 hover:bg-[#333]'}"
                  onclick={() => pickLang(opt.value as LangChoice)}
                >
                  {opt.label}
                </button>
              {/each}
            </div>
          {/if}
        </div>
      {/if}

      <button
        type="button"
        class="flex items-center gap-1 px-2 py-0.5 bg-transparent border border-[#3c3c3c] rounded text-[11px] text-gray-400 hover:bg-[#333] hover:text-gray-100 cursor-pointer transition-colors"
        onclick={handleCopy}
        title="Copy value"
        aria-label="Copy value"
      >
        {#if copied}
          <Check size={12} class="text-green-400" />
          <span>Copied</span>
        {:else}
          <Copy size={12} />
          <span>Copy</span>
        {/if}
      </button>
    </div>
  </div>

  <!-- Body. `flex-1 min-h-0` lets the editor expand to fill the
       parent's remaining height. min-h-[8rem] is a backstop so the
       editor never collapses to zero when its parent doesn't bound
       it vertically. -->
  <div class="relative flex-1 min-h-[8rem] overflow-auto">
    <AppCodeMirror
      {value}
      lang={languageExtension}
      extensions={lintExtensions}
      {readonly}
      {onchange}
    />
    {#if !value && placeholder}
      <div
        class="pointer-events-none absolute top-1.5 left-3 text-[11px] text-slate-500 italic font-mono"
      >
        {placeholder}
      </div>
    {/if}
  </div>
</div>
