<!--
  CollapsibleCard — tier-1 settings card with a full-width header button
  that toggles the body. Used by the Authentication settings sections.
-->
<script lang="ts">
  import type { Snippet } from "svelte";
  import { ChevronDown, ChevronRight } from "lucide-svelte";

  type Props = {
    title: string;
    open: boolean;
    onToggle: () => void;
    /** Classes for the body wrapper (padding/spacing differ per section). */
    bodyClass?: string;
    children?: Snippet;
  };

  let {
    title,
    open,
    onToggle,
    bodyClass = "px-5 pb-5 pt-1 space-y-3 border-t border-slate-100 dark:border-warm-700",
    children,
  }: Props = $props();

  const uid = $props.id();
</script>

<div
  class="mb-3 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm overflow-hidden"
>
  <button
    type="button"
    class="w-full flex items-center justify-between px-5 py-3 text-sm font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-50 dark:hover:bg-warm-700 transition-colors cursor-pointer"
    aria-expanded={open}
    aria-controls="{uid}-body"
    onclick={onToggle}
  >
    {title}
    {#if open}<ChevronDown size={15} />{:else}<ChevronRight size={15} />{/if}
  </button>
  {#if open}
    <div id="{uid}-body" class={bodyClass}>
      {@render children?.()}
    </div>
  {/if}
</div>
