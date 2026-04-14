<script lang="ts">
  import { configStore } from "@/lib/store/config.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import { onMount } from "svelte";
  import { Plus, Trash2, Globe } from "lucide-svelte";
  import type { ExternalResource } from "@/lib/types/config";

  // ── External resource state ──
  let showAddExternal = $state(false);
  let newExtName = $state('');
  let newExtType = $state<'http' | 'vault' | 'kubernetes' | 'consul' | 'etcd' | 'aws' | 'gcp' | 'azure'>('http');
  let newExtHttpUrl = $state('');
  let newExtVaultAddr = $state('');
  let newExtVaultMount = $state('secret');
  let newExtVaultRoleId = $state('');
  let newExtVaultSecretId = $state('');
  let newExtVaultAppRolePath = $state('approle');
  let newExtK8sKubeconfig = $state('');
  // Consul fields
  let newExtConsulAddr = $state('');
  let newExtConsulToken = $state('');
  // etcd fields
  let newExtEtcdAddr = $state('');
  let newExtEtcdUsername = $state('');
  let newExtEtcdPassword = $state('');
  // AWS fields
  let newExtAwsRegion = $state('us-east-1');
  let newExtAwsAccessKey = $state('');
  let newExtAwsSecretKey = $state('');
  let newExtAwsService = $state<'secretsmanager' | 'ssm'>('secretsmanager');
  // GCP fields
  let newExtGcpServiceAccountJson = $state('');
  // Azure fields
  let newExtAzureVaultUrl = $state('');
  let newExtAzureTenantId = $state('');
  let newExtAzureClientId = $state('');
  let newExtAzureClientSecret = $state('');

  const settings = $derived(configStore.settings);
  const externalResources = $derived(
    settings?.external ? Object.entries(settings.external) : []
  );

  onMount(() => {
    configStore.loadSettings();
  });

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
    } else if (newExtType === 'consul') {
      if (!newExtConsulAddr.trim()) {
        addToast('Consul address is required', 'alert');
        return;
      }
      resource.consul = {
        address: newExtConsulAddr.trim(),
        token: newExtConsulToken.trim() || undefined
      };
    } else if (newExtType === 'etcd') {
      if (!newExtEtcdAddr.trim()) {
        addToast('etcd address is required', 'alert');
        return;
      }
      resource.etcd = {
        address: newExtEtcdAddr.trim(),
        username: newExtEtcdUsername.trim() || undefined,
        password: newExtEtcdPassword.trim() || undefined
      };
    } else if (newExtType === 'aws') {
      if (!newExtAwsRegion.trim() || !newExtAwsAccessKey.trim() || !newExtAwsSecretKey.trim()) {
        addToast('AWS region, access key, and secret key are required', 'alert');
        return;
      }
      resource.aws = {
        region: newExtAwsRegion.trim(),
        access_key: newExtAwsAccessKey.trim(),
        secret_key: newExtAwsSecretKey.trim(),
        service: newExtAwsService
      };
    } else if (newExtType === 'gcp') {
      if (!newExtGcpServiceAccountJson.trim()) {
        addToast('GCP service account JSON is required', 'alert');
        return;
      }
      resource.gcp = {
        service_account_json: newExtGcpServiceAccountJson.trim()
      };
    } else if (newExtType === 'azure') {
      if (!newExtAzureVaultUrl.trim() || !newExtAzureTenantId.trim() || !newExtAzureClientId.trim() || !newExtAzureClientSecret.trim()) {
        addToast('Azure vault URL, tenant ID, client ID, and client secret are required', 'alert');
        return;
      }
      resource.azure = {
        vault_url: newExtAzureVaultUrl.trim(),
        tenant_id: newExtAzureTenantId.trim(),
        client_id: newExtAzureClientId.trim(),
        client_secret: newExtAzureClientSecret.trim()
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
      newExtConsulAddr = '';
      newExtConsulToken = '';
      newExtEtcdAddr = '';
      newExtEtcdUsername = '';
      newExtEtcdPassword = '';
      newExtAwsRegion = 'us-east-1';
      newExtAwsAccessKey = '';
      newExtAwsSecretKey = '';
      newExtAwsService = 'secretsmanager';
      newExtGcpServiceAccountJson = '';
      newExtAzureVaultUrl = '';
      newExtAzureTenantId = '';
      newExtAzureClientId = '';
      newExtAzureClientSecret = '';
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
</script>

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
          <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
            <input type="radio" bind:group={newExtType} value="consul" class="text-blue-500" />
            Consul
          </label>
          <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
            <input type="radio" bind:group={newExtType} value="etcd" class="text-blue-500" />
            etcd
          </label>
          <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
            <input type="radio" bind:group={newExtType} value="aws" class="text-blue-500" />
            AWS
          </label>
          <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
            <input type="radio" bind:group={newExtType} value="gcp" class="text-blue-500" />
            GCP
          </label>
          <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
            <input type="radio" bind:group={newExtType} value="azure" class="text-blue-500" />
            Azure
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
      {:else if newExtType === 'consul'}
        <div class="mb-4">
          <label for="ext-consul-addr" class="block text-xs font-medium text-slate-500 mb-1.5">Address</label>
          <input id="ext-consul-addr" type="url" bind:value={newExtConsulAddr} placeholder="http://consul.example.com:8500"
            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
        </div>
        <div class="mb-4">
          <label for="ext-consul-token" class="block text-xs font-medium text-slate-500 mb-1.5">ACL Token (optional)</label>
          <input id="ext-consul-token" type="password" bind:value={newExtConsulToken} placeholder="Consul ACL token"
            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
        </div>
      {:else if newExtType === 'etcd'}
        <div class="mb-4">
          <label for="ext-etcd-addr" class="block text-xs font-medium text-slate-500 mb-1.5">Address</label>
          <input id="ext-etcd-addr" type="url" bind:value={newExtEtcdAddr} placeholder="http://etcd.example.com:2379"
            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
        </div>
        <div class="grid grid-cols-2 gap-3 mb-4">
          <div>
            <label for="ext-etcd-username" class="block text-xs font-medium text-slate-500 mb-1.5">Username (optional)</label>
            <input id="ext-etcd-username" type="text" bind:value={newExtEtcdUsername} placeholder="root"
              class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
          </div>
          <div>
            <label for="ext-etcd-password" class="block text-xs font-medium text-slate-500 mb-1.5">Password (optional)</label>
            <input id="ext-etcd-password" type="password" bind:value={newExtEtcdPassword} placeholder="Password"
              class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
          </div>
        </div>
      {:else if newExtType === 'aws'}
        <div class="mb-4">
          <span class="block text-xs font-medium text-slate-500 mb-1.5">AWS Service</span>
          <div class="flex gap-3">
            <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
              <input type="radio" bind:group={newExtAwsService} value="secretsmanager" class="text-blue-500" /> Secrets Manager
            </label>
            <label class="flex items-center gap-1.5 text-sm text-slate-600 cursor-pointer">
              <input type="radio" bind:group={newExtAwsService} value="ssm" class="text-blue-500" /> SSM Parameter Store
            </label>
          </div>
        </div>
        <div class="mb-4">
          <label for="ext-aws-region" class="block text-xs font-medium text-slate-500 mb-1.5">Region</label>
          <input id="ext-aws-region" type="text" bind:value={newExtAwsRegion} placeholder="us-east-1"
            class="w-full px-3 py-2 text-sm border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
        </div>
        <div class="grid grid-cols-2 gap-3 mb-4">
          <div>
            <label for="ext-aws-access-key" class="block text-xs font-medium text-slate-500 mb-1.5">Access Key ID</label>
            <input id="ext-aws-access-key" type="text" bind:value={newExtAwsAccessKey} placeholder="AKIA..."
              class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
          </div>
          <div>
            <label for="ext-aws-secret-key" class="block text-xs font-medium text-slate-500 mb-1.5">Secret Access Key</label>
            <input id="ext-aws-secret-key" type="password" bind:value={newExtAwsSecretKey} placeholder="Secret key"
              class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
          </div>
        </div>
      {:else if newExtType === 'gcp'}
        <div class="mb-4">
          <label for="ext-gcp-sa-json" class="block text-xs font-medium text-slate-500 mb-1.5">Service Account JSON Key</label>
          <textarea id="ext-gcp-sa-json" bind:value={newExtGcpServiceAccountJson} placeholder={'{"type": "service_account", "project_id": "...", ...}'}
            rows="6"
            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10 resize-y"></textarea>
          <p class="mt-1 text-[11px] text-slate-400">Paste the full JSON content of a GCP service account key with Secret Manager access</p>
        </div>
      {:else if newExtType === 'azure'}
        <div class="mb-4">
          <label for="ext-azure-vault-url" class="block text-xs font-medium text-slate-500 mb-1.5">Vault URL</label>
          <input id="ext-azure-vault-url" type="url" bind:value={newExtAzureVaultUrl} placeholder="https://my-vault.vault.azure.net"
            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
        </div>
        <div class="mb-4">
          <label for="ext-azure-tenant-id" class="block text-xs font-medium text-slate-500 mb-1.5">Tenant ID</label>
          <input id="ext-azure-tenant-id" type="text" bind:value={newExtAzureTenantId} placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
        </div>
        <div class="grid grid-cols-2 gap-3 mb-4">
          <div>
            <label for="ext-azure-client-id" class="block text-xs font-medium text-slate-500 mb-1.5">Client ID</label>
            <input id="ext-azure-client-id" type="text" bind:value={newExtAzureClientId} placeholder="Application (client) ID"
              class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
          </div>
          <div>
            <label for="ext-azure-client-secret" class="block text-xs font-medium text-slate-500 mb-1.5">Client Secret</label>
            <input id="ext-azure-client-secret" type="password" bind:value={newExtAzureClientSecret} placeholder="Client secret"
              class="w-full px-3 py-2 text-sm font-mono border border-slate-200 rounded-md focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/10" />
          </div>
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
              <span class="px-1.5 py-0.5 text-[10px] font-medium rounded
                {resource.vault ? 'bg-amber-100 text-amber-700' : resource.aws ? 'bg-orange-100 text-orange-700' : resource.gcp ? 'bg-green-100 text-green-700' : resource.azure ? 'bg-sky-100 text-sky-700' : resource.consul ? 'bg-pink-100 text-pink-700' : resource.etcd ? 'bg-teal-100 text-teal-700' : 'bg-blue-100 text-blue-700'}">
                {resource.http ? 'HTTP' : resource.vault ? 'Vault' : resource.kubernetes ? 'Kubernetes' : resource.consul ? 'Consul' : resource.etcd ? 'etcd' : resource.aws ? (resource.aws.service === 'ssm' ? 'AWS SSM' : 'AWS Secrets Manager') : resource.gcp ? 'GCP Secret Manager' : resource.azure ? 'Azure Key Vault' : 'Unknown'}
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
              {:else if resource.consul}
                <span class="text-xs font-mono text-slate-400">{resource.consul.address}</span>
              {:else if resource.etcd}
                <span class="text-xs font-mono text-slate-400">{resource.etcd.address}</span>
              {:else if resource.aws}
                <span class="text-xs font-mono text-slate-400">{resource.aws.region}</span>
              {:else if resource.gcp}
                <span class="text-xs text-slate-400">GCP Secret Manager (service account)</span>
              {:else if resource.azure}
                <span class="text-xs font-mono text-slate-400">{resource.azure.vault_url}</span>
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
