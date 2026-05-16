<script lang="ts">
 import Router from "svelte-spa-router";
 import Navbar from "@/lib/components/Navbar.svelte";
 import Toast from "@/lib/components/Toast.svelte";
 import UnlockScreen from "@/lib/components/UnlockScreen.svelte";
 import Login from "@/pages/Login.svelte";
 import routes from "@/routes";
 import { appStore } from "@/lib/store/store.svelte";
 import { prefsStore } from "@/lib/store/prefs.svelte";
 import { vaultStore } from "@/lib/vault/store.svelte";
 import { keymgrStore } from "@/lib/store/keymgr.svelte";

 $effect.root(() => {
 // boot-time: load info, identity, preferences AND server-key
 // status in parallel. The key status is on the lockgate
 // allowlist so it succeeds even when the server is locked; the
 // other three may legitimately fail with 503 in that case but
 // their stores all degrade gracefully.
 appStore.loadInfo();
 appStore.loadIdentity();
 prefsStore.loadPreferences();
 keymgrStore.refreshStatus();
 });

 const authenticated = $derived(appStore.authenticated);
 const loading = $derived(authenticated === null);

 // Server-lock state. The unlock takeover is triggered ONLY when
 // the operator has previously opted in to at-rest encryption
 // (verifier exists on disk = initialized) AND the current process
 // hasn't yet been unlocked. Fresh installs (initialized=false)
 // render the normal app shell so the operator can use the system
 // before deciding to turn on encryption; the opt-in lives under
 // Settings → Server encryption key.
 //
 // We treat "status === null" (haven't fetched yet) as "not locked"
 // so the boot loader doesn't flash an UnlockScreen before the
 // status round-trips.
 const serverLocked = $derived(
   keymgrStore.status !== null
     && keymgrStore.status.initialized
     && !keymgrStore.status.unlocked
 );

 // Vault session safety. Hooks installed once the user is logged in:
 //   - activity watcher resets the idle auto-lock timer on any input
 //     (kept at App level so navigating between pages doesn't pause
 //     the timer's input-driven reset)
 //   - visibility changes are forwarded to the store, which decides
 //     whether to lock immediately, schedule a delayed lock (the
 //     hidden-grace window — gives the user a few seconds to come
 //     back from another tab), or do nothing at all. The grace
 //     length is per-device and configurable in Settings → Vault.
 //
 // We intentionally do NOT listen to window 'blur'. That event fires
 // for any focus departure — native alert()/confirm()/prompt(), file
 // pickers, print dialogs, the address bar, devtools — and would
 // lock the vault on essentially every interactive modal. The
 // visibilitychange path already covers tab-switch and minimize,
 // which is the actual threat model. Longer absences are handled by
 // the idle auto-lock timer (session_lock_seconds).
 //
 // Within-app navigation does NOT lock the vault; lock() is a no-op
 // when already locked so installing these unconditionally is safe.
 $effect(() => {
  if (authenticated !== true) return;
  const stopWatcher = vaultStore.installActivityWatcher();
  const onVisibility = () => {
   vaultStore.notifyVisibilityChange(document.hidden);
  };
  document.addEventListener('visibilitychange', onVisibility);
  return () => {
   stopWatcher();
   document.removeEventListener('visibilitychange', onVisibility);
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
 <!-- Server-lock takeover. Authenticated user but server has no
      live key — show the unlock/initialize screen on top of
      everything else. We deliberately render the normal app shell
      UNDER it (rather than swapping it out) so re-locks don't tear
      down route state; closing the overlay just unmounts it.
      The fixed inset-0 + z-50 in UnlockScreen handles the visual
      cover. -->
 {#if serverLocked}
   <UnlockScreen />
 {:else}
   <div class="flex flex-col h-full w-full overflow-hidden bg-slate-100 dark:bg-warm-900 text-slate-800 dark:text-slate-100">
   <Navbar />
   <div class="flex-1 overflow-hidden">
   <Router {routes} />
   </div>
   </div>
 {/if}
{/if}
