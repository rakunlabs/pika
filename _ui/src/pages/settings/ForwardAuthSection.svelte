<script lang="ts">
  import { configStore } from "@/lib/store/config.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import { appStore } from "@/lib/store/store.svelte";
  import { onMount } from "svelte";
  import { Trash2, Lock, Check, ShieldAlert } from "lucide-svelte";
  import type { ForwardAuthSettings, ExternalPermissionsSettings } from "@/lib/types/config";
  import axios from 'axios';

  // Internal tab within the Forward Auth section
  let forwardAuthTab = $state<'settings' | 'permissions'>('settings');

  // ── Forward Auth state ──
  let fwdAuthEnabled = $state(false);
  let fwdAuthAddress = $state('');
  let fwdAuthResponseHeaders = $state<string[]>([]);
  let fwdAuthResponseHeaderInput = $state('');
  let fwdAuthResponseHeadersRegex = $state('');
  let fwdAuthRequestHeaders = $state<string[]>([]);
  let fwdAuthRequestHeaderInput = $state('');
  let fwdAuthTrustForwardHeader = $state(false);
  let fwdAuthInsecureSkipVerify = $state(false);
  let fwdAuthTimeout = $state('30s');
  let fwdAuthRedirectURL = $state('');
  let fwdAuthRedirectCode = $state(302);
  let fwdAuthRedirectStatusCodes = $state<number[]>([401]);
  let fwdAuthRequestMethod = $state('GET');
  let isSavingForwardAuth = $state(false);

  // ── External Permissions (forward-auth mapping) state ──
  let extPermEnabled = $state(false);
  let extPermGroupsHeader = $state('X-Groups');
  let extPermGroupsSeparator = $state(',');
  // Rows of (external group name, selected capability keys).
  // Keys are rendered via the capabilities list from /api/v1/info.
  let extPermMappingRows = $state<{ group: string; keys: string[] }[]>([]);
  let extPermSuperadmins = $state<string[]>([]);
  let extPermSuperadminInput = $state('');
  let isSavingExtPerm = $state(false);

  // Derived from appStore — canonical capability list from the server.
  const capabilities = $derived(appStore.info?.capabilities ?? []);

  // ── Forward Auth handlers ──

  // X-User is always required — pika reads it to identify forward-auth users.
  const REQUIRED_RESPONSE_HEADERS = ['X-User'];

  function ensureRequiredResponseHeaders(headers: string[]): string[] {
    const result = [...headers];
    for (const h of REQUIRED_RESPONSE_HEADERS) {
      if (!result.includes(h)) result.unshift(h);
    }
    return result;
  }

  function loadForwardAuthSettings() {
    const fa = configStore.settings?.forward_auth;
    fwdAuthEnabled = fa?.enabled ?? false;
    fwdAuthAddress = fa?.address ?? '';
    fwdAuthResponseHeaders = ensureRequiredResponseHeaders(fa?.auth_response_headers || []);
    fwdAuthResponseHeaderInput = '';
    fwdAuthResponseHeadersRegex = fa?.auth_response_headers_regex ?? '';
    fwdAuthRequestHeaders = [...(fa?.auth_request_headers || [])];
    fwdAuthRequestHeaderInput = '';
    fwdAuthTrustForwardHeader = fa?.trust_forward_header ?? false;
    fwdAuthInsecureSkipVerify = fa?.insecure_skip_verify ?? false;
    fwdAuthTimeout = fa?.timeout || '30s';
    fwdAuthRedirectURL = fa?.redirect_url ?? '';
    fwdAuthRedirectCode = fa?.redirect_code || 302;
    fwdAuthRedirectStatusCodes = [...(fa?.redirect_status_codes || [401])];
    fwdAuthRequestMethod = fa?.request_method || 'GET';
  }

  function addFwdAuthResponseHeader() {
    const v = fwdAuthResponseHeaderInput.trim();
    if (!v) return;
    if (fwdAuthResponseHeaders.includes(v)) return;
    fwdAuthResponseHeaders = [...fwdAuthResponseHeaders, v];
    fwdAuthResponseHeaderInput = '';
  }

  function removeFwdAuthResponseHeader(index: number) {
    const header = fwdAuthResponseHeaders[index];
    if (REQUIRED_RESPONSE_HEADERS.includes(header)) return; // cannot remove required headers
    fwdAuthResponseHeaders = fwdAuthResponseHeaders.filter((_, i) => i !== index);
  }

  function addFwdAuthRequestHeader() {
    const v = fwdAuthRequestHeaderInput.trim();
    if (!v) return;
    if (fwdAuthRequestHeaders.includes(v)) return;
    fwdAuthRequestHeaders = [...fwdAuthRequestHeaders, v];
    fwdAuthRequestHeaderInput = '';
  }

  function removeFwdAuthRequestHeader(index: number) {
    fwdAuthRequestHeaders = fwdAuthRequestHeaders.filter((_, i) => i !== index);
  }

  async function handleSaveForwardAuth() {
    isSavingForwardAuth = true;
    try {
      if (fwdAuthEnabled && !fwdAuthAddress.trim()) {
        addToast('Address is required when forward-auth is enabled', 'alert');
        return;
      }

      const responseHeaders = ensureRequiredResponseHeaders(fwdAuthResponseHeaders);
      const cfg: ForwardAuthSettings = {
        enabled: fwdAuthEnabled,
        address: fwdAuthAddress.trim(),
        auth_response_headers: responseHeaders,
        auth_response_headers_regex: fwdAuthResponseHeadersRegex.trim() || undefined,
        auth_request_headers: fwdAuthRequestHeaders.length > 0 ? [...fwdAuthRequestHeaders] : undefined,
        trust_forward_header: fwdAuthTrustForwardHeader || undefined,
        insecure_skip_verify: fwdAuthInsecureSkipVerify || undefined,
        timeout: fwdAuthTimeout.trim() || undefined,
        redirect_url: fwdAuthRedirectURL.trim() || undefined,
        redirect_code: fwdAuthRedirectCode !== 302 ? fwdAuthRedirectCode : undefined,
        redirect_status_codes: fwdAuthRedirectStatusCodes.length > 0 ? [...fwdAuthRedirectStatusCodes] : undefined,
        request_method: fwdAuthRequestMethod !== 'GET' ? fwdAuthRequestMethod : undefined,
      };

      await axios.post('/api/v1/settings', {
        action: 'set',
        forward_auth: cfg,
      });
      await configStore.loadSettings();
      await appStore.loadInfo();
      addToast('Forward-auth settings saved', 'success');
    } catch (error: any) {
      const msg = error.response?.data?.message || 'Failed to save forward-auth settings';
      addToast(msg, 'alert');
    } finally {
      isSavingForwardAuth = false;
    }
  }

  // ── External Permissions handlers ──

  function loadExternalPermissionsSettings() {
    const s = configStore.settings;
    const ep = s?.external_permissions;
    extPermEnabled = ep?.enabled ?? false;
    extPermGroupsHeader = ep?.groups_header || 'X-Groups';
    extPermGroupsSeparator = ep?.groups_separator || ',';
    extPermSuperadmins = [...(ep?.superadmins || [])];
    extPermSuperadminInput = '';
    extPermMappingRows = Object.entries(ep?.mapping || {}).map(([group, keys]) => ({
      group,
      keys: [...keys],
    }));
    if (extPermMappingRows.length === 0) {
      // Start with one empty row so the form isn't blank
      extPermMappingRows = [{ group: '', keys: [] }];
    }
  }

  function addExtPermMappingRow() {
    extPermMappingRows = [...extPermMappingRows, { group: '', keys: [] }];
  }

  function removeExtPermMappingRow(index: number) {
    extPermMappingRows = extPermMappingRows.filter((_, i) => i !== index);
    if (extPermMappingRows.length === 0) {
      extPermMappingRows = [{ group: '', keys: [] }];
    }
  }

  function toggleExtPermMappingKey(rowIndex: number, key: string) {
    const row = extPermMappingRows[rowIndex];
    if (!row) return;
    const idx = row.keys.indexOf(key);
    if (idx >= 0) {
      row.keys = row.keys.filter((k) => k !== key);
    } else {
      row.keys = [...row.keys, key];
    }
    extPermMappingRows = [...extPermMappingRows];
  }

  function addExtPermSuperadmin() {
    const v = extPermSuperadminInput.trim();
    if (!v) return;
    if (extPermSuperadmins.includes(v)) {
      addToast('Superadmin already added', 'alert');
      return;
    }
    extPermSuperadmins = [...extPermSuperadmins, v];
    extPermSuperadminInput = '';
  }

  function removeExtPermSuperadmin(index: number) {
    extPermSuperadmins = extPermSuperadmins.filter((_, i) => i !== index);
  }

  async function handleSaveExternalPermissions() {
    isSavingExtPerm = true;
    try {
      // Validate: each non-empty row must have at least one key, and keys
      // must match a known capability.
      const mapping: Record<string, string[]> = {};
      const knownKeySet = new Set(capabilities.map((c) => c.key));
      for (const row of extPermMappingRows) {
        const group = row.group.trim();
        if (!group) continue;
        if (mapping[group]) {
          addToast(`Duplicate group "${group}" in mapping`, 'alert');
          return;
        }
        const filteredKeys = row.keys.filter((k) => knownKeySet.has(k));
        mapping[group] = filteredKeys;
      }

      const cfg: ExternalPermissionsSettings = {
        enabled: extPermEnabled,
        groups_header: extPermGroupsHeader.trim() || undefined,
        groups_separator: extPermGroupsSeparator || undefined,
        mapping: Object.keys(mapping).length > 0 ? mapping : undefined,
        superadmins: extPermSuperadmins.length > 0 ? [...extPermSuperadmins] : undefined,
      };

      await axios.post('/api/v1/settings', {
        action: 'set',
        external_permissions: cfg,
      });
      await configStore.loadSettings();
      // Refresh info so auth_enabled / permissions update immediately.
      await appStore.loadInfo();
      addToast('External permissions saved', 'success');
    } catch (error: any) {
      const msg = error.response?.data?.message || 'Failed to save external permissions';
      addToast(msg, 'alert');
    } finally {
      isSavingExtPerm = false;
    }
  }

  // Load both settings on mount
  onMount(() => {
    loadForwardAuthSettings();
    loadExternalPermissionsSettings();
  });
