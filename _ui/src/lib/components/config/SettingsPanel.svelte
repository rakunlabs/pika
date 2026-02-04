<script lang="ts">
  import { configStore } from '@/lib/store/config.svelte';
  import type { FileFormat } from '@/lib/types/config';
  import { Play, Info, Clock, HardDrive, GitBranch, FileText } from 'lucide-svelte';
  import { onMount } from 'svelte';

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

  function handleInheritChange(e: Event) {
    const target = e.target as HTMLSelectElement;
    if (activeTab) {
      configStore.updateTabMeta(activeTab.id, { 
        inherit: target.value || undefined 
      });
    }
  }

  function handleVersionChange(e: Event) {
    const target = e.target as HTMLSelectElement;
    const version = parseInt(target.value, 10);
    if (activeTab && !isNaN(version)) {
      configStore.loadVersion(activeTab.id, version);
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

  function handleRender() {
    if (onRender) {
      onRender();
    }
  }
</script>

<div class="flex flex-col h-full bg-slate-50 border-l border-slate-200">
  {#if activeTab}
    <div class="flex items-center gap-2 px-3.5 py-2.5 bg-slate-100 border-b border-slate-200 text-xs font-semibold text-gray-700">
      <FileText size={14} />
      <span>File Settings</span>
    </div>

    <div class="flex-1 overflow-y-auto p-3">
      <!-- Format Selection -->
      <div class="mb-4">
        <label class="flex items-center gap-1.5 text-[11px] font-medium text-slate-500 mb-1.5 uppercase tracking-wide" for="format-select">
          <span class="flex items-center text-gray-400"><FileText size={12} /></span>
          Format
        </label>
        <select 
          id="format-select"
          class="w-full px-2.5 py-2 text-[13px] border border-slate-200 rounded bg-white text-gray-700 cursor-pointer transition-colors hover:border-slate-300 focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
          value={activeTab.format}
          onchange={handleFormatChange}
        >
          {#each formats as format}
            <option value={format}>{format.toUpperCase()}</option>
          {/each}
        </select>
      </div>

      <!-- Version Selection -->
      <div class="mb-4">
        <label class="flex items-center gap-1.5 text-[11px] font-medium text-slate-500 mb-1.5 uppercase tracking-wide" for="version-select">
          <span class="flex items-center text-gray-400"><GitBranch size={12} /></span>
          Version
        </label>
        <select 
          id="version-select"
          class="w-full px-2.5 py-2 text-[13px] border border-slate-200 rounded bg-white text-gray-700 cursor-pointer transition-colors hover:border-slate-300 focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
          value={activeTab.version}
          onchange={handleVersionChange}
        >
          <option value={0}>Latest</option>
          {#each activeTab.versions as ver}
            <option value={ver.version}>v{ver.version}</option>
          {/each}
        </select>
      </div>

      <!-- Description -->
      <div class="mb-4">
        <label class="flex items-center gap-1.5 text-[11px] font-medium text-slate-500 mb-1.5 uppercase tracking-wide" for="description-input">
          <span class="flex items-center text-gray-400"><Info size={12} /></span>
          Description
        </label>
        <textarea
          id="description-input"
          class="w-full px-2.5 py-2 text-[13px] border border-slate-200 rounded bg-white text-gray-700 resize-y min-h-[60px] font-sans transition-colors focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
          placeholder="Add a description..."
          value={activeTab.meta.description || ''}
          onchange={handleDescriptionChange}
          rows="3"
        ></textarea>
      </div>

      <!-- Inheritance -->
      <div class="mb-4">
        <label class="flex items-center gap-1.5 text-[11px] font-medium text-slate-500 mb-1.5 uppercase tracking-wide" for="inherit-select">
          <span class="flex items-center text-gray-400"><GitBranch size={12} /></span>
          Inherit From
        </label>
        <select 
          id="inherit-select"
          class="w-full px-2.5 py-2 text-[13px] border border-slate-200 rounded bg-white text-gray-700 cursor-pointer transition-colors hover:border-slate-300 focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
          value={activeTab.meta.inherit || ''}
          onchange={handleInheritChange}
        >
          <option value="">None</option>
          {#each externalResources as resource}
            <option value={resource}>{resource}</option>
          {/each}
        </select>
        <p class="mt-1 text-[11px] text-gray-400">Inherit configuration from external resource</p>
      </div>

      <!-- Divider -->
      <div class="h-px bg-slate-200 my-4"></div>

      <!-- File Info -->
      <div class="mb-2">
        <h4 class="text-[11px] font-semibold text-slate-500 mb-2.5 uppercase tracking-wide">File Information</h4>
        
        <div class="flex items-center gap-2 py-1.5 text-xs">
          <span class="flex items-center text-gray-400"><HardDrive size={12} /></span>
          <span class="text-slate-500 min-w-[60px]">Size</span>
          <span class="text-gray-700 flex-1">{formatSize(activeTab.size)}</span>
        </div>

        <div class="flex items-center gap-2 py-1.5 text-xs">
          <span class="flex items-center text-gray-400"><Clock size={12} /></span>
          <span class="text-slate-500 min-w-[60px]">Modified</span>
          <span class="text-gray-700 flex-1">{formatDate(activeTab.modifiedAt)}</span>
        </div>

        <div class="flex items-center gap-2 py-1.5 text-xs">
          <span class="flex items-center text-gray-400"><FileText size={12} /></span>
          <span class="text-slate-500 min-w-[60px]">Path</span>
          <span class="text-gray-700 flex-1 overflow-hidden text-ellipsis whitespace-nowrap font-mono text-[11px]" title={activeTab.path}>{activeTab.path}</span>
        </div>
      </div>

      <!-- Divider -->
      <div class="h-px bg-slate-200 my-4"></div>

      <!-- Render Button -->
      <div class="pt-2">
        <button 
          class="flex items-center justify-center gap-2 w-full px-4 py-2.5 bg-blue-500 text-white border-none rounded-md text-[13px] font-medium cursor-pointer transition-colors hover:bg-blue-600 active:bg-blue-700"
          onclick={handleRender}
        >
          <Play size={14} />
          <span>Render Preview</span>
        </button>
        <p class="mt-2 text-[11px] text-gray-400 text-center">Preview the final rendered configuration with inheritance applied</p>
      </div>
    </div>
  {:else}
    <div class="flex items-center justify-center h-full text-gray-400 text-[13px]">
      <p>Select a file to view settings</p>
    </div>
  {/if}
</div>
