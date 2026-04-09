<script lang="ts">
  import { appStore, type UserInfo, type UserQuery } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { onMount } from 'svelte';
  import { Plus, Trash2, UserCheck, UserX, KeyRound, LogOut, Search, ChevronUp, ChevronDown, ChevronsUpDown, ChevronLeft, ChevronRight } from 'lucide-svelte';

  let showCreateForm = $state(false);
  let newUsername = $state('');
  let newPassword = $state('');
  let creating = $state(false);

  let editingUser = $state<UserInfo | null>(null);
  let editPassword = $state('');
  let editUsername = $state('');

  let confirmDeleteId = $state<string | null>(null);

  // Query state
  let searchText = $state('');
  let sortField = $state('username');
  let sortDir = $state<'asc' | 'desc'>('asc');
  let pageSize = $state(20);
  let currentPage = $state(1);
  let searchTimeout = $state<ReturnType<typeof setTimeout> | null>(null);

  const users = $derived(appStore.users);
  const total = $derived(appStore.usersTotal);
  const currentUser = $derived(appStore.info?.user);
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

  function startEdit(user: UserInfo) {
    editingUser = user;
    editUsername = user.username;
    editPassword = '';
  }

  async function handleSaveEdit() {
    if (!editingUser) return;

    const updates: { username?: string; password?: string } = {};
    if (editUsername && editUsername !== editingUser.username) {
      updates.username = editUsername;
    }
    if (editPassword) {
      updates.password = editPassword;
    }

    if (Object.keys(updates).length === 0) {
      editingUser = null;
      return;
    }

    try {
      await appStore.updateUser(editingUser.id, updates);
      addToast('User updated', 'success');
      editingUser = null;
    } catch (err: any) {
      addToast(err?.response?.data?.message || 'Failed to update user', 'alert');
    }
  }
</script>

