<script lang="ts">
 import { appStore } from '@/lib/store/store.svelte';
 import { Blocks, LogIn, UserPlus, ExternalLink, Key, ShieldCheck, ArrowLeft } from 'lucide-svelte';
 import { onMount, onDestroy } from 'svelte';
 import type { LoginStrategy } from '@/lib/store/store.svelte';
 import ThemeSwitcher from '@/lib/components/ThemeSwitcher.svelte';
 import axios from 'axios';
 import {
  isWebAuthnSupported,
  isConditionalMediationAvailable,
  startAuthentication,
  type ServerRequestOptions,
 } from '@/lib/webauthn';

 let loading = $state(false);
 let infoLoading = $state(true);
 let infoError = $state('');
 let error = $state('');

 // signup_first: show register form first when the server requests it
 let showRegister = $state(false);

 // Track form field values keyed by field name
 let loginFields = $state<Record<string, string>>({});
 let registerFields = $state<Record<string, string>>({});

 // MFA / TOTP step-up state. When the password POST returns a
 // `phase: totp_required` response, we stash the session id + url
 // here and flip the form into "enter your TOTP code" mode. The
 // sessionUrl is the same /login/pass/<name> the password went to —
 // phase-2 is dispatched server-side by body shape.
 interface MFAChallenge {
  url: string;
  sessionID: string;
  strategy: string;
  expiresIn: number;
 }
 let mfaChallenge = $state<MFAChallenge | null>(null);
 let mfaCode = $state('');

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

 // Conditional mediation ("autofill UI") is gated on a runtime feature
 // probe — Safari 16+, Chrome 108+, Firefox is partial. When available
 // we paint `autocomplete="username webauthn"` on the username input
 // so the browser surfaces enrolled passkeys inline with autofill, and
 // we kick off a non-blocking conditional get() ceremony that resolves
 // when the user picks a passkey from the dropdown.
 let conditionalSupported = $state(false);
 let conditionalController: AbortController | null = null;

 // Used as the `autocomplete` token suffix when conditional UI is on.
 // Field name heuristic: we treat anything that looks like a username
 // field (the password strategy's first non-password field, or fields
 // named "username"/"email"/"user") as the surface for the webauthn
 // hint. Other text fields keep their original autocomplete value so
 // we don't disturb password manager autofill on, say, an org-name
 // input.
 //
 // The return type is widened to `any` because the WebIDL FullAutoFill
 // union doesn't model multi-token compound values like
 // `"username webauthn"` cleanly — the HTML spec allows it (the
 // browser parses it as autofill field + credential type) but the
 // TypeScript DOM types reject it. Using `any` here is the smallest
 // escape hatch; the value still flows into the autocomplete
 // attribute verbatim.
 function passkeyAutocomplete(fieldName: string, fieldType: string): any {
  if (fieldType === 'password') return 'current-password';
  if (!passkeyStrategy || !conditionalSupported) return fieldName;
  const looksLikeUsername = fieldName === 'username' || fieldName === 'email' || fieldName === 'user';
  return looksLikeUsername ? 'username webauthn' : fieldName;
 }

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
 // Fire-and-forget the conditional-UI ceremony so the browser's
 // autofill dropdown can surface enrolled passkeys the moment the
 // user focuses the username field. We deliberately don't await
 // this — the login form must stay interactive even when the
 // conditional get() sits there for minutes waiting for the user
 // to pick something.
 if (passkeyStrategy) {
 void tryConditionalAuth(passkeyStrategy);
 }
 } catch (err: any) {
 infoError = err?.response?.data?.message || 'Failed to load login configuration';
 } finally {
 infoLoading = false;
 }
 });

 onDestroy(() => {
 // Cancel any in-flight conditional get() so a stale resolve can't
 // race the next route — without this an autofill pick after the
 // user navigates away would still try to post a finish request.
 conditionalController?.abort();
 conditionalController = null;
 });

 function getFieldValue(fields: Record<string, string>, name: string): string {
 return fields[name] ?? '';
 }

 function setFieldValue(fields: Record<string, string>, name: string, value: string): Record<string, string> {
 return { ...fields, [name]: value };
 }

 async function handleLogin(strategy: LoginStrategy) {
 // Cancel any in-flight conditional passkey ceremony. Submitting
 // the password form is an explicit signal that the user doesn't
 // want to use a passkey on this attempt; leaving the conditional
 // get() running would race with the form post in some browsers.
 conditionalController?.abort();
 conditionalController = null;

 error = '';
 loading = true;
 try {
 const body: Record<string, string> = {};
 for (const field of strategy.fields ?? []) {
 body[field.name] = loginFields[field.name] ?? '';
 }
 const challenge = await appStore.loginWith(strategy.url, body);
 if (challenge) {
 // Server requires a second factor. Flip the form into TOTP mode;
 // the user enters the 6-digit code (or a recovery code) and we
 // POST it back to the same url with the session id we just got.
 mfaChallenge = {
 url: strategy.url,
 sessionID: challenge.totp_session_id,
 strategy: challenge.strategy,
 expiresIn: challenge.expires_in,
 };
 mfaCode = '';
 error = '';
 return;
 }
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

 async function handleMFASubmit() {
 if (!mfaChallenge) return;
 const code = mfaCode.trim();
 // Accept either a 6-digit TOTP code or a 14-char recovery code
 // (xxxx-xxxx-xxxx). The server validates the format too — this
 // is just a UX trim.
 if (!code) {
 error = 'Enter the 6-digit code or a recovery code';
 return;
 }
 error = '';
 loading = true;
 try {
 await appStore.finishMFA(mfaChallenge.url, mfaChallenge.sessionID, code);
 // On success App.svelte swaps us out of the login view.
 mfaChallenge = null;
 mfaCode = '';
 } catch (err: any) {
 error = err?.response?.data?.message || 'Verification failed';
 // Don't clear mfaChallenge — the server only marks the session as
 // consumed after a successful verification, so the user can retry
 // with a fresh code from their authenticator. But the server-side
 // ConsumePending actually drops the entry on first attempt; surface
 // the message and let them retry from password.
 if (err?.response?.status === 401 && err?.response?.data?.error === 'invalid_session') {
 // Session was already consumed or expired — start over.
 mfaChallenge = null;
 mfaCode = '';
 error = 'Your verification session expired. Please sign in again.';
 }
 } finally {
 loading = false;
 }
 }

 function cancelMFA() {
 mfaChallenge = null;
 mfaCode = '';
 error = '';
 // Note: the server's pending entry will sit until its TTL fires
 // (5 min). We could call a dedicated cancel endpoint, but the
 // one-shot semantics on the server side make a stale entry
 // harmless — no second attempt is possible.
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
 // A click on the manual passkey button overrides any in-flight
 // conditional get(). If we leave the conditional ceremony running
 // the explicit one will throw "operation already in progress" in
 // some browsers.
 conditionalController?.abort();
 conditionalController = null;

 error = '';
 loading = true;
 try {
 // Step 1: begin. POST empty body; server returns options. The
 // backend goes discoverable when no user_handle / username hint
 // is supplied, which is what we want for an explicit click — the
 // platform UI shows the credential picker.
 const beginRes = await axios.post<{
 phase: string;
 session_id: string;
 options: ServerRequestOptions;
 }>(strategy.url, {}, { headers: { Accept: 'application/json' } });

 const { session_id, options } = beginRes.data;

 // Step 2: browser ceremony.
 const assertion = await startAuthentication(options);
 if (!assertion) {
 // User dismissed the picker without choosing a credential. No
 // toast — silent is friendlier here.
 return;
 }

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

 // Conditional ("autofill") passkey login.
 //
 // Conditional mediation is the WebAuthn flow where the browser hangs
 // the get() promise in the background and offers the enrolled
 // passkey via the username input's autofill dropdown. Picking one
 // resolves the promise; typing a password and submitting the form
 // never resolves it — the AbortController on the form handlers
 // tears the ceremony down so the next attempt has a fresh one.
 //
 // Failure modes are all silent: a busted conditional ceremony must
 // never block the manual sign-in paths from working, so we swallow
 // errors here and let the user fall back to typing credentials.
 async function tryConditionalAuth(strategy: LoginStrategy) {
 if (!passkeySupported) return;
 if (!(await isConditionalMediationAvailable())) return;
 conditionalSupported = true;

 // Tear down any previous attempt (e.g. on hot reload).
 conditionalController?.abort();
 conditionalController = new AbortController();
 const signal = conditionalController.signal;

 try {
 const beginRes = await axios.post<{
 phase: string;
 session_id: string;
 options: ServerRequestOptions;
 }>(strategy.url, {}, { headers: { Accept: 'application/json' } });
 if (signal.aborted) return;

 const { session_id, options } = beginRes.data;
 const assertion = await startAuthentication(options, {
 mediation: 'conditional',
 signal,
 });
 if (!assertion || signal.aborted) return; // user picked something else or aborted

 await axios.post(strategy.url, { session_id, assertion }, { headers: { Accept: 'application/json' } });
 await Promise.allSettled([
 appStore.loadIdentity(),
 appStore.loadInfo(),
 ]);
 } catch {
 // Swallowed on purpose: conditional UI is a progressive
 // enhancement. Any failure here must not pre-empt the manual
 // sign-in flow that's also live on the page.
 }
 }
</script>

<div class="flex flex-col items-center justify-start h-full w-full bg-slate-100 dark:bg-warm-900 pt-8">
 <div class="w-full max-w-sm">
 <div class="relative bg-white dark:bg-warm-800 rounded-lg shadow-lg border border-slate-200 dark:border-warm-700 p-8">
 <!-- Version: top-left mirror of the theme switcher. Absolute so it
 sits in the card's chrome instead of competing with the title for
 vertical space, and it's always present when the server reports
 a version — independent of whether `subtitle` is set. -->
 {#if appStore.info?.version}
 <span class="absolute top-3 left-3 text-[10px] font-mono text-slate-400 dark:text-slate-500">
 {appStore.info.version}
 </span>
 {/if}

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
 <!-- Subtitle slot. Always rendered (min-h reserves the line) so
 the form below doesn't shift when the server toggles
 subtitle on/off, and so version no longer fights for this
 spot — version lives in the top-left now. -->
 <span class="text-sm text-slate-500 dark:text-slate-400 min-h-5">
 {loginInfo?.subtitle ?? ''}
 </span>
 </div>
 </div>

  {#if infoLoading}
  <div class="text-center text-sm text-slate-400 dark:text-slate-500 py-4">Loading...</div>
  {:else if infoError}
  <div class="p-3 bg-red-50 dark:bg-red-900/30 border border-red-200 dark:border-red-800 rounded-md text-sm text-red-700 dark:text-red-300">
  {infoError}
  </div>
  {:else if mfaChallenge}
  <!-- TOTP step-up: shown after password succeeds but the user has 2FA on. -->
  <form
  onsubmit={(e) => { e.preventDefault(); handleMFASubmit(); }}
  class="space-y-4"
  >
  <div class="flex flex-col items-center text-center">
   <ShieldCheck size={32} class="text-accent-600 dark:text-accent-400 mb-2" />
   <h2 class="text-base font-semibold text-slate-800 dark:text-slate-100">
    Two-factor authentication
   </h2>
   <p class="mt-1 text-xs text-slate-500 dark:text-slate-400 max-w-xs">
    Enter the 6-digit code from your authenticator app, or one of
    your recovery codes if you lost the device.
   </p>
  </div>

  <div>
   <label for="mfa-code" class="block text-xs font-medium text-slate-600 dark:text-slate-300 mb-1">
    Code
   </label>
   <input
    id="mfa-code"
    type="text"
    inputmode="text"
    autocomplete="one-time-code"
    autocapitalize="off"
    spellcheck="false"
    bind:value={mfaCode}
    placeholder="123456 or xxxx-xxxx-xxxx"
    required
    class="w-full px-3 py-2 text-center font-mono text-lg tracking-widest border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 rounded-md focus:outline-none focus:ring-2 focus:ring-accent-500 focus:border-transparent"
   />
  </div>

  {#if error}
   <div class="p-3 bg-red-50 dark:bg-red-900/30 border border-red-200 dark:border-red-800 rounded-md text-sm text-red-700 dark:text-red-300">
    {error}
   </div>
  {/if}

  <button
   type="submit"
   disabled={loading}
   class="w-full flex items-center justify-center gap-2 px-4 py-2 bg-accent-600 text-white text-sm font-medium rounded-md hover:bg-accent-700 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
  >
   <LogIn size={14} />
   {loading ? 'Verifying...' : 'Verify and sign in'}
  </button>

  <button
   type="button"
   onclick={cancelMFA}
   class="w-full flex items-center justify-center gap-1.5 px-4 py-2 text-xs text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 cursor-pointer"
  >
   <ArrowLeft size={12} />
   Back to sign in
  </button>
  </form>
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
 class="w-full px-3 py-2 border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-accent-500 focus:border-transparent"
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
 class="w-full flex items-center justify-center gap-2 px-4 py-2 bg-accent-600 text-white text-sm font-medium rounded-md hover:bg-accent-700 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
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
  autocomplete={passkeyAutocomplete(field.name, field.type)}
  class="w-full px-3 py-2 border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-accent-500 focus:border-transparent"
  />
  </div>
  {/each}
 
  <button
  type="submit"
  disabled={loading}
  class="w-full flex items-center justify-center gap-2 px-4 py-2 bg-accent-600 text-white text-sm font-medium rounded-md hover:bg-accent-700 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
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
 class="mt-4 w-full text-center text-xs text-accent-500 hover:text-accent-400 cursor-pointer"
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
  class="w-full flex items-center justify-center gap-2 px-4 py-2 bg-slate-100 dark:bg-warm-900 text-slate-700 dark:text-slate-200 text-sm font-medium rounded-md border border-slate-300 dark:border-warm-600 hover:bg-slate-200 dark:hover:bg-warm-700 cursor-pointer transition-colors"
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
  class="w-full flex items-center justify-center gap-2 px-4 py-2 bg-slate-100 dark:bg-warm-900 text-slate-700 dark:text-slate-200 text-sm font-medium rounded-md border border-slate-300 dark:border-warm-600 hover:bg-slate-200 dark:hover:bg-warm-700 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
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
