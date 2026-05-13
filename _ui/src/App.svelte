<script lang="ts">
 import Router from "svelte-spa-router";
 import Navbar from "@/lib/components/Navbar.svelte";
 import Toast from "@/lib/components/Toast.svelte";
 import Login from "@/pages/Login.svelte";
 import routes from "@/routes";
 import { appStore } from "@/lib/store/store.svelte";
 import { prefsStore } from "@/lib/store/prefs.svelte";
 import { vaultStore } from "@/lib/vault/store.svelte";

 $effect.root(() => {
 // boot-time: load info, identity and preferences in parallel.
 // Preferences require auth; on a 401 the store silently falls back
 // to defaults and the post-login flow re-fetches it.
 appStore.loadInfo();
 appStore.loadIdentity();
 prefsStore.loadPreferences();
 });

 const authenticated = $derived(appStore.authenticated);
 const loading = $derived(authenticated === null);

 // Vault session safety. Hooks installed once the user is logged in:
 //   - activity watcher resets the idle auto-lock timer on any input
 //     (kept at App level so navigating between pages doesn't pause
 //     the timer's input-driven reset)
 //   - lock() runs when the document becomes hidden (tab switch /
 //     window minimized) or the window loses focus (Alt+Tab to
 //     another app) — the "real coffee-break" trigger.
 // Within-app navigation does NOT lock the vault; lock() is a no-op
 // when already locked so installing these unconditionally is safe.
 $effect(() => {
  if (authenticated !== true) return;
  const stopWatcher = vaultStore.installActivityWatcher();
  const onVisibility = () => {
   if (document.hidden) vaultStore.lock();
  };
  const onBlur = () => vaultStore.lock();
  document.addEventListener('visibilitychange', onVisibility);
  window.addEventListener('blur', onBlur);
  return () => {
   stopWatcher();
   document.removeEventListener('visibilitychange', onVisibility);
   window.removeEventListener('blur', onBlur);
  };
 });
</script>

<Toast />

{#if loading}
 <!-- Loading state while checking auth -->
 <div class="flex items-center justify-center h-full w-full bg-slate-100 dark:bg-warm-900">
 <div class="text-sm text-slate-400 dark:text-slate-500">Loading...</div>
 </div>
{:else if authenticated === false}
 <Login />
{:else if authenticated === true}
 <div class="flex flex-col h-full w-full overflow-hidden bg-slate-100 dark:bg-warm-900 text-slate-800 dark:text-slate-100">
 <Navbar />
 <div class="flex-1 overflow-hidden">
 <Router {routes} />
 </div>
 </div>
{/if}
