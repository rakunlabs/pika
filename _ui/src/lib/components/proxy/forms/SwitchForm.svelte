<script lang="ts">
 // SwitchForm — the dedicated config panel UI for switch nodes.
 //
 // Renders a vertical list of rule cards (one per rule in
 // config.rules), each with the full set of matchers the backend
 // supports (path, methods, host, cidrs, headers, query). The
 // operator can add/remove/reorder rules; reordering matters
 // because the backend tries rules top-down and the first matching
 // host/IP group claims the request.
 //
 // We mirror the rule list locally so an in-flight edit (typing in
 // a path field) does not race with re-renders. Every mutation
 // immediately calls onChange with the full config object — the
 // page is the authority over what gets persisted; this form is
 // just a stateful editor.
 //
 // The "default" branch is NOT represented as a rule here. It
 // shows up as the bottom output handle on the node itself. This
 // form only configures rules; the default branch is wired by the
 // operator dragging an edge from the node's "default" handle.
 import { Plus, Trash2, ChevronUp, ChevronDown } from 'lucide-svelte';
 import KVEditor from './KVEditor.svelte';

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

 interface SwitchConfigShape {
  rules?: SwitchRule[];
 }

 let {
  value,
  canManage,
  onChange,
  onRuleRemoved,
 }: {
  value: Record<string, unknown>;
  canManage: boolean;
  onChange: (next: Record<string, unknown>) => void;
  // Fired AFTER a rule is removed from the config. The page uses
  // it to also delete the matching output edge on the canvas, so
  // a deleted rule does not leave behind a dangling wired branch
  // (which would compile-fail as orphan/missing branch).
  onRuleRemoved?: (ruleId: string) => void;
 } = $props();

 // Local mirror. Two-way binding through onChange keeps the page
 // store as the source of truth, but we read straight off `rules`
 // for the form fields.
 const rules = $derived<SwitchRule[]>(((value as SwitchConfigShape).rules ?? []) as SwitchRule[]);

 // METHOD_OPTIONS mirrors ada/mux's supported method tags. Listed
 // here in alphabetical order so the checkboxes don't shuffle on
 // re-render.
 const METHOD_OPTIONS = ['DELETE', 'GET', 'HEAD', 'OPTIONS', 'PATCH', 'POST', 'PUT'];

 // genID returns a stable string ID for a new rule. URL-safe and
 // short enough to read in edge.source_handle values during
 // debugging. The frontend never re-uses an ID, so collisions are
 // not a concern at this scale.
 function genID(): string {
  const buf = new Uint8Array(8);
  if (typeof crypto !== 'undefined' && crypto.getRandomValues) {
   crypto.getRandomValues(buf);
  } else {
   for (let i = 0; i < 8; i++) buf[i] = Math.floor(Math.random() * 256);
  }
  return 'rule-' + Array.from(buf).map((b) => b.toString(16).padStart(2, '0')).join('').slice(0, 8);
 }

 function emit(next: SwitchRule[]) {
  onChange({ ...value, rules: next });
 }

 function addRule() {
  if (!canManage) return;
  const next = [...rules, { id: genID(), label: 'New rule' }];
  emit(next);
 }

 function removeRule(idx: number) {
  if (!canManage) return;
  const removed = rules[idx];
  const next = rules.slice();
  next.splice(idx, 1);
  emit(next);
  if (removed && onRuleRemoved) onRuleRemoved(removed.id);
 }

 function moveRule(idx: number, delta: -1 | 1) {
  if (!canManage) return;
  const target = idx + delta;
  if (target < 0 || target >= rules.length) return;
  const next = rules.slice();
  [next[idx], next[target]] = [next[target], next[idx]];
  emit(next);
 }

 // patchRule replaces one rule by index with a partial update. We
 // shallow-merge so the caller can pass just the fields it changed
 // without re-typing the whole rule object.
 function patchRule(idx: number, patch: Partial<SwitchRule>) {
  if (!canManage) return;
  const next = rules.slice();
  next[idx] = { ...next[idx], ...patch };
  emit(next);
 }

 // Methods are stored as an array but edited via per-method
 // checkboxes. toggleMethod flips one entry on/off.
 function toggleMethod(idx: number, method: string) {
  const cur = rules[idx].methods ?? [];
  const has = cur.includes(method);
  const next = has ? cur.filter((m) => m !== method) : [...cur, method];
  patchRule(idx, { methods: next.length === 0 ? undefined : next });
 }

 // CIDRs are stored as an array; we edit them as a single
 // comma-separated string for compactness. Empty entries are
 // dropped on commit so a trailing comma doesn't leak into the
 // backend list.
 function cidrsToString(c: string[] | undefined): string {
  return (c ?? []).join(', ');
 }
 function commitCIDRs(idx: number, raw: string) {
  const arr = raw.split(',').map((s) => s.trim()).filter((s) => s !== '');
  patchRule(idx, { cidrs: arr.length === 0 ? undefined : arr });
 }

 // Headers and Query share the kv-map editor model: a list of
 // {k,v} pairs the operator manages row-by-row. The map is
 // re-built on every commit so the field order in JSON matches
 // the on-screen order.
 type KV = { k: string; v: string };
 function mapToList(m: Record<string, string> | undefined): KV[] {
  if (!m) return [];
  return Object.entries(m).map(([k, v]) => ({ k, v }));
 }
 function listToMap(l: KV[]): Record<string, string> | undefined {
  const out: Record<string, string> = {};
  for (const kv of l) if (kv.k !== '') out[kv.k] = kv.v;
  return Object.keys(out).length === 0 ? undefined : out;
 }
 function commitHeaders(idx: number, l: KV[]) {
  patchRule(idx, { headers: listToMap(l) });
 }
 function commitQuery(idx: number, l: KV[]) {
  patchRule(idx, { query: listToMap(l) });
 }
