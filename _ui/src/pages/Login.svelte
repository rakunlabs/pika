<script lang="ts">
  import { appStore } from '@/lib/store/store.svelte';
  import { Blocks, LogIn, UserPlus, ExternalLink } from 'lucide-svelte';
  import { onMount } from 'svelte';
  import type { LoginStrategy } from '@/lib/store/store.svelte';

  let loading = $state(false);
  let infoLoading = $state(true);
  let infoError = $state('');
  let error = $state('');

  // signup_first: show register form first when the server requests it
  let showRegister = $state(false);

  // Track form field values keyed by field name
  let loginFields = $state<Record<string, string>>({});
  let registerFields = $state<Record<string, string>>({});

  const loginInfo = $derived(appStore.loginInfo);

  // The first password strategy (if any).
  // Defensive: when /login/info isn't reachable (e.g. the SPA fallback
  // returned index.html as a 200, or a misconfigured backend returned
  // an object without `strategies`), `loginInfo.strategies` is
  // undefined. Calling `.find` on undefined throws synchronously inside
  // the $derived, which freezes the whole reactive graph and leaves the
  // app stuck on the App.svelte loading state. Treat any non-array as
  // an empty list.
  const strategies = $derived(
    Array.isArray(loginInfo?.strategies) ? loginInfo!.strategies : []
  );

  const passwordStrategy = $derived(
    strategies.find((s) => s.kind === 'password') ?? null
  );

  // All oauth2 strategies
  const oauthStrategies = $derived(
    strategies.filter((s) => s.kind === 'oauth2')
  );

  // Signup is only exposed in the UI during initial bootstrap (no users
  // exist yet). Once the first admin is created, the server flips
  // signup_first to false and the signup affordances disappear — further
  // users are added by an admin from inside the app. The backend also
  // refuses self-registration past bootstrap via LocalRegistrar, so this
  // UI gate is purely about affordance.
  const hasRegister = $derived(
    !!passwordStrategy?.register && !!loginInfo?.signup_first
  );

  onMount(async () => {
    infoLoading = true;
    infoError = '';
    try {
      await appStore.loadLoginInfo();
      if (loginInfo?.signup_first && hasRegister) {
        showRegister = true;
      }
    } catch (err: any) {
      infoError = err?.response?.data?.message || 'Failed to load login configuration';
    } finally {
      infoLoading = false;
    }
  });

  function getFieldValue(fields: Record<string, string>, name: string): string {
    return fields[name] ?? '';
  }

  function setFieldValue(fields: Record<string, string>, name: string, value: string): Record<string, string> {
    return { ...fields, [name]: value };
  }

  async function handleLogin(strategy: LoginStrategy) {
    error = '';
    loading = true;
    try {
      const body: Record<string, string> = {};
      for (const field of strategy.fields ?? []) {
        body[field.name] = loginFields[field.name] ?? '';
      }
      await appStore.loginWith(strategy.url, body);
      // Deliberately no location change here: App.svelte watches
      // appStore.authenticated and swaps this Login component for the
      // router once it flips true. The router reads location.hash, which
      // still contains whatever deep-link the user was trying to reach
      // (e.g. #/settings). Forcing location.href = '/' would wipe that
      // hash and dump the user at the root.
    } catch (err: any) {
      error = err?.response?.data?.message || 'Login failed';
    } finally {
      loading = false;
    }
  }

  async function handleRegister(strategy: LoginStrategy) {
    if (!strategy.register) return;
    error = '';

    // Client-side validation: catch obvious problems before the network
    // round-trip. The backend also validates — this is just for responsive
    // feedback. Keep in sync with the checks in ada's readRegister.
    const fields = strategy.register.fields ?? [];
    const declared = new Set(fields.map((f) => f.name));

    // Required fields must be non-empty.
    for (const field of fields) {
      if (field.required && !(registerFields[field.name] ?? '').trim()) {
        error = `${field.label || field.name} is required`;
        return;
      }
    }

    // Password confirmation must match the password field. We match by
    // convention on the "password_confirm" name used by ada's default form.
    if (declared.has('password_confirm')) {
      const pw = registerFields['password'] ?? '';
      const confirm = registerFields['password_confirm'] ?? '';
      if (pw !== confirm) {
        error = 'Passwords do not match';
        return;
      }
    }

    loading = true;
    try {
      const body: Record<string, string> = {};
      for (const field of fields) {
        body[field.name] = registerFields[field.name] ?? '';
      }
      await appStore.registerWith(strategy.register.url, body);
      if (!appStore.authenticated) {
        // No auto-login: the server created the account but did not
        // issue a session. Flip back to the login form so the user can
        // sign in with the credentials they just set.
        showRegister = false;
        error = '';
        registerFields = {};
      }
      // On auto-login success: App.svelte's reactive gate swaps us out
      // for the router, which reads location.hash — so any deep-link the
      // user was trying to reach before signup is preserved.
    } catch (err: any) {
      const code = err?.response?.data?.error;
      const msg = err?.response?.data?.message;
      if (code === 'password_mismatch') {
        error = 'Passwords do not match';
      } else if (code === 'user_exists') {
        // Bootstrap-only signup: the backend's LocalRegistrar rejects
        // further registrations with 409 once the first user exists. If we
        // see that, another tab or admin likely completed bootstrap in
        // parallel — refresh login info so signup affordances disappear
        // and the user is returned to the login form.
        error = 'Signup is no longer available. Please sign in.';
        try {
          await appStore.loadLoginInfo();
        } catch {
          // keep the friendly message even if the refresh fails
        }
        showRegister = false;
        registerFields = {};
      } else {
        error = msg || 'Registration failed';
      }
    } finally {
      loading = false;
    }
  }

  function handleOAuth(url: string) {
    location.href = url;
  }
