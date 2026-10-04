<script lang="ts">
  import { untrack } from "svelte";
  import { Trash2 } from "lucide-svelte";
  import type { HookTarget, LogTarget } from "@/lib/types/config";
  import { inputClass, inputSmClass, selectClass } from "@/lib/ui";
  import {
    codeClass,
    labelClass,
    pairsToRecord,
    recordToPairs,
    removeIconBtnClass,
    type KeyValue,
  } from "./shared";

  type Level = "debug" | "info" | "warn" | "error";
  type Props = { initial?: HookTarget["log"] };
  let { initial }: Props = $props();

  const init = untrack(() => initial);
  let level = $state<Level>((init?.level as Level) || "info");
  let message = $state(init?.message || "");
  let fields = $state<KeyValue[]>(recordToPairs(init?.fields));

  // Body Template is intentionally omitted: the log sink renders its own
  // message and fields from the Event and ignores any rendered payload.
  export function build(_bodyTemplate: string | undefined): HookTarget | null {
    const rec = pairsToRecord(fields);
    const log: LogTarget = {
      level,
      message: message || undefined,
      fields: Object.keys(rec).length > 0 ? rec : undefined,
    };
    return { type: "log", log };
  }
</script>

<div class="grid grid-cols-4 gap-3 mb-3">
  <div>
    <label for="target-log-level" class={labelClass}>Level</label>
    <select id="target-log-level" bind:value={level} class={selectClass}>
      <option value="debug">debug</option>
      <option value="info">info</option>
      <option value="warn">warn</option>
      <option value="error">error</option>
    </select>
  </div>
  <div class="col-span-3">
    <label for="target-log-message" class={labelClass}
      >Message Template (optional)</label
    >
    <input
      id="target-log-message"
      type="text"
      bind:value={message}
      placeholder={"file {{.Path}} {{.Type}} on {{.Mount}}"}
      class="{inputClass} font-mono"
    />
  </div>
</div>
<p class="mb-3 text-[11px] text-slate-400 dark:text-slate-500">
  Default message (when empty) is the event type, e.g. <code class={codeClass}
    >file.created</code
  >.
</p>

<!-- Log Fields -->
<div class="mb-3" role="group" aria-labelledby="target-log-fields-label">
  <div class="flex items-center justify-between mb-1">
    <span
      id="target-log-fields-label"
      class="block text-xs font-medium text-slate-500 dark:text-slate-400"
      >Fields</span
    >
    <button
      type="button"
      class="text-[10px] text-accent-600 hover:text-accent-700 dark:text-accent-400 dark:hover:text-accent-300 cursor-pointer"
      onclick={() => (fields = [...fields, { key: "", value: "" }])}
      >+ Add Field</button
    >
  </div>
  {#each fields as field, i}
    <div class="flex gap-2 mb-1.5">
      <input
        type="text"
        bind:value={field.key}
        placeholder="attr name"
        aria-label="Field name"
        class="{inputSmClass} flex-1 font-mono"
      />
      <input
        type="text"
        bind:value={field.value}
        placeholder={"{{.Mount}}/{{.Path}}"}
        aria-label="Field value template"
        class="{inputSmClass} flex-1 font-mono"
      />
      <button
        type="button"
        class={removeIconBtnClass}
        onclick={() => (fields = fields.filter((_, j) => j !== i))}
        title="Remove"
        aria-label="Remove field"
      >
        <Trash2 size={12} />
      </button>
    </div>
  {/each}
  <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">
    Each value is a Go template rendered against the Event. Rendered as slog
    attributes alongside <code class={codeClass}>hook</code>.
  </p>
</div>
