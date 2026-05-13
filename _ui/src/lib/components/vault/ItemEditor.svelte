<script lang="ts">
  import { Star, Archive, Trash2, Save, Plus, Eye, EyeOff, Copy, Check, GripVertical, X, Dices, RotateCcw } from 'lucide-svelte';
  import { vaultStore } from '@/lib/vault/store.svelte';
  import { newCustomField, extractHostnames, typeLabel } from '@/lib/vault/templates';
  import type { VaultItem, VaultItemType } from '@/lib/vault/api';
  import type { VaultItemField, VaultItemPayload } from '@/lib/vault/crypto';
  import { addToast } from '@/lib/store/toast.svelte';
  import TOTPDisplay from './TOTPDisplay.svelte';
  import PasswordGenerator from './PasswordGenerator.svelte';

  interface Props {
    item: VaultItem;
    /** Decrypted title plaintext (null when the AEAD failed). */
    title: string | null;
    /** Decrypted tag array (empty when absent or AEAD failed). */
    tagsCleartext: string[];
    /** Decrypted hostname array. */
    hostnamesCleartext: string[];
    payload: VaultItemPayload | null;
    onClose: () => void;
  }
  let {
    item,
    title: initialTitle,
    tagsCleartext,
    hostnamesCleartext,
    payload,
    onClose,
  }: Props = $props();

  // Editable working state. Deep-copied from props so cancel works.
  // The parent (Vault.svelte) wraps this component in `{#key ...}` on
  // item.id + version, so a new identity always remounts us fresh.
  // The "props only captured at init" warnings below are expected —
  // they're the form-working-state pattern, not bugs.
  /* eslint-disable svelte/no-reactive-reassign */
  // svelte-ignore state_referenced_locally
  let title = $state(initialTitle ?? '');
  // svelte-ignore state_referenced_locally
  let tags = $state<string[]>([...tagsCleartext]);
  let tagDraft = $state('');
  // svelte-ignore state_referenced_locally
  let favorite = $state(item.favorite ?? false);
  // svelte-ignore state_referenced_locally
  let fields = $state<VaultItemField[]>(payload ? deepCopyFields(payload.fields) : []);
  // svelte-ignore state_referenced_locally
  let notes = $state(payload?.notes ?? '');
  let busy = $state(false);
  let unmasked = $state<Set<string>>(new Set()); // field ids whose value is shown
  let generatorFor = $state<string | null>(null); // field id receiving generator output
  let copiedField = $state<string | null>(null);

  // Track the original hostnames so we don't trigger an unnecessary
  // re-encrypt if the URL fields haven't changed. The save path
  // recomputes them from the current `fields` and only sends the
  // updated list when it differs.
  // svelte-ignore state_referenced_locally
  const originalHostnames = [...hostnamesCleartext];

  // Tracks whether the form has unsaved changes.
  const dirty = $derived(
    title !== (initialTitle ?? '') ||
    JSON.stringify(tags) !== JSON.stringify(tagsCleartext) ||
    favorite !== (item.favorite ?? false) ||
    JSON.stringify(fields) !== JSON.stringify(payload?.fields ?? []) ||
    (notes ?? '') !== (payload?.notes ?? ''),
  );

  function deepCopyFields(f: VaultItemField[]): VaultItemField[] {
    return f.map(x => ({ ...x }));
  }

  function addTag() {
    const t = tagDraft.trim();
    if (!t) return;
    if (!tags.includes(t)) tags = [...tags, t];
    tagDraft = '';
  }
  function removeTag(t: string) {
    tags = tags.filter(x => x !== t);
  }
  function toggleMask(id: string) {
    if (unmasked.has(id)) unmasked.delete(id);
    else unmasked.add(id);
    unmasked = new Set(unmasked);
  }
  async function copyField(id: string, value: string) {
    if (!value) return;
    try {
      await navigator.clipboard.writeText(value);
      copiedField = id;
      setTimeout(() => (copiedField = null), 1500);
    } catch {
      addToast('Clipboard unavailable', 'warn');
    }
  }
  function addField() {
    fields = [...fields, newCustomField()];
  }
  function removeField(id: string) {
    fields = fields.filter(f => f.id !== id);
  }
  function moveField(id: string, delta: number) {
    const idx = fields.findIndex(f => f.id === id);
    if (idx < 0) return;
    const target = idx + delta;
    if (target < 0 || target >= fields.length) return;
    const copy = [...fields];
    [copy[idx], copy[target]] = [copy[target], copy[idx]];
    fields = copy;
  }
  function applyGenerator(value: string) {
    if (!generatorFor) return;
    fields = fields.map(f => (f.id === generatorFor ? { ...f, value } : f));
    generatorFor = null;
  }

  async function save() {
    if (busy) return;
    if (!title.trim()) {
      addToast('Title is required', 'alert');
      return;
    }
    busy = true;
    try {
      const newPayload: VaultItemPayload = { fields, notes: notes || undefined };
      // Recompute hostnames from the URL fields. We send them only
      // if they differ from the original — saves an unnecessary
      // re-encrypt on saves that only touched non-URL fields.
      const hostnames = extractHostnames(newPayload);
      const hostnamesChanged = JSON.stringify(hostnames) !== JSON.stringify(originalHostnames);
      await vaultStore.updateItem(
        item.id,
        { expected_version: item.version },
        {
          title: title.trim(),
          tags,
          urlHostnames: hostnamesChanged ? hostnames : undefined,
          favorite,
          payload: newPayload,
        },
      );
      addToast('Saved', 'success', 1500);
    } catch (e: any) {
      const status = e?.response?.status;
      if (status === 409) {
        addToast('This item was changed elsewhere. Reload to see the latest version.', 'alert');
      } else {
        addToast(e?.response?.data?.message ?? e?.message ?? 'Save failed', 'alert');
      }
    } finally {
      busy = false;
    }
  }

  async function trash() {
    busy = true;
    try {
      await vaultStore.softDeleteItem(item.id);
      addToast('Moved to trash', 'success', 1500);
      onClose();
    } catch (e: any) {
      addToast(e?.response?.data?.message ?? 'Delete failed', 'alert');
    } finally {
      busy = false;
    }
  }
  async function purge() {
    if (!confirm('Permanently delete this item? This cannot be undone.')) return;
    busy = true;
    try {
      await vaultStore.purgeItem(item.id);
      addToast('Deleted permanently', 'success', 1500);
      onClose();
    } catch (e: any) {
      addToast(e?.response?.data?.message ?? 'Delete failed', 'alert');
    } finally {
      busy = false;
    }
  }
  async function restore() {
    busy = true;
    try {
      await vaultStore.restoreItem(item.id);
      addToast('Restored', 'success', 1500);
      onClose();
    } catch (e: any) {
      addToast(e?.response?.data?.message ?? 'Restore failed', 'alert');
    } finally {
      busy = false;
    }
  }
  async function toggleArchive() {
    busy = true;
    try {
      await vaultStore.updateItem(
        item.id,
        { expected_version: item.version },
        { archived: !item.archived },
      );
      addToast(item.archived ? 'Unarchived' : 'Archived', 'success', 1500);
      onClose();
    } catch (e: any) {
      addToast(e?.response?.data?.message ?? 'Update failed', 'alert');
    } finally {
      busy = false;
    }
  }
