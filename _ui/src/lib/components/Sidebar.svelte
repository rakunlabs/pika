<script lang="ts">
  import { link, location } from 'svelte-spa-router';
</script>

<div class="sidebar-bg border-r border-black">
  <div class="sticky top-0 overflow-auto max-h-svh no-scrollbar">
    <div class="border-b border-black h-7">
      <a
        href="/"
        use:link
        class={[
          'block h-full hover:bg-slate-700 hover:text-white',
          $location === '/' ? 'bg-black text-white' : 'bg-white text-black'
        ]}
      >
        <span class="block px-2">Main</span>
      </a>
    </div>
    <div class="border-b border-black h-7">
      <a
        href="/test"
        use:link
        class={[
          'block h-full hover:bg-slate-700 hover:text-white',
          $location === '/test' ? 'bg-black text-white' : 'bg-white text-black'
        ]}
      >
        <span class="block px-2">Test</span>
      </a>
    </div>
  </div>
</div>

<style>
  @reference "tailwindcss";

  .sidebar-bg {
    background: url(data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAoAAAAKCAYAAACNMs+9AAAAAXNSR0IArs4c6QAAAB1JREFUKFNjvHjx4n99fX1GBgKAcVQhvhCifvAAAM43KAsXWPfwAAAAAElFTkSuQmCC)
      repeat;
  }

  /* Hide scrollbar for Chrome, Safari and Opera */
  .no-scrollbar::-webkit-scrollbar {
    display: none;
  }

  /* Hide scrollbar for IE, Edge and Firefox */
  .no-scrollbar {
    -ms-overflow-style: none; /* IE and Edge */
    scrollbar-width: none; /* Firefox */
  }
</style>
