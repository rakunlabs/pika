<script lang="ts">
 // Left rail listing every ProxyServer in the deployment. The
 // active entry is highlighted with the canonical "active nav
 // entry" pattern from DESIGN_SYSTEM §4. The Dashboard row at
 // the top is special — it switches the main pane back to the
 // overview / test view instead of the editor.
 import type { ProxyServer } from '@/lib/types/config';
 import type { ProxyInstanceStatus } from '@/lib/store/proxy.svelte';
 import { Trash2, Circle, LayoutDashboard } from 'lucide-svelte';

 type ProxyProtocol = 'http' | 'tcp';

 let {
  servers,
  status,
  activeId,
  view,
  canManage,
  onSelectDashboard,
  onSelect,
  onDelete,
 }: {
  servers: ProxyServer[];
  status: ProxyInstanceStatus[];
  activeId: string | null;
  view: 'dashboard' | 'editor';
  canManage: boolean;
  onSelectDashboard: () => void;
  onSelect: (id: string) => void;
  onDelete: (id: string) => void;
 } = $props();

 function statusFor(id: string): ProxyInstanceStatus | undefined {
  return status.find(s => s.id === id);
 }

 function protocolFor(srv: ProxyServer): ProxyProtocol {
  return (srv.protocol ?? 'http') as ProxyProtocol;
 }

 // Active state classes per DESIGN_SYSTEM §4 "Active nav-item".
 const activeRow =
  'bg-accent-50 text-accent-700 border border-accent-200 ' +
  'dark:bg-accent-900/40 dark:text-accent-300 dark:border-accent-700';
 const inactiveRow =
  'border border-transparent ' +
  'text-slate-600 dark:text-warm-200 ' +
  'hover:bg-slate-100 dark:hover:bg-warm-700 ' +
  'hover:text-slate-800 dark:hover:text-white';
</script>

<aside class="w-60 shrink-0 border-r border-slate-200 dark:border-warm-700
              bg-white dark:bg-warm-800 overflow-y-auto">
 <header class="flex items-center justify-between p-3 border-b border-slate-200 dark:border-warm-700">
  <h2 class="text-sm font-semibold text-slate-800 dark:text-slate-100">Proxy</h2>
 </header>

 <div class="p-2">
  <button
   type="button"
   class="w-full flex items-center gap-2 px-2 py-1.5 rounded text-sm cursor-pointer
          {view === 'dashboard' ? activeRow : inactiveRow}"
   onclick={onSelectDashboard}
  >
   <LayoutDashboard size={14} />
   <span class="grow text-left">Dashboard</span>
  </button>
 </div>

 <div class="px-3 pt-2 pb-1 text-[10px] uppercase tracking-wide text-slate-500 dark:text-slate-400">
  Servers
 </div>

 {#if servers.length === 0}
  <p class="px-3 py-4 text-xs text-slate-400 dark:text-slate-500 text-center">
   No proxy servers yet.{#if canManage}<br />Use the dashboard to create one.{/if}
  </p>
 {/if}

 <ul class="px-2 pb-2 space-y-1">
  {#each servers as srv (srv.id)}
   {@const st = statusFor(srv.id)}
   {@const isActive = view === 'editor' && srv.id === activeId}
   {@const protocol = protocolFor(srv)}
   <li>
     <div class="flex items-stretch gap-1 rounded {isActive ? activeRow : inactiveRow}">
     <button
      type="button"
      class="grow text-left px-2 py-1.5 min-w-0 cursor-pointer"
      onclick={() => onSelect(srv.id)}
     >
      <div class="flex items-center gap-2">
       <Circle
        size={8}
        class={st?.running
         ? 'text-emerald-500 fill-emerald-500'
         : (st?.last_err
            ? 'text-vermilion-500 fill-vermilion-500'
            : 'text-slate-400 fill-slate-400')}
       />
       <div class="grow min-w-0">
        <div class="text-sm font-medium truncate">{srv.name || srv.id}</div>
        <div class="text-[10px] opacity-70 font-mono truncate">
         {protocol}:{srv.port}{st?.addr ? ` · ${st.addr}` : ''}
        </div>
       </div>
      </div>
      {#if st?.last_err}
       <div class="text-[10px] text-vermilion-600 dark:text-vermilion-400 mt-0.5 truncate" title={st.last_err}>
        {st.last_err}
       </div>
      {/if}
      </button>
      {#if canManage}
       <div class="flex items-center p-1.5">
        <button
         type="button"
         aria-label="Delete proxy server"
        title="Delete"
        class="p-1 rounded text-vermilion-600 dark:text-vermilion-400
               hover:bg-vermilion-50 dark:hover:bg-vermilion-950/40 cursor-pointer"
        onclick={() => onDelete(srv.id)}
       >
        <Trash2 size={12} />
       </button>
      </div>
     {/if}
    </div>
   </li>
  {/each}
 </ul>
</aside>
