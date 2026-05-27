<script lang="ts">
  import { onMount } from "svelte";
  import {
    Lock,
    RotateCw,
    ShieldOff,
    ShieldCheck,
    KeyRound,
    Loader2,
    AlertTriangle,
  } from "lucide-svelte";
  import { vaultStore } from "@/lib/vault/store.svelte";
  import { appStore } from "@/lib/store/store.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import * as api from "@/lib/vault/api";
  import { parseSecretKey, type KDFPreset } from "@/lib/vault/crypto";
  import EmergencyKit from "@/lib/components/vault/EmergencyKit.svelte";

  // Per-user settings panel for the personal vault. Sits inside the
  // existing /settings sectional layout. Unlike the /vault route, this
  // panel never asks for the master password just to render — it only
  // requires it for the destructive / cryptographic operations (rotate,
  // regenerate kit). Read-only knobs (auto-lock TTL) are exposed
  // immediately.

  let status = $state<api.VaultStatus | null>(null);
  let account = $state<api.VaultAccountView | null>(null);
  let loading = $state(true);
  let busy = $state(false);

  // Auto-lock setting (in minutes for the UI; persisted as seconds).
  let lockMinutes = $state(15);

  // Trusted-device status. The store reads from localStorage which
  // isn't a reactive source, so we cache the lookup result here and
  // refresh it after every mutation (revoke / rotate / reset).
  let trustInfo = $state<{ created_at: string } | null>(null);

  function refreshTrustInfo() {
    // Wait until the store has the account view so user_id is known.
    trustInfo = vaultStore.account ? vaultStore.trustedDeviceInfo() : null;
  }

  onMount(async () => {
    try {
      status = await api.getStatus();
      if (status.initialized) {
        account = await api.getAccount();
        lockMinutes = Math.max(
          1,
          Math.round((account.session_lock_seconds ?? 900) / 60),
        );
        // Mirror the freshly-fetched account into the store so
        // trustedDeviceInfo() can resolve its user_id even when the
        // user lands directly on /settings without first visiting
        // /vault.
        if (!vaultStore.account) {
          await vaultStore.refreshAccount();
        }
        refreshTrustInfo();
      }
    } catch (e: any) {
      addToast(
        e?.response?.data?.message ?? "Failed to load vault status",
        "alert",
      );
    } finally {
      loading = false;
    }
  });

  function revokeTrust() {
    vaultStore.untrustDevice();
    refreshTrustInfo();
    addToast("Device trust revoked", "success", 2000);
  }

  // ── Rotate master password ──────────────────────────────────
  let showRotate = $state(false);
  let rotateOldPw = $state("");
  let rotateSecretKey = $state("");
  let rotateNewPw = $state("");
  let rotateConfirmPw = $state("");
  let rotatePreset = $state<KDFPreset>("default");

  async function doRotate() {
    if (rotateNewPw.length < 8) {
      addToast("New password must be at least 8 characters", "alert");
      return;
    }
    if (rotateNewPw !== rotateConfirmPw) {
      addToast("New passwords do not match", "alert");
      return;
    }
    let sk: Uint8Array;
    try {
      sk = parseSecretKey(rotateSecretKey);
    } catch (e: any) {
      addToast(e?.message ?? "Invalid Secret Key", "alert");
      return;
    }

    busy = true;
    try {
      // Unlock with the OLD password to obtain the current vault key
      // before re-wrapping. This is the same path /vault uses.
      const ok = await vaultStore.unlock(rotateOldPw, sk);
      if (!ok) {
        addToast("Wrong current password or Secret Key", "alert");
        return;
      }
      await vaultStore.rotateMasterPassword(rotateNewPw, rotatePreset);
      addToast("Master password updated", "success");
      showRotate = false;
      rotateOldPw = rotateSecretKey = rotateNewPw = rotateConfirmPw = "";
      account = await api.getAccount();
      // Lock again so the new password is required next time.
      vaultStore.lock();
      // The blob (if any) was re-sealed by rotateMasterPassword;
      // its created_at timestamp is fresh now.
      refreshTrustInfo();
    } catch (e: any) {
      addToast(
        e?.response?.data?.message ?? e?.message ?? "Rotate failed",
        "alert",
      );
    } finally {
      busy = false;
    }
  }

  // ── Regenerate kit ──────────────────────────────────────────
  let showKit = $state(false);
  let kitOldPw = $state("");
  let kitSecretKey = $state("");
  let regeneratedKey = $state<string | null>(null);
  let regeneratedKitId = $state<string | null>(null);

  async function doRegenerateKit() {
    let sk: Uint8Array;
    try {
      sk = parseSecretKey(kitSecretKey);
    } catch (e: any) {
      addToast(e?.message ?? "Invalid Secret Key", "alert");
      return;
    }

    busy = true;
    try {
      const ok = await vaultStore.unlock(kitOldPw, sk);
      if (!ok) {
        addToast("Wrong password or Secret Key", "alert");
        return;
      }
      const newKitID = await api.regenerateKit();
      // We re-render the Emergency Kit display using the existing
      // Secret Key (unchanged) and the new kit ID. The Secret Key
      // itself isn't rotated — only the kit id is — so we display
      // the same formatted form the user typed in.
      regeneratedKey = kitSecretKey;
      regeneratedKitId = newKitID;
      account = await api.getAccount();
      addToast("Emergency Kit regenerated", "success");
    } catch (e: any) {
      addToast(e?.response?.data?.message ?? "Regenerate failed", "alert");
    } finally {
      busy = false;
    }
  }

  // ── Auto-lock TTL ───────────────────────────────────────────
  async function saveLockTTL() {
    busy = true;
    try {
      await api.setSessionLock(lockMinutes * 60);
      addToast("Auto-lock updated", "success", 2000);
      account = await api.getAccount();
    } catch (e: any) {
      addToast(e?.response?.data?.message ?? "Save failed", "alert");
    } finally {
      busy = false;
    }
  }

  // ── Lock-on-hidden grace window ─────────────────────────────
  //
  // Per-device preference: how long to wait after the tab becomes
  // hidden before we lock the vault. The mapping mirrors the store:
  //   -1  → never lock when hidden (idle TTL still applies)
  //    0  → lock instantly (legacy behavior)
  //   >0  → grace period in seconds
  // Stored in localStorage so it persists across sessions; not
  // synced to the server because it's a device-trust setting, not
  // a vault-secrets one.
  let hiddenGraceChoice = $state<number>(vaultStore.hiddenGraceSeconds);

  function saveHiddenGrace(value: number) {
    hiddenGraceChoice = value;
    vaultStore.setHiddenGraceSeconds(value);
    addToast("Tab-hide behavior updated", "success", 2000);
  }

  // ── Reset vault ─────────────────────────────────────────────
  let showReset = $state(false);
  let resetConfirm = $state("");

  async function doReset() {
    if (resetConfirm !== "delete my vault") {
      addToast("Type the confirmation phrase exactly", "alert");
      return;
    }
    busy = true;
    try {
      const n = await vaultStore.reset();
      addToast(`Vault destroyed (${n} items)`, "success");
      showReset = false;
      resetConfirm = "";
      status = await api.getStatus();
      account = null;
      // Reset clears the trust blob; reflect that immediately.
      refreshTrustInfo();
    } catch (e: any) {
      addToast(e?.response?.data?.message ?? "Reset failed", "alert");
    } finally {
      busy = false;
    }
  }

  const vaultEnabled = $derived(appStore.info?.vault_enabled ?? false);
