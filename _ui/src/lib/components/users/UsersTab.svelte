<script lang="ts">
  import {
    Trash2,
    UserCheck,
    UserX,
    KeyRound,
    LogOut,
    Search,
    ChevronUp,
    ChevronDown,
    ChevronsUpDown,
    ChevronLeft,
    ChevronRight,
    Shield,
    ShieldCheck,
    ShieldOff,
    Globe,
  } from "lucide-svelte";
  import { apiServerMessage } from "@/lib/api/client";
  import { appStore, type UserInfo } from "@/lib/store/store.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import type { UserQueryState } from "./userQuery.svelte";

  type Props = {
    query: UserQueryState;
    showCreateForm: boolean;
    canManagePermissions: boolean;
    onEdit: (user: UserInfo) => void;
    reload: () => void;
  };

  let {
    query,
    showCreateForm = $bindable(),
    canManagePermissions,
    onEdit,
    reload,
  }: Props = $props();

  let newUsername = $state("");
  let newPassword = $state("");
  let creating = $state(false);

  let confirmDeleteId = $state<string | null>(null);
  // confirmResetTOTPId mirrors confirmDeleteId for the "Reset 2FA"
  // affordance. The action is destructive (the user's enrolled
  // authenticator is wiped and they have to re-enroll), so we use the
  // same inline-confirm pattern as Delete rather than a modal dialog.
  let confirmResetTOTPId = $state<string | null>(null);

  let searchTimeout: ReturnType<typeof setTimeout> | null = null;

  const users = $derived(appStore.users);
  const total = $derived(appStore.usersTotal);
  const currentUser = $derived(appStore.info?.user);
  const allPermissions = $derived(appStore.permissions);
  const totalPages = $derived(Math.max(1, Math.ceil(total / query.pageSize)));
  const showingFrom = $derived(
    total === 0 ? 0 : (query.currentPage - 1) * query.pageSize + 1,
  );
  const showingTo = $derived(Math.min(query.currentPage * query.pageSize, total));

  // Reset to page 1 whenever the permission filter changes — keeps the
  // pagination state consistent with the new (typically smaller) result set.
  function handlePermissionFilterChange(value: string) {
    query.filterPermissionId = value;
    query.currentPage = 1;
    reload();
  }

  function handleSearch(value: string) {
    query.searchText = value;
    if (searchTimeout) clearTimeout(searchTimeout);
    searchTimeout = setTimeout(() => {
      query.currentPage = 1;
      reload();
    }, 300);
  }

  function handleSort(field: string) {
    if (query.sortField === field) {
      query.sortDir = query.sortDir === "asc" ? "desc" : "asc";
    } else {
      query.sortField = field;
      query.sortDir = "asc";
    }
    query.currentPage = 1;
    reload();
  }

  function goToPage(page: number) {
    if (page < 1 || page > totalPages) return;
    query.currentPage = page;
    reload();
  }

  function handlePageSizeChange(size: number) {
    query.pageSize = size;
    query.currentPage = 1;
    reload();
  }

  async function handleCreate() {
    if (!newUsername || !newPassword) return;
    creating = true;
    try {
      await appStore.createUser(newUsername, newPassword);
      addToast(`User "${newUsername}" created`, "success");
      newUsername = "";
      newPassword = "";
      showCreateForm = false;
    } catch (err) {
      addToast(apiServerMessage(err, "Failed to create user"), "alert");
    } finally {
      creating = false;
    }
  }

  async function handleToggleDisabled(user: UserInfo) {
    try {
      await appStore.updateUser(user.id, { disabled: !user.disabled });
      addToast(
        `User "${user.username}" ${user.disabled ? "enabled" : "disabled"}`,
        "success",
      );
    } catch (err) {
      addToast(apiServerMessage(err, "Failed to update user"), "alert");
    }
  }

  async function handleDelete(id: string) {
    try {
      await appStore.deleteUser(id);
      addToast("User deleted", "success");
      confirmDeleteId = null;
    } catch (err) {
      addToast(apiServerMessage(err, "Failed to delete user"), "alert");
    }
  }

  async function handleKick(user: UserInfo) {
    try {
      await appStore.kickUser(user.id);
      addToast(`All sessions for "${user.username}" terminated`, "success");
    } catch (err) {
      addToast(apiServerMessage(err, "Failed to kick user"), "alert");
    }
  }

  // handleResetTOTP wipes the target user's TOTP enrollment from
  // the admin side. Used when a user has lost both their authenticator
  // device and their recovery codes — without this they would be
  // permanently locked out of any account whose login goes through
  // the MFA wrapper.
  async function handleResetTOTP(user: UserInfo) {
    try {
      await appStore.resetUserTOTP(user.id);
      addToast(
        `2FA reset for "${user.username}" — they can sign in with password and re-enroll`,
        "success",
      );
      confirmResetTOTPId = null;
    } catch (err) {
      addToast(apiServerMessage(err, "Failed to reset 2FA"), "alert");
    }
  }

  const inputClass =
    "w-full px-3 py-1.5 border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-accent-500 focus:border-transparent";
  const iconBtn =
    "p-1.5 text-slate-400 dark:text-slate-500 rounded transition-colors cursor-pointer";
  const smallCancel =
    "px-2 py-1 text-xs bg-slate-200 dark:bg-warm-700 text-slate-600 dark:text-warm-200 rounded hover:bg-slate-300 dark:hover:bg-warm-600 transition-colors cursor-pointer";
