<script lang="ts">
  // InheritDialog — modal editor for a single inheritance entry.
  //
  // Why a modal: the inline form inside SettingsPanel grew tall (source type
  // toggle + source field + optional path field + suggestions + paths filter
  // + inject target = 6+ fields) and pushed the actual entry list off-screen.
  // A dialog gives the form room to breathe, keeps the entry list visible on
  // close, and matches the pattern already used by CreateDialog and
  // NewItemDialog elsewhere in the app.
  //
  // The parent (SettingsPanel) owns the inherits[] array and the active
  // tab — this component is purely a controlled editor. It calls onSubmit
  // with the final entry; the parent decides whether to push or replace.

  import {
    X,
    GitBranch,
    Eye,
    Loader2,
    AlertTriangle,
    CheckCircle2,
  } from "lucide-svelte";
  import type { InheritEntry } from "@/lib/types/config";
  import { configStore } from "@/lib/store/config.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import yaml from "js-yaml";
  import { parse as parseToml } from "smol-toml";

  interface Props {
    isOpen: boolean;
    // null = creating a new entry, number = editing existing at that index.
    // We only use it to flip the title and submit-button label; the parent
    // still owns the actual array splice.
    editIndex: number | null;
    initialEntry: InheritEntry | null;
    externalResources: string[];
    onSubmit: (entry: InheritEntry) => void;
    onClose: () => void;
  }

  let {
    isOpen,
    editIndex,
    initialEntry,
    externalResources,
    onSubmit,
    onClose,
  }: Props = $props();

  // ── Form state ─────────────────────────────────────────────────────
  // Mirrors what used to live inline in SettingsPanel; reset whenever
  // the dialog opens so reopening for a fresh add never leaks stale
  // values from a previous edit session.
  let sourceType = $state<"internal" | "external">("internal");
  let sourceField = $state("");
  let resourceField = $state("");
  let pathField = $state("");
  let pathsField = $state("");
  let injectField = $state("");
  // '' = auto (omit on submit, backend keeps current behaviour). Only
  // exposed for external; for internal entries the source file's
  // own meta.format already governs decoding.
  let formatField = $state<"" | "json" | "yaml" | "toml">("");
  let externalPathSuggestions = $state<string[]>([]);
  let loadingPaths = $state(false);

  // ── Preview state ──────────────────────────────────────────────────
  // User-driven peek at what the external backend actually returns for
  // the configured (resource, path). Intentionally manual (no auto-fetch
  // on field changes) because (a) reads can be slow and (b) every fetch
  // hits external.read permission + the upstream backend's quota.
  //
  // We capture three things on a successful peek:
  //   - raw object: whatever readExternalEntry returned (data field).
  //     This is what the merge pipeline will see *before* any decode.
  //   - isWrapped:  did the provider wrap the body as {"value":"<str>"}?
  //                 That's the cue to set "Decode As".
  //   - decoded:    if isWrapped and the user picked a format, the
  //                 client-side decode of the inner string. Identical
  //                 to what the server will do at render time, so the
  //                 user can verify their format choice without a full
  //                 Render round-trip.
  let previewLoading = $state(false);
  let previewError = $state<string | null>(null);
  let previewRaw = $state<unknown>(null);
  let previewWrappedString = $state<string | null>(null); // non-null = wrapper detected
  let previewDecoded = $state<unknown>(null);
  let previewDecodeError = $state<string | null>(null);
  let previewHasRun = $state(false);

  // Re-decode whenever the user changes the Decode As selector after a
  // preview — they can flip between formats and watch the result update
  // without re-hitting the backend. The reactivity is driven by the
  // $effect below.
  $effect(() => {
    if (!previewHasRun || previewWrappedString === null) {
      previewDecoded = null;
      previewDecodeError = null;
      return;
    }
    if (!formatField) {
      previewDecoded = null;
      previewDecodeError = null;
      return;
    }
    try {
      const raw = previewWrappedString;
      if (formatField === "json") {
        previewDecoded = JSON.parse(raw);
      } else if (formatField === "yaml") {
        previewDecoded = yaml.load(raw);
      } else if (formatField === "toml") {
        previewDecoded = parseToml(raw);
      }
      previewDecodeError = null;
    } catch (e: unknown) {
      previewDecoded = null;
      previewDecodeError = e instanceof Error ? e.message : String(e);
    }
  });

  // True when readExternalEntry's data shape exactly matches the
  // synthetic {"value": "<string>"} wrapper produced by Consul / etcd /
  // GCP / HTTP for plain-string secrets. Mirrors the server's
  // decodeWrappedValue contract so the UI never disagrees with the
  // backend about whether decoding will engage.
  function detectWrapper(obj: unknown): string | null {
    if (!obj || typeof obj !== "object" || Array.isArray(obj)) return null;
    const rec = obj as Record<string, unknown>;
    const keys = Object.keys(rec);
    if (keys.length !== 1 || keys[0] !== "value") return null;
    const v = rec.value;
    return typeof v === "string" ? v : null;
  }

  async function runPreview() {
    if (sourceType !== "external") return;
    if (!resourceField || !pathField.trim()) {
      addToast("Pick a resource and path before previewing", "alert");
      return;
    }
    previewLoading = true;
    previewError = null;
    previewRaw = null;
    previewWrappedString = null;
    previewHasRun = false;
    try {
      const entry = await configStore.readExternalEntry(
        resourceField,
        pathField.trim(),
      );
      previewRaw = entry.data ?? null;
      previewWrappedString = detectWrapper(entry.data);
      previewHasRun = true;
    } catch (e: unknown) {
      // Surface the server message verbatim — most useful failure modes
      // here (403, 404, upstream timeout) carry an explanation in the
      // body that's worth showing instead of a generic "preview failed".
      // axios.AxiosError typing isn't worth pulling in for one field.
      const errObj = e as {
        response?: { data?: { message?: string } };
        message?: string;
      };
      previewError =
        errObj?.response?.data?.message || errObj?.message || "Preview failed";
    } finally {
      previewLoading = false;
    }
  }

  function clearPreview() {
    previewHasRun = false;
    previewRaw = null;
    previewWrappedString = null;
    previewDecoded = null;
    previewDecodeError = null;
    previewError = null;
  }

  // Hydrate from initialEntry every time the dialog flips open. We key
  // off isOpen (not just initialEntry) so reopening with the same edit
  // target still resets transient state like suggestions.
  $effect(() => {
    if (!isOpen) return;
    const e = initialEntry;
    if (e?.resource) {
      sourceType = "external";
    } else {
      sourceType = "internal";
    }
    sourceField = e?.source || "";
    resourceField = e?.resource || "";
    pathField = e?.path || "";
    pathsField = e?.paths ? e.paths.join(", ") : "";
    injectField = e?.inject || "";
    formatField = (e?.format ?? "") as "" | "json" | "yaml" | "toml";
    externalPathSuggestions = [];
    loadingPaths = false;
    clearPreview();
  });

  // Switching source type wipes the other type's fields so we don't ship
  // a half-populated entry (e.g. resource set on an internal entry).
  function switchType(t: "internal" | "external") {
    sourceType = t;
    sourceField = "";
    resourceField = "";
    pathField = "";
    // Format is only meaningful for external. Switching to
    // internal drops the value rather than carrying it silently.
    if (t === "internal") formatField = "";
    externalPathSuggestions = [];
    clearPreview();
  }

  async function loadExternalPaths(resourceName: string, prefix: string = "") {
    if (!resourceName) {
      externalPathSuggestions = [];
      return;
    }
    loadingPaths = true;
    try {
      externalPathSuggestions = await configStore.listExternalPaths(
        resourceName,
        prefix,
      );
    } catch {
      externalPathSuggestions = [];
    } finally {
      loadingPaths = false;
    }
  }

  function buildEntry(): InheritEntry | null {
    const entry: InheritEntry = {};
    if (sourceType === "internal") {
      if (!sourceField.trim()) {
        addToast("Source path is required", "alert");
        return null;
      }
      entry.source = sourceField.trim();
    } else {
      if (!resourceField) {
        addToast("External resource is required", "alert");
        return null;
      }
      entry.resource = resourceField;
      if (pathField.trim()) entry.path = pathField.trim();
    }

    const paths = pathsField
      .split(",")
      .map((p) => p.trim())
      .filter((p) => p.length > 0);
    if (paths.length > 0) entry.paths = paths;
    if (injectField.trim()) entry.inject = injectField.trim();
    // Format only travels with external/mount entries; the buildEntry
    // gate plus switchType reset above prevents an internal entry from
    // shipping a stale format value to the server.
    if (formatField && sourceType !== "internal") entry.format = formatField;
    return entry;
  }

  function handleSubmit(e: Event) {
    e.preventDefault();
    const entry = buildEntry();
    if (!entry) return;
    onSubmit(entry);
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (e.key === "Escape" && isOpen) onClose();
  }

  // Mirror CreateDialog's "only close if the mousedown started on the
  // backdrop too" pattern — prevents accidental closes when the user
  // drags a selection from inside the dialog and releases on the dim.
  let mouseDownTarget: EventTarget | null = null;
  function handleBackdropMouseDown(e: MouseEvent) {
    mouseDownTarget = e.target;
  }
  function handleBackdropClick(e: MouseEvent) {
    if (e.target === e.currentTarget && mouseDownTarget === e.currentTarget)
      onClose();
  }

  // Shared input chrome — kept in sync visually with SettingsPanel.
  const inputClass =
    "w-full px-2.5 py-2 text-[13px] font-mono border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-800 text-slate-700 dark:text-warm-100 placeholder:text-slate-400 dark:placeholder:text-warm-300 rounded focus:outline-none focus:border-accent-500 dark:focus:border-accent-500 focus:ring-2 focus:ring-accent-500/20";
  const selectClass = inputClass;
  const cardClass =
    "bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded";
