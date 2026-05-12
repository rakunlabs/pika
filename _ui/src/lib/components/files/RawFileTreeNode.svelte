<script lang="ts">
 import { ChevronRight, ChevronDown, Folder, FolderOpen, FileText, HardDrive, RefreshCw, Upload, FolderPlus, Trash2, Copy, Scissors, ClipboardPaste, Pencil, Files, Download } from 'lucide-svelte';
 import type { RawTreeNode } from '@/lib/types/config';
 import { filesStore } from '@/lib/store/files.svelte';
 import { basePath } from '@/lib/basepath';
 import RawFileTreeNode from './RawFileTreeNode.svelte';

 interface Props {
 node: RawTreeNode;
 level?: number;
 }

 let { node, level = 0 }: Props = $props();

 let isHovered = $state(false);
 let fileInput: HTMLInputElement | undefined = $state();
 let renameInput: HTMLInputElement | undefined = $state();
 let renameValue = $state('');

 // Context menu
 let contextMenu = $state<{ x: number; y: number } | null>(null);

 function handleClick() {
 filesStore.toggleNode(node);
 }

 function handleKeyDown(e: KeyboardEvent) {
 if (e.key === 'Enter' || e.key === ' ') {
 e.preventDefault();
 handleClick();
 }
 if (e.key === 'F2' && isWritable) {
 e.preventDefault();
 startRename();
 }
 if (e.key === 'Delete' && isWritable && node.type !== 'mount') {
 e.preventDefault();
 handleDelete();
 }
 }

 function handleContextMenu(e: MouseEvent) {
 e.preventDefault();
 e.stopPropagation();
 contextMenu = { x: e.clientX, y: e.clientY };
 }

 function closeContextMenu() {
 contextMenu = null;
 }

 // ── Context menu actions ──
 function handleCopy() {
 filesStore.copyNodes([node]);
 closeContextMenu();
 }

 function handleCut() {
 filesStore.cutNodes([node]);
 closeContextMenu();
 }

 async function handlePaste() {
 await filesStore.pasteItems(node);
 await filesStore.refreshNode(node);
 closeContextMenu();
 }

 function startRename() {
 closeContextMenu();
 if (node.type === 'mount') return;
 renameValue = node.name;
 filesStore.startRename(node);
 // Focus the input in next tick
 requestAnimationFrame(() => {
 renameInput?.focus();
 renameInput?.select();
 });
 }

 async function confirmRename() {
 await filesStore.renameItem(node, renameValue);
 // Find and refresh parent
 findAndRefreshParent(node.path);
 }

 function cancelRename() {
 filesStore.cancelRename();
 }

 async function handleDuplicate() {
 await filesStore.duplicateItem(node);
 findAndRefreshParent(node.path);
 closeContextMenu();
 }

 async function handleDelete() {
 closeContextMenu();
 if (!confirm(`Delete "${node.name}"? This cannot be undone.`)) return;
 const itemPath = node.path.slice(node.mount.length + 1);
 try {
 await filesStore.deleteItem(node.mount, itemPath);
 findAndRefreshParent(node.path);
 } catch {
 // toast already shown
 }
 }

 function handleDownload() {
 closeContextMenu();
 if (node.type === 'file') {
 const url = `${basePath}/api/v1/raw/${node.path}`;
 const a = document.createElement('a');
 a.href = url;
 a.download = node.name;
 document.body.appendChild(a);
 a.click();
 document.body.removeChild(a);
 }
 }

 async function handleRefresh(e?: MouseEvent) {
 e?.stopPropagation();
 closeContextMenu();
 await filesStore.refreshNode(node);
 }

 function handleUploadClick(e?: MouseEvent) {
 e?.stopPropagation();
 closeContextMenu();
 fileInput?.click();
 }

 async function handleFileSelected(e: Event) {
 const input = e.target as HTMLInputElement;
 if (!input.files?.length) return;
 const dirPath = node.type === 'mount' ? '' : node.path.slice(node.mount.length + 1);
 for (const file of input.files) {
 try {
 await filesStore.uploadFile(node.mount, dirPath, file);
 } catch { /* toast */ }
 }
 await filesStore.refreshNode(node);
 input.value = '';
 }

 async function handleCreateFolder() {
 closeContextMenu();
 const name = prompt('Folder name:');
 if (!name?.trim()) return;
 const dirPath = node.type === 'mount' ? '' : node.path.slice(node.mount.length + 1);
 try {
 await filesStore.createFolder(node.mount, dirPath, name.trim());
 await filesStore.refreshNode(node);
 } catch { /* toast */ }
 }

 function findAndRefreshParent(childPath: string) {
 // Walk the tree to find the parent and refresh it
 const parentPath = childPath.substring(0, childPath.lastIndexOf('/'));
 function findNode(nodes: RawTreeNode[], targetPath: string): RawTreeNode | null {
 for (const n of nodes) {
 if (n.path === targetPath) return n;
 if (n.children) {
 const found = findNode(n.children, targetPath);
 if (found) return found;
 }
 }
 return null;
 }
 const parent = findNode(filesStore.tree, parentPath);
 if (parent) {
 filesStore.refreshNode(parent);
 }
 }

 // Close context menu on click elsewhere
 function handleWindowClick() {
 if (contextMenu) closeContextMenu();
 }

 const isFolder = $derived(node.type === 'folder' || node.type === 'mount');
 const isWritable = $derived(node.writable === true);
 const isRenaming = $derived(filesStore.renamingNodePath === node.path);
 const isCut = $derived(filesStore.clipboard?.mode === 'cut' && filesStore.clipboard.nodes.some(n => n.path === node.path));
 const hasClipboard = $derived(filesStore.clipboard !== null && filesStore.clipboard.nodes.length > 0);
 const tabId = $derived(node.path);
 const isActive = $derived(filesStore.activeTabId === tabId);
 const isOpen = $derived(filesStore.openTabs.some(t => t.id === tabId));

 const mountTypeLabel = $derived(
 node.type === 'mount' && node.mountType
 ? node.mountType === 's3' ? 'S3' : node.mountType === 'ftp' ? 'FTP' : node.mountType === 'sftp' ? 'SFTP' : ''
 : ''
 );
