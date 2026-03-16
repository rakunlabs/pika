<script lang="ts">
  import { configStore } from "@/lib/store/config.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import { onMount } from "svelte";
  import { Plus, Trash2, Copy, Eye, EyeOff, Shield, Globe, Key, RotateCw } from "lucide-svelte";
  import type { TokenScope, CreateTokenRequest, ExternalResource } from "@/lib/types/config";
  import axios from 'axios';

  // ── Tab state ──
  let activeSection = $state<'tokens' | 'external' | 'rotation'>('tokens');

  // ── Rotation state ──
  let rotationAdminSecret = $state('');
  let rotationNewKey = $state('');
  let isRotating = $state(false);
  let showAdminSecret = $state(false);
  let showNewKey = $state(false);

  // ── Token state ──
  let showCreateToken = $state(false);
  let newTokenName = $state('');
  let newTokenScopes = $state<TokenScope[]>([{ path: '**', operations: ['read'] }]);
  let newTokenExpiry = $state('');
  let createdTokenKey = $state<string | null>(null);
  let showKey = $state(false);

  // ── External resource state ──
  let showAddExternal = $state(false);
  let newExtName = $state('');
  let newExtType = $state<'http' | 'vault' | 'kubernetes'>('http');
  let newExtHttpUrl = $state('');
  let newExtVaultAddr = $state('');
  let newExtVaultMount = $state('secret');
  let newExtVaultRoleId = $state('');
  let newExtVaultSecretId = $state('');
  let newExtVaultAppRolePath = $state('approle');
  let newExtK8sKubeconfig = $state('');

  const tokens = $derived(configStore.tokens);
  const settings = $derived(configStore.settings);
  const externalResources = $derived(
    settings?.external ? Object.entries(settings.external) : []
  );

  onMount(() => {
    configStore.loadSettings();
    configStore.loadTokens();
  });

  // ── Token handlers ──
  function addScope() {
    newTokenScopes = [...newTokenScopes, { path: '', operations: ['read'] }];
  }

  function removeScope(index: number) {
    newTokenScopes = newTokenScopes.filter((_, i) => i !== index);
  }

  function toggleOperation(index: number, op: string) {
    const scope = newTokenScopes[index];
    if (scope.operations.includes(op)) {
      scope.operations = scope.operations.filter(o => o !== op);
    } else {
      scope.operations = [...scope.operations, op];
    }
    newTokenScopes = [...newTokenScopes];
  }

  async function handleCreateToken() {
    if (!newTokenName.trim()) {
      addToast('Token name is required', 'alert');
      return;
    }
    if (newTokenScopes.some(s => !s.path.trim())) {
      addToast('All scope paths are required', 'alert');
      return;
    }

    try {
      const req: CreateTokenRequest = {
        name: newTokenName.trim(),
        scopes: newTokenScopes,
      };
      if (newTokenExpiry) {
        req.expires_at = new Date(newTokenExpiry).toISOString();
      }

      const result = await configStore.createToken(req);
      createdTokenKey = result.raw_key;
      showCreateToken = false;
      newTokenName = '';
      newTokenScopes = [{ path: '**', operations: ['read'] }];
      newTokenExpiry = '';
      addToast('Token created successfully', 'success');
    } catch (error) {
      console.error('Failed to create token:', error);
      addToast('Failed to create token', 'alert');
    }
  }

  async function handleDeleteToken(id: string) {
    if (!confirm('Are you sure you want to delete this token?')) return;
    try {
      await configStore.deleteToken(id);
    } catch (error) {
      addToast('Failed to delete token', 'alert');
    }
  }

  async function handleToggleToken(id: string, active: boolean) {
    try {
      await configStore.patchToken(id, { active: !active });
    } catch (error) {
      addToast('Failed to update token', 'alert');
    }
  }

  async function copyTokenKey() {
    if (createdTokenKey) {
      await navigator.clipboard.writeText(createdTokenKey);
      addToast('Token copied to clipboard', 'success');
    }
  }

  function dismissTokenKey() {
    createdTokenKey = null;
  }

  // ── External resource handlers ──
  async function handleAddExternal() {
    if (!newExtName.trim()) {
      addToast('Resource name is required', 'alert');
      return;
    }

    const resource: ExternalResource = {} as ExternalResource;
    if (newExtType === 'http') {
      if (!newExtHttpUrl.trim()) {
        addToast('HTTP URL is required', 'alert');
        return;
      }
      resource.http = { base_url: newExtHttpUrl.trim() };
    } else if (newExtType === 'vault') {
      if (!newExtVaultAddr.trim() || !newExtVaultMount.trim()) {
        addToast('Vault address and mount are required', 'alert');
        return;
      }
      if (!newExtVaultRoleId.trim() || !newExtVaultSecretId.trim()) {
        addToast('AppRole Role ID and Secret ID are required', 'alert');
        return;
      }
      resource.vault = {
        address: newExtVaultAddr.trim(),
        mount: newExtVaultMount.trim(),
        app_role: {
          role_id: newExtVaultRoleId.trim(),
          secret_id: newExtVaultSecretId.trim(),
          app_role_base_path: newExtVaultAppRolePath.trim() || 'approle'
        }
      };
    } else if (newExtType === 'kubernetes') {
      resource.kubernetes = {
        kubeconfig: newExtK8sKubeconfig.trim() || undefined
      };
    }

    try {
      const currentExternal = settings?.external || {};
      await configStore.saveSettings({
        external: { ...currentExternal, [newExtName.trim()]: resource }
      });
      showAddExternal = false;
      newExtName = '';
      newExtHttpUrl = '';
      newExtVaultAddr = '';
      newExtVaultMount = 'secret';
      newExtVaultRoleId = '';
      newExtVaultSecretId = '';
      newExtVaultAppRolePath = 'approle';
      newExtK8sKubeconfig = '';
    } catch (error) {
      addToast('Failed to add external resource', 'alert');
    }
  }

  async function handleRemoveExternal(name: string) {
    if (!confirm(`Remove external resource "${name}"?`)) return;
    try {
      const currentExternal = { ...(settings?.external || {}) };
      delete currentExternal[name];
      await configStore.saveSettings({ external: currentExternal });
    } catch (error) {
      addToast('Failed to remove external resource', 'alert');
    }
  }

  async function handleRotateKey() {
    if (!rotationAdminSecret.trim()) {
      addToast('Admin secret is required', 'alert');
      return;
    }
    if (!rotationNewKey.trim()) {
      addToast('New encryption key is required', 'alert');
      return;
    }

    isRotating = true;
    try {
      await axios.post('/api/v1/rotate', {
        admin_secret: rotationAdminSecret.trim(),
        new_key: rotationNewKey.trim()
      });
      addToast('Key rotation completed successfully', 'success');
      rotationAdminSecret = '';
      rotationNewKey = '';
    } catch (error: any) {
      const msg = error.response?.data?.message || 'Key rotation failed';
      addToast(msg, 'alert');
    } finally {
      isRotating = false;
    }
  }

  function formatDate(dateStr: string): string {
    return new Date(dateStr).toLocaleDateString(undefined, {
      year: 'numeric', month: 'short', day: 'numeric',
      hour: '2-digit', minute: '2-digit'
    });
  }
