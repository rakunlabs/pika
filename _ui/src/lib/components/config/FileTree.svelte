<script lang="ts">
  import { Search, X, FolderPlus, FilePlus, RefreshCw, Loader2, StopCircle, FileText, File } from 'lucide-svelte';
  import FileTreeNode from './FileTreeNode.svelte';
  import CreateDialog from './CreateDialog.svelte';
  import { configStore } from '@/lib/store/config.svelte';
  import { onMount } from 'svelte';

  let searchInput = $state('');
  let isSearchFocused = $state(false);
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;

  let showCreateDialog = $state(false);
  let createType = $state<'file' | 'folder'>('file');
  let createParentPath = $state('');

  onMount(() => {
    configStore.loadTree();
  });

  function handleSearchInput() {
    if (debounceTimer) clearTimeout(debounceTimer);

    if (!searchInput.trim()) {
      configStore.clearSearch();
      return;
    }

    debounceTimer = setTimeout(() => {
      configStore.search(searchInput);
    }, 300);
  }

  function handleSearchKeyDown(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      // Immediate search on Enter
      if (debounceTimer) clearTimeout(debounceTimer);
      if (searchInput.trim()) {
        configStore.search(searchInput);
      }
    }
    if (e.key === 'Escape') {
      clearSearch();
    }
  }

  function clearSearch() {
    if (debounceTimer) clearTimeout(debounceTimer);
    searchInput = '';
    configStore.clearSearch();
  }

  function handleCreateFile(folderPath: string) {
    createParentPath = folderPath;
    createType = 'file';
    showCreateDialog = true;
  }

  function handleCreateFolder(folderPath: string) {
    createParentPath = folderPath;
    createType = 'folder';
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

  // Derived: separate name matches from content matches
  const nameResults = $derived(
    configStore.searchResults.filter(r => r.type === 'name')
  );
  const contentResults = $derived(
    configStore.searchResults.filter(r => r.type === 'content')
  );
  const hasResults = $derived(configStore.searchResults.length > 0);
  const isActive = $derived(searchInput.trim().length > 0);
</script>

<div class="flex flex-col h-full bg-slate-50 border-r border-slate-200 select-none">
  <!-- Header -->
  <div class="flex items-center justify-between px-3 py-2 bg-slate-100 border-b border-slate-200">
    <span class="text-xs font-semibold text-gray-700 uppercase tracking-wide">Explorer</span>
    <div class="flex gap-0.5">
      <button
        class="flex items-center justify-center w-6 h-6 text-gray-500 bg-transparent border-none cursor-pointer hover:text-gray-800 hover:bg-slate-200 rounded"
        onclick={() => handleCreateFile('')}
        title="New Config"
      >
        <FilePlus size={14} />
      </button>
      <button
        class="flex items-center justify-center w-6 h-6 text-gray-500 bg-transparent border-none cursor-pointer hover:text-gray-800 hover:bg-slate-200 rounded"
        onclick={() => handleCreateFolder('')}
        title="New Folder"
      >
        <FolderPlus size={14} />
      </button>
      <button
        class="flex items-center justify-center w-6 h-6 text-gray-500 bg-transparent border-none cursor-pointer hover:text-gray-800 hover:bg-slate-200 rounded"
        onclick={() => configStore.loadTree()}
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
        placeholder="Search configs..."
        bind:value={searchInput}
        oninput={handleSearchInput}
        onkeydown={handleSearchKeyDown}
        onfocus={() => isSearchFocused = true}
        onblur={() => isSearchFocused = false}
        class="flex-1 border-none outline-none text-xs bg-transparent min-w-0 placeholder:text-gray-400"
      />
      {#if configStore.isSearching}
        <button
          class="flex items-center justify-center p-0.5 rounded text-amber-500 bg-transparent border-none cursor-pointer hover:text-red-500 hover:bg-red-50"
          onclick={() => configStore.cancelSearch()}
          title="Stop search"
        >
          <StopCircle size={14} />
        </button>
      {:else if searchInput}
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
  {#if isActive && (hasResults || configStore.isSearching)}
    <div class="flex-1 overflow-y-auto">
      <!-- Status bar -->
      <div class="flex items-center justify-between px-3 py-1.5 text-[11px] text-slate-500 bg-slate-100 border-b border-slate-200">
        <span>
          {configStore.searchResults.length} result{configStore.searchResults.length !== 1 ? 's' : ''}
        </span>
        {#if configStore.isSearching}
          <span class="flex items-center gap-1 text-blue-500">
            <Loader2 size={10} class="animate-spin" />
            searching...
          </span>
        {/if}
      </div>

      <!-- Name matches -->
      {#if nameResults.length > 0}
        <div class="px-3 py-1 text-[10px] font-semibold text-slate-400 uppercase tracking-wider bg-slate-50 border-b border-slate-100">
          Config Names
        </div>
        {#each nameResults as result (result.path)}
          <button
            class="flex items-center gap-2 w-full px-3 py-1.5 text-left bg-transparent border-none border-b border-slate-100 cursor-pointer hover:bg-blue-50 transition-colors"
            onclick={() => configStore.openFile(result.path)}
          >
            <FileText size={13} class="text-slate-400 shrink-0" />
            <span class="text-xs text-gray-700 overflow-hidden text-ellipsis whitespace-nowrap">{result.path}</span>
          </button>
        {/each}
      {/if}

      <!-- Content matches -->
      {#if contentResults.length > 0}
        <div class="px-3 py-1 text-[10px] font-semibold text-slate-400 uppercase tracking-wider bg-slate-50 border-b border-slate-100">
          Content Matches
        </div>
        {#each contentResults as result (`${result.path}:${result.line}`)}
          <button
            class="flex flex-col w-full px-3 py-1.5 text-left bg-transparent border-none border-b border-slate-100 cursor-pointer hover:bg-blue-50 transition-colors"
            onclick={() => configStore.openFile(result.path)}
          >
            <div class="flex items-center gap-1.5">
              <File size={11} class="text-slate-400 shrink-0" />
              <span class="text-[11px] font-medium text-gray-700 overflow-hidden text-ellipsis whitespace-nowrap">{result.path}</span>
              <span class="text-[10px] text-slate-400 shrink-0">:{result.line}</span>
            </div>
            <span class="text-[11px] text-slate-500 overflow-hidden text-ellipsis whitespace-nowrap mt-0.5 pl-[19px]">{result.snippet}</span>
          </button>
        {/each}
      {/if}

      <!-- Searching indicator when no results yet -->
      {#if configStore.isSearching && !hasResults}
        <div class="flex items-center justify-center gap-2 py-8 text-slate-400 text-xs">
          <Loader2 size={16} class="animate-spin" />
          Searching...
        </div>
      {/if}

      <!-- No results after search completed -->
      {#if !configStore.isSearching && !hasResults && isActive}
        <div class="py-8 text-center text-slate-400 text-xs">
          No results for "{searchInput}"
        </div>
      {/if}
    </div>
  {:else if isActive && !configStore.isSearching && !hasResults}
    <div class="flex-1 flex items-start justify-center pt-8">
      <span class="text-xs text-slate-400">No results for "{searchInput}"</span>
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