</script>

<svelte:window onclick={handleWindowClick} />

<div class="select-none">
 <!-- svelte-ignore a11y_no_static_element_interactions -->
 <div
 class="flex items-center gap-1 py-0.5 pr-2 cursor-pointer text-[13px]
  {isActive
    ? 'bg-accent-600 text-white hover:bg-accent-700'
    : 'text-gray-700 dark:text-warm-100 hover:bg-gray-200 dark:hover:bg-warm-700'}
  {isOpen && !isActive ? 'text-brand-600 dark:text-accent-400' : ''}
  {isCut ? 'opacity-50' : ''}"
 style="padding-left: {level * 12 + 4}px"
 onclick={handleClick}
 onkeydown={handleKeyDown}
 oncontextmenu={handleContextMenu}
 onmouseenter={() => isHovered = true}
 onmouseleave={() => isHovered = false}
 role="treeitem"
 tabindex="0"
 aria-expanded={isFolder ? node.expanded : undefined}
 aria-selected={isActive}
 >
 {#if isFolder}
 <span class="flex items-center justify-center shrink-0 w-3.5 opacity-60">
 {#if node.expanded}
 <ChevronDown size={14} />
 {:else}
 <ChevronRight size={14} />
 {/if}
 </span>
 <span class="flex items-center justify-center shrink-0 {isActive ? 'text-white' : node.type === 'mount' ? 'text-brand-500' : 'text-amber-500'}">
 {#if node.type === 'mount'}
 <HardDrive size={14} />
 {:else if node.expanded}
 <FolderOpen size={14} />
 {:else}
 <Folder size={14} />
 {/if}
 </span>
 {:else}
 <span class="w-3.5 shrink-0"></span>
 <span class="flex items-center justify-center shrink-0 {isActive ? 'text-white' : 'text-gray-500 dark:text-slate-400'}">
 <FileText size={14} />
 </span>
 {/if}

 {#if isRenaming}
 <!-- Inline rename input -->
 <!-- svelte-ignore a11y_no_static_element_interactions -->
 <input
 bind:this={renameInput}
 bind:value={renameValue}
 class="flex-1 px-1 py-0 text-[13px] bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 border border-brand-400 rounded outline-none min-w-0"
 onclick={(e) => e.stopPropagation()}
 onkeydown={(e) => {
 e.stopPropagation();
 if (e.key === 'Enter') { confirmRename(); }
 if (e.key === 'Escape') { cancelRename(); }
 }}
 onblur={confirmRename}
 />
 {:else}
 <span class="flex-1 overflow-hidden text-ellipsis whitespace-nowrap {node.type === 'mount' ? 'font-semibold' : ''}" title={node.path}>
 {node.name}
 </span>
 {/if}

 {#if mountTypeLabel && !isRenaming}
 <span class="px-1 py-0 text-[9px] font-medium rounded shrink-0
 {node.mountType === 's3' ? 'bg-orange-100 text-orange-600' : 'bg-purple-100 text-purple-600'}">
 {mountTypeLabel}
 </span>
 {/if}

 {#if isFolder && isHovered && !isRenaming}
 <span class="flex gap-0.5 shrink-0">
 {#if isWritable}
 <button
 class="flex items-center justify-center w-4.5 h-4.5 rounded p-0 border-none cursor-pointer
 {isActive ? 'text-white/70 hover:bg-white/20 hover:text-white' : 'text-slate-500 dark:text-slate-400 bg-transparent hover:bg-slate-300 dark:hover:bg-warm-600 hover:text-gray-700 dark:hover:text-slate-200'}"
 onclick={(e) => handleUploadClick(e)}
 title="Upload file"
 >
 <Upload size={12} />
 </button>
 <button
 class="flex items-center justify-center w-4.5 h-4.5 rounded p-0 border-none cursor-pointer
 {isActive ? 'text-white/70 hover:bg-white/20 hover:text-white' : 'text-slate-500 dark:text-slate-400 bg-transparent hover:bg-slate-300 dark:hover:bg-warm-600 hover:text-gray-700 dark:hover:text-slate-200'}"
 onclick={(e) => { e.stopPropagation(); handleCreateFolder(); }}
 title="New folder"
 >
 <FolderPlus size={12} />
 </button>
 {/if}
 <button
 class="flex items-center justify-center w-4.5 h-4.5 rounded p-0 border-none cursor-pointer
 {isActive ? 'text-white/70 hover:bg-white/20 hover:text-white' : 'text-slate-500 dark:text-slate-400 bg-transparent hover:bg-slate-300 dark:hover:bg-warm-600 hover:text-gray-700 dark:hover:text-slate-200'}"
 onclick={(e) => handleRefresh(e)}
 title="Refresh"
 >
 <RefreshCw size={12} />
 </button>
 </span>
 {:else if !isFolder && isHovered && isWritable && !isRenaming}
 <span class="flex gap-0.5 shrink-0">
 <button
 class="flex items-center justify-center w-4.5 h-4.5 rounded p-0 border-none cursor-pointer
 {isActive ? 'text-white/70 hover:bg-red-500/20 hover:text-red-300' : 'text-slate-400 dark:text-slate-500 bg-transparent hover:bg-red-100 hover:text-red-500'}"
 onclick={(e) => { e.stopPropagation(); handleDelete(); }}
 title="Delete file"
 >
 <Trash2 size={12} />
 </button>
 </span>
 {/if}

 {#if isOpen && !isActive && !isHovered && !isRenaming}
 <span class="w-1.5 h-1.5 rounded-full bg-brand-500 shrink-0"></span>
 {/if}
 </div>

 <!-- Hidden file input for uploads -->
 {#if isWritable && isFolder}
 <input
 bind:this={fileInput}
 type="file"
 multiple
 class="hidden"
 onchange={handleFileSelected}
 />
 {/if}

 {#if isFolder && node.expanded && node.children}
 <div role="group">
 {#each node.children as child (`${child.type}:${child.path}`)}
 <RawFileTreeNode node={child} level={level + 1} />
 {/each}
 </div>
 {/if}
</div>

<!-- Context Menu -->
{#if contextMenu}
 <!-- svelte-ignore a11y_no_static_element_interactions -->
 <div
 role="menu"
 tabindex="-1"
 class="fixed z-50 min-w-48 py-1 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-xl text-[13px]"
 style="left: {contextMenu.x}px; top: {contextMenu.y}px;"
 onclick={(e) => e.stopPropagation()}
 onkeydown={(e) => e.stopPropagation()}
 >
 {#if isFolder && isWritable}
 <button class="flex items-center gap-2.5 w-full px-3 py-1.5 text-left text-slate-700 dark:text-slate-200 bg-transparent border-none cursor-pointer hover:bg-slate-100 dark:hover:bg-warm-700"
 onclick={(e) => { e.stopPropagation(); handleUploadClick(); }}>
 <Upload size={14} class="text-slate-400 dark:text-slate-500" /> Upload Files...
 </button>
 <button class="flex items-center gap-2.5 w-full px-3 py-1.5 text-left text-slate-700 dark:text-slate-200 bg-transparent border-none cursor-pointer hover:bg-slate-100 dark:hover:bg-warm-700"
 onclick={(e) => { e.stopPropagation(); handleCreateFolder(); }}>
 <FolderPlus size={14} class="text-slate-400 dark:text-slate-500" /> New Folder...
 </button>
 <div class="my-1 border-t border-slate-150 dark:border-warm-700"></div>
 {/if}

 <button class="flex items-center gap-2.5 w-full px-3 py-1.5 text-left text-slate-700 dark:text-slate-200 bg-transparent border-none cursor-pointer hover:bg-slate-100 dark:hover:bg-warm-700"
 onclick={handleCopy}>
 <Copy size={14} class="text-slate-400 dark:text-slate-500" />
 Copy
 <span class="ml-auto text-[11px] text-slate-400 dark:text-slate-500">Ctrl+C</span>
 </button>

 {#if isWritable && node.type !== 'mount'}
 <button class="flex items-center gap-2.5 w-full px-3 py-1.5 text-left text-slate-700 dark:text-slate-200 bg-transparent border-none cursor-pointer hover:bg-slate-100 dark:hover:bg-warm-700"
 onclick={handleCut}>
 <Scissors size={14} class="text-slate-400 dark:text-slate-500" />
 Cut
 <span class="ml-auto text-[11px] text-slate-400 dark:text-slate-500">Ctrl+X</span>
 </button>
 {/if}

 {#if isFolder && isWritable && hasClipboard}
 <button class="flex items-center gap-2.5 w-full px-3 py-1.5 text-left text-slate-700 dark:text-slate-200 bg-transparent border-none cursor-pointer hover:bg-slate-100 dark:hover:bg-warm-700"
 onclick={handlePaste}>
 <ClipboardPaste size={14} class="text-slate-400 dark:text-slate-500" />
 Paste {filesStore.clipboard ? `(${filesStore.clipboard.nodes.length})` : ''}
 <span class="ml-auto text-[11px] text-slate-400 dark:text-slate-500">Ctrl+V</span>
 </button>
 {/if}

 <div class="my-1 border-t border-slate-150 dark:border-warm-700"></div>

 {#if isWritable && node.type !== 'mount'}
 <button class="flex items-center gap-2.5 w-full px-3 py-1.5 text-left text-slate-700 dark:text-slate-200 bg-transparent border-none cursor-pointer hover:bg-slate-100 dark:hover:bg-warm-700"
 onclick={startRename}>
 <Pencil size={14} class="text-slate-400 dark:text-slate-500" />
 Rename
 <span class="ml-auto text-[11px] text-slate-400 dark:text-slate-500">F2</span>
 </button>
 {/if}

 {#if node.type === 'file'}
 <button class="flex items-center gap-2.5 w-full px-3 py-1.5 text-left text-slate-700 dark:text-slate-200 bg-transparent border-none cursor-pointer hover:bg-slate-100 dark:hover:bg-warm-700"
 onclick={handleDuplicate}>
 <Files size={14} class="text-slate-400 dark:text-slate-500" /> Duplicate
 </button>
 <button class="flex items-center gap-2.5 w-full px-3 py-1.5 text-left text-slate-700 dark:text-slate-200 bg-transparent border-none cursor-pointer hover:bg-slate-100 dark:hover:bg-warm-700"
 onclick={handleDownload}>
 <Download size={14} class="text-slate-400 dark:text-slate-500" /> Download
 </button>
 {/if}

 {#if isFolder}
 <button class="flex items-center gap-2.5 w-full px-3 py-1.5 text-left text-slate-700 dark:text-slate-200 bg-transparent border-none cursor-pointer hover:bg-slate-100 dark:hover:bg-warm-700"
 onclick={() => handleRefresh()}>
 <RefreshCw size={14} class="text-slate-400 dark:text-slate-500" /> Refresh
 </button>
 {/if}

 {#if isWritable && node.type !== 'mount'}
 <div class="my-1 border-t border-slate-150 dark:border-warm-700"></div>
 <button class="flex items-center gap-2.5 w-full px-3 py-1.5 text-left text-red-600 bg-transparent border-none cursor-pointer hover:bg-red-50"
 onclick={handleDelete}>
 <Trash2 size={14} />
 Delete
 <span class="ml-auto text-[11px] text-red-400">Del</span>
 </button>
 {/if}
 </div>
{/if}
