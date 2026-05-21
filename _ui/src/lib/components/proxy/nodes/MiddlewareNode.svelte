<script lang="ts">
 // Middleware node — one transformation in the request pipeline.
 // Subtype is shown as a mono tag below the friendly label so the
 // operator can tell at a glance "this is the cors one" vs "this
 // is the ratelimit one" without opening the config panel.
 //
 // hasError flips on when the server's validate endpoint returns a
 // CompileError referencing this node — the border turns vermilion
 // (DESIGN_SYSTEM destructive stem).
 import { Handle } from 'kaykay';
 import type { NodeProps } from 'kaykay';
 import { Layers } from 'lucide-svelte';

 let { data, selected }: NodeProps<{
  subtype: string;
  protocol?: 'http' | 'tcp';
  label?: string;
  hasError?: boolean;
  }> = $props();
 const protocol = $derived(data.protocol ?? 'http');
 const streamPort = $derived(protocol === 'tcp' ? 'tcp-stream' : 'http-stream');
 const iconClass = $derived(protocol === 'tcp'
  ? 'text-violet-600 dark:text-violet-400'
  : 'text-accent-600 dark:text-accent-400');
</script>

<div
 class="proxy-node bg-white dark:bg-warm-800
        text-slate-800 dark:text-slate-100
        border {data.hasError
         ? 'border-vermilion-500 dark:border-vermilion-500'
         : 'border-slate-300 dark:border-warm-600'}
        {selected ? 'ring-2 ring-accent-500 ring-offset-2 ring-offset-slate-100 dark:ring-offset-warm-950' : ''}"
>
 <div class="flex items-center gap-1.5">
   <Layers size={12} class={iconClass} />
   <span class="font-semibold text-sm truncate">{data.label ?? data.subtype}</span>
 </div>
 <div class="mt-1">
   <span class="text-[10px] font-mono text-slate-500 dark:text-slate-400">{protocol}:{data.subtype}</span>
  </div>
  <Handle id="in" type="input" port={streamPort} position="left" />
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
