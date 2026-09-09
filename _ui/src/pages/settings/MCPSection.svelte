<script lang="ts">
  import { onMount } from "svelte";
  import { configStore } from "@/lib/store/config.svelte";
  import { basePath } from "@/lib/basepath";
  import type { MCPSettings, TokenScope } from "@/lib/types/config";

  let draft = $state<MCPSettings>({ endpoint: "/api/v1/mcp", auth_disabled: false, scopes: [] });
  let saved = $state("");
  let loading = $state(true);
  let busy = $state(false);
  let error = $state("");
  const operations = ["read", "write", "delete"];
  const inputClass = "w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500";
  const endpointURL = $derived(`${window.location.origin}${basePath}${draft.endpoint || "/api/v1/mcp"}`);
  const dirty = $derived(JSON.stringify(draft) !== saved);
  const invalidScopes = $derived(draft.scopes.some((s) => !s.path.trim() || s.operations.length === 0));
  const visibleTools = $derived.by(() => {
    const ops = new Set(draft.scopes.filter((s) => !s.resource).flatMap((s) => s.operations));
    const externalOps = new Set(draft.scopes.filter((s) => s.resource).flatMap((s) => s.operations));
    return [
      ...(ops.has("read") ? ["search_configs", "list_folder", "get_config", "get_resolved_config", "list_versions", "list_variants"] : []),
      ...(ops.has("write") ? ["set_config"] : []),
      ...(ops.has("delete") ? ["delete_config", "delete_folder"] : []),
      ...(externalOps.has("read") ? ["list_external_resources", "list_external_paths", "search_external", "read_external"] : []),
      ...(externalOps.has("write") ? ["write_external"] : []),
      ...(externalOps.has("delete") ? ["delete_external"] : []),
    ];
  });

  async function load() {
    loading = true;
    error = "";
    try {
      await configStore.loadSettings();
      const value = configStore.settings?.mcp;
      draft = {
        endpoint: value?.endpoint || "/api/v1/mcp",
        auth_disabled: value?.auth_disabled ?? false,
        scopes: value?.scopes?.map((s) => ({ ...s, operations: [...s.operations] })) ?? [],
      };
      saved = JSON.stringify(draft);
    } catch {
      error = "Could not load MCP settings. Retry to edit them.";
    } finally {
      loading = false;
    }
  }

  function toggleOperation(scope: TokenScope, op: string, checked: boolean) {
    scope.operations = checked ? [...scope.operations, op] : scope.operations.filter((v) => v !== op);
  }

  async function save() {
    busy = true;
    error = "";
    try {
      const value = $state.snapshot(draft);
      value.endpoint = value.endpoint.trim() || "/api/v1/mcp";
      value.scopes = value.scopes.map((s) => ({ ...s, resource: s.resource?.trim() || undefined, path: s.path.trim() }));
      await configStore.saveMCPSettings(value);
      draft = value;
      saved = JSON.stringify(value);
    } catch (err: any) {
      error = err.response?.data?.message || "Could not save MCP settings. Try again.";
    } finally {
      busy = false;
    }
  }

  onMount(load);
</script>

