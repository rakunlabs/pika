<script lang="ts">
 import { appStore } from '@/lib/store/store.svelte';
 import { Blocks, LogIn, UserPlus, ExternalLink, Key } from 'lucide-svelte';
 import { onMount } from 'svelte';
 import type { LoginStrategy } from '@/lib/store/store.svelte';
 import ThemeSwitcher from '@/lib/components/ThemeSwitcher.svelte';
 import axios from 'axios';
 import { isWebAuthnSupported, startAuthentication, type ServerRequestOptions } from '@/lib/webauthn';

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

 // Passkey strategy. The backend advertises kind="passkey" via the
 // ada/passkey Strategy.Descriptor — the login URL is the same
 // /login/pass/<name> endpoint as a password strategy, but the body
 // shape is different (it dispatches between begin/finish via the
 // assertion field's presence). We never POST a form to that URL
 // ourselves; the SPA runs the WebAuthn ceremony and uses two
 // explicit calls instead.
 const passkeyStrategy = $derived(
 strategies.find((s) => s.kind === 'passkey') ?? null
 );
 const passkeySupported = $derived(isWebAuthnSupported());

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

 // Passkey login: two-step ceremony that hits the same strategy URL
 // ada exposes for any login (POST /login/pass/<name>). The first call
 // sends an empty body and gets back { phase:"begin", session_id,
 // options }. The SPA then runs the WebAuthn ceremony and POSTs back
 // { session_id, assertion:{...} } — the strategy keys off "assertion"
 // to dispatch to the finish path.
 async function handlePasskeyLogin(strategy: LoginStrategy) {
 if (!passkeySupported) {
 error = 'Your browser does not support passkeys.';
 return;
 }
 error = '';
 loading = true;
 try {
 // Step 1: begin. POST empty body; server returns options.
 const beginRes = await axios.post<{
 phase: string;
 session_id: string;
 options: ServerRequestOptions;
 }>(strategy.url, {}, { headers: { Accept: 'application/json' } });

 const { session_id, options } = beginRes.data;

 // Step 2: browser ceremony.
 const assertion = await startAuthentication(options);

 // Step 3: finish. Same URL, body now carries the assertion
 // which makes the strategy dispatch to its finish handler.
 // On success the strategy mints a session cookie and ada's
 // auth middleware writes a redirect or success JSON — we just
 // need the cookie to be set, then refresh the post-login state
 // the same way loginWith does.
 await axios.post(strategy.url, { session_id, assertion }, { headers: { Accept: 'application/json' } });

 // Reuse loginWith's post-login fan-out by calling its tail —
 // but loginWith does the POST itself. To avoid re-POSTing we
 // duplicate the post-login refresh logic inline. allSettled
 // matches the rationale in loginWith.
 await Promise.allSettled([
 appStore.loadIdentity(),
 appStore.loadInfo(),
 ]);
 } catch (err: any) {
 const code = err?.name ?? '';
 if (code === 'NotAllowedError') {
 // User cancelled. No toast — silent is friendlier here.
 } else {
 error = err?.response?.data?.message ?? err?.message ?? 'Passkey sign-in failed';
 }
 } finally {
 loading = false;
 }
 }
</script>

