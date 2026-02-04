<script lang="ts">
  import { ChevronRight, ChevronDown, Folder, FolderOpen, File, FileJson, FileCode, FilePlus, FolderPlus, RefreshCw } from 'lucide-svelte';
  import type { TreeNode } from '@/lib/types/config';
  import { configStore } from '@/lib/store/config.svelte';
  import FileTreeNode from './FileTreeNode.svelte';

  interface Props {
    node: TreeNode;
    level?: number;
    onCreateFile?: (folderPath: string) => void;
    onCreateFolder?: (folderPath: string) => void;
  }

  let { node, level = 0, onCreateFile, onCreateFolder }: Props = $props();

  let isHovered = $state(false);

  function getFileIcon(filename: string) {
    const ext = filename.split('.').pop()?.toLowerCase();
    switch (ext) {
      case 'json':
        return FileJson;
      case 'yaml':
      case 'yml':
      case 'toml':
        return FileCode;
      default:
        return File;
    }
  }

  function handleClick() {
    if (node.type === 'folder') {
      configStore.toggleFolder(node);
    } else {
      configStore.openFile(node.path);
    }
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      handleClick();
    }
  }

  function handleCreateFile(e: MouseEvent) {
    e.stopPropagation();
    if (onCreateFile) {
      onCreateFile(node.path);
    }
  }

  function handleCreateFolder(e: MouseEvent) {
    e.stopPropagation();
    if (onCreateFolder) {
      onCreateFolder(node.path);
    }
  }

  async function handleRefresh(e: MouseEvent) {
    e.stopPropagation();
    if (node.type === 'folder') {
      node.loaded = false;
      await configStore.expandFolder(node);
    }
  }

  const isActive = $derived(configStore.activeTabId === node.path);
  const isOpen = $derived(configStore.openTabs.some(t => t.path === node.path));
  const FileIcon = $derived(getFileIcon(node.name));
</script>

<div class="select-none">
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="flex items-center gap-1 py-0.5 pr-2 cursor-pointer rounded text-[13px] text-gray-700 hover:bg-gray-200
      {isActive ? 'bg-blue-500 text-white' : ''} 
      {isOpen && !isActive ? 'text-blue-500' : ''}"
    style="padding-left: {level * 12 + 4}px"
    onclick={handleClick}
    onkeydown={handleKeyDown}
    onmouseenter={() => isHovered = true}
    onmouseleave={() => isHovered = false}
    role="treeitem"
    tabindex="0"
    aria-expanded={node.type === 'folder' ? node.expanded : undefined}
    aria-selected={isActive}
  >
    {#if node.type === 'folder'}
      <span class="flex items-center justify-center shrink-0 w-3.5 opacity-60">
        {#if node.expanded}
          <ChevronDown size={14} />
        {:else}
          <ChevronRight size={14} />
        {/if}
      </span>
      <span class="flex items-center justify-center shrink-0 {isActive ? 'text-white' : 'text-amber-500'}">
        {#if node.expanded}
          <FolderOpen size={14} />
        {:else}
          <Folder size={14} />
        {/if}
      </span>
    {:else}
      <span class="w-3.5 shrink-0"></span>
      <span class="flex items-center justify-center shrink-0 {isActive ? 'text-white' : 'text-gray-500'}">
        <FileIcon size={14} />
      </span>
    {/if}
    <span class="flex-1 overflow-hidden text-ellipsis whitespace-nowrap" title={node.path}>{node.name}</span>
    
    {#if node.type === 'folder' && isHovered}
      <span class="flex gap-0.5 shrink-0">
        <button 
          class="flex items-center justify-center w-4.5 h-4.5 rounded p-0 border-none cursor-pointer
            {isActive ? 'text-white/70 hover:bg-white/20 hover:text-white' : 'text-slate-500 bg-transparent hover:bg-slate-300 hover:text-gray-700'}"
          onclick={handleCreateFile}
          title="New File"
        >
          <FilePlus size={12} />
        </button>
        <button 
          class="flex items-center justify-center w-4.5 h-4.5 rounded p-0 border-none cursor-pointer
            {isActive ? 'text-white/70 hover:bg-white/20 hover:text-white' : 'text-slate-500 bg-transparent hover:bg-slate-300 hover:text-gray-700'}"
          onclick={handleCreateFolder}
          title="New Folder"
        >
          <FolderPlus size={12} />
        </button>
        <button 
          class="flex items-center justify-center w-4.5 h-4.5 rounded p-0 border-none cursor-pointer
            {isActive ? 'text-white/70 hover:bg-white/20 hover:text-white' : 'text-slate-500 bg-transparent hover:bg-slate-300 hover:text-gray-700'}"
          onclick={handleRefresh}
          title="Refresh"
        >
          <RefreshCw size={12} />
        </button>
      </span>
    {/if}
    
    {#if isOpen && !isActive && !isHovered}
      <span class="w-1.5 h-1.5 rounded-full bg-blue-500 shrink-0"></span>
    {/if}
  </div>

  {#if node.type === 'folder' && node.expanded && node.children}
    <div role="group">
      {#each node.children as child (child.path)}
        <FileTreeNode node={child} level={level + 1} {onCreateFile} {onCreateFolder} />
      {/each}
    </div>
  {/if}
</div>
