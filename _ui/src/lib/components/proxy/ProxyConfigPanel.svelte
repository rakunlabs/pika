<script lang="ts">
 // Right-side config panel for the currently selected node.
 //
 // For nodes whose subtype is in the form catalogue we render a
 // proper labelled DynamicForm (every field has a label, helper
 // text, validation hint). Listener nodes have no editable config
 // — the panel shows a short "nothing to configure" hint so the
 // affordance is honest. Switch nodes get a dedicated SwitchForm
 // that manages the rule list (CRUD + reorder + per-matcher
 // fields) because DynamicForm's static schema is not expressive
 // enough for nested arrays.
 //
 // The raw JSON view stays available behind an expandable
 // "Advanced JSON" section so an operator can sanity-check what's
 // actually being persisted and edit fields the schema hasn't
 // caught up to yet.
 import type { ProxyNode } from '@/lib/types/config';
 import type { CatalogSpec } from '@/lib/store/proxy.svelte';
 import { Trash2 } from 'lucide-svelte';
 import { formFor } from '@/lib/components/proxy/forms/schema';
 import DynamicForm from '@/lib/components/proxy/forms/DynamicForm.svelte';
 import SwitchForm from '@/lib/components/proxy/forms/SwitchForm.svelte';

 let {
  node,
  spec,
  canManage,
  onChange,
  onDelete,
  onSwitchRuleRemoved,
 }: {
  node: ProxyNode | null;
  spec: CatalogSpec | null;
  canManage: boolean;
  onChange: (next: ProxyNode) => void;
  onDelete: () => void;
  // Forwarded straight to SwitchForm so the page can clean up
  // dangling edges when a switch rule is removed. Optional —
  // non-switch nodes never trigger it.
  onSwitchRuleRemoved?: (ruleId: string) => void;
 } = $props();

 // Local mirror of the JSON view text so commits don't fight the
 // textarea as the user types (newlines, trailing whitespace).
 let rawText = $state('{}');
 let rawError = $state<string | null>(null);
 let showJson = $state(false);

 // Resolve the form schema for this (type, subtype). Returns null
 // for listener / router (no config) or for unknown subtypes — the
 // page falls back to the JSON editor in those cases.
 const form = $derived(node ? formFor(node.type, node.subtype) : null);

 $effect(() => {
  if (node) {
   rawText = JSON.stringify(node.config ?? {}, null, 2);
   rawError = null;
   // Keep the JSON section collapsed when switching to a node with
   // a known form so the user lands on the friendly UI first.
   if (form) showJson = false;
   else showJson = true;
  }
 });

 function applyFormPatch(next: Record<string, unknown>) {
  if (!node) return;
  // Keep the JSON view in sync so the operator can flip the
  // collapse open and see the live state.
  rawText = JSON.stringify(next, null, 2);
  rawError = null;
  onChange({ ...node, config: next });
 }

 function commitRaw(text: string) {
  if (!node) return;
  rawText = text;
  try {
   const parsed = text.trim() === '' ? {} : JSON.parse(text);
   rawError = null;
   onChange({ ...node, config: parsed });
  } catch (e: any) {
   rawError = e.message ?? 'invalid JSON';
  }
 }
</script>

<aside class="panel">
 {#if !node}
  <div class="empty">
   <p class="text-xs text-slate-500 dark:text-slate-400 text-center">
    Select a node to edit its configuration.
   </p>
  </div>
 {:else}
  <header class="header">
   <div class="grow min-w-0">
    <div class="text-[10px] uppercase tracking-wide text-slate-500 dark:text-slate-400">{node.type}</div>
    <div class="text-sm font-semibold text-slate-800 dark:text-slate-100 truncate">
     {spec?.label ?? node.subtype ?? node.type}
    </div>
    {#if spec?.description}
     <p class="text-xs text-slate-500 dark:text-slate-400 mt-1">{spec.description}</p>
    {/if}
   </div>
   {#if canManage && node.type !== 'listener'}
    <button
     type="button"
     aria-label="Delete node"
     title="Delete node"
     class="p-1.5 rounded text-vermilion-600 dark:text-vermilion-400
            hover:bg-vermilion-50 dark:hover:bg-vermilion-950/40 cursor-pointer"
     onclick={onDelete}
    >
     <Trash2 size={14} />
    </button>
   {/if}
  </header>

  <div class="mt-4">
    {#if !canManage}
     <p class="text-xs text-slate-500 dark:text-slate-400 italic">
      Read-only — your account has <code class="font-mono">proxy.read</code> but not <code class="font-mono">proxy.manage</code>.
     </p>
    {:else if node.type === 'switch'}
     <SwitchForm
      value={node.config ?? {}}
      {canManage}
      onChange={applyFormPatch}
      onRuleRemoved={onSwitchRuleRemoved}
     />
    {:else if form}
     <DynamicForm form={form} value={node.config ?? {}} onChange={applyFormPatch} />
    {:else if node.type === 'listener'}
     <p class="text-xs text-slate-500 dark:text-slate-400 italic">
      The listener's host and port live on the toolbar above the canvas.
     </p>
    {/if}

   <details class="mt-4 group" bind:open={showJson}>
    <summary class="cursor-pointer text-xs text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200">
     Advanced JSON
    </summary>
    <textarea
     class="mt-2 w-full px-3 py-2 text-xs font-mono rounded
            border border-slate-300 dark:border-warm-600
            bg-white dark:bg-warm-900
            text-slate-800 dark:text-slate-100
            focus:outline-none focus:ring-2 focus:ring-accent-500"
     rows="8"
     spellcheck="false"
     disabled={!canManage}
     value={rawText}
     oninput={(e) => commitRaw((e.currentTarget as HTMLTextAreaElement).value)}
    ></textarea>
    {#if rawError}
     <p class="mt-1 text-xs text-vermilion-600 dark:text-vermilion-400">{rawError}</p>
    {/if}
   </details>
  </div>
 {/if}
</aside>

<style>
 .panel {
  width: 340px;
  flex-shrink: 0;
  border-left-width: 1px;
  border-left-style: solid;
  padding: 16px;
  overflow-y: auto;
 }
 .header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 8px;
 }
 .empty {
  padding: 24px 8px;
 }
</style>