</script>

<div>
  <h2 class="text-base font-semibold mb-1 flex items-center gap-2">
    <Lock size={16} class="text-accent-600 dark:text-accent-400" /> Personal Vault
  </h2>
  <p class="text-xs text-slate-500 dark:text-slate-400 mb-4">
    Manage your end-to-end encrypted personal vault. The server cannot read
    these settings — every cryptographic operation runs in your browser.
  </p>

  {#if !vaultEnabled}
    <div
      class="bg-slate-50 dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded p-3 text-sm text-slate-600 dark:text-slate-300"
    >
      The vault feature is disabled on this server.
    </div>
  {:else if loading}
    <div class="text-sm text-slate-400">
      <Loader2 size={14} class="inline animate-spin" /> Loading...
    </div>
  {:else if !status?.initialized}
    <div
      class="bg-blue-50 dark:bg-blue-950/30 border border-blue-300 dark:border-blue-700 rounded p-3 text-sm"
    >
      You haven't set up a vault yet. <a
        href="#/vault"
        class="underline text-accent-700 dark:text-accent-400"
        >Set up the vault</a
      > to start storing secrets.
    </div>
  {:else}
    <div class="space-y-6">
      <!-- Auto-lock -->
      <div
        class="bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded p-4"
      >
        <h3 class="text-sm font-semibold mb-1">Auto-lock</h3>
        <p class="text-xs text-slate-500 dark:text-slate-400 mb-3">
          The vault locks automatically after this many minutes of inactivity.
          Locking zeroizes the in-memory key; you'll need to re-enter your
          master password to read items again.
        </p>
        <div class="flex items-center gap-2">
          <input
            type="number"
            min="1"
            max="1440"
            bind:value={lockMinutes}
            class="w-28 px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500"
          />
          <span class="text-sm text-slate-500 dark:text-slate-400">minutes</span
          >
          <button
            onclick={saveLockTTL}
            disabled={busy}
            class="ml-auto px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
            >Save</button
          >
        </div>
      </div>

      <!-- Lock when tab is hidden -->
      <!-- This setting controls what happens when you switch away
           from this browser tab. The default (60 s grace) was a
           response to user feedback: instant-lock made the vault
           unusable when copying TOTP codes into other apps or
           briefly checking another tab. -->
      <div
        class="bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded p-4"
      >
        <h3 class="text-sm font-semibold mb-1">Lock when tab is hidden</h3>
        <p class="text-xs text-slate-500 dark:text-slate-400 mb-3">
          Wait this long after switching tabs or minimizing the window before
          locking the vault. Coming back inside the grace window cancels the
          pending lock. The auto-lock above still applies after total
          inactivity, no matter what you pick here. This setting is per-device
          and isn't synced to the server.
        </p>
        <div class="flex flex-wrap items-center gap-1.5">
          {#each [{ value: 0, label: "Instant" }, { value: 30, label: "30 seconds" }, { value: 60, label: "1 minute" }, { value: 300, label: "5 minutes" }, { value: 900, label: "15 minutes" }, { value: -1, label: "Never" }] as opt (opt.value)}
            <button
              type="button"
              onclick={() => saveHiddenGrace(opt.value)}
              class="px-3 py-1.5 text-xs rounded border cursor-pointer
                {hiddenGraceChoice === opt.value
                ? 'bg-accent-600 border-accent-600 text-white font-medium'
                : 'border-slate-300 dark:border-warm-600 text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-warm-700'}"
              aria-pressed={hiddenGraceChoice === opt.value}
            >
              {opt.label}
            </button>
          {/each}
        </div>
        {#if hiddenGraceChoice === -1}
          <p
            class="mt-2 text-[11px] text-amber-700 dark:text-amber-400 flex items-start gap-1.5"
          >
            <AlertTriangle size={12} class="shrink-0 mt-0.5" />
            <span
              >Tab-hide locking is disabled on this device. Anyone with access
              to this browser can read your vault while the auto-lock timer is
              still running.</span
            >
          </p>
        {/if}
      </div>

      <!-- Trusted device -->
      <div
        class="bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded p-4"
      >
        <h3 class="text-sm font-semibold mb-1 flex items-center gap-1.5">
          <ShieldCheck size={14} /> Trusted device
        </h3>
        <p class="text-xs text-slate-500 dark:text-slate-400 mb-3">
          When this browser is trusted, unlocking only requires your master
          password — the Secret Key is stored locally, encrypted with that
          password (Argon2id). Three wrong unlock attempts automatically revoke
          trust.
        </p>
        {#if trustInfo}
          <div class="flex items-center justify-between gap-3 flex-wrap">
            <div class="flex items-start gap-2 text-xs">
              <ShieldCheck
                size={14}
                class="shrink-0 mt-0.5 text-green-600 dark:text-green-500"
              />
              <div>
                <div class="text-slate-700 dark:text-slate-200 font-medium">
                  This device is trusted
                </div>
                <div class="text-slate-500 dark:text-slate-400">
                  since {new Date(trustInfo.created_at).toLocaleString()}
                </div>
              </div>
            </div>
            <button
              onclick={revokeTrust}
              class="px-3 py-1.5 text-xs rounded border border-slate-300 dark:border-warm-600 bg-slate-100 dark:bg-warm-700 hover:bg-slate-200 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-100 cursor-pointer"
            >
              Revoke trust
            </button>
          </div>
        {:else}
          <div
            class="flex items-start gap-2 text-xs text-slate-500 dark:text-slate-400"
          >
            <ShieldOff size={14} class="shrink-0 mt-0.5" />
            <span>
              This device is not trusted. To enable trust, check
              <em>Trust this device</em> on the next unlock prompt.
            </span>
          </div>
        {/if}
      </div>

      <!-- Rotate master password. Border tinted with the accent
           teal (and a thicker left-stripe) so this card and the
           Emergency Kit card below clearly stand out as
           "cryptographic operation" affordances — different intent
           from the read-only Auto-lock / Trusted-device cards
           above, and lighter weight than the red Destroy panel. -->
      <div
        class="bg-white dark:bg-warm-800 border border-accent-300 dark:border-accent-700 border-l-4 border-l-accent-500 dark:border-l-accent-400 rounded p-4"
      >
        <h3 class="text-sm font-semibold mb-1 flex items-center gap-1.5">
          <RotateCw size={14} /> Change master password
        </h3>
        <p class="text-xs text-slate-500 dark:text-slate-400 mb-3">
          Re-wraps the vault key with a new password. Items are not
          re-encrypted; this is a fast, O(1) operation regardless of vault size.
        </p>
        {#if !showRotate}
          <button
            onclick={() => (showRotate = true)}
            class="px-3 py-1.5 text-xs rounded border border-slate-300 dark:border-warm-600 bg-slate-100 dark:bg-warm-700 hover:bg-slate-200 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-100 cursor-pointer"
          >
            Change password
          </button>
        {:else}
          <form
            onsubmit={(e) => {
              e.preventDefault();
              doRotate();
            }}
            class="space-y-2"
          >
            <input
              type="password"
              bind:value={rotateOldPw}
              required
              placeholder="Current master password"
              class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500"
              autocomplete="current-password"
            />
            <textarea
              bind:value={rotateSecretKey}
              required
              rows="2"
              placeholder="Secret Key"
              spellcheck="false"
              class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500"
            ></textarea>
            <input
              type="password"
              bind:value={rotateNewPw}
              required
              minlength="8"
              placeholder="New master password (≥ 8 chars)"
              class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500"
              autocomplete="new-password"
            />
            <input
              type="password"
              bind:value={rotateConfirmPw}
              required
              placeholder="Confirm new password"
              class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500"
              autocomplete="new-password"
            />
            <select
              bind:value={rotatePreset}
              class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500"
            >
              <option value="fast">Fast preset</option>
              <option value="default">Default preset</option>
              <option value="strong">Strong preset</option>
            </select>
            <div class="flex gap-2">
              <button
                type="submit"
                disabled={busy}
                class="px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
              >
                {busy ? "Working..." : "Confirm rotate"}
              </button>
              <button
                type="button"
                onclick={() => (showRotate = false)}
                class="px-3 py-1.5 text-xs rounded hover:bg-slate-100 dark:hover:bg-warm-700 text-slate-700 dark:text-slate-200 cursor-pointer"
              >
                Cancel
              </button>
            </div>
          </form>
        {/if}
      </div>

      <!-- Regenerate kit. Same accent border treatment as the
           Rotate card so the two "do a crypto thing" cards visually
           group together. -->
      <div
        class="bg-white dark:bg-warm-800 border border-accent-300 dark:border-accent-700 border-l-4 border-l-accent-500 dark:border-l-accent-400 rounded p-4"
      >
        <h3 class="text-sm font-semibold mb-1 flex items-center gap-1.5">
          <KeyRound size={14} /> Emergency Kit
        </h3>
        <p class="text-xs text-slate-500 dark:text-slate-400 mb-3">
          Regenerate the kit ID and re-render the printout. The Secret Key
          itself is unchanged. Use this when you want to mark a previous kit as
          superseded (the old printout still works because the Secret Key isn't
          rotated; a future Secret Key rotation is planned).
        </p>
        {#if regeneratedKey && regeneratedKitId}
          <EmergencyKit
            username={appStore.identity?.subject ??
              appStore.info?.user ??
              "user"}
            secretKey={regeneratedKey}
            kitID={regeneratedKitId}
          />
          <button
            onclick={() => {
              regeneratedKey = null;
              regeneratedKitId = null;
              showKit = false;
              kitOldPw = kitSecretKey = "";
            }}
            class="mt-3 px-3 py-1.5 text-xs rounded border border-slate-300 dark:border-warm-600 bg-slate-100 dark:bg-warm-700 hover:bg-slate-200 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-100 cursor-pointer"
            >Done</button
          >
        {:else if !showKit}
          <button
            onclick={() => (showKit = true)}
            class="px-3 py-1.5 text-xs rounded border border-slate-300 dark:border-warm-600 bg-slate-100 dark:bg-warm-700 hover:bg-slate-200 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-100 cursor-pointer"
          >
            Regenerate kit
          </button>
        {:else}
          <form
            onsubmit={(e) => {
              e.preventDefault();
              doRegenerateKit();
            }}
            class="space-y-2"
          >
            <input
              type="password"
              bind:value={kitOldPw}
              required
              placeholder="Master password"
              class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500"
              autocomplete="current-password"
            />
            <textarea
              bind:value={kitSecretKey}
              required
              rows="2"
              placeholder="Secret Key"
              spellcheck="false"
              class="w-full px-3 py-2 text-sm font-mono rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500"
            ></textarea>
            <div class="flex gap-2">
              <button
                type="submit"
                disabled={busy}
                class="px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
              >
                {busy ? "Working..." : "Regenerate"}
              </button>
              <button
                type="button"
                onclick={() => (showKit = false)}
                class="px-3 py-1.5 text-xs rounded hover:bg-slate-100 dark:hover:bg-warm-700 text-slate-700 dark:text-slate-200 cursor-pointer"
              >
                Cancel
              </button>
            </div>
          </form>
        {/if}
      </div>

      <!-- Reset / destroy. The `/40` opacity on the dark surface
           keeps the red tint legible without screaming — the
           previous `/20` was so subtle that the warning panel
           blended into the page background in dark mode. -->
      <div
        class="bg-red-50 dark:bg-red-950/40 border border-red-300 dark:border-red-800 rounded p-4"
      >
        <h3
          class="text-sm font-semibold mb-1 flex items-center gap-1.5 text-red-700 dark:text-red-400"
        >
          <ShieldOff size={14} /> Destroy vault
        </h3>
        <div
          class="flex items-start gap-2 text-xs text-red-700 dark:text-red-300 mb-3"
        >
          <AlertTriangle size={14} class="shrink-0 mt-0.5" />
          <p>
            Deletes <strong>every item</strong> and the account record. Cannot be
            undone. Use this if you've lost your master password and want to start
            over.
          </p>
        </div>
        {#if !showReset}
          <button
            onclick={() => (showReset = true)}
            class="px-3 py-1.5 text-xs rounded bg-red-600 text-white font-medium hover:bg-red-700 cursor-pointer"
          >
            Destroy vault...
          </button>
        {:else}
          <form
            onsubmit={(e) => {
              e.preventDefault();
              doReset();
            }}
            class="space-y-2"
          >
            <p class="text-xs text-red-700 dark:text-red-300">
              Type <code
                class="font-mono bg-red-100 dark:bg-red-900/60 text-red-800 dark:text-red-200 px-1 rounded"
                >delete my vault</code
              > to confirm:
            </p>
            <input
              type="text"
              bind:value={resetConfirm}
              required
              class="w-full px-3 py-2 text-sm rounded border border-red-300 dark:border-red-700 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100"
            />
            <div class="flex gap-2">
              <button
                type="submit"
                disabled={busy || resetConfirm !== "delete my vault"}
                class="px-3 py-1.5 text-xs rounded bg-red-600 text-white font-medium hover:bg-red-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer"
              >
                {busy ? "Working..." : "Destroy vault permanently"}
              </button>
              <button
                type="button"
                onclick={() => {
                  showReset = false;
                  resetConfirm = "";
                }}
                class="px-3 py-1.5 text-xs rounded hover:bg-slate-100 dark:hover:bg-warm-700 text-slate-700 dark:text-slate-200 cursor-pointer"
              >
                Cancel
              </button>
            </div>
          </form>
        {/if}
      </div>

      <!-- Stats. The previous variant used `text-slate-500` for the
           dt labels with no dark: override, so the labels collapsed
           into the background in dark mode. The bg also dropped to
           50% which made the card barely distinguishable from the
           page; bumped to a solid warm-900 panel surface. -->
      <div
        class="bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded p-4"
      >
        <h3 class="text-sm font-semibold mb-3">Vault info</h3>
        <dl class="grid grid-cols-2 gap-2 text-xs">
          <dt class="text-slate-500 dark:text-slate-400">Items</dt>
          <dd class="text-slate-700 dark:text-slate-200">
            {status.item_count}
          </dd>
          <dt class="text-slate-500 dark:text-slate-400">KDF</dt>
          <dd class="text-slate-700 dark:text-slate-200">
            argon2id, {account?.kdf.memory
              ? (account.kdf.memory / 1024).toFixed(0)
              : 0} MiB, {account?.kdf.iterations ?? "?"} iters
          </dd>
          <dt class="text-slate-500 dark:text-slate-400">Wrap version</dt>
          <dd class="text-slate-700 dark:text-slate-200">
            v{account?.wrapped_vault_key_version ?? "?"}
          </dd>
          <dt class="text-slate-500 dark:text-slate-400">Created</dt>
          <dd class="text-slate-700 dark:text-slate-200">
            {account?.created_at
              ? new Date(account.created_at).toLocaleString()
              : ""}
          </dd>
          <dt class="text-slate-500 dark:text-slate-400">Last update</dt>
          <dd class="text-slate-700 dark:text-slate-200">
            {account?.updated_at
              ? new Date(account.updated_at).toLocaleString()
              : ""}
          </dd>
          <dt class="text-slate-500 dark:text-slate-400">Kit ID</dt>
          <dd class="font-mono break-all text-slate-700 dark:text-slate-200">
            {account?.recovery_kit_id ?? ""}
          </dd>
        </dl>
      </div>
    </div>
  {/if}
</div>