</script>

<svelte:window onkeydown={handleKeyDown} />

{#if isOpen}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    class="fixed inset-0 bg-black/50 flex items-center justify-center z-[100] p-5"
    onmousedown={handleBackdropMouseDown}
    onclick={handleBackdropClick}
    onkeydown={(e) => e.key === "Escape" && onClose()}
    role="dialog"
    aria-modal="true"
    aria-labelledby="inherit-dialog-title"
    tabindex="-1"
  >
    <div
      class="bg-white dark:bg-warm-900 rounded-lg shadow-xl w-full max-w-[560px] max-h-[90vh] flex flex-col overflow-hidden"
    >
      <!-- Header -->
      <div
        class="flex items-center justify-between px-5 py-4 border-b border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900 shrink-0"
      >
        <div
          class="flex items-center gap-2.5 text-gray-700 dark:text-slate-200"
        >
          <GitBranch size={18} />
          <h2 id="inherit-dialog-title" class="text-base font-semibold m-0">
            {editIndex !== null ? "Edit Inheritance" : "Add Inheritance"}
          </h2>
        </div>
        <button
          class="flex items-center justify-center p-1.5 bg-transparent border-none rounded text-slate-500 dark:text-slate-400 cursor-pointer transition-all hover:bg-slate-200 dark:hover:bg-warm-700 hover:text-slate-800 dark:hover:text-slate-100"
          onclick={onClose}
          aria-label="Close"
        >
          <X size={18} />
        </button>
      </div>

      <form
        onsubmit={handleSubmit}
        class="flex flex-col flex-1 overflow-hidden"
      >
        <div class="p-5 overflow-y-auto flex-1 space-y-3">
          <!-- Source type toggle. Group is a <fieldset>+<legend> rather
               than a <label>, because the three buttons aren't a single
               form control and assistive tech needs to announce the
               grouping, not bind a label to a non-existent input. -->
          <fieldset>
            <legend
              class="block text-[11px] font-medium text-slate-500 dark:text-warm-300 mb-1.5 uppercase tracking-wide"
              >Source Type</legend
            >
            <div class="flex gap-1.5">
              <button
                type="button"
                class="flex-1 py-1.5 text-xs font-medium rounded transition-colors cursor-pointer
                {sourceType === 'internal'
                  ? 'bg-accent-100 text-accent-700 border border-accent-300 dark:bg-accent-900/40 dark:text-accent-200 dark:border-accent-700'
                  : 'bg-slate-50 dark:bg-warm-900 text-slate-500 dark:text-warm-300 border border-slate-200 dark:border-warm-700 hover:bg-slate-100 dark:hover:bg-warm-700'}"
                onclick={() => switchType("internal")}>Internal</button
              >
              <button
                type="button"
                class="flex-1 py-1.5 text-xs font-medium rounded transition-colors cursor-pointer
                {sourceType === 'external'
                  ? 'bg-accent-100 text-accent-700 border border-accent-300 dark:bg-accent-900/40 dark:text-accent-200 dark:border-accent-700'
                  : 'bg-slate-50 dark:bg-warm-900 text-slate-500 dark:text-warm-300 border border-slate-200 dark:border-warm-700 hover:bg-slate-100 dark:hover:bg-warm-700'}"
                onclick={() => switchType("external")}>External</button
              >
            </div>
          </fieldset>

          <!-- Source-specific fields -->
          {#if sourceType === "internal"}
            <label class="block">
              <span
                class="block text-[11px] font-medium text-slate-500 dark:text-warm-300 mb-1.5 uppercase tracking-wide"
                >Config Path</span
              >
              <input
                type="text"
                bind:value={sourceField}
                placeholder="base/database"
                class={inputClass}
              />
            </label>
          {:else}
            <label class="block">
              <span
                class="block text-[11px] font-medium text-slate-500 dark:text-warm-300 mb-1.5 uppercase tracking-wide"
                >External Resource</span
              >
              <select
                class={selectClass}
                bind:value={resourceField}
                onchange={() => {
                  pathField = "";
                  loadExternalPaths(resourceField);
                }}
              >
                <option value="">Select external resource</option>
                {#each externalResources as resource}
                  <option value={resource}>{resource}</option>
                {/each}
              </select>
            </label>
            {#if resourceField}
              <label class="block">
                <span
                  class="block text-[11px] font-medium text-slate-500 dark:text-warm-300 mb-1.5 uppercase tracking-wide"
                  >Path</span
                >
                <input
                  type="text"
                  bind:value={pathField}
                  placeholder="myapp/database"
                  class={inputClass}
                />
              </label>
              {#if loadingPaths}
                <div class="text-[11px] text-slate-400 dark:text-warm-400">
                  Loading paths...
                </div>
              {:else if externalPathSuggestions.length > 0}
                <div class="max-h-40 overflow-y-auto {cardClass}">
                  {#each externalPathSuggestions as suggestion}
                    {@const isDir = suggestion.endsWith("/")}
                    <button
                      type="button"
                      class="w-full text-left px-2.5 py-1.5 text-xs font-mono hover:bg-accent-50 dark:hover:bg-accent-900/30 transition-colors cursor-pointer border-b border-slate-100 dark:border-warm-700 last:border-b-0
                      {isDir
                        ? 'text-slate-500 dark:text-warm-300'
                        : 'text-accent-700 dark:text-accent-300'}"
                      onclick={() => {
                        if (isDir) {
                          pathField =
                            (pathField ? pathField.replace(/\/?$/, "/") : "") +
                            suggestion;
                          loadExternalPaths(resourceField, pathField);
                        } else {
                          const base = pathField.includes("/")
                            ? pathField.replace(/[^/]*$/, "")
                            : "";
                          pathField = base + suggestion;
                          externalPathSuggestions = [];
                        }
                      }}
                    >
                      {#if isDir}📁{:else}📄{/if}
                      {suggestion}
                    </button>
                  {/each}
                </div>
              {/if}
            {/if}
          {/if}

          <!-- Decoder hint + preview. Only meaningful when the backend
               stores a string blob (Consul/etcd/GCP/HTTP) — internal
               sources carry their own meta.format and won't read this
               field. -->`
          {#if sourceType !== "internal"}
            <div>
              <span
                class="block text-[11px] font-medium text-slate-500 dark:text-warm-300 mb-1.5 uppercase tracking-wide"
              >
                Decode As <span
                  class="text-slate-400 dark:text-warm-400 normal-case font-normal"
                  >(optional)</span
                >
              </span>
              <div class="flex gap-1.5">
                <select class="{selectClass} flex-1" bind:value={formatField}>
                  <option value="">Auto (use as-is)</option>
                  <option value="json">JSON</option>
                  <option value="yaml">YAML</option>
                  <option value="toml">TOML</option>
                </select>
                {#if sourceType === "external"}
                  <button
                    type="button"
                    class="flex items-center gap-1 px-2.5 py-2 text-[11px] font-medium text-white bg-accent-600 rounded cursor-pointer hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
                    onclick={runPreview}
                    disabled={previewLoading ||
                      !resourceField ||
                      !pathField.trim()}
                    title={!resourceField || !pathField.trim()
                      ? "Pick a resource and path first"
                      : "Read the entry and show what the backend returns"}
                  >
                    {#if previewLoading}
                      <Loader2 size={11} class="animate-spin" />
                    {:else}
                      <Eye size={11} />
                    {/if}
                    Preview
                  </button>
                {/if}
              </div>
              <span
                class="block mt-1 text-[10px] text-slate-400 dark:text-warm-400 leading-snug"
              >
                Use this when the secret value is a YAML / JSON / TOML document
                stored as a string (e.g. a Consul KV body). The backend will
                parse the string before merging.
              </span>

              <!-- Preview output. Three layered messages so the user can
                   reason about what the backend sees and what their
                   Decode As selection will actually do:
                     1. Raw payload (what the provider returned).
                     2. Wrapper detection notice + suggestion.
                     3. Live decoded preview (client-side parse with the
                        selected format). -->
              {#if previewError}
                <div
                  class="mt-2 p-2 rounded border border-red-300 dark:border-red-700 bg-red-50 dark:bg-red-950/40 text-[11px] text-red-700 dark:text-red-300 flex items-start gap-1.5"
                >
                  <AlertTriangle size={12} class="shrink-0 mt-0.5" />
                  <span>{previewError}</span>
                </div>
              {/if}

              {#if previewHasRun && !previewError}
                <!-- Wrapper detection line. The wording maps 1:1 to the
                     backend's decodeWrappedValue contract: a single
                     {"value": "<string>"} key is the trigger. -->
                {#if previewWrappedString !== null}
                  <div
                    class="mt-2 p-2 rounded border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-950/40 text-[11px] text-amber-800 dark:text-amber-200 flex items-start gap-1.5"
                  >
                    <AlertTriangle size={12} class="shrink-0 mt-0.5" />
                    <span>
                      The backend returned a <code
                        class="px-1 py-0.5 bg-amber-100 dark:bg-amber-900/40 rounded font-mono"
                        >{`{ "value": "..." }`}</code
                      >
                      wrapper — the entry is stored as a plain string. Pick a
                      <strong>Decode As</strong> format to parse it before merge.
                    </span>
                  </div>
                {:else}
                  <div
                    class="mt-2 p-2 rounded border border-emerald-300 dark:border-emerald-700 bg-emerald-50 dark:bg-emerald-950/40 text-[11px] text-emerald-800 dark:text-emerald-200 flex items-start gap-1.5"
                  >
                    <CheckCircle2 size={12} class="shrink-0 mt-0.5" />
                    <span
                      >Returned a structured object — will merge as-is. Decode
                      As is not needed.</span
                    >
                  </div>
                {/if}

                <!-- Raw payload, JSON-pretty. Capped height so a huge
                     blob doesn't push the dialog past the viewport. -->
                <details class="mt-2" open>
                  <summary
                    class="text-[10px] font-medium text-slate-500 dark:text-warm-300 uppercase tracking-wide cursor-pointer hover:text-slate-700 dark:hover:text-white"
                  >
                    Raw response
                  </summary>
                  <pre
                    class="mt-1 max-h-40 overflow-auto px-2 py-1.5 text-[10px] font-mono leading-snug rounded border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900 text-slate-700 dark:text-warm-100">{JSON.stringify(
                      previewRaw,
                      null,
                      2,
                    )}</pre>
                </details>

                <!-- Decoded preview. Only meaningful when wrapper +
                     format are both set; otherwise the raw response
                     above is already the final shape. -->
                {#if previewWrappedString !== null && formatField}
                  <details class="mt-2" open>
                    <summary
                      class="text-[10px] font-medium text-slate-500 dark:text-warm-300 uppercase tracking-wide cursor-pointer hover:text-slate-700 dark:hover:text-white"
                    >
                      Decoded as {formatField.toUpperCase()}
                    </summary>
                    {#if previewDecodeError}
                      <div
                        class="mt-1 p-2 rounded border border-red-300 dark:border-red-700 bg-red-50 dark:bg-red-950/40 text-[11px] text-red-700 dark:text-red-300 flex items-start gap-1.5"
                      >
                        <AlertTriangle size={12} class="shrink-0 mt-0.5" />
                        <span
                          >{formatField.toUpperCase()} parse failed: {previewDecodeError}</span
                        >
                      </div>
                    {:else}
                      <pre
                        class="mt-1 max-h-40 overflow-auto px-2 py-1.5 text-[10px] font-mono leading-snug rounded border border-emerald-300 dark:border-emerald-700 bg-emerald-50/40 dark:bg-emerald-950/20 text-slate-700 dark:text-warm-100">{JSON.stringify(
                          previewDecoded,
                          null,
                          2,
                        )}</pre>
                    {/if}
                  </details>
                {/if}
              {/if}
            </div>
          {/if}

          <!-- Include paths filter -->
          <label class="block">
            <span
              class="block text-[11px] font-medium text-slate-500 dark:text-warm-300 mb-1.5 uppercase tracking-wide"
              >Include Paths <span
                class="text-slate-400 dark:text-warm-400 normal-case font-normal"
                >(optional, comma-separated)</span
              ></span
            >
            <input
              type="text"
              bind:value={pathsField}
              placeholder="host, port, credentials.*"
              class={inputClass}
            />
            <span
              class="block mt-1 text-[10px] text-slate-400 dark:text-warm-400 leading-snug"
            >
              Leave empty to inherit the entire source. Filters are applied
              verbatim against the source's top-level keys after any Decode As
              step — list the names you actually want (e.g. <code
                class="font-mono">host</code
              >, <code class="font-mono">db.password</code>,
              <code class="font-mono">logging.*</code>). To pick a wrapper-style
              <code class="font-mono">value</code>
              key, write <code class="font-mono">value</code>.
            </span>
          </label>

          <!-- Inject target -->
          <label class="block">
            <span
              class="block text-[11px] font-medium text-slate-500 dark:text-warm-300 mb-1.5 uppercase tracking-wide"
              >Inject At <span
                class="text-slate-400 dark:text-warm-400 normal-case font-normal"
                >(optional)</span
              ></span
            >
            <input
              type="text"
              bind:value={injectField}
              placeholder="database.auth (empty = root)"
              class={inputClass}
            />
          </label>
        </div>

        <!-- Footer -->
        <div
          class="flex justify-end gap-2.5 px-5 py-4 border-t border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900 shrink-0"
        >
          <button
            type="button"
            class="px-4 py-2 bg-white dark:bg-warm-900 text-slate-600 dark:text-slate-300 border border-slate-200 dark:border-warm-700 rounded text-[13px] font-medium cursor-pointer transition-all hover:bg-slate-100 dark:hover:bg-warm-700"
            onclick={onClose}>Cancel</button
          >
          <button
            type="submit"
            class="px-4 py-2 bg-accent-600 text-white border-none rounded text-[13px] font-medium cursor-pointer transition-colors hover:bg-accent-700"
            >{editIndex !== null ? "Save" : "Add"}</button
          >
        </div>
      </form>
    </div>
  </div>
{/if}
