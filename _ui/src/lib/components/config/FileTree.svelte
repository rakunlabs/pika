<script lang="ts">
  import { Search, X, FolderPlus, FilePlus, RefreshCw } from 'lucide-svelte';
  import FileTreeNode from './FileTreeNode.svelte';
  import CreateDialog from './CreateDialog.svelte';
  import { configStore } from '@/lib/store/config.svelte';
  import { onMount } from 'svelte';

  let searchInput = $state('');
  let isSearchFocused = $state(false);
  
  let showCreateDialog = $state(false);
  let createType = $state<'file' | 'folder'>('file');
  let createParentPath = $state('');

  onMount(() => {
    configStore.loadTree();
  });

  function handleSearch() {
    configStore.searchInContent(searchInput);
  }

  function clearSearch() {
    searchInput = '';
    configStore.clearSearch();
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      handleSearch();
    } else if (e.key === 'Escape') {
      clearSearch();
    }
  }

  function refresh() {
    configStore.loadTree();
  }

  function openCreateDialog(type: 'file' | 'folder') {
    createType = type;
    createParentPath = '';
    showCreateDialog = true;
  }

  function handleCreateFile(folderPath: string) {
    createType = 'file';
    createParentPath = folderPath;
    showCreateDialog = true;
  }

  function handleCreateFolder(folderPath: string) {
    createType = 'folder';
    createParentPath = folderPath;
    showCreateDialog = true;
  }

  function closeCreateDialog() {
    showCreateDialog = false;
  }

  async function handleCreatePath(fullPath: string, type: 'file' | 'folder') {
    const parts = fullPath.split('/');
    const name = parts.pop() || '';
    const parentPath = parts.join('/');
    
    if (type === 'folder') {
      await configStore.createNewFolder(parentPath, name);
    } else {
      await configStore.createNewFile(parentPath, name);
    }
  }
</script>

<div class="flex flex-col h-full bg-slate-50 border-r border-slate-200">
  <!-- Header -->
  <div class="flex items-center justify-between px-3 py-2 border-b border-slate-200 bg-slate-100">
    <span class="text-[11px] font-semibold uppercase text-slate-500 tracking-wide">Explorer</span>
    <div class="flex gap-1">
      <button 
        class="flex items-center justify-center w-5.5 h-5.5 rounded text-slate-500 bg-transparent border-none cursor-pointer hover:bg-slate-200 hover:text-gray-700"
        onclick={() => openCreateDialog('file')}
        title="New File"
        aria-label="Create new file"
      >
        <FilePlus size={14} />
      </button>
      <button 
        class="flex items-center justify-center w-5.5 h-5.5 rounded text-slate-500 bg-transparent border-none cursor-pointer hover:bg-slate-200 hover:text-gray-700"
        onclick={() => openCreateDialog('folder')}
        title="New Folder"
        aria-label="Create new folder"
      >
        <FolderPlus size={14} />
      </button>
      <button 
        class="flex items-center justify-center w-5.5 h-5.5 rounded text-slate-500 bg-transparent border-none cursor-pointer hover:bg-slate-200 hover:text-gray-700"
        onclick={refresh}
        title="Refresh"
        aria-label="Refresh tree"
      >
        <RefreshCw size={14} />
      </button>
    </div>
  </div>

  <!-- Search -->
  <div class="p-2 border-b border-slate-200">
    <div class="flex items-center gap-1.5 px-2 py-1 bg-white border border-slate-200 rounded transition-all
      {isSearchFocused ? 'border-blue-500 ring-2 ring-blue-500/10' : ''}">
      <Search size={14} class="text-gray-400 shrink-0" />
      <input
        type="text"
        placeholder="Search in files..."
        bind:value={searchInput}
        onkeydown={handleKeyDown}
        onfocus={() => isSearchFocused = true}
        onblur={() => isSearchFocused = false}
        class="flex-1 border-none outline-none text-xs bg-transparent min-w-0 placeholder:text-gray-400"
      />
      {#if searchInput}
        <button 
          class="flex items-center justify-center p-0.5 rounded text-gray-400 bg-transparent border-none cursor-pointer hover:text-gray-700 hover:bg-slate-100"
          onclick={clearSearch} 
          aria-label="Clear search"
        >
          <X size={14} />
        </button>
      {/if}
    </div>
  </div>

  <!-- Search Results -->
  {#if configStore.searchResults.length > 0}
    <div class="flex-1 overflow-y-auto">
      <div class="px-3 py-1.5 text-[11px] text-slate-500 bg-slate-100 border-b border-slate-200">
        {configStore.searchResults.length} result{configStore.searchResults.length !== 1 ? 's' : ''}
      </div>
      {#each configStore.searchResults as result (result.path + ':' + result.line)}
        <button
          class="flex flex-col w-full px-3 py-1.5 text-left bg-transparent border-none border-b border-slate-100 cursor-pointer hover:bg-gray-200"
          onclick={() => configStore.openFile(result.path)}
        >
          <span class="text-xs font-medium text-gray-700">{result.path}</span>
          <span class="text-[11px] text-slate-500">:{result.line}</span>
          <span class="text-[11px] text-gray-500 overflow-hidden text-ellipsis whitespace-nowrap mt-0.5">{result.content}</span>
        </button>
      {/each}
    </div>
  {:else}
    <!-- Tree -->
    <div class="flex-1 overflow-y-auto py-1" role="tree">
      {#if configStore.isLoading}
        <div class="p-5 text-center text-gray-400 text-[13px]">Loading...</div>
      {:else if configStore.tree}
        {#if configStore.tree.children && configStore.tree.children.length > 0}
          {#each configStore.tree.children as node (node.path)}
            <FileTreeNode 
              {node} 
              onCreateFile={handleCreateFile}
              onCreateFolder={handleCreateFolder}
            />
          {/each}
        {:else}
          <div class="p-5 text-center text-gray-400 text-[13px]">No files found</div>
        {/if}
      {:else}
        <div class="p-5 text-center text-gray-400 text-[13px]">No configurations</div>
      {/if}
    </div>
  {/if}
</div>

<!-- Create Dialog -->
<CreateDialog
  isOpen={showCreateDialog}
  type={createType}
  parentPath={createParentPath}
  onClose={closeCreateDialog}
  onCreatePath={handleCreatePath}
/>
