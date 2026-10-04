<!--
  RuleTester — sends the unsaved request-check rules to the backend
  evaluator and renders the resulting trace.
-->
<script lang="ts">
  import { Play, Plus, Trash2 } from "lucide-svelte";
  import { apiErrorMessage } from "@/lib/api/client";
  import { configStore } from "@/lib/store/config.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import type {
    RequestCheck,
    RequestRuleTestResult,
  } from "@/lib/types/config";
  import {
    actionTraceSummary,
    pathWithQuery,
    removeBtnClass,
    ruleInputClass,
    smallNeutralBtnClass,
    terminalLabel,
  } from "./rules";

  type Props = {
    requestCheck: RequestCheck | undefined;
    /** Cleared by the parent whenever the rules change. */
    result: RequestRuleTestResult | null;
  };

  let { requestCheck, result = $bindable() }: Props = $props();

  let method = $state("GET");
  let path = $state("/legacy/myapp/config");
  let headers = $state<{ name: string; value: string }[]>([]);
  let testing = $state(false);

  async function run() {
    testing = true;
    result = null;
    try {
      const hdrs: Record<string, string> = {};
      for (const h of headers) {
        const name = h.name.trim();
        if (name) hdrs[name] = h.value;
      }
      result = await configStore.testPublicEndpointRules({
        request_check: requestCheck ?? { rules: [] },
        method,
        path,
        headers: Object.keys(hdrs).length ? hdrs : undefined,
      });
    } catch (err) {
      addToast(apiErrorMessage(err, "Rule test failed"), "alert");
    } finally {
      testing = false;
    }
  }

  const mono = `${ruleInputClass} font-mono`;
</script>

<div
  class="p-3 rounded border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900 space-y-3"
>
  <div class="flex items-center justify-between gap-3 flex-wrap">
    <div>
      <h5 class="text-xs font-semibold text-slate-700 dark:text-slate-200">
        Test draft rules
      </h5>
      <p class="text-[11px] text-slate-500 dark:text-slate-400">
        Runs the unsaved rules through the backend evaluator and shows what the
        shim would see.
      </p>
    </div>
    <button
      type="button"
      onclick={run}
      disabled={testing}
      class="px-2 py-1 text-xs rounded bg-accent-600 hover:bg-accent-700 text-white cursor-pointer inline-flex items-center gap-1 disabled:opacity-50"
    >
      <Play size={12} />
      {testing ? "Testing…" : "Test rules"}
    </button>
  </div>

  <div class="grid grid-cols-[6rem_1fr] gap-2 text-xs">
    <select bind:value={method} aria-label="Test request method" class={mono}>
      <option value="GET">GET</option>
      <option value="POST">POST</option>
      <option value="PUT">PUT</option>
      <option value="PATCH">PATCH</option>
      <option value="DELETE">DELETE</option>
      <option value="HEAD">HEAD</option>
    </select>
    <input
      type="text"
      bind:value={path}
      placeholder="/legacy/myapp/config?variant=prod"
      aria-label="Test request path"
      class={mono}
    />
  </div>

  <div class="space-y-1.5">
    <div class="flex items-center gap-2">
      <span
        class="text-[11px] uppercase tracking-wider text-slate-500 dark:text-slate-400"
      >
        Headers
      </span>
      <button
        type="button"
        onclick={() => (headers = [...headers, { name: "", value: "" }])}
        class={smallNeutralBtnClass}
      >
        <Plus size={10} /> add
      </button>
    </div>
    {#each headers as h, i}
      <div class="flex gap-2 items-center">
        <input
          type="text"
          bind:value={h.name}
          placeholder="Header"
          aria-label="Header name"
          class="flex-1 {mono}"
        />
        <input
          type="text"
          bind:value={h.value}
          placeholder="value"
          aria-label="Header value"
          class="flex-1 {mono}"
        />
        <button
          type="button"
          onclick={() => (headers = headers.filter((_, j) => j !== i))}
          class={removeBtnClass}
          title="Remove header"
          aria-label="Remove header"
        >
          <Trash2 size={12} />
        </button>
      </div>
    {/each}
  </div>

  {#if result}
    <div
      class="text-xs space-y-2 rounded border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-800 p-2"
    >
      <div class="font-mono text-slate-700 dark:text-slate-200">
        {terminalLabel(result)}
      </div>
      <div class="grid grid-cols-2 gap-2 text-[11px]">
        <div>
          <span class="text-slate-500 dark:text-slate-400">Input</span>
          <div class="font-mono text-slate-700 dark:text-slate-200">
            {result.initial.method}
            {pathWithQuery(result.initial.path, result.initial.raw_query)}
          </div>
        </div>
        <div>
          <span class="text-slate-500 dark:text-slate-400">Shim sees</span>
          <div class="font-mono text-slate-700 dark:text-slate-200">
            {result.final.method}
            {pathWithQuery(result.final.path, result.final.raw_query)}
          </div>
        </div>
      </div>
      {#if result.matched_rules.length === 0}
        <div class="text-[11px] text-slate-500 dark:text-slate-400">
          No rules matched; request falls through to the shim.
        </div>
      {:else}
        <div class="space-y-1.5">
          {#each result.matched_rules as trace}
            <div
              class="text-[11px] border border-slate-200 dark:border-warm-700 rounded p-2 bg-slate-50 dark:bg-warm-900"
            >
              <div class="font-medium text-slate-700 dark:text-slate-200">
                Rule #{trace.rule_index + 1}{trace.rule_name
                  ? ` — ${trace.rule_name}`
                  : ""}
              </div>
              <div class="mt-1 space-y-1 font-mono text-slate-600 dark:text-slate-300">
                {#each trace.actions as action}
                  <div>
                    {action.action_index + 1}. {action.type}: {actionTraceSummary(
                      action,
                    )}
                  </div>
                {/each}
              </div>
            </div>
          {/each}
        </div>
      {/if}
      {#if result.block}
        <pre
          class="text-[11px] p-2 rounded bg-warm-950 text-slate-100 overflow-auto max-h-32">{result.block.body}</pre>
      {/if}
    </div>
  {/if}
</div>