</script>

<div class="flex flex-col h-full bg-white dark:bg-warm-950">
  <!-- Toolbar -->
  <div class="flex items-center gap-2 px-4 py-2 border-b border-slate-200 dark:border-warm-700">
    <div class="text-xs uppercase tracking-wider text-slate-400">{typeLabel(item.type as VaultItemType)}</div>
    <div class="flex-1"></div>
    <button
      onclick={() => (favorite = !favorite)}
      class="p-1.5 rounded hover:bg-slate-100 dark:hover:bg-warm-800 cursor-pointer"
      title={favorite ? 'Unfavorite' : 'Favorite'}
    >
      <Star size={14} fill={favorite ? 'currentColor' : 'none'} class={favorite ? 'text-amber-500' : 'text-slate-400'} />
    </button>
    {#if item.deleted_at}
      <button onclick={restore} disabled={busy} class="flex items-center gap-1 px-2 py-1 text-xs rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 cursor-pointer">
        <RotateCcw size={12} /> Restore
      </button>
      <button onclick={purge} disabled={busy} class="flex items-center gap-1 px-2 py-1 text-xs rounded bg-red-600 text-white hover:bg-red-700 cursor-pointer">
        <Trash2 size={12} /> Delete forever
      </button>
    {:else}
      <button onclick={toggleArchive} disabled={busy} class="flex items-center gap-1 px-2 py-1 text-xs rounded hover:bg-slate-100 dark:hover:bg-warm-800 cursor-pointer" title={item.archived ? 'Unarchive' : 'Archive'}>
        <Archive size={12} />
      </button>
      <button onclick={trash} disabled={busy} class="flex items-center gap-1 px-2 py-1 text-xs rounded hover:bg-slate-100 dark:hover:bg-warm-800 cursor-pointer" title="Move to trash">
        <Trash2 size={12} />
      </button>
      <button
        onclick={save}
        disabled={busy || !dirty}
        class="flex items-center gap-1 px-3 py-1 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
      >
        <Save size={12} /> Save
      </button>
    {/if}
  </div>

  <!-- Body -->
  <div class="flex-1 overflow-y-auto p-4 space-y-4 max-w-3xl">
    {#if !payload}
      <div class="bg-red-50 dark:bg-red-950/30 border border-red-300 dark:border-red-700 rounded p-3 text-sm text-red-700 dark:text-red-300">
        This item's encrypted payload could not be decrypted.
        It may have been encrypted under a different vault key (e.g. before a vault reset).
        You can still delete it.
      </div>
    {/if}

    <!-- Title -->
    <div>
      <label class="block text-[10px] font-medium uppercase tracking-wider text-slate-500 mb-1" for="vi-title">Title</label>
      <input
        id="vi-title"
        type="text"
        bind:value={title}
        disabled={!!item.deleted_at}
        class="w-full px-3 py-2 text-base font-medium rounded border border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-900 focus:outline-none focus:ring-2 focus:ring-accent-500 disabled:opacity-60"
      />
    </div>

    <!-- Tags -->
    <div>
      <label class="block text-[10px] font-medium uppercase tracking-wider text-slate-500 mb-1" for="vi-tag-draft">Tags</label>
      <div class="flex flex-wrap gap-1 mb-2">
        {#each tags as tag (tag)}
          <span class="inline-flex items-center gap-1 px-2 py-0.5 text-xs rounded bg-slate-100 dark:bg-warm-800 border border-slate-200 dark:border-warm-700">
            {tag}
            {#if !item.deleted_at}
              <button type="button" onclick={() => removeTag(tag)} class="hover:text-red-600 cursor-pointer" aria-label="Remove tag">
                <X size={10} />
              </button>
            {/if}
          </span>
        {/each}
      </div>
      {#if !item.deleted_at}
        <input
          id="vi-tag-draft"
          type="text"
          placeholder="Add a tag and press Enter"
          bind:value={tagDraft}
          onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addTag(); } }}
          class="w-full px-3 py-1.5 text-sm rounded border border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-900 focus:outline-none focus:ring-2 focus:ring-accent-500"
        />
      {/if}
    </div>

    <!-- Fields -->
    {#if payload}
      <div class="space-y-2">
        <div class="flex items-center justify-between">
          <div class="text-[10px] font-medium uppercase tracking-wider text-slate-500">Fields</div>
          {#if !item.deleted_at}
            <button type="button" onclick={addField} class="flex items-center gap-1 px-2 py-1 text-xs rounded hover:bg-slate-100 dark:hover:bg-warm-800 cursor-pointer">
              <Plus size={12} /> Add field
            </button>
          {/if}
        </div>

        {#each fields as field, idx (field.id)}
          <div class="border border-slate-200 dark:border-warm-700 rounded p-2 bg-slate-50 dark:bg-warm-900/30">
            <div class="flex items-center gap-2">
              {#if !item.deleted_at}
                <div class="flex flex-col">
                  <button type="button" disabled={idx === 0} onclick={() => moveField(field.id, -1)} class="disabled:opacity-20 cursor-pointer disabled:cursor-not-allowed" aria-label="Move up">
                    <GripVertical size={12} />
                  </button>
                </div>
              {/if}
              <input
                type="text"
                bind:value={fields[idx].label}
                disabled={!!item.deleted_at}
                class="w-32 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-900 disabled:opacity-60"
                placeholder="Label"
              />
              <select
                bind:value={fields[idx].type}
                disabled={!!item.deleted_at}
                class="px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-900 disabled:opacity-60"
              >
                <option value="text">Text</option>
                <option value="password">Password</option>
                <option value="email">Email</option>
                <option value="phone">Phone</option>
                <option value="url">URL</option>
                <option value="username">Username</option>
                <option value="totp">TOTP</option>
                <option value="date">Date</option>
                <option value="month_year">MM/YY</option>
                <option value="cvv">CVV</option>
                <option value="card_number">Card number</option>
                <option value="pin">PIN</option>
                <option value="address">Address</option>
                <option value="ssh_private_key">SSH private key</option>
                <option value="ssh_public_key">SSH public key</option>
                <option value="api_key">API key</option>
                <option value="secret_token">Secret token</option>
                <option value="hostname">Hostname</option>
                <option value="port">Port</option>
                <option value="connection_string">Connection string</option>
              </select>
              <label class="flex items-center gap-1 text-xs cursor-pointer">
                <input type="checkbox" bind:checked={fields[idx].sensitive} disabled={!!item.deleted_at} />
                <span class="text-slate-500">mask</span>
              </label>
              <div class="flex-1"></div>
              {#if !item.deleted_at}
                <button type="button" onclick={() => removeField(field.id)} class="text-slate-400 hover:text-red-600 cursor-pointer" aria-label="Remove field">
                  <X size={14} />
                </button>
              {/if}
            </div>

            <div class="mt-1.5 flex items-start gap-1">
              {#if field.type === 'totp'}
                <div class="flex-1 space-y-1">
                  <input
                    type="text"
                    bind:value={fields[idx].value}
                    disabled={!!item.deleted_at}
                    placeholder="otpauth://totp/... or base32 secret"
                    class="w-full px-2 py-1 text-xs font-mono rounded border border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-900 disabled:opacity-60"
                  />
                  {#if field.value}
                    <TOTPDisplay field={field} />
                  {/if}
                </div>
              {:else if field.type === 'ssh_private_key' || field.type === 'ssh_public_key' || field.type === 'connection_string' || field.type === 'address'}
                <textarea
                  bind:value={fields[idx].value}
                  disabled={!!item.deleted_at}
                  rows="4"
                  class="flex-1 px-2 py-1 text-xs font-mono rounded border border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-900 disabled:opacity-60 {field.sensitive && !unmasked.has(field.id) ? '[-webkit-text-security:disc] [text-security:disc]' : ''}"
                ></textarea>
              {:else}
                <input
                  type={field.sensitive && !unmasked.has(field.id) ? 'password' : 'text'}
                  bind:value={fields[idx].value}
                  disabled={!!item.deleted_at}
                  class="flex-1 px-2 py-1 text-sm rounded border border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-900 disabled:opacity-60"
                />
              {/if}
              {#if field.sensitive}
                <button type="button" onclick={() => toggleMask(field.id)} class="p-1.5 rounded hover:bg-slate-200 dark:hover:bg-warm-800 cursor-pointer" aria-label="Toggle visibility">
                  {#if unmasked.has(field.id)}<EyeOff size={14} />{:else}<Eye size={14} />{/if}
                </button>
              {/if}
              <button type="button" onclick={() => copyField(field.id, field.value)} class="p-1.5 rounded hover:bg-slate-200 dark:hover:bg-warm-800 cursor-pointer" aria-label="Copy value">
                {#if copiedField === field.id}<Check size={14} class="text-emerald-600" />{:else}<Copy size={14} />{/if}
              </button>
              {#if field.type === 'password' && !item.deleted_at}
                <button type="button" onclick={() => (generatorFor = field.id)} class="p-1.5 rounded hover:bg-slate-200 dark:hover:bg-warm-800 cursor-pointer" aria-label="Generate password">
                  <Dices size={14} />
                </button>
              {/if}
            </div>
          </div>
        {/each}
      </div>

      <!-- Notes -->
      <div>
        <label class="block text-[10px] font-medium uppercase tracking-wider text-slate-500 mb-1" for="vi-notes">Notes</label>
        <textarea
          id="vi-notes"
          bind:value={notes}
          disabled={!!item.deleted_at}
          rows="5"
          class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-900 focus:outline-none focus:ring-2 focus:ring-accent-500 disabled:opacity-60"
        ></textarea>
      </div>
    {/if}

    <!-- Metadata footer -->
    <div class="text-[10px] text-slate-400 pt-2 border-t border-slate-100 dark:border-warm-800 space-y-0.5">
      <div>Created: {new Date(item.created_at).toLocaleString()}</div>
      <div>Updated: {new Date(item.updated_at).toLocaleString()} · v{item.version}</div>
      {#if item.deleted_at}
        <div class="text-red-500">Deleted: {new Date(item.deleted_at).toLocaleString()}</div>
      {/if}
    </div>
  </div>
</div>

<!-- Generator overlay -->
{#if generatorFor}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40" onclick={() => (generatorFor = null)} role="dialog" tabindex="-1" onkeydown={(e) => { if (e.key === 'Escape') generatorFor = null; }} aria-modal="true">
    <!-- Inner panel: stopPropagation is for click bubbling only; no -->
    <!-- a11y semantics needed because keyboard interaction is on the -->
    <!-- backdrop (Escape) and the dismiss button (Enter/Space). -->
    <!-- svelte-ignore a11y_no_static_element_interactions a11y_click_events_have_key_events -->
    <div class="bg-white dark:bg-warm-900 rounded-lg shadow-xl p-4 w-96" onclick={(e) => e.stopPropagation()}>
      <div class="flex items-center justify-between mb-3">
        <h3 class="text-sm font-semibold">Password generator</h3>
        <button onclick={() => (generatorFor = null)} class="text-slate-400 hover:text-slate-600 cursor-pointer" aria-label="Close">
          <X size={16} />
        </button>
      </div>
      <PasswordGenerator onApply={applyGenerator} />
    </div>
  </div>
{/if}
