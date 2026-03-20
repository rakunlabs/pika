<script lang="ts">
  import CodeMirror from 'svelte-codemirror-editor';
  import { json } from '@codemirror/lang-json';
  import { yaml } from '@codemirror/lang-yaml';
  import { StreamLanguage, LanguageSupport } from '@codemirror/language';
  import { toml } from '@codemirror/legacy-modes/mode/toml';
  import { oneDark } from '@codemirror/theme-one-dark';
  import { configStore } from '@/lib/store/config.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import type { FileFormat, ViewMode } from '@/lib/types/config';
  import { Save, Sparkles, ArrowRightLeft, Upload, Binary, Type } from 'lucide-svelte';
  import axios from 'axios';
  import HexViewer from './HexViewer.svelte';

  function getLanguageExtension(format: FileFormat): LanguageSupport | undefined {
    switch (format) {
      case 'json':
        return json();
      case 'yaml':
        return yaml();
      case 'toml':
        return new LanguageSupport(StreamLanguage.define(toml));
      default:
        return undefined;
    }
  }

  // Beautify content based on format
  function beautify(content: string, format: FileFormat): string | null {
    try {
      switch (format) {
        case 'json': {
          const parsed = JSON.parse(content);
          return JSON.stringify(parsed, null, 2);
        }
        case 'yaml': {
          const lines = content.split('\n');
          const cleaned = lines
            .map(line => line.trimEnd())
            .join('\n')
            .replace(/\n{3,}/g, '\n\n')
            .trim();
          return cleaned + '\n';
        }
        case 'toml': {
          const lines = content.split('\n');
          const cleaned = lines
            .map(line => {
              const trimmed = line.trimEnd();
              if (trimmed.startsWith('[') || trimmed.startsWith('#') || trimmed === '') {
                return trimmed;
              }
              const eqIndex = trimmed.indexOf('=');
              if (eqIndex > 0) {
                const key = trimmed.slice(0, eqIndex).trimEnd();
                const value = trimmed.slice(eqIndex + 1).trimStart();
                return `${key} = ${value}`;
              }
              return trimmed;
            })
            .join('\n')
            .replace(/\n{3,}/g, '\n\n')
            .trim();
          return cleaned + '\n';
        }
        default:
          return null;
      }
    } catch {
      return null;
    }
  }

  const allFormats: FileFormat[] = ['json', 'yaml', 'toml'];

  const activeTab = $derived(configStore.activeTab);
  const languageExtension = $derived(activeTab ? getLanguageExtension(activeTab.format) : undefined);
  const convertTargets = $derived(
    activeTab ? allFormats.filter(f => f !== activeTab.format) : []
  );
  const isHexMode = $derived(activeTab?.viewMode === 'hex');
  const hexData = $derived(activeTab?.rawData || '');

  let isConverting = $state(false);
  let saveConstraint = $state('');
  let fileInput: HTMLInputElement | undefined = $state();

  function handleChange(value: string) {
    if (activeTab) {
      configStore.updateTabContent(activeTab.id, value);
    }
  }

  async function handleSave() {
    if (activeTab && activeTab.isDirty) {
      try {
        const constraint = saveConstraint.trim() || undefined;
        await configStore.saveTab(activeTab.id, constraint);
        saveConstraint = '';
      } catch (error) {
        console.error('Failed to save:', error);
      }
    }
  }

  function handleBeautify() {
    if (!activeTab) return;

    const result = beautify(activeTab.content, activeTab.format);
    if (result === null) {
      addToast(`Could not beautify — check ${activeTab.format.toUpperCase()} syntax`, 'alert');
      return;
    }

    if (result === activeTab.content) {
      addToast('Already formatted', 'info');
      return;
    }

    configStore.updateTabContent(activeTab.id, result);
    addToast('Formatted', 'success');
  }

  async function handleConvert(targetFormat: FileFormat) {
    if (!activeTab || isConverting) return;
    if (activeTab.format === targetFormat) return;
    if (activeTab.format === 'raw') {
      addToast('Cannot convert from raw format', 'alert');
      return;
    }

    isConverting = true;
    try {
      const response = await axios.post('/api/v1/convert', {
        content: activeTab.content,
        from: activeTab.format,
        to: targetFormat
      });

      configStore.updateTabContent(activeTab.id, response.data.content);
      configStore.updateTabFormat(activeTab.id, targetFormat);
      addToast(`Converted to ${targetFormat.toUpperCase()}`, 'success');
    } catch (error: any) {
      const msg = error.response?.data?.message || 'Conversion failed';
      addToast(msg, 'alert');
    } finally {
      isConverting = false;
    }
  }

  function handleImportClick() {
    fileInput?.click();
  }

  async function handleFileSelected(e: Event) {
    const input = e.target as HTMLInputElement;
    const file = input.files?.[0];
    if (!file || !activeTab) return;

    try {
      await configStore.importFileToTab(activeTab.id, file);
    } catch (error) {
      console.error('Failed to import file:', error);
    }

    // Reset the file input so the same file can be re-imported
    input.value = '';
  }

  function toggleViewMode() {
    if (!activeTab) return;
    const newMode: ViewMode = activeTab.viewMode === 'hex' ? 'text' : 'hex';
    configStore.setTabViewMode(activeTab.id, newMode);
  }

  function handleKeyDown(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && e.key === 's') {
      e.preventDefault();
      handleSave();
    }
    // Ctrl/Cmd + Shift + F to beautify
    if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.key === 'F') {
      e.preventDefault();
      handleBeautify();
    }
  }
</script>

