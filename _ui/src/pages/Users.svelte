<script lang="ts">
  import { appStore, type UserInfo } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { onMount } from 'svelte';
  import { Plus, Trash2, UserCheck, UserX, KeyRound } from 'lucide-svelte';

  let showCreateForm = $state(false);
  let newUsername = $state('');
  let newPassword = $state('');
  let creating = $state(false);

  let editingUser = $state<UserInfo | null>(null);
  let editPassword = $state('');
  let editUsername = $state('');

  let confirmDeleteId = $state<string | null>(null);

  const users = $derived(appStore.users);
  const currentUser = $derived(appStore.info?.user);

  onMount(() => {
    appStore.loadUsers();
  });

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
    <div class="flex items-center justify-between mb-6">
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

    <!-- Users Table -->
    <div class="bg-white rounded-lg border border-slate-200 overflow-hidden">
      <table class="w-full text-sm">
        <thead>
          <tr class="bg-slate-50 border-b border-slate-200">
            <th class="text-left px-4 py-2.5 text-xs font-medium text-slate-500 uppercase tracking-wider">Username</th>
            <th class="text-left px-4 py-2.5 text-xs font-medium text-slate-500 uppercase tracking-wider">Status</th>
            <th class="text-left px-4 py-2.5 text-xs font-medium text-slate-500 uppercase tracking-wider">Created</th>
            <th class="text-right px-4 py-2.5 text-xs font-medium text-slate-500 uppercase tracking-wider">Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each users as user (user.id)}
            <tr class="border-b border-slate-100 hover:bg-slate-50">
              <td class="px-4 py-3">
                <span class="font-medium text-slate-800">{user.username}</span>
                {#if user.username === currentUser}
                  <span class="ml-2 text-[10px] px-1.5 py-0.5 bg-blue-100 text-blue-700 rounded">you</span>
                {/if}
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
                  <button
                    onclick={() => handleToggleDisabled(user)}
                    class="p-1.5 text-slate-400 hover:text-amber-500 hover:bg-amber-50 rounded transition-colors"
                    title={user.disabled ? 'Enable user' : 'Disable user'}
                    disabled={user.username === currentUser}
                  >
                    {#if user.disabled}
                      <UserCheck size={14} />
                    {:else}
                      <UserX size={14} />
                    {/if}
                  </button>
                  {#if user.username !== currentUser}
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
              <td colspan="4" class="px-4 py-8 text-center text-slate-400 text-sm">
                No users found
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
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
