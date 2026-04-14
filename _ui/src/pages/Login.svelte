<script lang="ts">
  import { appStore } from '@/lib/store/store.svelte';
  import { Blocks, LogIn, ExternalLink } from 'lucide-svelte';

  let username = $state('');
  let password = $state('');
  let error = $state('');
  let loading = $state(false);

  const ssoUrl = $derived(
    appStore.info?.forward_auth_enabled && appStore.info?.forward_auth_redirect_url
      ? appStore.info.forward_auth_redirect_url
      : null
  );

  async function handleSubmit(e: Event) {
    e.preventDefault();
    error = '';
    loading = true;

    try {
      await appStore.login(username, password);
    } catch (err: any) {
      error = err?.response?.data?.message || 'Login failed';
    } finally {
      loading = false;
    }
  }
</script>

<div class="flex flex-col items-center justify-start h-full w-full bg-slate-100 pt-8">
  <div class="w-full max-w-sm">
    <div class="bg-white rounded-lg shadow-lg border border-slate-200 p-8">
      <!-- Logo -->
      <div class="flex items-center justify-center gap-2 mb-6">
        <Blocks size={24} color="#EF233C" />
        <div class="flex flex-col leading-none">
          <span class="text-xl font-bold tracking-wide text-slate-800">pika</span>
          {#if appStore.info?.version}
            <span class="text-[10px] font-mono text-slate-400">{appStore.info.version}</span>
          {/if}
        </div>
      </div>
      {#if error}
        <div class="mb-4 p-3 bg-red-50 border border-red-200 rounded-md text-sm text-red-700">
          {error}
        </div>
      {/if}

      <form onsubmit={handleSubmit} class="space-y-4">
        <div>
          <label for="username" class="block text-xs font-medium text-slate-600 mb-1">Username</label>
          <input
            id="username"
            type="text"
            bind:value={username}
            required
            autocomplete="username"
            class="w-full px-3 py-2 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            placeholder="Enter username"
          />
        </div>

        <div>
          <label for="password" class="block text-xs font-medium text-slate-600 mb-1">Password</label>
          <input
            id="password"
            type="password"
            bind:value={password}
            required
            autocomplete="current-password"
            class="w-full px-3 py-2 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            placeholder="Enter password"
          />
        </div>

        <button
          type="submit"
          disabled={loading || !username || !password}
          class="w-full flex items-center justify-center gap-2 px-4 py-2 bg-blue-500 text-white text-sm font-medium rounded-md hover:bg-blue-600 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
        >
          <LogIn size={14} />
          {loading ? 'Signing in...' : 'Sign in'}
        </button>
      </form>

      {#if ssoUrl}
        <div class="flex items-center gap-3 my-5">
          <div class="flex-1 h-px bg-slate-200"></div>
          <span class="text-xs text-slate-400">or</span>
          <div class="flex-1 h-px bg-slate-200"></div>
        </div>

        <a
          href={ssoUrl}
          class="w-full flex items-center justify-center gap-2 px-4 py-2 bg-slate-100 text-slate-700 text-sm font-medium rounded-md border border-slate-300 hover:bg-slate-200 transition-colors"
        >
          <ExternalLink size={14} />
          Login with SSO
        </a>
      {/if}
    </div>
  </div>
</div>
