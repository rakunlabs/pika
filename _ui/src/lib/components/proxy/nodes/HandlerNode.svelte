<script lang="ts">
 // Handler node — terminal of the proxy chain. Accepts either an
 // http-stream input (when wired directly after middleware) or a
 // route input (when fed from a router); kaykay's accept lets one
 // input handle multiple port types so the same node component
 // works in both topologies.
 //
 // Emerald stem because every handler is a "success path" terminal
 // — the user reads from left (listener, white) through middleware
 // (white/teal accents) to the green output that actually serves
 // the response.
 import { Handle } from 'kaykay';
 import type { NodeProps } from 'kaykay';
 import { Boxes } from 'lucide-svelte';

 let { data, selected }: NodeProps<{
  subtype: string;
  protocol?: 'http' | 'tcp';
  label?: string;
  path?: string;
  hasError?: boolean;
  }> = $props();
 const protocol = $derived(data.protocol ?? 'http');
 const streamPort = $derived(protocol === 'tcp' ? 'tcp-stream' : 'http-stream');
 const accepts = $derived(protocol === 'tcp' ? ['tcp-stream'] : ['http-stream', 'route']);
</script>

<div
 class="proxy-node bg-emerald-50 dark:bg-emerald-950/40
        text-emerald-900 dark:text-emerald-100
        border {data.hasError
         ? 'border-vermilion-500 dark:border-vermilion-500'
         : 'border-emerald-300 dark:border-emerald-700'}
        {selected ? 'ring-2 ring-accent-500 ring-offset-2 ring-offset-slate-100 dark:ring-offset-warm-950' : ''}"
>
 <div class="flex items-center gap-1.5">
  <Boxes size={12} />
  <span class="font-semibold text-sm truncate">{data.label ?? data.subtype}</span>
 </div>
 <div class="mt-1">
   <span class="text-[10px] font-mono opacity-80">{protocol}:{data.subtype}</span>
  {#if data.path}
   <span class="block text-[10px] font-mono opacity-80 mt-0.5">{data.path}</span>
  {/if}
 </div>
  <Handle id="in" type="input" port={streamPort} accept={accepts} position="left" />
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
