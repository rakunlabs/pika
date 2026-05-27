<script lang="ts">
  import {
    ChevronRight,
    ChevronDown,
    Folder,
    FolderOpen,
    FileText,
    FilePlus,
    FolderPlus,
    RefreshCw,
    Layers,
    Trash2,
  } from "lucide-svelte";
  import type { TreeNode } from "@/lib/types/config";
  import { configStore } from "@/lib/store/config.svelte";
  import FileTreeNode from "./FileTreeNode.svelte";

  interface Props {
    node: TreeNode;
    level?: number;
    onCreateFile?: (folderPath: string) => void;
    onCreateFolder?: (folderPath: string) => void;
  }

  let { node, level = 0, onCreateFile, onCreateFolder }: Props = $props();

  let isHovered = $state(false);
  let showVariants = $state(true);

  function handleClick() {
    if (node.type === "folder") {
      configStore.toggleFolder(node);
    } else if (node.type === "variant") {
      if (node.parentPath && node.variantKey) {
        configStore.openVariant(node.parentPath, node.variantKey);
      }
    } else {
      configStore.openFile(node.path);
    }
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (e.key === "Enter" || e.key === " ") {
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
    if (node.type === "folder") {
      node.loaded = false;
      await configStore.expandFolder(node);
    }
  }

  function toggleVariants(e: MouseEvent) {
    e.stopPropagation();
    showVariants = !showVariants;
  }

  async function handleDeleteFolder(e: MouseEvent) {
    e.stopPropagation();
    if (
      !confirm(
        `Delete folder "${node.name}" and all its contents? This cannot be undone.`,
      )
    )
      return;
    await configStore.deleteFolder(node.path);
  }

  async function handleDeleteFile(e: MouseEvent) {
    e.stopPropagation();
    if (
      !confirm(
        `Delete file "${node.name}" and all its versions? This cannot be undone.`,
      )
    )
      return;
    await configStore.deleteFile(node.path);
  }

  async function handleDeleteVariant(e: MouseEvent) {
    e.stopPropagation();
    if (!node.parentPath || !node.variantKey) return;
    if (
      !confirm(`Delete variant "@${node.variantKey}"? This cannot be undone.`)
    )
      return;
    await configStore.deleteVariant(node.parentPath, node.variantKey);
  }

  // Determine active/open state based on node type
  const tabId = $derived(
    node.type === "variant" && node.parentPath && node.variantKey
      ? `${node.parentPath}@${node.variantKey}`
      : node.path,
  );
  const isActive = $derived(configStore.activeTabId === tabId);
  const isOpen = $derived(configStore.openTabs.some((t) => t.id === tabId));
  const hasVariants = $derived(
    node.type === "file" && node.children && node.children.length > 0,
  );
</script>

<div class="select-none">
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="flex items-center gap-1 py-0.5 pr-2 cursor-pointer text-[13px]
  {isActive
      ? 'bg-accent-600 text-white hover:bg-accent-700'
      : 'text-gray-700 dark:text-warm-100 hover:bg-gray-200 dark:hover:bg-warm-700'}
  {isOpen && !isActive ? 'text-brand-600 dark:text-accent-400' : ''}"
    style="padding-left: {level * 12 + 4}px"
    onclick={handleClick}
    onkeydown={handleKeyDown}
    onmouseenter={() => (isHovered = true)}
    onmouseleave={() => (isHovered = false)}
    role="treeitem"
    tabindex="0"
    aria-expanded={node.type === "folder" ? node.expanded : undefined}
    aria-selected={isActive}
  >
    {#if node.type === "folder"}
      <span class="flex items-center justify-center shrink-0 w-3.5 opacity-60">
        {#if node.expanded}
          <ChevronDown size={14} />
        {:else}
          <ChevronRight size={14} />
        {/if}
      </span>
      <span
        class="flex items-center justify-center shrink-0 {isActive
          ? 'text-white'
          : 'text-amber-500'}"
      >
        {#if node.expanded}
          <FolderOpen size={14} />
        {:else}
          <Folder size={14} />
        {/if}
      </span>
    {:else if node.type === "variant"}
      <span class="w-3.5 shrink-0"></span>
      <span
        class="flex items-center justify-center shrink-0 {isActive
          ? 'text-white'
          : 'text-purple-500'}"
      >
        <Layers size={13} />
      </span>
    {:else}
      {#if hasVariants}
        <button
          class="flex items-center justify-center shrink-0 w-3.5 opacity-60 bg-transparent border-none p-0 cursor-pointer"
          onclick={toggleVariants}
        >
          {#if showVariants}
            <ChevronDown size={14} />
          {:else}
            <ChevronRight size={14} />
          {/if}
        </button>
      {:else}
        <span class="w-3.5 shrink-0"></span>
      {/if}
      <span
        class="flex items-center justify-center shrink-0 {isActive
          ? 'text-white'
          : 'text-gray-500 dark:text-slate-400'}"
      >
        <FileText size={14} />
      </span>
    {/if}
    <span
      class="flex-1 overflow-hidden text-ellipsis whitespace-nowrap {node.type ===
      'variant'
        ? 'text-[12px] font-mono'
        : ''}"
      title={node.type === "variant"
        ? `${node.parentPath}@${node.variantKey}`
        : node.path}>{node.name}</span
    >

    {#if node.type === "folder" && isHovered}
      <span class="flex gap-0.5 shrink-0">
        <button
          class="flex items-center justify-center w-4.5 h-4.5 rounded p-0 border-none cursor-pointer
 {isActive
            ? 'text-white/70 hover:bg-white/20 hover:text-white'
            : 'text-slate-500 dark:text-slate-400 bg-transparent hover:bg-slate-300 dark:hover:bg-warm-600 hover:text-gray-700 dark:hover:text-slate-200'}"
          onclick={handleCreateFile}
          title="New File"
        >
          <FilePlus size={12} />
        </button>
        <button
          class="flex items-center justify-center w-4.5 h-4.5 rounded p-0 border-none cursor-pointer
 {isActive
            ? 'text-white/70 hover:bg-white/20 hover:text-white'
            : 'text-slate-500 dark:text-slate-400 bg-transparent hover:bg-slate-300 dark:hover:bg-warm-600 hover:text-gray-700 dark:hover:text-slate-200'}"
          onclick={handleCreateFolder}
          title="New Folder"
        >
          <FolderPlus size={12} />
        </button>
        <button
          class="flex items-center justify-center w-4.5 h-4.5 rounded p-0 border-none cursor-pointer
 {isActive
            ? 'text-white/70 hover:bg-white/20 hover:text-white'
            : 'text-slate-500 dark:text-slate-400 bg-transparent hover:bg-slate-300 dark:hover:bg-warm-600 hover:text-gray-700 dark:hover:text-slate-200'}"
          onclick={handleRefresh}
          title="Refresh"
        >
          <RefreshCw size={12} />
        </button>
        <button
          class="flex items-center justify-center w-4.5 h-4.5 rounded p-0 border-none cursor-pointer
 {isActive
            ? 'text-white/70 hover:bg-red-500/20 hover:text-red-300'
            : 'text-slate-400 dark:text-slate-500 bg-transparent hover:bg-red-100 hover:text-red-500'}"
          onclick={handleDeleteFolder}
          title="Delete Folder"
        >
          <Trash2 size={12} />
        </button>
      </span>
    {:else if node.type === "file" && isHovered}
      <span class="flex gap-0.5 shrink-0">
        <button
          class="flex items-center justify-center w-4.5 h-4.5 rounded p-0 border-none cursor-pointer
 {isActive
            ? 'text-white/70 hover:bg-red-500/20 hover:text-red-300'
            : 'text-slate-400 dark:text-slate-500 bg-transparent hover:bg-red-100 hover:text-red-500'}"
          onclick={handleDeleteFile}
          title="Delete File"
        >
          <Trash2 size={12} />
        </button>
      </span>
    {:else if node.type === "variant" && isHovered}
      <span class="flex gap-0.5 shrink-0">
        <button
          class="flex items-center justify-center w-4.5 h-4.5 rounded p-0 border-none cursor-pointer
 {isActive
            ? 'text-white/70 hover:bg-red-500/20 hover:text-red-300'
            : 'text-slate-400 dark:text-slate-500 bg-transparent hover:bg-red-100 hover:text-red-500'}"
          onclick={handleDeleteVariant}
          title="Delete Variant"
        >
          <Trash2 size={12} />
        </button>
      </span>
    {/if}

    {#if isOpen && !isActive && !isHovered}
      <span class="w-1.5 h-1.5 rounded-full bg-brand-500 shrink-0"></span>
    {/if}
  </div>

  {#if node.type === "folder" && node.expanded && node.children}
    <div role="group">
      {#each node.children as child (child.type === "variant" ? `variant:${child.path}@${child.variantKey}` : `${child.type}:${child.path}`)}
        <FileTreeNode
          node={child}
          level={level + 1}
          {onCreateFile}
          {onCreateFolder}
        />
      {/each}
    </div>
  {/if}

  <!-- Show variant children under file nodes -->
  {#if node.type === "file" && hasVariants && showVariants}
    <div role="group">
      {#each node.children || [] as child (`${child.path}@${child.variantKey}`)}
        <FileTreeNode
          node={child}
          level={level + 1}
          {onCreateFile}
          {onCreateFolder}
        />
      {/each}
    </div>
  {/if}
</div>
