<script lang="ts">
  import type { GitLabVariableOptions } from "@/lib/types/config";

  let { value = $bindable(), readonly = false, hidden = false, disabled = false }: {
    value: GitLabVariableOptions;
    readonly?: boolean;
    hidden?: boolean;
    disabled?: boolean;
  } = $props();
  const id = $props.id();
</script>

<div class="px-5 py-3 border-b border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-800 shrink-0 max-h-[40%] overflow-y-auto">
  {#if readonly}
    <dl class="flex flex-wrap gap-x-6 gap-y-2 text-xs">
      <div><dt class="text-slate-500 dark:text-slate-400">Variable type</dt><dd class="text-slate-800 dark:text-slate-100">{value.variable_type === "file" ? "File" : "Variable"}</dd></div>
      <div><dt class="text-slate-500 dark:text-slate-400">Masked</dt><dd class="text-slate-800 dark:text-slate-100">{value.masked ? "Yes" : "No"}</dd></div>
      <div><dt class="text-slate-500 dark:text-slate-400">Protected</dt><dd class="text-slate-800 dark:text-slate-100">{value.protected ? "Yes" : "No"}</dd></div>
      <div><dt class="text-slate-500 dark:text-slate-400">Expand variable reference</dt><dd class="text-slate-800 dark:text-slate-100">{value.raw ? "No" : "Yes"}</dd></div>
    </dl>
  {:else}
    <fieldset {disabled} class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-3 disabled:opacity-60">
      <legend class="sr-only">GitLab variable settings</legend>
      <div>
        <label for={`${id}-type`} class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1">Variable type</label>
        <select id={`${id}-type`} bind:value={value.variable_type} class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500">
          <option value="env_var">Variable</option>
          <option value="file">File</option>
        </select>
      </div>
      <div class="flex flex-col justify-center gap-2">
        <label class="flex items-center gap-2 text-sm text-slate-800 dark:text-slate-100">
          <input type="checkbox" bind:checked={value.masked} disabled={hidden} class="accent-accent-600 focus-visible:outline-2 focus-visible:outline-accent-500" />
          Masked
        </label>
        <label class="flex items-center gap-2 text-sm text-slate-800 dark:text-slate-100">
          <input type="checkbox" bind:checked={value.protected} class="accent-accent-600 focus-visible:outline-2 focus-visible:outline-accent-500" />
          Protected
        </label>
        <label class="flex items-center gap-2 text-sm text-slate-800 dark:text-slate-100">
          <input type="checkbox" checked={!value.raw} onchange={(event) => value.raw = !event.currentTarget.checked} class="accent-accent-600 focus-visible:outline-2 focus-visible:outline-accent-500" />
          Expand variable reference
        </label>
      </div>
    </fieldset>
    <p class="mt-2 text-xs text-slate-500 dark:text-slate-400">
      Masked hides the value in job logs; GitLab requires at least 8 characters and no spaces or line breaks. Protected limits access to protected branches and tags. Expansion resolves references such as $OTHER_VARIABLE.
      {#if value.variable_type === "file"}The editor below contains the file content.{/if}
    </p>
  {/if}
  {#if hidden}
    <p class="mt-2 text-xs text-slate-500 dark:text-slate-400">This variable is also hidden in GitLab. Hidden status is read-only and requires masking.</p>
  {/if}
</div>
