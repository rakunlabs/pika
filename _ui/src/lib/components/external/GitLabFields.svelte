<script lang="ts">
  import type { GitLabConfig } from "@/lib/types/config";

  let { config = $bindable(), readonly = false }: {
    config: GitLabConfig;
    readonly?: boolean;
  } = $props();
  const id = $props.id();
  const inputClass = "w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500";
</script>

<div class="space-y-4 mb-4">
  <div>
    <label for={`${id}-address`} class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">GitLab URL</label>
    <input id={`${id}-address`} type="url" bind:value={config.address} {readonly} placeholder="https://gitlab.com" class={inputClass} />
    <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">Instance URL, including any installation subpath, without /api/v4.</p>
  </div>
  <div>
    <label for={`${id}-group`} class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Group ID or path</label>
    <input id={`${id}-group`} bind:value={config.group} {readonly} placeholder="my-team/subgroup or 123" class={inputClass} />
    <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">Lists this group's own CI/CD variables. Parent-group and project variables are not included.</p>
  </div>
  <div>
    <label for={`${id}-token`} class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Access token</label>
    {#if readonly}
      <div id={`${id}-token`} class="text-sm text-slate-500 dark:text-slate-400">{config.token ? "Token configured (hidden)" : "No token configured"}</div>
    {:else}
      <input id={`${id}-token`} type="password" bind:value={config.token} autocomplete="new-password" spellcheck={false} class={inputClass} />
      <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">Use a personal or group access token with api scope and Owner access to the group. The token is encrypted at rest.</p>
    {/if}
  </div>
  <div>
    <label for={`${id}-scope`} class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Environment scope</label>
    <input id={`${id}-scope`} bind:value={config.environment_scope} {readonly} placeholder="*" class={inputClass} />
    <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">Exact scope, such as * or production. * selects the default scope, not every environment. Add another resource for a different scope.</p>
  </div>
</div>
