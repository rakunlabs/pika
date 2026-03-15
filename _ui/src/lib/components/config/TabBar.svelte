<script lang="ts">
  import { X, FileJson, FileCode, File, FileText } from 'lucide-svelte';
  import { configStore } from '@/lib/store/config.svelte';
  import type { Tab, FileFormat } from '@/lib/types/config';

  function getFormatIcon(format: FileFormat) {
    switch (format) {
      case 'json':
        return FileJson;
      case 'yaml':
      case 'toml':
        return FileCode;
      default:
        return FileText;
    }
  }

  function handleTabClick(tab: Tab) {
    configStore.selectTab(tab.id);
  }

  function handleCloseTab(e: MouseEvent, tab: Tab) {
    e.stopPropagation();
    if (tab.isDirty) {
      if (!confirm(`"${tab.name}" has unsaved changes. Close anyway?`)) {
        return;
      }
    }
    configStore.closeTab(tab.id);
  }

  function handleMiddleClick(e: MouseEvent, tab: Tab) {
    if (e.button === 1) {
      e.preventDefault();
      handleCloseTab(e, tab);
    }
  }
</script>

<div class="flex items-stretch bg-slate-100 border-b border-slate-200 min-h-[35px] overflow-hidden">
  {#if configStore.openTabs.length === 0}
    <div class="flex items-center px-4 text-gray-400 text-[13px]">No files open</div>
  {:else}
    <div class="flex items-stretch overflow-x-auto overflow-y-hidden flex-1 scrollbar-thin scrollbar-track-slate-200 scrollbar-thumb-slate-400">
      {#each configStore.openTabs as tab (tab.id)}
        {@const FileIcon = getFormatIcon(tab.format)}
        {@const isActive = configStore.activeTabId === tab.id}
        <div
          class="group flex items-center gap-1.5 pl-3 pr-2 bg-transparent border-none border-r border-slate-200 cursor-pointer text-[13px] whitespace-nowrap min-w-0 max-w-[200px] transition-colors
            {isActive ? 'bg-white text-slate-800 border-b-2 border-b-blue-500 -mb-px' : 'text-slate-500 hover:bg-slate-200'}"
          onclick={() => handleTabClick(tab)}
          onauxclick={(e) => handleMiddleClick(e, tab)}
          onkeydown={(e) => e.key === 'Enter' && handleTabClick(tab)}
          title={tab.path}
          role="tab"
          tabindex="0"
          aria-selected={isActive}
        >
          <span class="flex items-center shrink-0 {isActive ? 'text-gray-500' : 'text-gray-400'}">
            <FileIcon size={14} />
          </span>
          <span class="overflow-hidden text-ellipsis">{tab.name}</span>
          {#if tab.isDirty}
            <span class="w-2 h-2 rounded-full bg-amber-500 shrink-0 group-hover:hidden"></span>
          {/if}
          <button
            class="flex items-center justify-center p-0.5 rounded text-gray-400 bg-transparent border-none cursor-pointer transition-all
              {tab.isDirty ? 'opacity-0 group-hover:opacity-100' : 'opacity-0 group-hover:opacity-100'}
              hover:bg-red-600 hover:text-white"
            onclick={(e) => handleCloseTab(e, tab)}
            aria-label="Close tab"
          >
            <X size={14} />
          </button>
        </div>
      {/each}
    </div>
  {/if}
</div>
