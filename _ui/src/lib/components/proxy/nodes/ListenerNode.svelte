<script lang="ts">
 // Listener node — single source of every proxy graph. Exposes one
 // outgoing handle of port "http-stream"; ports are how kaykay
 // validates connections so the type names here must match the
 // input ports declared on middleware / router / handler nodes.
 //
 // Colours track DESIGN_SYSTEM: warm-* for dark surfaces, slate-*
 // for light. The selection ring uses accent (teal) to match
 // every other "current item" indicator across the app.
 import { Handle } from 'kaykay';
 import type { NodeProps } from 'kaykay';
 import { Radio } from 'lucide-svelte';

 let { data, selected }: NodeProps<{ label?: string; port?: string; protocol?: 'http' | 'tcp' }> = $props();
 const protocol = $derived(data.protocol ?? 'http');
 const streamPort = $derived(protocol === 'tcp' ? 'tcp-stream' : 'http-stream');
</script>

<div
 class="proxy-node bg-white dark:bg-warm-800
        border border-slate-300 dark:border-warm-600
        text-slate-800 dark:text-slate-100
        {selected ? 'ring-2 ring-accent-500 ring-offset-2 ring-offset-slate-100 dark:ring-offset-warm-950' : ''}"
>
 <div class="flex items-center gap-1.5">
  <Radio size={12} class="text-accent-600 dark:text-accent-400" />
   <span class="font-semibold text-sm">{protocol.toUpperCase()} Listener</span>
 </div>
 <div class="flex justify-between items-baseline mt-1">
  <span class="text-[10px] text-slate-500 dark:text-slate-400">port</span>
  <span class="text-xs font-mono">{data.port ?? '—'}</span>
 </div>
  <Handle id="out" type="output" port={streamPort} position="right" />
</div>

<style>
 .proxy-node {
  border-radius: 6px;
  padding: 6px 10px;
  min-width: 140px;
  font-size: 12px;
  box-shadow: 0 1px 2px rgba(0,0,0,0.08);
 }
</style>
