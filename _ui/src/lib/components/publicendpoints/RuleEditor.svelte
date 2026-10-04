<!--
  RuleEditor — one request-check rule (matcher + ordered actions).
  Structural changes (move/remove/action list edits) are reported to the
  parent so it can rebuild the rule list and reset any test result.
-->
<script lang="ts">
  import { Plus, Trash2 } from "lucide-svelte";
  import type { RequestActionType } from "@/lib/types/config";
  import ActionEditor from "./ActionEditor.svelte";
  import {
    actionOfType,
    effectiveRuleActions,
    removeBtnClass,
    ruleHasAction,
    ruleInputClass,
    ruleSummary,
    setWhenKind,
    whenKindOf,
    type ActionDraft,
    type CaptureTransformDraft,
    type RuleDraft,
    type WhenKind,
  } from "./rules";

  type Props = {
    rule: RuleDraft;
    index: number;
    count: number;
    onMove: (dir: -1 | 1) => void;
    onRemove: () => void;
    /** Replace this rule with a new object (used for action list edits). */
    onReplace: (next: RuleDraft) => void;
  };

  let {
    rule = $bindable(),
    index,
    count,
    onMove,
    onRemove,
    onReplace,
  }: Props = $props();

  const kind = $derived(whenKindOf(rule));
  const actions = $derived(effectiveRuleActions(rule));

  function setRuleActions(next: ActionDraft[]) {
    const nextActions = next.length
      ? next
      : [{ type: "allow" } satisfies ActionDraft];
    onReplace({ ...rule, then: nextActions[0], actions: nextActions });
  }

  function setActionType(actionIdx: number, t: RequestActionType) {
    const current = actions[actionIdx] ?? { type: "allow" };
    const next = actionOfType(current, t);
    setRuleActions(actions.map((a, i) => (i === actionIdx ? next : a)));
  }

  function setActionTransforms(
    actionIdx: number,
    transforms: CaptureTransformDraft[],
  ) {
    const action = actions[actionIdx];
    if (!action || action.type !== "replace_path") return;
    const nextAction: ActionDraft = {
      ...action,
      capture_transforms: transforms.length ? transforms : undefined,
    };
    setRuleActions(actions.map((a, i) => (i === actionIdx ? nextAction : a)));
  }

  const mono = `${ruleInputClass} font-mono`;
  const moveBtn =
    "px-1.5 py-0.5 text-[11px] rounded bg-slate-200 dark:bg-warm-700 hover:bg-slate-300 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-200 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed";
</script>

<div
  class="p-3 rounded border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900 space-y-2"
