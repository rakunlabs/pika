<script lang="ts">
 import { X, Copy, Check, Loader2, Eye, EyeOff } from 'lucide-svelte';
 import AppCodeMirror from '@/lib/editor/AppCodeMirror.svelte';
 import { json } from '@codemirror/lang-json';
 import { yaml } from '@codemirror/lang-yaml';
 import { StreamLanguage, LanguageSupport } from '@codemirror/language';
 import { toml } from '@codemirror/legacy-modes/mode/toml';
 import type { EditorView } from '@codemirror/view';
 import { configStore } from '@/lib/store/config.svelte';
 import type { FileFormat } from '@/lib/types/config';
 import axios from 'axios';
 import { createMaskExtension, createMaskWatcher, setMaskEnabled, type MaskInfo } from './maskExtension';

 interface Props {
 isOpen: boolean;
 onClose: () => void;
 }

 let { isOpen, onClose }: Props = $props();

 let renderedContent = $state('');
 let isLoading = $state(false);
 let error = $state<string | null>(null);
 let copied = $state(false);
 let maskInfo: MaskInfo = $state({ enabled: true, hasReveals: false });
 const fullyMasked = $derived(maskInfo.enabled && !maskInfo.hasReveals);
 let cmView: EditorView | undefined = $state();

 const activeTab = $derived(configStore.activeTab);
 const previewFormat = $derived<FileFormat>(activeTab?.format ?? 'raw');
 const canMask = $derived(previewFormat !== 'raw');

 // Stable watcher reference, created once.
 const maskWatcher = createMaskWatcher((info) => { maskInfo = info; });
 const maskExtensions = $derived([createMaskExtension(previewFormat), maskWatcher]);

 // Re-tape every time the modal opens.
 $effect(() => {
 if (isOpen && cmView) setMaskEnabled(cmView, true);
 });

 function toggleMask() {
 if (!cmView) return;
 setMaskEnabled(cmView, !fullyMasked);
 }

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
 // Call the render endpoint. For variant tabs we pass `variant`
 // so the backend can disambiguate the file identity when seeding
 // the inheritance cycle guard — without it, a variant that
 // (auto-)inherits its parent would be mis-detected as a cycle.
 const response = await axios.post(`/api/v1/render/${activeTab.path}`, {
 content: activeTab.content,
 meta: activeTab.meta
 }, {
 params: activeTab.variantKey ? { variant: activeTab.variantKey } : {}
 });
 
 // Check for parse/conversion errors from backend
 if (response.data.error) {
 error = response.data.error;
 }

 // If the backend returns raw bytes, decode them (Unicode-safe).
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

 // The backend's JSON renderer emits compact, one-line output —
 // useful for transport, unreadable in the preview pane. Re-format
 // with 2-space indentation when the active tab is JSON. We don't
 // touch yaml/toml/raw: yaml/toml have their own canonical layout
 // the renderer is already responsible for, and raw is opaque.
 // Failed parse leaves the content as-is so an invalid render is
 // still inspectable verbatim.
 if (previewFormat === 'json' && renderedContent) {
 try {
 renderedContent = JSON.stringify(JSON.parse(renderedContent), null, 2);
 } catch {
 // Leave compact / malformed output alone — the editor still
 // renders it; reformatting a broken doc would mask the bug.
 }
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
 class="fixed inset-0 bg-black/50 flex items-center justify-center z-[100] p-4"
 onmousedown={handleBackdropMouseDown}
 onclick={handleBackdropClick}
 onkeydown={(e) => e.key === 'Escape' && onClose()}
 role="dialog"
 aria-modal="true"
 aria-labelledby="modal-title"
 tabindex="-1"
 >
 <div class="bg-white dark:bg-warm-900 rounded-lg shadow-xl w-full max-w-[900px] max-h-[85vh] flex flex-col overflow-hidden">
 <!-- Header: title + action buttons. `flex-wrap` so narrow viewports
      (or extra action buttons in the future) spill onto a second
      row instead of crushing the title into the buttons. `gap-y-2`
      gives the wrapped row a little breathing space without
      affecting the normal single-line layout. The file name used
      to live on this title too ("Rendered Configuration — foo");
      removed because the active tab strip already shows it and
      the duplication just stole horizontal room. -->
 <div class="flex flex-wrap items-center justify-between gap-x-3 gap-y-2 px-4 py-2 border-b border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900">
 <h2 id="modal-title" class="text-sm font-semibold text-slate-800 dark:text-slate-200">
 Rendered Configuration
 </h2>
 <div class="flex items-center gap-1.5 flex-wrap">
 {#if canMask}
 <button
 class="flex items-center gap-1 px-2 py-1 border rounded text-xs cursor-pointer transition-all duration-150
 {fullyMasked
 ? 'bg-amber-50 dark:bg-amber-900/30 text-amber-700 dark:text-amber-300 border-amber-200 dark:border-amber-700 hover:bg-amber-100 dark:hover:bg-amber-900/50'
 : 'bg-slate-100 dark:bg-warm-900 text-slate-600 dark:text-slate-300 border-slate-200 dark:border-warm-500 hover:bg-slate-200 dark:hover:bg-warm-600'}"
 onclick={toggleMask}
 title={fullyMasked ? 'Reveal all values' : 'Mask all values'}
 >
 {#if fullyMasked}
 <EyeOff size={14} />
 <span>Masked</span>
 {:else}
 <Eye size={14} />
 <span>Visible</span>
 {/if}
 </button>
 {/if}
 <button 
 class="flex items-center gap-1 px-2 py-1 bg-slate-100 dark:bg-warm-900 text-slate-600 dark:text-slate-300 border border-slate-200 dark:border-warm-500 rounded text-xs cursor-pointer transition-all duration-150 hover:bg-slate-200 dark:hover:bg-warm-600 disabled:opacity-50 disabled:cursor-not-allowed"
 onclick={copyToClipboard}
 disabled={!renderedContent || isLoading}
 title="Copy to clipboard"
 >
 {#if copied}
 <Check size={14} />
 <span>Copied!</span>
 {:else}
 <Copy size={14} />
 <span>Copy</span>
 {/if}
 </button>
 <button 
 class="flex items-center justify-center p-1 bg-transparent border-none rounded text-slate-500 dark:text-slate-400 cursor-pointer transition-all duration-150 hover:bg-slate-200 dark:hover:bg-warm-700 hover:text-slate-800 dark:hover:text-slate-200"
 onclick={onClose} 
 aria-label="Close modal"
 >
 <X size={16} />
 </button>
 </div>
 </div>

 <div class="flex-1 min-h-0 flex flex-col min-h-[300px]">
 {#if isLoading}
 <div class="flex flex-col items-center justify-center h-full gap-3 text-slate-500 dark:text-slate-400">
 <Loader2 size={32} class="animate-spin" />
 <p>Rendering configuration...</p>
 </div>
  {:else if error}
 <p class="px-4 py-2 bg-red-50 dark:bg-red-900/30 text-red-600 dark:text-red-400 text-xs border-b border-red-200 dark:border-red-800 shrink-0">{error}</p>
 {#if renderedContent}
 <div class="flex-1 min-h-0 overflow-auto">
 <AppCodeMirror
 value={renderedContent}
 onready={(view: EditorView) => { cmView = view; }}
 onreconfigure={(view: EditorView) => { cmView = view; }}
 lang={languageExtension}
 extensions={maskExtensions}
 readonly={true}
 hideGutter
 />
 </div>
 {/if}
 {:else}
 <div class="flex-1 min-h-0 overflow-auto">
 <AppCodeMirror
 value={renderedContent}
 onready={(view: EditorView) => { cmView = view; }}
 onreconfigure={(view: EditorView) => { cmView = view; }}
 lang={languageExtension}
 extensions={maskExtensions}
 readonly={true}
 hideGutter
 />
 </div>
 {/if}
 </div>

  <div class="flex items-center justify-between px-4 py-1.5 border-t border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900">
  <!-- Inheritance summary line. Entries can be one of three kinds —
       internal (source), external (resource + optional path), or
       raw mount (mount + optional path). The previous version only
       printed entry.source, so external/mount entries rendered as
       an empty <strong> and the footer looked like "Inheriting from:"
       with nothing after the colon — exactly the symptom that made
       working inheritance look broken from the UI side. -->
  <span class="text-[11px] text-slate-500 dark:text-slate-400">
 {#if activeTab?.meta.inherits?.length}
 Inheriting from:
 {#each activeTab.meta.inherits as entry, i}
 {@const label =
   entry.mount
     ? (entry.path ? `${entry.mount}/${entry.path}` : entry.mount)
     : entry.resource
       ? (entry.path ? `${entry.resource}:${entry.path}` : entry.resource)
       : (entry.source || '(empty)')}
 {@const kind = entry.mount ? 'mount' : entry.resource ? 'ext' : 'src'}
 <span class="ml-1 inline-flex items-baseline gap-1">
   <span class="px-1 py-px text-[9px] font-medium uppercase tracking-wider rounded
     {kind === 'mount'
       ? 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-200'
       : kind === 'ext'
         ? 'bg-purple-100 text-purple-700 dark:bg-purple-900/40 dark:text-purple-200'
         : 'bg-slate-100 text-slate-600 dark:bg-warm-700 dark:text-warm-200'}">{kind}</span>
   <strong class="text-brand-500 dark:text-brand-300 font-mono">{label}</strong>
   {#if entry.format}<span class="text-slate-400 dark:text-warm-400">as {entry.format}</span>{/if}
   {#if entry.inject}<span class="text-emerald-500">-&gt;{entry.inject}</span>{/if}
 </span>{#if i < activeTab.meta.inherits.length - 1}<span class="text-slate-400 dark:text-slate-500">,</span>{/if}
 {/each}
 {:else}
 No inheritance configured
 {/if}
 </span>
 <button 
 class="px-3 py-1 bg-slate-100 dark:bg-warm-900 text-slate-600 dark:text-slate-300 border border-slate-200 dark:border-warm-500 rounded text-xs cursor-pointer transition-all duration-150 hover:bg-slate-200 dark:hover:bg-warm-600"
 onclick={onClose}
 >
 Close
 </button>
 </div>
 </div>
 </div>
{/if}
