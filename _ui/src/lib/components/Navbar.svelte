<script lang="ts">
 import { link, router } from 'svelte-spa-router';
 import { appStore } from '@/lib/store/store.svelte';
  import { Blocks, Settings, User, Users, LogOut, HardDrive, FileSliders, Lock, Globe } from 'lucide-svelte';
 import ThemeSwitcher from '@/lib/components/ThemeSwitcher.svelte';

 const info = $derived(appStore.info);
 const identity = $derived(appStore.identity);

  const hasRawMounts = $derived((info?.raw_mounts?.length ?? 0) > 0);
  // VaultEnabled is set by /api/v1/info from the server-side vault
  // coordinator. The /vault page renders an "unavailable" fallback
  // when this is false, but we hide the nav entry entirely so the
  // user isn't tempted to click a dead link.
  const vaultEnabled = $derived(info?.vault_enabled ?? false);

  const navItems = $derived.by(() => {
  const items: { path: string; label: string; icon: typeof Settings }[] = [];

  if (appStore.hasPermission('files.read')) {
  items.push({ path: '/configurations', label: 'Configurations', icon: FileSliders });
  }

  if (hasRawMounts && appStore.hasPermission('raw.read')) {
  items.push({ path: '/files', label: 'Files', icon: HardDrive });
  }

  // Personal vault is per-user; no capability gate (every logged-in
   // user owns their own vault). The link is only hidden when the
   // feature is server-disabled.
   if (vaultEnabled) {
   items.push({ path: '/vault', label: 'Vault', icon: Lock });
   }

   // External resources page: dedicated list/view/edit/test surface for
   // configured external backends (Vault, K8s, Consul, etcd, AWS, GCP,
   // Azure, HTTP). Same capability as the in-Settings section that
   // also exposes the same data, so hide the nav entry for users who
   // would only see a 403 banner anyway.
   if (appStore.hasPermission('settings.manage')) {
   items.push({ path: '/external', label: 'External', icon: Globe });
   }

  // Settings is always visible: even users without settings.manage can
  // reach the About section. Individual sections gate themselves inside
  // the Settings page based on their own capability.
  items.push({ path: '/settings', label: 'Settings', icon: Settings });

 // Users page hosts both the Users tab (users.manage) and Permissions
 // tab (permissions.manage). Show the nav entry if the user has either.
 if (appStore.hasAnyPermission('users.manage', 'permissions.manage')) {
 items.push({ path: '/users', label: 'Users', icon: Users });
 }

 return items;
 });

 async function handleLogout() {
 await appStore.logout();
 }
</script>

<nav class="flex items-center h-10 bg-warm-900 text-white border-b border-warm-700 px-4 shrink-0">
 <!-- Logo / Brand. The line under "pika" carries the editable
 subtitle from settings (mirrors the login card). Version moved
 out of the logo cluster to the right side, next to the user
 pill — keeping operator-editable branding and build metadata
 visually separated. -->
 <div class="flex items-center gap-2 mr-6 text-white">
 <Blocks size={18} color="#EF233C" />
 <div class="flex flex-col leading-none">
 <span class="text-sm font-bold tracking-wide">pika</span>
 {#if info?.subtitle}
  <span class="text-[9px] text-warm-300">{info.subtitle}</span>
 {/if}
 </div>
 </div>

 <!-- Nav Links -->
 <div class="flex items-center gap-1">
 {#each navItems as item (item.path)}
 {@const isActive = router.location === item.path || (router.location === '/' && item.path === '/configurations')}
 <a
 href={item.path}
 use:link
    class="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs font-medium no-underline transition-colors
    {isActive
    ? 'bg-accent-600 text-white'
    : 'text-warm-200 hover:text-white hover:bg-warm-700'}"
 >
 <item.icon size={14} />
 {item.label}
 </a>
 {/each}
 </div>

 <!-- Spacer -->
 <div class="flex-1"></div>

 <!-- User + Logout: sized to match the nav links on the left
 (text-xs / size=14 / px-3 py-1.5) so the entire navbar reads as
 one consistent row instead of having a slightly smaller cluster
 on the right. -->
 <div class="flex items-center gap-1 text-warm-300">
  {#if info?.version}
  <!-- Build version sits just left of the user pill. Bare text
  (no pill background) keeps the build-metadata visually
  subordinate to the user identity it sits next to. -->
  <span class="px-2 text-[10px] font-mono text-warm-300 select-text">{info.version}</span>
  {/if}
  {#if identity}
  <span class="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs font-medium bg-warm-700 text-warm-100">
  <User size={14} />
  {identity.name ?? identity.subject}
  </span>
  <button
  onclick={handleLogout}
  class="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs font-medium bg-warm-700 text-warm-200 hover:text-white hover:bg-warm-600 transition-colors cursor-pointer border-none"
  title="Sign out"
  >
  <LogOut size={14} />
  <span>Logout</span>
  </button>
  {/if}
  <!-- Theme switcher: same local-only toggle exposed inside the app.
  The dark variant blends into the navbar's warm-900 background. -->
  <ThemeSwitcher variant="dark" class="ml-1" />
  </div>
</nav>
