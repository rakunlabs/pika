<script lang="ts">
     import AppCodeMirror from "@/lib/editor/AppCodeMirror.svelte";
     import { json, jsonParseLinter } from "@codemirror/lang-json";
     import { yaml } from "@codemirror/lang-yaml";
     import { StreamLanguage, LanguageSupport } from "@codemirror/language";
     import { toml } from "@codemirror/legacy-modes/mode/toml";
     import { linter, lintGutter, type Diagnostic } from "@codemirror/lint";
     import type { Extension } from "@codemirror/state";
     import type { EditorView } from "@codemirror/view";
     import { configStore } from "@/lib/store/config.svelte";
     import { prefsStore } from "@/lib/store/prefs.svelte";
     import { addToast } from "@/lib/store/toast.svelte";
     import type { FileFormat, ViewMode } from "@/lib/types/config";
     import {
          Save,
          Sparkles,
          ArrowRightLeft,
          Upload,
          Binary,
          Type,
          Eye,
          EyeOff,
          Copy,
          Check,
          RefreshCw,
          TextWrap,
     } from "lucide-svelte";
     import { AlertTriangle } from "lucide-svelte";
     import axios from "axios";
     import jsYaml from "js-yaml";
     import { parse as parseToml, TomlError } from "smol-toml";
     import HexViewer from "./HexViewer.svelte";
     import {
          createMaskExtension,
          createMaskWatcher,
          setMaskEnabled,
          type MaskInfo,
     } from "./maskExtension";

     const LINT_DELAY = 500; // ms debounce

     // YAML linter using js-yaml for full validation
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
                         // js-yaml provides 0-based line/column via e.mark
                         const line = Math.min(e.mark.line, doc.lines - 1);
                         const lineObj = doc.line(line + 1);
                         const from =
                              lineObj.from +
                              Math.min(e.mark.column || 0, lineObj.length);
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
                              message: e.message || "YAML syntax error",
                         });
                    }
                    return diagnostics;
               }
          },
          { delay: LINT_DELAY },
     );

     // TOML linter using smol-toml for full validation
     const tomlLinter = linter(
          (view) => {
               const doc = view.state.doc;
               const content = doc.toString();
               if (!content.trim()) return [];

               try {
                    parseToml(content);
                    return [];
               } catch (e: any) {
                    const diagnostics: Diagnostic[] = [];
                    if (e instanceof TomlError && e.line !== undefined) {
                         // smol-toml provides 1-based line/column
                         const line = Math.min(e.line, doc.lines);
                         const lineObj = doc.line(line);
                         const col = Math.min(
                              (e.column || 1) - 1,
                              lineObj.length,
                         );
                         const from = lineObj.from + col;
                         const to = Math.min(from + 1, doc.length);
                         diagnostics.push({
                              from,
                              to,
                              severity: "error",
                              message: e.message.split("\n")[0],
                         });
                    } else {
                         diagnostics.push({
                              from: 0,
                              to: Math.min(1, doc.length),
                              severity: "error",
                              message: e.message || "TOML syntax error",
                         });
                    }
                    return diagnostics;
               }
          },
          { delay: LINT_DELAY },
     );

     function getLanguageExtension(
          format: FileFormat,
     ): LanguageSupport | undefined {
          switch (format) {
               case "json":
                    return json();
               case "yaml":
                    return yaml();
               case "toml":
                    return new LanguageSupport(StreamLanguage.define(toml));
               default:
                    return undefined;
          }
     }

     function getLintExtensions(format: FileFormat): Extension[] {
          switch (format) {
               case "json":
                    return [
                         lintGutter(),
                         linter(jsonParseLinter(), { delay: LINT_DELAY }),
                    ];
               case "yaml":
                    return [lintGutter(), yamlLinter];
               case "toml":
                    return [lintGutter(), tomlLinter];
               default:
                    return [];
          }
     }

     // Beautify content based on format
     function beautify(content: string, format: FileFormat): string | null {
          try {
               switch (format) {
                    case "json": {
                         const parsed = JSON.parse(content);
                         return JSON.stringify(parsed, null, 2);
                    }
                    case "yaml": {
                         const lines = content.split("\n");
                         const cleaned = lines
                              .map((line) => line.trimEnd())
                              .join("\n")
                              .replace(/\n{3,}/g, "\n\n")
                              .trim();
                         return cleaned + "\n";
                    }
                    case "toml": {
                         const lines = content.split("\n");
                         const cleaned = lines
                              .map((line) => {
                                   const trimmed = line.trimEnd();
                                   if (
                                        trimmed.startsWith("[") ||
                                        trimmed.startsWith("#") ||
                                        trimmed === ""
                                   ) {
                                        return trimmed;
                                   }
                                   const eqIndex = trimmed.indexOf("=");
                                   if (eqIndex > 0) {
                                        const key = trimmed
                                             .slice(0, eqIndex)
                                             .trimEnd();
                                        const value = trimmed
                                             .slice(eqIndex + 1)
                                             .trimStart();
                                        return `${key} = ${value}`;
                                   }
                                   return trimmed;
                              })
                              .join("\n")
                              .replace(/\n{3,}/g, "\n\n")
                              .trim();
                         return cleaned + "\n";
                    }
                    default:
                         return null;
               }
          } catch {
               return null;
          }
     }

     const allFormats: FileFormat[] = ["json", "yaml", "toml"];

     const activeTab = $derived(configStore.activeTab);
     const languageExtension = $derived(
          activeTab ? getLanguageExtension(activeTab.format) : undefined,
     );
     const isHexMode = $derived(activeTab?.viewMode === "hex");
     const hexData = $derived(activeTab?.rawData || "");
     const canMask = $derived(
          !!activeTab && activeTab.format !== "raw" && !isHexMode,
     );
     const isLineWrapped = $derived(prefsStore.editor.line_wrap);

     // Mask state surfaced from the editor. The button reflects this directly:
     // anything other than "fully masked, no reveals" flips the button to
     // "Visible" so the next click re-masks everything.
     let maskInfo: MaskInfo = $state({ enabled: true, hasReveals: false });
     const fullyMasked = $derived(maskInfo.enabled && !maskInfo.hasReveals);
     let cmView: EditorView | undefined = $state();

     // Stable watcher — created once, included as a stable extension reference.
     const maskWatcher = createMaskWatcher((info) => {
          maskInfo = info;
     });

     // Switching tabs should re-tape, since the StateField is module-scoped and
     // would otherwise carry reveals (with stale positions) over to the new file.
     $effect(() => {
          if (activeTab && cmView) setMaskEnabled(cmView, true);
     });

     function toggleMask() {
          if (!cmView) return;
          // If anything is revealed → click re-masks everything.
          // If everything is masked → click reveals all.
          setMaskEnabled(cmView, !fullyMasked);
     }

     // The mask extension joins lint extensions; a format change re-derives this
     // array, which makes svelte-codemirror reconfigure. The mask state field is
     // a module-level singleton so its value persists across reconfigures.
     const lintExtensions = $derived(
          activeTab
               ? [
                      ...(activeTab.meta.go_template
                           ? []
                           : getLintExtensions(activeTab.format)),
                      createMaskExtension(activeTab.format),
                      maskWatcher,
                 ]
               : [],
     );
     const convertTargets = $derived(
          activeTab ? allFormats.filter((f) => f !== activeTab.format) : [],
     );

     let isConverting = $state(false);
     let saveConstraint = $state("");
     let fileInput: HTMLInputElement | undefined = $state();
     let pendingSaveConfirm = $state(false);
     let pendingSaveTimer: ReturnType<typeof setTimeout> | undefined;
     let copied = $state(false);
     let copyTimer: ReturnType<typeof setTimeout> | undefined;

     async function handleCopy() {
          if (!activeTab) return;
          try {
               await navigator.clipboard.writeText(activeTab.content);
               copied = true;
               clearTimeout(copyTimer);
               copyTimer = setTimeout(() => {
                    copied = false;
               }, 1500);
          } catch (err) {
               console.error("Failed to copy editor content:", err);
               addToast("Failed to copy", "alert");
          }
     }

     // Validate content against its format. Returns error message or null if valid.
     function validateContent(
          content: string,
          format: FileFormat,
     ): string | null {
          if (!content.trim()) return null;
          try {
               switch (format) {
                    case "json":
                         JSON.parse(content);
                         return null;
                    case "yaml":
                         jsYaml.load(content);
                         return null;
                    case "toml":
                         parseToml(content);
                         return null;
                    default:
                         return null;
               }
          } catch (e: any) {
               return (
                    e.reason || e.message || `Invalid ${format.toUpperCase()}`
               );
          }
     }

     function handleChange(value: string) {
          if (activeTab) {
               configStore.updateTabContent(activeTab.id, value);
               // Reset confirmation state when content changes
               if (pendingSaveConfirm) {
                    pendingSaveConfirm = false;
                    clearTimeout(pendingSaveTimer);
               }
          }
     }

     async function handleSave() {
          if (!activeTab || !activeTab.isDirty) return;

          // Check for lint errors before saving
          const validationError = activeTab.meta.go_template
               ? null
               : validateContent(activeTab.content, activeTab.format);
          if (validationError && !pendingSaveConfirm) {
               pendingSaveConfirm = true;
               addToast(
                    `${activeTab.format.toUpperCase()} has errors — press Save again to confirm`,
                    "warn",
               );
               // Auto-reset after 5 seconds
               clearTimeout(pendingSaveTimer);
               pendingSaveTimer = setTimeout(() => {
                    pendingSaveConfirm = false;
               }, 5000);
               return;
          }

          pendingSaveConfirm = false;
          clearTimeout(pendingSaveTimer);

          try {
               const constraint = saveConstraint.trim() || undefined;
               await configStore.saveTab(activeTab.id, constraint);
               saveConstraint = "";
          } catch (error) {
               console.error("Failed to save:", error);
          }
     }

     function handleBeautify() {
          if (!activeTab) return;

          const result = beautify(activeTab.content, activeTab.format);
          if (result === null) {
               addToast(
                    `Could not beautify — check ${activeTab.format.toUpperCase()} syntax`,
                    "alert",
               );
               return;
          }

          if (result === activeTab.content) {
               addToast("Already formatted", "info");
               return;
          }

          configStore.updateTabContent(activeTab.id, result);
          addToast("Formatted", "success");
     }

     async function handleConvert(targetFormat: FileFormat) {
          if (!activeTab || isConverting) return;
          if (activeTab.format === targetFormat) return;
          if (activeTab.format === "raw") {
               addToast("Cannot convert from raw format", "alert");
               return;
          }

          isConverting = true;
          try {
               const response = await axios.post("/api/v1/convert", {
                    content: activeTab.content,
                    from: activeTab.format,
                    to: targetFormat,
               });

               configStore.updateTabContent(
                    activeTab.id,
                    response.data.content,
               );
               configStore.updateTabFormat(activeTab.id, targetFormat);
               addToast(
                    `Converted to ${targetFormat.toUpperCase()}`,
                    "success",
               );
          } catch (error: any) {
               const msg = error.response?.data?.message || "Conversion failed";
               addToast(msg, "alert");
          } finally {
               isConverting = false;
          }
     }

     let isReloading = $state(false);

     async function handleReload() {
          if (!activeTab || isReloading) return;
          if (activeTab.isDirty) {
               const ok = confirm(
                    "Unsaved changes will be discarded. Reload from server?",
               );
               if (!ok) return;
          }
          isReloading = true;
          try {
               await configStore.reloadTab(activeTab.id);
          } catch (error) {
               console.error("Failed to reload:", error);
          } finally {
               isReloading = false;
          }
     }

     function handleImportClick() {
          fileInput?.click();
     }

     async function handleFileSelected(e: Event) {
          const input = e.target as HTMLInputElement;
          const file = input.files?.[0];
          if (!file || !activeTab) return;

          try {
               await configStore.importFileToTab(activeTab.id, file);
          } catch (error) {
               console.error("Failed to import file:", error);
          }

          // Reset the file input so the same file can be re-imported
          input.value = "";
     }

     function toggleViewMode() {
          if (!activeTab) return;
          const newMode: ViewMode =
               activeTab.viewMode === "hex" ? "text" : "hex";
          configStore.setTabViewMode(activeTab.id, newMode);
     }

     function toggleLineWrap() {
          prefsStore.updatePreferences({
               editor: { line_wrap: !prefsStore.editor.line_wrap },
          });
     }

     function handleKeyDown(e: KeyboardEvent) {
          if ((e.ctrlKey || e.metaKey) && e.key === "s") {
               e.preventDefault();
               handleSave();
          }
          // Ctrl/Cmd + Shift + F to beautify
          if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key === "F") {
               e.preventDefault();
               handleBeautify();
          }
          // Ctrl/Cmd + M to toggle the value mask
          if (
               (e.ctrlKey || e.metaKey) &&
               !e.shiftKey &&
               (e.key === "m" || e.key === "M")
          ) {
               if (!canMask) return;
               e.preventDefault();
               toggleMask();
          }
     }