<section class="space-y-4">
  <div>
    <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">MCP</h2>
    <p class="mt-0.5 text-sm text-slate-500 dark:text-slate-400">
      Connect AI clients to Pika over Streamable HTTP.
    </p>
  </div>

  {#if loading}
    <p role="status" class="text-sm text-slate-500 dark:text-slate-400">Loading MCP settings…</p>
  {:else if !saved}
    <p role="alert" class="text-sm text-vermilion-600 dark:text-vermilion-400">{error}</p>
    <button type="button" onclick={load} class="px-3 py-1.5 text-xs rounded bg-accent-600 text-white hover:bg-accent-700 cursor-pointer">Retry</button>
  {:else}
    <form onsubmit={(e) => { e.preventDefault(); save(); }} class="p-5 space-y-5 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm">
      <fieldset disabled={busy} class="space-y-5 disabled:opacity-60">
        <div>
          <label for="mcp-endpoint" class="block mb-1.5 text-sm font-medium text-slate-800 dark:text-slate-100">Endpoint path</label>
          <input id="mcp-endpoint" class={inputClass} bind:value={draft.endpoint} placeholder="/api/v1/mcp" aria-describedby="mcp-path-help" />
          <p id="mcp-path-help" class="mt-1.5 text-xs text-slate-500 dark:text-slate-400">Use /mcp or another custom path. Pika’s API, login and asset paths are reserved. The server base path is added automatically.</p>
          <code class="block mt-3 p-3 rounded bg-slate-50 dark:bg-warm-900 text-xs text-slate-700 dark:text-slate-200 break-all">{endpointURL}</code>
        </div>

        <label class="flex items-start gap-3 cursor-pointer">
          <input type="checkbox" bind:checked={draft.auth_disabled} class="mt-0.5 h-4 w-4 accent-accent-600 focus:ring-accent-500" />
          <span>
            <span class="block text-sm font-medium text-slate-800 dark:text-slate-100">Disable Pika authentication for MCP</span>
            <span class="block mt-1 text-xs text-slate-500 dark:text-slate-400">Use when your reverse proxy handles authentication. Every request to this endpoint uses the scopes below, including requests with a token or session cookie.</span>
            <span class="block mt-1 text-xs text-slate-500 dark:text-slate-400">The proxy’s X-User header is recorded as the change author; otherwise, mcp-proxy is used. It does not grant user permissions.</span>
          </span>
        </label>

        {#if draft.auth_disabled}
          <div class="space-y-3">
            <div>
              <h3 class="text-sm font-semibold text-slate-800 dark:text-slate-100">Access scopes</h3>
              <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">Choose a source, path and allowed operations. Use * for one path segment or ** for a subtree. Config and external grants are independent.</p>
            </div>
            {#each draft.scopes as scope, i}
              <div class="space-y-2 border-b border-slate-200 dark:border-warm-700 pb-3">
                <label for={`mcp-resource-${i}`} class="block text-xs font-medium text-slate-700 dark:text-slate-200">Source {i + 1}</label>
                <select id={`mcp-resource-${i}`} class={inputClass} value={scope.resource || ""} onchange={(e) => scope.resource = e.currentTarget.value || undefined}>
                  <option value="">Pika configs</option>
                  {#each Object.keys(configStore.settings?.external ?? {}).sort() as name}
                    <option value={name}>External: {name}</option>
                  {/each}
                  {#if scope.resource && !configStore.settings?.external?.[scope.resource]}
                    <option value={scope.resource}>External: {scope.resource} (unavailable)</option>
                  {/if}
                </select>
                <label for={`mcp-scope-${i}`} class="block text-xs font-medium text-slate-700 dark:text-slate-200">Path pattern {i + 1}</label>
                <input id={`mcp-scope-${i}`} class={inputClass} bind:value={scope.path} placeholder="team-a/**" required />
                <div class="flex flex-wrap items-center gap-x-4 gap-y-2">
                  {#each operations as op}
                    <label class="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-200 cursor-pointer">
                      <input type="checkbox" checked={scope.operations.includes(op)} onchange={(e) => toggleOperation(scope, op, e.currentTarget.checked)} class="h-4 w-4 accent-accent-600 focus:ring-accent-500" />
                      {op}
                    </label>
                  {/each}
                  <button type="button" onclick={() => draft.scopes = draft.scopes.filter((_, n) => n !== i)} aria-label={`Remove scope ${i + 1}`} class="ml-auto text-xs text-vermilion-600 dark:text-vermilion-400 hover:underline cursor-pointer">Remove</button>
                </div>
              </div>
            {/each}
            {#if draft.scopes.length === 0}
              <p class="text-sm text-slate-500 dark:text-slate-400">No access scopes. Add a path and select its allowed operations.</p>
            {/if}
            <button type="button" onclick={() => draft.scopes = [...draft.scopes, { path: "", operations: ["read"] }]} class="px-3 py-1.5 text-xs rounded bg-slate-100 dark:bg-warm-700 hover:bg-slate-200 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-200 cursor-pointer">Add scope</button>
          </div>

          <div class="p-3 rounded bg-slate-50 dark:bg-warm-900 space-y-2">
            <h3 class="text-xs font-semibold text-slate-700 dark:text-slate-200">Tools visible to clients</h3>
            <p class="text-xs text-slate-500 dark:text-slate-400">Tools for unchecked operations are hidden and cannot be called. External operations also depend on the backend’s capabilities.</p>
            <p class="font-mono text-xs text-slate-700 dark:text-slate-200 break-words" aria-live="polite">{visibleTools.length ? visibleTools.join(", ") : "No tools"}</p>
          </div>
        {:else}
          <p class="text-sm text-slate-500 dark:text-slate-400">Clients authenticate with a Pika API token or session. Each caller sees only the tools their permissions allow.</p>
        {/if}
      </fieldset>

      {#if error}<p role="alert" class="text-sm text-vermilion-600 dark:text-vermilion-400">{error}</p>{/if}
      <div class="flex flex-wrap items-center gap-3">
        <button type="submit" disabled={busy || !dirty || (draft.auth_disabled && (draft.scopes.length === 0 || invalidScopes))} class="px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer">{busy ? "Saving…" : "Save MCP settings"}</button>
        <p class="text-xs text-slate-500 dark:text-slate-400">Changes apply immediately. Update your client if you change the path.</p>
      </div>
    </form>
  {/if}
</section>
