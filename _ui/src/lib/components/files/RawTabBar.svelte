<script lang="ts">
 import { X, FileText, XCircle } from 'lucide-svelte';
 import { filesStore } from '@/lib/store/files.svelte';

 // Context menu state
 let contextMenu = $state<{ x: number; y: number; tabId: string } | null>(null);

 function handleCloseTab(e: MouseEvent, tabId: string) {
 e.stopPropagation();
 filesStore.closeTab(tabId);
 }

 function handleMiddleClick(e: MouseEvent, tabId: string) {
 if (e.button === 1) {
 e.preventDefault();
 handleCloseTab(e, tabId);
 }
 }

 function handleContextMenu(e: MouseEvent, tabId: string) {
 e.preventDefault();
 e.stopPropagation();
 contextMenu = { x: e.clientX, y: e.clientY, tabId };
 }

 function closeContextMenu() {
 contextMenu = null;
 }

 function ctxClose() {
 if (contextMenu) filesStore.closeTab(contextMenu.tabId);
 closeContextMenu();
 }

 function ctxCloseOthers() {
 if (contextMenu) filesStore.closeOtherTabs(contextMenu.tabId);
 closeContextMenu();
 }

 function ctxCloseToRight() {
 if (contextMenu) filesStore.closeTabsToRight(contextMenu.tabId);
 closeContextMenu();
 }

 function ctxCloseToLeft() {
 if (contextMenu) filesStore.closeTabsToLeft(contextMenu.tabId);
 closeContextMenu();
 }

 function ctxCloseAll() {
 filesStore.closeAllTabs();
 closeContextMenu();
 }

 // Close context menu when clicking elsewhere
 function handleWindowClick() {
 if (contextMenu) closeContextMenu();
 }

 const hasMultipleTabs = $derived(filesStore.openTabs.length > 1);
 // Local non-null binding so TS narrowing carries through the findIndex
 // closure. The outer `contextMenu ? ... : -1` already gates on
 // truthiness, but TS treats the closure capture as a fresh read of
 // the variable (which is again possibly null) — pinning to a local
 // makes the closure see a non-null `cm`.
 const ctxTabIndex = $derived.by(() => {
 const cm = contextMenu;
 if (!cm) return -1;
 return filesStore.openTabs.findIndex(t => t.id === cm.tabId);
 });
 const hasTabsToRight = $derived(ctxTabIndex >= 0 && ctxTabIndex < filesStore.openTabs.length - 1);
 const hasTabsToLeft = $derived(ctxTabIndex > 0);
</script>

<svelte:window onclick={handleWindowClick} />

<div class="flex items-stretch bg-slate-100 dark:bg-warm-800 border-b border-slate-200 dark:border-warm-700 min-h-[35px] overflow-hidden">
  {#if filesStore.openTabs.length === 0}
  <div class="flex items-center px-4 text-gray-400 dark:text-slate-500 text-[13px]">No files open</div>
  {:else}
  <div class="flex items-stretch overflow-x-auto overflow-y-hidden flex-1 scrollbar-thin scrollbar-track-slate-200 scrollbar-thumb-slate-400">
  {#each filesStore.openTabs as tab (tab.id)}
  {@const isActive = filesStore.activeTabId === tab.id}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
  class="group flex items-center gap-1.5 pl-3 pr-2 bg-transparent border-none border-r border-slate-200 dark:border-warm-700 cursor-pointer text-[13px] whitespace-nowrap min-w-0 max-w-[200px] transition-colors
  {isActive
    ? 'bg-white dark:bg-warm-900 text-slate-800 dark:text-white border-b-2 border-b-accent-600 -mb-px'
    : 'text-slate-500 dark:text-warm-200 hover:bg-slate-200 dark:hover:bg-warm-700'}"
 onclick={() => filesStore.selectTab(tab.id)}
 onauxclick={(e) => handleMiddleClick(e, tab.id)}
 oncontextmenu={(e) => handleContextMenu(e, tab.id)}
 onkeydown={(e) => e.key === 'Enter' && filesStore.selectTab(tab.id)}
 title="{tab.mount}/{tab.path}"
 role="tab"
 tabindex="0"
 aria-selected={isActive}
 >
 <span class="flex items-center shrink-0 {isActive ? 'text-gray-500 dark:text-slate-400' : 'text-gray-400 dark:text-slate-500'}">
 <FileText size={14} />
 </span>
 <span class="overflow-hidden text-ellipsis">{tab.name}</span>
 <button
 class="flex items-center justify-center p-0.5 rounded text-gray-400 dark:text-slate-500 bg-transparent border-none cursor-pointer transition-all
 opacity-0 group-hover:opacity-100
 hover:bg-red-600 hover:text-white"
 onclick={(e) => handleCloseTab(e, tab.id)}
 aria-label="Close tab"
 >
 <X size={14} />
 </button>
 </div>
 {/each}
 </div>

 <!-- Close All button -->
 <button
 class="flex items-center justify-center px-2 shrink-0 text-gray-400 dark:text-slate-500 bg-transparent border-none border-l border-slate-200 dark:border-warm-700 cursor-pointer transition-colors hover:text-red-500 hover:bg-slate-200 dark:hover:bg-warm-700"
 onclick={() => filesStore.closeAllTabs()}
 title="Close all tabs"
 aria-label="Close all tabs"
 >
 <XCircle size={15} />
 </button>
 {/if}
</div>

<!-- Context Menu -->
{#if contextMenu}
 <!-- svelte-ignore a11y_no_static_element_interactions -->
 <div
 role="menu"
 tabindex="-1"
 class="fixed z-50 min-w-44 py-1 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-xl text-[13px]"
 style="left: {contextMenu.x}px; top: {contextMenu.y}px;"
 onclick={(e) => e.stopPropagation()}
 onkeydown={(e) => e.stopPropagation()}
 >
 <button
 class="flex items-center w-full px-3 py-1.5 text-left text-slate-700 dark:text-slate-200 bg-transparent border-none cursor-pointer hover:bg-slate-100 dark:hover:bg-warm-700 transition-colors"
 onclick={ctxClose}
 >
 Close
 </button>
 <button
 class="flex items-center w-full px-3 py-1.5 text-left bg-transparent border-none cursor-pointer transition-colors
 {hasMultipleTabs ? 'text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700' : 'text-slate-300 cursor-default'}"
 onclick={ctxCloseOthers}
 disabled={!hasMultipleTabs}
 >
 Close Others
 </button>
 <button
 class="flex items-center w-full px-3 py-1.5 text-left bg-transparent border-none cursor-pointer transition-colors
 {hasTabsToRight ? 'text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700' : 'text-slate-300 cursor-default'}"
 onclick={ctxCloseToRight}
 disabled={!hasTabsToRight}
 >
 Close to the Right
 </button>
 <button
 class="flex items-center w-full px-3 py-1.5 text-left bg-transparent border-none cursor-pointer transition-colors
 {hasTabsToLeft ? 'text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700' : 'text-slate-300 cursor-default'}"
 onclick={ctxCloseToLeft}
 disabled={!hasTabsToLeft}
 >
 Close to the Left
 </button>
 <div class="my-1 border-t border-slate-150 dark:border-warm-700"></div>
 <button
 class="flex items-center w-full px-3 py-1.5 text-left text-red-600 bg-transparent border-none cursor-pointer hover:bg-red-50 transition-colors"
 onclick={ctxCloseAll}
 >
 Close All
 </button>
 </div>
{/if}
