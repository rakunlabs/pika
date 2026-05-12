<script lang="ts">
 import { RefreshCw, HardDrive } from 'lucide-svelte';
 import RawFileTreeNode from './RawFileTreeNode.svelte';
 import { filesStore } from '@/lib/store/files.svelte';
 import { appStore } from '@/lib/store/store.svelte';
 import { onMount } from 'svelte';

 onMount(() => {
 const mounts = appStore.info?.raw_mounts ?? [];
 filesStore.openFromURL(mounts);
 });
</script>

<div class="flex flex-col h-full bg-slate-50 dark:bg-warm-900 border-r border-slate-200 dark:border-warm-700 select-none">
  <!-- Header -->
  <div class="flex items-center justify-between px-3 py-2 bg-slate-100 dark:bg-warm-800 border-b border-slate-200 dark:border-warm-700">
  <span class="text-xs font-semibold text-gray-700 dark:text-warm-100 uppercase tracking-wide flex items-center gap-1.5">
  <HardDrive size={12} class="text-brand-500" />
  Files
  </span>
  <div class="flex gap-0.5">
  <button
  class="flex items-center justify-center w-6 h-6 text-gray-500 dark:text-warm-200 bg-transparent border-none cursor-pointer hover:text-gray-800 dark:hover:text-white hover:bg-slate-200 dark:hover:bg-warm-600 rounded"
 onclick={() => {
 const mounts = appStore.info?.raw_mounts ?? [];
 filesStore.initTree(mounts);
 }}
 title="Refresh all"
 aria-label="Refresh all mounts"
 >
 <RefreshCw size={14} />
 </button>
 </div>
 </div>

 <!-- Tree -->
 <div class="flex-1 overflow-y-auto py-1" role="tree">
 {#if filesStore.tree.length > 0}
 {#each filesStore.tree as node (`${node.type}:${node.path}`)}
 <RawFileTreeNode {node} />
 {/each}
 {:else}
 <div class="p-5 text-center text-gray-400 dark:text-slate-500 text-[13px]">No mounts configured</div>
 {/if}
 </div>
</div>
