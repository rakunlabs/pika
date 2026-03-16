<script lang="ts">
  import { X, Copy, Check, Loader2 } from 'lucide-svelte';
  import CodeMirror from 'svelte-codemirror-editor';
  import { json } from '@codemirror/lang-json';
  import { yaml } from '@codemirror/lang-yaml';
  import { StreamLanguage, LanguageSupport } from '@codemirror/language';
  import { toml } from '@codemirror/legacy-modes/mode/toml';
  import { oneDark } from '@codemirror/theme-one-dark';
  import { configStore } from '@/lib/store/config.svelte';
  import type { FileFormat } from '@/lib/types/config';
  import axios from 'axios';

  interface Props {
    isOpen: boolean;
    onClose: () => void;
  }

  let { isOpen, onClose }: Props = $props();

  let renderedContent = $state('');
  let isLoading = $state(false);
  let error = $state<string | null>(null);
  let copied = $state(false);

  const activeTab = $derived(configStore.activeTab);

  // Get language extension based on format
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

  const languageExtension = $derived(
    activeTab ? getLanguageExtension(activeTab.format) : undefined
  );

  // Fetch rendered content when modal opens
  $effect(() => {
    if (isOpen && activeTab) {
      fetchRendered();
    }
  });

  async function fetchRendered() {
    if (!activeTab) return;

    isLoading = true;
    error = null;

    try {
      // Call the render endpoint
      const response = await axios.post(`/api/v1/render/${activeTab.path}`, {
        content: activeTab.content,
        meta: activeTab.meta
      });
      
      // If the backend returns raw bytes, decode them (Unicode-safe)
      if (response.data.data) {
        try {
          const binaryStr = atob(response.data.data);
          const bytes = Uint8Array.from(binaryStr, (c: string) => c.charCodeAt(0));
          renderedContent = new TextDecoder().decode(bytes);
        } catch {
          renderedContent = response.data.data;
        }
      } else if (typeof response.data === 'string') {
        renderedContent = response.data;
      } else {
        renderedContent = JSON.stringify(response.data, null, 2);
      }
    } catch (err: any) {
      // If render endpoint doesn't exist yet, just show the current content
      if (err.response?.status === 404) {
        renderedContent = activeTab.content;
        error = 'Render endpoint not available. Showing current content.';
      } else {
        error = err.response?.data?.message || 'Failed to render configuration';
        renderedContent = '';
      }
    } finally {
      isLoading = false;
    }
  }

  async function copyToClipboard() {
    try {
      await navigator.clipboard.writeText(renderedContent);
      copied = true;
      setTimeout(() => {
        copied = false;
      }, 2000);
    } catch (err) {
      console.error('Failed to copy:', err);
    }
  }

  let mouseDownTarget: EventTarget | null = null;

  function handleBackdropMouseDown(e: MouseEvent) {
    mouseDownTarget = e.target;
  }

  function handleBackdropClick(e: MouseEvent) {
    if (e.target === e.currentTarget && mouseDownTarget === e.currentTarget) {
      onClose();
    }
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      onClose();
    }
  }
</script>

<svelte:window onkeydown={handleKeyDown} />

