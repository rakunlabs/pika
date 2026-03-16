<script lang="ts">
  import { appStore } from '@/lib/store/store.svelte';
  import { Blocks, UserPlus } from 'lucide-svelte';

  let username = $state('');
  let password = $state('');
  let confirmPassword = $state('');
  let error = $state('');
  let loading = $state(false);

  const passwordMismatch = $derived(confirmPassword !== '' && password !== confirmPassword);
  const canSubmit = $derived(
    username.trim() !== '' &&
    password !== '' &&
    confirmPassword !== '' &&
    !passwordMismatch &&
    !loading
  );

  async function handleSubmit(e: Event) {
    e.preventDefault();
    error = '';

    if (password !== confirmPassword) {
      error = 'Passwords do not match';
      return;
    }

    loading = true;

    try {
      await appStore.setup(username.trim(), password);
    } catch (err: any) {
      error = err?.response?.data?.message || 'Setup failed';
    } finally {
      loading = false;
    }
  }
</script>

<div class="flex flex-col items-center justify-start h-full w-full bg-slate-100 pt-8">
  <div class="w-full max-w-sm">
    <div class="bg-white rounded-lg shadow-lg border border-slate-200 p-8">
      <!-- Logo -->
      <div class="flex items-center justify-center gap-2 mb-2">
        <Blocks size={24} color="#EF233C" />
        <div class="flex flex-col leading-none">
          <span class="text-xl font-bold tracking-wide text-slate-800">pika</span>
        </div>
      </div>

      <div class="text-center mb-6">
        <h2 class="text-sm font-semibold text-slate-700 mb-1">Create Admin Account</h2>
        <p class="text-xs text-slate-400">Set up your first account to get started</p>
      </div>

      {#if error}
        <div class="mb-4 p-3 bg-red-50 border border-red-200 rounded-md text-sm text-red-700">
          {error}
        </div>
      {/if}

      <form onsubmit={handleSubmit} class="space-y-4">
        <div>
          <label for="setup-username" class="block text-xs font-medium text-slate-600 mb-1">Username</label>
          <input
            id="setup-username"
            type="text"
            bind:value={username}
            required
            autocomplete="username"
            class="w-full px-3 py-2 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            placeholder="Choose a username"
          />
        </div>

        <div>
          <label for="setup-password" class="block text-xs font-medium text-slate-600 mb-1">Password</label>
          <input
            id="setup-password"
            type="password"
            bind:value={password}
            required
            autocomplete="new-password"
            class="w-full px-3 py-2 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            placeholder="Choose a password"
          />
        </div>

        <div>
          <label for="setup-confirm" class="block text-xs font-medium text-slate-600 mb-1">Confirm Password</label>
          <input
            id="setup-confirm"
            type="password"
            bind:value={confirmPassword}
            required
            autocomplete="new-password"
            class="w-full px-3 py-2 border rounded-md text-sm focus:outline-none focus:ring-2 focus:border-transparent
              {passwordMismatch ? 'border-red-300 focus:ring-red-500' : 'border-slate-300 focus:ring-blue-500'}"
            placeholder="Confirm your password"
          />
          {#if passwordMismatch}
            <p class="mt-1 text-xs text-red-500">Passwords do not match</p>
          {/if}
        </div>

        <button
          type="submit"
          disabled={!canSubmit}
          class="w-full flex items-center justify-center gap-2 px-4 py-2 bg-blue-500 text-white text-sm font-medium rounded-md hover:bg-blue-600 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
        >
          <UserPlus size={14} />
          {loading ? 'Creating account...' : 'Create Account'}
        </button>
      </form>
    </div>
  </div>
</div>