</script>

<!-- Create User Form -->
{#if showCreateForm}
  <div
    class="bg-white dark:bg-warm-800 rounded-lg border border-slate-200 dark:border-warm-700 p-4 mb-4"
  >
    <h3 class="text-sm font-medium text-slate-700 dark:text-slate-200 mb-3">
      Create New User
    </h3>
    <div class="flex gap-3 items-end">
      <div class="flex-1">
        <label
          for="new-username"
          class="block text-xs text-slate-500 dark:text-slate-400 mb-1"
          >Username</label
        >
        <input
          id="new-username"
          type="text"
          bind:value={newUsername}
          class={inputClass}
          placeholder="username"
        />
      </div>
      <div class="flex-1">
        <label
          for="new-password"
          class="block text-xs text-slate-500 dark:text-slate-400 mb-1"
          >Password</label
        >
        <input
          id="new-password"
          type="password"
          bind:value={newPassword}
          class={inputClass}
          placeholder="password"
        />
      </div>
      <button
        onclick={handleCreate}
        disabled={creating || !newUsername || !newPassword}
        class="px-4 py-1.5 bg-accent-600 text-white text-sm rounded-md hover:bg-accent-700 disabled:opacity-50 transition-colors cursor-pointer disabled:cursor-not-allowed"
      >
        {creating ? "Creating..." : "Create"}
      </button>
      <button
        onclick={() => {
          showCreateForm = false;
        }}
        class="px-4 py-1.5 bg-white dark:bg-warm-800 border border-slate-300 dark:border-warm-600 text-slate-600 dark:text-slate-300 text-sm rounded-md hover:bg-slate-50 dark:hover:bg-warm-700 transition-colors cursor-pointer"
      >
        Cancel
      </button>
    </div>
  </div>
{/if}

<!-- Search + Permission filter -->
<div class="flex items-center gap-2 mb-3">
  <div class="relative flex-1">
    <Search
      size={14}
      class="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400 dark:text-slate-500"
    />
    <input
      type="text"
      value={query.searchText}
      oninput={(e) => handleSearch(e.currentTarget.value)}
      aria-label="Search users"
      class="w-full pl-9 pr-3 py-2 border border-slate-200 dark:border-warm-700 rounded-lg text-sm bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500 focus:border-transparent placeholder-slate-400 dark:placeholder-slate-500"
      placeholder="Search users..."
    />
  </div>
  {#if canManagePermissions && allPermissions.length > 0}
    <!-- Filter by permission bundle. Empty value = no filter (all users). -->
    <div class="relative">
      <Shield
        size={14}
        class="absolute left-3 top-1/2 -translate-y-1/2 text-slate-400 dark:text-slate-500 pointer-events-none"
      />
      <select
        value={query.filterPermissionId}
        onchange={(e) => handlePermissionFilterChange(e.currentTarget.value)}
        aria-label="Filter users by permission"
        class="appearance-none pl-9 pr-8 py-2 border border-slate-200 dark:border-warm-700 rounded-lg text-sm bg-white dark:bg-warm-800 focus:outline-none focus:ring-2 focus:ring-accent-500 focus:border-transparent text-slate-700 dark:text-slate-200 cursor-pointer min-w-[180px]"
        title="Filter users by permission"
      >
        <option value="">All permissions</option>
        {#each allPermissions as p (p.id)}
          <option value={p.id}>{p.name}</option>
        {/each}
      </select>
      <ChevronDown
        size={14}
        class="absolute right-2 top-1/2 -translate-y-1/2 text-slate-400 dark:text-slate-500 pointer-events-none"
      />
    </div>
  {/if}
</div>

<!-- Users Table -->
<div
  class="bg-white dark:bg-warm-800 rounded-lg border border-slate-200 dark:border-warm-700 overflow-hidden"
>
  <table class="w-full text-sm">
    <thead>
      <tr
        class="bg-slate-50 dark:bg-warm-900 border-b border-slate-200 dark:border-warm-700"
      >
        <th class="text-left px-4 py-2.5">
          <button
            onclick={() => handleSort("username")}
            class="flex items-center gap-1 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider hover:text-slate-800 dark:hover:text-slate-100 transition-colors cursor-pointer"
          >
            Username
            {#if query.sortField === "username"}
              {#if query.sortDir === "asc"}<ChevronUp
                  size={12}
                />{:else}<ChevronDown size={12} />{/if}
            {:else}
              <ChevronsUpDown size={12} class="text-slate-300 dark:text-slate-600" />
            {/if}
          </button>
        </th>
        <th
          class="text-left px-4 py-2.5 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider"
          >Status</th
        >
        <th
          class="text-left px-4 py-2.5 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider"
          >Sessions</th
        >
        <th class="text-left px-4 py-2.5">
          <button
            onclick={() => handleSort("created_at")}
            class="flex items-center gap-1 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider hover:text-slate-800 dark:hover:text-slate-100 transition-colors cursor-pointer"
          >
            Created
            {#if query.sortField === "created_at"}
              {#if query.sortDir === "asc"}<ChevronUp
                  size={12}
                />{:else}<ChevronDown size={12} />{/if}
            {:else}
              <ChevronsUpDown size={12} class="text-slate-300 dark:text-slate-600" />
            {/if}
          </button>
        </th>
        <th
          class="text-right px-4 py-2.5 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider"
          >Actions</th
        >
      </tr>
    </thead>
    <tbody>
      {#each users as user (user.id)}
        {@const isYou = user.username === currentUser}
        {@const isOnline = user.active_sessions > 0}
        <tr
          class="border-b border-slate-100 dark:border-warm-700 {isYou
            ? 'bg-accent-50/60 border-l-2 border-l-accent-500 dark:bg-accent-900/40'
            : 'hover:bg-slate-50 dark:hover:bg-warm-700'}"
        >
          <td class="px-4 py-3">
            <div class="flex items-center gap-2">
              <span class="relative flex h-2 w-2 shrink-0">
                {#if isOnline && !user.disabled}
                  <span
                    class="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75"
                  ></span>
                  <span
                    class="relative inline-flex rounded-full h-2 w-2 bg-emerald-500"
                  ></span>
                {:else}
                  <span
                    class="relative inline-flex rounded-full h-2 w-2 bg-slate-300 dark:bg-warm-600"
                  ></span>
                {/if}
              </span>
              <span
                class="font-medium {isYou
                  ? 'text-accent-800 dark:text-accent-200'
                  : 'text-slate-800 dark:text-slate-100'}">{user.username}</span
              >
              {#if isYou}
                <span
                  class="text-[10px] px-1.5 py-0.5 bg-accent-100 text-accent-700 dark:bg-accent-900/40 dark:text-accent-200 rounded font-medium"
                  >you</span
                >
              {/if}
              {#if user.is_superadmin}
                <span
                  class="text-[10px] px-1.5 py-0.5 bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-200 rounded font-medium"
                  >superadmin</span
                >
              {/if}
              {#if user.external}
                <span
                  class="inline-flex items-center gap-0.5 text-[10px] px-1.5 py-0.5 bg-sky-50 dark:bg-sky-900/40 text-sky-700 dark:text-sky-300 rounded font-medium"
                  title="External user (authenticated via an identity provider)"
                >
                  <Globe size={10} /> external
                </span>
              {/if}
              {#if user.has_totp}
                <span
                  class="inline-flex items-center gap-0.5 text-[10px] px-1.5 py-0.5 bg-emerald-50 dark:bg-emerald-900/40 text-emerald-700 dark:text-emerald-300 rounded font-medium"
                  title="User has TOTP / 2FA enabled"
                >
                  <ShieldCheck size={10} /> 2FA
                </span>
              {/if}
            </div>
          </td>
          <td class="px-4 py-3">
            {#if user.disabled}
              <span
                class="inline-flex items-center gap-1 text-xs px-2 py-0.5 bg-vermilion-50 dark:bg-vermilion-900/40 text-vermilion-600 dark:text-vermilion-300 rounded-full"
              >
                <UserX size={12} /> Disabled
              </span>
            {:else}
              <span
                class="inline-flex items-center gap-1 text-xs px-2 py-0.5 bg-emerald-50 dark:bg-emerald-900/40 text-emerald-600 dark:text-emerald-300 rounded-full"
              >
                <UserCheck size={12} /> Active
              </span>
            {/if}
          </td>
          <td class="px-4 py-3">
            {#if isOnline}
              <span
                class="inline-flex items-center gap-1 text-xs px-2 py-0.5 bg-emerald-50 dark:bg-emerald-900/40 text-emerald-700 dark:text-emerald-300 rounded-full tabular-nums"
                >{user.active_sessions}</span
              >
            {:else}
              <span class="text-xs text-slate-400 dark:text-slate-500">0</span>
            {/if}
          </td>
          <td class="px-4 py-3 text-slate-500 dark:text-slate-400 text-xs"
            >{new Date(user.created_at).toLocaleDateString()}</td
          >
          <td class="px-4 py-3 text-right">
            <div class="flex items-center justify-end gap-1">
              <button
                onclick={() => onEdit(user)}
                class="{iconBtn} hover:text-accent-600 hover:bg-accent-50 dark:hover:text-accent-400 dark:hover:bg-warm-600"
                title="Edit user"
                aria-label="Edit user {user.username}"
              >
                <KeyRound size={14} />
              </button>
              {#if !isYou && isOnline}
                <button
                  onclick={() => handleKick(user)}
                  class="{iconBtn} hover:text-amber-600 hover:bg-amber-50 dark:hover:text-amber-400 dark:hover:bg-warm-600"
                  title="Kick user"
                  aria-label="Kick user {user.username}"
                >
                  <LogOut size={14} />
                </button>
              {/if}
              <button
                onclick={() => handleToggleDisabled(user)}
                class="{iconBtn} hover:text-amber-600 hover:bg-amber-50 dark:hover:text-amber-400 dark:hover:bg-warm-600 disabled:opacity-40 disabled:cursor-not-allowed"
                title={user.disabled ? "Enable user" : "Disable user"}
                aria-label={user.disabled
                  ? `Enable user ${user.username}`
                  : `Disable user ${user.username}`}
                disabled={isYou}
              >
                {#if user.disabled}<UserCheck size={14} />{:else}<UserX
                    size={14}
                  />{/if}
              </button>
              {#if user.has_totp && !isYou}
                {#if confirmResetTOTPId === user.id}
                  <button
                    onclick={() => handleResetTOTP(user)}
                    class="px-2 py-1 text-xs bg-vermilion-600 text-white rounded hover:bg-vermilion-700 transition-colors cursor-pointer"
                    title="Confirm reset — user must re-enroll their authenticator"
                    >Reset 2FA</button
                  >
                  <button
                    onclick={() => {
                      confirmResetTOTPId = null;
                    }}
                    class={smallCancel}>Cancel</button
                  >
                {:else}
                  <button
                    onclick={() => {
                      confirmResetTOTPId = user.id;
                      confirmDeleteId = null;
                    }}
                    class="{iconBtn} hover:text-vermilion-600 hover:bg-vermilion-50 dark:hover:text-vermilion-400 dark:hover:bg-warm-600"
                    title="Reset 2FA (user lost their authenticator)"
                    aria-label="Reset 2FA for {user.username}"
                  >
                    <ShieldOff size={14} />
                  </button>
                {/if}
              {/if}
              {#if !isYou}
                {#if confirmDeleteId === user.id}
                  <button
                    onclick={() => handleDelete(user.id)}
                    class="px-2 py-1 text-xs bg-vermilion-600 text-white rounded hover:bg-vermilion-700 transition-colors cursor-pointer"
                    >Confirm</button
                  >
                  <button
                    onclick={() => {
                      confirmDeleteId = null;
                    }}
                    class={smallCancel}>Cancel</button
                  >
                {:else}
                  <button
                    onclick={() => {
                      confirmDeleteId = user.id;
                      confirmResetTOTPId = null;
                    }}
                    class="{iconBtn} hover:text-vermilion-600 hover:bg-vermilion-50 dark:hover:text-vermilion-400 dark:hover:bg-warm-600"
                    title="Delete user"
                    aria-label="Delete user {user.username}"
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
          <td
            colspan="5"
            class="px-4 py-8 text-center text-slate-400 dark:text-slate-500 text-sm"
          >
            {query.searchText || query.filterPermissionId
              ? "No users matching your filters"
              : "No users found"}
          </td>
        </tr>
      {/each}
    </tbody>
  </table>

  <!-- Pagination -->
  {#if total > 0}
    <div
      class="flex items-center justify-between px-4 py-3 border-t border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900"
    >
      <div class="flex items-center gap-2 text-xs text-slate-500 dark:text-slate-400">
        <span>Showing {showingFrom}-{showingTo} of {total}</span>
        <span class="text-slate-300 dark:text-warm-600">|</span>
        <label for="page-size" class="sr-only">Rows per page</label>
        <select
          id="page-size"
          value={query.pageSize}
          onchange={(e) => handlePageSizeChange(Number(e.currentTarget.value))}
          class="px-1.5 py-0.5 border border-slate-200 dark:border-warm-600 rounded text-xs bg-white dark:bg-warm-800 text-slate-700 dark:text-slate-200 focus:outline-none focus:ring-1 focus:ring-accent-500"
        >
          <option value={10}>10 / page</option>
          <option value={20}>20 / page</option>
          <option value={50}>50 / page</option>
          <option value={100}>100 / page</option>
        </select>
      </div>
      <div class="flex items-center gap-1">
        <button
          onclick={() => goToPage(query.currentPage - 1)}
          disabled={query.currentPage <= 1}
          class="p-1 rounded text-slate-400 dark:text-slate-500 hover:text-slate-700 dark:hover:text-slate-200 hover:bg-slate-200 dark:hover:bg-warm-700 disabled:opacity-30 disabled:cursor-not-allowed transition-colors cursor-pointer"
          title="Previous page"
          aria-label="Previous page"
        >
          <ChevronLeft size={16} />
        </button>
        <span class="text-xs text-slate-600 dark:text-slate-300 px-2 tabular-nums"
          >{query.currentPage} / {totalPages}</span
        >
        <button
          onclick={() => goToPage(query.currentPage + 1)}
          disabled={query.currentPage >= totalPages}
          class="p-1 rounded text-slate-400 dark:text-slate-500 hover:text-slate-700 dark:hover:text-slate-200 hover:bg-slate-200 dark:hover:bg-warm-700 disabled:opacity-30 disabled:cursor-not-allowed transition-colors cursor-pointer"
          title="Next page"
          aria-label="Next page"
        >
          <ChevronRight size={16} />
        </button>
      </div>
    </div>
  {/if}
</div>
