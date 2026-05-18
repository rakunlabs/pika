<script lang="ts">
 import { configStore } from "@/lib/store/config.svelte";
 import { appStore } from "@/lib/store/store.svelte";
 import { onMount } from "svelte";
 import { KeyRound, AlertTriangle, Network } from "lucide-svelte";

 // Features section — deployment-wide feature flags.
 //
 // Renamed from "Security" because the panel ended up holding
 // capability-style toggles ("personal vault on/off") rather than
 // real security controls. Every flag here is a deployment-wide
 // decision (not per-user) and gets its own bordered card.

 // ── Personal vault feature toggle ──
 let vaultDisabledDraft = $state(false);
 let vaultBusy = $state(false);

 // ── Proxy feature toggle ──
 //
 // Hides the /proxy route from the SPA and turns every
 // /api/v1/proxy/* endpoint into a 404. Existing graphs in
 // settings.proxy_servers are preserved — re-enabling spins
 // every previously enabled listener back up.
 let proxyDisabledDraft = $state(false);
 let proxyBusy = $state(false);

 async function loadToggles() {
  await configStore.loadSettings();
  vaultDisabledDraft = configStore.settings?.vault?.disabled === true;
  proxyDisabledDraft = configStore.settings?.proxy?.disabled === true;
 }

 async function saveVaultToggle(disabled: boolean) {
  vaultBusy = true;
  try {
   await configStore.saveVaultSettings({ disabled });
   vaultDisabledDraft = disabled;
  } catch {
   vaultDisabledDraft = configStore.settings?.vault?.disabled === true;
  } finally {
   vaultBusy = false;
  }
 }

 async function saveProxyToggle(disabled: boolean) {
  proxyBusy = true;
  try {
   await configStore.saveProxySettings({ disabled });
   proxyDisabledDraft = disabled;
  } catch {
   proxyDisabledDraft = configStore.settings?.proxy?.disabled === true;
  } finally {
   proxyBusy = false;
  }
 }

 const liveVaultEnabled = $derived(appStore.info?.vault_enabled ?? true);
 const liveProxyEnabled = $derived(appStore.info?.proxy_enabled ?? true);

 onMount(() => { loadToggles(); });
</script>

<div class="space-y-6">
 <!-- Personal vault feature flag -->
 <section>
  <div class="mb-3">
   <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">Personal vault</h2>
   <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
    Control whether users on this deployment can use the personal vault feature.
   </p>
  </div>

  <div class="p-5 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm">
   <div class="mb-4 flex items-center gap-2 p-3 rounded
    {liveVaultEnabled
     ? 'bg-emerald-50 dark:bg-emerald-950/30 border border-emerald-300 dark:border-emerald-700'
     : 'bg-amber-50 dark:bg-amber-950/40 border border-amber-300 dark:border-amber-700'}">
    <KeyRound size={14} class={liveVaultEnabled
     ? 'text-emerald-700 dark:text-emerald-300 shrink-0'
     : 'text-amber-700 dark:text-amber-300 shrink-0'} />
    <p class={liveVaultEnabled
     ? 'text-xs text-emerald-900 dark:text-emerald-200 m-0'
     : 'text-xs text-amber-900 dark:text-amber-200 m-0'}>
     {liveVaultEnabled
      ? 'Personal vault is enabled. Users see the Vault link in the navigation.'
      : 'Personal vault is disabled. The Vault link is hidden and new vault operations return 404.'}
    </p>
   </div>

   <label class="flex items-start gap-3 cursor-pointer">
    <input
     type="checkbox"
     class="mt-0.5 h-4 w-4 rounded border-slate-300 dark:border-warm-600 text-accent-600 focus:ring-accent-500 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
     checked={!vaultDisabledDraft}
     disabled={vaultBusy}
     onchange={(e) => saveVaultToggle(!(e.currentTarget as HTMLInputElement).checked)}
    />
    <span class="flex-1">
     <span class="block text-sm font-medium text-slate-800 dark:text-slate-100">
      Enable personal vault for users
     </span>
     <span class="block text-xs text-slate-500 dark:text-slate-400 mt-0.5">
      Each authenticated user can set up a private, end-to-end encrypted vault.
      Disabling preserves existing data and only hides the feature — re-enable any time to restore access.
     </span>
    </span>
   </label>

   {#if vaultDisabledDraft}
    <div class="mt-3 p-3 rounded border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-950/40 text-xs text-amber-900 dark:text-amber-200 flex items-start gap-2">
     <AlertTriangle size={13} class="shrink-0 mt-0.5" />
     <span>
      Existing vault data is not deleted. Users currently on the /vault page will be redirected after their next request.
     </span>
    </div>
   {/if}
  </div>
 </section>

 <!-- Proxy feature flag -->
 <section>
  <div class="mb-3">
   <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">Proxy servers</h2>
   <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
    Control whether operators on this deployment can build custom HTTP listeners (pipelines of middlewares and handlers).
   </p>
  </div>

  <div class="p-5 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm">
   <div class="mb-4 flex items-center gap-2 p-3 rounded
    {liveProxyEnabled
     ? 'bg-emerald-50 dark:bg-emerald-950/30 border border-emerald-300 dark:border-emerald-700'
     : 'bg-amber-50 dark:bg-amber-950/40 border border-amber-300 dark:border-amber-700'}">
    <Network size={14} class={liveProxyEnabled
     ? 'text-emerald-700 dark:text-emerald-300 shrink-0'
     : 'text-amber-700 dark:text-amber-300 shrink-0'} />
    <p class={liveProxyEnabled
     ? 'text-xs text-emerald-900 dark:text-emerald-200 m-0'
     : 'text-xs text-amber-900 dark:text-amber-200 m-0'}>
     {liveProxyEnabled
      ? 'Proxy feature is enabled. Operators with the proxy.* capability see the Proxy link in the navigation.'
      : 'Proxy feature is disabled. The Proxy link is hidden, every configured listener is stopped and the API returns 404.'}
    </p>
   </div>

   <label class="flex items-start gap-3 cursor-pointer">
    <input
     type="checkbox"
     class="mt-0.5 h-4 w-4 rounded border-slate-300 dark:border-warm-600 text-accent-600 focus:ring-accent-500 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
     checked={!proxyDisabledDraft}
     disabled={proxyBusy}
     onchange={(e) => saveProxyToggle(!(e.currentTarget as HTMLInputElement).checked)}
    />
    <span class="flex-1">
     <span class="block text-sm font-medium text-slate-800 dark:text-slate-100">
      Enable proxy servers for this deployment
     </span>
     <span class="block text-xs text-slate-500 dark:text-slate-400 mt-0.5">
      Operators with the <code class="font-mono text-[11px]">proxy.read</code> capability can view proxy servers and run test requests;
      <code class="font-mono text-[11px]">proxy.manage</code> additionally allows creating, editing and deleting graphs.
      Disabling stops every running listener — saved graphs are kept and restored when you re-enable.
     </span>
    </span>
   </label>

   {#if proxyDisabledDraft}
    <div class="mt-3 p-3 rounded border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-950/40 text-xs text-amber-900 dark:text-amber-200 flex items-start gap-2">
     <AlertTriangle size={13} class="shrink-0 mt-0.5" />
     <span>
      Every running proxy listener will be torn down on save. Saved graphs are preserved and will be re-started the next time you enable the feature.
     </span>
    </div>
   {/if}
  </div>
 </section>
</div>
