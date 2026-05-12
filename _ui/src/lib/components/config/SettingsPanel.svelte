<script lang="ts">
  import { configStore } from '@/lib/store/config.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { basePath } from '@/lib/basepath';
  import type { FileFormat, InheritEntry } from '@/lib/types/config';
  import { Play, Info, Clock, HardDrive, GitBranch, FileText, Plus, Trash2, Layers, ChevronDown, ChevronRight, Pencil, Link, Copy, Check } from 'lucide-svelte';
  import { onMount, tick } from 'svelte';

  interface Props {
    onRender?: () => void;
  }

  let { onRender }: Props = $props();

  const formats: FileFormat[] = ['json', 'yaml', 'toml', 'raw'];

  const activeTab = $derived(configStore.activeTab);
  const settings = $derived(configStore.settings);
  const externalResources = $derived(
    settings?.external ? Object.keys(settings.external) : []
  );
  const rawMountPrefixes = $derived(
    settings?.raw_mounts ? settings.raw_mounts.map(m => m.prefix).filter(Boolean) : []
  );

  // Version list state
  let versionsExpanded = $state(false);
  const allVersions = $derived(activeTab ? [...activeTab.versions].reverse() : []);
  const visibleVersions = $derived(versionsExpanded ? allVersions : allVersions.slice(0, 1));
  const hasMoreVersions = $derived(allVersions.length > 1);

  // Constraint editing state
  let editingConstraintVersion = $state<number | null>(null);
  let editingConstraintValue = $state('');

  async function startEditConstraint(version: number, currentConstraint: string) {
    editingConstraintVersion = version;
    editingConstraintValue = currentConstraint || '';
    await tick();
    const input = document.querySelector<HTMLInputElement>('[data-constraint-input]');
    input?.focus();
    input?.select();
  }

  function cancelEditConstraint() {
    editingConstraintVersion = null;
    editingConstraintValue = '';
  }

  async function saveConstraint(version: number) {
    if (!activeTab) return;
    try {
      await configStore.updateVersionConstraint(activeTab.id, version, editingConstraintValue.trim());
      editingConstraintVersion = null;
      editingConstraintValue = '';
    } catch {
      // Toast is handled by store
    }
  }

  function handleConstraintKeydown(e: KeyboardEvent, version: number) {
    if (e.key === 'Enter') {
      e.preventDefault();
      saveConstraint(version);
    } else if (e.key === 'Escape') {
      e.preventDefault();
      cancelEditConstraint();
    }
  }

  // Variant state
  let showAddVariant = $state(false);
  let newVariantKey = $state('');

  // API endpoint copy state
  let copiedEndpoint = $state(false);

  const dataEndpoint = $derived(
    activeTab
      ? `${basePath}/data/${activeTab.path}${activeTab.variantKey ? `?variant=${activeTab.variantKey}` : ''}`
      : ''
  );

  async function copyEndpoint() {
    if (!dataEndpoint) return;
    const fullUrl = `${window.location.origin}${dataEndpoint}`;
    try {
      await navigator.clipboard.writeText(fullUrl);
      copiedEndpoint = true;
      setTimeout(() => { copiedEndpoint = false; }, 1500);
    } catch {
      addToast('Failed to copy', 'alert');
    }
  }

  // Inheritance state
  let showInheritance = $state(true);
  let showAddInherit = $state(false);
  let newInheritSource = $state('');
  let newInheritType = $state<'internal' | 'external' | 'mount'>('internal');
  let newInheritResource = $state('');
  let newInheritMount = $state('');
  let newInheritPath = $state('');
  let newInheritPaths = $state('');
  let newInheritInject = $state('');
  let externalPathSuggestions = $state<string[]>([]);
  let loadingPaths = $state(false);
  let editingInheritIndex = $state<number | null>(null);

  const inheritEntries = $derived(activeTab?.meta?.inherits || []);

  onMount(() => {
    configStore.loadSettings();
  });

  function handleFormatChange(e: Event) {
    const target = e.target as HTMLSelectElement;
    if (activeTab) {
      configStore.updateTabFormat(activeTab.id, target.value as FileFormat);
    }
  }

  function handleDescriptionChange(e: Event) {
    const target = e.target as HTMLTextAreaElement;
    if (activeTab) {
      configStore.updateTabMeta(activeTab.id, { description: target.value });
    }
  }

  // ── Inheritance handlers ──
  function addInheritEntry() {
    if (!activeTab) return;

    const entry: InheritEntry = {};

    if (newInheritType === 'internal') {
      if (!newInheritSource.trim()) {
        addToast('Source path is required', 'alert');
        return;
      }
      entry.source = newInheritSource.trim();
    } else if (newInheritType === 'mount') {
      if (!newInheritMount) {
        addToast('Mount prefix is required', 'alert');
        return;
      }
      entry.mount = newInheritMount;
      if (newInheritPath.trim()) {
        entry.path = newInheritPath.trim();
      }
    } else {
      if (!newInheritResource) {
        addToast('External resource is required', 'alert');
        return;
      }
      entry.resource = newInheritResource;
      if (newInheritPath.trim()) {
        entry.path = newInheritPath.trim();
      }
    }

    const paths = newInheritPaths.split(',').map(p => p.trim()).filter(p => p.length > 0);
    if (paths.length > 0) {
      entry.paths = paths;
    }
    if (newInheritInject.trim()) {
      entry.inject = newInheritInject.trim();
    }

    const current = [...(activeTab.meta.inherits || [])];
    const isEdit = editingInheritIndex !== null;
    if (isEdit) {
      current[editingInheritIndex!] = entry;
    } else {
      current.push(entry);
    }
    configStore.updateTabMeta(activeTab.id, { inherits: current });

    // Reset form
    newInheritSource = '';
    newInheritResource = '';
    newInheritMount = '';
    newInheritPath = '';
    newInheritPaths = '';
    newInheritInject = '';
    externalPathSuggestions = [];
    showAddInherit = false;
    editingInheritIndex = null;
    addToast(isEdit ? 'Inheritance updated' : 'Inheritance added', 'success');
  }

  function startEditInherit(index: number) {
    if (!activeTab) return;
    const entry = activeTab.meta.inherits?.[index];
    if (!entry) return;

    if (entry.mount) {
      newInheritType = 'mount';
    } else if (entry.resource) {
      newInheritType = 'external';
    } else {
      newInheritType = 'internal';
    }
    newInheritSource = entry.source || '';
    newInheritResource = entry.resource || '';
    newInheritMount = entry.mount || '';
    newInheritPath = entry.path || '';
    newInheritPaths = entry.paths ? entry.paths.join(', ') : '';
    newInheritInject = entry.inject || '';
    externalPathSuggestions = [];
    editingInheritIndex = index;
    showAddInherit = true;
  }

  function cancelInheritForm() {
    showAddInherit = false;
    editingInheritIndex = null;
    newInheritSource = '';
    newInheritResource = '';
    newInheritMount = '';
    newInheritPath = '';
    newInheritPaths = '';
    newInheritInject = '';
    externalPathSuggestions = [];
  }

  async function loadExternalPaths(resourceName: string, prefix: string = '') {
    if (!resourceName) {
      externalPathSuggestions = [];
      return;
    }
    loadingPaths = true;
    try {
      externalPathSuggestions = await configStore.listExternalPaths(resourceName, prefix);
    } catch {
      externalPathSuggestions = [];
    } finally {
      loadingPaths = false;
    }
  }

  function removeInheritEntry(index: number) {
    if (!activeTab) return;
    const current = [...(activeTab.meta.inherits || [])];
    current.splice(index, 1);
    configStore.updateTabMeta(activeTab.id, { inherits: current.length > 0 ? current : undefined });
    if (editingInheritIndex === index) {
      cancelInheritForm();
    } else if (editingInheritIndex !== null && editingInheritIndex > index) {
      editingInheritIndex = editingInheritIndex - 1;
    }
    addToast('Inheritance removed', 'success');
  }

  // ── Variant handlers ──
  async function handleAddVariant() {
    if (!activeTab) return;
    if (!newVariantKey.trim()) {
      addToast('Variant name is required (e.g., prod, staging)', 'alert');
      return;
    }

    await configStore.createVariant(activeTab.path, newVariantKey.trim());
    newVariantKey = '';
    showAddVariant = false;
  }

  function handleRender() {
    if (onRender) {
      onRender();
    }
  }

  function formatSize(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }

  function formatDate(timestamp?: number): string {
    if (!timestamp) return 'Unknown';
    return new Date(timestamp).toLocaleString();
  }

  // ────────────────────────────────────────────────────────────────────
  // Shared dark-mode class strings.
  //
  // Many widgets in this panel reuse the same chrome (input, card, pill,
  // selected highlight), so the dark-mode treatments are factored out
  // here. Two anchors:
  //   - accent-500 / accent-600 (#F95738 / #EB5E28) — selected / focus.
  //     This replaces the previous brand-blue focus rings so the
  //     "active" cue matches the rest of the dark UI.
  //   - warm-800 (#222120) — input / card surface, one shade above the
  //     panel base (warm-900) so fields are visually distinct from the
  //     dark canvas behind them.
  // ────────────────────────────────────────────────────────────────────
  const inputClass =
    'w-full px-2 py-1.5 text-xs font-mono border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-800 text-slate-700 dark:text-warm-100 placeholder:text-slate-400 dark:placeholder:text-warm-300 rounded focus:outline-none focus:border-accent-500 dark:focus:border-accent-500 focus:ring-2 focus:ring-accent-500/20';
  const selectClass = inputClass; // selects share the same chrome
  const cardClass =
    'bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded';
