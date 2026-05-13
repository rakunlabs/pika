<script lang="ts">
  import { Search, Star, Archive, Trash2, KeyRound, CreditCard, UserSquare2, FileText, Terminal, Plug, Database, Server, FileBadge, ShieldCheck, Plus } from 'lucide-svelte';
  import { vaultStore } from '@/lib/vault/store.svelte';
  import { typeLabel } from '@/lib/vault/templates';
  import type { VaultItem, VaultItemType, VaultListFilter } from '@/lib/vault/api';

  interface Props {
    selectedId: string | null;
    onSelect: (id: string | null) => void;
    onNew: () => void;
  }
  let { selectedId, onSelect, onNew }: Props = $props();

  // The view tab + type/favorite filters are server-evaluable (the
  // server holds them in cleartext) so they go into the API filter.
  // The free-text search and the tag filter run entirely in memory
  // because the corresponding fields are encrypted at rest.
  let view = $state<'active' | 'archived' | 'trash'>('active');
  let typeFilter = $state<VaultItemType | ''>('');
  let tagFilter = $state('');
  let q = $state('');
  let favoritesOnly = $state(false);

  // Re-fetch only when SERVER-EVALUABLE filters change. Typing in
  // the search box (or selecting a tag) does not hit the network —
  // those filter the already-decrypted list below.
  $effect(() => {
    const filter: VaultListFilter = {};
    if (typeFilter) filter.type = typeFilter;
    if (favoritesOnly) filter.favorite = true;
    if (view === 'archived') filter.archived = 'only';
    if (view === 'trash') filter.trash = true;
    vaultStore.refreshItems(filter);
  });

  function iconFor(type: VaultItemType) {
    switch (type) {
      case 'login': return KeyRound;
      case 'card': return CreditCard;
      case 'identity': return UserSquare2;
      case 'secure_note': return FileText;
      case 'ssh_key': return Terminal;
      case 'api_credential': return Plug;
      case 'database': return Database;
      case 'server': return Server;
      case 'license': return FileBadge;
      case 'tls_cert': return ShieldCheck;
      default: return FileText;
    }
  }

  // Pull the decrypted view of an item (title + tags) so we can sort
  // and search against it without re-running AEAD on every keystroke.
  function decryptedTitle(item: VaultItem): string {
    const d = vaultStore.decrypted.get(item.id);
    if (!d) return '';
    if (d.title === null) return '(unreadable)';
    return d.title;
  }
  function decryptedTags(item: VaultItem): string[] {
    return vaultStore.decrypted.get(item.id)?.tags ?? [];
  }

  // Filter (in memory: title substring + tag chip) and sort
  // (favorites first, then by decrypted title).
  const sorted = $derived.by(() => {
    const needle = q.trim().toLowerCase();
    const tag = tagFilter.trim().toLowerCase();
    const filtered = vaultStore.items.filter((i: VaultItem) => {
      if (needle) {
        const title = decryptedTitle(i).toLowerCase();
        const tags = decryptedTags(i).map(t => t.toLowerCase());
        const matched = title.includes(needle) || tags.some(t => t.includes(needle));
        if (!matched) return false;
      }
      if (tag) {
        const tags = decryptedTags(i).map(t => t.toLowerCase());
        if (!tags.includes(tag)) return false;
      }
      return true;
    });
    filtered.sort((a, b) => {
      if ((a.favorite ?? false) !== (b.favorite ?? false)) {
        return a.favorite ? -1 : 1;
      }
      return decryptedTitle(a).localeCompare(decryptedTitle(b));
    });
    return filtered;
  });

  // Tag chip dropdown options — union of all tags currently
  // decrypted. Refreshes reactively as items load.
  const availableTags = $derived(vaultStore.allTags());
</script>

