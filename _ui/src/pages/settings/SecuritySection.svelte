<script lang="ts">
  import { configStore } from "@/lib/store/config.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import { onMount } from "svelte";
  import { Eye, EyeOff, Shield, Lock } from "lucide-svelte";

  // ── Admin secret state ──
  let adminSecretConfigured = $state(false);
  let currentAdminSecret = $state('');
  let newAdminSecret = $state('');
  let confirmAdminSecret = $state('');
  let isSavingAdminSecret = $state(false);
  let showCurrentAdminSecret = $state(false);
  let showNewAdminSecret = $state(false);
  let showConfirmAdminSecret = $state(false);

  async function loadAdminSecretStatus() {
    const status = await configStore.fetchAdminSecretStatus();
    adminSecretConfigured = status.configured;
  }

  async function handleSetAdminSecret() {
    if (!newAdminSecret.trim()) {
      addToast('New secret is required', 'alert');
      return;
    }
    if (newAdminSecret !== confirmAdminSecret) {
      addToast('Secrets do not match', 'alert');
      return;
    }
    if (adminSecretConfigured && !currentAdminSecret.trim()) {
      addToast('Current secret is required', 'alert');
      return;
    }

    isSavingAdminSecret = true;
    try {
      await configStore.setAdminSecret(currentAdminSecret.trim(), newAdminSecret.trim());
      addToast(adminSecretConfigured ? 'Admin secret updated' : 'Admin secret set', 'success');
      adminSecretConfigured = true;
      currentAdminSecret = '';
      newAdminSecret = '';
      confirmAdminSecret = '';
    } catch (error: any) {
      const msg = error.response?.data?.message || 'Failed to set admin secret';
      addToast(msg, 'alert');
    } finally {
      isSavingAdminSecret = false;
    }
  }

  onMount(() => {
    loadAdminSecretStatus();
  });
</script>

<div>
  <div class="mb-4">
    <h2 class="text-lg font-semibold text-slate-800">Admin Secret</h2>
    <p class="text-sm text-slate-500 mt-0.5">Set or update the admin secret used to authorize key rotation</p>
  </div>

  <div class="p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
    {#if adminSecretConfigured}
      <div class="mb-5 flex items-center gap-2 p-3 bg-green-50 border border-green-200 rounded-md">
        <Shield size={14} class="text-green-600 shrink-0" />
        <p class="text-xs text-green-800 m-0">Admin secret is configured.</p>
      </div>
    {:else}
      <div class="mb-5 flex items-center gap-2 p-3 bg-amber-50 border border-amber-200 rounded-md">
        <Shield size={14} class="text-amber-600 shrink-0" />
        <p class="text-xs text-amber-800 m-0">No admin secret configured. Set one to enable key rotation.</p>
      </div>
    {/if}

    {#if adminSecretConfigured}
      <div class="mb-4">
        <label for="current-admin-secret" class="block text-xs font-medium text-slate-500 mb-1.5">Current Secret</label>
        <div class="relative">
          <input
            id="current-admin-secret"
            type={showCurrentAdminSecret ? 'text' : 'password'}
            bind:value={currentAdminSecret}
            placeholder="Enter current admin secret"
            class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
          />
          <button
            type="button"
            class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 bg-transparent border-none cursor-pointer hover:text-slate-600 transition-colors"
            onclick={() => showCurrentAdminSecret = !showCurrentAdminSecret}
            title={showCurrentAdminSecret ? 'Hide' : 'Show'}
          >
            {#if showCurrentAdminSecret}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
          </button>
        </div>
      </div>
    {/if}

    <div class="mb-4">
      <label for="new-admin-secret" class="block text-xs font-medium text-slate-500 mb-1.5">New Secret</label>
      <div class="relative">
        <input
          id="new-admin-secret"
          type={showNewAdminSecret ? 'text' : 'password'}
          bind:value={newAdminSecret}
          placeholder="Enter new admin secret"
          class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
        />
        <button
          type="button"
          class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 bg-transparent border-none cursor-pointer hover:text-slate-600 transition-colors"
          onclick={() => showNewAdminSecret = !showNewAdminSecret}
          title={showNewAdminSecret ? 'Hide' : 'Show'}
        >
          {#if showNewAdminSecret}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
        </button>
      </div>
    </div>

    <div class="mb-4">
      <label for="confirm-admin-secret" class="block text-xs font-medium text-slate-500 mb-1.5">Confirm New Secret</label>
      <div class="relative">
        <input
          id="confirm-admin-secret"
          type={showConfirmAdminSecret ? 'text' : 'password'}
          bind:value={confirmAdminSecret}
          placeholder="Confirm new admin secret"
          class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
        />
        <button
          type="button"
          class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 bg-transparent border-none cursor-pointer hover:text-slate-600 transition-colors"
          onclick={() => showConfirmAdminSecret = !showConfirmAdminSecret}
          title={showConfirmAdminSecret ? 'Hide' : 'Show'}
        >
          {#if showConfirmAdminSecret}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
        </button>
      </div>
    </div>

    <button
      class="flex items-center justify-center gap-2 w-full px-4 py-2.5 text-sm font-medium text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
      onclick={handleSetAdminSecret}
      disabled={isSavingAdminSecret}
    >
      <Lock size={14} />
      {isSavingAdminSecret ? 'Saving...' : adminSecretConfigured ? 'Update Admin Secret' : 'Set Admin Secret'}
    </button>
  </div>
</div>