</script>

<div class="flex flex-col h-full bg-slate-50 dark:bg-warm-900 border-l border-slate-200 dark:border-warm-700">
  {#if activeTab}
    <!-- Panel header. warm-800 surface picks up the deep #222120; just
         one shade above the body so the header reads as a distinct
         section without competing with the navbar.

         The Render Preview action lives in the header (right side) as a
         compact pill — it used to be a full-width button at the bottom
         of the scrollable panel which meant users had to scroll past
         every section to reach it. Header placement keeps it always
         visible and saves the vertical space at the end. -->
    <div class="flex items-center gap-2 px-3.5 py-1.5 bg-slate-100 dark:bg-warm-800 border-b border-slate-200 dark:border-warm-700 text-xs font-semibold text-gray-700 dark:text-warm-100">
      <FileText size={14} />
      <span>File Settings</span>
      <button
        class="ml-auto flex items-center gap-1 px-2 py-1 text-[11px] font-medium text-white bg-vermilion-500 hover:bg-vermilion-600 rounded cursor-pointer transition-colors"
        onclick={handleRender}
        title="Preview the final rendered configuration with inheritance applied"
      >
        <Play size={11} />
        <span>Render</span>
      </button>
    </div>

    <div class="flex-1 overflow-y-auto p-3">
      <!-- Format Selection -->
      <div class="mb-4">
        <label class="flex items-center gap-1.5 text-[11px] font-medium text-slate-500 dark:text-warm-300 mb-1.5 uppercase tracking-wide" for="format-select">
          <span class="flex items-center text-gray-400 dark:text-warm-400"><FileText size={12} /></span>
          Format
        </label>
        <select
          id="format-select"
          class="{selectClass} text-[13px] py-2 cursor-pointer hover:border-slate-300 dark:hover:border-warm-500"
          value={activeTab.format}
          onchange={handleFormatChange}
        >
          {#each formats as format}
            <option value={format}>{format.toUpperCase()}</option>
          {/each}
        </select>
      </div>

      <!-- Version History -->
      <div class="mb-4">
        <label class="flex items-center gap-1.5 text-[11px] font-medium text-slate-500 dark:text-warm-300 mb-1.5 uppercase tracking-wide">
          <span class="flex items-center text-gray-400 dark:text-warm-400"><GitBranch size={12} /></span>
          Versions ({allVersions.length})
        </label>
        {#if allVersions.length > 0}
          <div class="{cardClass} overflow-hidden">
            <div style={versionsExpanded ? 'max-height: 200px; overflow-y: auto' : ''}>
              {#each visibleVersions as ver}
                {@const lastStatus = ver.status[ver.status.length - 1]}
                {@const isCurrent = activeTab.version === ver.version || (activeTab.version === 0 && ver.version === activeTab.latestVersion)}
                {@const isEditing = editingConstraintVersion === ver.version}
                <div
                  class="flex flex-col gap-1 w-full px-2.5 py-2 text-left border-b border-slate-100 dark:border-warm-700 last:border-b-0 transition-colors
                  {isCurrent
                    ? 'bg-accent-50 text-accent-700 dark:bg-accent-900/30 dark:text-accent-200'
                    : 'hover:bg-slate-50 dark:hover:bg-warm-700 text-slate-600 dark:text-warm-200'}"
                >
                  <span class="flex items-center gap-2 min-w-0">
                    <button
                      class="font-mono font-medium text-[12px] shrink-0 cursor-pointer hover:underline"
                      onclick={() => configStore.loadVersion(activeTab.id, ver.version)}
                      title="Load version {ver.version}"
                    >v{ver.version}</button>
                    {#if isEditing}
                      <!-- svelte-ignore a11y_no_static_element_interactions a11y_no_noninteractive_element_interactions -->
                      <span class="flex items-center gap-1.5" onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()}>
                        <input
                          type="text"
                          data-constraint-input
                          bind:value={editingConstraintValue}
                          onkeydown={(e) => handleConstraintKeydown(e, ver.version)}
                          placeholder=">= 0.0.0"
                          class="w-28 px-2 py-1 text-[11px] font-mono border border-amber-400 dark:border-amber-500 rounded bg-white dark:bg-warm-800 text-amber-700 dark:text-amber-300 focus:outline-none focus:border-amber-500 focus:ring-1 focus:ring-amber-500/20"
                        />
                        <button
                          class="px-1.5 py-0.5 text-[10px] text-white bg-amber-500 rounded cursor-pointer hover:bg-amber-600 transition-colors"
                          onclick={() => saveConstraint(ver.version)}
                          title="Save constraint (Enter)"
                        >OK</button>
                        <button
                          class="px-1.5 py-0.5 text-[10px] text-slate-500 dark:text-warm-300 bg-slate-100 dark:bg-warm-700 rounded cursor-pointer hover:bg-slate-200 dark:hover:bg-warm-600 transition-colors"
                          onclick={cancelEditConstraint}
                          title="Cancel (Esc)"
                        >X</button>
                      </span>
                    {:else if ver.constraint}
                      <button
                        class="px-1.5 py-0.5 text-[11px] font-mono bg-amber-100 dark:bg-amber-900/40 text-amber-700 dark:text-amber-200 rounded cursor-pointer hover:bg-amber-200 dark:hover:bg-amber-900/60 transition-colors"
                        onclick={() => startEditConstraint(ver.version, ver.constraint ?? '')}
                        title="Click to edit constraint"
                      >{ver.constraint}</button>
                    {:else}
                      <button
                        class="p-1 text-slate-300 dark:text-warm-500 cursor-pointer hover:text-amber-500 dark:hover:text-amber-400 transition-colors"
                        onclick={() => startEditConstraint(ver.version, '')}
                        title="Add constraint"
                      >
                        <Pencil size={12} />
                      </button>
                    {/if}
                  </span>
                  <span class="flex items-center gap-1.5 text-[10px] text-slate-400 dark:text-warm-400 pl-0.5">
                    {#if lastStatus?.author && lastStatus.author !== 'system'}
                      <span class="text-slate-500 dark:text-warm-300">{lastStatus.author}</span>
                      <span class="text-slate-300 dark:text-warm-500">&middot;</span>
                    {/if}
                    {#if lastStatus?.timestamp}
                      <span>{new Date(lastStatus.timestamp * 1000).toLocaleDateString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</span>
                    {/if}
                    {#if lastStatus?.status === 'DELETED'}
                      <span class="text-red-400">deleted</span>
                    {/if}
                  </span>
                </div>
              {/each}
            </div>
            {#if hasMoreVersions}
              <button
                class="w-full px-2.5 py-1 text-[10px] text-accent-700 dark:text-accent-300 bg-slate-50 dark:bg-warm-800 border-t border-slate-100 dark:border-warm-700 cursor-pointer hover:bg-accent-50 dark:hover:bg-accent-900/30 transition-colors text-center sticky bottom-0"
                onclick={() => versionsExpanded = !versionsExpanded}
              >
                {versionsExpanded ? 'Show less' : `Show all ${allVersions.length} versions`}
              </button>
            {/if}
          </div>
        {:else}
          <p class="text-[11px] text-slate-400 dark:text-warm-400 italic">No versions yet</p>
        {/if}
      </div>

      <!-- API Endpoint -->
      {#if activeTab}
        <div class="mb-4">
          <label class="flex items-center gap-1.5 text-[11px] font-medium text-slate-500 dark:text-warm-300 mb-1.5 uppercase tracking-wide">
            <span class="flex items-center text-gray-400 dark:text-warm-400"><Link size={12} /></span>
            API Endpoint
          </label>
          <div class="{cardClass} overflow-hidden">
            <div class="flex items-center gap-1.5 px-2.5 py-2">
              <code class="flex-1 text-[12px] font-mono text-slate-700 dark:text-warm-100 break-all select-all">{dataEndpoint}</code>
              <button
                class="shrink-0 p-1 text-slate-400 dark:text-warm-300 rounded cursor-pointer hover:text-slate-600 dark:hover:text-white hover:bg-slate-100 dark:hover:bg-warm-700 transition-colors"
                onclick={copyEndpoint}
                title="Copy full URL"
              >
                {#if copiedEndpoint}
                  <Check size={13} class="text-green-500" />
                {:else}
                  <Copy size={13} />
                {/if}
              </button>
            </div>
            <div class="px-2.5 py-1.5 bg-slate-50 dark:bg-warm-900 border-t border-slate-100 dark:border-warm-700 text-[10px] text-slate-400 dark:text-warm-400 leading-relaxed">
              <span class="text-slate-500 dark:text-warm-300">Query params:</span>
              <code class="ml-1 px-1 py-0.5 bg-slate-100 dark:bg-warm-700 rounded text-slate-500 dark:text-warm-200">?version=1.0.0</code>
              <code class="ml-1 px-1 py-0.5 bg-slate-100 dark:bg-warm-700 rounded text-slate-500 dark:text-warm-200">?variant=prod</code>
              <code class="ml-1 px-1 py-0.5 bg-slate-100 dark:bg-warm-700 rounded text-slate-500 dark:text-warm-200">?format=yaml</code>
            </div>
          </div>
        </div>
      {/if}

      <!-- Description -->
      <div class="mb-4">
        <label class="flex items-center gap-1.5 text-[11px] font-medium text-slate-500 dark:text-warm-300 mb-1.5 uppercase tracking-wide" for="description-input">
          <span class="flex items-center text-gray-400 dark:text-warm-400"><Info size={12} /></span>
          Description
        </label>
        <textarea
          id="description-input"
          class="{inputClass} resize-y min-h-[60px] font-sans text-[13px] py-2"
          placeholder="Add a description..."
          value={activeTab.meta.description || ''}
          onchange={handleDescriptionChange}
          rows="3"
        ></textarea>
      </div>

      <div class="h-px bg-slate-200 dark:bg-warm-700 my-4"></div>

      <!-- ════════════════════════════════ -->
      <!-- Inheritance Section -->
      <!-- ════════════════════════════════ -->
      <div class="mb-4">
        <div class="flex items-center justify-between mb-2">
          <button
            class="flex items-center gap-1.5 text-left text-[11px] font-semibold text-slate-500 dark:text-warm-300 uppercase tracking-wide cursor-pointer hover:text-slate-700 dark:hover:text-white"
            onclick={() => showInheritance = !showInheritance}
          >
            {#if showInheritance}<ChevronDown size={12} />{:else}<ChevronRight size={12} />{/if}
            <GitBranch size={12} />
            Inherits ({inheritEntries.length})
          </button>
          {#if showInheritance}
            <button
              class="flex items-center gap-0.5 px-1.5 py-0.5 text-[10px] text-accent-700 dark:text-accent-300 bg-accent-50 dark:bg-accent-900/30 rounded cursor-pointer hover:bg-accent-100 dark:hover:bg-accent-900/50 transition-colors"
              onclick={() => { editingInheritIndex = null; showAddInherit = true; }}
            >
              <Plus size={10} /> Add
            </button>
          {/if}
        </div>

        {#if showInheritance}
          <!-- Add Inherit Form -->
          {#if showAddInherit}
            <div class="p-2.5 mb-2 {cardClass}">
              <!-- Source type toggle -->
              <div class="flex gap-1.5 mb-2">
                <button
                  class="flex-1 py-1 text-[10px] font-medium rounded transition-colors cursor-pointer
                  {newInheritType === 'internal'
                    ? 'bg-accent-100 text-accent-700 border border-accent-300 dark:bg-accent-900/40 dark:text-accent-200 dark:border-accent-700'
                    : 'bg-slate-50 dark:bg-warm-900 text-slate-500 dark:text-warm-300 border border-slate-200 dark:border-warm-700 hover:bg-slate-100 dark:hover:bg-warm-700'}"
                  onclick={() => { newInheritType = 'internal'; newInheritSource = ''; newInheritResource = ''; newInheritMount = ''; newInheritPath = ''; externalPathSuggestions = []; }}
                >
                  Internal
                </button>
                <button
                  class="flex-1 py-1 text-[10px] font-medium rounded transition-colors cursor-pointer
                  {newInheritType === 'external'
                    ? 'bg-accent-100 text-accent-700 border border-accent-300 dark:bg-accent-900/40 dark:text-accent-200 dark:border-accent-700'
                    : 'bg-slate-50 dark:bg-warm-900 text-slate-500 dark:text-warm-300 border border-slate-200 dark:border-warm-700 hover:bg-slate-100 dark:hover:bg-warm-700'}"
                  onclick={() => { newInheritType = 'external'; newInheritSource = ''; newInheritResource = ''; newInheritMount = ''; newInheritPath = ''; externalPathSuggestions = []; }}
                >
                  External
                </button>
                <button
                  class="flex-1 py-1 text-[10px] font-medium rounded transition-colors cursor-pointer
                  {newInheritType === 'mount'
                    ? 'bg-accent-100 text-accent-700 border border-accent-300 dark:bg-accent-900/40 dark:text-accent-200 dark:border-accent-700'
                    : 'bg-slate-50 dark:bg-warm-900 text-slate-500 dark:text-warm-300 border border-slate-200 dark:border-warm-700 hover:bg-slate-100 dark:hover:bg-warm-700'}"
                  onclick={() => { newInheritType = 'mount'; newInheritSource = ''; newInheritResource = ''; newInheritMount = ''; newInheritPath = ''; externalPathSuggestions = []; }}
                >
                  Mount
                </button>
              </div>

              <!-- Source -->
              {#if newInheritType === 'internal'}
                <input
                  type="text"
                  bind:value={newInheritSource}
                  placeholder="Config path (e.g., base/database)"
                  class="{inputClass} mb-2"
                />
              {:else if newInheritType === 'mount'}
                <!-- Raw mount selector -->
                <select
                  class="{selectClass} mb-2"
                  bind:value={newInheritMount}
                  onchange={() => { newInheritPath = ''; }}
                >
                  <option value="">Select mount</option>
                  {#each rawMountPrefixes as prefix}
                    <option value={prefix}>{prefix}</option>
                  {/each}
                </select>

                <!-- Path within the mount -->
                {#if newInheritMount}
                  <label class="block mb-2">
                    <span class="block text-[10px] text-slate-400 dark:text-warm-400 mb-0.5">File path (e.g., configs/base.json)</span>
                    <input
                      type="text"
                      bind:value={newInheritPath}
                      placeholder="Path within mount"
                      class={inputClass}
                    />
                  </label>
                {/if}
              {:else}
                <!-- External resource selector -->
                <select
                  class="{selectClass} mb-2"
                  bind:value={newInheritResource}
                  onchange={() => { newInheritPath = ''; loadExternalPaths(newInheritResource); }}
                >
                  <option value="">Select external resource</option>
                  {#each externalResources as resource}
                    <option value={resource}>{resource}</option>
                  {/each}
                </select>

                <!-- Path within the resource -->
                {#if newInheritResource}
                  <label class="block mb-2">
                    <span class="block text-[10px] text-slate-400 dark:text-warm-400 mb-0.5">Path (e.g., myapp/database)</span>
                    <input
                      type="text"
                      bind:value={newInheritPath}
                      placeholder="Secret or endpoint path"
                      class={inputClass}
                    />
                  </label>

                  <!-- Path suggestions from browse -->
                  {#if loadingPaths}
                    <div class="text-[10px] text-slate-400 dark:text-warm-400 mb-2">Loading paths...</div>
                  {:else if externalPathSuggestions.length > 0}
                    <div class="mb-2 max-h-32 overflow-y-auto {cardClass}">
                      {#each externalPathSuggestions as suggestion}
                        {@const isDir = suggestion.endsWith('/')}
                        <button
                          class="w-full text-left px-2 py-1 text-xs font-mono hover:bg-accent-50 dark:hover:bg-accent-900/30 transition-colors cursor-pointer border-b border-slate-100 dark:border-warm-700 last:border-b-0
                          {isDir
                            ? 'text-slate-500 dark:text-warm-300'
                            : 'text-accent-700 dark:text-accent-300'}"
                          onclick={() => {
                            if (isDir) {
                              newInheritPath = (newInheritPath ? newInheritPath.replace(/\/?$/, '/') : '') + suggestion;
                              loadExternalPaths(newInheritResource, newInheritPath);
                            } else {
                              const base = newInheritPath.includes('/') ? newInheritPath.replace(/[^/]*$/, '') : '';
                              newInheritPath = base + suggestion;
                              externalPathSuggestions = [];
                            }
                          }}
                        >
                          {#if isDir}📁{:else}📄{/if} {suggestion}
                        </button>
                      {/each}
                    </div>
                  {/if}
                {/if}
              {/if}

              <!-- Paths filter -->
              <label class="block mb-2">
                <span class="block text-[10px] text-slate-400 dark:text-warm-400 mb-0.5">Include paths (optional)</span>
                <input
                  type="text"
                  bind:value={newInheritPaths}
                  placeholder="e.g., host, port, credentials.*"
                  class={inputClass}
                />
              </label>

              <!-- Inject target -->
              <label class="block mb-2">
                <span class="block text-[10px] text-slate-400 dark:text-warm-400 mb-0.5">Inject at (optional)</span>
                <input
                  type="text"
                  bind:value={newInheritInject}
                  placeholder="e.g., database.auth (empty = root)"
                  class={inputClass}
                />
              </label>

              <div class="flex gap-1.5">
                <button
                  class="flex-1 py-1 text-[11px] text-white bg-accent-600 rounded cursor-pointer hover:bg-accent-700 transition-colors"
                  onclick={addInheritEntry}
                >
                  {editingInheritIndex !== null ? 'Save' : 'Add'}
                </button>
                <button
                  class="flex-1 py-1 text-[11px] text-slate-500 dark:text-warm-300 bg-slate-100 dark:bg-warm-700 rounded cursor-pointer hover:bg-slate-200 dark:hover:bg-warm-600 transition-colors"
                  onclick={cancelInheritForm}
                >
                  Cancel
                </button>
              </div>
            </div>
          {/if}

          <!-- Inherit entries list -->
          {#if inheritEntries.length === 0 && !showAddInherit}
            <p class="text-[11px] text-slate-400 dark:text-warm-400 italic">No inheritance. Add sources to merge configs from other files or external resources.</p>
          {:else}
            <div class="space-y-1.5">
              {#each inheritEntries as entry, i (i)}
                <div class="{cardClass} overflow-hidden">
                  <div class="flex items-start justify-between px-2.5 py-2 gap-2">
                    <div class="flex-1 min-w-0">
                      {#if entry.mount}
                        <div class="flex items-center gap-1 text-xs">
                          <span class="px-1 py-0.5 bg-amber-50 dark:bg-amber-900/40 text-amber-600 dark:text-amber-200 rounded text-[10px] font-medium shrink-0">mount</span>
                          <span class="font-mono text-accent-700 dark:text-accent-300 truncate" title="{entry.mount}/{entry.path || ''}">{entry.mount}</span>
                        </div>
                        {#if entry.path}
                          <div class="text-[10px] text-slate-400 dark:text-warm-400 mt-0.5">
                            path: <span class="font-mono text-slate-500 dark:text-warm-200">{entry.path}</span>
                          </div>
                        {/if}
                      {:else if entry.resource}
                        <div class="flex items-center gap-1 text-xs">
                          <span class="px-1 py-0.5 bg-purple-50 dark:bg-purple-900/40 text-purple-600 dark:text-purple-200 rounded text-[10px] font-medium shrink-0">ext</span>
                          <span class="font-mono text-accent-700 dark:text-accent-300 truncate" title="{entry.resource}:{entry.path || ''}">{entry.resource}</span>
                        </div>
                        {#if entry.path}
                          <div class="text-[10px] text-slate-400 dark:text-warm-400 mt-0.5">
                            path: <span class="font-mono text-slate-500 dark:text-warm-200">{entry.path}</span>
                          </div>
                        {/if}
                      {:else}
                        <div class="text-xs font-mono text-accent-700 dark:text-accent-300 truncate" title={entry.source}>
                          {entry.source}
                        </div>
                      {/if}
                      {#if entry.paths?.length}
                        <div class="text-[10px] text-slate-400 dark:text-warm-400 mt-0.5">
                          paths: <span class="font-mono text-slate-500 dark:text-warm-200">{entry.paths.join(', ')}</span>
                        </div>
                      {/if}
                      {#if entry.inject}
                        <div class="text-[10px] text-slate-400 dark:text-warm-400 mt-0.5">
                          inject: <span class="font-mono text-emerald-600 dark:text-emerald-300">{entry.inject}</span>
                        </div>
                      {/if}
                    </div>
                    <div class="flex items-center gap-1 shrink-0">
                      <button
                        class="p-0.5 text-slate-400 dark:text-warm-400 hover:text-accent-600 dark:hover:text-accent-300 cursor-pointer transition-colors"
                        onclick={() => startEditInherit(i)}
                        title="Edit"
                      >
                        <Pencil size={12} />
                      </button>
                      <button
                        class="p-0.5 text-slate-400 dark:text-warm-400 hover:text-red-500 dark:hover:text-red-400 cursor-pointer transition-colors"
                        onclick={() => removeInheritEntry(i)}
                        title="Remove"
                      >
                        <Trash2 size={12} />
                      </button>
                    </div>
                  </div>
                </div>
              {/each}
            </div>
          {/if}
        {/if}
      </div>

      <div class="h-px bg-slate-200 dark:bg-warm-700 my-4"></div>

      <!-- ════════════════════════════════ -->
      <!-- Variants Section -->
      <!-- ════════════════════════════════ -->
      {#if !activeTab.variantKey}
        <div class="mb-4">
          <div class="flex items-center justify-between mb-2">
            <span class="flex items-center gap-1.5 text-[11px] font-semibold text-slate-500 dark:text-warm-300 uppercase tracking-wide">
              <Layers size={12} />
              Variants
            </span>
            <button
              class="flex items-center gap-0.5 px-1.5 py-0.5 text-[10px] text-accent-700 dark:text-accent-300 bg-accent-50 dark:bg-accent-900/30 rounded cursor-pointer hover:bg-accent-100 dark:hover:bg-accent-900/50 transition-colors"
              onclick={() => showAddVariant = true}
            >
              <Plus size={10} /> Add
            </button>
          </div>

          {#if showAddVariant}
            <div class="p-2.5 mb-2 {cardClass}">
              <input
                type="text"
                bind:value={newVariantKey}
                placeholder="e.g., prod, staging"
                class="{inputClass} mb-2"
              />
              <div class="flex gap-1.5">
                <button
                  class="flex-1 py-1 text-[11px] text-white bg-accent-600 rounded cursor-pointer hover:bg-accent-700 transition-colors"
                  onclick={handleAddVariant}
                >
                  Create
                </button>
                <button
                  class="flex-1 py-1 text-[11px] text-slate-500 dark:text-warm-300 bg-slate-100 dark:bg-warm-700 rounded cursor-pointer hover:bg-slate-200 dark:hover:bg-warm-600 transition-colors"
                  onclick={() => { showAddVariant = false; newVariantKey = ''; }}
                >
                  Cancel
                </button>
              </div>
            </div>
          {/if}

          <p class="text-[11px] text-slate-400 dark:text-warm-400">Variants are independent configs accessed via <span class="font-mono">?variant=name</span>. Manage them in the file tree.</p>
        </div>
      {:else}
        <div class="mb-4">
          <span class="flex items-center gap-1.5 text-[11px] font-medium text-purple-600 dark:text-purple-300 mb-1">
            <Layers size={12} />
            Variant: <span class="font-mono">@{activeTab.variantKey}</span>
          </span>
          <p class="text-[11px] text-slate-400 dark:text-warm-400">This is an independent variant of <span class="font-mono text-slate-500 dark:text-warm-200">{activeTab.path}</span></p>
        </div>
      {/if}

      <div class="h-px bg-slate-200 dark:bg-warm-700 my-4"></div>

      <!-- File Info -->
      <div class="mb-2">
        <h4 class="text-[11px] font-semibold text-slate-500 dark:text-warm-300 mb-2.5 uppercase tracking-wide">File Information</h4>

        <div class="flex items-center gap-2 py-1.5 text-xs">
          <span class="flex items-center text-gray-400 dark:text-warm-400"><HardDrive size={12} /></span>
          <span class="text-slate-500 dark:text-warm-300 min-w-[60px]">Size</span>
          <span class="text-gray-700 dark:text-warm-100 flex-1">{formatSize(activeTab.size)}</span>
        </div>

        <div class="flex items-center gap-2 py-1.5 text-xs">
          <span class="flex items-center text-gray-400 dark:text-warm-400"><Clock size={12} /></span>
          <span class="text-slate-500 dark:text-warm-300 min-w-[60px]">Modified</span>
          <span class="text-gray-700 dark:text-warm-100 flex-1">{formatDate(activeTab.modifiedAt)}</span>
        </div>

        <div class="flex items-center gap-2 py-1.5 text-xs">
          <span class="flex items-center text-gray-400 dark:text-warm-400"><FileText size={12} /></span>
          <span class="text-slate-500 dark:text-warm-300 min-w-[60px]">Path</span>
          <span class="text-gray-700 dark:text-warm-100 flex-1 overflow-hidden text-ellipsis whitespace-nowrap font-mono text-[11px]" title={activeTab.path}>{activeTab.path}</span>
        </div>
      </div>

      <!-- The full-width "Render Preview" button used to live here.
           It has moved to the panel header (top right) so it is always
           visible without scrolling. -->
    </div>
  {:else}
    <div class="flex items-center justify-center h-full text-gray-400 dark:text-warm-400 text-[13px]">
      <p>Select a file to view settings</p>
    </div>
  {/if}
</div>
