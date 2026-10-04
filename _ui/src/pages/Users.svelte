<script lang="ts">
  import {
    appStore,
    type UserInfo,
    type PermissionInfo,
  } from "@/lib/store/store.svelte";
  import { Plus, Shield } from "lucide-svelte";
  import UsersTab from "@/lib/components/users/UsersTab.svelte";
  import PermissionsTab from "@/lib/components/users/PermissionsTab.svelte";
  import EditUserModal from "@/lib/components/users/EditUserModal.svelte";
  import EditPermissionModal from "@/lib/components/users/EditPermissionModal.svelte";
  import {
    UserQueryState,
    type KnownCapability,
  } from "@/lib/components/users/userQuery.svelte";

  // Tab state.
  //
  // The initial value used to be computed eagerly from `appStore.hasPermission`
  // at script-init time, but on a fresh page load `appStore.info` is still
  // null at that moment — every `hasPermission` call returns false, so the
  // initial value reliably became 'permissions' (the else branch). Then the
  // info response arrived asynchronously and the reactive effect below tried
  // to fix things up, producing a race: depending on the request timing the
  // user saw either the empty-permissions tab or the users tab.
  //
  // Fix: start at `null` until info actually loads, then snap to the first
  // tab the user is allowed to see. Render gates below skip the tab buttons
  // and content while activeTab is null, so there's no flash of the wrong
  // tab before info resolves.
  const canManageUsers = $derived(appStore.hasPermission("users.manage"));
  const canManagePermissions = $derived(
    appStore.hasPermission("permissions.manage"),
  );
  const infoLoaded = $derived(appStore.info !== null);
  let activeTab = $state<"users" | "permissions" | null>(null);

  $effect(() => {
    if (!infoLoaded) return;
    // Initial snap once info has loaded.
    if (activeTab === null) {
      if (canManageUsers) {
        activeTab = "users";
      } else if (canManagePermissions) {
        activeTab = "permissions";
      }
      return;
    }
    // If the active tab becomes inaccessible (e.g. permissions reload),
    // flip to the allowed one.
    if (activeTab === "users" && !canManageUsers && canManagePermissions) {
      activeTab = "permissions";
    } else if (
      activeTab === "permissions" &&
      !canManagePermissions &&
      canManageUsers
    ) {
      activeTab = "users";
    }
  });

  const query = new UserQueryState();
  let showCreateForm = $state(false);
  let showCreatePermForm = $state(false);
  let editingUser = $state<UserInfo | null>(null);
  let editingPerm = $state<PermissionInfo | null>(null);

  // Canonical list of capability keys — sourced from the server via
  // /api/v1/info so this stays in sync with the Go constants in
  // internal/service/capabilities.go automatically.
  const knownKeys = $derived<KnownCapability[]>(
    appStore.info?.capabilities ?? [],
  );

  // Cross-tab shortcut from the Permissions table: jump to the Users tab
  // with the permission filter pre-applied, so the admin sees exactly who
  // holds the bundle they just inspected.
  function viewUsersWithPermission(permissionId: string) {
    query.filterPermissionId = permissionId;
    query.searchText = "";
    query.currentPage = 1;
    activeTab = "users";
    reload();
  }

  function reload() {
    appStore.loadUsers(query.build());
  }

  // Data fetch is keyed off `infoLoaded` AND the per-tab capability
  // becoming true. The previous version used a single `dataLoaded`
  // flag that was flipped BEFORE the capability check, which caused a
  // silent race on first navigation after login:
  //
  //   1. info loads (unauthenticated GET at boot returns empty caps),
  //      then identity resolves moments later.
  //   2. Effect fires the first time with `infoLoaded=true` but
  //      `canManageUsers=false` (because identity hasn't propagated
  //      through hasPermission yet, or the post-login info hasn't
  //      replaced the boot-time empty one).
  //   3. The old code set `dataLoaded=true` unconditionally and
  //      skipped reload(). When canManageUsers later flipped true the
  //      effect re-ran but the early-return swallowed it.
  //   4. User saw an empty Users tab until they navigated away and
  //      back, which remounted the component with fresh local state.
  //
  // Per-resource flags fix this: each flag is only set after the
  // matching capability check passes AND the load fires, so a "not
  // yet permitted" effect run leaves the flag false and a later
  // capability flip triggers the load on re-run. Flicker is still
  // suppressed because each resource only loads on the FIRST run
  // where its capability is true.
  let usersLoaded = $state(false);
  let permissionsLoaded = $state(false);
  $effect(() => {
    if (!infoLoaded) return;
    if (canManageUsers && !usersLoaded) {
      usersLoaded = true;
      reload();
    }
    if (canManagePermissions && !permissionsLoaded) {
      permissionsLoaded = true;
      appStore.loadPermissions();
    }
  });