</script>

<div class="h-full overflow-y-auto">
<div class="max-w-4xl mx-auto p-6">
  <!-- Section Tabs -->
  <div class="flex gap-1 mb-6 border-b border-slate-200">
    <button
      class="flex items-center gap-2 px-4 py-2.5 text-sm font-medium border-b-2 cursor-pointer transition-colors
        {activeSection === 'tokens' ? 'border-blue-500 text-blue-600' : 'border-transparent text-slate-500 hover:text-slate-700'}"
      onclick={() => activeSection = 'tokens'}
    >
      <Key size={16} />
      Access Tokens
    </button>
    <button
      class="flex items-center gap-2 px-4 py-2.5 text-sm font-medium border-b-2 cursor-pointer transition-colors
        {activeSection === 'external' ? 'border-blue-500 text-blue-600' : 'border-transparent text-slate-500 hover:text-slate-700'}"
      onclick={() => activeSection = 'external'}
    >
      <Globe size={16} />
      External Resources
    </button>
    <button
      class="flex items-center gap-2 px-4 py-2.5 text-sm font-medium border-b-2 cursor-pointer transition-colors
        {activeSection === 'rotation' ? 'border-blue-500 text-blue-600' : 'border-transparent text-slate-500 hover:text-slate-700'}"
      onclick={() => activeSection = 'rotation'}
    >
      <RotateCw size={16} />
      Key Rotation
    </button>
  </div>

  <!-- ══════════════════════════════════════════ -->
  <!-- Token Created Banner -->
  <!-- ══════════════════════════════════════════ -->
  {#if createdTokenKey}
    <div class="mb-6 p-4 bg-green-50 border border-green-200 rounded-lg">
      <p class="text-sm font-semibold text-green-800 mb-2">Token Created Successfully</p>
      <p class="text-xs text-green-700 mb-3">Copy this token now. It will not be shown again.</p>
      <div class="flex items-center gap-2">
        <code class="flex-1 px-3 py-2 bg-white border border-green-200 rounded text-xs font-mono text-green-900 overflow-hidden text-ellipsis">
          {showKey ? createdTokenKey : '••••••••••••••••••••••••••••••••'}
        </code>
        <button
          class="p-2 bg-white border border-green-200 rounded hover:bg-green-100 transition-colors"
          onclick={() => showKey = !showKey}
          title={showKey ? 'Hide' : 'Show'}
        >
          {#if showKey}<EyeOff size={14} />{:else}<Eye size={14} />{/if}
        </button>
        <button
          class="p-2 bg-white border border-green-200 rounded hover:bg-green-100 transition-colors"
          onclick={copyTokenKey}
          title="Copy"
        >
          <Copy size={14} />
        </button>
      </div>
      <button
        class="mt-3 px-3 py-1.5 text-xs text-green-700 bg-transparent border border-green-300 rounded hover:bg-green-100 transition-colors"
        onclick={dismissTokenKey}
      >
        Dismiss
      </button>
    </div>
  {/if}

  <!-- ══════════════════════════════════════════ -->
  <!-- Access Tokens Section -->
  <!-- ══════════════════════════════════════════ -->
  {#if activeSection === 'tokens'}
    <div>
      <div class="flex items-center justify-between mb-4">
        <div>
          <h2 class="text-lg font-semibold text-slate-800">Access Tokens</h2>
          <p class="text-sm text-slate-500 mt-0.5">Tokens authenticate consumers accessing configs via the data API</p>
        </div>
        <button
          class="flex items-center gap-1.5 px-3 py-2 bg-blue-500 text-white text-sm font-medium rounded-md hover:bg-blue-600 transition-colors"
          onclick={() => showCreateToken = true}
        >
          <Plus size={14} />
          New Token
        </button>
      </div>

      <!-- Create Token Form -->
      {#if showCreateToken}
        <div class="mb-6 p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
          <h3 class="text-sm font-semibold text-slate-700 mb-4">Create New Token</h3>

          <div class="mb-4">
            <label for="token-name" class="block text-xs font-medium text-slate-500 mb-1.5">Name</label>
            <input
              id="token-name"
              type="text"
              bind:value={newTokenName}
              placeholder="e.g., production-reader"
              class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
            />
          </div>

          <div class="mb-4">
            <label for="token-expiry" class="block text-xs font-medium text-slate-500 mb-1.5">Expires At (optional)</label>
            <input
              id="token-expiry"
              type="datetime-local"
              bind:value={newTokenExpiry}
              class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
            />
          </div>

          <div class="mb-4">
            <div class="flex items-center justify-between mb-2">
              <span class="block text-xs font-medium text-slate-500">Scopes</span>
              <button
                class="flex items-center gap-1 px-2 py-1 text-xs text-blue-600 bg-blue-50 rounded hover:bg-blue-100 transition-colors"
                onclick={addScope}
              >
                <Plus size={12} /> Add Scope
              </button>
            </div>

            {#each newTokenScopes as scope, i (i)}
              <div class="flex items-start gap-2 mb-2 p-3 bg-slate-50 rounded-md border border-slate-100">
                <div class="flex-1">
                  <input
                    type="text"
                    bind:value={scope.path}
                    placeholder="Path pattern (e.g., app/**, production/*)"
                    class="w-full px-2.5 py-1.5 text-xs font-mono border border-slate-200 rounded focus:outline-none focus:border-blue-500"
                  />
                  <div class="flex gap-2 mt-2">
                    {#each ['read', 'write', 'delete'] as op}
                      <label class="flex items-center gap-1 text-xs text-slate-600 cursor-pointer">
                        <input
                          type="checkbox"
                          checked={scope.operations.includes(op)}
                          onchange={() => toggleOperation(i, op)}
                          class="rounded border-slate-300"
                        />
                        {op}
                      </label>
                    {/each}
                  </div>
                </div>
                {#if newTokenScopes.length > 1}
                  <button
                    class="p-1 text-slate-400 hover:text-red-500 transition-colors"
                    onclick={() => removeScope(i)}
                  >
                    <Trash2 size={14} />
                  </button>
                {/if}
              </div>
            {/each}
          </div>

          <div class="flex justify-end gap-2">
            <button
              class="px-3 py-2 text-sm text-slate-600 bg-white border border-slate-200 rounded-md hover:bg-slate-50 transition-colors"
              onclick={() => showCreateToken = false}
            >
              Cancel
            </button>
            <button
              class="px-3 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors"
              onclick={handleCreateToken}
            >
              Create Token
            </button>
          </div>
        </div>
      {/if}

      <!-- Token List -->
      {#if tokens.length === 0}
        <div class="text-center py-12 bg-white border border-slate-200 rounded-lg">
          <Shield size={32} class="mx-auto text-slate-300 mb-3" />
          <p class="text-sm text-slate-500">No access tokens yet</p>
          <p class="text-xs text-slate-400 mt-1">Create a token to allow consumers to access configurations</p>
        </div>
      {:else}
        <div class="space-y-2">
          {#each tokens as token (token.id)}
            <div class="flex items-center gap-4 p-4 bg-white border border-slate-200 rounded-lg hover:border-slate-300 transition-colors">
              <div class="flex-1 min-w-0">
                <div class="flex items-center gap-2">
                  <span class="text-sm font-medium text-slate-800">{token.name}</span>
                  <span class="px-1.5 py-0.5 text-[10px] font-medium rounded
                    {token.active ? 'bg-green-100 text-green-700' : 'bg-slate-100 text-slate-500'}">
                    {token.active ? 'Active' : 'Disabled'}
                  </span>
                </div>
                <div class="flex gap-3 mt-1 flex-wrap">
                  <span class="text-xs text-slate-400">Created: {formatDate(token.created_at)}</span>
                  {#if token.created_by}
                    <span class="text-xs text-slate-400">by: <span class="text-slate-600">{token.created_by}</span></span>
                  {/if}
                  {#if token.expires_at}
                    <span class="text-xs text-slate-400">Expires: {formatDate(token.expires_at)}</span>
                  {/if}
                </div>
                <div class="flex flex-wrap gap-1.5 mt-2">
                  {#each token.scopes as scope}
                    <span class="px-2 py-0.5 text-[10px] font-mono bg-slate-100 text-slate-600 rounded">
                      {scope.operations.join(',')}:{scope.path}
                    </span>
                  {/each}
                </div>
              </div>
              <div class="flex items-center gap-1 shrink-0">
                <button
                  class="px-2.5 py-1.5 text-xs rounded transition-colors
                    {token.active ? 'text-amber-600 bg-amber-50 hover:bg-amber-100' : 'text-green-600 bg-green-50 hover:bg-green-100'}"
                  onclick={() => handleToggleToken(token.id, token.active)}
                >
                  {token.active ? 'Disable' : 'Enable'}
                </button>
                <button
                  class="p-1.5 text-slate-400 hover:text-red-500 hover:bg-red-50 rounded transition-colors"
                  onclick={() => handleDeleteToken(token.id)}
                  title="Delete token"
                >
                  <Trash2 size={14} />
                </button>
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </div>
  {/if}

  <!-- ══════════════════════════════════════════ -->
  <!-- External Resources Section -->
  <!-- ══════════════════════════════════════════ -->
  {#if activeSection === 'external'}
    <div>
      <div class="flex items-center justify-between mb-4">
        <div>
          <h2 class="text-lg font-semibold text-slate-800">External Resources</h2>
          <p class="text-sm text-slate-500 mt-0.5">Configure external sources for configuration inheritance</p>
        </div>
        <button
          class="flex items-center gap-1.5 px-3 py-2 bg-blue-500 text-white text-sm font-medium rounded-md hover:bg-blue-600 transition-colors"
          onclick={() => showAddExternal = true}
        >
          <Plus size={14} />
          Add Resource
        </button>
      </div>

      <!-- Add External Form -->
      {#if showAddExternal}
        <div class="mb-6 p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
          <h3 class="text-sm font-semibold text-slate-700 mb-4">Add External Resource</h3>

          <div class="mb-4">
            <label for="ext-name" class="block text-xs font-medium text-slate-500 mb-1.5">Resource Name</label>
            <input
              id="ext-name"
              type="text"
              bind:value={newExtName}
              placeholder="e.g., shared-config"
              class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
            />
          </div>

          <div class="mb-4">
            <span class="block text-xs font-medium text-slate-500 mb-1.5">Type</span>
            <div class="flex gap-3">
              <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
                <input type="radio" bind:group={newExtType} value="http" class="text-blue-500" />
                HTTP
              </label>
              <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
                <input type="radio" bind:group={newExtType} value="vault" class="text-blue-500" />
                Vault
              </label>
              <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
                <input type="radio" bind:group={newExtType} value="kubernetes" class="text-blue-500" />
                Kubernetes
              </label>
            </div>
          </div>

          {#if newExtType === 'http'}
            <div class="mb-4">
              <label for="ext-url" class="block text-xs font-medium text-slate-500 mb-1.5">Base URL</label>
              <input
                id="ext-url"
                type="url"
                bind:value={newExtHttpUrl}
                placeholder="https://config-server.example.com/api/config"
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
              />
            </div>
          {:else if newExtType === 'vault'}
            <div class="mb-4">
              <label for="ext-vault-addr" class="block text-xs font-medium text-slate-500 mb-1.5">Vault Address</label>
              <input
                id="ext-vault-addr"
                type="url"
                bind:value={newExtVaultAddr}
                placeholder="https://vault.example.com"
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
              />
            </div>
            <div class="mb-4">
              <label for="ext-vault-mount" class="block text-xs font-medium text-slate-500 mb-1.5">Mount</label>
              <input
                id="ext-vault-mount"
                type="text"
                bind:value={newExtVaultMount}
                placeholder="secret"
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
              />
              <p class="mt-1 text-[11px] text-slate-400">KV secrets engine mount path. Secret paths are specified per-inheritance entry.</p>
            </div>

            <div class="mb-3 pt-2 border-t border-slate-100">
              <p class="text-xs font-medium text-slate-500 mb-2">AppRole Authentication</p>
            </div>

            <div class="mb-4">
              <label for="ext-vault-role-id" class="block text-xs font-medium text-slate-500 mb-1.5">Role ID</label>
              <input
                id="ext-vault-role-id"
                type="text"
                bind:value={newExtVaultRoleId}
                placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
                class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
              />
            </div>
            <div class="mb-4">
              <label for="ext-vault-secret-id" class="block text-xs font-medium text-slate-500 mb-1.5">Secret ID</label>
              <input
                id="ext-vault-secret-id"
                type="password"
                bind:value={newExtVaultSecretId}
                placeholder="Secret ID"
                class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
              />
            </div>
            <div class="mb-4">
              <label for="ext-vault-approle-path" class="block text-xs font-medium text-slate-500 mb-1.5">AppRole Mount Path</label>
              <input
                id="ext-vault-approle-path"
                type="text"
                bind:value={newExtVaultAppRolePath}
                placeholder="approle"
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
              />
              <p class="mt-1 text-[11px] text-slate-400">Usually "approle" unless using a custom mount</p>
            </div>
          {:else if newExtType === 'kubernetes'}
            <div class="mb-4">
              <label for="ext-k8s-kubeconfig" class="block text-xs font-medium text-slate-500 mb-1.5">Kubeconfig Path (optional)</label>
              <input
                id="ext-k8s-kubeconfig"
                type="text"
                bind:value={newExtK8sKubeconfig}
                placeholder="/path/to/kubeconfig"
                class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
              />
              <p class="mt-1 text-[11px] text-slate-400">Leave empty to use in-cluster config (service account token). Path format: <code class="px-1 py-0.5 bg-slate-100 rounded text-[10px]">namespace/secret/name</code> or <code class="px-1 py-0.5 bg-slate-100 rounded text-[10px]">namespace/configmap/name</code></p>
            </div>
          {/if}

          <div class="flex justify-end gap-2">
            <button
              class="px-3 py-2 text-sm text-slate-600 bg-white border border-slate-200 rounded-md hover:bg-slate-50 transition-colors"
              onclick={() => showAddExternal = false}
            >
              Cancel
            </button>
            <button
              class="px-3 py-2 text-sm text-white bg-blue-500 rounded-md hover:bg-blue-600 transition-colors"
              onclick={handleAddExternal}
            >
              Add Resource
            </button>
          </div>
        </div>
      {/if}

      <!-- Resource List -->
      {#if externalResources.length === 0}
        <div class="text-center py-12 bg-white border border-slate-200 rounded-lg">
          <Globe size={32} class="mx-auto text-slate-300 mb-3" />
          <p class="text-sm text-slate-500">No external resources configured</p>
          <p class="text-xs text-slate-400 mt-1">Add external sources for configuration inheritance</p>
        </div>
      {:else}
        <div class="space-y-2">
          {#each externalResources as [name, resource] (name)}
            <div class="flex items-center gap-4 p-4 bg-white border border-slate-200 rounded-lg hover:border-slate-300 transition-colors">
              <div class="flex-1 min-w-0">
                <div class="flex items-center gap-2">
                  <span class="text-sm font-medium text-slate-800">{name}</span>
                  <span class="px-1.5 py-0.5 text-[10px] font-medium rounded bg-blue-100 text-blue-700">
                    {resource.http ? 'HTTP' : resource.vault ? 'Vault' : 'Kubernetes'}
                  </span>
                </div>
                <div class="mt-1 space-y-0.5">
                  {#if resource.http}
                    <span class="text-xs font-mono text-slate-400">{resource.http.base_url}</span>
                  {:else if resource.vault}
                    <div class="text-xs font-mono text-slate-400">{resource.vault.address}</div>
                    <div class="text-xs text-slate-400">
                      Mount: <span class="font-mono">{resource.vault.mount}</span>
                    </div>
                    {#if resource.vault.app_role}
                      <div class="flex items-center gap-1.5 text-[10px] text-slate-400">
                        <span class="px-1 py-0.5 bg-slate-100 rounded text-slate-500">AppRole</span>
                        <span class="font-mono">{resource.vault.app_role.app_role_base_path || 'approle'}</span>
                        <span class="text-slate-300">|</span>
                        <span>Role: {resource.vault.app_role.role_id.slice(0, 8)}...</span>
                      </div>
                    {/if}
                  {:else if resource.kubernetes}
                    <div class="text-xs text-slate-400">
                      {resource.kubernetes.kubeconfig
                        ? `Kubeconfig: ${resource.kubernetes.kubeconfig}`
                        : 'In-cluster (service account)'}
                    </div>
                    <div class="text-[10px] text-slate-400">
                      Path format: <code class="px-1 py-0.5 bg-slate-100 rounded">namespace/secret/name</code> or <code class="px-1 py-0.5 bg-slate-100 rounded">namespace/configmap/name</code>
                    </div>
                  {/if}
                </div>
              </div>
              <button
                class="p-1.5 text-slate-400 hover:text-red-500 hover:bg-red-50 rounded transition-colors shrink-0"
                onclick={() => handleRemoveExternal(name)}
                title="Remove resource"
              >
                <Trash2 size={14} />
              </button>
            </div>
          {/each}
        </div>
      {/if}
    </div>
  {/if}

  <!-- ══════════════════════════════════════════ -->
  <!-- Key Rotation Section -->
  <!-- ══════════════════════════════════════════ -->
  {#if activeSection === 'rotation'}
    <div>
      <div class="mb-4">
        <h2 class="text-lg font-semibold text-slate-800">Key Rotation</h2>
        <p class="text-sm text-slate-500 mt-0.5">Rotate the encryption key used to protect stored configurations</p>
      </div>

      <div class="p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
        <div class="mb-5 p-3 bg-amber-50 border border-amber-200 rounded-md">
          <p class="text-xs text-amber-800 leading-relaxed m-0">
            Key rotation will re-encrypt all stored data with the new key. This operation may take time depending on the amount of data.
            After rotation, update <code class="px-1 py-0.5 bg-amber-100 rounded text-[11px]">encryption_key</code> in your config file to the new key.
          </p>
        </div>

        <div class="mb-4">
          <label for="rotation-admin-secret" class="block text-xs font-medium text-slate-500 mb-1.5">Admin Secret</label>
          <div class="relative">
            <input
              id="rotation-admin-secret"
              type={showAdminSecret ? 'text' : 'password'}
              bind:value={rotationAdminSecret}
              placeholder="Enter admin secret from config"
              class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
            />
            <button
              type="button"
              class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 bg-transparent border-none cursor-pointer hover:text-slate-600 transition-colors"
              onclick={() => showAdminSecret = !showAdminSecret}
              title={showAdminSecret ? 'Hide' : 'Show'}
            >
              {#if showAdminSecret}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
            </button>
          </div>
          <p class="mt-1 text-[11px] text-slate-400">The <code class="px-1 py-0.5 bg-slate-100 rounded">admin_secret</code> value from your server config</p>
        </div>

        <div class="mb-4">
          <label for="rotation-new-key" class="block text-xs font-medium text-slate-500 mb-1.5">New Encryption Key</label>
          <div class="relative">
            <input
              id="rotation-new-key"
              type={showNewKey ? 'text' : 'password'}
              bind:value={rotationNewKey}
              placeholder="Enter new encryption key"
              class="w-full px-3 py-2 pr-9 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10"
            />
            <button
              type="button"
              class="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-slate-400 bg-transparent border-none cursor-pointer hover:text-slate-600 transition-colors"
              onclick={() => showNewKey = !showNewKey}
              title={showNewKey ? 'Hide' : 'Show'}
            >
              {#if showNewKey}<EyeOff size={15} />{:else}<Eye size={15} />{/if}
            </button>
          </div>
          <p class="mt-1 text-[11px] text-slate-400">Any string — will be hashed (SHA-256) to derive the encryption key. After rotation, update <code class="px-1 py-0.5 bg-slate-100 rounded text-[11px]">encryption_key</code> in your config.</p>
        </div>

        <button
          class="flex items-center justify-center gap-2 w-full px-4 py-2.5 text-sm font-medium text-white rounded-md transition-colors disabled:opacity-50 disabled:cursor-not-allowed
            {isRotating ? 'bg-amber-500' : 'bg-red-600 hover:bg-red-700'}"
          onclick={handleRotateKey}
          disabled={isRotating}
        >
          <RotateCw size={14} class={isRotating ? 'animate-spin' : ''} />
          {isRotating ? 'Rotating...' : 'Rotate Encryption Key'}
        </button>
      </div>
    </div>
  {/if}
</div>
</div>
