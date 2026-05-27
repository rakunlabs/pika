<script lang="ts">
 // Listeners panel — operator-facing CRUD for socket bind points.
 // A ProxyListener owns one port (HTTP or TCP) and one or more
 // graphs (ProxyServer rows) attach to it. HTTP listeners can host
 // many graphs routed by Host header; TCP listeners hold one.
 //
 // Surface rules (DESIGN_SYSTEM.md):
 //   - page container = dark:bg-warm-900 (inherited)
 //   - card surface   = dark:bg-warm-800
 //   - elevated input = dark:bg-warm-700
 import type { ProxyListener, ProxyServer } from '@/lib/types/config';
 import type { ProxyListenerStatus } from '@/lib/store/proxy.svelte';
 import { proxyStore } from '@/lib/store/proxy.svelte';
 import { Circle, Plus, Trash2, Power, Cable, Network } from 'lucide-svelte';

 let {
  listeners,
  status,
  servers,
  canManage,
 }: {
  listeners: ProxyListener[];
  status: ProxyListenerStatus[];
  servers: ProxyServer[];
  canManage: boolean;
 } = $props();

 // editingId tracks which listener is open for editing in the
 // inline drawer. `null` collapses every card; "new" opens a fresh
 // draft.
 let editingId = $state<string | null>(null);
 let draft = $state<ProxyListener>(emptyDraft('http'));

 function emptyDraft(protocol: 'http' | 'tcp'): ProxyListener {
  return {
   id: '',
   name: protocol === 'tcp' ? 'New TCP listener' : 'New HTTP listener',
   enabled: false,
   protocol,
   host: '',
   port: protocol === 'tcp' ? '9091' : '9090',
   notes: '',
  };
 }

 function statusFor(id: string): ProxyListenerStatus | undefined {
  return status.find(s => s.id === id);
 }

 function graphsFor(id: string): ProxyServer[] {
  return servers.filter(s => (s.listener_id ?? '') === id);
 }

 function openNew(protocol: 'http' | 'tcp') {
  draft = emptyDraft(protocol);
  editingId = 'new';
 }

 function openEdit(ln: ProxyListener) {
  draft = { ...ln };
  editingId = ln.id;
 }

 function closeEditor() {
  editingId = null;
 }

 async function saveDraft() {
  try {
   if (editingId === 'new') {
    await proxyStore.createListener({ ...draft, id: '' });
   } else {
    await proxyStore.updateListener(draft);
   }
   closeEditor();
  } catch {/* toast in store */}
 }

 async function deleteListener(ln: ProxyListener) {
  if (!confirm(`Delete listener "${ln.name || ln.id}"? Any attached graphs must be moved first.`)) return;
  try { await proxyStore.removeListener(ln.id); } catch {/* toast in store */}
 }

 async function toggleEnabled(ln: ProxyListener, enabled: boolean) {
  try { await proxyStore.updateListener({ ...ln, enabled }); } catch {/* toast in store */}
 }
</script>

