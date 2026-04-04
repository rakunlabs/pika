import type {
  RawTreeNode, RawTab, RawDirEntry, RawMount, RawViewerMode
} from '@/lib/types/config';
import { addToast } from '@/lib/store/toast.svelte';
import { basePath } from '@/lib/basepath';
import axios from 'axios';

// Well-known text file extensions that should use the text/code viewer
const TEXT_EXTENSIONS = new Set([
  '.json', '.yaml', '.yml', '.toml', '.xml', '.html', '.htm', '.css',
  '.js', '.ts', '.jsx', '.tsx', '.go', '.py', '.rb', '.rs', '.java',
  '.c', '.cpp', '.h', '.hpp', '.cs', '.sh', '.bash', '.zsh', '.fish',
  '.bat', '.cmd', '.ps1', '.sql', '.graphql', '.gql',
  '.md', '.markdown', '.txt', '.text', '.log', '.csv', '.tsv',
  '.env', '.ini', '.cfg', '.conf', '.config', '.properties',
  '.gitignore', '.dockerignore', '.editorconfig',
  '.svelte', '.vue', '.astro', '.scss', '.sass', '.less',
  '.tf', '.hcl', '.nix', '.lua', '.pl', '.pm', '.r',
  '.makefile', '.dockerfile', '.pem', '.crt', '.key', '.pub',
]);

// Image extensions
const IMAGE_EXTENSIONS = new Set([
  '.png', '.jpg', '.jpeg', '.gif', '.svg', '.webp', '.ico', '.bmp', '.avif',
]);

// PDF extension
const PDF_EXTENSIONS = new Set(['.pdf']);

// Video extensions
const VIDEO_EXTENSIONS = new Set([
  '.mp4', '.webm', '.ogv', '.ogg', '.mov', '.m4v',
]);

// Audio extensions
const AUDIO_EXTENSIONS = new Set([
  '.mp3', '.wav', '.flac', '.aac', '.oga', '.m4a', '.weba', '.opus',
]);

// Size limits
const MAX_TEXT_SIZE = 5 * 1024 * 1024;   // 5 MB — truncate text files beyond this
const MAX_HEX_SIZE = 10 * 1024 * 1024;   // 10 MB — refuse hex viewer beyond this

function getExtension(name: string): string {
  const lower = name.toLowerCase();
  // Handle dotfiles like ".gitignore"
  const lastDot = lower.lastIndexOf('.');
  if (lastDot <= 0) {
    // No extension or dotfile — check if the whole name is known
    if (TEXT_EXTENSIONS.has('.' + lower)) return '.' + lower;
    return '';
  }
  return lower.slice(lastDot);
}

function determineViewerMode(name: string, contentType: string): RawViewerMode {
  const ext = getExtension(name);

  if (IMAGE_EXTENSIONS.has(ext)) return 'image';
  if (VIDEO_EXTENSIONS.has(ext)) return 'video';
  if (AUDIO_EXTENSIONS.has(ext)) return 'audio';
  if (PDF_EXTENSIONS.has(ext)) return 'pdf';
  if (TEXT_EXTENSIONS.has(ext)) return 'text';

  // Fallback: check content-type from response
  if (contentType.startsWith('text/')) return 'text';
  if (contentType.startsWith('image/')) return 'image';
  if (contentType.startsWith('video/')) return 'video';
  if (contentType.startsWith('audio/')) return 'audio';
  if (contentType === 'application/json') return 'text';
  if (contentType === 'application/xml') return 'text';
  if (contentType === 'application/javascript') return 'text';
  if (contentType === 'application/pdf') return 'pdf';

  return 'binary-placeholder';
}