<div class="flex flex-col items-center justify-start h-full w-full bg-slate-100 dark:bg-warm-900 pt-8">
 <div class="w-full max-w-sm">
 <div class="relative bg-white dark:bg-warm-900 rounded-lg shadow-lg border border-slate-200 dark:border-warm-700 p-8">
 <!-- Theme switcher: inside the card, pinned top-right. Square, not
 circular. Same component is reused in the navbar (dark variant)
 after login so the toggle is always available. -->
 <ThemeSwitcher class="absolute top-3 right-3" />

 <!-- Logo -->
 <div class="flex items-center justify-center gap-3 mb-6">
 <Blocks size={40} color="#EF233C" />
 <div class="flex flex-col leading-tight">
 <span class="text-2xl font-semibold tracking-tight text-slate-800 dark:text-slate-100">
 {loginInfo?.title ?? 'pika'}
 </span>
 {#if loginInfo?.subtitle}
 <span class="text-sm text-slate-500 dark:text-slate-400">{loginInfo.subtitle}</span>
 {:else if appStore.info?.version}
 <span class="text-sm font-mono text-slate-500 dark:text-slate-400">{appStore.info.version}</span>
 {/if}
 </div>
 </div>

 {#if infoLoading}
 <div class="text-center text-sm text-slate-400 dark:text-slate-500 py-4">Loading...</div>
 {:else if infoError}
 <div class="p-3 bg-red-50 dark:bg-red-900/30 border border-red-200 dark:border-red-800 rounded-md text-sm text-red-700 dark:text-red-300">
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
 <label for={`reg-${field.name}`} class="block text-xs font-medium text-slate-600 dark:text-slate-300 mb-1">
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
 class="w-full px-3 py-2 border border-slate-300 dark:border-warm-500 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent"
 />
 </div>
 {/each}

 {#if error}
 <div class="p-3 bg-red-50 dark:bg-red-900/30 border border-red-200 dark:border-red-800 rounded-md text-sm text-red-700 dark:text-red-300">
 {error}
 </div>
 {/if}

 <button
 type="submit"
 disabled={loading}
 class="w-full flex items-center justify-center gap-2 px-4 py-2 bg-brand-500 text-white text-sm font-medium rounded-md hover:bg-brand-600 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
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
 <label for={`login-${field.name}`} class="block text-xs font-medium text-slate-600 dark:text-slate-300 mb-1">
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
 class="w-full px-3 py-2 border border-slate-300 dark:border-warm-500 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent"
 />
 </div>
 {/each}

 <button
 type="submit"
 disabled={loading}
 class="w-full flex items-center justify-center gap-2 px-4 py-2 bg-brand-500 text-white text-sm font-medium rounded-md hover:bg-brand-600 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
 >
 <LogIn size={14} />
 {loading ? 'Signing in...' : (passwordStrategy.label || 'Sign in')}
 </button>

 {#if error}
 <div class="p-3 bg-red-50 dark:bg-red-900/30 border border-red-200 dark:border-red-800 rounded-md text-sm text-red-700 dark:text-red-300">
 {error}
 </div>
 {/if}
 </form>

 {#if hasRegister}
 <button
 type="button"
 class="mt-4 w-full text-center text-xs text-brand-500 hover:text-brand-400 cursor-pointer"
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
 <div class="flex-1 h-px bg-slate-200 dark:bg-warm-900"></div>
 <span class="text-xs text-slate-400 dark:text-slate-500">or</span>
 <div class="flex-1 h-px bg-slate-200 dark:bg-warm-900"></div>
 </div>
 {/if}

  <div class="space-y-2">
  {#each oauthStrategies as strategy}
  <button
  type="button"
  onclick={() => handleOAuth(strategy.url)}
  class="w-full flex items-center justify-center gap-2 px-4 py-2 bg-slate-100 dark:bg-warm-900 text-slate-700 dark:text-slate-200 text-sm font-medium rounded-md border border-slate-300 dark:border-warm-500 hover:bg-slate-200 dark:hover:bg-warm-600 cursor-pointer transition-colors"
  >
  <ExternalLink size={14} />
  {strategy.label}
  </button>
  {/each}
  </div>
  {/if}

  <!-- Passkey strategy. Always shown below password/oauth, with a
  divider when something else is present. We render the button
  even when the browser doesn't support WebAuthn — clicking it
  surfaces a friendly error rather than silently hiding the
  affordance, which would confuse users on managed devices. -->
  {#if passkeyStrategy}
  {#if passwordStrategy || oauthStrategies.length > 0}
  <div class="flex items-center gap-3 my-5">
  <div class="flex-1 h-px bg-slate-200 dark:bg-warm-900"></div>
  <span class="text-xs text-slate-400 dark:text-slate-500">or</span>
  <div class="flex-1 h-px bg-slate-200 dark:bg-warm-900"></div>
  </div>
  {/if}
  <button
  type="button"
  onclick={() => handlePasskeyLogin(passkeyStrategy)}
  disabled={loading}
  class="w-full flex items-center justify-center gap-2 px-4 py-2 bg-slate-100 dark:bg-warm-900 text-slate-700 dark:text-slate-200 text-sm font-medium rounded-md border border-slate-300 dark:border-warm-500 hover:bg-slate-200 dark:hover:bg-warm-600 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
  >
  <Key size={14} />
  {loading ? 'Waiting for device...' : (passkeyStrategy.label || 'Sign in with passkey')}
  </button>
  {/if}

  {#if !passwordStrategy && oauthStrategies.length === 0 && !passkeyStrategy}
  <div class="text-center text-sm text-slate-400 dark:text-slate-500 py-4">
  No login methods configured.
  </div>
  {/if}
  {/if}
 </div>
 </div>
</div>
