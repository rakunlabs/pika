<script lang="ts">
  import { appStore, type UserInfo, type UserQuery, type PermissionInfo } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { onMount } from 'svelte';
  import { Plus, Trash2, UserCheck, UserX, KeyRound, LogOut, Search, ChevronUp, ChevronDown, ChevronsUpDown, ChevronLeft, ChevronRight, Shield, ShieldCheck, Check } from 'lucide-svelte';

  // Tab state
  let activeTab = $state<'users' | 'permissions'>('users');

  // User state
  let showCreateForm = $state(false);
  let newUsername = $state('');
  let newPassword = $state('');
  let creating = $state(false);

  let editingUser = $state<UserInfo | null>(null);
  let editPassword = $state('');
  let editUsername = $state('');
  let editTab = $state<'details' | 'permissions'>('details');
  let editUserPermissionIds = $state<string[]>([]);
  let loadingUserPerms = $state(false);

  let confirmDeleteId = $state<string | null>(null);

  // Query state
  let searchText = $state('');
  let sortField = $state('username');
  let sortDir = $state<'asc' | 'desc'>('asc');
  let pageSize = $state(20);
  let currentPage = $state(1);
  let searchTimeout = $state<ReturnType<typeof setTimeout> | null>(null);

  // Permission state
  let showCreatePermForm = $state(false);
  let newPermKey = $state('');
  let newPermName = $state('');
  let newPermDesc = $state('');
  let newPermKeys = $state<string[]>([]);
  let creatingPerm = $state(false);
  let editingPerm = $state<PermissionInfo | null>(null);
  let editPermKey = $state('');
  let editPermName = $state('');
  let editPermDesc = $state('');
  let editPermKeys = $state<string[]>([]);
  let confirmDeletePermId = $state<string | null>(null);

  const knownKeys: { key: string; name: string; description: string }[] = [
    { key: 'users.manage', name: 'User Management', description: 'Create, edit, delete and kick users' },
    { key: 'permissions.manage', name: 'Permission Management', description: 'Create, edit, delete permissions and assign them to users' },
    { key: 'settings.manage', name: 'Settings Management', description: 'View and modify server settings, admin secret, backup/restore, key rotation' },
    { key: 'tokens.manage', name: 'Token Management', description: 'Create, edit and revoke API access tokens' },
    { key: 'files.read', name: 'Configurations Read', description: 'View folders, files, versions, variants, render and search configurations' },
    { key: 'files.write', name: 'Configurations Write', description: 'Create, update and delete folders and configuration files' },
    { key: 'raw.read', name: 'Raw Files Read', description: 'Browse and download raw files from mounted filesystems' },
    { key: 'raw.write', name: 'Raw Files Write', description: 'Upload, delete, rename, copy and move raw files' },
  ];

  const users = $derived(appStore.users);
  const total = $derived(appStore.usersTotal);
  const currentUser = $derived(appStore.info?.user);
  const allPermissions = $derived(appStore.permissions);
  const totalPages = $derived(Math.max(1, Math.ceil(total / pageSize)));
  const showingFrom = $derived(total === 0 ? 0 : (currentPage - 1) * pageSize + 1);
  const showingTo = $derived(Math.min(currentPage * pageSize, total));

  function buildQuery(): UserQuery {
    return {
      limit: pageSize,
      offset: (currentPage - 1) * pageSize,
      sort: sortDir === 'desc' ? `-${sortField}` : sortField,
      search: searchText || undefined,
    };
  }

  function reload() {
    appStore.loadUsers(buildQuery());
  }

  onMount(() => {
    reload();
    appStore.loadPermissions();
  });

  function handleSearch(value: string) {
    searchText = value;
    if (searchTimeout) clearTimeout(searchTimeout);
    searchTimeout = setTimeout(() => {
      currentPage = 1;
      reload();
    }, 300);
  }

  function handleSort(field: string) {
    if (sortField === field) {
      sortDir = sortDir === 'asc' ? 'desc' : 'asc';
    } else {
      sortField = field;
      sortDir = 'asc';
    }
    currentPage = 1;
    reload();
  }

  function goToPage(page: number) {
    if (page < 1 || page > totalPages) return;
    currentPage = page;
    reload();
  }

  function handlePageSizeChange(size: number) {
    pageSize = size;
    currentPage = 1;
    reload();
  }

  async function handleCreate() {
    if (!newUsername || !newPassword) return;
    creating = true;
    try {
      await appStore.createUser(newUsername, newPassword);
      addToast(`User "${newUsername}" created`, 'success');
      newUsername = '';
      newPassword = '';
      showCreateForm = false;
    } catch (err: any) {
      addToast(err?.response?.data?.message || 'Failed to create user', 'alert');
    } finally {
      creating = false;
    }
  }

  async function handleToggleDisabled(user: UserInfo) {
    try {
      await appStore.updateUser(user.id, { disabled: !user.disabled });
      addToast(`User "${user.username}" ${user.disabled ? 'enabled' : 'disabled'}`, 'success');
    } catch (err: any) {
      addToast(err?.response?.data?.message || 'Failed to update user', 'alert');
    }
  }

  async function handleDelete(id: string) {
    try {
      await appStore.deleteUser(id);
      addToast('User deleted', 'success');
      confirmDeleteId = null;
    } catch (err: any) {
      addToast(err?.response?.data?.message || 'Failed to delete user', 'alert');
    }
  }

  async function handleKick(user: UserInfo) {
    try {
      await appStore.kickUser(user.id);
      addToast(`All sessions for "${user.username}" terminated`, 'success');
    } catch (err: any) {
      addToast(err?.response?.data?.message || 'Failed to kick user', 'alert');
    }
  }

  async function startEdit(user: UserInfo) {
    editingUser = user;
    editUsername = user.username;
    editPassword = '';
    editTab = 'details';
    editUserPermissionIds = [];
    loadingUserPerms = true;
    try {
      const perms = await appStore.getUserPermissions(user.id);
      editUserPermissionIds = perms.map((p: PermissionInfo) => p.id);
    } catch {
      editUserPermissionIds = [];
    } finally {
      loadingUserPerms = false;
    }
  }

  function toggleEditPermission(permId: string) {
    if (editUserPermissionIds.includes(permId)) {
      editUserPermissionIds = editUserPermissionIds.filter(id => id !== permId);
    } else {
      editUserPermissionIds = [...editUserPermissionIds, permId];
    }
  }

  async function handleSaveEdit() {
    if (!editingUser) return;

    try {
      // Save user details if changed
      const updates: { username?: string; password?: string } = {};
      if (editUsername && editUsername !== editingUser.username) {
        updates.username = editUsername;
      }
      if (editPassword) {
        updates.password = editPassword;
      }
      if (Object.keys(updates).length > 0) {
        await appStore.updateUser(editingUser.id, updates);
      }

      // Save permissions
      if (!editingUser.is_superadmin) {
        await appStore.setUserPermissions(editingUser.id, editUserPermissionIds);
      }

      addToast('User updated', 'success');
      editingUser = null;
    } catch (err: any) {
      addToast(err?.response?.data?.message || 'Failed to update user', 'alert');
    }
  }

  function toggleNewPermKey(key: string) {
    if (newPermKeys.includes(key)) {
      newPermKeys = newPermKeys.filter(k => k !== key);
    } else {
      newPermKeys = [...newPermKeys, key];
    }
  }

  function toggleEditPermKey(key: string) {
    if (editPermKeys.includes(key)) {
      editPermKeys = editPermKeys.filter(k => k !== key);
    } else {
      editPermKeys = [...editPermKeys, key];
    }
  }

  // Permission CRUD handlers
  async function handleCreatePerm() {
    if (!newPermName || newPermKeys.length === 0) return;
    creatingPerm = true;
    // Auto-generate key from name if not provided
    const key = newPermKey || newPermName.toLowerCase().replace(/\s+/g, '-').replace(/[^a-z0-9.-]/g, '');
    try {
      await appStore.createPermission(key, newPermName, newPermDesc, newPermKeys);
      addToast(`Permission "${newPermName}" created`, 'success');
      newPermKey = '';
      newPermName = '';
      newPermDesc = '';
      newPermKeys = [];
      showCreatePermForm = false;
    } catch (err: any) {
      addToast(err?.response?.data?.message || 'Failed to create permission', 'alert');
    } finally {
      creatingPerm = false;
    }
  }

  function startEditPerm(perm: PermissionInfo) {
    editingPerm = perm;
    editPermKey = perm.key;
    editPermName = perm.name;
    editPermDesc = perm.description;
    editPermKeys = [...(perm.keys || [])];
  }

  async function handleSaveEditPerm() {
    if (!editingPerm) return;
    try {
      const updates: { key?: string; name?: string; description?: string; keys?: string[] } = {};
      if (editPermKey !== editingPerm.key) updates.key = editPermKey;
      if (editPermName !== editingPerm.name) updates.name = editPermName;
      if (editPermDesc !== editingPerm.description) updates.description = editPermDesc;
      // Always send keys to allow updating the capability set
      const origKeys = [...(editingPerm.keys || [])].sort().join(',');
      const newKeys = [...editPermKeys].sort().join(',');
      if (origKeys !== newKeys) updates.keys = editPermKeys;

      if (Object.keys(updates).length > 0) {
        await appStore.updatePermission(editingPerm.id, updates);
        addToast('Permission updated', 'success');
      }
      editingPerm = null;
    } catch (err: any) {
      addToast(err?.response?.data?.message || 'Failed to update permission', 'alert');
    }
  }

  async function handleDeletePerm(id: string) {
    try {
      await appStore.deletePermission(id);
      addToast('Permission deleted', 'success');
      confirmDeletePermId = null;
    } catch (err: any) {
      addToast(err?.response?.data?.message || 'Failed to delete permission', 'alert');
    }
  }