>
  <div class="flex items-center gap-2 flex-wrap">
    <span class="text-[10px] font-mono text-slate-500 dark:text-slate-400"
      >#{index + 1}</span
    >
    <input
      type="text"
      bind:value={rule.name}
      placeholder="rule name (optional)"
      aria-label="Rule name"
      class="flex-1 min-w-[8rem] {ruleInputClass}"
    />
    <label
      class="inline-flex items-center gap-1 text-[11px] text-slate-700 dark:text-slate-200"
    >
      <input
        type="checkbox"
        bind:checked={rule.enabled}
        class="rounded border-slate-300 dark:border-warm-600"
      />
      enabled
    </label>
    <button
      type="button"
      onclick={() => onMove(-1)}
      disabled={index === 0}
      class={moveBtn}
      title="Move up"
      aria-label="Move rule up">↑</button
    >
    <button
      type="button"
      onclick={() => onMove(1)}
      disabled={index === count - 1}
      class={moveBtn}
      title="Move down"
      aria-label="Move rule down">↓</button
    >
    <button
      type="button"
      onclick={onRemove}
      class={removeBtnClass}
      title="Delete rule"
      aria-label="Delete rule"
    >
      <Trash2 size={12} />
    </button>
  </div>

  <div class="grid grid-cols-[auto_1fr_1fr] gap-2 items-center text-xs">
    <span class="text-slate-500 dark:text-slate-400 font-mono">when</span>
    <select
      value={kind}
      aria-label="Match condition"
      onchange={(e) => setWhenKind(rule, e.currentTarget.value as WhenKind)}
      class={ruleInputClass}
    >
      <option value="any">any request</option>
      <option value="method">method equals</option>
      <option value="path_equals">path equals</option>
      <option value="path_prefix">path starts with</option>
      <option value="header_equals">header equals</option>
      <option value="header_present">header present</option>
      <option value="header_absent">header missing</option>
      <option value="query_equals">query equals</option>
      <option value="query_present">query present</option>
      <option value="query_absent">query missing</option>
    </select>
    <div class="flex gap-2">
      {#if kind === "method"}
        <input type="text" bind:value={rule.when.method} placeholder="GET" aria-label="Method" class="flex-1 {mono}" />
      {:else if kind === "path_equals"}
        <input type="text" bind:value={rule.when.path_equals} placeholder="/v1/kv/foo" aria-label="Path" class="flex-1 {mono}" />
      {:else if kind === "path_prefix"}
        <input type="text" bind:value={rule.when.path_prefix} placeholder="/v1/kv/" aria-label="Path prefix" class="flex-1 {mono}" />
      {:else if kind === "header_equals" && rule.when.header_equals}
        <input type="text" bind:value={rule.when.header_equals.name} placeholder="X-Tenant" aria-label="Header name" class="flex-1 {mono}" />
        <input type="text" bind:value={rule.when.header_equals.value} placeholder="value" aria-label="Header value" class="flex-1 {mono}" />
      {:else if kind === "header_present"}
        <input type="text" bind:value={rule.when.header_present} placeholder="X-Tenant" aria-label="Header name" class="flex-1 {mono}" />
      {:else if kind === "header_absent"}
        <input type="text" bind:value={rule.when.header_absent} placeholder="X-Tenant" aria-label="Header name" class="flex-1 {mono}" />
      {:else if kind === "query_equals" && rule.when.query_equals}
        <input type="text" bind:value={rule.when.query_equals.name} placeholder="variant" aria-label="Query parameter" class="flex-1 {mono}" />
        <input type="text" bind:value={rule.when.query_equals.value} placeholder="value" aria-label="Query value" class="flex-1 {mono}" />
      {:else if kind === "query_present"}
        <input type="text" bind:value={rule.when.query_present} placeholder="variant" aria-label="Query parameter" class="flex-1 {mono}" />
      {:else if kind === "query_absent"}
        <input type="text" bind:value={rule.when.query_absent} placeholder="variant" aria-label="Query parameter" class="flex-1 {mono}" />
      {/if}
    </div>
  </div>

  <div class="space-y-2">
    {#each actions as _action, actionIdx}
      <ActionEditor
        bind:action={
          () => actions[actionIdx],
          (v) => setRuleActions(actions.map((a, i) => (i === actionIdx ? v : a)))
        }
        first={actionIdx === 0}
        canRemove={actions.length > 1}
        onTypeChange={(t) => setActionType(actionIdx, t)}
        onTransformsChange={(tr) => setActionTransforms(actionIdx, tr)}
        onRemove={() => setRuleActions(actions.filter((_, i) => i !== actionIdx))}
      />
    {/each}
    <button
      type="button"
      onclick={() =>
        setRuleActions([
          ...actions,
          { type: "set_query", name: "variant", value: "prod" },
        ])}
      class="ml-10 px-2 py-1 text-[11px] rounded bg-slate-200 dark:bg-warm-700 hover:bg-slate-300 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-200 cursor-pointer inline-flex items-center gap-1"
    >
      <Plus size={10} /> add action
    </button>
  </div>

  <p class="text-[11px] text-slate-500 dark:text-slate-400 font-mono">
    {ruleSummary(rule)}
  </p>
  {#if ruleHasAction(rule, "set_path")}
    <div
      class="text-[11px] text-slate-500 dark:text-slate-400 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded px-2 py-1.5"
    >
      <strong class="text-slate-600 dark:text-slate-300"
        >Path rewrite is literal.</strong
      >
      It replaces the whole request path, not a regex capture. Example: when
      path starts with
      <code class="font-mono">/legacy/app</code>, set path to
      <code class="font-mono">/myapp/config</code> so the shim sees
      <code class="font-mono">/myapp/config</code>.
    </div>
  {/if}
  {#if ruleHasAction(rule, "replace_path")}
    <div
      class="text-[11px] text-slate-500 dark:text-slate-400 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded px-2 py-1.5"
    >
      <strong class="text-slate-600 dark:text-slate-300"
        >Regex path replacement.</strong
      >
      The first field is a Go regexp pattern; the second is the replacement.
      Capture groups use
      <code class="font-mono">$1</code>,
      <code class="font-mono">${"${1}"}</code>, or
      <code class="font-mono">$name</code>. Example:
      <code class="font-mono">^/legacy/(.*)$</code>
      → <code class="font-mono">/$1</code>
      turns <code class="font-mono">/legacy/myapp/config</code>
      into <code class="font-mono">/myapp/config</code>. Capture transforms can
      edit a capture first: with pattern
      <code class="font-mono">^/legacy/(.*)$</code>, replacement
      <code class="font-mono">/legacy/${"${1}"}</code>, transform capture
      <code class="font-mono">1</code> find <code class="font-mono">/</code>
      to <code class="font-mono">-</code>,
      <code class="font-mono">/legacy/1/2/3</code>
      becomes <code class="font-mono">/legacy/1-2-3</code>.
    </div>
  {/if}
</div>
