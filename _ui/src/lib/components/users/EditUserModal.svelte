<!--
  EditUserModal — details / permissions / access tabs for a single user.
  Mounted by the Users page only while a user is being edited; all
  per-edit state lives here and is reset by remounting.
-->
<script lang="ts">
  import { untrack } from "svelte";
  import { Ban, LogOut, Link as LinkIcon, Monitor, Shield, ShieldCheck } from "lucide-svelte";
  import Modal from "@/lib/components/Modal.svelte";
  import { apiServerMessage } from "@/lib/api/client";
  import {
    appStore,
    type CapSource,
    type EffectiveReport,
    type PermissionInfo,
    type SessionView,
    type UserIdentity,
    type UserInfo,
  } from "@/lib/store/store.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import type { KnownCapability } from "./userQuery.svelte";

  type Props = {
    user: UserInfo;
    knownKeys: KnownCapability[];
    canManagePermissions: boolean;
    onClose: () => void;
  };

  let { user, knownKeys, canManagePermissions, onClose }: Props = $props();

  const allPermissions = $derived(appStore.permissions);

  let editPassword = $state("");
  let editUsername = $state(untrack(() => user.username));
  let editTab = $state<"details" | "permissions" | "access">("details");
  let editUserPermissionIds = $state<string[]>([]);
  let loadingUserPerms = $state(true);

  // Access tab (effective permissions + sessions + per-user deny).
  let effectiveReport = $state<EffectiveReport | null>(null);
  let userSessions = $state<SessionView[]>([]);
  let userIdentities = $state<UserIdentity[]>([]);
  let editDeniedCaps = $state<string[]>(
    untrack(() => user.denied_capabilities ?? []),
  );
  let loadingAccess = $state(false);
  let accessLoaded = $state(false);

  $effect(() => {
    const id = untrack(() => user.id);
    appStore
      .getUserPermissions(id)
      .then((perms) => {
        editUserPermissionIds = perms.map((p: PermissionInfo) => p.id);
      })
      .catch(() => {
        editUserPermissionIds = [];
      })
      .finally(() => {
        loadingUserPerms = false;
      });
  });

  // Lazily load the Access tab payload (effective perms + sessions +
  // linked identities) the first time the tab is opened for a user.
  async function openAccessTab() {
    editTab = "access";
    if (accessLoaded || loadingAccess) return;
    loadingAccess = true;
    try {
      const [rep, sess, idents] = await Promise.all([
        appStore.getUserEffectivePermissions(user.id),
        appStore.listUserSessions(user.id),
        appStore.getUserIdentities(user.id),
      ]);
      effectiveReport = rep;
      userSessions = sess;
      userIdentities = idents;
      editDeniedCaps = rep.denied ?? [];
      accessLoaded = true;
    } catch (err) {
      addToast(apiServerMessage(err, "Failed to load access info"), "alert");
    } finally {
      loadingAccess = false;
    }
  }

  // Toggle a single capability into/out of the per-user deny overlay.
  // Persists immediately (separate from the modal Save) and re-resolves so
  // the effective set reflects the change. Gated by permissions.manage.
  async function toggleDeny(capKey: string) {
    const next = editDeniedCaps.includes(capKey)
      ? editDeniedCaps.filter((k) => k !== capKey)
      : [...editDeniedCaps, capKey];
    try {
      await appStore.setUserDeniedPermissions(user.id, next);
      effectiveReport = await appStore.getUserEffectivePermissions(user.id);
      editDeniedCaps = effectiveReport.denied ?? next;
      addToast("Deny updated — applies on the user's next request", "success");
    } catch (err) {
      addToast(apiServerMessage(err, "Failed to update deny"), "alert");
    }
  }

  async function revokeSession(handle: string) {
    try {
      await appStore.revokeUserSession(user.id, handle);
      userSessions = userSessions.filter((s) => s.handle !== handle);
      addToast("Session revoked", "success");
      await appStore.loadUsers();
    } catch (err) {
      addToast(apiServerMessage(err, "Failed to revoke session"), "alert");
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
      editUserPermissionIds = editUserPermissionIds.filter((id) => id !== permId);
    } else {
      editUserPermissionIds = [...editUserPermissionIds, permId];
    }
  }

  async function handleSaveEdit() {
    try {
      // Save user details if changed
      const updates: { username?: string; password?: string } = {};
      if (editUsername && editUsername !== user.username) {
        updates.username = editUsername;
      }
      if (editPassword) {
        updates.password = editPassword;
      }
      if (Object.keys(updates).length > 0) {
        await appStore.updateUser(user.id, updates);
      }

      // Save permissions
      if (!user.is_superadmin) {
        await appStore.setUserPermissions(user.id, editUserPermissionIds);
      }

      addToast("User updated", "success");
      onClose();
    } catch (err) {
      addToast(apiServerMessage(err, "Failed to update user"), "alert");
    }
  }

  const inputClass =
    "w-full px-3 py-1.5 border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-accent-500 focus:border-transparent";
  const tabClass = (active: boolean) =>
    `px-3 py-1.5 text-xs font-medium border-b-2 transition-colors -mb-px cursor-pointer ${
      active
        ? "border-accent-500 text-accent-700 dark:text-accent-300"
        : "border-transparent text-slate-400 dark:text-slate-500 hover:text-slate-600 dark:hover:text-slate-300"
    }`;