</script>

<div class="h-full overflow-auto p-6">
  <div class="max-w-3xl mx-auto">
    <!-- Header with tabs -->
    <div class="flex items-center justify-between mb-4">
      <div class="flex items-center gap-1">
        <button
          onclick={() => { activeTab = 'users'; }}
          class="px-3 py-1.5 text-sm font-medium rounded-md transition-colors {activeTab === 'users' ? 'bg-slate-800 text-white' : 'text-slate-500 hover:text-slate-800 hover:bg-slate-100'}"
        >
          Users
        </button>
        <button
          onclick={() => { activeTab = 'permissions'; }}
          class="px-3 py-1.5 text-sm font-medium rounded-md transition-colors {activeTab === 'permissions' ? 'bg-slate-800 text-white' : 'text-slate-500 hover:text-slate-800 hover:bg-slate-100'}"
        >
          Permissions
        </button>
      </div>
      {#if activeTab === 'users'}
        <button
          onclick={() => { showCreateForm = !showCreateForm; }}
          class="flex items-center gap-1.5 px-3 py-1.5 bg-blue-500 text-white text-xs font-medium rounded-md hover:bg-blue-600 transition-colors"
        >
          <Plus size={14} />
          New User
        </button>
      {:else}
        <button
          onclick={() => { showCreatePermForm = !showCreatePermForm; }}
          class="flex items-center gap-1.5 px-3 py-1.5 bg-blue-500 text-white text-xs font-medium rounded-md hover:bg-blue-600 transition-colors"
        >
          <Plus size={14} />
          New Permission
        </button>
      {/if}
    </div>

    <!-- ========== USERS TAB ========== -->
    {#if activeTab === 'users'}
      <!-- Create User Form -->
      {#if showCreateForm}
        <div class="bg-white rounded-lg border border-slate-200 p-4 mb-4">
          <h3 class="text-sm font-medium text-slate-700 mb-3">Create New User</h3>
          <div class="flex gap-3 items-end">
            <div class="flex-1">
              <label for="new-username" class="block text-xs text-slate-500 mb-1">Username</label>
              <input id="new-username" type="text" bind:value={newUsername}
                class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                placeholder="username" />
            </div>
            <div class="flex-1">
              <label for="new-password" class="block text-xs text-slate-500 mb-1">Password</label>
              <input id="new-password" type="password" bind:value={newPassword}
                class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                placeholder="password" />
            </div>
            <button onclick={handleCreate} disabled={creating || !newUsername || !newPassword}
              class="px-4 py-1.5 bg-blue-500 text-white text-sm rounded-md hover:bg-blue-600 disabled:opacity-50 transition-colors">
              {creating ? 'Creating...' : 'Create'}
            </button>
            <button onclick={() => { showCreateForm = false; }}
              class="px-4 py-1.5 bg-white border border-slate-300 text-slate-600 text-sm rounded-md hover:bg-slate-50 transition-colors">
              Cancel
            </button>
          </div>
        </div>
      {/if}

      <!-- Search Bar -->
      <div class="relative mb-3">
        <Search size={14} class="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
        <input type="text" value={searchText} oninput={(e) => handleSearch(e.currentTarget.value)}
          class="w-full pl-9 pr-3 py-2 border border-slate-200 rounded-lg text-sm bg-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent placeholder-slate-400"
          placeholder="Search users..." />
      </div>

      <!-- Users Table -->
      <div class="bg-white rounded-lg border border-slate-200 overflow-hidden">
        <table class="w-full text-sm">
          <thead>
            <tr class="bg-slate-50 border-b border-slate-200">
              <th class="text-left px-4 py-2.5">
                <button onclick={() => handleSort('username')} class="flex items-center gap-1 text-xs font-medium text-slate-500 uppercase tracking-wider hover:text-slate-800 transition-colors">
                  Username
                  {#if sortField === 'username'}
                    {#if sortDir === 'asc'}<ChevronUp size={12} />{:else}<ChevronDown size={12} />{/if}
                  {:else}
                    <ChevronsUpDown size={12} class="text-slate-300" />
                  {/if}
                </button>
              </th>
              <th class="text-left px-4 py-2.5 text-xs font-medium text-slate-500 uppercase tracking-wider">Status</th>
              <th class="text-left px-4 py-2.5 text-xs font-medium text-slate-500 uppercase tracking-wider">Sessions</th>
              <th class="text-left px-4 py-2.5">
                <button onclick={() => handleSort('created_at')} class="flex items-center gap-1 text-xs font-medium text-slate-500 uppercase tracking-wider hover:text-slate-800 transition-colors">
                  Created
                  {#if sortField === 'created_at'}
                    {#if sortDir === 'asc'}<ChevronUp size={12} />{:else}<ChevronDown size={12} />{/if}
                  {:else}
                    <ChevronsUpDown size={12} class="text-slate-300" />
                  {/if}
                </button>
              </th>
              <th class="text-right px-4 py-2.5 text-xs font-medium text-slate-500 uppercase tracking-wider">Actions</th>
            </tr>
          </thead>
          <tbody>
            {#each users as user (user.id)}
              {@const isYou = user.username === currentUser}
              {@const isOnline = user.active_sessions > 0}
              <tr class="border-b border-slate-100 {isYou ? 'bg-blue-50/50 border-l-2 border-l-blue-400' : 'hover:bg-slate-50'}">
                <td class="px-4 py-3">
                  <div class="flex items-center gap-2">
                    <span class="relative flex h-2 w-2 shrink-0">
                      {#if isOnline && !user.disabled}
                        <span class="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75"></span>
                        <span class="relative inline-flex rounded-full h-2 w-2 bg-green-500"></span>
                      {:else}
                        <span class="relative inline-flex rounded-full h-2 w-2 bg-slate-300"></span>
                      {/if}
                    </span>
                    <span class="font-medium {isYou ? 'text-blue-800' : 'text-slate-800'}">{user.username}</span>
                    {#if isYou}
                      <span class="text-[10px] px-1.5 py-0.5 bg-blue-100 text-blue-700 rounded font-medium">you</span>
                    {/if}
                    {#if user.is_superadmin}
                      <span class="text-[10px] px-1.5 py-0.5 bg-amber-100 text-amber-700 rounded font-medium">superadmin</span>
                    {/if}
                  </div>
                </td>
                <td class="px-4 py-3">
                  {#if user.disabled}
                    <span class="inline-flex items-center gap-1 text-xs px-2 py-0.5 bg-red-50 text-red-600 rounded-full">
                      <UserX size={12} /> Disabled
                    </span>
                  {:else}
                    <span class="inline-flex items-center gap-1 text-xs px-2 py-0.5 bg-green-50 text-green-600 rounded-full">
                      <UserCheck size={12} /> Active
                    </span>
                  {/if}
                </td>
                <td class="px-4 py-3">
                  {#if isOnline}
                    <span class="inline-flex items-center gap-1 text-xs px-2 py-0.5 bg-green-50 text-green-700 rounded-full tabular-nums">{user.active_sessions}</span>
                  {:else}
                    <span class="text-xs text-slate-400">0</span>
                  {/if}
                </td>
                <td class="px-4 py-3 text-slate-500 text-xs">{new Date(user.created_at).toLocaleDateString()}</td>
                <td class="px-4 py-3 text-right">
                  <div class="flex items-center justify-end gap-1">
                    <button onclick={() => startEdit(user)} class="p-1.5 text-slate-400 hover:text-blue-500 hover:bg-blue-50 rounded transition-colors" title="Edit user">
                      <KeyRound size={14} />
                    </button>
                    {#if !isYou && isOnline}
                      <button onclick={() => handleKick(user)} class="p-1.5 text-slate-400 hover:text-orange-500 hover:bg-orange-50 rounded transition-colors" title="Kick user">
                        <LogOut size={14} />
                      </button>
                    {/if}
                    <button onclick={() => handleToggleDisabled(user)} class="p-1.5 text-slate-400 hover:text-amber-500 hover:bg-amber-50 rounded transition-colors"
                      title={user.disabled ? 'Enable user' : 'Disable user'} disabled={isYou}>
                      {#if user.disabled}<UserCheck size={14} />{:else}<UserX size={14} />{/if}
                    </button>
                    {#if !isYou}
                      {#if confirmDeleteId === user.id}
                        <button onclick={() => handleDelete(user.id)} class="px-2 py-1 text-xs bg-red-500 text-white rounded hover:bg-red-600 transition-colors">Confirm</button>
                        <button onclick={() => { confirmDeleteId = null; }} class="px-2 py-1 text-xs bg-slate-200 text-slate-600 rounded hover:bg-slate-300 transition-colors">Cancel</button>
                      {:else}
                        <button onclick={() => { confirmDeleteId = user.id; }} class="p-1.5 text-slate-400 hover:text-red-500 hover:bg-red-50 rounded transition-colors" title="Delete user">
                          <Trash2 size={14} />
                        </button>
                      {/if}
                    {/if}
                  </div>
                </td>
              </tr>
            {:else}
              <tr>
                <td colspan="5" class="px-4 py-8 text-center text-slate-400 text-sm">
                  {searchText ? 'No users matching your search' : 'No users found'}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>

        <!-- Pagination -->
        {#if total > 0}
          <div class="flex items-center justify-between px-4 py-3 border-t border-slate-200 bg-slate-50">
            <div class="flex items-center gap-2 text-xs text-slate-500">
              <span>Showing {showingFrom}-{showingTo} of {total}</span>
              <span class="text-slate-300">|</span>
              <label for="page-size" class="sr-only">Rows per page</label>
              <select id="page-size" value={pageSize} onchange={(e) => handlePageSizeChange(Number(e.currentTarget.value))}
                class="px-1.5 py-0.5 border border-slate-200 rounded text-xs bg-white focus:outline-none focus:ring-1 focus:ring-blue-500">
                <option value={10}>10 / page</option>
                <option value={20}>20 / page</option>
                <option value={50}>50 / page</option>
                <option value={100}>100 / page</option>
              </select>
            </div>
            <div class="flex items-center gap-1">
              <button onclick={() => goToPage(currentPage - 1)} disabled={currentPage <= 1}
                class="p-1 rounded text-slate-400 hover:text-slate-700 hover:bg-slate-200 disabled:opacity-30 disabled:cursor-not-allowed transition-colors" title="Previous page">
                <ChevronLeft size={16} />
              </button>
              <span class="text-xs text-slate-600 px-2 tabular-nums">{currentPage} / {totalPages}</span>
              <button onclick={() => goToPage(currentPage + 1)} disabled={currentPage >= totalPages}
                class="p-1 rounded text-slate-400 hover:text-slate-700 hover:bg-slate-200 disabled:opacity-30 disabled:cursor-not-allowed transition-colors" title="Next page">
                <ChevronRight size={16} />
              </button>
            </div>
          </div>
        {/if}
      </div>

    <!-- ========== PERMISSIONS TAB ========== -->
    {:else}
      <!-- Create Permission Form -->
      {#if showCreatePermForm}
        <div class="bg-white rounded-lg border border-slate-200 p-4 mb-4">
          <h3 class="text-sm font-medium text-slate-700 mb-3">Create Permission</h3>
          <div class="grid grid-cols-2 gap-3 mb-3">
            <div>
              <label for="new-perm-name" class="block text-xs text-slate-500 mb-1">Name</label>
              <input id="new-perm-name" type="text" bind:value={newPermName}
                class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                placeholder="e.g. Editor" />
            </div>
            <div>
              <label for="new-perm-key" class="block text-xs text-slate-500 mb-1">Slug <span class="text-slate-400">(auto-generated if empty)</span></label>
              <input id="new-perm-key" type="text" bind:value={newPermKey}
                class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                placeholder="e.g. editor" />
            </div>
          </div>
          <div class="mb-3">
            <label for="new-perm-desc" class="block text-xs text-slate-500 mb-1">Description</label>
            <input id="new-perm-desc" type="text" bind:value={newPermDesc}
              class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              placeholder="What this permission grants" />
          </div>
          <!-- Capability keys selector -->
          <div class="mb-3">
            <div class="text-xs text-slate-500 mb-2">Capabilities granted:</div>
            <div class="grid grid-cols-2 gap-1.5">
              {#each knownKeys as known (known.key)}
                {@const selected = newPermKeys.includes(known.key)}
                <button
                  onclick={() => toggleNewPermKey(known.key)}
                  type="button"
                  class="flex items-start gap-2 px-2.5 py-2 border rounded-md transition-all text-left text-xs
                    {selected ? 'bg-blue-50 border-blue-300' : 'bg-white border-slate-200 hover:border-slate-300'}"
                >
                  <div class="flex items-center justify-center w-3.5 h-3.5 mt-0.5 shrink-0 rounded border transition-colors
                    {selected ? 'bg-blue-500 border-blue-500' : 'border-slate-300'}">
                    {#if selected}<Check size={9} class="text-white" />{/if}
                  </div>
                  <div class="min-w-0">
                    <code class="font-medium {selected ? 'text-blue-700' : 'text-slate-600'}">{known.key}</code>
                    <div class="text-[10px] text-slate-400 mt-0.5 leading-snug">{known.description}</div>
                  </div>
                </button>
              {/each}
            </div>
          </div>
          <div class="flex items-center gap-2">
            <button onclick={handleCreatePerm} disabled={creatingPerm || !newPermName || newPermKeys.length === 0}
              class="px-4 py-1.5 bg-blue-500 text-white text-sm rounded-md hover:bg-blue-600 disabled:opacity-50 transition-colors">
              {creatingPerm ? 'Creating...' : `Create (${newPermKeys.length} capabilities)`}
            </button>
            <button onclick={() => { showCreatePermForm = false; newPermKeys = []; }}
              class="px-4 py-1.5 bg-white border border-slate-300 text-slate-600 text-sm rounded-md hover:bg-slate-50 transition-colors">
              Cancel
            </button>
          </div>
        </div>
      {/if}

      <!-- Permissions Table -->
      <div class="bg-white rounded-lg border border-slate-200 overflow-hidden">
        <table class="w-full text-sm">
          <thead>
            <tr class="bg-slate-50 border-b border-slate-200">
              <th class="text-left px-4 py-2.5 text-xs font-medium text-slate-500 uppercase tracking-wider">Name</th>
              <th class="text-left px-4 py-2.5 text-xs font-medium text-slate-500 uppercase tracking-wider">Capabilities</th>
              <th class="text-right px-4 py-2.5 text-xs font-medium text-slate-500 uppercase tracking-wider">Actions</th>
            </tr>
          </thead>
          <tbody>
            {#each allPermissions as perm (perm.id)}
              <tr class="border-b border-slate-100 hover:bg-slate-50">
                <td class="px-4 py-3">
                  <div class="font-medium text-slate-800">{perm.name}</div>
                  {#if perm.description}
                    <div class="text-[11px] text-slate-400 mt-0.5">{perm.description}</div>
                  {/if}
                </td>
                <td class="px-4 py-3">
                  <div class="flex flex-wrap gap-1">
                    {#each (perm.keys || []) as k}
                      <code class="text-[10px] font-medium text-slate-600 bg-slate-100 px-1.5 py-0.5 rounded">{k}</code>
                    {:else}
                      <span class="text-xs text-slate-400">none</span>
                    {/each}
                  </div>
                </td>
                <td class="px-4 py-3 text-right">
                  <div class="flex items-center justify-end gap-1">
                    <button onclick={() => startEditPerm(perm)} class="p-1.5 text-slate-400 hover:text-blue-500 hover:bg-blue-50 rounded transition-colors" title="Edit permission">
                      <KeyRound size={14} />
                    </button>
                    {#if confirmDeletePermId === perm.id}
                      <button onclick={() => handleDeletePerm(perm.id)} class="px-2 py-1 text-xs bg-red-500 text-white rounded hover:bg-red-600 transition-colors">Confirm</button>
                      <button onclick={() => { confirmDeletePermId = null; }} class="px-2 py-1 text-xs bg-slate-200 text-slate-600 rounded hover:bg-slate-300 transition-colors">Cancel</button>
                    {:else}
                      <button onclick={() => { confirmDeletePermId = perm.id; }} class="p-1.5 text-slate-400 hover:text-red-500 hover:bg-red-50 rounded transition-colors" title="Delete permission">
                        <Trash2 size={14} />
                      </button>
                    {/if}
                  </div>
                </td>
              </tr>
            {:else}
              <tr>
                <td colspan="3" class="px-4 py-8 text-center text-slate-400 text-sm">
                  No permissions defined. Create one to start restricting access.
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    <!-- ========== EDIT USER MODAL ========== -->
    {#if editingUser}
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div class="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
        onkeydown={(e) => { if (e.key === 'Escape') editingUser = null; }}
        onclick={(e) => { if (e.target === e.currentTarget) editingUser = null; }}>
        <div class="bg-white rounded-lg shadow-xl border border-slate-200 p-6 w-full max-w-lg">
          <h3 class="text-sm font-semibold text-slate-800 mb-4">
            Edit User: {editingUser.username}
            {#if editingUser.is_superadmin}
              <span class="ml-2 text-[10px] px-1.5 py-0.5 bg-amber-100 text-amber-700 rounded font-medium">superadmin</span>
            {/if}
          </h3>

          <!-- Tabs inside modal -->
          <div class="flex gap-1 mb-4 border-b border-slate-200">
            <button onclick={() => { editTab = 'details'; }}
              class="px-3 py-1.5 text-xs font-medium border-b-2 transition-colors -mb-px {editTab === 'details' ? 'border-blue-500 text-blue-600' : 'border-transparent text-slate-400 hover:text-slate-600'}">
              Details
            </button>
            <button onclick={() => { editTab = 'permissions'; }}
              class="px-3 py-1.5 text-xs font-medium border-b-2 transition-colors -mb-px {editTab === 'permissions' ? 'border-blue-500 text-blue-600' : 'border-transparent text-slate-400 hover:text-slate-600'}">
              Permissions
            </button>
          </div>

          {#if editTab === 'details'}
            <div class="space-y-3">
              <div>
                <label for="edit-username" class="block text-xs text-slate-500 mb-1">Username</label>
                <input id="edit-username" type="text" bind:value={editUsername}
                  class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent" />
              </div>
              <div>
                <label for="edit-password" class="block text-xs text-slate-500 mb-1">New Password (leave empty to keep current)</label>
                <input id="edit-password" type="password" bind:value={editPassword}
                  class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                  placeholder="New password" />
              </div>
            </div>
          {:else}
            <!-- Permissions tab -->
            {#if editingUser.is_superadmin}
              <div class="flex items-center gap-2 p-3 bg-amber-50 border border-amber-200 rounded-lg text-xs text-amber-700">
                <ShieldCheck size={16} />
                <span>Superadmin users have all permissions automatically.</span>
              </div>
            {:else if loadingUserPerms}
              <div class="py-6 text-center text-sm text-slate-400">Loading permissions...</div>
            {:else if allPermissions.length === 0}
              <div class="py-6 text-center text-sm text-slate-400">No permissions defined. Create permissions in the Permissions tab first.</div>
            {:else}
              <div class="space-y-1 max-h-64 overflow-y-auto">
                {#each allPermissions as perm (perm.id)}
                  {@const checked = editUserPermissionIds.includes(perm.id)}
                  <label class="flex items-center gap-3 px-3 py-2 rounded-md hover:bg-slate-50 cursor-pointer transition-colors">
                    <input type="checkbox" checked={checked} onchange={() => toggleEditPermission(perm.id)}
                      class="rounded border-slate-300 text-blue-500 focus:ring-blue-500" />
                    <div class="flex-1 min-w-0">
                      <div class="flex items-center gap-2">
                        <Shield size={12} class="text-slate-400 shrink-0" />
                        <code class="text-xs font-medium text-slate-700">{perm.key}</code>
                      </div>
                      <div class="text-[11px] text-slate-400 mt-0.5">{perm.name}{perm.description ? ` — ${perm.description}` : ''}</div>
                    </div>
                  </label>
                {/each}
              </div>
            {/if}
          {/if}

          <div class="flex justify-end gap-2 mt-4">
            <button onclick={() => { editingUser = null; }}
              class="px-4 py-1.5 bg-white border border-slate-300 text-slate-600 text-sm rounded-md hover:bg-slate-50 transition-colors">
              Cancel
            </button>
            <button onclick={handleSaveEdit}
              class="px-4 py-1.5 bg-blue-500 text-white text-sm rounded-md hover:bg-blue-600 transition-colors">
              Save
            </button>
          </div>
        </div>
      </div>
    {/if}

    <!-- ========== EDIT PERMISSION MODAL ========== -->
    {#if editingPerm}
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div class="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
        onkeydown={(e) => { if (e.key === 'Escape') editingPerm = null; }}
        onclick={(e) => { if (e.target === e.currentTarget) editingPerm = null; }}>
        <div class="bg-white rounded-lg shadow-xl border border-slate-200 p-6 w-full max-w-lg">
          <h3 class="text-sm font-semibold text-slate-800 mb-4">Edit Permission: {editingPerm.name}</h3>
          <div class="space-y-3">
            <div class="grid grid-cols-2 gap-3">
              <div>
                <label for="edit-perm-name" class="block text-xs text-slate-500 mb-1">Name</label>
                <input id="edit-perm-name" type="text" bind:value={editPermName}
                  class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent" />
              </div>
              <div>
                <label for="edit-perm-key" class="block text-xs text-slate-500 mb-1">Slug</label>
                <input id="edit-perm-key" type="text" bind:value={editPermKey}
                  class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent" />
              </div>
            </div>
            <div>
              <label for="edit-perm-desc" class="block text-xs text-slate-500 mb-1">Description</label>
              <input id="edit-perm-desc" type="text" bind:value={editPermDesc}
                class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent" />
            </div>
            <!-- Capability keys selector -->
            <div>
              <div class="text-xs text-slate-500 mb-2">Capabilities granted:</div>
              <div class="grid grid-cols-2 gap-1.5 max-h-56 overflow-y-auto">
                {#each knownKeys as known (known.key)}
                  {@const selected = editPermKeys.includes(known.key)}
                  <button
                    onclick={() => toggleEditPermKey(known.key)}
                    type="button"
                    class="flex items-start gap-2 px-2.5 py-2 border rounded-md transition-all text-left text-xs
                      {selected ? 'bg-blue-50 border-blue-300' : 'bg-white border-slate-200 hover:border-slate-300'}"
                  >
                    <div class="flex items-center justify-center w-3.5 h-3.5 mt-0.5 shrink-0 rounded border transition-colors
                      {selected ? 'bg-blue-500 border-blue-500' : 'border-slate-300'}">
                      {#if selected}<Check size={9} class="text-white" />{/if}
                    </div>
                    <div class="min-w-0">
                      <code class="font-medium {selected ? 'text-blue-700' : 'text-slate-600'}">{known.key}</code>
                    </div>
                  </button>
                {/each}
              </div>
            </div>
          </div>
          <div class="flex justify-end gap-2 mt-4">
            <button onclick={() => { editingPerm = null; }}
              class="px-4 py-1.5 bg-white border border-slate-300 text-slate-600 text-sm rounded-md hover:bg-slate-50 transition-colors">Cancel</button>
            <button onclick={handleSaveEditPerm}
              class="px-4 py-1.5 bg-blue-500 text-white text-sm rounded-md hover:bg-blue-600 transition-colors">Save</button>
          </div>
        </div>
      </div>
    {/if}
  </div>
</div>
