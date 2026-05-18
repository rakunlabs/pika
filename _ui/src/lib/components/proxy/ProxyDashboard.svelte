<script lang="ts">
 // Dashboard view — the landing pane when /proxy first opens.
 // Shows every configured server as a card with live status,
 // listener address and a "Run a test" shortcut. Clicking the
 // card title or "Open editor" switches the main pane to the
 // node-graph view for that server.
 //
 // The grid is purely informational; mutations (create / delete
 // / enable) live in the sidebar so the dashboard stays a clean
 // overview.
 import type { ProxyServer } from '@/lib/types/config';
 import type { ProxyInstanceStatus } from '@/lib/store/proxy.svelte';
 import { Network, Circle, ExternalLink, PlayCircle, Plus } from 'lucide-svelte';

 let {
  servers,
  status,
  canManage,
  onOpenEditor,
  onAdd,
  onTestServer,
 }: {
  servers: ProxyServer[];
  status: ProxyInstanceStatus[];
  canManage: boolean;
  onOpenEditor: (id: string) => void;
  onAdd: () => void;
  onTestServer: (id: string) => void;
 } = $props();

 function statusFor(id: string): ProxyInstanceStatus | undefined {
  return status.find(s => s.id === id);
 }

 function nodeCount(srv: ProxyServer): { mw: number; handlers: number } {
  let mw = 0, handlers = 0;
  for (const n of srv.nodes ?? []) {
   if (n.type === 'middleware') mw++;
   else if (n.type === 'handler') handlers++;
  }
  return { mw, handlers };
 }

 // Quick aggregate for the summary strip at the top.
 const summary = $derived.by(() => {
  let total = servers.length;
  let running = 0;
  let stopped = 0;
  let errored = 0;
  for (const s of servers) {
   const st = statusFor(s.id);
   if (st?.running) running++;
   else if (st?.last_err) errored++;
   else stopped++;
  }
  return { total, running, stopped, errored };
 });
</script>

<div class="flex-1 overflow-y-auto p-6">
 <div class="max-w-6xl mx-auto">
  <header class="mb-6 flex items-start justify-between gap-4">
   <div>
    <h1 class="text-xl font-semibold text-slate-800 dark:text-slate-100 flex items-center gap-2">
     <Network size={20} class="text-accent-600 dark:text-accent-400" />
     Proxy dashboard
    </h1>
    <p class="text-sm text-slate-500 dark:text-slate-400 mt-1">
     Every configured proxy server, its current listener address and a shortcut to the editor or the test console.
    </p>
   </div>
   {#if canManage}
    <button
     type="button"
     class="px-3 py-1.5 text-xs rounded
            bg-accent-600 text-white font-medium hover:bg-accent-700
            inline-flex items-center gap-1.5 cursor-pointer"
     onclick={onAdd}
    >
     <Plus size={12} /> New proxy server
    </button>
   {/if}
  </header>

  <!-- Summary strip -->
  <div class="grid grid-cols-2 md:grid-cols-4 gap-3 mb-6">
   <div class="bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg p-3">
    <div class="text-[10px] uppercase tracking-wide text-slate-500 dark:text-slate-400">Total</div>
    <div class="text-2xl font-semibold text-slate-800 dark:text-slate-100">{summary.total}</div>
   </div>
   <div class="bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg p-3">
    <div class="text-[10px] uppercase tracking-wide text-slate-500 dark:text-slate-400">Running</div>
    <div class="text-2xl font-semibold text-emerald-600 dark:text-emerald-400">{summary.running}</div>
   </div>
   <div class="bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg p-3">
    <div class="text-[10px] uppercase tracking-wide text-slate-500 dark:text-slate-400">Stopped</div>
    <div class="text-2xl font-semibold text-slate-600 dark:text-slate-300">{summary.stopped}</div>
   </div>
   <div class="bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg p-3">
    <div class="text-[10px] uppercase tracking-wide text-slate-500 dark:text-slate-400">Errored</div>
    <div class="text-2xl font-semibold text-vermilion-600 dark:text-vermilion-400">{summary.errored}</div>
   </div>
  </div>

  {#if servers.length === 0}
   <div class="flex flex-col items-center justify-center py-16 px-6 text-center
               text-slate-400 dark:text-slate-500">
    <Network size={28} class="mb-3 opacity-40" />
    <div class="text-sm font-medium text-slate-600 dark:text-slate-300 mb-1">
     No proxy servers yet
    </div>
    <div class="text-xs mb-4">
     Build a custom HTTP listener from a graph of middlewares and handlers.
     Each server gets its own port and can mount /data, /raw, /external, a Consul KV shim,
     a reverse proxy and more.
    </div>
    {#if canManage}
     <button
      type="button"
      class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs rounded
             bg-accent-600 text-white font-medium hover:bg-accent-700 cursor-pointer"
      onclick={onAdd}
     >
      <Plus size={12} /> Create your first proxy server
     </button>
    {/if}
   </div>
  {:else}
   <!-- Server grid -->
   <div class="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
    {#each servers as srv (srv.id)}
     {@const st = statusFor(srv.id)}
     {@const counts = nodeCount(srv)}
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
         onclick={() => onOpenEditor(srv.id)}
        >
         {srv.name || srv.id}
        </button>
        <div class="text-xs text-slate-500 dark:text-slate-400 font-mono truncate">
         {st?.addr ?? `:${srv.port}`}
        </div>
       </div>
       <span class={'shrink-0 text-[10px] uppercase tracking-wide px-1.5 py-0.5 rounded ' + (srv.enabled
        ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300'
        : 'bg-slate-100 text-slate-600 dark:bg-warm-900 dark:text-slate-400')}>
        {srv.enabled ? 'enabled' : 'disabled'}
       </span>
      </div>

      <div class="flex items-center gap-4 text-xs text-slate-600 dark:text-slate-300 mb-3">
       <span><strong>{counts.mw}</strong> middleware{counts.mw === 1 ? '' : 's'}</span>
       <span><strong>{counts.handlers}</strong> handler{counts.handlers === 1 ? '' : 's'}</span>
      </div>

      {#if st?.last_err}
       <div class="mb-3 p-2 rounded text-xs
                   bg-vermilion-50 dark:bg-vermilion-950/40
                   border border-vermilion-200 dark:border-vermilion-800
                   text-vermilion-800 dark:text-vermilion-200">
        {st.last_err}
       </div>
      {/if}

      <div class="mt-auto flex items-center justify-between gap-2 pt-2">
       <button
        type="button"
        class="text-xs text-accent-600 dark:text-accent-400 hover:text-accent-700 dark:hover:text-accent-300 inline-flex items-center gap-1 cursor-pointer"
        onclick={() => onOpenEditor(srv.id)}
       >
        <ExternalLink size={12} /> Open editor
       </button>
       {#if st?.running}
        <button
         type="button"
         class="text-xs text-slate-600 dark:text-slate-300 hover:text-slate-800 dark:hover:text-slate-100 inline-flex items-center gap-1 cursor-pointer"
         onclick={() => onTestServer(srv.id)}
        >
         <PlayCircle size={12} /> Test
        </button>
       {/if}
      </div>
     </div>
    {/each}
   </div>
  {/if}
 </div>
</div>