</script>

<section class="space-y-3">
 <header class="flex items-start justify-between gap-2">
  <p class="text-xs text-slate-500 dark:text-slate-400">
   Rules are tried top-to-bottom. Rules sharing the same host + CIDRs combine into one mux; the
   first group whose host/IP predicate matches owns the request. Wire each rule's output handle on
   the canvas to whichever downstream node should handle it.
  </p>
 </header>

 <ul class="space-y-2">
  {#each rules as r, idx (r.id)}
   <li class="rule-card">
    <div class="rule-head">
     <input
      type="text"
      class="label-input"
      placeholder="Label (optional)"
      value={r.label ?? ''}
      oninput={(e) => patchRule(idx, { label: (e.currentTarget as HTMLInputElement).value })}
      disabled={!canManage}
     />
     <div class="ml-auto flex items-center gap-0.5">
      <button
       type="button"
       class="icon-btn"
       title="Move up"
       aria-label="Move rule up"
       disabled={!canManage || idx === 0}
       onclick={() => moveRule(idx, -1)}
      ><ChevronUp size={12} /></button>
      <button
       type="button"
       class="icon-btn"
       title="Move down"
       aria-label="Move rule down"
       disabled={!canManage || idx === rules.length - 1}
       onclick={() => moveRule(idx, 1)}
      ><ChevronDown size={12} /></button>
      <button
       type="button"
       class="icon-btn danger"
       title="Delete rule"
       aria-label="Delete rule"
       disabled={!canManage}
       onclick={() => removeRule(idx)}
      ><Trash2 size={12} /></button>
     </div>
    </div>

    <label class="field">
     <span>Path</span>
     <input
      type="text"
      placeholder="/api/* or /metrics or empty (= /*)"
      value={r.path ?? ''}
      oninput={(e) => patchRule(idx, { path: (e.currentTarget as HTMLInputElement).value || undefined })}
      disabled={!canManage}
     />
    </label>

    <fieldset class="field">
     <legend>Methods</legend>
     <div class="methods">
      {#each METHOD_OPTIONS as m}
       <label class="method">
        <input
         type="checkbox"
         checked={(r.methods ?? []).includes(m)}
         onchange={() => toggleMethod(idx, m)}
         disabled={!canManage}
        />
        {m}
       </label>
      {/each}
      <span class="help">empty = any method</span>
     </div>
    </fieldset>

    <label class="field">
     <span>Host</span>
     <input
      type="text"
      placeholder="api.example.com or *.example.com (empty = any)"
      value={r.host ?? ''}
      oninput={(e) => patchRule(idx, { host: (e.currentTarget as HTMLInputElement).value || undefined })}
      disabled={!canManage}
     />
    </label>

    <label class="field">
     <span>Source CIDRs (comma-separated)</span>
     <input
      type="text"
      placeholder="10.0.0.0/8, 192.168.1.5"
      value={cidrsToString(r.cidrs)}
      oninput={(e) => commitCIDRs(idx, (e.currentTarget as HTMLInputElement).value)}
      disabled={!canManage}
     />
    </label>

    <details class="field details">
     <summary>Header match ({Object.keys(r.headers ?? {}).length})</summary>
     <KVEditor
      list={mapToList(r.headers)}
      onChange={(l) => commitHeaders(idx, l)}
      canManage={canManage}
      keyPlaceholder="X-Api-Version"
      valuePlaceholder="v2"
     />
    </details>

    <details class="field details">
     <summary>Query match ({Object.keys(r.query ?? {}).length})</summary>
     <KVEditor
      list={mapToList(r.query)}
      onChange={(l) => commitQuery(idx, l)}
      canManage={canManage}
      keyPlaceholder="tier"
      valuePlaceholder="gold"
     />
    </details>
   </li>
  {/each}
 </ul>

 {#if canManage}
  <button type="button" class="add-btn" onclick={addRule}>
   <Plus size={12} /> Add rule
  </button>
 {/if}
</section>

<style>
 .rule-card {
  border: 1px solid rgb(203 213 225);
  background: rgb(255 255 255);
  border-radius: 6px;
  padding: 8px;
  display: flex;
  flex-direction: column;
  gap: 6px;
 }
 :global(.dark) .rule-card {
  border-color: rgb(64 60 56);
  background: rgb(38 35 33);
 }
 .rule-head {
  display: flex;
  align-items: center;
  gap: 4px;
 }
 .label-input {
  flex: 1;
  min-width: 0;
  font-size: 12px;
  font-weight: 600;
  padding: 3px 6px;
  border-radius: 4px;
  border: 1px solid transparent;
  background: transparent;
  color: inherit;
 }
 .label-input:hover:not(:disabled) {
  border-color: rgb(203 213 225);
 }
 .label-input:focus {
  outline: none;
  border-color: rgb(20 184 166);
  background: rgb(255 255 255);
 }
 :global(.dark) .label-input:focus {
  background: rgb(28 25 23);
 }
 .icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 4px;
  border: none;
  background: transparent;
  color: rgb(100 116 139);
  cursor: pointer;
 }
 .icon-btn:hover:not(:disabled) {
  background: rgb(241 245 249);
  color: rgb(15 23 42);
 }
 .icon-btn.danger:hover:not(:disabled) {
  background: rgb(254 226 226);
  color: rgb(185 28 28);
 }
 .icon-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
 }
 :global(.dark) .icon-btn:hover:not(:disabled) {
  background: rgb(64 60 56);
  color: rgb(241 245 249);
 }
 :global(.dark) .icon-btn.danger:hover:not(:disabled) {
  background: rgb(127 29 29 / 0.4);
  color: rgb(252 165 165);
 }
 .field {
  display: flex;
  flex-direction: column;
  gap: 3px;
  font-size: 11px;
 }
 .field > span, .field > legend {
  font-size: 10px;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: rgb(100 116 139);
 }
 :global(.dark) .field > span,
 :global(.dark) .field > legend {
  color: rgb(148 163 184);
 }
 .field input[type="text"] {
  font-size: 12px;
  padding: 4px 6px;
  border-radius: 4px;
  border: 1px solid rgb(203 213 225);
  background: rgb(255 255 255);
  color: rgb(30 41 59);
  font-family: ui-monospace, SFMono-Regular, monospace;
 }
 :global(.dark) .field input[type="text"] {
  border-color: rgb(64 60 56);
  background: rgb(28 25 23);
  color: rgb(226 232 240);
 }
 .field input[type="text"]:focus {
  outline: none;
  border-color: rgb(20 184 166);
  box-shadow: 0 0 0 2px rgb(20 184 166 / 0.2);
 }
 .methods {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
 }
 .method {
  font-size: 11px;
  font-family: ui-monospace, SFMono-Regular, monospace;
  display: inline-flex;
  align-items: center;
  gap: 3px;
 }
 .help {
  font-size: 10px;
  font-style: italic;
  color: rgb(148 163 184);
 }
 .details summary {
  font-size: 11px;
  cursor: pointer;
  user-select: none;
  color: rgb(71 85 105);
 }
 :global(.dark) .details summary {
  color: rgb(148 163 184);
 }
 .add-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 10px;
  font-size: 12px;
  font-weight: 500;
  border-radius: 4px;
  border: 1px dashed rgb(148 163 184);
  background: transparent;
  color: rgb(71 85 105);
  cursor: pointer;
 }
 .add-btn:hover {
  border-color: rgb(20 184 166);
  color: rgb(15 118 110);
 }
 :global(.dark) .add-btn {
  border-color: rgb(82 78 75);
  color: rgb(203 213 225);
 }
 :global(.dark) .add-btn:hover {
  border-color: rgb(45 212 191);
  color: rgb(94 234 212);
 }
</style>


