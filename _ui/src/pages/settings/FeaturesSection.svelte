<script lang="ts">
  import { configStore } from "@/lib/store/config.svelte";
  import { appStore } from "@/lib/store/store.svelte";
  import { keymgrStore } from "@/lib/store/keymgr.svelte";
  import { confirmDialog } from "@/lib/store/confirm.svelte";
  import { onMount } from "svelte";
  import { KeyRound, AlertTriangle } from "lucide-svelte";

  // Features section — deployment-wide feature flags.
  //
  // After the raw-mount/registry/proxy/serve extraction, the only
  // remaining toggle is the personal vault. Other deployment-wide
  // flags were extracted alongside their feature.

  // ── Personal vault feature toggle ──
  let vaultDisabledDraft = $state(false);
  let userEncryptionDraft = $state(true);
  let vaultBusy = $state(false);

  async function loadToggles() {
    await configStore.loadSettings();
    vaultDisabledDraft = configStore.settings?.vault?.disabled === true;
    userEncryptionDraft = configStore.settings?.vault?.key_mode !== "server";
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

  async function saveUserEncryption(enabled: boolean) {
    const ok = await confirmDialog({
      title: enabled
        ? "Require a master password for vaults?"
        : "Turn off per-user vault encryption?",
      message: enabled
        ? "Each user will choose a master password and receive a Secret Key the next time they open their vault. After that, admins can no longer read vault contents."
        : "Vault keys will be protected by the server encryption key instead of each user's master password. Users open their vault directly, and anyone with the server key can read vault contents. Existing vaults switch over the next time their owner unlocks them.",
      confirmLabel: enabled ? "Require master password" : "Use server encryption",
      danger: !enabled,
    });
    if (!ok) {
      userEncryptionDraft = configStore.settings?.vault?.key_mode !== "server";
      return;
    }
    vaultBusy = true;
    try {
      await configStore.saveVaultSettings(
        { key_mode: enabled ? "user" : "server" },
        enabled
          ? "Vaults now require a master password."
          : "Vaults now use the server encryption key.",
      );
      userEncryptionDraft = enabled;
    } catch {
      userEncryptionDraft = configStore.settings?.vault?.key_mode !== "server";
    } finally {
      vaultBusy = false;
    }
  }

  const liveVaultEnabled = $derived(appStore.info?.vault_enabled ?? true);
  // Server-managed keys need the server encryption key to be set up
  // and unlocked; without it the switch would leave vaults unopenable.
  const serverKeyReady = $derived(
    keymgrStore.status?.initialized === true && keymgrStore.status?.unlocked === true,
  );

  onMount(() => {
    loadToggles();
    keymgrStore.refreshStatus();
  });
</script>

<div class="space-y-6">
  <!-- Personal vault feature flag -->
  <section>
    <div class="mb-3">
      <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">
        Personal vault
      </h2>
      <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
        Control whether users on this deployment can use the personal vault
        feature.
      </p>
    </div>

    <div
      class="p-5 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm"
    >
      <div
        class="mb-4 flex items-center gap-2 p-3 rounded
    {liveVaultEnabled
          ? 'bg-emerald-50 dark:bg-emerald-950/30 border border-emerald-300 dark:border-emerald-700'
          : 'bg-amber-50 dark:bg-amber-950/40 border border-amber-300 dark:border-amber-700'}"
      >
        <KeyRound
          size={14}
          class={liveVaultEnabled
            ? "text-emerald-700 dark:text-emerald-300 shrink-0"
            : "text-amber-700 dark:text-amber-300 shrink-0"}
        />
        <p
          class={liveVaultEnabled
            ? "text-xs text-emerald-900 dark:text-emerald-200 m-0"
            : "text-xs text-amber-900 dark:text-amber-200 m-0"}
        >
          {liveVaultEnabled
            ? "Personal vault is enabled. Users see the Vault link in the navigation."
            : "Personal vault is disabled. The Vault link is hidden and new vault operations return 404."}
        </p>
      </div>

      <label class="flex items-start gap-3 cursor-pointer">
        <input
          type="checkbox"
          class="mt-0.5 h-4 w-4 rounded border-slate-300 dark:border-warm-600 text-accent-600 focus:ring-accent-500 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed dark:text-accent-400"
          checked={!vaultDisabledDraft}
          disabled={vaultBusy}
          onchange={(e) =>
            saveVaultToggle(!(e.currentTarget as HTMLInputElement).checked)}
        />
        <span class="flex-1">
          <span
            class="block text-sm font-medium text-slate-800 dark:text-slate-100"
          >
            Enable personal vault for users
          </span>
          <span class="block text-xs text-slate-500 dark:text-slate-400 mt-0.5">
            Each authenticated user can set up a private, encrypted vault.
            Disabling preserves existing data and only hides the feature —
            re-enable any time to restore access.
          </span>
        </span>
      </label>

      {#if vaultDisabledDraft}
        <div
          class="mt-3 p-3 rounded border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-950/40 text-xs text-amber-900 dark:text-amber-200 flex items-start gap-2"
        >
          <AlertTriangle size={13} class="shrink-0 mt-0.5" />
          <span>
            Existing vault data is not deleted. Users currently on the /vault
            page will be redirected after their next request.
          </span>
        </div>
      {/if}

      <div class="mt-5 pt-4 border-t border-slate-200 dark:border-warm-700">
        <label class="flex items-start gap-3 cursor-pointer">
          <input
            type="checkbox"
            class="mt-0.5 h-4 w-4 rounded border-slate-300 dark:border-warm-600 text-accent-600 focus:ring-accent-500 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed dark:text-accent-400"
            checked={userEncryptionDraft}
            disabled={vaultBusy || (userEncryptionDraft && !serverKeyReady)}
            onchange={(e) =>
              saveUserEncryption((e.currentTarget as HTMLInputElement).checked)}
          />
          <span class="flex-1">
            <span
              class="block text-sm font-medium text-slate-800 dark:text-slate-100"
            >
              Require a master password for each vault
            </span>
            <span class="block text-xs text-slate-500 dark:text-slate-400 mt-0.5">
              On: every user protects their vault with their own master
              password and Secret Key (end-to-end, admins can't read it). Off:
              vault keys are sealed with the server encryption key and users
              open their vault directly.
            </span>
          </span>
        </label>

        {#if userEncryptionDraft && !serverKeyReady}
          <p class="mt-2 ml-7 text-xs text-slate-500 dark:text-slate-400">
            To turn this off, first enable and unlock the server encryption key
            in Settings → Server encryption key.
          </p>
        {/if}

        {#if !userEncryptionDraft}
          <div
            class="mt-3 p-3 rounded border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-950/40 text-xs text-amber-900 dark:text-amber-200 flex items-start gap-2"
          >
            <AlertTriangle size={13} class="shrink-0 mt-0.5" />
            <span>
              Vault contents can be read by anyone who has the server
              encryption key. Vaults that still use a master password switch
              over the next time their owner unlocks them.
            </span>
          </div>
        {/if}
      </div>
    </div>
  </section>
</div>
