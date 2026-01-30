<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { ChevronRight, Menu } from 'lucide-svelte';
</script>

<div class="bg-black text-white flex items-center border-b border-gray-500">
  <button
    class="mr-2 hover:bg-slate-700 flex items-center h-full px-1 hover:cursor-pointer"
    onclick={() => (storeNavbar.sideBarOpen = !storeNavbar.sideBarOpen)}
  >
    {#if storeNavbar.sideBarOpen}
      <ChevronRight />
    {:else}
      <Menu />
    {/if}
  </button>
  <span>
    {storeNavbar.title}
  </span>
</div>
