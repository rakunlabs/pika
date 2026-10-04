<!--
  ActionEditor — one "then"/"and" action row inside a request rule.
  Simple fields are bound directly; type changes and capture-transform
  edits go through callbacks so the parent can rebuild the action list.
-->
<script lang="ts">
  import { Plus, Trash2 } from "lucide-svelte";
  import type { RequestActionType } from "@/lib/types/config";
  import {
    removeBtnClass,
    ruleInputClass,
    type ActionDraft,
    type CaptureTransformDraft,
  } from "./rules";

  type Props = {
    action: ActionDraft;
    first: boolean;
    canRemove: boolean;
    onTypeChange: (t: RequestActionType) => void;
    onTransformsChange: (transforms: CaptureTransformDraft[]) => void;
    onRemove: () => void;
  };

  let {
    action = $bindable(),
    first,
    canRemove,
    onTypeChange,
    onTransformsChange,
    onRemove,
  }: Props = $props();

  function addTransform() {
    onTransformsChange([
      ...(action.capture_transforms ?? []),
      { capture: "1", find: "/", value: "-" },
    ]);
  }

  function updateTransform(
    idx: number,
    field: keyof CaptureTransformDraft,
    value: string,
  ) {
    onTransformsChange(
      (action.capture_transforms ?? []).map((tr, i) =>
        i === idx ? { ...tr, [field]: value } : tr,
      ),
    );
  }

  function removeTransform(idx: number) {
    onTransformsChange(
      (action.capture_transforms ?? []).filter((_, i) => i !== idx),
    );
  }

  const mono = `${ruleInputClass} font-mono`;
</script>

<div class="grid grid-cols-[auto_1fr_1fr_auto] gap-2 items-center text-xs">
  <span class="text-slate-500 dark:text-slate-400 font-mono">
    {first ? "then" : "and"}
  </span>
  <select
    value={action.type}
    aria-label="Action type"
    onchange={(e) =>
      onTypeChange(e.currentTarget.value as RequestActionType)}
    class={ruleInputClass}
  >
    <option value="allow">allow (stop)</option>
    <option value="block">block (stop)</option>
    <option value="set_header">set header</option>
    <option value="del_header">delete header</option>
    <option value="set_query">set query param</option>
    <option value="del_query">delete query param</option>
    <option value="set_path">set path (literal)</option>
    <option value="replace_path">regex replace path</option>
  </select>
  <div class="flex gap-2">
    {#if action.type === "block"}
      <input
        type="number"
        min="100"
        max="599"
        bind:value={action.status}
        placeholder="403"
        aria-label="Block status code"
        class="w-20 {ruleInputClass}"
      />
      <input
        type="text"
        bind:value={action.body}
        placeholder="response body"
        aria-label="Block response body"
        class="flex-1 {ruleInputClass}"
      />
    {:else if action.type === "set_header" || action.type === "set_query"}
      <input
        type="text"
        bind:value={action.name}
        placeholder={action.type === "set_header" ? "X-Tenant" : "variant"}
        aria-label="Name"
        class="flex-1 {mono}"
      />
      <input
        type="text"
        bind:value={action.value}
        placeholder="value"
        aria-label="Value"
        class="flex-1 {mono}"
      />
    {:else if action.type === "del_header" || action.type === "del_query"}
      <input
        type="text"
        bind:value={action.name}
        placeholder={action.type === "del_header" ? "Cookie" : "debug"}
        aria-label="Name"
        class="flex-1 {mono}"
      />
    {:else if action.type === "set_path"}
      <input
        type="text"
        bind:value={action.value}
        placeholder="/new/path"
        aria-label="New path"
        class="flex-1 {mono}"
      />
    {:else if action.type === "replace_path"}
      <div class="flex-1 space-y-1.5">
        <div class="flex gap-2">
          <input
            type="text"
            bind:value={action.pattern}
            placeholder="^/legacy/(.*)$"
            aria-label="Path regex"
            class="flex-1 {mono}"
          />
          <input
            type="text"
            bind:value={action.value}
            placeholder="/$1"
            aria-label="Replacement"
            class="flex-1 {mono}"
          />
        </div>
        <div
          class="space-y-1 rounded border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-900 p-1.5"
        >
          <div class="flex items-center justify-between gap-2">
            <span
              class="text-[10px] uppercase tracking-wider text-slate-500 dark:text-slate-400"
            >
              Capture transforms
            </span>
            <button
              type="button"
              onclick={addTransform}
              class="px-1.5 py-0.5 text-[10px] rounded bg-slate-200 dark:bg-warm-700 hover:bg-slate-300 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-200 cursor-pointer inline-flex items-center gap-1"
            >
              <Plus size={10} /> add
            </button>
          </div>
          {#if action.capture_transforms?.length}
            {#each action.capture_transforms as transform, transformIdx}
              <div class="grid grid-cols-[4rem_1fr_1fr_auto] gap-1 items-center">
                <input
                  type="text"
                  value={transform.capture}
                  oninput={(e) =>
                    updateTransform(transformIdx, "capture", e.currentTarget.value)}
                  placeholder="1"
                  aria-label="Capture"
                  class={mono}
                />
                <input
                  type="text"
                  value={transform.find}
                  oninput={(e) =>
                    updateTransform(transformIdx, "find", e.currentTarget.value)}
                  placeholder="/"
                  aria-label="Find"
                  class={mono}
                />
                <input
                  type="text"
                  value={transform.value}
                  oninput={(e) =>
                    updateTransform(transformIdx, "value", e.currentTarget.value)}
                  placeholder="-"
                  aria-label="Replace with"
                  class={mono}
                />
                <button
                  type="button"
                  onclick={() => removeTransform(transformIdx)}
                  class={removeBtnClass}
                  title="Remove capture transform"
                  aria-label="Remove capture transform"
                >
                  <Trash2 size={12} />
                </button>
              </div>
            {/each}
          {:else}
            <p class="text-[10px] text-slate-500 dark:text-slate-400">
              Optional: pick a capture and replace literal text inside it
              before expanding the replacement.
            </p>
          {/if}
        </div>
      </div>
    {/if}
  </div>
  <button
    type="button"
    onclick={onRemove}
    disabled={!canRemove}
    class="{removeBtnClass} disabled:opacity-30 disabled:cursor-not-allowed"
    title="Remove action"
    aria-label="Remove action"
  >
    <Trash2 size={12} />
  </button>
</div>
