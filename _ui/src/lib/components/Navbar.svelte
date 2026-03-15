<script lang="ts">
  import { link, location } from 'svelte-spa-router';
  import { appStore } from '@/lib/store/store.svelte';
  import { onMount } from 'svelte';
  import { Boxes, Settings, Database, User } from 'lucide-svelte';

  onMount(() => {
    appStore.loadInfo();
  });

  const info = $derived(appStore.info);

  const navItems = [
    { path: '/configurations', label: 'Configurations', icon: Database },
    { path: '/settings', label: 'Settings', icon: Settings },
  ];
</script>

<nav class="flex items-center h-10 bg-slate-900 text-white border-b border-slate-700 px-4 shrink-0">
  <!-- Logo / Brand -->
  <a href="/configurations" use:link class="flex items-center gap-2 mr-6 hover:opacity-80 transition-opacity no-underline text-white">
    <Boxes size={18} class="text-blue-400" />
    <span class="text-sm font-bold tracking-wide">pika</span>
  </a>

  <!-- Nav Links -->
  <div class="flex items-center gap-1">
    {#each navItems as item (item.path)}
      {@const isActive = $location === item.path || ($location === '/' && item.path === '/configurations')}
      <a
        href={item.path}
        use:link
        class="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs font-medium no-underline transition-colors
          {isActive
            ? 'bg-slate-700 text-white'
            : 'text-slate-400 hover:text-white hover:bg-slate-800'}"
      >
        <item.icon size={14} />
        {item.label}
      </a>
    {/each}
  </div>

  <!-- Spacer -->
  <div class="flex-1"></div>

  <!-- User + Version Info -->
  {#if info}
    <div class="flex items-center gap-2 text-[11px] text-slate-500">
      {#if info.user && info.user !== 'system'}
        <span class="flex items-center gap-1 px-1.5 py-0.5 bg-slate-800 rounded text-slate-300">
          <User size={11} />
          {info.user}
        </span>
      {/if}
      {#if info.commit && info.commit !== '-'}
        <span class="px-1.5 py-0.5 bg-slate-800 rounded font-mono text-slate-400" title="Commit: {info.commit}">
          {info.commit}
        </span>
      {/if}
      <span class="px-1.5 py-0.5 bg-blue-900/50 text-blue-300 rounded font-mono">
        {info.version}
      </span>
    </div>
  {/if}
</nav>
