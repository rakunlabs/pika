<!--
  PermissionMappingEditor — role → permissions / scope → permissions
  editor used by the Capabilities card in Authentication settings. Each
  row maps an external role (or OAuth2 scope) name to a set of pika
  Permission bundle keys.
-->
<script lang="ts">
  import type { Snippet } from "svelte";
  import { Plus, Trash2 } from "lucide-svelte";
  import type { PermissionInfo } from "@/lib/store/store.svelte";
  import type { MappingRow } from "./types";

  type Props = {
    rows: MappingRow[];
    availablePermissions: PermissionInfo[];
    /** Visible label, e.g. "Role → Permissions". */
    label: string;
    /** Noun used for buttons/placeholders, e.g. "role" or "scope". */
    noun: string;
    placeholder: string;
    /** Shown when there are no rows; omit to render nothing. */
    emptyText?: string;
    inputId: string;
    description: Snippet;
  };

  let {
    rows = $bindable(),
    availablePermissions,
    label,
    noun,
    placeholder,
    emptyText,
    inputId,
    description,
  }: Props = $props();

  let newKey = $state("");

  function addRow() {
    const k = newKey.trim();
    if (!k) return;
    if (rows.some((r) => r.key === k)) return;
    rows = [...rows, { key: k, permissions: [] }];
    newKey = "";
  }

  function removeRow(i: number) {
    rows = rows.filter((_, idx) => idx !== i);
  }

  function togglePerm(rowIdx: number, permKey: string) {
    rows = rows.map((r, i) => {
      if (i !== rowIdx) return r;
      const has = r.permissions.includes(permKey);
      return {
        key: r.key,
        permissions: has
          ? r.permissions.filter((c) => c !== permKey)
          : [...r.permissions, permKey],
      };
    });
  }

  // Permission keys selected on a row that no longer correspond to an
  // existing bundle (e.g. the bundle was deleted/renamed, or this is a
  // legacy capability-key value from before the permission-based mapping).
  // Surfaced as removable chips so they aren't silently dropped on save and
  // the operator can re-point them at a real permission.
  function unknownPermKeys(row: MappingRow): string[] {
    return row.permissions.filter(
      (k) => !availablePermissions.some((p) => p.key === k),
    );
  }
</script>

<div class="pt-4 border-t border-slate-100 dark:border-warm-700">
  <label
    for={inputId}
    class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1"
    >{label}</label
  >
  <p class="text-[11px] text-slate-400 dark:text-slate-500 mb-2">
    {@render description()}
  </p>
  <div class="flex gap-2">
    <input
      id={inputId}
      type="text"
      bind:value={newKey}
      {placeholder}
      onkeydown={(e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          addRow();
        }
      }}
      class="flex-1 px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
    />
    <button
      type="button"
      class="flex items-center gap-1 px-3 py-2 text-sm text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
      onclick={addRow}
    >
      <Plus size={13} />
      Add {noun}
    </button>
  </div>
  {#if rows.length === 0}
    {#if emptyText}
      <p class="mt-3 text-[11px] text-slate-400 dark:text-slate-500 italic">
        {emptyText}
      </p>
    {/if}
  {:else}
    <div class="mt-3 space-y-2">
      {#each rows as row, rowIdx}
        <div
          class="p-3 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
        >
          <div class="flex items-center justify-between mb-2 gap-2">
            <input
              type="text"
              bind:value={row.key}
              placeholder="{noun} name"
              aria-label="{noun} name"
              class="flex-1 px-2 py-1 text-xs font-mono bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 border border-slate-300 dark:border-warm-600 rounded focus:outline-none focus:ring-2 focus:ring-accent-500"
            />
            <button
              type="button"
              class="p-1 text-slate-400 dark:text-slate-500 hover:text-vermilion-500 transition-colors cursor-pointer"
              onclick={() => removeRow(rowIdx)}
              aria-label="Remove {noun} mapping"
              title="Remove {noun} mapping"
            >
              <Trash2 size={13} />
            </button>
          </div>
          {#if availablePermissions.length === 0}
            <p class="text-[11px] text-amber-600 dark:text-amber-400">
              No permissions defined yet. Create permissions in the Permissions
              section first, then assign them here.
            </p>
          {:else}
            <div class="grid grid-cols-2 gap-x-3 gap-y-1">
              {#each availablePermissions as perm}
                <label
                  class="flex items-start gap-2 text-xs text-slate-700 dark:text-slate-200 cursor-pointer"
                >
                  <input
                    type="checkbox"
                    checked={row.permissions.includes(perm.key)}
                    onchange={() => togglePerm(rowIdx, perm.key)}
                    class="mt-0.5 rounded border-slate-300 dark:border-warm-600"
                  />
                  <span class="leading-tight">
                    <span class="font-medium">{perm.name}</span>
                    <span
                      class="block text-[10px] text-slate-400 dark:text-slate-500 font-mono"
                      >{perm.key}</span
                    >
                  </span>
                </label>
              {/each}
            </div>
          {/if}
          {#if unknownPermKeys(row).length > 0}
            <div class="mt-2 flex flex-wrap gap-1">
              {#each unknownPermKeys(row) as uk}
                <button
                  type="button"
                  onclick={() => togglePerm(rowIdx, uk)}
                  title="Unknown permission — click to remove"
                  class="inline-flex items-center gap-1 px-1.5 py-0.5 text-[10px] font-mono rounded bg-amber-50 dark:bg-amber-950/40 text-amber-700 dark:text-amber-300 border border-amber-300 dark:border-amber-700 cursor-pointer"
                >
                  {uk}
                  <Trash2 size={10} />
                </button>
              {/each}
            </div>
          {/if}
        </div>
      {/each}
    </div>
  {/if}
</div>
