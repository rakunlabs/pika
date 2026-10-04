<!--
  PermissionPatternEditor — capability picker plus the optional per-
  capability path-scoping editor. Shared by the "Create permission" form
  and the "Edit permission" modal (they used to be two copy-pasted
  blocks driven by addPattern(target: "new" | "edit", ...)).
-->
<script lang="ts">
  import { Check, Trash2 } from "lucide-svelte";
  import type { KnownCapability } from "./userQuery.svelte";

  type Props = {
    knownKeys: KnownCapability[];
    /** Selected capability keys. */
    keys: string[];
    /** Per-capability glob patterns. */
    patterns: Record<string, string[]>;
    /** "create" shows descriptions; "edit" uses a compact, scrollable layout. */
    variant?: "create" | "edit";
  };

  let {
    knownKeys,
    keys = $bindable(),
    patterns = $bindable(),
    variant = "create",
  }: Props = $props();

  let showHelp = $state(false);
  const compact = $derived(variant === "edit");

  function toggleKey(key: string) {
    if (keys.includes(key)) {
      keys = keys.filter((k) => k !== key);
      // Drop any patterns staged for this key — they're meaningless without
      // the cap selected, and re-adding the cap should start fresh.
      if (patterns[key]) {
        const { [key]: _, ...rest } = patterns;
        patterns = rest;
      }
    } else {
      keys = [...keys, key];
    }
  }

  // Pattern editors mutate a Record<string, string[]>. Reassigning the
  // outer object on every change keeps Svelte 5 reactivity happy.
  function addPattern(key: string) {
    patterns = { ...patterns, [key]: [...(patterns[key] ?? []), ""] };
  }

  function updatePattern(key: string, idx: number, value: string) {
    const arr = [...(patterns[key] ?? [])];
    arr[idx] = value;
    patterns = { ...patterns, [key]: arr };
  }

  function removePattern(key: string, idx: number) {
    const arr = (patterns[key] ?? []).filter((_, i) => i !== idx);
    if (arr.length === 0) {
      const { [key]: _drop, ...rest } = patterns;
      patterns = rest;
    } else {
      patterns = { ...patterns, [key]: arr };
    }
  }
</script>