</script>

<div class="flex flex-col items-center justify-start h-full w-full bg-slate-100 pt-8">
  <div class="w-full max-w-sm">
    <div class="bg-white rounded-lg shadow-lg border border-slate-200 p-8">
      <!-- Logo -->
      <div class="flex items-center justify-center gap-2 mb-6">
        <Blocks size={24} color="#EF233C" />
        <div class="flex flex-col leading-none">
          <span class="text-xl font-bold tracking-wide text-slate-800">
            {loginInfo?.title ?? 'pika'}
          </span>
          {#if loginInfo?.subtitle}
            <span class="text-[10px] text-slate-400">{loginInfo.subtitle}</span>
          {:else if appStore.info?.version}
            <span class="text-[10px] font-mono text-slate-400">{appStore.info.version}</span>
          {/if}
        </div>
      </div>

      {#if infoLoading}
        <div class="text-center text-sm text-slate-400 py-4">Loading...</div>
      {:else if infoError}
        <div class="p-3 bg-red-50 border border-red-200 rounded-md text-sm text-red-700">
          {infoError}
        </div>
      {:else}
        <!-- Password strategy: login or register form -->
        {#if passwordStrategy}
          {#if showRegister && hasRegister && passwordStrategy.register}
            <!-- Register form -->
            <form
              onsubmit={(e) => { e.preventDefault(); handleRegister(passwordStrategy!); }}
              class="space-y-4"
            >
              {#each passwordStrategy.register.fields ?? [] as field}
                <div>
                  <label for={`reg-${field.name}`} class="block text-xs font-medium text-slate-600 mb-1">
                    {field.label}
                  </label>
                  <input
                    id={`reg-${field.name}`}
                    type={field.type}
                    value={getFieldValue(registerFields, field.name)}
                    oninput={(e) => { registerFields = setFieldValue(registerFields, field.name, (e.target as HTMLInputElement).value); }}
                    required={field.required ?? false}
                    placeholder={field.placeholder ?? ''}
                    autocomplete={field.type === 'password' ? 'new-password' : field.name}
                    class="w-full px-3 py-2 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                  />
                </div>
              {/each}

              {#if error}
                <div class="p-3 bg-red-50 border border-red-200 rounded-md text-sm text-red-700">
                  {error}
                </div>
              {/if}

              <button
                type="submit"
                disabled={loading}
                class="w-full flex items-center justify-center gap-2 px-4 py-2 bg-blue-500 text-white text-sm font-medium rounded-md hover:bg-blue-600 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
                <UserPlus size={14} />
                {loading ? 'Creating account...' : 'Create Account'}
              </button>
            </form>
          {:else}
            <!-- Login form -->
            <form
              onsubmit={(e) => { e.preventDefault(); handleLogin(passwordStrategy!); }}
              class="space-y-4"
            >
              {#each passwordStrategy.fields ?? [] as field}
                <div>
                  <label for={`login-${field.name}`} class="block text-xs font-medium text-slate-600 mb-1">
                    {field.label}
                  </label>
                  <input
                    id={`login-${field.name}`}
                    type={field.type}
                    value={getFieldValue(loginFields, field.name)}
                    oninput={(e) => { loginFields = setFieldValue(loginFields, field.name, (e.target as HTMLInputElement).value); }}
                    required={field.required ?? false}
                    placeholder={field.placeholder ?? ''}
                    autocomplete={field.type === 'password' ? 'current-password' : field.name}
                    class="w-full px-3 py-2 border border-slate-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
                  />
                </div>
              {/each}

              <button
                type="submit"
                disabled={loading}
                class="w-full flex items-center justify-center gap-2 px-4 py-2 bg-blue-500 text-white text-sm font-medium rounded-md hover:bg-blue-600 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
                <LogIn size={14} />
                {loading ? 'Signing in...' : (passwordStrategy.label || 'Sign in')}
              </button>

              {#if error}
                <div class="p-3 bg-red-50 border border-red-200 rounded-md text-sm text-red-700">
                  {error}
                </div>
              {/if}
            </form>

            {#if hasRegister}
              <button
                type="button"
                class="mt-4 w-full text-center text-xs text-blue-500 hover:text-blue-700 cursor-pointer"
                onclick={() => { showRegister = true; error = ''; }}
              >
                Don't have an account? Sign up
              </button>
            {/if}
          {/if}
        {/if}

        <!-- OAuth2 strategies -->
        {#if oauthStrategies.length > 0}
          {#if passwordStrategy}
            <div class="flex items-center gap-3 my-5">
              <div class="flex-1 h-px bg-slate-200"></div>
              <span class="text-xs text-slate-400">or</span>
              <div class="flex-1 h-px bg-slate-200"></div>
            </div>
          {/if}

          <div class="space-y-2">
            {#each oauthStrategies as strategy}
              <button
                type="button"
                onclick={() => handleOAuth(strategy.url)}
                class="w-full flex items-center justify-center gap-2 px-4 py-2 bg-slate-100 text-slate-700 text-sm font-medium rounded-md border border-slate-300 hover:bg-slate-200 cursor-pointer transition-colors"
              >
                <ExternalLink size={14} />
                {strategy.label}
              </button>
            {/each}
          </div>
        {/if}

        {#if !passwordStrategy && oauthStrategies.length === 0}
          <div class="text-center text-sm text-slate-400 py-4">
            No login methods configured.
          </div>
        {/if}
      {/if}
    </div>
  </div>
</div>