<svelte:window onkeydown={handleKeyDown} />

<!-- Hidden file input for import -->
<input
  type="file"
  class="hidden"
  bind:this={fileInput}
  onchange={handleFileSelected}
/>

<div class="flex flex-col h-full bg-[#1e1e1e]">
  {#if activeTab}
    <!-- Editor toolbar -->
    <div class="flex items-center justify-between px-3 py-1.5 bg-[#252526] border-b border-[#3c3c3c] text-xs text-gray-400 shrink-0">
      <div class="flex items-center gap-2 min-w-0">
        <span class="px-1.5 py-0.5 bg-blue-500 text-white rounded text-[10px] font-semibold shrink-0">{activeTab.format.toUpperCase()}</span>
        {#if activeTab.format !== 'raw' && !isHexMode}
          <div class="flex items-center gap-1 shrink-0">
            <ArrowRightLeft size={11} class="text-gray-600" />
            {#each convertTargets as target}
              <button
                class="px-1.5 py-0.5 text-[10px] font-medium rounded border border-[#3c3c3c] text-gray-500 bg-transparent cursor-pointer transition-colors hover:bg-[#333] hover:text-gray-200 hover:border-gray-500 disabled:opacity-40 disabled:cursor-not-allowed"
                onclick={() => handleConvert(target)}
                disabled={isConverting}
                title="Convert to {target.toUpperCase()}"
              >
                {target.toUpperCase()}
              </button>
            {/each}
          </div>
        {/if}
        <span class="text-gray-600 shrink-0">|</span>
        <span class="text-gray-500 overflow-hidden text-ellipsis whitespace-nowrap" title={activeTab.path}>{activeTab.path}</span>
      </div>
      <div class="flex items-center gap-1.5 shrink-0">
        <!-- View mode toggle: Text / Hex -->
        <div class="flex items-center rounded border border-[#3c3c3c] overflow-hidden shrink-0">
          <button
            class="flex items-center gap-1 px-2 py-1 text-[11px] cursor-pointer transition-colors
              {!isHexMode ? 'bg-[#3c3c3c] text-gray-200' : 'bg-transparent text-gray-500 hover:bg-[#333] hover:text-gray-300'}"
            onclick={() => !isHexMode || toggleViewMode()}
            title="Text view"
          >
            <Type size={12} />
            <span>Text</span>
          </button>
          <button
            class="flex items-center gap-1 px-2 py-1 text-[11px] cursor-pointer transition-colors
              {isHexMode ? 'bg-[#3c3c3c] text-gray-200' : 'bg-transparent text-gray-500 hover:bg-[#333] hover:text-gray-300'}"
            onclick={() => isHexMode || toggleViewMode()}
            title="Hex view"
          >
            <Binary size={12} />
            <span>Hex</span>
          </button>
        </div>

        <!-- Import button -->
        <button
          class="flex items-center gap-1 px-2 py-1 text-gray-400 bg-transparent border border-[#3c3c3c] rounded text-[11px] cursor-pointer transition-colors hover:bg-[#333] hover:text-gray-200"
          onclick={handleImportClick}
          title="Import file from disk"
        >
          <Upload size={12} />
          <span>Import</span>
        </button>

        {#if !isHexMode}
          <button
            class="flex items-center gap-1 px-2 py-1 text-gray-400 bg-transparent border border-[#3c3c3c] rounded text-[11px] cursor-pointer transition-colors hover:bg-[#333] hover:text-gray-200"
            onclick={handleBeautify}
            title="Beautify (Ctrl+Shift+F)"
          >
            <Sparkles size={12} />
            <span>Beautify</span>
          </button>
        {/if}
        {#if activeTab.isDirty}
          <input
            type="text"
            bind:value={saveConstraint}
            placeholder=">= 0.0.0"
            title="Semver constraint for this version (optional)"
            class="w-20 px-1.5 py-0.5 text-[10px] font-mono bg-[#1e1e1e] border border-[#3c3c3c] rounded text-gray-400 placeholder:text-gray-600 focus:outline-none focus:border-amber-500"
          />
          <button
            class="flex items-center gap-1 px-2.5 py-1 bg-green-600 text-white border-none rounded text-[11px] font-medium cursor-pointer transition-colors hover:bg-green-500"
            onclick={handleSave}
            title="Save (Ctrl+S)"
          >
            <Save size={12} />
            <span>Save</span>
          </button>
        {:else}
          <span class="px-2 py-1 text-[11px] text-green-500">Saved</span>
        {/if}
      </div>
    </div>

    <div class="flex-1 min-h-0 overflow-auto">
      {#if isHexMode}
        <HexViewer data={hexData} />
      {:else}
        <CodeMirror
          value={activeTab.content}
          onchange={handleChange}
          lang={languageExtension}
          theme={oneDark}
          styles={{
            '&': {
              height: '100%',
              fontSize: '13px',
              overflow: 'auto'
            },
            '.cm-content': {
              fontFamily: "'JetBrains Mono', 'Fira Code', 'Monaco', 'Menlo', monospace"
            },
            '.cm-gutters': {
              backgroundColor: '#1e1e1e',
              color: '#6e7681',
              border: 'none'
            }
          }}
        />
      {/if}
    </div>
  {:else}
    <div class="flex items-center justify-center h-full bg-slate-50">
      <div class="text-center text-gray-400">
        <h3 class="text-base font-medium mb-1 text-gray-500">No file open</h3>
        <p class="text-[13px]">Select a file from the explorer to start editing</p>
      </div>
    </div>
  {/if}
</div>
