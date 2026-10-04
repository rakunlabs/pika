<!--
  HookTargetForm — add/edit form for a single hook target. Hosts the type
  picker, the per-type field component, and the shared body template.
  All per-type editors stay mounted (only the active one is visible) so
  switching type back and forth doesn't discard what was typed.
-->
<script lang="ts">
  import { untrack } from "svelte";
  import type { HookTarget } from "@/lib/types/config";
  import { inputClass } from "@/lib/ui";
  import HttpTargetFields from "./HttpTargetFields.svelte";
  import KafkaTargetFields from "./KafkaTargetFields.svelte";
  import RedisTargetFields from "./RedisTargetFields.svelte";
  import NatsTargetFields from "./NatsTargetFields.svelte";
  import LogTargetFields from "./LogTargetFields.svelte";
  import {
    codeClass,
    labelClass,
    targetTypeSelected,
    type HookTargetType,
    type TargetFieldsApi,
  } from "./shared";

  type Props = {
    /** Existing target when editing; undefined when adding. */
    initial?: HookTarget;
    onSave: (target: HookTarget) => void;
    onCancel: () => void;
  };

  let { initial, onSave, onCancel }: Props = $props();

  const init = untrack(() => initial);
  let targetType = $state<HookTargetType>(
    (init?.type as HookTargetType) ?? "http",
  );
  let bodyTemplate = $state(init?.body_template || "");

  let editors = $state<Partial<Record<HookTargetType, TargetFieldsApi>>>({});

  const types: { id: HookTargetType; label: string }[] = [
    { id: "http", label: "HTTP Webhook" },
    { id: "kafka", label: "Kafka" },
    { id: "redis", label: "Redis" },
    { id: "nats", label: "NATS" },
    { id: "log", label: "Log" },
  ];

  function handleSave() {
    const target = editors[targetType]?.build(bodyTemplate || undefined);
    if (target) onSave(target);
  }

  const bodyFields = [
    ".Type",
    ".Mount",
    ".Path",
    ".Size",
    ".Protocol",
    ".User",
    ".Timestamp",
  ];
</script>

<div
  class="p-4 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md mb-3"
>
  <h4 class="text-xs font-semibold text-slate-600 dark:text-slate-300 mb-3">
    {init ? "Edit Target" : "Add Target"}
  </h4>

  <!-- Target type -->
  <div class="mb-3" role="group" aria-labelledby="hook-target-type-label">
    <span id="hook-target-type-label" class="{labelClass} mb-1.5">Type</span>
    <div class="flex gap-2">
      {#each types as t (t.id)}
        <button
          type="button"
          aria-pressed={targetType === t.id}
          class="flex-1 px-3 py-2 text-xs font-medium rounded-md border transition-colors cursor-pointer {targetType ===
          t.id
            ? targetTypeSelected[t.id]
            : 'bg-white dark:bg-warm-800 border-slate-200 dark:border-warm-700 text-slate-500 dark:text-slate-400 hover:bg-slate-50 dark:hover:bg-warm-700'}"
          onclick={() => (targetType = t.id)}>{t.label}</button
        >
      {/each}
    </div>
  </div>

  <div hidden={targetType !== "http"}>
    <HttpTargetFields bind:this={editors.http} initial={init?.http} />
  </div>
  <div hidden={targetType !== "kafka"}>
    <KafkaTargetFields bind:this={editors.kafka} initial={init?.kafka} />
  </div>
  <div hidden={targetType !== "redis"}>
    <RedisTargetFields bind:this={editors.redis} initial={init?.redis} />
  </div>
  <div hidden={targetType !== "nats"}>
    <NatsTargetFields bind:this={editors.nats} initial={init?.nats} />
  </div>
  <div hidden={targetType !== "log"}>
    <LogTargetFields bind:this={editors.log} initial={init?.log} />
  </div>

  <!-- Body Template (shared) — not applicable to the log target,
       which renders its own Message and Fields directly from the Event. -->
  {#if targetType !== "log"}
    <div class="mb-3">
      <label for="target-body-template" class={labelClass}
        >Body Template (optional)</label
      >
      <textarea
        id="target-body-template"
        bind:value={bodyTemplate}
        rows={3}
        placeholder={'{"text": "File {{.Path}} was {{.Type}} on {{.Mount}}"}'}
        class="{inputClass.replace('text-sm', 'text-xs')} font-mono resize-y"
      ></textarea>
      <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">
        Go text/template. Available fields:
        {#each bodyFields as f, i}<code class={codeClass}>{f}</code
          >{i < bodyFields.length - 1 ? ", " : ""}{/each}. Leave empty for
        default JSON payload.
      </p>
    </div>
  {/if}

  <div class="flex justify-end gap-2">
    <button
      type="button"
      class="px-3 py-1.5 text-xs text-slate-600 dark:text-slate-300 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-md hover:bg-slate-50 dark:hover:bg-warm-700 transition-colors cursor-pointer"
      onclick={onCancel}>Cancel</button
    >
    <button
      type="button"
      class="px-3 py-1.5 text-xs text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
      onclick={handleSave}>{init ? "Save Target" : "Add Target"}</button
    >
  </div>
</div>