</script>

<Modal
  open={true}
  {onClose}
  labelledby="edit-user-title"
  panelClass="rounded-lg p-6 w-full max-w-2xl"
>
  <h3
    id="edit-user-title"
    class="text-sm font-semibold text-slate-800 dark:text-slate-100 mb-4"
  >
    Edit User: {user.username}
    {#if user.is_superadmin}
      <span
        class="ml-2 text-[10px] px-1.5 py-0.5 bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-200 rounded font-medium"
        >superadmin</span
      >
    {/if}
    {#if user.external}
      <span
        class="ml-2 text-[10px] px-1.5 py-0.5 bg-sky-100 text-sky-700 dark:bg-sky-900/40 dark:text-sky-200 rounded font-medium"
        >external</span
      >
    {/if}
  </h3>

  <!-- Tabs inside modal -->
  <div
    class="flex gap-1 mb-4 border-b border-slate-200 dark:border-warm-700"
    role="tablist"
  >
    <button
      role="tab"
      aria-selected={editTab === "details"}
      onclick={() => {
        editTab = "details";
      }}
      class={tabClass(editTab === "details")}
    >
      Details
    </button>
    <button
      role="tab"
      aria-selected={editTab === "permissions"}
      onclick={() => {
        editTab = "permissions";
      }}
      class={tabClass(editTab === "permissions")}
    >
      Permissions
    </button>
    <button
      role="tab"
      aria-selected={editTab === "access"}
      onclick={openAccessTab}
      class={tabClass(editTab === "access")}
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
          class={inputClass}
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
          class={inputClass}
          placeholder="New password"
        />
      </div>
    </div>
  {:else if editTab === "permissions"}
    <!-- Permissions tab -->
    {#if user.is_superadmin}
      <div
        class="flex items-center gap-2 p-3 bg-amber-50 border border-amber-200 dark:bg-amber-950/40 dark:border-amber-700 rounded-lg text-xs text-amber-700 dark:text-amber-200"
      >
        <ShieldCheck size={16} />
        <span>Superadmin users have all permissions automatically.</span>
      </div>
    {:else if loadingUserPerms}
      <div class="py-6 text-center text-sm text-slate-400 dark:text-slate-500">
        Loading permissions...
      </div>
    {:else if allPermissions.length === 0}
      <div class="py-6 text-center text-sm text-slate-400 dark:text-slate-500">
        No permissions defined. Create permissions in the Permissions tab
        first.
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
              class="rounded border-slate-300 dark:border-warm-600 text-accent-500 focus:ring-accent-500"
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
              <div class="text-[11px] text-slate-400 dark:text-slate-500 mt-0.5">
                {perm.name}{perm.description ? ` — ${perm.description}` : ""}
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
      <div class="py-6 text-center text-sm text-slate-400 dark:text-slate-500">
        Loading access…
      </div>
    {:else if effectiveReport}
      {@const rep = effectiveReport}
      <div class="space-y-4 max-h-[60vh] overflow-y-auto pr-1">
        <!-- Summary -->
        <div class="flex flex-wrap items-center gap-2">
          <span
            class="inline-flex items-center gap-1.5 text-[11px] px-2 py-0.5 rounded-full {rep.online
              ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
              : 'bg-slate-100 text-slate-500 dark:bg-warm-900 dark:text-slate-400'}"
          >
            <span
              class="w-1.5 h-1.5 rounded-full {rep.online
                ? 'bg-emerald-500'
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
            User is offline — IdP roles are only shown while they have an
            active session. DB-assigned bundles, superadmin and deny still
            apply.
          </p>
        {/if}

        <!-- Effective capabilities with source + deny toggle -->
        <div>
          <div class="text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">
            Effective capabilities
          </div>
          <div class="space-y-1">
            {#each knownKeys as cap (cap.key)}
              {@const granted = rep.capabilities.includes(cap.key)}
              {@const denied = editDeniedCaps.includes(cap.key)}
              {@const srcs = rep.sources.filter((s) => s.capability === cap.key)}
              <div
                class="flex items-start gap-2 px-3 py-2 rounded-md bg-slate-50 dark:bg-warm-900"
              >
                <div class="flex-1 min-w-0">
                  <div class="flex items-center gap-2">
                    <code
                      class="text-xs font-medium {denied
                        ? 'line-through text-slate-400 dark:text-slate-500'
                        : 'text-slate-700 dark:text-slate-200'}">{cap.key}</code
                    >
                    {#if denied}
                      <span
                        class="text-[10px] px-1.5 py-0.5 rounded bg-vermilion-100 text-vermilion-700 dark:bg-vermilion-900/40 dark:text-vermilion-300"
                        >denied</span
                      >
                    {:else if granted}
                      <span
                        class="text-[10px] px-1.5 py-0.5 rounded bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300"
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
                        <span class="font-mono">{sourceLabel(s)}</span>
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
                    aria-label={denied
                      ? `Allow ${cap.key} again`
                      : `Deny ${cap.key} for this user`}
                    aria-pressed={denied}
                    class="shrink-0 p-1 rounded transition-colors cursor-pointer {denied
                      ? 'text-vermilion-500 bg-vermilion-50 dark:bg-vermilion-900/40 hover:bg-vermilion-100 dark:hover:bg-vermilion-900/60'
                      : 'text-slate-400 hover:text-vermilion-500 hover:bg-vermilion-50 dark:hover:bg-vermilion-900/40'}"
                  >
                    <Ban size={13} />
                  </button>
                {/if}
              </div>
            {/each}
          </div>
          {#if rep.superadmin && rep.superadmin_reason === "allowlist"}
            <p class="mt-1 text-[10px] text-amber-600 dark:text-amber-400">
              This user is in the Superadmins allowlist — deny does not apply
              (break-glass).
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
                  class="flex items-center gap-2 px-3 py-1.5 rounded-md bg-slate-50 dark:bg-warm-900 text-xs"
                >
                  <LinkIcon size={12} class="text-slate-400 shrink-0" />
                  <span class="font-medium text-slate-700 dark:text-slate-200"
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
          <div class="text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5">
            Active sessions ({userSessions.length})
          </div>
          {#if userSessions.length === 0}
            <p class="text-[11px] text-slate-400 dark:text-slate-500">
              No active sessions.
            </p>
          {:else}
            <div class="space-y-1">
              {#each userSessions as sess (sess.handle)}
                <div
                  class="flex items-center gap-2 px-3 py-1.5 rounded-md bg-slate-50 dark:bg-warm-900 text-xs"
                >
                  <Monitor size={12} class="text-slate-400 shrink-0" />
                  <span class="font-medium text-slate-700 dark:text-slate-200"
                    >{sess.provider || "local"}</span
                  >
                  {#if sess.current}
                    <span
                      class="text-[10px] px-1.5 py-0.5 rounded bg-accent-50 text-accent-700 dark:bg-accent-900/40 dark:text-accent-200"
                      >this is you</span
                    >
                  {/if}
                  <span class="text-[10px] text-slate-400 dark:text-slate-500 ml-auto"
                    >expires {new Date(sess.expires_at).toLocaleDateString()}</span
                  >
                  <button
                    onclick={() => revokeSession(sess.handle)}
                    title="Revoke this session"
                    aria-label="Revoke this session"
                    class="shrink-0 p-1 rounded text-slate-400 hover:text-vermilion-500 hover:bg-vermilion-50 dark:hover:bg-vermilion-900/40 transition-colors cursor-pointer"
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
      <div class="py-6 text-center text-sm text-slate-400 dark:text-slate-500">
        No access information.
      </div>
    {/if}
  {/if}

  <div class="flex justify-end gap-2 mt-4">
    <button
      onclick={onClose}
      class="px-4 py-1.5 bg-white dark:bg-warm-800 border border-slate-300 dark:border-warm-600 text-slate-600 dark:text-slate-300 text-sm rounded-md hover:bg-slate-50 dark:hover:bg-warm-700 transition-colors cursor-pointer"
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
</Modal>
