<script lang="ts">
  import { KeyRound, Trash2, Users as UsersIcon } from "lucide-svelte";
  import { apiServerMessage } from "@/lib/api/client";
  import { appStore, type PermissionInfo } from "@/lib/store/store.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import PermissionPatternEditor from "./PermissionPatternEditor.svelte";
  import { cleanPatterns, type KnownCapability } from "./userQuery.svelte";

  type Props = {
    knownKeys: KnownCapability[];
    showCreateForm: boolean;
    canManageUsers: boolean;
    onEdit: (perm: PermissionInfo) => void;
    onViewUsers: (permissionId: string) => void;
  };

  let {
    knownKeys,
    showCreateForm = $bindable(),
    canManageUsers,
    onEdit,
    onViewUsers,
  }: Props = $props();

  const allPermissions = $derived(appStore.permissions);

  let newPermKey = $state("");
  let newPermName = $state("");
  let newPermDesc = $state("");
  let newPermKeys = $state<string[]>([]);
  let newPermPatterns = $state<Record<string, string[]>>({});
  let creatingPerm = $state(false);
  let confirmDeletePermId = $state<string | null>(null);

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
      showCreateForm = false;
    } catch (err) {
      addToast(apiServerMessage(err, "Failed to create permission"), "alert");
    } finally {
      creatingPerm = false;
    }
  }

  async function handleDeletePerm(id: string) {
    try {
      await appStore.deletePermission(id);
      addToast("Permission deleted", "success");
      confirmDeletePermId = null;
    } catch (err) {
      addToast(apiServerMessage(err, "Failed to delete permission"), "alert");
    }
  }

  const inputClass =
    "w-full px-3 py-1.5 border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-accent-500 focus:border-transparent";
  const iconBtn =
    "p-1.5 text-slate-400 dark:text-slate-500 rounded transition-colors cursor-pointer";
</script>

<!-- Create Permission Form -->
{#if showCreateForm}
  <div
    class="bg-white dark:bg-warm-800 rounded-lg border border-slate-200 dark:border-warm-700 p-4 mb-4"
  >
    <h3 class="text-sm font-medium text-slate-700 dark:text-slate-200 mb-3">
      Create Permission
    </h3>
    <div class="grid grid-cols-2 gap-3 mb-3">
      <div>
        <label
          for="new-perm-name"
          class="block text-xs text-slate-500 dark:text-slate-400 mb-1">Name</label
        >
        <input
          id="new-perm-name"
          type="text"
          bind:value={newPermName}
          class={inputClass}
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
          class={inputClass}
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
        class={inputClass}
        placeholder="What this permission grants"
      />
    </div>

    <PermissionPatternEditor
      {knownKeys}
      bind:keys={newPermKeys}
      bind:patterns={newPermPatterns}
      variant="create"
    />

    <div class="flex items-center gap-2">
      <button
        onclick={handleCreatePerm}
        disabled={creatingPerm || !newPermName || newPermKeys.length === 0}
        class="px-4 py-1.5 bg-accent-600 text-white text-sm rounded-md hover:bg-accent-700 disabled:opacity-50 transition-colors cursor-pointer disabled:cursor-not-allowed"
      >
        {creatingPerm
          ? "Creating..."
          : `Create (${newPermKeys.length} capabilities)`}
      </button>
      <button
        onclick={() => {
          showCreateForm = false;
          newPermKeys = [];
          newPermPatterns = {};
        }}
        class="px-4 py-1.5 bg-white dark:bg-warm-800 border border-slate-300 dark:border-warm-600 text-slate-600 dark:text-slate-300 text-sm rounded-md hover:bg-slate-50 dark:hover:bg-warm-700 transition-colors cursor-pointer"
      >
        Cancel
      </button>
    </div>
  </div>
{/if}

<!-- Permissions Table -->
<div
  class="bg-white dark:bg-warm-800 rounded-lg border border-slate-200 dark:border-warm-700 overflow-hidden"
>
  <table class="w-full text-sm">
    <thead>
      <tr
        class="bg-slate-50 dark:bg-warm-900 border-b border-slate-200 dark:border-warm-700"
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
              <div class="text-[11px] text-slate-400 dark:text-slate-500 mt-0.5">
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
                <span class="text-xs text-slate-400 dark:text-slate-500">none</span>
              {/each}
            </div>
          </td>
          <td class="px-4 py-3 text-right">
            <div class="flex items-center justify-end gap-1">
              {#if canManageUsers}
                <!-- Jump to the Users tab pre-filtered to users that
                     hold this permission. Saves a manual dropdown trip. -->
                <button
                  onclick={() => onViewUsers(perm.id)}
                  class="{iconBtn} hover:text-accent-600 hover:bg-accent-50 dark:hover:text-accent-400 dark:hover:bg-warm-600"
                  title="View users with this permission"
                  aria-label="View users with permission {perm.name}"
                >
                  <UsersIcon size={14} />
                </button>
              {/if}
              <button
                onclick={() => onEdit(perm)}
                class="{iconBtn} hover:text-accent-600 hover:bg-accent-50 dark:hover:text-accent-400 dark:hover:bg-warm-600"
                title="Edit permission"
                aria-label="Edit permission {perm.name}"
              >
                <KeyRound size={14} />
              </button>
              {#if confirmDeletePermId === perm.id}
                <button
                  onclick={() => handleDeletePerm(perm.id)}
                  class="px-2 py-1 text-xs bg-vermilion-600 text-white rounded hover:bg-vermilion-700 transition-colors cursor-pointer"
                  >Confirm</button
                >
                <button
                  onclick={() => {
                    confirmDeletePermId = null;
                  }}
                  class="px-2 py-1 text-xs bg-slate-200 dark:bg-warm-700 text-slate-600 dark:text-warm-200 rounded hover:bg-slate-300 dark:hover:bg-warm-600 transition-colors cursor-pointer"
                  >Cancel</button
                >
              {:else}
                <button
                  onclick={() => {
                    confirmDeletePermId = perm.id;
                  }}
                  class="{iconBtn} hover:text-vermilion-600 hover:bg-vermilion-50 dark:hover:text-vermilion-400 dark:hover:bg-warm-600"
                  title="Delete permission"
                  aria-label="Delete permission {perm.name}"
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
            No permissions defined. Create one to start restricting access.
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>
