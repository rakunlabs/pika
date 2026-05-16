<script lang="ts">
 import { configStore } from "@/lib/store/config.svelte";
 import { appStore } from "@/lib/store/store.svelte";
 import { onMount } from "svelte";
 import { KeyRound, AlertTriangle } from "lucide-svelte";

 // Security section.
 //
 // Historically this panel hosted the "Admin Secret" feature — a
 // second password layered on top of CapSettingsManage for the
 // backup / key-rotation endpoints. That was redundant: anyone
 // holding CapSettingsManage can already export the full DB, so a
 // separate secret on the same surface bought no real protection
 // and was a UX hazard (operators forgot they'd set it). The
 // Admin Secret has been retired; this panel now only carries the
 // deployment-wide personal-vault toggle.

 // ── Personal vault feature toggle ──
 //
 // Deployment-wide gate that hides /vault from the SPA navigation
 // and turns every /api/v1/me/vault/* endpoint into a 404. Existing
 // vault data is preserved on the server — this is a feature flag,
 // not a destructive action. Re-enabling later restores access.
 //
 // We mirror appStore.info.vault_enabled as the read source so the
 // toggle reflects whatever state the API currently reports. After
 // the save we re-fetch /api/v1/info from configStore.saveVaultSettings
 // so the navbar updates without a page reload.
 let vaultDisabledDraft = $state(false);
 let vaultBusy = $state(false);

 async function loadVaultToggle() {
  // configStore.settings has the raw stored value (including the
  // disabled flag). We rely on that rather than appStore.info
  // because info combines this admin toggle with the boot-time
  // gate, and the toggle should reflect the stored value verbatim.
  await configStore.loadSettings();
  const stored = configStore.settings?.vault?.disabled === true;
  vaultDisabledDraft = stored;
 }

 async function saveVaultToggle(disabled: boolean) {
  vaultBusy = true;
  try {
   await configStore.saveVaultSettings({ disabled });
   vaultDisabledDraft = disabled;
  } catch {
   // Roll back the UI toggle to whatever the server actually has.
   vaultDisabledDraft = configStore.settings?.vault?.disabled === true;
  } finally {
   vaultBusy = false;
  }
 }

 // Sync the toggle when the user navigates here after switching the
 // value elsewhere (e.g. by hitting the API directly).
 const liveVaultEnabled = $derived(appStore.info?.vault_enabled ?? true);

 onMount(() => {
  loadVaultToggle();
 });
</script>

<div>
 <!-- Personal vault feature flag — deployment-level toggle.
      Lives in the Security panel because the decision to expose
      the personal-vault feature is a security/policy choice (every
      user becomes able to store sensitive credentials in pika),
      not a per-user preference. -->
 <div class="mb-4">
  <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">Personal vault</h2>
  <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
   Control whether users on this deployment can use the personal vault feature.
  </p>
 </div>

 <div class="p-5 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm">
  <div class="mb-4 flex items-center gap-2 p-3 rounded-md
   {liveVaultEnabled
    ? 'bg-green-50 dark:bg-green-950/40 border border-green-200 dark:border-green-800'
    : 'bg-amber-50 dark:bg-amber-950/40 border border-amber-200 dark:border-amber-800'}">
   {#if liveVaultEnabled}
    <KeyRound size={14} class="text-green-600 dark:text-green-400 shrink-0" />
    <p class="text-xs text-green-800 dark:text-green-200 m-0">
     Personal vault is enabled. Users see the Vault link in the navigation.
    </p>
   {:else}
    <KeyRound size={14} class="text-amber-600 dark:text-amber-400 shrink-0" />
    <p class="text-xs text-amber-800 dark:text-amber-200 m-0">
     Personal vault is disabled. The Vault link is hidden and new vault operations return 404.
    </p>
   {/if}
  </div>

  <!-- Inline toggle row. Clicking the switch fires saveVaultToggle
       immediately; we don't gate on a separate "Save" button
       because there's exactly one knob here and a two-step flow
       would feel pointless. -->
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
   <div class="mt-3 p-3 rounded border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-950/40 text-xs text-amber-800 dark:text-amber-200 flex items-start gap-2">
    <AlertTriangle size={13} class="shrink-0 mt-0.5" />
    <span>
     Existing vault data is not deleted. Users currently on the /vault page will be redirected after their next request.
    </span>
   </div>
  {/if}
 </div>
</div>