</script>

<div class="h-full overflow-auto p-6">
  <div class="max-w-4xl mx-auto">
    {#if !infoLoaded || activeTab === null}
      <!-- Wait for /api/v1/info before deciding what to render. Without
       this gate the page briefly shows the "No access" stub or the
       wrong tab while capability info is still loading, which looks
       like an inconsistent page on every reload. -->
      <div class="py-12 text-center text-sm text-slate-400 dark:text-warm-400">
        Loading…
      </div>
    {:else if !canManageUsers && !canManagePermissions}
      <!-- No access to either tab — show a stub instead of empty UI. -->
      <div
        class="bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg p-8 text-center"
      >
        <Shield class="mx-auto text-slate-400 dark:text-slate-500" size={32} />
        <p class="mt-3 text-sm font-medium text-slate-700 dark:text-slate-200">
          No access
        </p>
        <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">
          You do not have permission to manage users or permissions.
        </p>
      </div>
    {:else}
      <!-- Header with tabs -->
      <div
        class="flex items-end justify-between mb-4 border-b border-slate-200 dark:border-warm-700"
      >
        <div class="flex items-center gap-1">
          {#if canManageUsers}
            <button
              onclick={() => {
                activeTab = "users";
              }}
              class="px-4 py-2 text-sm font-medium transition-colors cursor-pointer border-b-2 -mb-px {activeTab ===
              'users'
                ? 'border-accent-500 text-accent-700 dark:text-accent-300'
                : 'border-transparent text-slate-500 dark:text-warm-300 hover:text-slate-800 dark:hover:text-white hover:border-slate-300 dark:hover:border-warm-600'}"
            >
              Users
            </button>
          {/if}
          {#if canManagePermissions}
            <button
              onclick={() => {
                activeTab = "permissions";
              }}
              class="px-4 py-2 text-sm font-medium transition-colors cursor-pointer border-b-2 -mb-px {activeTab ===
              'permissions'
                ? 'border-accent-500 text-accent-700 dark:text-accent-300'
                : 'border-transparent text-slate-500 dark:text-warm-300 hover:text-slate-800 dark:hover:text-white hover:border-slate-300 dark:hover:border-warm-600'}"
            >
              Permissions
            </button>
          {/if}
        </div>
        {#if activeTab === "users"}
          <button
            onclick={() => {
              showCreateForm = !showCreateForm;
            }}
            class="flex items-center gap-1.5 px-3 py-1.5 mb-1.5 bg-accent-600 text-white text-xs font-medium rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
          >
            <Plus size={14} />
            New User
          </button>
        {:else}
          <button
            onclick={() => {
              showCreatePermForm = !showCreatePermForm;
            }}
            class="flex items-center gap-1.5 px-3 py-1.5 mb-1.5 bg-accent-600 text-white text-xs font-medium rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
          >
            <Plus size={14} />
            New Permission
          </button>
        {/if}
      </div>

      {#if activeTab === "users"}
        <UsersTab
          {query}
          bind:showCreateForm
          {canManagePermissions}
          onEdit={(u) => (editingUser = u)}
          {reload}
        />
      {:else}
        <PermissionsTab
          {knownKeys}
          bind:showCreateForm={showCreatePermForm}
          {canManageUsers}
          onEdit={(p) => (editingPerm = p)}
          onViewUsers={viewUsersWithPermission}
        />
      {/if}

      {#if editingUser}
        {#key editingUser.id}
          <EditUserModal
            user={editingUser}
            {knownKeys}
            {canManagePermissions}
            onClose={() => (editingUser = null)}
          />
        {/key}
      {/if}

      {#if editingPerm}
        {#key editingPerm.id}
          <EditPermissionModal
            perm={editingPerm}
            {knownKeys}
            onClose={() => (editingPerm = null)}
          />
        {/key}
      {/if}
    {/if}
  </div>
</div>