</script>

<div>
  <div class="mb-4">
    <h2 class="text-lg font-semibold text-slate-800">Forward Auth</h2>
    <p class="text-sm text-slate-500 mt-0.5">
      Delegate authentication to an external service and map external groups to pika permissions.
    </p>
  </div>

  <!-- Internal tabs -->
  <div class="flex gap-1 mb-4 border-b border-slate-200">
    <button
      type="button"
      class="px-4 py-2 text-sm font-medium transition-colors
        {forwardAuthTab === 'settings'
          ? 'text-blue-700 border-b-2 border-blue-600'
          : 'text-slate-500 hover:text-slate-700'}"
      onclick={() => forwardAuthTab = 'settings'}
    >
      Settings
    </button>
    <button
      type="button"
      class="px-4 py-2 text-sm font-medium transition-colors
        {forwardAuthTab === 'permissions'
          ? 'text-blue-700 border-b-2 border-blue-600'
          : 'text-slate-500 hover:text-slate-700'}"
      onclick={() => forwardAuthTab = 'permissions'}
    >
      Permissions
    </button>
  </div>

  <!-- ── Settings tab ── -->
  {#if forwardAuthTab === 'settings'}
  <div class="p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
    <!-- Enable toggle -->
    <div class="mb-5">
      <label class="flex items-start gap-2 cursor-pointer">
        <input type="checkbox" bind:checked={fwdAuthEnabled} class="mt-0.5 rounded border-slate-300" />
        <div class="flex-1">
          <div class="text-sm font-medium text-slate-700">Enable forward-auth</div>
          <p class="mt-0.5 text-[11px] text-slate-400 leading-relaxed">
            When enabled, unauthenticated requests (no local session) are validated against the
            external auth service. The middleware is hot-swapped at runtime.
          </p>
        </div>
      </label>
    </div>

    <!-- Address -->
    <div class="mb-4">
      <label for="fwd-auth-address" class="block text-xs font-medium text-slate-500 mb-1">Auth Service Address</label>
      <input id="fwd-auth-address" type="text" bind:value={fwdAuthAddress}
        placeholder="http://auth-service:9000/verify"
        class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
      <p class="mt-1 text-[11px] text-slate-400">URL of the external authentication service endpoint.</p>
    </div>

    <!-- Response Headers -->
    <div class="mb-4">
      <label for="fwd-auth-resp-header" class="block text-xs font-medium text-slate-500 mb-1">Auth Response Headers</label>
      <p class="text-[11px] text-slate-400 mb-2">
        Headers to copy from the auth service response onto the original request.
        <code class="px-0.5 bg-slate-100 rounded text-[10px]">X-User</code> is always required — pika
        reads it to identify forward-auth users. Add any additional headers your auth service returns
        (e.g. X-Groups for permission mapping).
      </p>
      <div class="flex gap-2">
        <input id="fwd-auth-resp-header" type="text" bind:value={fwdAuthResponseHeaderInput}
          placeholder="X-Groups"
          onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addFwdAuthResponseHeader(); } }}
          class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
        <button class="px-3 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors"
          onclick={addFwdAuthResponseHeader}>Add</button>
      </div>
      {#if fwdAuthResponseHeaders.length > 0}
        <div class="mt-2 flex flex-wrap gap-1.5">
          {#each fwdAuthResponseHeaders as header, i}
            {@const isRequired = REQUIRED_RESPONSE_HEADERS.includes(header)}
            {#if isRequired}
              <span class="inline-flex items-center gap-1 px-2 py-1 bg-slate-100 border border-slate-300 rounded text-xs font-mono text-slate-500" title="Required by pika">
                {header}
                <Lock size={10} class="text-slate-400" />
              </span>
            {:else}
              <span class="inline-flex items-center gap-1 px-2 py-1 bg-blue-50 border border-blue-200 rounded text-xs font-mono text-blue-700">
                {header}
                <button class="flex items-center justify-center w-3.5 h-3.5 p-0 border-none cursor-pointer bg-transparent text-blue-400 hover:text-red-500 transition-colors"
                  onclick={() => removeFwdAuthResponseHeader(i)} title="Remove">&times;</button>
              </span>
            {/if}
          {/each}
        </div>
      {/if}
    </div>

    <!-- Response Headers Regex -->
    <div class="mb-4">
      <label for="fwd-auth-resp-regex" class="block text-xs font-medium text-slate-500 mb-1">Response Headers Regex</label>
      <input id="fwd-auth-resp-regex" type="text" bind:value={fwdAuthResponseHeadersRegex}
        placeholder="^X-Auth-.*"
        class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
      <p class="mt-1 text-[11px] text-slate-400">Regex pattern to match additional auth response headers to copy. Leave empty to disable.</p>
    </div>

    <!-- Request Headers -->
    <div class="mb-4">
      <label for="fwd-auth-req-header" class="block text-xs font-medium text-slate-500 mb-1">Auth Request Headers (allowlist)</label>
      <p class="text-[11px] text-slate-400 mb-2">Only forward these headers to the auth service. If empty, all headers are forwarded.</p>
      <div class="flex gap-2">
        <input id="fwd-auth-req-header" type="text" bind:value={fwdAuthRequestHeaderInput}
          placeholder="Authorization"
          onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addFwdAuthRequestHeader(); } }}
          class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
        <button class="px-3 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors"
          onclick={addFwdAuthRequestHeader}>Add</button>
      </div>
      {#if fwdAuthRequestHeaders.length > 0}
        <div class="mt-2 flex flex-wrap gap-1.5">
          {#each fwdAuthRequestHeaders as header, i}
            <span class="inline-flex items-center gap-1 px-2 py-1 bg-slate-50 border border-slate-200 rounded text-xs font-mono text-slate-600">
              {header}
              <button class="flex items-center justify-center w-3.5 h-3.5 p-0 border-none cursor-pointer bg-transparent text-slate-400 hover:text-red-500 transition-colors"
                onclick={() => removeFwdAuthRequestHeader(i)} title="Remove">&times;</button>
            </span>
          {/each}
        </div>
      {/if}
    </div>

    <!-- Timeout + Method + Redirect Code -->
    <div class="grid grid-cols-3 gap-3 mb-4">
      <div>
        <label for="fwd-auth-timeout" class="block text-xs font-medium text-slate-500 mb-1">Timeout</label>
        <input id="fwd-auth-timeout" type="text" bind:value={fwdAuthTimeout}
          placeholder="30s"
          class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
      </div>
      <div>
        <label for="fwd-auth-method" class="block text-xs font-medium text-slate-500 mb-1">Request Method</label>
        <select id="fwd-auth-method" bind:value={fwdAuthRequestMethod}
          class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10">
          <option value="GET">GET</option>
          <option value="POST">POST</option>
          <option value="HEAD">HEAD</option>
        </select>
      </div>
      <div>
        <label for="fwd-auth-redirect-code" class="block text-xs font-medium text-slate-500 mb-1">Redirect Code</label>
        <select id="fwd-auth-redirect-code" bind:value={fwdAuthRedirectCode}
          class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10">
          <option value={301}>301</option>
          <option value={302}>302</option>
          <option value={307}>307</option>
          <option value={308}>308</option>
        </select>
      </div>
    </div>

    <!-- Redirect URL -->
    <div class="mb-4">
      <label for="fwd-auth-redirect-url" class="block text-xs font-medium text-slate-500 mb-1">Redirect URL</label>
      <input id="fwd-auth-redirect-url" type="text" bind:value={fwdAuthRedirectURL}
        placeholder="https://login.example.com?rd={'{url}'}"
        class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
      <p class="mt-1 text-[11px] text-slate-400">
        Where to redirect GET/HEAD requests on auth failure. Placeholders:
        <code class="px-0.5 bg-slate-100 rounded text-[10px]">{'{url}'}</code>,
        <code class="px-0.5 bg-slate-100 rounded text-[10px]">{'{uri}'}</code>,
        <code class="px-0.5 bg-slate-100 rounded text-[10px]">{'{host}'}</code>.
        Leave empty to return the auth service response directly.
      </p>
    </div>

    <!-- Toggles -->
    <div class="mb-5 space-y-3">
      <label class="flex items-start gap-2 cursor-pointer">
        <input type="checkbox" bind:checked={fwdAuthTrustForwardHeader} class="mt-0.5 rounded border-slate-300" />
        <div class="flex-1">
          <div class="text-sm font-medium text-slate-700">Trust forward headers</div>
          <p class="mt-0.5 text-[11px] text-slate-400">Reuse existing X-Forwarded-* headers instead of overwriting them.</p>
        </div>
      </label>
      <label class="flex items-start gap-2 cursor-pointer">
        <input type="checkbox" bind:checked={fwdAuthInsecureSkipVerify} class="mt-0.5 rounded border-slate-300" />
        <div class="flex-1">
          <div class="text-sm font-medium text-slate-700">Skip TLS verification</div>
          <p class="mt-0.5 text-[11px] text-slate-400 text-amber-600">Development only. Disables certificate verification for the auth service connection.</p>
        </div>
      </label>
    </div>

    <!-- Save button -->
    <div class="flex justify-end">
      <button
        onclick={handleSaveForwardAuth}
        disabled={isSavingForwardAuth}
        class="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors cursor-pointer"
      >
        {isSavingForwardAuth ? 'Saving...' : 'Save Settings'}
      </button>
    </div>
  </div>
  {/if}

  <!-- ── Permissions tab ── -->
  {#if forwardAuthTab === 'permissions'}
  <div class="p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
    <p class="text-[11px] text-slate-400 mb-4">
      Map forward-auth groups to pika capability keys. When enabled, pika reads a groups
      header from each request and checks the mapping below to decide what the user can do.
    </p>

    <!-- Enable toggle -->
    <div class="mb-5">
      <label class="flex items-start gap-2 cursor-pointer">
        <input type="checkbox" bind:checked={extPermEnabled} class="mt-0.5 rounded border-slate-300" />
        <div class="flex-1">
          <div class="text-sm font-medium text-slate-700">Enable permission enforcement</div>
          <p class="mt-0.5 text-[11px] text-slate-400 leading-relaxed">
            When disabled, forward-auth users have no permissions applied. When enabled,
            the mapping below is enforced — users without a required capability get 403.
          </p>
        </div>
      </label>
    </div>

    <!-- Header name + separator -->
    <div class="grid grid-cols-2 gap-3 mb-5">
      <div>
        <label for="ext-perm-header" class="block text-xs font-medium text-slate-500 mb-1">Groups Header</label>
        <input id="ext-perm-header" type="text" bind:value={extPermGroupsHeader}
          placeholder="X-Groups"
          class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
        <p class="mt-1 text-[11px] text-slate-400">
          The gateway must include this header in its
          <code class="px-0.5 bg-slate-100 rounded text-[10px]">auth_response_headers</code> allowlist.
        </p>
      </div>
      <div>
        <label for="ext-perm-separator" class="block text-xs font-medium text-slate-500 mb-1">Value Separator</label>
        <input id="ext-perm-separator" type="text" bind:value={extPermGroupsSeparator}
          placeholder=","
          class="w-24 px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
        <p class="mt-1 text-[11px] text-slate-400">Delimiter inside a single header value.</p>
      </div>
    </div>

    <!-- Superadmins -->
    <div class="mb-5">
      <label for="ext-perm-superadmin-input" class="block text-xs font-medium text-slate-500 mb-1">Superadmins</label>
      <p class="text-[11px] text-slate-400 mb-2">Usernames that bypass all permission checks.</p>
      <div class="flex gap-2">
        <input id="ext-perm-superadmin-input" type="text" bind:value={extPermSuperadminInput}
          placeholder="forward-auth username"
          onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addExtPermSuperadmin(); } }}
          class="flex-1 px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
        <button class="px-3 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors"
          onclick={addExtPermSuperadmin}>Add</button>
      </div>
      {#if extPermSuperadmins.length > 0}
        <div class="mt-2 flex flex-wrap gap-1.5">
          {#each extPermSuperadmins as name, i}
            <span class="inline-flex items-center gap-1 px-2 py-1 bg-amber-50 border border-amber-200 rounded text-xs font-mono text-amber-700">
              {name}
              <button class="flex items-center justify-center w-3.5 h-3.5 p-0 border-none cursor-pointer bg-transparent text-amber-400 hover:text-red-500 transition-colors"
                onclick={() => removeExtPermSuperadmin(i)} title="Remove">&times;</button>
            </span>
          {/each}
        </div>
      {/if}
    </div>

    <!-- Mapping editor -->
    <div class="mb-5">
      <div class="flex items-center justify-between mb-2">
        <label class="block text-xs font-medium text-slate-500">Group &rarr; Capability Mapping</label>
        <button class="text-[10px] text-blue-500 hover:text-blue-700"
          onclick={addExtPermMappingRow}>+ Add Row</button>
      </div>
      <p class="text-[11px] text-slate-400 mb-3">
        Each row maps one external group name to a set of pika capabilities. A user in multiple
        groups gets the union.
      </p>

      {#if capabilities.length === 0}
        <p class="text-xs text-amber-600 italic">Loading capability list from server...</p>
      {:else}
        <div class="space-y-3">
          {#each extPermMappingRows as row, rowIndex}
            <div class="p-3 bg-slate-50 border border-slate-200 rounded-md">
              <div class="flex items-center gap-2 mb-2">
                <input type="text" bind:value={row.group}
                  placeholder="external group name (e.g. pika-editor)"
                  class="flex-1 px-2 py-1.5 text-xs font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500" />
                <button class="p-1 text-slate-400 hover:text-red-500" onclick={() => removeExtPermMappingRow(rowIndex)} title="Remove row">
                  <Trash2 size={12} />
                </button>
              </div>
              <div class="flex flex-wrap gap-1.5">
                {#each capabilities as cap}
                  {@const selected = row.keys.includes(cap.key)}
                  <button
                    type="button"
                    class="inline-flex items-center gap-1 px-2 py-1 text-[11px] font-mono rounded border transition-colors
                      {selected
                        ? 'bg-emerald-50 border-emerald-300 text-emerald-700'
                        : 'bg-white border-slate-200 text-slate-500 hover:bg-slate-100'}"
                    onclick={() => toggleExtPermMappingKey(rowIndex, cap.key)}
                    title={cap.description}
                  >
                    {#if selected}<Check size={11} />{/if}
                    {cap.key}
                  </button>
                {/each}
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </div>

    <!-- Save button -->
    <div class="flex justify-end">
      <button
        onclick={handleSaveExternalPermissions}
        disabled={isSavingExtPerm}
        class="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors cursor-pointer"
      >
        {isSavingExtPerm ? 'Saving...' : 'Save Permissions'}
      </button>
    </div>
  </div>
  {/if}
</div>
