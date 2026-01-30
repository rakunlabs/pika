<script lang="ts">
  import { removeToast, storeToast } from '@/lib/store/toast.svelte';
  import { ChevronRight } from 'lucide-svelte';

  const close = (id: number) => {
    removeToast(id);
  };
  const customSlide = (_: HTMLElement, { duration }: { duration: number }) => {
    return {
      duration,
      css: (_: number, u: number) => `transform: translateX(${u * 400}px)`
    };
  };
</script>

<div class="fixed bottom-0 right-0 z-50">
  {#each storeToast as toast (toast.id)}
    <div
      class={`${toast.type} flex p-2 h-12 items-center border-l border-t border-gray-700 w-[28rem]`}
      transition:customSlide={{ duration: 250 }}
    >
      <button onclick={() => close(toast.id)} class="hover:fill-red-500">
        <ChevronRight />
      </button>
      <div class="pl-2">
        <span>{toast.message}</span>
      </div>
    </div>
  {/each}
</div>

<style>
  @reference "tailwindcss";

  .alert {
    @apply bg-red-100 text-red-800;
  }

  .info {
    @apply bg-teal-100 text-teal-800;
  }

  .warn {
    @apply bg-yellow-100 text-yellow-800;
  }
</style>