<div class="h-full overflow-auto p-6">
  <div class="max-w-3xl mx-auto">
    <div class="flex items-center justify-between mb-4">
      <h1 class="text-lg font-semibold text-slate-800">User Management</h1>
      <button
        onclick={() => { showCreateForm = !showCreateForm; }}
        class="flex items-center gap-1.5 px-3 py-1.5 bg-blue-500 text-white text-xs font-medium rounded-md hover:bg-blue-600 transition-colors"
      >
        <Plus size={14} />
        New User
      </button>
    </div>

    <!-- Create User Form -->
    {#if showCreateForm}
      <div class="bg-white rounded-lg border border-slate-200 p-4 mb-4">
        <h3 class="text-sm font-medium text-slate-700 mb-3">Create New User</h3>
        <div class="flex gap-3 items-end">
          <div class="flex-1">
            <label for="new-username" class="block text-xs text-slate-500 mb-1">Username</label>
            <input
              id="new-username"
              type="text"
              bind:value={newUsername}
              class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              placeholder="username"
            />
          </div>
          <div class="flex-1">
            <label for="new-password" class="block text-xs text-slate-500 mb-1">Password</label>
            <input
              id="new-password"
              type="password"
              bind:value={newPassword}
              class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              placeholder="password"
            />
          </div>
          <button
            onclick={handleCreate}
            disabled={creating || !newUsername || !newPassword}
            class="px-4 py-1.5 bg-blue-500 text-white text-sm rounded-md hover:bg-blue-600 disabled:opacity-50 transition-colors"
          >
            {creating ? 'Creating...' : 'Create'}
          </button>
          <button
            onclick={() => { showCreateForm = false; }}
            class="px-4 py-1.5 bg-white border border-slate-300 text-slate-600 text-sm rounded-md hover:bg-slate-50 transition-colors"
          >
            Cancel
          </button>
        </div>
      </div>
    {/if}

    <!-- Search Bar -->
    <div class="relative mb-3">
      <Search size={14} class="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400" />
      <input
        type="text"
        value={searchText}
        oninput={(e) => handleSearch(e.currentTarget.value)}
        class="w-full pl-9 pr-3 py-2 border border-slate-200 rounded-lg text-sm bg-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent placeholder-slate-400"
        placeholder="Search users..."
      />
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
                </div>
              </td>
              <td class="px-4 py-3">
                {#if user.disabled}
                  <span class="inline-flex items-center gap-1 text-xs px-2 py-0.5 bg-red-50 text-red-600 rounded-full">
                    <UserX size={12} />
                    Disabled
                  </span>
                {:else}
                  <span class="inline-flex items-center gap-1 text-xs px-2 py-0.5 bg-green-50 text-green-600 rounded-full">
                    <UserCheck size={12} />
                    Active
                  </span>
                {/if}
              </td>
              <td class="px-4 py-3">
                {#if isOnline}
                  <span class="inline-flex items-center gap-1 text-xs px-2 py-0.5 bg-green-50 text-green-700 rounded-full tabular-nums">
                    {user.active_sessions}
                  </span>
                {:else}
                  <span class="text-xs text-slate-400">0</span>
                {/if}
              </td>
              <td class="px-4 py-3 text-slate-500 text-xs">
                {new Date(user.created_at).toLocaleDateString()}
              </td>
              <td class="px-4 py-3 text-right">
                <div class="flex items-center justify-end gap-1">
                  <button
                    onclick={() => startEdit(user)}
                    class="p-1.5 text-slate-400 hover:text-blue-500 hover:bg-blue-50 rounded transition-colors"
                    title="Change password"
                  >
                    <KeyRound size={14} />
                  </button>
                  {#if !isYou && isOnline}
                    <button
                      onclick={() => handleKick(user)}
                      class="p-1.5 text-slate-400 hover:text-orange-500 hover:bg-orange-50 rounded transition-colors"
                      title="Kick user (terminate all sessions)"
                    >
                      <LogOut size={14} />
                    </button>
                  {/if}
                  <button
                    onclick={() => handleToggleDisabled(user)}
                    class="p-1.5 text-slate-400 hover:text-amber-500 hover:bg-amber-50 rounded transition-colors"
                    title={user.disabled ? 'Enable user' : 'Disable user'}
                    disabled={isYou}
                  >
                    {#if user.disabled}
                      <UserCheck size={14} />
                    {:else}
                      <UserX size={14} />
                    {/if}
                  </button>
                  {#if !isYou}
                    {#if confirmDeleteId === user.id}
                      <button
                        onclick={() => handleDelete(user.id)}
                        class="px-2 py-1 text-xs bg-red-500 text-white rounded hover:bg-red-600 transition-colors"
                      >
                        Confirm
                      </button>
                      <button
                        onclick={() => { confirmDeleteId = null; }}
                        class="px-2 py-1 text-xs bg-slate-200 text-slate-600 rounded hover:bg-slate-300 transition-colors"
                      >
                        Cancel
                      </button>
                    {:else}
                      <button
                        onclick={() => { confirmDeleteId = user.id; }}
                        class="p-1.5 text-slate-400 hover:text-red-500 hover:bg-red-50 rounded transition-colors"
                        title="Delete user"
                      >
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
            <select
              id="page-size"
              value={pageSize}
              onchange={(e) => handlePageSizeChange(Number(e.currentTarget.value))}
              class="px-1.5 py-0.5 border border-slate-200 rounded text-xs bg-white focus:outline-none focus:ring-1 focus:ring-blue-500"
            >
              <option value={10}>10 / page</option>
              <option value={20}>20 / page</option>
              <option value={50}>50 / page</option>
              <option value={100}>100 / page</option>
            </select>
          </div>
          <div class="flex items-center gap-1">
            <button
              onclick={() => goToPage(currentPage - 1)}
              disabled={currentPage <= 1}
              class="p-1 rounded text-slate-400 hover:text-slate-700 hover:bg-slate-200 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
              title="Previous page"
            >
              <ChevronLeft size={16} />
            </button>
            <span class="text-xs text-slate-600 px-2 tabular-nums">
              {currentPage} / {totalPages}
            </span>
            <button
              onclick={() => goToPage(currentPage + 1)}
              disabled={currentPage >= totalPages}
              class="p-1 rounded text-slate-400 hover:text-slate-700 hover:bg-slate-200 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
              title="Next page"
            >
              <ChevronRight size={16} />
            </button>
          </div>
        </div>
      {/if}
    </div>

    <!-- Edit User Modal -->
    {#if editingUser}
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div
        class="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
        onkeydown={(e) => { if (e.key === 'Escape') editingUser = null; }}
        onclick={(e) => { if (e.target === e.currentTarget) editingUser = null; }}
      >
        <div class="bg-white rounded-lg shadow-xl border border-slate-200 p-6 w-full max-w-md">
          <h3 class="text-sm font-semibold text-slate-800 mb-4">Edit User: {editingUser.username}</h3>

          <div class="space-y-3">
            <div>
              <label for="edit-username" class="block text-xs text-slate-500 mb-1">Username</label>
              <input
                id="edit-username"
                type="text"
                bind:value={editUsername}
                class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              />
            </div>
            <div>
              <label for="edit-password" class="block text-xs text-slate-500 mb-1">New Password (leave empty to keep current)</label>
              <input
                id="edit-password"
                type="password"
                bind:value={editPassword}
                class="w-full px-3 py-1.5 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                placeholder="New password"
              />
            </div>
          </div>

          <div class="flex justify-end gap-2 mt-4">
            <button
              onclick={() => { editingUser = null; }}
              class="px-4 py-1.5 bg-white border border-slate-300 text-slate-600 text-sm rounded-md hover:bg-slate-50 transition-colors"
            >
              Cancel
            </button>
            <button
              onclick={handleSaveEdit}
              class="px-4 py-1.5 bg-blue-500 text-white text-sm rounded-md hover:bg-blue-600 transition-colors"
            >
              Save
            </button>
          </div>
        </div>
      </div>
    {/if}
  </div>
</div>