<!-- Capability keys selector -->
<div class={compact ? "" : "mb-3"}>
  <div class="text-xs text-slate-500 dark:text-slate-400 mb-2">
    Capabilities granted:
  </div>
  <div
    class="grid grid-cols-2 gap-1.5 {compact ? 'max-h-56 overflow-y-auto' : ''}"
  >
    {#each knownKeys as known (known.key)}
      {@const selected = keys.includes(known.key)}
      <button
        onclick={() => toggleKey(known.key)}
        type="button"
        aria-pressed={selected}
        class="flex items-start gap-2 px-2.5 py-2 border rounded-md transition-all text-left text-xs cursor-pointer
 {selected
          ? 'bg-accent-50 border-accent-300 dark:bg-accent-900/40 dark:border-accent-700'
          : 'bg-white dark:bg-warm-900 border-slate-200 dark:border-warm-700 hover:border-slate-300 dark:hover:border-warm-600'}"
      >
        <div
          class="flex items-center justify-center w-3.5 h-3.5 mt-0.5 shrink-0 rounded border transition-colors
 {selected
            ? 'bg-accent-500 border-accent-500'
            : 'border-slate-300 dark:border-warm-600'}"
        >
          {#if selected}<Check size={9} class="text-white" />{/if}
        </div>
        <div class="min-w-0">
          <code
            class="font-medium {selected
              ? 'text-accent-700 dark:text-accent-300'
              : 'text-slate-600 dark:text-slate-300'}">{known.key}</code
          >
          {#if !compact}
            <div
              class="text-[10px] text-slate-400 dark:text-slate-500 mt-0.5 leading-snug"
            >
              {known.description}
            </div>
          {/if}
        </div>
      </button>
    {/each}
  </div>
</div>

<!-- Path scoping (optional). Renders one block per selected cap.
     Empty list = unrestricted, identical to the prior behavior. -->
{#if keys.length > 0}
  <div
    class="{compact ? '' : 'mb-3 '}border-t border-slate-200 dark:border-warm-700 pt-3"
  >
    <div class="flex items-baseline justify-between mb-1">
      <div class="text-xs font-medium text-slate-600 dark:text-slate-300">
        Path scoping <span class="text-slate-400 dark:text-slate-500 font-normal"
          >(optional)</span
        >
      </div>
      <button
        type="button"
        onclick={() => {
          showHelp = !showHelp;
        }}
        aria-expanded={showHelp}
        class="text-[10px] text-accent-600 dark:text-accent-400 hover:underline cursor-pointer"
      >
        {showHelp ? "Hide help" : "How do patterns work?"}
      </button>
    </div>

    {#if showHelp}
      <div
        class="mb-2 p-2.5 bg-blue-50 border border-blue-300 dark:bg-blue-950/30 dark:border-blue-700 rounded text-[11px] text-slate-700 dark:text-slate-200 space-y-1.5 leading-relaxed"
      >
        <div>
          Patterns match the <strong>storage key</strong> — the part after
          <code>/api/v1/file/</code> or
          <code>/api/v1/folder/</code>. No leading slash. No implicit prefix: a
          file stored as
          <code>team-a/app.yaml</code>
          is matched as <code>team-a/app.yaml</code>. Restrictions only apply
          to the <code>files.*</code> capabilities; admin caps (users, tokens,
          settings) ignore patterns.
        </div>
        <div>
          <span class="font-medium">Glob syntax:</span>
          <code>*</code> = one path segment ·
          <code>**</code> = any number of segments (including zero) ·
          <code>?</code> = one character ·
          <code>[abc]</code> = character class.
        </div>
        <div>
          <span class="font-medium">Examples:</span>
          <ul class="ml-4 list-disc space-y-0.5 mt-1">
            <li>
              <code>team-a/**</code> — anything under
              <code>team-a/</code> (any depth)
            </li>
            <li>
              <code>**/*.yaml</code> — every yaml file at any depth
            </li>
            <li>
              <code>apps/*/config.yaml</code> —
              <code>config.yaml</code>
              in any direct child of <code>apps/</code>
            </li>
            <li>
              <code>shared</code> — only the literal name
              <code>shared</code>, no descendants
            </li>
            <li>
              <code>prod/**</code> + <code>staging/**</code> — multiple patterns
              are OR'd
            </li>
          </ul>
        </div>
        <div class="text-slate-500 dark:text-slate-400">
          Empty list = unrestricted (default). For folder listings, parent
          directories of a matched path are allowed automatically so users can
          navigate.
          <code>..</code> segments are rejected.
        </div>
      </div>
    {/if}

    <div class="space-y-2 {compact ? 'max-h-56 overflow-y-auto' : ''}">
      {#each keys as k (k)}
        {@const pats = patterns[k] ?? []}
        {@const scopable = k.startsWith("files.")}
        <div
          class="border border-slate-200 dark:border-warm-700 rounded-md p-2 bg-slate-50 dark:bg-warm-900"
        >
          <div class="flex items-center justify-between mb-1.5">
            <div class="flex items-center gap-2">
              <code
                class="text-[11px] font-medium text-slate-700 dark:text-slate-200"
                >{k}</code
              >
              {#if !scopable}
                <span class="text-[9px] text-slate-400 dark:text-slate-500"
                  >(patterns ignored — not a path-bound capability)</span
                >
              {/if}
            </div>
            {#if scopable}
              <button
                type="button"
                onclick={() => addPattern(k)}
                class="text-[10px] text-accent-600 hover:text-accent-700 dark:text-accent-400 dark:hover:text-accent-300 hover:underline cursor-pointer"
                >+ Add pattern</button
              >
            {/if}
          </div>
          {#if !scopable}
            <div class="text-[10px] text-slate-400 dark:text-slate-500 italic">
              applies globally
            </div>
          {:else if pats.length === 0}
            <div class="text-[10px] text-slate-400 dark:text-slate-500 italic">
              all paths
            </div>
          {:else}
            <div class="space-y-1">
              {#each pats as pat, i}
                <div class="flex gap-1 items-center">
                  <input
                    type="text"
                    value={pat}
                    oninput={(e) => updatePattern(k, i, e.currentTarget.value)}
                    placeholder="e.g. team-a/**"
                    aria-label="Path pattern for {k}"
                    class="flex-1 px-2 py-1 border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 rounded text-[11px] font-mono focus:outline-none focus:ring-1 focus:ring-accent-500"
                  />
                  <button
                    type="button"
                    onclick={() => removePattern(k, i)}
                    class="p-1 text-slate-400 dark:text-slate-500 hover:text-vermilion-500 hover:bg-vermilion-50 dark:hover:bg-vermilion-900/40 rounded transition-colors cursor-pointer"
                    title="Remove pattern"
                    aria-label="Remove pattern"
                  >
                    <Trash2 size={12} />
                  </button>
                </div>
              {/each}
            </div>
          {/if}
        </div>
      {/each}
    </div>
  </div>
{/if}