function getLanguageFromExt(name: string): string {
  const ext = getExtension(name);
  switch (ext) {
    case '.json': return 'json';
    case '.yaml': case '.yml': return 'yaml';
    case '.toml': return 'toml';
    default: return 'plain';
  }
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function createFilesStore() {
  let tree = $state<RawTreeNode[]>([]);
  let openTabs = $state<RawTab[]>([]);
  let activeTabId = $state<string | null>(null);
  let leftPanelWidth = $state(250);
  let rightPanelWidth = $state(260);
  let clipboard = $state<{ mode: 'copy' | 'cut'; nodes: RawTreeNode[] } | null>(null);
  let renamingNodePath = $state<string | null>(null);

  const activeTab = $derived(openTabs.find(t => t.id === activeTabId) ?? null);

  // Initialize tree from raw mounts info
  function initTree(mounts: RawMount[]): void {
    // Store mounts globally for writable check
    (window as any).__pikaRawMounts = mounts;

    tree = mounts.map(m => ({
      name: m.prefix,
      path: m.prefix,
      mount: m.prefix,
      type: 'mount' as const,
      expanded: false,
      loaded: false,
      children: [],
      writable: m.writable,
      mountType: m.type || 'local',
    }));
  }

  async function fetchDirectory(mountPrefix: string, dirPath: string): Promise<RawDirEntry[]> {
    const fullPath = dirPath ? `${mountPrefix}/${dirPath}` : mountPrefix;
    try {
      const response = await axios.get(`/api/v1/raw/${fullPath}`);
      return response.data || [];
    } catch (error: any) {
      if (error.response?.status === 404) return [];
      throw error;
    }
  }

  async function expandNode(node: RawTreeNode): Promise<void> {
    if (node.type === 'file') return;

    if (node.loaded) {
      node.expanded = !node.expanded;
      return;
    }

    try {
      const relativePath = node.type === 'mount' ? '' : node.path.slice(node.mount.length + 1);
      const entries = await fetchDirectory(node.mount, relativePath);

      node.children = entries
        .sort((a, b) => {
          // Folders first, then files
          if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
          return a.name.localeCompare(b.name);
        })
        .map(entry => ({
          name: entry.name,
          path: `${node.path}/${entry.name}`,
          mount: node.mount,
          type: entry.is_dir ? 'folder' as const : 'file' as const,
          expanded: false,
          loaded: false,
          children: entry.is_dir ? [] : undefined,
          size: entry.size,
          writable: node.writable,
          mountType: node.mountType,
        }));

      node.loaded = true;
      node.expanded = true;
    } catch (error) {
      console.error('Failed to expand:', error);
      addToast(`Failed to expand: ${node.name}`, 'alert');
    }
  }

  function toggleNode(node: RawTreeNode): void {
    if (node.type === 'file') {
      openFile(node);
    } else {
      if (node.expanded) {
        node.expanded = false;
      } else {
        expandNode(node);
      }
    }
  }

  async function openFile(node: RawTreeNode): Promise<void> {
    const tabId = node.path;

    // Check if already open
    const existing = openTabs.find(t => t.id === tabId);
    if (existing) {
      activeTabId = tabId;
      updateURL();
      return;
    }

    const newTab: RawTab = {
      id: tabId,
      mount: node.mount,
      path: node.path.slice(node.mount.length + 1), // relative path within mount
      name: node.name,
      size: node.size || 0,
      contentType: '',
      viewerMode: 'binary-placeholder',
      loaded: false,
    };

    openTabs = [...openTabs, newTab];
    activeTabId = tabId;
    updateURL();

    // Load file content — use tabId to find the reactive proxy in openTabs
    await loadFileContent(tabId);
  }

  async function loadFileContent(tabId: string): Promise<void> {
    const tab = openTabs.find(t => t.id === tabId);
    if (!tab) return;

    const apiPath = `${tab.mount}/${tab.path}`;

    try {
      const viewerGuess = determineViewerMode(tab.name, '');

      if (viewerGuess === 'image' || viewerGuess === 'pdf' || viewerGuess === 'video' || viewerGuess === 'audio') {
        // For media files, just set the URL — the browser handles rendering
        tab.rawUrl = `${basePath}/api/v1/raw/${apiPath}`;
        tab.viewerMode = viewerGuess;
        tab.loaded = true;
        return;
      }

      if (viewerGuess === 'text') {
        // Check file size first via HEAD to avoid downloading huge files
        try {
          const headResp = await axios.head(`/api/v1/raw/${apiPath}`);
          const contentLength = parseInt(headResp.headers['content-length'] || '0', 10);
          tab.contentType = headResp.headers['content-type'] || '';
          if (contentLength > 0) tab.size = contentLength;

          if (contentLength > MAX_TEXT_SIZE) {
            // Fetch only the first MAX_TEXT_SIZE bytes using Range header
            const response = await axios.get(`/api/v1/raw/${apiPath}`, {
              responseType: 'text',
              transformResponse: [(data) => data],
              headers: { Range: `bytes=0-${MAX_TEXT_SIZE - 1}` },
            });
            tab.textContent = response.data;
            tab.truncated = true;
            tab.viewerMode = 'text';
            tab.loaded = true;
            return;
          }
        } catch {
          // HEAD failed or Range not supported — fall through to full fetch
        }

        // Fetch full text (file is within size limit or HEAD failed)
        const response = await axios.get(`/api/v1/raw/${apiPath}`, {
          responseType: 'text',
          transformResponse: [(data) => data],
        });
        tab.contentType = response.headers['content-type'] || '';
        tab.textContent = response.data;
        tab.size = new Blob([response.data]).size;
        tab.truncated = false;
        tab.viewerMode = 'text';
        tab.loaded = true;
        return;
      }

      // Unknown extension — show binary placeholder immediately without downloading.
      // The hex data is only fetched when the user clicks "Open Anyway".
      tab.viewerMode = 'binary-placeholder';
      tab.loaded = true;
    } catch (error) {
      console.error('Failed to load file:', error);
      addToast(`Failed to load file: ${tab.name}`, 'alert');
    }
  }

  async function forceOpenHex(tabId: string): Promise<void> {
    const tab = openTabs.find(t => t.id === tabId);
    if (!tab) return;

    // If hex data already loaded, just switch the view
    if (tab.hexData) {
      tab.forceHex = true;
      tab.viewerMode = 'hex';
      return;
    }

    // Check size first via HEAD
    const apiPath = `${tab.mount}/${tab.path}`;
    try {
      const headResp = await axios.head(`/api/v1/raw/${apiPath}`);
      const contentLength = parseInt(headResp.headers['content-length'] || '0', 10);
      tab.contentType = headResp.headers['content-type'] || 'application/octet-stream';
      if (contentLength > 0) tab.size = contentLength;

      if (contentLength > MAX_HEX_SIZE) {
        tab.tooLargeForHex = true;
        // Stay on placeholder — the viewer will show the "too large" message
        return;
      }
    } catch {
      // HEAD failed — try fetching anyway
    }

    // Fetch binary data
    tab.viewerMode = 'binary-loading';

    try {
      const response = await axios.get(`/api/v1/raw/${apiPath}`, {
        responseType: 'arraybuffer',
      });
      tab.contentType = response.headers['content-type'] || 'application/octet-stream';
      tab.size = response.data.byteLength;

      const bytes = new Uint8Array(response.data);
      const chunkSize = 8192;
      let binaryStr = '';
      for (let i = 0; i < bytes.length; i += chunkSize) {
        const chunk = bytes.subarray(i, i + chunkSize);
        binaryStr += String.fromCharCode(...chunk);
      }
      tab.hexData = btoa(binaryStr);
      tab.forceHex = true;
      tab.viewerMode = 'hex';
    } catch (error) {
      console.error('Failed to load binary data:', error);
      addToast(`Failed to load file: ${tab.name}`, 'alert');
      tab.viewerMode = 'binary-placeholder';
    }
  }

  function closeTab(tabId: string): void {
    const tabIndex = openTabs.findIndex(t => t.id === tabId);
    if (tabIndex === -1) return;

    openTabs = openTabs.filter(t => t.id !== tabId);

    if (activeTabId === tabId) {
      if (openTabs.length === 0) {
        activeTabId = null;
      } else if (tabIndex >= openTabs.length) {
        activeTabId = openTabs[openTabs.length - 1].id;
      } else {
        activeTabId = openTabs[tabIndex].id;
      }
    }
    updateURL();
  }

  function selectTab(tabId: string): void {
    activeTabId = tabId;
    updateURL();
  }

  function closeAllTabs(): void {
    openTabs = [];
    activeTabId = null;
    updateURL();
  }

  function closeOtherTabs(tabId: string): void {
    openTabs = openTabs.filter(t => t.id === tabId);
    activeTabId = tabId;
    updateURL();
  }

  function closeTabsToRight(tabId: string): void {
    const idx = openTabs.findIndex(t => t.id === tabId);
    if (idx === -1) return;
    openTabs = openTabs.slice(0, idx + 1);
    if (activeTabId && !openTabs.find(t => t.id === activeTabId)) {
      activeTabId = openTabs[openTabs.length - 1]?.id ?? null;
    }
    updateURL();
  }

  function closeTabsToLeft(tabId: string): void {
    const idx = openTabs.findIndex(t => t.id === tabId);
    if (idx === -1) return;
    openTabs = openTabs.slice(idx);
    if (activeTabId && !openTabs.find(t => t.id === activeTabId)) {
      activeTabId = openTabs[0]?.id ?? null;
    }
    updateURL();
  }

  // Refresh a node (re-fetch directory contents)
  async function refreshNode(node: RawTreeNode): Promise<void> {
    if (node.type === 'file') return;
    node.loaded = false;
    await expandNode(node);
  }

  // URL deep linking
  function updateURL(): void {
    const tab = openTabs.find(t => t.id === activeTabId);
    const base = '#/files';
    if (tab) {
      const url = `${base}?mount=${encodeURIComponent(tab.mount)}&path=${encodeURIComponent(tab.path)}`;
      history.replaceState(null, '', url);
    } else {
      history.replaceState(null, '', base);
    }
  }

  function openFromURL(mounts: RawMount[]): void {
    initTree(mounts);

    const hash = window.location.hash;
    const qsIndex = hash.indexOf('?');
    if (qsIndex === -1) return;

    const params = new URLSearchParams(hash.slice(qsIndex));
    const mount = params.get('mount');
    const path = params.get('path');

    if (mount && path) {
      const tabId = `${mount}/${path}`;
      const node: RawTreeNode = {
        name: path.split('/').pop() || path,
        path: tabId,
        mount,
        type: 'file',
      };
      openFile(node);
    }
  }

  function getDownloadUrl(tab: RawTab): string {
    return `${basePath}/api/v1/raw/${tab.mount}/${tab.path}`;
  }

  // ── Write operations (for writable mounts like S3) ──

  async function uploadFile(mount: string, dirPath: string, file: File): Promise<void> {
    const fullPath = dirPath ? `${mount}/${dirPath}/${file.name}` : `${mount}/${file.name}`;
    try {
      await axios.put(`/api/v1/raw/${fullPath}`, file, {
        headers: { 'Content-Type': file.type || 'application/octet-stream' },
      });
      addToast(`Uploaded ${file.name}`, 'success');
    } catch (error: any) {
      const msg = error.response?.data?.message || `Failed to upload ${file.name}`;
      addToast(msg, 'alert');
      throw error;
    }
  }

  async function deleteItem(mount: string, itemPath: string): Promise<void> {
    const fullPath = `${mount}/${itemPath}`;
    try {
      await axios.delete(`/api/v1/raw/${fullPath}`);
      // Close tab if open
      const tabId = `${mount}/${itemPath}`;
      const existing = openTabs.find(t => t.id === tabId);
      if (existing) {
        closeTab(tabId);
      }
      addToast(`Deleted ${itemPath.split('/').pop()}`, 'success');
    } catch (error: any) {
      const msg = error.response?.data?.message || `Failed to delete`;
      addToast(msg, 'alert');
      throw error;
    }
  }

  async function createFolder(mount: string, dirPath: string, folderName: string): Promise<void> {
    const fullPath = dirPath ? `${mount}/${dirPath}/${folderName}` : `${mount}/${folderName}`;
    try {
      await axios.post(`/api/v1/raw-mkdir/${fullPath}`);
      addToast(`Created folder ${folderName}`, 'success');
    } catch (error: any) {
      const msg = error.response?.data?.message || `Failed to create folder`;
      addToast(msg, 'alert');
      throw error;
    }
  }

  // ── Clipboard & File Operations ──

  function copyNodes(nodes: RawTreeNode[]): void {
    clipboard = { mode: 'copy', nodes: [...nodes] };
    addToast(`Copied ${nodes.length} item${nodes.length > 1 ? 's' : ''}`, 'success');
  }

  function cutNodes(nodes: RawTreeNode[]): void {
    clipboard = { mode: 'cut', nodes: [...nodes] };
    addToast(`Cut ${nodes.length} item${nodes.length > 1 ? 's' : ''}`, 'success');
  }

  function clearClipboard(): void {
    clipboard = null;
  }

  async function pasteItems(targetNode: RawTreeNode): Promise<void> {
    if (!clipboard || clipboard.nodes.length === 0) return;

    const isCut = clipboard.mode === 'cut';
    const endpoint = isCut ? '/api/v1/raw-move' : '/api/v1/raw-copy';
    const targetDir = targetNode.type === 'file'
      ? targetNode.path.substring(0, targetNode.path.lastIndexOf('/'))
      : targetNode.path;

    let successCount = 0;
    for (const node of clipboard.nodes) {
      const srcPath = node.path;
      const fileName = node.name;
      const dstPath = `${targetDir}/${fileName}`;

      try {
        await axios.post(endpoint, { src: srcPath, dst: dstPath });
        successCount++;
      } catch (error: any) {
        const msg = error.response?.data?.message || `Failed to ${isCut ? 'move' : 'copy'} ${fileName}`;
        addToast(msg, 'alert');
      }
    }

    if (successCount > 0) {
      addToast(`${isCut ? 'Moved' : 'Copied'} ${successCount} item${successCount > 1 ? 's' : ''}`, 'success');
    }

    if (isCut) {
      clipboard = null;
    }
  }

  async function renameItem(node: RawTreeNode, newName: string): Promise<void> {
    if (!newName.trim() || newName === node.name) {
      renamingNodePath = null;
      return;
    }

    const parentPath = node.path.substring(0, node.path.lastIndexOf('/'));
    const newPath = `${parentPath}/${newName.trim()}`;

    try {
      await axios.post('/api/v1/raw-rename', { src: node.path, dst: newPath });
      // Update tab if open
      const oldTabId = node.path;
      const tab = openTabs.find(t => t.id === oldTabId);
      if (tab) {
        closeTab(oldTabId);
      }
      addToast(`Renamed to ${newName.trim()}`, 'success');
    } catch (error: any) {
      const msg = error.response?.data?.message || 'Failed to rename';
      addToast(msg, 'alert');
    } finally {
      renamingNodePath = null;
    }
  }

  async function duplicateItem(node: RawTreeNode): Promise<void> {
    const ext = node.name.lastIndexOf('.');
    const baseName = ext > 0 ? node.name.substring(0, ext) : node.name;
    const extension = ext > 0 ? node.name.substring(ext) : '';
    const newName = `${baseName} (copy)${extension}`;

    const parentPath = node.path.substring(0, node.path.lastIndexOf('/'));
    const dstPath = `${parentPath}/${newName}`;

    try {
      await axios.post('/api/v1/raw-copy', { src: node.path, dst: dstPath });
      addToast(`Duplicated as ${newName}`, 'success');
    } catch (error: any) {
      const msg = error.response?.data?.message || 'Failed to duplicate';
      addToast(msg, 'alert');
    }
  }

  function startRename(node: RawTreeNode): void {
    renamingNodePath = node.path;
  }

  function cancelRename(): void {
    renamingNodePath = null;
  }

  // Check if a mount is writable from the app info
  function isMountWritable(mountPrefix: string): boolean {
    const mounts = (window as any).__pikaRawMounts as RawMount[] | undefined;
    if (!mounts) return false;
    const m = mounts.find(m => m.prefix === mountPrefix);
    return m?.writable ?? false;
  }

  function setLeftPanelWidth(width: number): void {
    leftPanelWidth = Math.max(150, Math.min(500, width));
  }

  function setRightPanelWidth(width: number): void {
    rightPanelWidth = Math.max(180, Math.min(400, width));
  }

  return {
    get tree() { return tree; },
    get openTabs() { return openTabs; },
    get activeTabId() { return activeTabId; },
    get activeTab() { return activeTab; },
    get leftPanelWidth() { return leftPanelWidth; },
    get rightPanelWidth() { return rightPanelWidth; },

    initTree,
    expandNode,
    toggleNode,
    openFile,
    forceOpenHex,
    closeTab,
    closeAllTabs,
    closeOtherTabs,
    closeTabsToRight,
    closeTabsToLeft,
    selectTab,
    refreshNode,
    openFromURL,
    setLeftPanelWidth,
    setRightPanelWidth,
    getLanguageFromExt,
    getDownloadUrl,
    formatSize,
    MAX_TEXT_SIZE,
    MAX_HEX_SIZE,
    uploadFile,
    deleteItem,
    createFolder,
    isMountWritable,
    // Clipboard & file operations
    get clipboard() { return clipboard; },
    get renamingNodePath() { return renamingNodePath; },
    copyNodes,
    cutNodes,
    clearClipboard,
    pasteItems,
    renameItem,
    duplicateItem,
    startRename,
    cancelRename,
  };
}

export const filesStore = createFilesStore();
