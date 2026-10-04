<script lang="ts">
  import { untrack } from "svelte";
  import Modal from "@/lib/components/Modal.svelte";
  import { apiServerMessage } from "@/lib/api/client";
  import { appStore, type PermissionInfo } from "@/lib/store/store.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import PermissionPatternEditor from "./PermissionPatternEditor.svelte";
  import {
    cleanPatterns,
    patternsEqual,
    type KnownCapability,
  } from "./userQuery.svelte";

  type Props = {
    perm: PermissionInfo;
    knownKeys: KnownCapability[];
    onClose: () => void;
  };

  let { perm, knownKeys, onClose }: Props = $props();

  // Snapshot the permission on mount; edits never touch the cached copy.
  const initial = untrack(() => perm);
  let editPermKey = $state(initial.key);
  let editPermName = $state(initial.name);
  let editPermDesc = $state(initial.description);
  let editPermKeys = $state<string[]>([...(initial.keys || [])]);
  // Deep-copy patterns so edits don't mutate the cached PermissionInfo.
  let editPermPatterns = $state<Record<string, string[]>>(
    Object.fromEntries(
      Object.entries(initial.key_patterns ?? {}).map(([k, v]) => [k, [...v]]),
    ),
  );

  async function handleSaveEditPerm() {
    try {
      const updates: {
        key?: string;
        name?: string;
        description?: string;
        keys?: string[];
        key_patterns?: Record<string, string[]>;
      } = {};
      if (editPermKey !== perm.key) updates.key = editPermKey;
      if (editPermName !== perm.name) updates.name = editPermName;
      if (editPermDesc !== perm.description) updates.description = editPermDesc;
      // Always send keys to allow updating the capability set
      const origKeys = [...(perm.keys || [])].sort().join(",");
      const newKeys = [...editPermKeys].sort().join(",");
      if (origKeys !== newKeys) updates.keys = editPermKeys;

      // Send patterns whenever they changed (including transitions to empty —
      // that's how the user clears all path scoping for a permission).
      const cleaned = cleanPatterns(editPermPatterns, editPermKeys);
      if (!patternsEqual(cleaned, perm.key_patterns)) {
        // Backend treats `key_patterns: {}` as "replace with empty",
        // i.e. clear all patterns. Send an empty object explicitly so a
        // user who removes all patterns gets that effect.
        updates.key_patterns = cleaned ?? {};
      }

      if (Object.keys(updates).length > 0) {
        await appStore.updatePermission(perm.id, updates);
        addToast("Permission updated", "success");
      }
      onClose();
    } catch (err) {
      addToast(apiServerMessage(err, "Failed to update permission"), "alert");
    }
  }

  const inputClass =
    "w-full px-3 py-1.5 border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-accent-500 focus:border-transparent";
</script>

<Modal
  open={true}
  {onClose}
  labelledby="edit-perm-title"
  panelClass="rounded-lg p-6 w-full max-w-lg"
>
  <h3
    id="edit-perm-title"
    class="text-sm font-semibold text-slate-800 dark:text-slate-100 mb-4"
  >
    Edit Permission: {perm.name}
  </h3>
  <div class="space-y-3">
    <div class="grid grid-cols-2 gap-3">
      <div>
        <label
          for="edit-perm-name"
          class="block text-xs text-slate-500 dark:text-slate-400 mb-1">Name</label
        >
        <input
          id="edit-perm-name"
          type="text"
          bind:value={editPermName}
          class={inputClass}
        />
      </div>
      <div>
        <label
          for="edit-perm-key"
          class="block text-xs text-slate-500 dark:text-slate-400 mb-1">Slug</label
        >
        <input
          id="edit-perm-key"
          type="text"
          bind:value={editPermKey}
          class={inputClass}
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
        class={inputClass}
      />
    </div>
    <PermissionPatternEditor
      {knownKeys}
      bind:keys={editPermKeys}
      bind:patterns={editPermPatterns}
      variant="edit"
    />
  </div>
  <div class="flex justify-end gap-2 mt-4">
    <button
      onclick={onClose}
      class="px-4 py-1.5 bg-white dark:bg-warm-800 border border-slate-300 dark:border-warm-600 text-slate-600 dark:text-slate-300 text-sm rounded-md hover:bg-slate-50 dark:hover:bg-warm-700 transition-colors cursor-pointer"
      >Cancel</button
    >
    <button
      onclick={handleSaveEditPerm}
      class="px-4 py-1.5 bg-accent-600 text-white text-sm rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
      >Save</button
    >
  </div>
</Modal>