<div class="flex-1 overflow-y-auto p-6">
 <div class="max-w-6xl mx-auto">
  <header class="mb-6 flex items-start justify-between gap-4">
   <div>
    <h1 class="text-xl font-semibold text-slate-800 dark:text-slate-100 flex items-center gap-2">
     <Network size={20} class="text-accent-600 dark:text-accent-400" />
     Proxy listeners
    </h1>
    <p class="text-sm text-slate-500 dark:text-slate-400 mt-1">
     Configured bind points. Graphs attach to a listener via Host header (HTTP) or one-to-one (TCP).
    </p>
   </div>
   {#if canManage}
    <div class="flex flex-wrap items-center justify-end gap-2">
     <button
      type="button"
      class="px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 inline-flex items-center gap-1.5 cursor-pointer"
      onclick={() => openNew('http')}
     >
      <Plus size={12} /> New HTTP listener
     </button>
     <button
      type="button"
      class="px-3 py-1.5 text-xs rounded bg-slate-700 text-white hover:bg-slate-800 dark:bg-warm-700 dark:hover:bg-warm-600 dark:text-slate-100 inline-flex items-center gap-1.5 cursor-pointer"
      onclick={() => openNew('tcp')}
     >
      <Cable size={12} /> New TCP listener
     </button>
    </div>
   {/if}
  </header>

  {#if listeners.length === 0 && editingId !== 'new'}
   <div class="flex flex-col items-center justify-center py-16 px-6 text-center text-slate-400 dark:text-slate-500">
    <Network size={28} class="mb-3 opacity-40" />
    <div class="text-sm font-medium text-slate-600 dark:text-slate-300 mb-1">
     No listeners configured yet
    </div>
    <div class="text-xs mb-4 max-w-md">
     Create a listener to bind a port. Graphs attach to listeners by ID — one HTTP
     listener can host several graphs routed by Host header.
    </div>
   </div>
  {:else}
   <div class="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
    {#each listeners as ln (ln.id)}
     {@const st = statusFor(ln.id)}
     {@const attached = graphsFor(ln.id)}
     <div class="bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg p-4 flex flex-col">
      <div class="flex items-start gap-2 mb-2">
       <Circle
        size={10}
        class={st?.running
         ? 'text-emerald-500 fill-emerald-500 shrink-0 mt-1'
         : (st?.last_err
          ? 'text-vermilion-500 fill-vermilion-500 shrink-0 mt-1'
          : 'text-slate-400 fill-slate-400 shrink-0 mt-1')}
       />
       <div class="grow min-w-0">
        <button
         type="button"
         class="text-sm font-semibold text-slate-800 dark:text-slate-100 hover:text-accent-600 dark:hover:text-accent-400 truncate text-left cursor-pointer"
         onclick={() => openEdit(ln)}
        >
         {ln.name || ln.id}
        </button>
        <div class="text-xs text-slate-500 dark:text-slate-400 font-mono truncate">
         {st?.addr ?? `${ln.host || '0.0.0.0'}:${ln.port}`}
        </div>
       </div>
       <span class={'shrink-0 text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded ' + (ln.enabled
        ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300'
        : 'bg-slate-100 text-slate-600 dark:bg-warm-900 dark:text-slate-400')}>
        {ln.enabled ? 'enabled' : 'disabled'}
       </span>
       <span class={'shrink-0 text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded font-mono ' + (ln.protocol === 'tcp'
        ? 'bg-violet-100 text-violet-700 dark:bg-violet-950/40 dark:text-violet-300'
        : 'bg-accent-50 text-accent-700 dark:bg-accent-900/40 dark:text-accent-300')}>
        {ln.protocol}
       </span>
      </div>

      <div class="text-xs text-slate-600 dark:text-slate-300 mb-3">
       <strong>{attached.length}</strong> graph{attached.length === 1 ? '' : 's'} attached
       {#if attached.length > 0}
        <div class="mt-1 text-[11px] text-slate-500 dark:text-slate-400 truncate">
         {attached.map(g => g.name || g.id).join(', ')}
        </div>
       {/if}
      </div>

      {#if st?.last_err}
       <div class="mb-3 p-2 rounded text-xs bg-vermilion-50 dark:bg-vermilion-950/40 border border-vermilion-200 dark:border-vermilion-800 text-vermilion-800 dark:text-vermilion-200">
        {st.last_err}
       </div>
      {/if}

      <div class="mt-auto flex items-center justify-between gap-2 pt-2">
       <button
        type="button"
        class="text-xs text-accent-600 dark:text-accent-400 hover:text-accent-700 dark:hover:text-accent-300 cursor-pointer"
        onclick={() => openEdit(ln)}
       >Edit</button>
       {#if canManage}
        <div class="flex items-center gap-2">
         <button
          type="button"
          class={'px-2 py-1 rounded text-xs inline-flex items-center gap-1 cursor-pointer ' + (ln.enabled
           ? 'bg-slate-100 text-slate-700 hover:bg-slate-200 dark:bg-warm-900 dark:text-slate-300 dark:hover:bg-warm-700'
           : 'bg-emerald-600 text-white hover:bg-emerald-700')}
          onclick={() => toggleEnabled(ln, !ln.enabled)}
         >
          <Power size={12} /> {ln.enabled ? 'Deactivate' : 'Activate'}
         </button>
         <button
          type="button"
          class="px-2 py-1 rounded text-xs text-vermilion-700 dark:text-vermilion-300 hover:bg-vermilion-50 dark:hover:bg-vermilion-950/40 inline-flex items-center gap-1 cursor-pointer disabled:opacity-50"
          disabled={attached.length > 0}
          title={attached.length > 0 ? 'Detach attached graphs first' : 'Delete listener'}
          onclick={() => deleteListener(ln)}
         >
          <Trash2 size={12} />
         </button>
        </div>
       {/if}
      </div>
     </div>
    {/each}
   </div>
  {/if}

  {#if editingId !== null}
   <div class="mt-6 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg p-5">
    <h2 class="text-sm font-semibold text-slate-800 dark:text-slate-100 mb-3">
     {editingId === 'new' ? 'New listener' : 'Edit listener'}
    </h2>
    <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
     <label class="text-xs text-slate-600 dark:text-slate-300 flex flex-col gap-1">
      Name
      <input
       type="text"
       class="rounded border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-900 px-2 py-1 text-xs"
       bind:value={draft.name}
       placeholder="my-edge-http"
      />
     </label>
     <label class="text-xs text-slate-600 dark:text-slate-300 flex flex-col gap-1">
      Protocol
      <select
       class="rounded border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-900 px-2 py-1 text-xs"
       bind:value={draft.protocol}
      >
       <option value="http">http</option>
       <option value="tcp">tcp</option>
      </select>
     </label>
     <label class="text-xs text-slate-600 dark:text-slate-300 flex flex-col gap-1">
      Host (empty = inherit pika server host)
      <input
       type="text"
       class="rounded border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-900 px-2 py-1 text-xs font-mono"
       bind:value={draft.host}
       placeholder="0.0.0.0"
      />
     </label>
     <label class="text-xs text-slate-600 dark:text-slate-300 flex flex-col gap-1">
      Port
      <input
       type="text"
       class="rounded border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-900 px-2 py-1 text-xs font-mono"
       bind:value={draft.port}
       placeholder="9090"
      />
     </label>
     <label class="text-xs text-slate-600 dark:text-slate-300 flex flex-col gap-1 md:col-span-2">
      Notes
      <textarea
       class="rounded border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-900 px-2 py-1 text-xs"
       rows="2"
       bind:value={draft.notes}
      ></textarea>
     </label>
     <label class="text-xs text-slate-600 dark:text-slate-300 inline-flex items-center gap-2 md:col-span-2">
      <input type="checkbox" bind:checked={draft.enabled} />
      Enabled
     </label>
    </div>
    <div class="mt-4 flex items-center justify-end gap-2">
     <button
      type="button"
      class="px-3 py-1.5 text-xs rounded bg-slate-100 dark:bg-warm-900 text-slate-700 dark:text-slate-300 hover:bg-slate-200 dark:hover:bg-warm-700 cursor-pointer"
      onclick={closeEditor}
     >Cancel</button>
     <button
      type="button"
      class="px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 cursor-pointer"
      onclick={saveDraft}
      disabled={!canManage}
     >Save</button>
    </div>
   </div>
  {/if}
 </div>
</div>
