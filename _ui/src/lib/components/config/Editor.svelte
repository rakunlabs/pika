<script lang="ts">
  import CodeMirror from 'svelte-codemirror-editor';
  import { json } from '@codemirror/lang-json';
  import { yaml } from '@codemirror/lang-yaml';
  import { StreamLanguage, LanguageSupport } from '@codemirror/language';
  import { toml } from '@codemirror/legacy-modes/mode/toml';
  import { oneDark } from '@codemirror/theme-one-dark';
  import { configStore } from '@/lib/store/config.svelte';
  import type { FileFormat } from '@/lib/types/config';
  import { Save } from 'lucide-svelte';

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

  const activeTab = $derived(configStore.activeTab);
  const languageExtension = $derived(activeTab ? getLanguageExtension(activeTab.format) : undefined);

  function handleChange(value: string) {
    if (activeTab) {
      configStore.updateTabContent(activeTab.id, value);
    }
  }

  async function handleSave() {
    if (activeTab && activeTab.isDirty) {
      try {
        await configStore.saveTab(activeTab.id);
      } catch (error) {
        console.error('Failed to save:', error);
        alert('Failed to save file');
      }
    }
  }

  function handleKeyDown(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && e.key === 's') {
      e.preventDefault();
      handleSave();
    }
  }
</script>

<svelte:window onkeydown={handleKeyDown} />

<div class="flex flex-col h-full bg-[#1e1e1e]">
  {#if activeTab}
    <div class="flex-1 overflow-hidden [&_.cm-editor]:h-full">
      <CodeMirror
        value={activeTab.content}
        onchange={handleChange}
        lang={languageExtension}
        theme={oneDark}
        styles={{
          '&': {
            height: '100%',
            fontSize: '13px'
          },
          '.cm-scroller': {
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
    </div>
    
    <!-- Editor footer/status bar -->
    <div class="flex items-center justify-between px-3 py-1 bg-[#252526] border-t border-[#3c3c3c] text-xs text-gray-400">
      <div class="flex items-center gap-2">
        <span class="px-1.5 py-0.5 bg-blue-500 text-white rounded text-[10px] font-semibold">{activeTab.format.toUpperCase()}</span>
        <span class="text-gray-600">|</span>
        <span class="text-gray-500">{activeTab.path}</span>
      </div>
      <div class="flex items-center gap-2">
        {#if activeTab.isDirty}
          <button 
            class="flex items-center gap-1 px-2.5 py-1 bg-green-500 text-white border-none rounded text-xs cursor-pointer transition-colors hover:bg-green-600"
            onclick={handleSave}
          >
            <Save size={14} />
            <span>Save</span>
          </button>
        {:else}
          <span class="text-green-500">Saved</span>
        {/if}
      </div>
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
