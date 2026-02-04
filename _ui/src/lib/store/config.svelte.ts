import type { Tab, TreeNode, SearchResult, FileFormat, FileVersion, FileMeta, Settings } from '@/lib/types/config';
import { addToast } from '@/lib/store/toast.svelte';
import axios from 'axios';

// Helper to detect format from file extension
function detectFormat(filename: string): FileFormat {
  const ext = filename.split('.').pop()?.toLowerCase();
  switch (ext) {
    case 'json':
      return 'json';
    case 'yaml':
    case 'yml':
      return 'yaml';
    case 'toml':
      return 'toml';
    default:
      return 'raw';
  }
}

// Helper to decode base64 data
function decodeContent(data: string): string {
  try {
    return atob(data);
  } catch {
    return data;
  }
}

// Helper to encode content to base64
function encodeContent(content: string): string {
  try {
    return btoa(content);
  } catch {
    return content;
  }
}

// Create the config store
function createConfigStore() {
  // State using Svelte 5 runes
  let tree = $state<TreeNode | null>(null);
  let openTabs = $state<Tab[]>([]);
  let activeTabId = $state<string | null>(null);
  let searchQuery = $state('');
  let searchResults = $state<SearchResult[]>([]);
  let isSearching = $state(false);
  let settings = $state<Settings | null>(null);
  let isLoading = $state(false);
  let leftPanelWidth = $state(250);
  let rightPanelWidth = $state(280);

  // Computed values
  const activeTab = $derived(openTabs.find(t => t.id === activeTabId) ?? null);
  const hasUnsavedChanges = $derived(openTabs.some(t => t.isDirty));

  // API functions
  async function fetchFolder(path: string): Promise<{ folders: string[]; files: string[] }> {
    try {
      const response = await axios.get(`/api/v1/folder/${path}`);
      return response.data;
    } catch (error: any) {
      if (error.response?.status === 404) {
        return { folders: [], files: [] };
      }
      throw error;
    }
  }

  async function fetchFile(path: string, version: number = 0): Promise<{ meta: FileMeta; data: string; versions: FileVersion[] }> {
    const response = await axios.get(`/api/v1/file/${path}`, {
      params: { version }
    });
    
    // Also fetch version info
    let versions: FileVersion[] = [];
    try {
      // For now, we'll extract version from the response if available
      // The backend might need an endpoint for this
      versions = [];
    } catch {
      // Ignore version fetch errors
    }

    return {
      meta: response.data.meta || {},
      data: response.data.data || '',
      versions
    };
  }

  async function saveFile(path: string, content: string, meta: FileMeta): Promise<void> {
    await axios.post(`/api/v1/file/${path}`, {
      meta,
      data: encodeContent(content)
    });
  }

  async function createFolder(path: string): Promise<void> {
    await axios.post(`/api/v1/folder/${path}`);
  }

  async function createFile(path: string, content: string = '', meta: FileMeta = {}): Promise<void> {
    await axios.post(`/api/v1/file/${path}`, {
      meta,
      data: encodeContent(content)
    });
  }

  async function fetchSettings(): Promise<Settings> {
    try {
      const response = await axios.get('/api/v1/settings');
      return response.data;
    } catch {
      return { external: {} };
    }
  }

  // Tree operations
  async function loadTree(): Promise<void> {
    isLoading = true;
    try {
      const rootData = await fetchFolder('');
      const folders = rootData.folders || [];
      const files = rootData.files || [];
      tree = {
        name: 'root',
        path: '',
        type: 'folder',
        expanded: true,
        loaded: true,
        children: [
          ...folders.map(name => ({
            name,
            path: name,
            type: 'folder' as const,
            expanded: false,
            loaded: false,
            children: []
          })),
          ...files.map(name => ({
            name,
            path: name,
            type: 'file' as const
          }))
        ]
      };
    } catch (error) {
      console.error('Failed to load tree:', error);
      addToast('Failed to load file tree', 'alert');
      tree = {
        name: 'root',
        path: '',
        type: 'folder',
        expanded: true,
        loaded: true,
        children: []
      };
    } finally {
      isLoading = false;
    }
  }

  async function expandFolder(node: TreeNode): Promise<void> {
    if (node.type !== 'folder' || node.loaded) {
      node.expanded = !node.expanded;
      return;
    }

    try {
      const data = await fetchFolder(node.path);
      const folders = data.folders || [];
      const files = data.files || [];
      node.children = [
        ...folders.map(name => ({
          name,
          path: `${node.path}/${name}`,
          type: 'folder' as const,
          expanded: false,
          loaded: false,
          children: []
        })),
        ...files.map(name => ({
          name,
          path: `${node.path}/${name}`,
          type: 'file' as const
        }))
      ];
      node.loaded = true;
      node.expanded = true;
    } catch (error) {
      console.error('Failed to expand folder:', error);
      addToast(`Failed to expand folder: ${node.name}`, 'alert');
    }
  }

  function collapseFolder(node: TreeNode): void {
    if (node.type === 'folder') {
      node.expanded = false;
    }
  }

  function toggleFolder(node: TreeNode): void {
    if (node.type === 'folder') {
      if (node.expanded) {
        collapseFolder(node);
      } else {
        expandFolder(node);
      }
    }
  }

  // Tab operations
  async function openFile(path: string): Promise<void> {
    // Check if already open
    const existingTab = openTabs.find(t => t.path === path);
    if (existingTab) {
      activeTabId = existingTab.id;
      return;
    }

    isLoading = true;
    try {
      const fileData = await fetchFile(path);
      const content = decodeContent(fileData.data);
      const name = path.split('/').pop() || path;
      const format = fileData.meta.format || detectFormat(name);

      const newTab: Tab = {
        id: path,
        path,
        name,
        content,
        originalContent: content,
        format,
        version: 0, // Latest
        versions: fileData.versions,
        meta: fileData.meta,
        isDirty: false,
        size: new Blob([content]).size,
        modifiedAt: Date.now()
      };

      openTabs = [...openTabs, newTab];
      activeTabId = newTab.id;
    } catch (error) {
      console.error('Failed to open file:', error);
      addToast(`Failed to open file: ${path}`, 'alert');
      throw error;
    } finally {
      isLoading = false;
    }
  }

  function closeTab(tabId: string): void {
    const tabIndex = openTabs.findIndex(t => t.id === tabId);
    if (tabIndex === -1) return;

    openTabs = openTabs.filter(t => t.id !== tabId);

    // Update active tab if we closed the active one
    if (activeTabId === tabId) {
      if (openTabs.length === 0) {
        activeTabId = null;
      } else if (tabIndex >= openTabs.length) {
        activeTabId = openTabs[openTabs.length - 1].id;
      } else {
        activeTabId = openTabs[tabIndex].id;
      }
    }
  }

  function selectTab(tabId: string): void {
    activeTabId = tabId;
  }

  function updateTabContent(tabId: string, content: string): void {
    const tab = openTabs.find(t => t.id === tabId);
    if (tab) {
      tab.content = content;
      tab.isDirty = content !== tab.originalContent;
      tab.size = new Blob([content]).size;
    }
  }

  function updateTabFormat(tabId: string, format: FileFormat): void {
    const tab = openTabs.find(t => t.id === tabId);
    if (tab) {
      tab.format = format;
      tab.meta.format = format;
      tab.isDirty = true;
    }
  }

  function updateTabMeta(tabId: string, meta: Partial<FileMeta>): void {
    const tab = openTabs.find(t => t.id === tabId);
    if (tab) {
      tab.meta = { ...tab.meta, ...meta };
      tab.isDirty = true;
    }
  }

  async function saveTab(tabId: string): Promise<void> {
    const tab = openTabs.find(t => t.id === tabId);
    if (!tab) return;

    try {
      await saveFile(tab.path, tab.content, tab.meta);
      tab.originalContent = tab.content;
      tab.isDirty = false;
      tab.modifiedAt = Date.now();
      addToast(`Saved: ${tab.name}`, 'info');
    } catch (error) {
      console.error('Failed to save file:', error);
      addToast(`Failed to save: ${tab.name}`, 'alert');
      throw error;
    }
  }

  async function loadVersion(tabId: string, version: number): Promise<void> {
    const tab = openTabs.find(t => t.id === tabId);
    if (!tab) return;

    isLoading = true;
    try {
      const fileData = await fetchFile(tab.path, version);
      const content = decodeContent(fileData.data);
      
      tab.content = content;
      tab.originalContent = content;
      tab.version = version;
      tab.meta = fileData.meta;
      tab.isDirty = false;
      tab.size = new Blob([content]).size;
      addToast(`Loaded version ${version === 0 ? 'latest' : version}`, 'info');
    } catch (error) {
      console.error('Failed to load version:', error);
      addToast(`Failed to load version ${version}`, 'alert');
      throw error;
    } finally {
      isLoading = false;
    }
  }

  // Search operations
  function searchInContent(query: string): void {
    searchQuery = query;
    
    if (!query.trim()) {
      searchResults = [];
      return;
    }

    isSearching = true;
    const results: SearchResult[] = [];
    const lowerQuery = query.toLowerCase();

    // Search in all open tabs
    for (const tab of openTabs) {
      const lines = tab.content.split('\n');
      for (let i = 0; i < lines.length; i++) {
        const line = lines[i];
        const lowerLine = line.toLowerCase();
        let searchIndex = 0;
        
        searchIndex = lowerLine.indexOf(lowerQuery, searchIndex);
        while (searchIndex !== -1) {
          results.push({
            path: tab.path,
            line: i + 1,
            content: line.trim(),
            matchStart: searchIndex,
            matchEnd: searchIndex + query.length
          });
          searchIndex += query.length;
          searchIndex = lowerLine.indexOf(lowerQuery, searchIndex);
        }
      }
    }

    searchResults = results;
    isSearching = false;
  }

  function clearSearch(): void {
    searchQuery = '';
    searchResults = [];
  }

  // Settings operations
  async function loadSettings(): Promise<void> {
    settings = await fetchSettings();
  }

  // Create operations
  async function createNewFolder(parentPath: string, name: string): Promise<void> {
    const fullPath = parentPath ? `${parentPath}/${name}` : name;
    
    try {
      await createFolder(fullPath);
      addToast(`Created folder: ${name}`, 'info');
      
      // Refresh the parent folder in the tree
      await refreshFolder(parentPath);
    } catch (error) {
      console.error('Failed to create folder:', error);
      addToast(`Failed to create folder: ${name}`, 'alert');
      throw error;
    }
  }

  async function createNewFile(parentPath: string, name: string): Promise<void> {
    const fullPath = parentPath ? `${parentPath}/${name}` : name;
    // Default to JSON format - user can change in Settings panel
    const format: FileFormat = 'json';
    const defaultContent = '{\n  \n}';
    
    try {
      await createFile(fullPath, defaultContent, { format });
      addToast(`Created file: ${name}`, 'info');
      
      // Refresh the parent folder in the tree
      await refreshFolder(parentPath);
      
      // Open the newly created file
      await openFile(fullPath);
    } catch (error) {
      console.error('Failed to create file:', error);
      addToast(`Failed to create file: ${name}`, 'alert');
      throw error;
    }
  }

  async function refreshFolder(folderPath: string): Promise<void> {
    if (!tree) return;
    
    if (folderPath === '' || folderPath === '/') {
      // Refresh root
      await loadTree();
      return;
    }
    
    // Find the folder node and refresh it
    const node = findNodeByPath(tree, folderPath);
    if (node && node.type === 'folder') {
      node.loaded = false;
      await expandFolder(node);
    } else {
      // If we can't find the node, refresh the whole tree
      await loadTree();
    }
  }

  function findNodeByPath(node: TreeNode, path: string): TreeNode | null {
    if (node.path === path) {
      return node;
    }
    
    if (node.children) {
      for (const child of node.children) {
        const found = findNodeByPath(child, path);
        if (found) return found;
      }
    }
    
    return null;
  }

  // Delete operations
  async function deleteFile(path: string): Promise<void> {
    try {
      await axios.delete(`/api/v1/file/${path}`, {
        params: { version: 0 } // Delete all versions
      });
      
      // Close the tab if it's open
      const tab = openTabs.find(t => t.path === path);
      if (tab) {
        closeTab(tab.id);
      }
      
      // Get parent folder path and refresh
      const parts = path.split('/');
      parts.pop();
      const parentPath = parts.join('/');
      
      addToast(`Deleted file: ${path.split('/').pop()}`, 'info');
      await refreshFolder(parentPath);
    } catch (error) {
      console.error('Failed to delete file:', error);
      addToast(`Failed to delete file`, 'alert');
      throw error;
    }
  }

  async function deleteFolder(path: string): Promise<void> {
    try {
      await axios.delete(`/api/v1/folder/${path}`);
      
      // Close any tabs that are within this folder
      const tabsToClose = openTabs.filter(t => t.path.startsWith(path + '/') || t.path === path);
      for (const tab of tabsToClose) {
        closeTab(tab.id);
      }
      
      // Get parent folder path and refresh
      const parts = path.split('/');
      parts.pop();
      const parentPath = parts.join('/');
      
      addToast(`Deleted folder: ${path.split('/').pop()}`, 'info');
      await refreshFolder(parentPath);
    } catch (error) {
      console.error('Failed to delete folder:', error);
      addToast(`Failed to delete folder`, 'alert');
      throw error;
    }
  }

  // Panel width operations
  function setLeftPanelWidth(width: number): void {
    leftPanelWidth = Math.max(150, Math.min(500, width));
  }

  function setRightPanelWidth(width: number): void {
    rightPanelWidth = Math.max(200, Math.min(500, width));
  }

  return {
    // State getters
    get tree() { return tree; },
    get openTabs() { return openTabs; },
    get activeTabId() { return activeTabId; },
    get activeTab() { return activeTab; },
    get searchQuery() { return searchQuery; },
    get searchResults() { return searchResults; },
    get isSearching() { return isSearching; },
    get settings() { return settings; },
    get isLoading() { return isLoading; },
    get hasUnsavedChanges() { return hasUnsavedChanges; },
    get leftPanelWidth() { return leftPanelWidth; },
    get rightPanelWidth() { return rightPanelWidth; },

    // Tree operations
    loadTree,
    expandFolder,
    collapseFolder,
    toggleFolder,

    // Tab operations
    openFile,
    closeTab,
    selectTab,
    updateTabContent,
    updateTabFormat,
    updateTabMeta,
    saveTab,
    loadVersion,

    // Search operations
    searchInContent,
    clearSearch,

    // Settings operations
    loadSettings,

    // Create operations
    createNewFolder,
    createNewFile,

    // Delete operations
    deleteFile,
    deleteFolder,

    // Panel operations
    setLeftPanelWidth,
    setRightPanelWidth
  };
}

export const configStore = createConfigStore();
