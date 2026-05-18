<script lang="ts">
 // Switch node — the routing primitive that replaced the old
 // "router" kind. Holds a list of rules; each rule produces a
 // dedicated output handle whose ID matches rule.id, plus a
 // mandatory "default" output handle for the fallback branch.
 //
 // At request time the backend partitions rules by (host, cidrs)
 // into per-group ada.Mux instances and tries the groups in input
 // order; the first matching group's mux handles the request. The
 // canvas only needs to show one handle per rule so the operator
 // can wire each rule to whichever downstream node they want.
 //
 // Visual layout:
 //  - One left input handle (http-stream).
 //  - Right column: one handle per rule + the default handle at
 //    the bottom. Each row shows a compact summary of the rule
 //    (method+path / host / IP / header / query) so the operator
 //    can read the routing logic without opening the config panel.
 //
 // Detailed editing lives in ProxyConfigPanel → SwitchForm.
 import { Handle, HandleGroup } from 'kaykay';
 import type { NodeProps } from 'kaykay';
 import { Split, AlertTriangle } from 'lucide-svelte';

 // SwitchRule mirrors proxy.SwitchRule on the backend. Kept in
 // sync manually — there is no client-side codegen step. New
 // fields land here AND in switch.go.
 interface SwitchRule {
  id: string;
  label?: string;
  host?: string;
  cidrs?: string[];
  path?: string;
  methods?: string[];
  headers?: Record<string, string>;
  query?: Record<string, string>;
 }

 interface SwitchData {
  rules?: SwitchRule[];
  hasError?: boolean;
 }

 let { data, selected }: NodeProps<SwitchData> = $props();

 // Defensive: data.rules can be undefined if the operator created
 // the node and hasn't opened the form yet. Render an empty list
 // and a hint to add a rule.
 const rules = $derived(data.rules ?? []);

 // ruleSummary builds a single-line, plain-text description of a
 // rule for the node body. Kept simple on purpose — the config
 // panel is where the operator reads the full configuration.
 function ruleSummary(r: SwitchRule): string {
  const parts: string[] = [];
  if (r.methods && r.methods.length > 0) parts.push(r.methods.join('|'));
  if (r.path) parts.push(r.path);
  else if (parts.length === 0) parts.push('/*');
  if (r.host) parts.push('@ ' + r.host);
  if (r.cidrs && r.cidrs.length > 0) parts.push('ip:' + r.cidrs.join(','));
  if (r.headers && Object.keys(r.headers).length > 0) parts.push('hdr');
  if (r.query && Object.keys(r.query).length > 0) parts.push('q');
  return parts.join(' ');
 }
</script>

<div
 class="switch-node
        bg-indigo-50 dark:bg-indigo-950/40
        text-indigo-900 dark:text-indigo-100
        border {data.hasError
         ? 'border-vermilion-500 dark:border-vermilion-500'
         : 'border-indigo-300 dark:border-indigo-700'}
        {selected ? 'ring-2 ring-accent-500 ring-offset-2 ring-offset-slate-100 dark:ring-offset-warm-950' : ''}"
>
 <div class="header">
  <Split size={12} />
  <span class="title">Switch</span>
  {#if rules.length === 0}
   <AlertTriangle size={11} class="text-amber-500 dark:text-amber-400 ml-auto" />
  {/if}
 </div>

 <Handle id="in" type="input" port="http-stream" accept={['http-stream', 'route']} position="left" />

 {#if rules.length === 0}
  <div class="empty">no rules — add one in the panel</div>
 {:else}
  <div class="rules">
   {#each rules as r (r.id)}
    <div class="rule-row">
     <span class="rule-label" title={ruleSummary(r)}>
      {r.label || ruleSummary(r) || r.id}
     </span>
     <Handle id={r.id} type="output" port="http-stream" position="right" />
    </div>
   {/each}
  </div>
 {/if}

 <!-- Default handle is always present, fixed at the bottom. Its
      ID 'default' is hard-coded and must NOT be used as a rule
      ID; the backend rejects that case. -->
 <div class="default-row">
  <span class="default-label">default</span>
  <Handle id="default" type="output" port="http-stream" position="right" />
 </div>
</div>

<style>
 .switch-node {
  border-radius: 6px;
  padding: 6px 10px;
  min-width: 180px;
  font-size: 12px;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.08);
 }
 .header {
  display: flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
  margin-bottom: 4px;
 }
 .title { font-size: 13px; }
 .empty {
  font-size: 11px;
  font-style: italic;
  opacity: 0.7;
  padding: 4px 0;
 }
 .rules {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin-top: 2px;
 }
 .rule-row, .default-row {
  position: relative;
  display: flex;
  align-items: center;
  padding: 3px 0;
  border-top: 1px solid rgba(0, 0, 0, 0.06);
 }
 :global(.dark) .rule-row,
 :global(.dark) .default-row {
  border-top-color: rgba(255, 255, 255, 0.08);
 }
 .rule-label {
  font-family: ui-monospace, SFMono-Regular, monospace;
  font-size: 11px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 160px;
 }
 .default-row {
  margin-top: 4px;
  border-top: 1px dashed rgba(0, 0, 0, 0.15);
 }
 :global(.dark) .default-row {
  border-top-color: rgba(255, 255, 255, 0.2);
 }
 .default-label {
  font-size: 10px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  opacity: 0.7;
 }
</style>
