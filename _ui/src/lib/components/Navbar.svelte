<script lang="ts">
  import { link, router } from 'svelte-spa-router';
  import { appStore } from '@/lib/store/store.svelte';
  import { Blocks, Settings, User, Users, LogOut, HardDrive } from 'lucide-svelte';

  const info = $derived(appStore.info);
  const authEnabled = $derived(info?.auth_enabled ?? false);

  const hasRawMounts = $derived((info?.raw_mounts?.length ?? 0) > 0);

  const navItems = $derived.by(() => {
    const items: { path: string; label: string; icon: typeof Settings }[] = [];

    if (hasRawMounts) {
      items.push({ path: '/files', label: 'Files', icon: HardDrive });
    }

    items.push({ path: '/settings', label: 'Settings', icon: Settings });

    if (authEnabled) {
      items.push({ path: '/users', label: 'Users', icon: Users });
    }

    return items;
  });

  async function handleLogout() {
    await appStore.logout();
  }
</script>

<nav class="flex items-center h-10 bg-slate-900 text-white border-b border-slate-700 px-4 shrink-0">
  <!-- Logo / Brand -->
  <a href="/configurations" use:link class="flex items-center gap-2 mr-6 hover:opacity-80 transition-opacity no-underline text-white">
    <Blocks size={18} color="#EF233C" />
    <div class="flex flex-col leading-none">
      <span class="text-sm font-bold tracking-wide">pika</span>
      {#if info}
        <span class="text-[9px] font-mono text-slate-500">{info.version}</span>
      {/if}
    </div>
  </a>

  <!-- Nav Links -->
  <div class="flex items-center gap-1">
    {#each navItems as item (item.path)}
      {@const isActive = router.location === item.path || (router.location === '/' && item.path === '/configurations')}
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
      {#if authEnabled}
        <button
          onclick={handleLogout}
          class="flex items-center gap-1 px-1.5 py-0.5 bg-slate-800 rounded text-slate-400 hover:text-white hover:bg-slate-700 transition-colors cursor-pointer border-none"
          title="Sign out"
        >
          <LogOut size={11} />
          <span>Logout</span>
        </button>
      {/if}
      {#if info.commit && info.commit !== '-'}
        <span class="px-1.5 py-0.5 bg-slate-800 rounded font-mono text-slate-400" title="Commit: {info.commit}">
          {info.commit}
        </span>
      {/if}

    </div>
  {/if}
</nav>
