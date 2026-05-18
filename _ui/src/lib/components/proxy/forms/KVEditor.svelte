<script lang="ts">
 // KVEditor — minimal key/value list editor used by SwitchForm
 // for header and query matchers. Each row owns a {k, v} pair;
 // empty keys are filtered out on commit (the parent decides
 // how to turn the list back into a map).
 //
 // Kept tiny on purpose: this is not a generic widget, it does
 // exactly what the switch form needs. If a second consumer
 // shows up we can promote it to a shared component.
 import { Plus, Trash2 } from 'lucide-svelte';

 type KV = { k: string; v: string };

 let {
  list,
  canManage,
  keyPlaceholder,
  valuePlaceholder,
  onChange,
 }: {
  list: KV[];
  canManage: boolean;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
  onChange: (next: KV[]) => void;
 } = $props();

 function setAt(idx: number, patch: Partial<KV>) {
  const next = list.slice();
  next[idx] = { ...next[idx], ...patch };
  onChange(next);
 }
 function add() {
  onChange([...list, { k: '', v: '' }]);
 }
 function remove(idx: number) {
  const next = list.slice();
  next.splice(idx, 1);
  onChange(next);
 }
</script>

<div class="kv">
 {#each list as kv, idx (idx)}
  <div class="row">
   <input
    type="text"
    placeholder={keyPlaceholder ?? 'key'}
    value={kv.k}
    oninput={(e) => setAt(idx, { k: (e.currentTarget as HTMLInputElement).value })}
    disabled={!canManage}
   />
   <input
    type="text"
    placeholder={valuePlaceholder ?? 'value'}
    value={kv.v}
    oninput={(e) => setAt(idx, { v: (e.currentTarget as HTMLInputElement).value })}
    disabled={!canManage}
   />
   <button type="button" class="icon-btn" disabled={!canManage} onclick={() => remove(idx)} aria-label="Remove pair">
    <Trash2 size={11} />
   </button>
  </div>
 {/each}
 {#if canManage}
  <button type="button" class="add-btn" onclick={add}>
   <Plus size={11} /> Add
  </button>
 {/if}
</div>

<style>
 .kv {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-top: 4px;
 }
 .row {
  display: grid;
  grid-template-columns: 1fr 1fr auto;
  gap: 4px;
 }
 .row input {
  font-size: 11px;
  font-family: ui-monospace, SFMono-Regular, monospace;
  padding: 3px 5px;
  border-radius: 3px;
  border: 1px solid rgb(203 213 225);
  background: rgb(255 255 255);
  color: rgb(30 41 59);
 }
 :global(.dark) .row input {
  border-color: rgb(64 60 56);
  background: rgb(28 25 23);
  color: rgb(226 232 240);
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
  background: rgb(254 226 226);
  color: rgb(185 28 28);
 }
 .icon-btn:disabled { opacity: 0.4; cursor: not-allowed; }
 :global(.dark) .icon-btn:hover:not(:disabled) {
  background: rgb(127 29 29 / 0.4);
  color: rgb(252 165 165);
 }
 .add-btn {
  align-self: flex-start;
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 2px 8px;
  font-size: 11px;
  border-radius: 3px;
  border: 1px dashed rgb(148 163 184);
  background: transparent;
  color: rgb(71 85 105);
  cursor: pointer;
 }
 :global(.dark) .add-btn {
  border-color: rgb(82 78 75);
  color: rgb(203 213 225);
 }
</style>