{#if isOpen}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div 
    class="fixed inset-0 bg-black/50 flex items-center justify-center z-[100] p-5"
    onmousedown={handleBackdropMouseDown}
    onclick={handleBackdropClick}
    onkeydown={(e) => e.key === 'Escape' && onClose()}
    role="dialog"
    aria-modal="true"
    aria-labelledby="modal-title"
    tabindex="-1"
  >
    <div class="bg-white dark:bg-slate-800 rounded-lg shadow-xl w-full max-w-[900px] max-h-[80vh] flex flex-col overflow-hidden">
      <div class="flex items-center justify-between px-5 py-4 border-b border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-900">
        <h2 id="modal-title" class="text-base font-semibold text-slate-800 dark:text-slate-200">
          Rendered Configuration
          {#if activeTab}
            <span class="font-normal text-slate-500 dark:text-slate-400">- {activeTab.name}</span>
          {/if}
        </h2>
        <div class="flex items-center gap-2">
          <button 
            class="flex items-center gap-1.5 px-3 py-1.5 bg-slate-100 dark:bg-slate-700 text-slate-600 dark:text-slate-300 border border-slate-200 dark:border-slate-600 rounded text-sm cursor-pointer transition-all duration-150 hover:bg-slate-200 dark:hover:bg-slate-600 disabled:opacity-50 disabled:cursor-not-allowed"
            onclick={copyToClipboard}
            disabled={!renderedContent || isLoading}
            title="Copy to clipboard"
          >
            {#if copied}
              <Check size={16} />
              <span>Copied!</span>
            {:else}
              <Copy size={16} />
              <span>Copy</span>
            {/if}
          </button>
          <button 
            class="flex items-center justify-center p-1.5 bg-transparent border-none rounded text-slate-500 dark:text-slate-400 cursor-pointer transition-all duration-150 hover:bg-slate-200 dark:hover:bg-slate-700 hover:text-slate-800 dark:hover:text-slate-200"
            onclick={onClose} 
            aria-label="Close modal"
          >
            <X size={20} />
          </button>
        </div>
      </div>

      <div class="flex-1 overflow-hidden min-h-[300px]">
        {#if isLoading}
          <div class="flex flex-col items-center justify-center h-full gap-3 text-slate-500 dark:text-slate-400">
            <Loader2 size={32} class="animate-spin" />
            <p>Rendering configuration...</p>
          </div>
        {:else if error}
          <div class="flex flex-col h-full">
            <p class="px-5 py-3 bg-red-50 dark:bg-red-900/30 text-red-600 dark:text-red-400 text-sm border-b border-red-200 dark:border-red-800">{error}</p>
            {#if renderedContent}
              <div class="flex-1 h-full [&_.cm-editor]:h-full">
                <CodeMirror
                  value={renderedContent}
                  lang={languageExtension}
                  theme={oneDark}
                  readonly={true}
                  styles={{
                    '&': {
                      height: '100%',
                      fontSize: '13px'
                    },
                    '.cm-content': {
                      fontFamily: "'JetBrains Mono', 'Fira Code', 'Monaco', 'Menlo', monospace"
                    }
                  }}
                />
              </div>
            {/if}
          </div>
        {:else}
          <div class="flex-1 h-full [&_.cm-editor]:h-full">
            <CodeMirror
              value={renderedContent}
              lang={languageExtension}
              theme={oneDark}
              readonly={true}
              styles={{
                '&': {
                  height: '100%',
                  fontSize: '13px'
                },
                '.cm-content': {
                  fontFamily: "'JetBrains Mono', 'Fira Code', 'Monaco', 'Menlo', monospace"
                }
              }}
            />
          </div>
        {/if}
      </div>

      <div class="flex items-center justify-between px-5 py-3 border-t border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-900">
        <span class="text-xs text-slate-500 dark:text-slate-400">
          {#if activeTab?.meta.inherits?.length}
            Inheriting from:
            {#each activeTab.meta.inherits as entry, i}
              <strong class="text-blue-500">{entry.source}</strong>{#if entry.inject}<span class="text-emerald-500 ml-0.5">->{entry.inject}</span>{/if}{#if i < activeTab.meta.inherits.length - 1}<span class="text-slate-400">,</span>{/if}
            {/each}
          {:else}
            No inheritance configured
          {/if}
        </span>
        <button 
          class="px-4 py-2 bg-slate-100 dark:bg-slate-700 text-slate-600 dark:text-slate-300 border border-slate-200 dark:border-slate-600 rounded text-sm cursor-pointer transition-all duration-150 hover:bg-slate-200 dark:hover:bg-slate-600"
          onclick={onClose}
        >
          Close
        </button>
      </div>
    </div>
  </div>
{/if}
