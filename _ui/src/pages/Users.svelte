<script lang="ts">
  import {
    appStore,
    type UserInfo,
    type UserQuery,
    type PermissionInfo,
    type EffectiveReport,
    type SessionView,
    type UserIdentity,
    type CapSource,
  } from "@/lib/store/store.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import {
    Plus,
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
    Check,
    Ban,
    Globe,
    Monitor,
    Link as LinkIcon,
    Users as UsersIcon,
  } from "lucide-svelte";

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

  // User state
  let showCreateForm = $state(false);
  let newUsername = $state("");
  let newPassword = $state("");
  let creating = $state(false);

  let editingUser = $state<UserInfo | null>(null);
  let editPassword = $state("");
  let editUsername = $state("");
  let editTab = $state<"details" | "permissions" | "access">("details");
  let editUserPermissionIds = $state<string[]>([]);
  let loadingUserPerms = $state(false);

  // Access tab (effective permissions + sessions + per-user deny).
  let effectiveReport = $state<EffectiveReport | null>(null);
  let userSessions = $state<SessionView[]>([]);
  let userIdentities = $state<UserIdentity[]>([]);
  let editDeniedCaps = $state<string[]>([]);
  let loadingAccess = $state(false);
  let accessLoaded = $state(false);

  let confirmDeleteId = $state<string | null>(null);
  // confirmResetTOTPId mirrors confirmDeleteId for the "Reset 2FA"
  // affordance. The action is destructive (the user's enrolled
  // authenticator is wiped and they have to re-enroll), so we use the
  // same inline-confirm pattern as Delete rather than relying on a
  // browser confirm() dialog.
  let confirmResetTOTPId = $state<string | null>(null);

  // Query state
  let searchText = $state("");
  let sortField = $state("username");
  let sortDir = $state<"asc" | "desc">("asc");
  let pageSize = $state(20);
  let currentPage = $state(1);
  let searchTimeout = $state<ReturnType<typeof setTimeout> | null>(null);
  // Permission filter — when set, only users granted this permission bundle
  // are returned. Server resolves the matching user IDs and applies an
  // id IN (...) filter so pagination/sort still work.
  let filterPermissionId = $state<string>("");

  // Permission state
  let showCreatePermForm = $state(false);
  let newPermKey = $state("");
  let newPermName = $state("");
  let newPermDesc = $state("");
  let newPermKeys = $state<string[]>([]);
  let newPermPatterns = $state<Record<string, string[]>>({});
  let creatingPerm = $state(false);
  let editingPerm = $state<PermissionInfo | null>(null);
  let editPermKey = $state("");
  let editPermName = $state("");
  let editPermDesc = $state("");
  let editPermKeys = $state<string[]>([]);
  let editPermPatterns = $state<Record<string, string[]>>({});
  let confirmDeletePermId = $state<string | null>(null);
  let showNewPatternHelp = $state(false);
  let showEditPatternHelp = $state(false);

  // Canonical list of capability keys — sourced from the server via
  // /api/v1/info so this stays in sync with the Go constants in
  // internal/service/capabilities.go automatically.
  const knownKeys = $derived<
    { key: string; name: string; description: string }[]
  >(appStore.info?.capabilities ?? []);

  const users = $derived(appStore.users);
  const total = $derived(appStore.usersTotal);
  const currentUser = $derived(appStore.info?.user);
  const allPermissions = $derived(appStore.permissions);
  const totalPages = $derived(Math.max(1, Math.ceil(total / pageSize)));
  const showingFrom = $derived(
    total === 0 ? 0 : (currentPage - 1) * pageSize + 1,
  );
  const showingTo = $derived(Math.min(currentPage * pageSize, total));

  function buildQuery(): UserQuery {
    return {
      limit: pageSize,
      offset: (currentPage - 1) * pageSize,
      sort: sortDir === "desc" ? `-${sortField}` : sortField,
      search: searchText || undefined,
      permissionId: filterPermissionId || undefined,
    };
  }

  // Reset to page 1 whenever the permission filter changes — keeps the
  // pagination state consistent with the new (typically smaller) result set.
  function handlePermissionFilterChange(value: string) {
    filterPermissionId = value;
    currentPage = 1;
    reload();
  }

  // Cross-tab shortcut from the Permissions table: jump to the Users tab
  // with the permission filter pre-applied, so the admin sees exactly who
  // holds the bundle they just inspected.
  function viewUsersWithPermission(permissionId: string) {
    filterPermissionId = permissionId;
    searchText = "";
    currentPage = 1;
    activeTab = "users";
    reload();
  }

  function reload() {
    appStore.loadUsers(buildQuery());
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
      sortDir = sortDir === "asc" ? "desc" : "asc";
    } else {
      sortField = field;
      sortDir = "asc";
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
      addToast(`User "${newUsername}" created`, "success");
      newUsername = "";
      newPassword = "";
      showCreateForm = false;
    } catch (err: any) {
      addToast(
        err?.response?.data?.message || "Failed to create user",
        "alert",
      );
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
    } catch (err: any) {
      addToast(
        err?.response?.data?.message || "Failed to update user",
        "alert",
      );
    }
  }

  async function handleDelete(id: string) {
    try {
      await appStore.deleteUser(id);
      addToast("User deleted", "success");
      confirmDeleteId = null;
    } catch (err: any) {
      addToast(
        err?.response?.data?.message || "Failed to delete user",
        "alert",
      );
    }
  }

  async function handleKick(user: UserInfo) {
    try {
      await appStore.kickUser(user.id);
      addToast(`All sessions for "${user.username}" terminated`, "success");
    } catch (err: any) {
      addToast(err?.response?.data?.message || "Failed to kick user", "alert");
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
    } catch (err: any) {
      addToast(err?.response?.data?.message || "Failed to reset 2FA", "alert");
    }
  }

  async function startEdit(user: UserInfo) {
    editingUser = user;
    editUsername = user.username;
    editPassword = "";
    editTab = "details";
    editUserPermissionIds = [];
    // Reset Access-tab state; loaded lazily when that tab is opened.
    effectiveReport = null;
    userSessions = [];
    userIdentities = [];
    editDeniedCaps = user.denied_capabilities ?? [];
    accessLoaded = false;
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

  // Lazily load the Access tab payload (effective perms + sessions +
  // linked identities) the first time the tab is opened for a user.
  async function openAccessTab() {
    editTab = "access";
    if (!editingUser || accessLoaded || loadingAccess) return;
    loadingAccess = true;
    try {
      const [rep, sess, idents] = await Promise.all([
        appStore.getUserEffectivePermissions(editingUser.id),
        appStore.listUserSessions(editingUser.id),
        appStore.getUserIdentities(editingUser.id),
      ]);
      effectiveReport = rep;
      userSessions = sess;
      userIdentities = idents;
      editDeniedCaps = rep.denied ?? [];
      accessLoaded = true;
    } catch (err: any) {
      addToast(
        err?.response?.data?.message || "Failed to load access info",
        "alert",
      );
    } finally {
      loadingAccess = false;
    }
  }

  // Toggle a single capability into/out of the per-user deny overlay.
  // Persists immediately (separate from the modal Save) and re-resolves so
  // the effective set reflects the change. Gated by permissions.manage.
  async function toggleDeny(capKey: string) {
    if (!editingUser) return;
    const next = editDeniedCaps.includes(capKey)
      ? editDeniedCaps.filter((k) => k !== capKey)
      : [...editDeniedCaps, capKey];
    try {
      await appStore.setUserDeniedPermissions(editingUser.id, next);
      effectiveReport = await appStore.getUserEffectivePermissions(
        editingUser.id,
      );
      editDeniedCaps = effectiveReport.denied ?? next;
      addToast(
        "Deny updated — applies on the user's next request",
        "success",
      );
    } catch (err: any) {
      addToast(
        err?.response?.data?.message || "Failed to update deny",
        "alert",
      );
    }
  }

  async function revokeSession(handle: string) {
    if (!editingUser) return;
    try {
      await appStore.revokeUserSession(editingUser.id, handle);
      userSessions = userSessions.filter((s) => s.handle !== handle);
      addToast("Session revoked", "success");
      await appStore.loadUsers();
    } catch (err: any) {
      addToast(
        err?.response?.data?.message || "Failed to revoke session",
        "alert",
      );
    }
  }

  // Human label for a capability's grant source.
  function sourceLabel(s: CapSource): string {
    switch (s.kind) {
      case "superadmin":
        return "superadmin";
      case "db_bundle":
        return `bundle ${s.bundle}`;
      case "role":
        return `role ${s.role}${s.bundle ? ` → ${s.bundle}` : ""}`;
      case "scope":
        return `scope ${s.scope}${s.bundle ? ` → ${s.bundle}` : ""}`;
      default:
        return s.kind;
    }
  }

  function toggleEditPermission(permId: string) {
    if (editUserPermissionIds.includes(permId)) {
      editUserPermissionIds = editUserPermissionIds.filter(
        (id) => id !== permId,
      );
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
        await appStore.setUserPermissions(
          editingUser.id,
          editUserPermissionIds,
        );
      }

      addToast("User updated", "success");
      editingUser = null;
    } catch (err: any) {
      addToast(
        err?.response?.data?.message || "Failed to update user",
        "alert",
      );
    }
  }

  function toggleNewPermKey(key: string) {
    if (newPermKeys.includes(key)) {
      newPermKeys = newPermKeys.filter((k) => k !== key);
      // Drop any patterns staged for this key — they're meaningless without
      // the cap selected, and re-adding the cap should start fresh.
      if (newPermPatterns[key]) {
        const { [key]: _, ...rest } = newPermPatterns;
        newPermPatterns = rest;
      }
    } else {
      newPermKeys = [...newPermKeys, key];
    }
  }

  function toggleEditPermKey(key: string) {
    if (editPermKeys.includes(key)) {
      editPermKeys = editPermKeys.filter((k) => k !== key);
      if (editPermPatterns[key]) {
        const { [key]: _, ...rest } = editPermPatterns;
        editPermPatterns = rest;
      }
    } else {
      editPermKeys = [...editPermKeys, key];
    }
  }

  // Pattern editors mutate a Record<string, string[]>. Reassigning the
  // outer object on every change keeps Svelte 5 reactivity happy.
  function addPattern(target: "new" | "edit", key: string) {
    if (target === "new") {
      newPermPatterns = {
        ...newPermPatterns,
        [key]: [...(newPermPatterns[key] ?? []), ""],
      };
    } else {
      editPermPatterns = {
        ...editPermPatterns,
        [key]: [...(editPermPatterns[key] ?? []), ""],
      };
    }
  }

  function updatePattern(
    target: "new" | "edit",
    key: string,
    idx: number,
    value: string,
  ) {
    if (target === "new") {
      const arr = [...(newPermPatterns[key] ?? [])];
      arr[idx] = value;
      newPermPatterns = { ...newPermPatterns, [key]: arr };
    } else {
      const arr = [...(editPermPatterns[key] ?? [])];
      arr[idx] = value;
      editPermPatterns = { ...editPermPatterns, [key]: arr };
    }
  }

  function removePattern(target: "new" | "edit", key: string, idx: number) {
    if (target === "new") {
      const arr = (newPermPatterns[key] ?? []).filter((_, i) => i !== idx);
      if (arr.length === 0) {
        const { [key]: _drop, ...rest } = newPermPatterns;
        newPermPatterns = rest;
      } else {
        newPermPatterns = { ...newPermPatterns, [key]: arr };
      }
    } else {
      const arr = (editPermPatterns[key] ?? []).filter((_, i) => i !== idx);
      if (arr.length === 0) {
        const { [key]: _drop, ...rest } = editPermPatterns;
        editPermPatterns = rest;
      } else {
        editPermPatterns = { ...editPermPatterns, [key]: arr };
      }
    }
  }

  // Strip empties + return undefined when no patterns remain, so we don't
  // send `key_patterns: {}` in request bodies.
  function cleanPatterns(
    map: Record<string, string[]>,
    allowedKeys: string[],
  ): Record<string, string[]> | undefined {
    const out: Record<string, string[]> = {};
    for (const k of Object.keys(map)) {
      if (!allowedKeys.includes(k)) continue;
      const trimmed = (map[k] ?? []).map((s) => s.trim()).filter(Boolean);
      if (trimmed.length > 0) out[k] = trimmed;
    }
    return Object.keys(out).length > 0 ? out : undefined;
  }

  // Permission CRUD handlers
  async function handleCreatePerm() {
    if (!newPermName || newPermKeys.length === 0) return;
    creatingPerm = true;
    // Auto-generate key from name if not provided
    const key =
      newPermKey ||
      newPermName
        .toLowerCase()
        .replace(/\s+/g, "-")
        .replace(/[^a-z0-9.-]/g, "");
    try {
      const patterns = cleanPatterns(newPermPatterns, newPermKeys);
      await appStore.createPermission(
        key,
        newPermName,
        newPermDesc,
        newPermKeys,
        patterns,
      );
      addToast(`Permission "${newPermName}" created`, "success");
      newPermKey = "";
      newPermName = "";
      newPermDesc = "";
      newPermKeys = [];
      newPermPatterns = {};
      showCreatePermForm = false;
    } catch (err: any) {
      addToast(
        err?.response?.data?.message || "Failed to create permission",
        "alert",
      );
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
    // Deep-copy patterns so edits don't mutate the cached PermissionInfo.
    const src = perm.key_patterns ?? {};
    const copy: Record<string, string[]> = {};
    for (const k of Object.keys(src)) copy[k] = [...src[k]];
    editPermPatterns = copy;
  }

  // Compares two pattern maps (after cleaning) for content equality.
  function patternsEqual(
    a: Record<string, string[]> | undefined,
    b: Record<string, string[]> | undefined,
  ): boolean {
    const ka = a ? Object.keys(a).sort() : [];
    const kb = b ? Object.keys(b).sort() : [];
    if (ka.length !== kb.length) return false;
    for (let i = 0; i < ka.length; i++) {
      if (ka[i] !== kb[i]) return false;
      const av = [...(a?.[ka[i]] ?? [])].sort();
      const bv = [...(b?.[kb[i]] ?? [])].sort();
      if (av.length !== bv.length) return false;
      for (let j = 0; j < av.length; j++) if (av[j] !== bv[j]) return false;
    }
    return true;
  }

  async function handleSaveEditPerm() {
    if (!editingPerm) return;
    try {
      const updates: {
        key?: string;
        name?: string;
        description?: string;
        keys?: string[];
        key_patterns?: Record<string, string[]>;
      } = {};
      if (editPermKey !== editingPerm.key) updates.key = editPermKey;
      if (editPermName !== editingPerm.name) updates.name = editPermName;
      if (editPermDesc !== editingPerm.description)
        updates.description = editPermDesc;
      // Always send keys to allow updating the capability set
      const origKeys = [...(editingPerm.keys || [])].sort().join(",");
      const newKeys = [...editPermKeys].sort().join(",");
      if (origKeys !== newKeys) updates.keys = editPermKeys;

      // Send patterns whenever they changed (including transitions to empty —
      // that's how the user clears all path scoping for a permission).
      const cleaned = cleanPatterns(editPermPatterns, editPermKeys);
      const original = editingPerm.key_patterns;
      if (!patternsEqual(cleaned, original)) {
        // Backend treats `key_patterns: {}` as "replace with empty",
        // i.e. clear all patterns. Send an empty object explicitly so a
        // user who removes all patterns gets that effect.
        updates.key_patterns = cleaned ?? {};
      }

      if (Object.keys(updates).length > 0) {
        await appStore.updatePermission(editingPerm.id, updates);
        addToast("Permission updated", "success");
      }
      editingPerm = null;
    } catch (err: any) {
      addToast(
        err?.response?.data?.message || "Failed to update permission",
        "alert",
      );
    }
  }

  async function handleDeletePerm(id: string) {
    try {
      await appStore.deletePermission(id);
      addToast("Permission deleted", "success");
      confirmDeletePermId = null;
    } catch (err: any) {
      addToast(
        err?.response?.data?.message || "Failed to delete permission",
        "alert",
      );
    }
  }
</script>

<div class="h-full overflow-auto p-6">
  <div class="max-w-3xl mx-auto">
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
        class="bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg p-8 text-center"
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

      <!-- ========== USERS TAB ========== -->
      {#if activeTab === "users"}
        <!-- Create User Form -->
        {#if showCreateForm}
          <div
            class="bg-white dark:bg-warm-900 rounded-lg border border-slate-200 dark:border-warm-700 p-4 mb-4"
          >
            <h3
              class="text-sm font-medium text-slate-700 dark:text-slate-200 mb-3"
            >
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
                  class="w-full px-3 py-1.5 border border-slate-300 dark:border-warm-700 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent"
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
                  class="w-full px-3 py-1.5 border border-slate-300 dark:border-warm-700 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent"
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
                class="px-4 py-1.5 bg-white dark:bg-warm-900 border border-slate-300 dark:border-warm-700 text-slate-600 dark:text-slate-300 text-sm rounded-md hover:bg-slate-50 dark:hover:bg-warm-700 transition-colors"
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
              value={searchText}
              oninput={(e) => handleSearch(e.currentTarget.value)}
              class="w-full pl-9 pr-3 py-2 border border-slate-200 dark:border-warm-700 rounded-lg text-sm bg-white dark:bg-warm-900 focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent placeholder-slate-400"
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
                value={filterPermissionId}
                onchange={(e) =>
                  handlePermissionFilterChange(e.currentTarget.value)}
                class="appearance-none pl-9 pr-8 py-2 border border-slate-200 dark:border-warm-700 rounded-lg text-sm bg-white dark:bg-warm-900 focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent text-slate-700 dark:text-slate-200 cursor-pointer min-w-[180px]"
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
          class="bg-white dark:bg-warm-900 rounded-lg border border-slate-200 dark:border-warm-700 overflow-hidden"
        >
          <table class="w-full text-sm">
            <thead>
              <tr
                class="bg-slate-50 dark:bg-warm-800 border-b border-slate-200 dark:border-warm-700"
              >
                <th class="text-left px-4 py-2.5">
                  <button
                    onclick={() => handleSort("username")}
                    class="flex items-center gap-1 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider hover:text-slate-800 dark:hover:text-slate-100 transition-colors"
                  >
                    Username
                    {#if sortField === "username"}
                      {#if sortDir === "asc"}<ChevronUp
                          size={12}
                        />{:else}<ChevronDown size={12} />{/if}
                    {:else}
                      <ChevronsUpDown size={12} class="text-slate-300" />
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
                    class="flex items-center gap-1 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider hover:text-slate-800 dark:hover:text-slate-100 transition-colors"
                  >
                    Created
                    {#if sortField === "created_at"}
                      {#if sortDir === "asc"}<ChevronUp
                          size={12}
                        />{:else}<ChevronDown size={12} />{/if}
                    {:else}
                      <ChevronsUpDown size={12} class="text-slate-300" />
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
                    ? 'bg-accent-50/60 border-l-2 border-l-accent-500 dark:bg-accent-900/20'
                    : 'hover:bg-slate-50 dark:hover:bg-warm-700'}"
                >
                  <td class="px-4 py-3">
                    <div class="flex items-center gap-2">
                      <span class="relative flex h-2 w-2 shrink-0">
                        {#if isOnline && !user.disabled}
                          <span
                            class="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75"
                          ></span>
                          <span
                            class="relative inline-flex rounded-full h-2 w-2 bg-green-500"
                          ></span>
                        {:else}
                          <span
                            class="relative inline-flex rounded-full h-2 w-2 bg-slate-300"
                          ></span>
                        {/if}
                      </span>
                      <span
                        class="font-medium {isYou
                          ? 'text-accent-800 dark:text-accent-200'
                          : 'text-slate-800 dark:text-slate-100'}"
                        >{user.username}</span
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
                        class="inline-flex items-center gap-1 text-xs px-2 py-0.5 bg-red-50 dark:bg-red-900/40 text-red-600 dark:text-red-300 rounded-full"
                      >
                        <UserX size={12} /> Disabled
                      </span>
                    {:else}
                      <span
                        class="inline-flex items-center gap-1 text-xs px-2 py-0.5 bg-green-50 dark:bg-green-900/40 text-green-600 dark:text-green-300 rounded-full"
                      >
                        <UserCheck size={12} /> Active
                      </span>
                    {/if}
                  </td>
                  <td class="px-4 py-3">
                    {#if isOnline}
                      <span
                        class="inline-flex items-center gap-1 text-xs px-2 py-0.5 bg-green-50 dark:bg-green-900/40 text-green-700 dark:text-green-300 rounded-full tabular-nums"
                        >{user.active_sessions}</span
                      >
                    {:else}
                      <span class="text-xs text-slate-400 dark:text-slate-500"
                        >0</span
                      >
                    {/if}
                  </td>
                  <td
                    class="px-4 py-3 text-slate-500 dark:text-slate-400 text-xs"
                    >{new Date(user.created_at).toLocaleDateString()}</td
                  >
                  <td class="px-4 py-3 text-right">
                    <div class="flex items-center justify-end gap-1">
                      <button
                        onclick={() => startEdit(user)}
                        class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-brand-500 hover:bg-brand-50 dark:hover:bg-brand-900/30 rounded transition-colors"
                        title="Edit user"
                      >
                        <KeyRound size={14} />
                      </button>
                      {#if !isYou && isOnline}
                        <button
                          onclick={() => handleKick(user)}
                          class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-orange-500 hover:bg-orange-50 dark:hover:bg-orange-900/30 rounded transition-colors"
                          title="Kick user"
                        >
                          <LogOut size={14} />
                        </button>
                      {/if}
                      <button
                        onclick={() => handleToggleDisabled(user)}
                        class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-amber-500 hover:bg-amber-50 dark:hover:bg-amber-900/30 rounded transition-colors"
                        title={user.disabled ? "Enable user" : "Disable user"}
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
                            class="px-2 py-1 text-xs bg-vermilion-500 text-white rounded hover:bg-vermilion-600 transition-colors"
                            title="Confirm reset — user must re-enroll their authenticator"
                            >Reset 2FA</button
                          >
                          <button
                            onclick={() => {
                              confirmResetTOTPId = null;
                            }}
                            class="px-2 py-1 text-xs bg-slate-200 dark:bg-warm-700 text-slate-600 dark:text-warm-200 rounded hover:bg-slate-300 dark:hover:bg-warm-600 transition-colors"
                            >Cancel</button
                          >
                        {:else}
                          <button
                            onclick={() => {
                              confirmResetTOTPId = user.id;
                              confirmDeleteId = null;
                            }}
                            class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-vermilion-500 hover:bg-vermilion-50 dark:hover:bg-vermilion-900/30 rounded transition-colors"
                            title="Reset 2FA (user lost their authenticator)"
                          >
                            <ShieldOff size={14} />
                          </button>
                        {/if}
                      {/if}
                      {#if !isYou}
                        {#if confirmDeleteId === user.id}
                          <button
                            onclick={() => handleDelete(user.id)}
                            class="px-2 py-1 text-xs bg-red-500 text-white rounded hover:bg-red-600 transition-colors"
                            >Confirm</button
                          >
                          <button
                            onclick={() => {
                              confirmDeleteId = null;
                            }}
                            class="px-2 py-1 text-xs bg-slate-200 dark:bg-warm-700 text-slate-600 dark:text-warm-200 rounded hover:bg-slate-300 dark:hover:bg-warm-600 transition-colors"
                            >Cancel</button
                          >
                        {:else}
                          <button
                            onclick={() => {
                              confirmDeleteId = user.id;
                              confirmResetTOTPId = null;
                            }}
                            class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-900/30 rounded transition-colors"
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
                  <td
                    colspan="5"
                    class="px-4 py-8 text-center text-slate-400 dark:text-slate-500 text-sm"
                  >
                    {searchText || filterPermissionId
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
              class="flex items-center justify-between px-4 py-3 border-t border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-800"
            >
              <div
                class="flex items-center gap-2 text-xs text-slate-500 dark:text-slate-400"
              >
                <span>Showing {showingFrom}-{showingTo} of {total}</span>
                <span class="text-slate-300">|</span>
                <label for="page-size" class="sr-only">Rows per page</label>
                <select
                  id="page-size"
                  value={pageSize}
                  onchange={(e) =>
                    handlePageSizeChange(Number(e.currentTarget.value))}
                  class="px-1.5 py-0.5 border border-slate-200 dark:border-warm-700 rounded text-xs bg-white dark:bg-warm-900 focus:outline-none focus:ring-1 focus:ring-brand-500"
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
                  class="p-1 rounded text-slate-400 dark:text-slate-500 hover:text-slate-700 dark:hover:text-slate-200 hover:bg-slate-200 dark:hover:bg-warm-700 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
                  title="Previous page"
                >
                  <ChevronLeft size={16} />
                </button>
                <span
                  class="text-xs text-slate-600 dark:text-slate-300 px-2 tabular-nums"
                  >{currentPage} / {totalPages}</span
                >
                <button
                  onclick={() => goToPage(currentPage + 1)}
                  disabled={currentPage >= totalPages}
                  class="p-1 rounded text-slate-400 dark:text-slate-500 hover:text-slate-700 dark:hover:text-slate-200 hover:bg-slate-200 dark:hover:bg-warm-700 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
                  title="Next page"
                >
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
          <div
            class="bg-white dark:bg-warm-900 rounded-lg border border-slate-200 dark:border-warm-700 p-4 mb-4"
          >
            <h3
              class="text-sm font-medium text-slate-700 dark:text-slate-200 mb-3"
            >
              Create Permission
            </h3>
            <div class="grid grid-cols-2 gap-3 mb-3">
              <div>
                <label
                  for="new-perm-name"
                  class="block text-xs text-slate-500 dark:text-slate-400 mb-1"
                  >Name</label
                >
                <input
                  id="new-perm-name"
                  type="text"
                  bind:value={newPermName}
                  class="w-full px-3 py-1.5 border border-slate-300 dark:border-warm-700 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent"
                  placeholder="e.g. Editor"
                />
              </div>
              <div>
                <label
                  for="new-perm-key"
                  class="block text-xs text-slate-500 dark:text-slate-400 mb-1"
                  >Slug <span class="text-slate-400 dark:text-slate-500"
                    >(auto-generated if empty)</span
                  ></label
                >
                <input
                  id="new-perm-key"
                  type="text"
                  bind:value={newPermKey}
                  class="w-full px-3 py-1.5 border border-slate-300 dark:border-warm-700 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent"
                  placeholder="e.g. editor"
                />
              </div>
            </div>
            <div class="mb-3">
              <label
                for="new-perm-desc"
                class="block text-xs text-slate-500 dark:text-slate-400 mb-1"
                >Description</label
              >
              <input
                id="new-perm-desc"
                type="text"
                bind:value={newPermDesc}
                class="w-full px-3 py-1.5 border border-slate-300 dark:border-warm-700 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent"
                placeholder="What this permission grants"
              />
            </div>
            <!-- Capability keys selector -->
            <div class="mb-3">
              <div class="text-xs text-slate-500 dark:text-slate-400 mb-2">
                Capabilities granted:
              </div>
              <div class="grid grid-cols-2 gap-1.5">
                {#each knownKeys as known (known.key)}
                  {@const selected = newPermKeys.includes(known.key)}
                  <button
                    onclick={() => toggleNewPermKey(known.key)}
                    type="button"
                    class="flex items-start gap-2 px-2.5 py-2 border rounded-md transition-all text-left text-xs
 {selected
                      ? 'bg-accent-50 border-accent-300 dark:bg-accent-900/30 dark:border-accent-700'
                      : 'bg-white dark:bg-warm-900 border-slate-200 dark:border-warm-700 hover:border-slate-300 dark:border-warm-700'}"
                  >
                    <div
                      class="flex items-center justify-center w-3.5 h-3.5 mt-0.5 shrink-0 rounded border transition-colors
 {selected
                        ? 'bg-accent-500 border-accent-500'
                        : 'border-slate-300 dark:border-warm-700'}"
                    >
                      {#if selected}<Check size={9} class="text-white" />{/if}
                    </div>
                    <div class="min-w-0">
                      <code
                        class="font-medium {selected
                          ? 'text-accent-700 dark:text-accent-300'
                          : 'text-slate-600 dark:text-slate-300'}"
                        >{known.key}</code
                      >
                      <div
                        class="text-[10px] text-slate-400 dark:text-slate-500 mt-0.5 leading-snug"
                      >
                        {known.description}
                      </div>
                    </div>
                  </button>
                {/each}
              </div>
            </div>

            <!-- Path scoping (optional). Renders one block per selected cap.
 Empty list = unrestricted, identical to the prior behavior. -->
            {#if newPermKeys.length > 0}
              <div
                class="mb-3 border-t border-slate-200 dark:border-warm-700 pt-3"
              >
                <div class="flex items-baseline justify-between mb-1">
                  <div
                    class="text-xs font-medium text-slate-600 dark:text-slate-300"
                  >
                    Path scoping <span
                      class="text-slate-400 dark:text-slate-500 font-normal"
                      >(optional)</span
                    >
                  </div>
                  <button
                    type="button"
                    onclick={() => {
                      showNewPatternHelp = !showNewPatternHelp;
                    }}
                    class="text-[10px] text-brand-600 hover:underline"
                  >
                    {showNewPatternHelp ? "Hide help" : "How do patterns work?"}
                  </button>
                </div>

                {#if showNewPatternHelp}
                  <div
                    class="mb-2 p-2.5 bg-brand-50 border border-brand-200 dark:bg-brand-900/30 dark:border-brand-700 rounded text-[11px] text-slate-700 dark:text-slate-200 space-y-1.5 leading-relaxed"
                  >
                    <div>
                      Patterns match the <strong>storage key</strong> — the part
                      after <code>/api/v1/file/</code> or
                      <code>/api/v1/folder/</code>. No leading slash. No
                      implicit prefix: a file stored as
                      <code>team-a/app.yaml</code>
                      is matched as <code>team-a/app.yaml</code>. Restrictions
                      only apply to the <code>files.*</code> capabilities; admin
                      caps (users, tokens, settings) ignore patterns.
                    </div>
                    <div>
                      <span class="font-medium">Glob syntax:</span>
                      <code>*</code> = one path segment ·
                      <code>**</code> = any number of segments (including zero)
                      ·
                      <code>?</code> = one character ·
                      <code>[abc]</code> = character class.
                    </div>
                    <div>
                      <span class="font-medium">Examples:</span>
                      <ul class="ml-4 list-disc space-y-0.5 mt-1">
                        <li>
                          <code>team-a/**</code> — anything under
                          <code>team-a/</code> (any depth)
                        </li>
                        <li>
                          <code>**/*.yaml</code> — every yaml file at any depth
                        </li>
                        <li>
                          <code>apps/*/config.yaml</code> —
                          <code>config.yaml</code>
                          in any direct child of <code>apps/</code>
                        </li>
                        <li>
                          <code>shared</code> — only the literal name
                          <code>shared</code>, no descendants
                        </li>
                        <li>
                          <code>prod/**</code> + <code>staging/**</code> — multiple
                          patterns are OR'd
                        </li>
                      </ul>
                    </div>
                    <div class="text-slate-500 dark:text-slate-400">
                      Empty list = unrestricted (default). For folder listings,
                      parent directories of a matched path are allowed
                      automatically so users can navigate.
                      <code>..</code> segments are rejected.
                    </div>
                  </div>
                {/if}

                <div class="space-y-2">
                  {#each newPermKeys as k (k)}
                    {@const patterns = newPermPatterns[k] ?? []}
                    {@const scopable = k.startsWith("files.")}
                    <div
                      class="border border-slate-200 dark:border-warm-700 rounded-md p-2 bg-slate-50 dark:bg-warm-800"
                    >
                      <div class="flex items-center justify-between mb-1.5">
                        <div class="flex items-center gap-2">
                          <code
                            class="text-[11px] font-medium text-slate-700 dark:text-slate-200"
                            >{k}</code
                          >
                          {#if !scopable}
                            <span
                              class="text-[9px] text-slate-400 dark:text-slate-500"
                              >(patterns ignored — not a path-bound capability)</span
                            >
                          {/if}
                        </div>
                        {#if scopable}
                          <button
                            type="button"
                            onclick={() => addPattern("new", k)}
                            class="text-[10px] text-brand-600 hover:text-accent-700 dark:text-accent-300 hover:underline"
                            >+ Add pattern</button
                          >
                        {/if}
                      </div>
                      {#if !scopable}
                        <div
                          class="text-[10px] text-slate-400 dark:text-slate-500 italic"
                        >
                          applies globally
                        </div>
                      {:else if patterns.length === 0}
                        <div
                          class="text-[10px] text-slate-400 dark:text-slate-500 italic"
                        >
                          all paths
                        </div>
                      {:else}
                        <div class="space-y-1">
                          {#each patterns as pat, i}
                            <div class="flex gap-1 items-center">
                              <input
                                type="text"
                                value={pat}
                                oninput={(e) =>
                                  updatePattern(
                                    "new",
                                    k,
                                    i,
                                    e.currentTarget.value,
                                  )}
                                placeholder="e.g. team-a/**"
                                class="flex-1 px-2 py-1 border border-slate-300 dark:border-warm-700 rounded text-[11px] font-mono focus:outline-none focus:ring-1 focus:ring-brand-500"
                              />
                              <button
                                type="button"
                                onclick={() => removePattern("new", k, i)}
                                class="p-1 text-slate-400 dark:text-slate-500 hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-900/30 rounded transition-colors"
                                title="Remove pattern"
                              >
                                <Trash2 size={12} />
                              </button>
                            </div>
                          {/each}
                        </div>
                      {/if}
                    </div>
                  {/each}
                </div>
              </div>
            {/if}

            <div class="flex items-center gap-2">
              <button
                onclick={handleCreatePerm}
                disabled={creatingPerm ||
                  !newPermName ||
                  newPermKeys.length === 0}
                class="px-4 py-1.5 bg-accent-600 text-white text-sm rounded-md hover:bg-accent-700 disabled:opacity-50 transition-colors cursor-pointer disabled:cursor-not-allowed"
              >
                {creatingPerm
                  ? "Creating..."
                  : `Create (${newPermKeys.length} capabilities)`}
              </button>
              <button
                onclick={() => {
                  showCreatePermForm = false;
                  newPermKeys = [];
                  newPermPatterns = {};
                }}
                class="px-4 py-1.5 bg-white dark:bg-warm-900 border border-slate-300 dark:border-warm-700 text-slate-600 dark:text-slate-300 text-sm rounded-md hover:bg-slate-50 dark:hover:bg-warm-700 transition-colors"
              >
                Cancel
              </button>
            </div>
          </div>
        {/if}

        <!-- Permissions Table -->
        <div
          class="bg-white dark:bg-warm-900 rounded-lg border border-slate-200 dark:border-warm-700 overflow-hidden"
        >
          <table class="w-full text-sm">
            <thead>
              <tr
                class="bg-slate-50 dark:bg-warm-800 border-b border-slate-200 dark:border-warm-700"
              >
                <th
                  class="text-left px-4 py-2.5 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider"
                  >Name</th
                >
                <th
                  class="text-left px-4 py-2.5 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider"
                  >Capabilities</th
                >
                <th
                  class="text-right px-4 py-2.5 text-xs font-medium text-slate-500 dark:text-slate-400 uppercase tracking-wider"
                  >Actions</th
                >
              </tr>
            </thead>
            <tbody>
              {#each allPermissions as perm (perm.id)}
                <tr
                  class="border-b border-slate-100 dark:border-warm-700 hover:bg-slate-50 dark:hover:bg-warm-700"
                >
                  <td class="px-4 py-3">
                    <div class="font-medium text-slate-800 dark:text-slate-100">
                      {perm.name}
                    </div>
                    {#if perm.description}
                      <div
                        class="text-[11px] text-slate-400 dark:text-slate-500 mt-0.5"
                      >
                        {perm.description}
                      </div>
                    {/if}
                  </td>
                  <td class="px-4 py-3">
                    <div class="flex flex-wrap gap-1">
                      {#each perm.keys || [] as k}
                        {@const pats = perm.key_patterns?.[k] ?? []}
                        <span class="inline-flex items-center gap-1">
                          <code
                            class="text-[10px] font-medium text-slate-600 dark:text-warm-100 bg-slate-100 dark:bg-warm-700 px-1.5 py-0.5 rounded"
                            >{k}</code
                          >
                          {#if pats.length > 0}
                            <!-- Badge surfaces that this grant is path-scoped.
  Title attribute lists every pattern so admins
  can verify without opening the editor. -->
                            <span
                              class="text-[10px] font-medium text-amber-700 bg-amber-100 dark:bg-amber-900/40 dark:text-amber-200 px-1.5 py-0.5 rounded"
                              title={pats.join("\n")}
                            >
                              {pats.length}
                              {pats.length === 1 ? "path" : "paths"}
                            </span>
                          {/if}
                        </span>
                      {:else}
                        <span class="text-xs text-slate-400 dark:text-slate-500"
                          >none</span
                        >
                      {/each}
                    </div>
                  </td>
                  <td class="px-4 py-3 text-right">
                    <div class="flex items-center justify-end gap-1">
                      {#if canManageUsers}
                        <!-- Jump to the Users tab pre-filtered to users that
 hold this permission. Saves a manual dropdown trip. -->
                        <button
                          onclick={() => viewUsersWithPermission(perm.id)}
                          class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-brand-500 hover:bg-brand-50 dark:hover:bg-brand-900/30 rounded transition-colors"
                          title="View users with this permission"
                        >
                          <UsersIcon size={14} />
                        </button>
                      {/if}
                      <button
                        onclick={() => startEditPerm(perm)}
                        class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-brand-500 hover:bg-brand-50 dark:hover:bg-brand-900/30 rounded transition-colors"
                        title="Edit permission"
                      >
                        <KeyRound size={14} />
                      </button>
                      {#if confirmDeletePermId === perm.id}
                        <button
                          onclick={() => handleDeletePerm(perm.id)}
                          class="px-2 py-1 text-xs bg-red-500 text-white rounded hover:bg-red-600 transition-colors"
                          >Confirm</button
                        >
                        <button
                          onclick={() => {
                            confirmDeletePermId = null;
                          }}
                          class="px-2 py-1 text-xs bg-slate-200 dark:bg-warm-700 text-slate-600 dark:text-warm-200 rounded hover:bg-slate-300 dark:hover:bg-warm-600 transition-colors"
                          >Cancel</button
                        >
                      {:else}
                        <button
                          onclick={() => {
                            confirmDeletePermId = perm.id;
                          }}
                          class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-900/30 rounded transition-colors"
                          title="Delete permission"
                        >
                          <Trash2 size={14} />
                        </button>
                      {/if}
                    </div>
                  </td>
                </tr>
              {:else}
                <tr>
                  <td
                    colspan="3"
                    class="px-4 py-8 text-center text-slate-400 dark:text-slate-500 text-sm"
                  >
                    No permissions defined. Create one to start restricting
                    access.
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
        <div
          class="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
          onkeydown={(e) => {
            if (e.key === "Escape") editingUser = null;
          }}
          onclick={(e) => {
            if (e.target === e.currentTarget) editingUser = null;
          }}
        >
          <div
            class="bg-white dark:bg-warm-900 rounded-lg shadow-xl border border-slate-200 dark:border-warm-700 p-6 w-full max-w-2xl"
          >
            <h3
              class="text-sm font-semibold text-slate-800 dark:text-slate-100 mb-4"
            >
              Edit User: {editingUser.username}
              {#if editingUser.is_superadmin}
                <span
                  class="ml-2 text-[10px] px-1.5 py-0.5 bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-200 rounded font-medium"
                  >superadmin</span
                >
              {/if}
              {#if editingUser.external}
                <span
                  class="ml-2 text-[10px] px-1.5 py-0.5 bg-sky-100 text-sky-700 dark:bg-sky-900/40 dark:text-sky-200 rounded font-medium"
                  >external</span
                >
              {/if}
            </h3>

            <!-- Tabs inside modal -->
            <div
              class="flex gap-1 mb-4 border-b border-slate-200 dark:border-warm-700"
            >
              <button
                onclick={() => {
                  editTab = "details";
                }}
                class="px-3 py-1.5 text-xs font-medium border-b-2 transition-colors -mb-px {editTab ===
                'details'
                  ? 'border-accent-500 text-accent-700 dark:text-accent-300'
                  : 'border-transparent text-slate-400 dark:text-slate-500 hover:text-slate-600 dark:text-slate-300'}"
              >
                Details
              </button>
              <button
                onclick={() => {
                  editTab = "permissions";
                }}
                class="px-3 py-1.5 text-xs font-medium border-b-2 transition-colors -mb-px {editTab ===
                'permissions'
                  ? 'border-accent-500 text-accent-700 dark:text-accent-300'
                  : 'border-transparent text-slate-400 dark:text-slate-500 hover:text-slate-600 dark:text-slate-300'}"
              >
                Permissions
              </button>
              <button
                onclick={openAccessTab}
                class="px-3 py-1.5 text-xs font-medium border-b-2 transition-colors -mb-px {editTab ===
                'access'
                  ? 'border-accent-500 text-accent-700 dark:text-accent-300'
                  : 'border-transparent text-slate-400 dark:text-slate-500 hover:text-slate-600 dark:text-slate-300'}"
              >
                Access
              </button>
            </div>

            {#if editTab === "details"}
              <div class="space-y-3">
                <div>
                  <label
                    for="edit-username"
                    class="block text-xs text-slate-500 dark:text-slate-400 mb-1"
                    >Username</label
                  >
                  <input
                    id="edit-username"
                    type="text"
                    bind:value={editUsername}
                    class="w-full px-3 py-1.5 border border-slate-300 dark:border-warm-700 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent"
                  />
                </div>
                <div>
                  <label
                    for="edit-password"
                    class="block text-xs text-slate-500 dark:text-slate-400 mb-1"
                    >New Password (leave empty to keep current)</label
                  >
                  <input
                    id="edit-password"
                    type="password"
                    bind:value={editPassword}
                    class="w-full px-3 py-1.5 border border-slate-300 dark:border-warm-700 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent"
                    placeholder="New password"
                  />
                </div>
              </div>
            {:else if editTab === "permissions"}
              <!-- Permissions tab -->
              {#if editingUser.is_superadmin}
                <div
                  class="flex items-center gap-2 p-3 bg-amber-50 border border-amber-200 dark:bg-amber-900/30 dark:border-amber-700 rounded-lg text-xs text-amber-700 dark:text-amber-200"
                >
                  <ShieldCheck size={16} />
                  <span
                    >Superadmin users have all permissions automatically.</span
                  >
                </div>
              {:else if loadingUserPerms}
                <div
                  class="py-6 text-center text-sm text-slate-400 dark:text-slate-500"
                >
                  Loading permissions...
                </div>
              {:else if allPermissions.length === 0}
                <div
                  class="py-6 text-center text-sm text-slate-400 dark:text-slate-500"
                >
                  No permissions defined. Create permissions in the Permissions
                  tab first.
                </div>
              {:else}
                <div class="space-y-1 max-h-64 overflow-y-auto">
                  {#each allPermissions as perm (perm.id)}
                    {@const checked = editUserPermissionIds.includes(perm.id)}
                    <label
                      class="flex items-center gap-3 px-3 py-2 rounded-md hover:bg-slate-50 dark:hover:bg-warm-700 cursor-pointer transition-colors"
                    >
                      <input
                        type="checkbox"
                        {checked}
                        onchange={() => toggleEditPermission(perm.id)}
                        class="rounded border-slate-300 dark:border-warm-700 text-brand-500 focus:ring-brand-500"
                      />
                      <div class="flex-1 min-w-0">
                        <div class="flex items-center gap-2">
                          <Shield
                            size={12}
                            class="text-slate-400 dark:text-slate-500 shrink-0"
                          />
                          <code
                            class="text-xs font-medium text-slate-700 dark:text-slate-200"
                            >{perm.key}</code
                          >
                        </div>
                        <div
                          class="text-[11px] text-slate-400 dark:text-slate-500 mt-0.5"
                        >
                          {perm.name}{perm.description
                            ? ` — ${perm.description}`
                            : ""}
                        </div>
                      </div>
                    </label>
                  {/each}
                </div>
              {/if}
            {:else}
              <!-- Access tab: effective permissions (with provenance),
                   linked identities, and active sessions. Effective roles
                   are sourced from the user's live session(s); deny toggles
                   strip a single capability for this user only. -->
              {#if loadingAccess}
                <div
                  class="py-6 text-center text-sm text-slate-400 dark:text-slate-500"
                >
                  Loading access…
                </div>
              {:else if effectiveReport}
                {@const rep = effectiveReport}
                <div class="space-y-4 max-h-[60vh] overflow-y-auto pr-1">
                  <!-- Summary -->
                  <div class="flex flex-wrap items-center gap-2">
                    <span
                      class="inline-flex items-center gap-1.5 text-[11px] px-2 py-0.5 rounded-full {rep.online
                        ? 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300'
                        : 'bg-slate-100 text-slate-500 dark:bg-warm-800 dark:text-slate-400'}"
                    >
                      <span
                        class="w-1.5 h-1.5 rounded-full {rep.online
                          ? 'bg-green-500'
                          : 'bg-slate-400'}"
                      ></span>
                      {rep.online ? "online" : "offline"}
                    </span>
                    {#if rep.superadmin}
                      <span
                        class="text-[11px] px-2 py-0.5 rounded-full bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-200"
                        >superadmin ({rep.superadmin_reason})</span
                      >
                    {/if}
                    {#each rep.roles as role (role)}
                      <span
                        class="text-[11px] px-2 py-0.5 rounded-full bg-accent-50 text-accent-700 dark:bg-accent-900/40 dark:text-accent-200 font-mono"
                        >{role}</span
                      >
                    {/each}
                  </div>
                  {#if !rep.online}
                    <p class="text-[11px] text-slate-400 dark:text-slate-500">
                      User is offline — IdP roles are only shown while they
                      have an active session. DB-assigned bundles, superadmin
                      and deny still apply.
                    </p>
                  {/if}

                  <!-- Effective capabilities with source + deny toggle -->
                  <div>
                    <div
                      class="text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
                    >
                      Effective capabilities
                    </div>
                    <div class="space-y-1">
                      {#each knownKeys as cap (cap.key)}
                        {@const granted = rep.capabilities.includes(cap.key)}
                        {@const denied = editDeniedCaps.includes(cap.key)}
                        {@const srcs = rep.sources.filter(
                          (s) => s.capability === cap.key,
                        )}
                        <div
                          class="flex items-start gap-2 px-3 py-2 rounded-md bg-slate-50 dark:bg-warm-800"
                        >
                          <div class="flex-1 min-w-0">
                            <div class="flex items-center gap-2">
                              <code
                                class="text-xs font-medium {denied
                                  ? 'line-through text-slate-400 dark:text-slate-500'
                                  : 'text-slate-700 dark:text-slate-200'}"
                                >{cap.key}</code
                              >
                              {#if denied}
                                <span
                                  class="text-[10px] px-1.5 py-0.5 rounded bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300"
                                  >denied</span
                                >
                              {:else if granted}
                                <span
                                  class="text-[10px] px-1.5 py-0.5 rounded bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300"
                                  >granted</span
                                >
                              {:else}
                                <span
                                  class="text-[10px] px-1.5 py-0.5 rounded bg-slate-100 text-slate-400 dark:bg-warm-700 dark:text-slate-500"
                                  >—</span
                                >
                              {/if}
                            </div>
                            {#if srcs.length > 0}
                              <div
                                class="mt-0.5 flex flex-wrap gap-1 text-[10px] text-slate-400 dark:text-slate-500"
                              >
                                {#each srcs as s, i (i)}
                                  <span class="font-mono"
                                    >{sourceLabel(s)}</span
                                  >
                                {/each}
                              </div>
                            {/if}
                          </div>
                          {#if canManagePermissions && !(rep.superadmin && rep.superadmin_reason === "allowlist")}
                            <button
                              onclick={() => toggleDeny(cap.key)}
                              title={denied
                                ? "Allow this capability again"
                                : "Deny this capability for this user only"}
                              class="shrink-0 p-1 rounded transition-colors {denied
                                ? 'text-red-500 bg-red-50 dark:bg-red-900/30 hover:bg-red-100'
                                : 'text-slate-400 hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-900/30'}"
                            >
                              <Ban size={13} />
                            </button>
                          {/if}
                        </div>
                      {/each}
                    </div>
                    {#if rep.superadmin && rep.superadmin_reason === "allowlist"}
                      <p
                        class="mt-1 text-[10px] text-amber-600 dark:text-amber-400"
                      >
                        This user is in the Superadmins allowlist — deny does
                        not apply (break-glass).
                      </p>
                    {/if}
                  </div>

                  <!-- Linked identities -->
                  {#if userIdentities.length > 0}
                    <div>
                      <div
                        class="text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
                      >
                        Linked accounts
                      </div>
                      <div class="space-y-1">
                        {#each userIdentities as ident (ident.id)}
                          <div
                            class="flex items-center gap-2 px-3 py-1.5 rounded-md bg-slate-50 dark:bg-warm-800 text-xs"
                          >
                            <LinkIcon
                              size={12}
                              class="text-slate-400 shrink-0"
                            />
                            <span
                              class="font-medium text-slate-700 dark:text-slate-200"
                              >{ident.provider}</span
                            >
                            <span
                              class="font-mono text-[11px] text-slate-400 dark:text-slate-500 truncate"
                              >{ident.subject}</span
                            >
                          </div>
                        {/each}
                      </div>
                    </div>
                  {/if}

                  <!-- Active sessions -->
                  <div>
                    <div
                      class="text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
                    >
                      Active sessions ({userSessions.length})
                    </div>
                    {#if userSessions.length === 0}
                      <p
                        class="text-[11px] text-slate-400 dark:text-slate-500"
                      >
                        No active sessions.
                      </p>
                    {:else}
                      <div class="space-y-1">
                        {#each userSessions as sess (sess.handle)}
                          <div
                            class="flex items-center gap-2 px-3 py-1.5 rounded-md bg-slate-50 dark:bg-warm-800 text-xs"
                          >
                            <Monitor
                              size={12}
                              class="text-slate-400 shrink-0"
                            />
                            <span
                              class="font-medium text-slate-700 dark:text-slate-200"
                              >{sess.provider || "local"}</span
                            >
                            {#if sess.current}
                              <span
                                class="text-[10px] px-1.5 py-0.5 rounded bg-accent-50 text-accent-700 dark:bg-accent-900/40 dark:text-accent-200"
                                >this is you</span
                              >
                            {/if}
                            <span
                              class="text-[10px] text-slate-400 dark:text-slate-500 ml-auto"
                              >expires {new Date(
                                sess.expires_at,
                              ).toLocaleDateString()}</span
                            >
                            <button
                              onclick={() => revokeSession(sess.handle)}
                              title="Revoke this session"
                              class="shrink-0 p-1 rounded text-slate-400 hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-900/30 transition-colors"
                            >
                              <LogOut size={13} />
                            </button>
                          </div>
                        {/each}
                      </div>
                    {/if}
                  </div>
                </div>
              {:else}
                <div
                  class="py-6 text-center text-sm text-slate-400 dark:text-slate-500"
                >
                  No access information.
                </div>
              {/if}
            {/if}

            <div class="flex justify-end gap-2 mt-4">
              <button
                onclick={() => {
                  editingUser = null;
                }}
                class="px-4 py-1.5 bg-white dark:bg-warm-900 border border-slate-300 dark:border-warm-700 text-slate-600 dark:text-slate-300 text-sm rounded-md hover:bg-slate-50 dark:hover:bg-warm-700 transition-colors"
              >
                Cancel
              </button>
              <button
                onclick={handleSaveEdit}
                class="px-4 py-1.5 bg-accent-600 text-white text-sm rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
              >
                Save
              </button>
            </div>
          </div>
        </div>
      {/if}

      <!-- ========== EDIT PERMISSION MODAL ========== -->
      {#if editingPerm}
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div
          class="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
          onkeydown={(e) => {
            if (e.key === "Escape") editingPerm = null;
          }}
          onclick={(e) => {
            if (e.target === e.currentTarget) editingPerm = null;
          }}
        >
          <div
            class="bg-white dark:bg-warm-900 rounded-lg shadow-xl border border-slate-200 dark:border-warm-700 p-6 w-full max-w-lg"
          >
            <h3
              class="text-sm font-semibold text-slate-800 dark:text-slate-100 mb-4"
            >
              Edit Permission: {editingPerm.name}
            </h3>
            <div class="space-y-3">
              <div class="grid grid-cols-2 gap-3">
                <div>
                  <label
                    for="edit-perm-name"
                    class="block text-xs text-slate-500 dark:text-slate-400 mb-1"
                    >Name</label
                  >
                  <input
                    id="edit-perm-name"
                    type="text"
                    bind:value={editPermName}
                    class="w-full px-3 py-1.5 border border-slate-300 dark:border-warm-700 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent"
                  />
                </div>
                <div>
                  <label
                    for="edit-perm-key"
                    class="block text-xs text-slate-500 dark:text-slate-400 mb-1"
                    >Slug</label
                  >
                  <input
                    id="edit-perm-key"
                    type="text"
                    bind:value={editPermKey}
                    class="w-full px-3 py-1.5 border border-slate-300 dark:border-warm-700 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent"
                  />
                </div>
              </div>
              <div>
                <label
                  for="edit-perm-desc"
                  class="block text-xs text-slate-500 dark:text-slate-400 mb-1"
                  >Description</label
                >
                <input
                  id="edit-perm-desc"
                  type="text"
                  bind:value={editPermDesc}
                  class="w-full px-3 py-1.5 border border-slate-300 dark:border-warm-700 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent"
                />
              </div>
              <!-- Capability keys selector -->
              <div>
                <div class="text-xs text-slate-500 dark:text-slate-400 mb-2">
                  Capabilities granted:
                </div>
                <div class="grid grid-cols-2 gap-1.5 max-h-56 overflow-y-auto">
                  {#each knownKeys as known (known.key)}
                    {@const selected = editPermKeys.includes(known.key)}
                    <button
                      onclick={() => toggleEditPermKey(known.key)}
                      type="button"
                      class="flex items-start gap-2 px-2.5 py-2 border rounded-md transition-all text-left text-xs
 {selected
                        ? 'bg-accent-50 border-accent-300 dark:bg-accent-900/30 dark:border-accent-700'
                        : 'bg-white dark:bg-warm-900 border-slate-200 dark:border-warm-700 hover:border-slate-300 dark:border-warm-700'}"
                    >
                      <div
                        class="flex items-center justify-center w-3.5 h-3.5 mt-0.5 shrink-0 rounded border transition-colors
 {selected
                          ? 'bg-accent-500 border-accent-500'
                          : 'border-slate-300 dark:border-warm-700'}"
                      >
                        {#if selected}<Check size={9} class="text-white" />{/if}
                      </div>
                      <div class="min-w-0">
                        <code
                          class="font-medium {selected
                            ? 'text-accent-700 dark:text-accent-300'
                            : 'text-slate-600 dark:text-slate-300'}"
                          >{known.key}</code
                        >
                      </div>
                    </button>
                  {/each}
                </div>
              </div>

              <!-- Path scoping editor (mirrors the create form) -->
              {#if editPermKeys.length > 0}
                <div
                  class="border-t border-slate-200 dark:border-warm-700 pt-3"
                >
                  <div class="flex items-baseline justify-between mb-1">
                    <div
                      class="text-xs font-medium text-slate-600 dark:text-slate-300"
                    >
                      Path scoping <span
                        class="text-slate-400 dark:text-slate-500 font-normal"
                        >(optional)</span
                      >
                    </div>
                    <button
                      type="button"
                      onclick={() => {
                        showEditPatternHelp = !showEditPatternHelp;
                      }}
                      class="text-[10px] text-brand-600 hover:underline"
                    >
                      {showEditPatternHelp
                        ? "Hide help"
                        : "How do patterns work?"}
                    </button>
                  </div>

                  {#if showEditPatternHelp}
                    <div
                      class="mb-2 p-2.5 bg-brand-50 border border-brand-200 dark:bg-brand-900/30 dark:border-brand-700 rounded text-[11px] text-slate-700 dark:text-slate-200 space-y-1.5 leading-relaxed"
                    >
                      <div>
                        Patterns match the <strong>storage key</strong> — the
                        part after <code>/api/v1/file/</code> or
                        <code>/api/v1/folder/</code>. No leading slash. No
                        implicit prefix: a file stored as
                        <code>team-a/app.yaml</code>
                        is matched as <code>team-a/app.yaml</code>. Restrictions
                        only apply to the <code>files.*</code> capabilities; admin
                        caps (users, tokens, settings) ignore patterns.
                      </div>
                      <div>
                        <span class="font-medium">Glob syntax:</span>
                        <code>*</code> = one path segment ·
                        <code>**</code> = any number of segments (including
                        zero) ·
                        <code>?</code> = one character ·
                        <code>[abc]</code> = character class.
                      </div>
                      <div>
                        <span class="font-medium">Examples:</span>
                        <ul class="ml-4 list-disc space-y-0.5 mt-1">
                          <li>
                            <code>team-a/**</code> — anything under
                            <code>team-a/</code> (any depth)
                          </li>
                          <li>
                            <code>**/*.yaml</code> — every yaml file at any depth
                          </li>
                          <li>
                            <code>apps/*/config.yaml</code> —
                            <code>config.yaml</code>
                            in any direct child of <code>apps/</code>
                          </li>
                          <li>
                            <code>shared</code> — only the literal name
                            <code>shared</code>, no descendants
                          </li>
                          <li>
                            <code>prod/**</code> + <code>staging/**</code> — multiple
                            patterns are OR'd
                          </li>
                        </ul>
                      </div>
                      <div class="text-slate-500 dark:text-slate-400">
                        Empty list = unrestricted (default). For folder
                        listings, parent directories of a matched path are
                        allowed automatically so users can navigate.
                        <code>..</code> segments are rejected.
                      </div>
                    </div>
                  {/if}

                  <div class="space-y-2 max-h-56 overflow-y-auto">
                    {#each editPermKeys as k (k)}
                      {@const patterns = editPermPatterns[k] ?? []}
                      {@const scopable = k.startsWith("files.")}
                      <div
                        class="border border-slate-200 dark:border-warm-700 rounded-md p-2 bg-slate-50 dark:bg-warm-800"
                      >
                        <div class="flex items-center justify-between mb-1.5">
                          <div class="flex items-center gap-2">
                            <code
                              class="text-[11px] font-medium text-slate-700 dark:text-slate-200"
                              >{k}</code
                            >
                            {#if !scopable}
                              <span
                                class="text-[9px] text-slate-400 dark:text-slate-500"
                                >(patterns ignored — not a path-bound
                                capability)</span
                              >
                            {/if}
                          </div>
                          {#if scopable}
                            <button
                              type="button"
                              onclick={() => addPattern("edit", k)}
                              class="text-[10px] text-brand-600 hover:text-accent-700 dark:text-accent-300 hover:underline"
                              >+ Add pattern</button
                            >
                          {/if}
                        </div>
                        {#if !scopable}
                          <div
                            class="text-[10px] text-slate-400 dark:text-slate-500 italic"
                          >
                            applies globally
                          </div>
                        {:else if patterns.length === 0}
                          <div
                            class="text-[10px] text-slate-400 dark:text-slate-500 italic"
                          >
                            all paths
                          </div>
                        {:else}
                          <div class="space-y-1">
                            {#each patterns as pat, i}
                              <div class="flex gap-1 items-center">
                                <input
                                  type="text"
                                  value={pat}
                                  oninput={(e) =>
                                    updatePattern(
                                      "edit",
                                      k,
                                      i,
                                      e.currentTarget.value,
                                    )}
                                  placeholder="e.g. team-a/**"
                                  class="flex-1 px-2 py-1 border border-slate-300 dark:border-warm-700 rounded text-[11px] font-mono focus:outline-none focus:ring-1 focus:ring-brand-500"
                                />
                                <button
                                  type="button"
                                  onclick={() => removePattern("edit", k, i)}
                                  class="p-1 text-slate-400 dark:text-slate-500 hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-900/30 rounded transition-colors"
                                  title="Remove pattern"
                                >
                                  <Trash2 size={12} />
                                </button>
                              </div>
                            {/each}
                          </div>
                        {/if}
                      </div>
                    {/each}
                  </div>
                </div>
              {/if}
            </div>
            <div class="flex justify-end gap-2 mt-4">
              <button
                onclick={() => {
                  editingPerm = null;
                }}
                class="px-4 py-1.5 bg-white dark:bg-warm-900 border border-slate-300 dark:border-warm-700 text-slate-600 dark:text-slate-300 text-sm rounded-md hover:bg-slate-50 dark:hover:bg-warm-700 transition-colors"
                >Cancel</button
              >
              <button
                onclick={handleSaveEditPerm}
                class="px-4 py-1.5 bg-accent-600 text-white text-sm rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
                >Save</button
              >
            </div>
          </div>
        </div>
      {/if}
    {/if}
  </div>
</div>
