import type {
  Tab, TreeNode, SearchResult, FileFormat, FileVersion, FileMeta,
  Settings, TokenInfo, CreateTokenRequest, CreateTokenResponse,
  PatchTokenRequest, ViewMode
} from '@/lib/types/config';
import { addToast } from '@/lib/store/toast.svelte';
import { basePath } from '@/lib/basepath';
import axios from 'axios';

// Helper to decode base64 data (supports Unicode)
function decodeContent(data: string): string {
  if (!data) return '';
  try {
    const binaryStr = atob(data);
    const bytes = Uint8Array.from(binaryStr, c => c.charCodeAt(0));
    return new TextDecoder().decode(bytes);
  } catch {
    return data;
  }
}

// Helper to encode content to base64 (supports Unicode)
function encodeContent(content: string): string {
  if (!content) return '';
  try {
    const bytes = new TextEncoder().encode(content);
    const binaryStr = Array.from(bytes, b => String.fromCharCode(b)).join('');
    return btoa(binaryStr);
  } catch {
    return content;
  }
}

// Helper to get default content for a given format
function defaultContentForFormat(format: FileFormat): string {
  switch (format) {
    case 'json': return '{\n  \n}';
    case 'yaml': return '';
    case 'toml': return '';
    default: return '';
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
  let tokens = $state<TokenInfo[]>([]);
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

  async function fetchFile(path: string, version: number = 0, variantKey?: string): Promise<{
    meta: FileMeta;
    data: string;
    versions: FileVersion[];
  }> {
    const params: any = {};
    if (version) params.version = version;
    if (variantKey) params.variant = variantKey;

    const versionsParams: any = {};
    if (variantKey) versionsParams.variant = variantKey;

    // Fetch file data and versions in parallel
    const [response, versionsResult] = await Promise.all([
      axios.get(`/api/v1/file/${path}`, { params }),
      axios.get(`/api/v1/versions/${path}`, { params: versionsParams }).catch(() => ({ data: [] }))
    ]);

    return {
      meta: response.data.meta || {},
      data: response.data.data || '',
      versions: versionsResult.data || [],
    };
  }

  async function saveFile(path: string, content: string, meta: FileMeta, expectedVersion?: number, constraint?: string, variantKey?: string, rawData?: string): Promise<{ version: number }> {
    // For raw format, use rawData directly if available (preserves binary fidelity)
    const encodedData = (meta.format === 'raw' && rawData) ? rawData : encodeContent(content);

    const body: any = {
      meta,
      data: encodedData,
    };
    if (expectedVersion !== undefined && expectedVersion > 0) {
      body.expected_version = expectedVersion;
    }
    if (constraint) {
      body.constraint = constraint;
    }

    const params: any = {};
    if (variantKey) params.variant = variantKey;

    const response = await axios.post(`/api/v1/file/${path}`, body, { params });
    return { version: response.data.version };
  }

  async function createFolder(path: string): Promise<void> {
    await axios.post(`/api/v1/folder/${path}`);
  }

  async function createFile(path: string, content: string = '', meta: FileMeta = {}, variantKey?: string): Promise<void> {
    const params: any = {};
    if (variantKey) params.variant = variantKey;

    await axios.post(`/api/v1/file/${path}`, {
      meta,
      data: encodeContent(content),
    }, { params });
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
      const variants = rootData.variants || {};
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
            type: 'file' as const,
            children: (variants[name] || []).map((vk: string) => ({
              name: '@' + vk,
              path: name,
              type: 'variant' as const,
              variantKey: vk,
              parentPath: name
            }))
          }))
        ]
      };
    } catch {
      // Silently show empty tree — no configs exist yet or server is not ready.
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
      const variants = data.variants || {};
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
          type: 'file' as const,
          children: (variants[name] || []).map((vk: string) => ({
            name: '@' + vk,
            path: `${node.path}/${name}`,
            type: 'variant' as const,
            variantKey: vk,
            parentPath: `${node.path}/${name}`
          }))
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
      updateURL();
      return;
    }

    try {
      const fileData = await fetchFile(path);
      const name = path.split('/').pop() || path;
      const format = fileData.meta.format || 'yaml';
      const isRaw = format === 'raw';

      // For raw format, skip text decode (could be huge binary) and default to hex view
      const content = isRaw ? '' : decodeContent(fileData.data);

      // Determine the latest version number from version history
      const latestVersion = fileData.versions.length > 0
        ? Math.max(...fileData.versions.map(v => v.version))
        : 0;

      // Calculate size from raw base64 data length
      const rawSize = fileData.data ? Math.floor(fileData.data.length * 3 / 4) : 0;

      const newTab: Tab = {
        id: path,
        path,
        name,
        content,
        originalContent: content,
        format,
        version: 0, // Latest
        versions: fileData.versions,
        latestVersion,
        meta: fileData.meta,
        isDirty: false,
        size: isRaw ? rawSize : new Blob([content]).size,
        modifiedAt: Date.now(),
        rawData: fileData.data,
        originalRawData: fileData.data,
        viewMode: isRaw ? 'hex' : 'text',
      };

      openTabs = [...openTabs, newTab];
      activeTabId = newTab.id;
      updateURL();
    } catch (error) {
      console.error('Failed to open file:', error);
      addToast(`Failed to open file: ${path}`, 'alert');
      throw error;
    }
  }

  async function openVariant(filePath: string, variantKey: string): Promise<void> {
    const tabId = `${filePath}@${variantKey}`;

    // Check if already open
    const existingTab = openTabs.find(t => t.id === tabId);
    if (existingTab) {
      activeTabId = existingTab.id;
      updateURL();
      return;
    }

    try {
      const fileData = await fetchFile(filePath, 0, variantKey);
      const name = `${filePath.split('/').pop() || filePath}@${variantKey}`;
      const format = fileData.meta.format || 'yaml';
      const isRaw = format === 'raw';

      // For raw format, skip text decode and default to hex view
      const content = isRaw ? '' : decodeContent(fileData.data);

      const latestVersion = fileData.versions.length > 0
        ? Math.max(...fileData.versions.map(v => v.version))
        : 0;

      const rawSize = fileData.data ? Math.floor(fileData.data.length * 3 / 4) : 0;

      const newTab: Tab = {
        id: tabId,
        path: filePath,
        name,
        variantKey,
        content,
        originalContent: content,
        format,
        version: 0,
        versions: fileData.versions,
        latestVersion,
        meta: fileData.meta,
        isDirty: false,
        size: isRaw ? rawSize : new Blob([content]).size,
        modifiedAt: Date.now(),
        rawData: fileData.data,
        originalRawData: fileData.data,
        viewMode: isRaw ? 'hex' : 'text',
      };

      openTabs = [...openTabs, newTab];
      activeTabId = newTab.id;
      updateURL();
    } catch (error: any) {
      if (error.response?.status === 404) {
        // Variant doesn't exist yet — create it
        const format: FileFormat = 'yaml';
        const defaultContent = '';

        await createFile(filePath, defaultContent, { format }, variantKey);
        // Retry open
        return openVariant(filePath, variantKey);
      }
      console.error('Failed to open variant:', error);
      addToast(`Failed to open variant: @${variantKey}`, 'alert');
      throw error;
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
    updateURL();
  }

  function selectTab(tabId: string): void {
    activeTabId = tabId;
    updateURL();
  }

  function updateTabContent(tabId: string, content: string): void {
    const tab = openTabs.find(t => t.id === tabId);
    if (tab) {
      tab.content = content;
      tab.rawData = encodeContent(content);
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

  // Variant operations
  async function createVariant(filePath: string, variantKey: string): Promise<void> {
    try {
      const format: FileFormat = 'yaml';
      await createFile(filePath, '', { format }, variantKey);
      addToast(`Created variant: @${variantKey}`, 'success');

      // Refresh parent folder to show the new variant
      const parts = filePath.split('/');
      parts.pop();
      await refreshFolder(parts.join('/'));

      // Open the new variant
      await openVariant(filePath, variantKey);
    } catch (error) {
      console.error('Failed to create variant:', error);
      addToast(`Failed to create variant: @${variantKey}`, 'alert');
      throw error;
    }
  }

  async function deleteVariant(filePath: string, variantKey: string): Promise<void> {
    try {
      await axios.delete(`/api/v1/file/${filePath}`, {
        params: { variant: variantKey, version: 0 }
      });

      // Close tab if open
      const tabId = `${filePath}@${variantKey}`;
      const tab = openTabs.find(t => t.id === tabId);
      if (tab) closeTab(tab.id);

      // Refresh parent folder
      const parts = filePath.split('/');
      parts.pop();
      await refreshFolder(parts.join('/'));

      addToast(`Deleted variant: @${variantKey}`, 'success');
    } catch (error) {
      console.error('Failed to delete variant:', error);
      addToast('Failed to delete variant', 'alert');
      throw error;
    }
  }

  async function saveTab(tabId: string, constraint?: string): Promise<void> {
    const tab = openTabs.find(t => t.id === tabId);
    if (!tab) return;

    try {
      const result = await saveFile(tab.path, tab.content, tab.meta, tab.latestVersion, constraint, tab.variantKey, tab.rawData);
      tab.originalContent = tab.content;
      tab.originalRawData = tab.rawData;
      tab.isDirty = false;
      tab.modifiedAt = Date.now();
      tab.version = 0; // Reset to latest after save
      tab.latestVersion = result.version; // Track the new version we just created

      // Refresh version list
      try {
        const params: any = {};
        if (tab.variantKey) params.variant = tab.variantKey;
        const versionsResponse = await axios.get(`/api/v1/versions/${tab.path}`, { params });
        tab.versions = versionsResponse.data || [];
      } catch {
        // Ignore version refresh errors
      }

      addToast(`Saved: ${tab.name}`, 'success');
    } catch (error: any) {
      if (error.response?.status === 409) {
        addToast(`Conflict: "${tab.name}" was modified by another user. Reload to get the latest version.`, 'alert', 8000);
      } else {
        addToast(`Failed to save: ${tab.name}`, 'alert');
      }
      throw error;
    }
  }

  async function updateVersionConstraint(tabId: string, version: number, constraint: string): Promise<void> {
    const tab = openTabs.find(t => t.id === tabId);
    if (!tab) return;

    try {
      const params: any = {};
      if (tab.variantKey) params.variant = tab.variantKey;

      await axios.patch(`/api/v1/versions/${tab.path}`, { version, constraint }, { params });

      // Update the constraint in the local versions list
      const ver = tab.versions.find(v => v.version === version);
      if (ver) {
        ver.constraint = constraint;
      }

      addToast(constraint ? `Constraint set to ${constraint}` : 'Constraint removed', 'success');
    } catch (error: any) {
      const msg = error.response?.data?.message || 'Failed to update constraint';
      addToast(msg, 'alert');
      throw error;
    }
  }

  async function loadVersion(tabId: string, version: number): Promise<void> {
    const tab = openTabs.find(t => t.id === tabId);
    if (!tab) return;

    try {
      const fileData = await fetchFile(tab.path, version, tab.variantKey);
      const isRaw = tab.format === 'raw';

      // For raw format, skip text decode and switch to hex view
      const content = isRaw ? '' : decodeContent(fileData.data);

      tab.content = content;
      tab.originalContent = content;
      tab.version = version;
      tab.versions = fileData.versions;
      tab.latestVersion = fileData.versions.length > 0
        ? Math.max(...fileData.versions.map(v => v.version))
        : 0;
      tab.meta = fileData.meta;
      tab.rawData = fileData.data;
      tab.originalRawData = fileData.data;
      tab.isDirty = false;
      if (isRaw) {
        tab.viewMode = 'hex';
        tab.size = fileData.data ? Math.floor(fileData.data.length * 3 / 4) : 0;
      } else {
        tab.size = new Blob([content]).size;
      }
      addToast(`Loaded version ${version === 0 ? 'latest' : version}`, 'info');
    } catch (error) {
      console.error('Failed to load version:', error);
      addToast(`Failed to load version ${version}`, 'alert');
      throw error;
    }
  }

  // Search operations
  let searchAbortController: AbortController | null = null;

  function search(query: string): void {
    // Cancel any ongoing search
    cancelSearch();

    searchQuery = query;

    if (!query.trim()) {
      searchResults = [];
      isSearching = false;
      return;
    }

    isSearching = true;
    searchResults = [];

    const controller = new AbortController();
    searchAbortController = controller;

    const eventSource = new EventSource(`${basePath}/api/v1/search?q=${encodeURIComponent(query.trim())}`);

    // Handle abort — close the connection
    controller.signal.addEventListener('abort', () => {
      eventSource.close();
      isSearching = false;
    });

    eventSource.onmessage = (event) => {
      if (controller.signal.aborted) return;

      try {
        const result: SearchResult = JSON.parse(event.data);
        searchResults = [...searchResults, result];
      } catch {
        // skip bad data
      }
    };

    eventSource.addEventListener('done', () => {
      eventSource.close();
      isSearching = false;
      searchAbortController = null;
    });

    eventSource.onerror = () => {
      eventSource.close();
      isSearching = false;
      searchAbortController = null;
    };
  }

  function cancelSearch(): void {
    if (searchAbortController) {
      searchAbortController.abort();
      searchAbortController = null;
    }
    isSearching = false;
  }

  function clearSearch(): void {
    cancelSearch();
    searchQuery = '';
    searchResults = [];
  }

  // Settings operations
  async function loadSettings(): Promise<void> {
    settings = await fetchSettings();
  }

  async function saveSettings(updatedSettings: Settings): Promise<void> {
    try {
      const body: Record<string, any> = {
        action: 'set',
        external: updatedSettings.external || {}
      };
      // Include raw_mounts if present in the update
      if (updatedSettings.raw_mounts !== undefined) {
        body.raw_mounts = updatedSettings.raw_mounts;
      }
      await axios.post('/api/v1/settings', body);
      settings = updatedSettings;
      addToast('Settings saved', 'success');
    } catch (error) {
      console.error('Failed to save settings:', error);
      addToast('Failed to save settings', 'alert');
      throw error;
    }
  }

  async function saveFTPUsers(users: import('@/lib/types/config').FTPUserEntry[]): Promise<void> {
    try {
      await axios.post('/api/v1/settings', {
        action: 'set',
        ftp_users: users
      });
      if (settings) {
        settings = { ...settings, ftp_users: users };
      } else {
        settings = { ftp_users: users };
      }
      addToast('FTP users saved', 'success');
    } catch (error: any) {
      const msg = error.response?.data?.message || 'Failed to save FTP users';
      addToast(msg, 'alert');
      throw error;
    }
  }

  async function saveFTPShares(shares: import('@/lib/types/config').FTPShareEntry[]): Promise<void> {
    try {
      await axios.post('/api/v1/settings', {
        action: 'set',
        ftp_shares: shares
      });
      if (settings) {
        settings = { ...settings, ftp_shares: shares };
      } else {
        settings = { ftp_shares: shares };
      }
      addToast('FTP shares saved', 'success');
    } catch (error: any) {
      const msg = error.response?.data?.message || 'Failed to save FTP shares';
      addToast(msg, 'alert');
      throw error;
    }
  }

  async function saveServeSettings(patch: {
    ftp_serve?: import('@/lib/types/config').FTPServeSettings,
    sftp_serve?: import('@/lib/types/config').SFTPServeSettings,
    tftp_serve?: import('@/lib/types/config').TFTPServeSettings,
  }): Promise<void> {
    try {
      await axios.post('/api/v1/settings', {
        action: 'set',
        ...patch
      });
      if (settings) {
        settings = { ...settings, ...patch };
      } else {
        settings = { ...patch };
      }
      addToast('Server settings saved.', 'success');
    } catch (error: any) {
      const msg = error.response?.data?.message || 'Failed to save server settings';
      addToast(msg, 'alert');
      throw error;
    }
  }

  async function saveHooks(hooks: import('@/lib/types/config').Hook[]): Promise<void> {
    try {
      await axios.post('/api/v1/settings', {
        action: 'set',
        hooks: hooks
      });
      if (settings) {
        settings = { ...settings, hooks: hooks };
      } else {
        settings = { hooks: hooks };
      }
      addToast('Hooks saved', 'success');
    } catch (error: any) {
      const msg = error.response?.data?.message || 'Failed to save hooks';
      addToast(msg, 'alert');
      throw error;
    }
  }

  async function saveRawMounts(mounts: import('@/lib/types/config').RawMountEntry[]): Promise<void> {
    try {
      await axios.post('/api/v1/settings', {
        action: 'set',
        raw_mounts: mounts
      });
      if (settings) {
        settings = { ...settings, raw_mounts: mounts };
      } else {
        settings = { raw_mounts: mounts };
      }
      addToast('Raw mounts saved', 'success');
    } catch (error: any) {
      const msg = error.response?.data?.message || 'Failed to save raw mounts';
      addToast(msg, 'alert');
      throw error;
    }
  }

  async function listExternalPaths(resourceName: string, prefix: string = ''): Promise<string[]> {
    try {
      const response = await axios.get(`/api/v1/external/${resourceName}/paths`, {
        params: prefix ? { prefix } : undefined
      });
      return response.data || [];
    } catch {
      return [];
    }
  }

  // Token operations
  async function loadTokens(): Promise<void> {
    try {
      const response = await axios.get('/api/v1/tokens');
      tokens = response.data || [];
    } catch {
      tokens = [];
    }
  }

  async function createToken(req: CreateTokenRequest): Promise<CreateTokenResponse> {
    const response = await axios.post('/api/v1/tokens', req);
    await loadTokens();
    return response.data;
  }

  async function deleteToken(id: string): Promise<void> {
    await axios.delete(`/api/v1/tokens/${id}`);
    await loadTokens();
    addToast('Token deleted', 'success');
  }

  async function patchToken(id: string, req: PatchTokenRequest): Promise<void> {
    await axios.patch(`/api/v1/tokens/${id}`, req);
    await loadTokens();
    addToast('Token updated', 'success');
  }

  // Admin secret operations
  async function fetchAdminSecretStatus(): Promise<{ configured: boolean }> {
    try {
      const response = await axios.get('/api/v1/admin-secret/status');
      return response.data;
    } catch {
      return { configured: false };
    }
  }

  async function setAdminSecret(currentSecret: string, newSecret: string): Promise<void> {
    await axios.put('/api/v1/admin-secret', {
      current_secret: currentSecret,
      new_secret: newSecret,
    });
  }

  // Create operations
  async function createNewFolder(parentPath: string, name: string): Promise<void> {
    const fullPath = parentPath ? `${parentPath}/${name}` : name;

    try {
      await createFolder(fullPath);
      addToast(`Created folder: ${name}`, 'success');

      // Refresh the parent folder in the tree
      await refreshFolder(parentPath);
    } catch (error) {
      console.error('Failed to create folder:', error);
      addToast(`Failed to create folder: ${name}`, 'alert');
      throw error;
    }
  }

  async function createNewFile(parentPath: string, name: string, format: FileFormat = 'yaml'): Promise<void> {
    const fullPath = parentPath ? `${parentPath}/${name}` : name;
    const defaultContent = defaultContentForFormat(format);

    try {
      await createFile(fullPath, defaultContent, { format });
      addToast(`Created file: ${name}`, 'success');

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

      addToast(`Deleted file: ${path.split('/').pop()}`, 'success');
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

      addToast(`Deleted folder: ${path.split('/').pop()}`, 'success');
      await refreshFolder(parentPath);
    } catch (error) {
      console.error('Failed to delete folder:', error);
      addToast(`Failed to delete folder`, 'alert');
      throw error;
    }
  }

  // View mode operations
  function setTabViewMode(tabId: string, mode: ViewMode): void {
    const tab = openTabs.find(t => t.id === tabId);
    if (!tab) return;

    // When switching to text mode and content is empty (raw file that was never decoded),
    // lazily decode rawData into content
    if (mode === 'text' && !tab.content && tab.rawData) {
      const content = decodeContent(tab.rawData);
      tab.content = content;
      tab.originalContent = content;
    }

    tab.viewMode = mode;
  }

  // File import operations
  async function importFileToTab(tabId: string, file: globalThis.File): Promise<void> {
    const tab = openTabs.find(t => t.id === tabId);
    if (!tab) return;

    return new Promise((resolve, reject) => {
      const reader = new FileReader();

      reader.onload = () => {
        const arrayBuffer = reader.result as ArrayBuffer;
        const bytes = new Uint8Array(arrayBuffer);

        // Convert to base64 in chunks to avoid call stack overflow on large files
        const chunkSize = 8192;
        let binaryStr = '';
        for (let i = 0; i < bytes.length; i += chunkSize) {
          const chunk = bytes.subarray(i, i + chunkSize);
          binaryStr += String.fromCharCode(...chunk);
        }
        const base64Data = btoa(binaryStr);

        // Store raw base64
        tab.rawData = base64Data;
        tab.size = bytes.length;
        tab.isDirty = true;

        if (tab.format === 'raw') {
          // For raw format, don't decode to text — stay in hex view
          tab.content = '';
          tab.viewMode = 'hex';
        } else {
          // For text formats, decode as UTF-8 for the editor
          try {
            const textContent = new TextDecoder('utf-8', { fatal: false }).decode(bytes);
            tab.content = textContent;
          } catch {
            tab.content = '';
          }
        }

        addToast(`Imported: ${file.name} (${formatImportSize(bytes.length)})`, 'success');
        resolve();
      };

      reader.onerror = () => {
        addToast(`Failed to read file: ${file.name}`, 'alert');
        reject(new Error('Failed to read file'));
      };

      reader.readAsArrayBuffer(file);
    });
  }

  function formatImportSize(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }

  // URL deep linking operations
  function updateURL(): void {
    const tab = openTabs.find(t => t.id === activeTabId);
    const base = '#/configurations';
    if (tab) {
      let url = `${base}?file=${encodeURIComponent(tab.path)}`;
      if (tab.variantKey) {
        url += `&variant=${encodeURIComponent(tab.variantKey)}`;
      }
      history.replaceState(null, '', url);
    } else {
      history.replaceState(null, '', base);
    }
  }

  function openFromURL(): void {
    const hash = window.location.hash;
    const qsIndex = hash.indexOf('?');
    if (qsIndex === -1) return;

    const params = new URLSearchParams(hash.slice(qsIndex));
    const file = params.get('file');
    const variant = params.get('variant');

    if (file) {
      if (variant) {
        openVariant(file, variant);
      } else {
        openFile(file);
      }
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
    get tokens() { return tokens; },
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
    updateVersionConstraint,

    // Variant operations
    openVariant,
    createVariant,
    deleteVariant,

    // View mode & import operations
    setTabViewMode,
    importFileToTab,

    // Search operations
    search,
    cancelSearch,
    clearSearch,

    // Settings operations
    loadSettings,
    saveSettings,
    saveRawMounts,
    saveFTPShares,
    saveFTPUsers,
    saveServeSettings,
    saveHooks,
    listExternalPaths,

    // Token operations
    loadTokens,
    createToken,
    deleteToken,
    patchToken,

    // Admin secret operations
    fetchAdminSecretStatus,
    setAdminSecret,

    // Create operations
    createNewFolder,
    createNewFile,

    // Delete operations
    deleteFile,
    deleteFolder,

    // URL deep linking
    openFromURL,

    // Panel operations
    setLeftPanelWidth,
    setRightPanelWidth
  };
}

export const configStore = createConfigStore();