</script>

<svelte:window onkeydown={handleKeyDown} />

<!-- Hidden file input for import -->
<input
     type="file"
     class="hidden"
     bind:this={fileInput}
     onchange={handleFileSelected}
/>

<div class="flex flex-col h-full bg-[#1e1e1e]">
     {#if activeTab}
          <!-- Editor toolbar.
      `flex-wrap` + gap-y so the left (format / convert) and right
      (view / mask / reload / save) groups drop onto separate rows
      on narrow viewports instead of squashing into each other. The
      previous version used a `| <path>` suffix to label the file —
      removed because the active tab strip just above already
      shows the file path, and the duplicated label was the main
      thing pushing the toolbar wide enough to overflow. -->
          <div
               class="flex flex-wrap items-center justify-between gap-x-3 gap-y-1.5 px-3 py-1.5 bg-[#252526] border-b border-[#3c3c3c] text-xs text-gray-400 dark:text-slate-500 shrink-0"
          >
               <div class="flex items-center gap-2 min-w-0">
                    <span
                         class="px-1.5 py-0.5 bg-brand-500 text-white rounded text-[10px] font-semibold shrink-0"
                         >{activeTab.format.toUpperCase()}</span
                    >
                    {#if activeTab.format !== "raw" && !isHexMode}
                         <div class="flex items-center gap-1 shrink-0">
                              <ArrowRightLeft
                                   size={11}
                                   class="text-gray-600 dark:text-slate-300"
                              />
                              {#each convertTargets as target}
                                   <button
                                        class="px-1.5 py-0.5 text-[10px] font-medium rounded border border-[#3c3c3c] text-gray-500 dark:text-slate-400 bg-transparent cursor-pointer transition-colors hover:bg-[#333] hover:text-gray-200 hover:border-gray-500 disabled:opacity-40 disabled:cursor-not-allowed"
                                        onclick={() => handleConvert(target)}
                                        disabled={isConverting}
                                        title="Convert to {target.toUpperCase()}"
                                   >
                                        {target.toUpperCase()}
                                   </button>
                              {/each}
                         </div>
                    {/if}
               </div>
               <div class="flex flex-wrap items-center gap-1.5">
                    <!-- View mode toggle: Text / Hex -->
                    <div
                         class="flex items-center rounded border border-[#3c3c3c] overflow-hidden shrink-0"
                    >
                         <button
                              class="flex items-center gap-1 px-2 py-1 text-[11px] cursor-pointer transition-colors
 {!isHexMode
                                   ? 'bg-[#3c3c3c] text-gray-200'
                                   : 'bg-transparent text-gray-500 dark:text-slate-400 hover:bg-[#333] hover:text-gray-300'}"
                              onclick={() => !isHexMode || toggleViewMode()}
                              title="Text view"
                         >
                              <Type size={12} />
                              <span>Text</span>
                         </button>
                         <button
                              class="flex items-center gap-1 px-2 py-1 text-[11px] cursor-pointer transition-colors
 {isHexMode
                                   ? 'bg-[#3c3c3c] text-gray-200'
                                   : 'bg-transparent text-gray-500 dark:text-slate-400 hover:bg-[#333] hover:text-gray-300'}"
                              onclick={() => isHexMode || toggleViewMode()}
                              title="Hex view"
                         >
                              <Binary size={12} />
                              <span>Hex</span>
                         </button>
                    </div>

                    {#if !isHexMode}
                         <button
                              class="flex items-center gap-1 px-2 py-1 bg-transparent border border-[#3c3c3c] rounded text-[11px] cursor-pointer transition-colors
 {isLineWrapped
                                   ? 'bg-[#3c3c3c] text-accent-300 border-accent-700 hover:text-accent-200'
                                   : 'text-gray-400 dark:text-slate-500 hover:bg-[#333] hover:text-gray-200'}"
                              onclick={toggleLineWrap}
                              title={isLineWrapped
                                   ? "Disable line wrap"
                                   : "Enable line wrap"}
                              aria-pressed={isLineWrapped}
                         >
                              <TextWrap size={12} />
                              <span>Wrap</span>
                         </button>
                    {/if}

                    <!-- Mask toggle: hides scalar values behind black tape.
 Reflects the actual editor state — if any value is revealed
 (or all are), the button flips to "Visible" so the first click
 re-masks everything. -->
                    {#if canMask}
                         <button
                              class="flex items-center gap-1 px-2 py-1 bg-transparent border border-[#3c3c3c] rounded text-[11px] cursor-pointer transition-colors
 {fullyMasked
                                   ? 'text-amber-400 hover:bg-[#333] hover:text-amber-300'
                                   : 'text-gray-400 dark:text-slate-500 hover:bg-[#333] hover:text-gray-200'}"
                              onclick={toggleMask}
                              title={fullyMasked
                                   ? "Reveal all values (Ctrl+M)"
                                   : "Mask all values (Ctrl+M)"}
                         >
                              {#if fullyMasked}
                                   <EyeOff size={12} />
                                   <span>Masked</span>
                              {:else}
                                   <Eye size={12} />
                                   <span>Visible</span>
                              {/if}
                         </button>
                    {/if}

                    <!-- Reload button: refetch the current version from the server -->
                    <button
                         class="flex items-center gap-1 px-2 py-1 text-gray-400 dark:text-slate-500 bg-transparent border border-[#3c3c3c] rounded text-[11px] cursor-pointer transition-colors hover:bg-[#333] hover:text-gray-200 disabled:opacity-40 disabled:cursor-not-allowed"
                         onclick={handleReload}
                         disabled={isReloading}
                         title={activeTab.isDirty
                              ? "Reload from server (discards unsaved changes)"
                              : "Reload from server"}
                    >
                         <RefreshCw
                              size={12}
                              class={isReloading ? "animate-spin" : ""}
                         />
                         <span>Reload</span>
                    </button>

                    <!-- Import button -->
                    <button
                         class="flex items-center gap-1 px-2 py-1 text-gray-400 dark:text-slate-500 bg-transparent border border-[#3c3c3c] rounded text-[11px] cursor-pointer transition-colors hover:bg-[#333] hover:text-gray-200"
                         onclick={handleImportClick}
                         title="Import file from disk"
                    >
                         <Upload size={12} />
                         <span>Import</span>
                    </button>

                    {#if !isHexMode}
                         <button
                              class="flex items-center gap-1 px-2 py-1 text-gray-400 dark:text-slate-500 bg-transparent border border-[#3c3c3c] rounded text-[11px] cursor-pointer transition-colors hover:bg-[#333] hover:text-gray-200"
                              onclick={handleBeautify}
                              title="Beautify (Ctrl+Shift+F)"
                         >
                              <Sparkles size={12} />
                              <span>Beautify</span>
                         </button>
                    {/if}
                    {#if activeTab.isDirty}
                         <input
                              type="text"
                              bind:value={saveConstraint}
                              placeholder="Constraint (e.g. >= 1.0.0)"
                              title="Semver constraint for this version (optional)"
                              class="w-40 px-2 py-1 text-[11px] font-mono bg-[#1e1e1e] border border-[#3c3c3c] rounded text-gray-400 dark:text-slate-500 placeholder:text-gray-600 dark:text-slate-300 focus:outline-none focus:border-amber-500"
                         />
                         <button
                              class="flex items-center gap-1 px-2.5 py-1 border-none rounded text-[11px] font-medium cursor-pointer transition-colors
 {pendingSaveConfirm
                                   ? 'bg-amber-600 hover:bg-amber-500'
                                   : 'bg-green-600 hover:bg-green-500'} text-white"
                              onclick={handleSave}
                              title={pendingSaveConfirm
                                   ? "Content has errors — click to save anyway"
                                   : "Save (Ctrl+S)"}
                         >
                              {#if pendingSaveConfirm}
                                   <AlertTriangle size={12} />
                                   <span>Save anyway?</span>
                              {:else}
                                   <Save size={12} />
                                   <span>Save</span>
                              {/if}
                         </button>
                    {:else}
                         <span class="px-2 py-1 text-[11px] text-green-500"
                              >Saved</span
                         >
                    {/if}
               </div>
          </div>

          <div class="relative flex-1 min-h-0">
               <!-- Inner scroller fills the relative parent. CodeMirror lives inside
 it and handles its own scrolling. Splitting into two divs keeps
 the button overlay (absolute to the relative parent) pinned at
 the top-right of the visible area while content scrolls. -->
               <div class="absolute inset-0 overflow-auto">
                    {#if isHexMode}
                         <HexViewer data={hexData} />
                    {:else}
                         <AppCodeMirror
                              value={activeTab.content}
                              onchange={handleChange}
                              onready={(view: EditorView) => {
                                   cmView = view;
                              }}
                              onreconfigure={(view: EditorView) => {
                                   cmView = view;
                              }}
                              lang={languageExtension}
                              extensions={lintExtensions}
                         />
                    {/if}
               </div>

               <!-- Floating copy button: stays at the same low opacity at all times
 and only becomes fully visible when the mouse is directly over
 the button itself. Sibling of the scroller, so it never moves
 with scroll. z-index keeps it above CodeMirror's scrollbar. -->
               {#if !isHexMode}
                    <button
                         class="absolute top-2 right-3 z-20 flex items-center gap-1 px-2 py-1 bg-[#252526]/70 border border-[#3c3c3c] rounded text-[11px] text-gray-300 cursor-pointer opacity-50 hover:opacity-100 hover:bg-[#333] hover:text-gray-100 transition-opacity duration-150"
                         onclick={handleCopy}
                         title="Copy editor content"
                         aria-label="Copy editor content"
                    >
                         {#if copied}
                              <Check size={12} class="text-green-400" />
                              <span>Copied</span>
                         {:else}
                              <Copy size={12} />
                              <span>Copy</span>
                         {/if}
                    </button>
               {/if}
          </div>
     {:else}
          <div
               class="flex items-center justify-center h-full bg-slate-50 dark:bg-warm-900"
          >
               <div class="text-center text-gray-400 dark:text-slate-500">
                    <h3
                         class="text-base font-medium mb-1 text-gray-500 dark:text-slate-400"
                    >
                         No file open
                    </h3>
                    <p class="text-[13px]">
                         Select a file from the explorer to start editing
                    </p>
               </div>
          </div>
     {/if}
</div>
