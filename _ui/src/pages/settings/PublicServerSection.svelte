<script lang="ts">
  import { configStore } from "@/lib/store/config.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import { onMount } from "svelte";
  import type { PublicPortSettings, CompatSettings } from "@/lib/types/config";

  // ── Public server state ──
  let publicPortEnabled = $state(false);
  let publicPortPort = $state('9090');
  let compatConsulEnabled = $state(false);
  let compatConsulBasePath = $state('/consul');
  let isSavingPublicServer = $state(false);

  function loadPublicServerSettings() {
    const s = configStore.settings;
    publicPortEnabled = s?.public_port?.enabled ?? false;
    publicPortPort = s?.public_port?.port || '9090';
    compatConsulEnabled = !!s?.compat?.consul_kv;
    compatConsulBasePath = s?.compat?.consul_kv?.base_path || '/consul';
  }

  async function handleSavePublicServer() {
    isSavingPublicServer = true;
    try {
      if (!publicPortPort.trim() && publicPortEnabled) {
        addToast('Port is required when public server is enabled', 'alert');
        return;
      }

      const patch: {
        public_port?: PublicPortSettings;
        compat?: CompatSettings;
      } = {};

      const publicPort: PublicPortSettings = {
        enabled: publicPortEnabled,
        port: publicPortPort.trim() || undefined,
      };
      patch.public_port = publicPort;

      const compatSettings: CompatSettings = {};
      if (compatConsulEnabled) {
        compatSettings.consul_kv = {
          base_path: compatConsulBasePath.trim() || '/consul',
        };
      }
      patch.compat = compatSettings;

      await configStore.savePublicServerSettings(patch);
    } catch {
      // toast already shown by store
    } finally {
      isSavingPublicServer = false;
    }
  }

  onMount(() => {
    loadPublicServerSettings();
  });
</script>

<div>
  <div class="mb-6">
    <h2 class="text-lg font-semibold text-slate-800">Public Server</h2>
    <p class="text-sm text-slate-500 mt-0.5">Configure the public (unauthenticated) HTTP server for /data/*, /raw/*, and compatibility endpoints.</p>
  </div>

  <!-- Public Port -->
  <div class="mb-6 p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
    <div class="flex items-center justify-between mb-4">
      <div>
        <h3 class="text-sm font-semibold text-slate-700">Public Port</h3>
        <p class="text-xs text-slate-500 mt-0.5">A second HTTP server serving /data/* and /raw/* without token authentication.</p>
      </div>
      <label class="relative inline-flex items-center cursor-pointer">
        <input type="checkbox" bind:checked={publicPortEnabled} class="sr-only peer" />
        <div class="w-9 h-5 bg-slate-200 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-slate-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-blue-600"></div>
      </label>
    </div>
    {#if publicPortEnabled}
      <div class="grid grid-cols-1 gap-3">
        <div>
          <label class="block text-xs font-medium text-slate-600 mb-1">Port</label>
          <input type="text" bind:value={publicPortPort} placeholder="9090"
            class="w-full px-3 py-1.5 text-sm border border-slate-200 rounded-md focus:outline-none focus:ring-1 focus:ring-blue-500 focus:border-blue-500" />
        </div>
      </div>
    {/if}
  </div>

  <!-- Compat Endpoints -->
  <div class="mb-6 p-5 bg-white border border-slate-200 rounded-lg shadow-sm">
    <div class="flex items-center justify-between mb-4">
      <div>
        <h3 class="text-sm font-semibold text-slate-700">Consul KV Compatibility</h3>
        <p class="text-xs text-slate-500 mt-0.5">Emulates the Consul KV API (GET /v1/kv/*) on the public server. Requires public port to be enabled.</p>
      </div>
      <label class="relative inline-flex items-center cursor-pointer">
        <input type="checkbox" bind:checked={compatConsulEnabled} class="sr-only peer" />
        <div class="w-9 h-5 bg-slate-200 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-slate-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-blue-600"></div>
      </label>
    </div>
    {#if compatConsulEnabled}
      <div class="grid grid-cols-1 gap-3">
        <div>
          <label class="block text-xs font-medium text-slate-600 mb-1">Base Path</label>
          <input type="text" bind:value={compatConsulBasePath} placeholder="/consul"
            class="w-full px-3 py-1.5 text-sm border border-slate-200 rounded-md focus:outline-none focus:ring-1 focus:ring-blue-500 focus:border-blue-500" />
          <p class="text-xs text-slate-400 mt-1">Consul KV routes will be registered at {compatConsulBasePath}/v1/kv/*</p>
        </div>
      </div>
    {/if}
    {#if compatConsulEnabled && !publicPortEnabled}
      <div class="mt-3 p-2 bg-amber-50 border border-amber-200 rounded text-xs text-amber-700">
        Compat endpoints require the public port to be enabled. They will not be available until you enable the public port above.
      </div>
    {/if}
  </div>

  <!-- Save Button -->
  <div class="flex justify-end">
    <button
      onclick={handleSavePublicServer}
      disabled={isSavingPublicServer}
      class="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors cursor-pointer"
    >
      {isSavingPublicServer ? 'Saving...' : 'Save Public Server Settings'}
    </button>
  </div>
</div>