<div class="flex flex-col h-full w-72 border-r border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-950">
  <!-- View tabs -->
  <div class="flex border-b border-slate-200 dark:border-warm-700 text-xs">
    <button class="flex-1 py-2 {view === 'active' ? 'bg-accent-600 text-white' : 'hover:bg-slate-100 dark:hover:bg-warm-800'} cursor-pointer"
      onclick={() => (view = 'active')}>Items</button>
    <button class="flex-1 py-2 {view === 'archived' ? 'bg-accent-600 text-white' : 'hover:bg-slate-100 dark:hover:bg-warm-800'} cursor-pointer flex items-center justify-center gap-1"
      onclick={() => (view = 'archived')}><Archive size={12} /> Archive</button>
    <button class="flex-1 py-2 {view === 'trash' ? 'bg-accent-600 text-white' : 'hover:bg-slate-100 dark:hover:bg-warm-800'} cursor-pointer flex items-center justify-center gap-1"
      onclick={() => (view = 'trash')}><Trash2 size={12} /> Trash</button>
  </div>

  <!-- Search + filters -->
  <div class="p-2 space-y-2 border-b border-slate-200 dark:border-warm-700">
    <div class="relative">
      <Search size={14} class="absolute top-1/2 left-2 -translate-y-1/2 text-slate-400" />
      <input
        type="text"
        bind:value={q}
        placeholder="Search title or tag..."
        class="w-full pl-7 pr-2 py-1.5 text-xs rounded border border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-900 focus:outline-none focus:ring-1 focus:ring-accent-500"
      />
    </div>

    <div class="flex items-center gap-1">
      <select
        bind:value={typeFilter}
        class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-900 focus:outline-none focus:ring-1 focus:ring-accent-500"
      >
        <option value="">All types</option>
        <option value="login">Login</option>
        <option value="card">Card</option>
        <option value="identity">Identity</option>
        <option value="secure_note">Note</option>
        <option value="ssh_key">SSH key</option>
        <option value="api_credential">API</option>
        <option value="database">Database</option>
        <option value="server">Server</option>
        <option value="license">License</option>
        <option value="tls_cert">TLS</option>
      </select>
      <button
        onclick={() => (favoritesOnly = !favoritesOnly)}
        class="p-1.5 rounded {favoritesOnly ? 'bg-amber-100 dark:bg-amber-900 text-amber-700 dark:text-amber-300' : 'hover:bg-slate-100 dark:hover:bg-warm-800 text-slate-400'} cursor-pointer"
        title="Favorites only"
      >
        <Star size={14} fill={favoritesOnly ? 'currentColor' : 'none'} />
      </button>
    </div>

    {#if availableTags.length > 0}
      <select
        bind:value={tagFilter}
        class="w-full px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-900 focus:outline-none focus:ring-1 focus:ring-accent-500"
      >
        <option value="">All tags</option>
        {#each availableTags as t (t)}
          <option value={t}>#{t}</option>
        {/each}
      </select>
    {/if}

    {#if view === 'active'}
      <button
        onclick={onNew}
        class="w-full flex items-center justify-center gap-1.5 px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 cursor-pointer"
      >
        <Plus size={12} /> New item
      </button>
    {/if}
  </div>

  <!-- List -->
  <div class="flex-1 overflow-y-auto">
    {#if vaultStore.loading && sorted.length === 0}
      <div class="text-xs text-slate-400 text-center py-6">Loading...</div>
    {:else if sorted.length === 0}
      <div class="text-xs text-slate-400 text-center py-6">
        {#if view === 'trash'}Trash is empty
        {:else if view === 'archived'}No archived items
        {:else if q || typeFilter || favoritesOnly || tagFilter}No items match
        {:else}No items yet — click "New item" above
        {/if}
      </div>
    {:else}
      {#each sorted as item (item.id)}
        {@const Icon = iconFor(item.type)}
        {@const titleText = decryptedTitle(item)}
        <button
          class="w-full flex items-center gap-2 px-3 py-2 text-left text-sm border-l-2 cursor-pointer
            {selectedId === item.id
              ? 'bg-accent-50 dark:bg-accent-950/30 border-accent-500'
              : 'border-transparent hover:bg-slate-50 dark:hover:bg-warm-900'}"
          onclick={() => onSelect(item.id)}
        >
          <Icon size={16} class="shrink-0 text-slate-500 dark:text-slate-400" />
          <div class="flex-1 min-w-0">
            <div class="truncate font-medium {titleText === '(unreadable)' ? 'italic text-red-500' : ''}">{titleText || '(untitled)'}</div>
            <div class="truncate text-[10px] uppercase tracking-wide text-slate-400">{typeLabel(item.type)}</div>
          </div>
          {#if item.favorite}
            <Star size={12} fill="currentColor" class="text-amber-500" />
          {/if}
        </button>
      {/each}
    {/if}
  </div>
</div>
