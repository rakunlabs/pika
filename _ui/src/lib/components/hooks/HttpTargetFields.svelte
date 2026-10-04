<script lang="ts">
  import { untrack } from "svelte";
  import { Trash2 } from "lucide-svelte";
  import type { HookTarget } from "@/lib/types/config";
  import { addToast } from "@/lib/store/toast.svelte";
  import { inputClass, inputSmClass, selectClass } from "@/lib/ui";
  import {
    labelClass,
    pairsToRecord,
    recordToPairs,
    removeIconBtnClass,
    type KeyValue,
  } from "./shared";

  type Props = { initial?: HookTarget["http"] };
  let { initial }: Props = $props();

  const init = untrack(() => initial);
  let url = $state(init?.url ?? "");
  let method = $state(init?.method || "POST");
  let headers = $state<KeyValue[]>(recordToPairs(init?.headers));
  let timeout = $state(init?.timeout || "30s");

  export function build(bodyTemplate: string | undefined): HookTarget | null {
    if (!url.trim()) {
      addToast("URL is required", "alert");
      return null;
    }
    return {
      type: "http",
      http: {
        url: url.trim(),
        method: method || "POST",
        headers: headers.length > 0 ? pairsToRecord(headers) : undefined,
        timeout: timeout || undefined,
      },
      body_template: bodyTemplate,
    };
  }
</script>

<div class="grid grid-cols-4 gap-3 mb-3">
  <div>
    <label for="target-http-method" class={labelClass}>Method</label>
    <select id="target-http-method" bind:value={method} class={selectClass}>
      <option value="POST">POST</option>
      <option value="PUT">PUT</option>
      <option value="PATCH">PATCH</option>
    </select>
  </div>
  <div class="col-span-3">
    <label for="target-http-url" class={labelClass}>URL</label>
    <input
      id="target-http-url"
      type="text"
      bind:value={url}
      placeholder="https://example.com/webhook"
      class="{inputClass} font-mono"
    />
  </div>
</div>
<div class="mb-3">
  <label for="target-http-timeout" class={labelClass}>Timeout</label>
  <input
    id="target-http-timeout"
    type="text"
    bind:value={timeout}
    placeholder="30s"
    class={inputClass.replace("w-full", "w-32")}
  />
</div>

<!-- HTTP Headers -->
<div class="mb-3" role="group" aria-labelledby="target-http-headers-label">
  <div class="flex items-center justify-between mb-1">
    <span
      id="target-http-headers-label"
      class="block text-xs font-medium text-slate-500 dark:text-slate-400"
      >Headers</span
    >
    <button
      type="button"
      class="text-[10px] text-accent-600 hover:text-accent-700 dark:text-accent-400 dark:hover:text-accent-300 cursor-pointer"
      onclick={() => (headers = [...headers, { key: "", value: "" }])}
      >+ Add Header</button
    >
  </div>
  {#each headers as header, i}
    <div class="flex gap-2 mb-1.5">
      <input
        type="text"
        bind:value={header.key}
        placeholder="Header name"
        aria-label="Header name"
        class="{inputSmClass} flex-1 font-mono"
      />
      <input
        type="text"
        bind:value={header.value}
        placeholder="Value"
        aria-label="Header value"
        class="{inputSmClass} flex-1 font-mono"
      />
      <button
        type="button"
        class={removeIconBtnClass}
        onclick={() => (headers = headers.filter((_, j) => j !== i))}
        title="Remove"
        aria-label="Remove header"
      >
        <Trash2 size={12} />
      </button>
    </div>
  {/each}
</div>
