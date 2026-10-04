<script lang="ts">
  import { onMount } from "svelte";
  import {
    HardDrive,
    Cloud,
    Ban,
    CheckCircle2,
    AlertTriangle,
    Loader2,
    Info,
  } from "lucide-svelte";
  import { configStore } from "@/lib/store/config.svelte";
  import { keymgrStore } from "@/lib/store/keymgr.svelte";
  import { link } from "svelte-spa-router";
  import { apiServerMessage } from "@/lib/api/client";
  import type { VaultFilesSettings } from "@/lib/types/config";
  import {
    inputClass,
    labelClass,
    btnPrimary,
    btnSecondary,
    cardClass,
  } from "@/lib/ui";

  function empty(): VaultFilesSettings {
    return {
      backend: "",
      local: { path: "" },
      s3: { endpoint: "", region: "", bucket: "", prefix: "", access_key_id: "", use_path_style: false },
    };
  }

  let form = $state<VaultFilesSettings>(empty());
  let saved = $state<VaultFilesSettings>(empty());
  let secret = $state("");
  let replaceSecret = $state(false);
  let saving = $state(false);
  let testing = $state(false);
  let testResult = $state<{ ok: boolean; message?: string } | null>(null);
  let loaded = $state(false);

  function fromSettings(): VaultFilesSettings {
    const v = configStore.settings?.vault_files;
    const base = empty();
    if (!v) return base;
    return {
      backend: v.backend ?? "",
      local: { path: v.local?.path ?? "" },
      s3: { ...base.s3, ...v.s3 },
    };
  }

  async function load() {
    await configStore.loadSettings();
    saved = fromSettings();
    form = structuredClone($state.snapshot(saved));
    secret = "";
    replaceSecret = !saved.s3.secret_access_key_set;
    loaded = true;
  }
  onMount(() => {
    load();
    keymgrStore.refreshStatus();
  });

  // The S3 secret goes through the seal layer, which refuses writes
  // unless the server encryption key is initialized and unlocked.
  const keyStatus = $derived(keymgrStore.status);
  const encryptionReady = $derived(
    keyStatus !== null && keyStatus.initialized && keyStatus.unlocked,
  );

  const secretStored = $derived(!!saved.s3.secret_access_key_set);

  function payload(): VaultFilesSettings {
    const p = structuredClone($state.snapshot(form)) as VaultFilesSettings;
    p.s3.secret_access_key = replaceSecret ? secret : "";
    delete p.s3.secret_access_key_set;
    return p;
  }

  async function save(e: Event) {
    e.preventDefault();
    saving = true;
    testResult = null;
    try {
      await configStore.saveVaultFilesSettings(payload());
      await load();
    } catch {
      /* toast shown by store */
    } finally {
      saving = false;
    }
  }

  async function test() {
    testing = true;
    testResult = null;
    try {
      testResult = await configStore.testVaultFilesSettings(payload());
    } catch (err) {
      testResult = { ok: false, message: apiServerMessage(err, "Connection test failed") };
    } finally {
      testing = false;
    }
  }

  function pickBackend(b: VaultFilesSettings["backend"]) {
    form.backend = b;
    testResult = null;
    if (b === "s3" && !form.s3.region) form.s3.region = "us-east-1";
  }

  const backendChanged = $derived(saved.backend !== "" && form.backend !== saved.backend);

  const options = [
    { key: "", label: "Disabled", desc: "Users can't upload files.", icon: Ban },
    { key: "local", label: "Local disk", desc: "A directory on this server.", icon: HardDrive },
    { key: "s3", label: "S3 bucket", desc: "AWS S3, MinIO, R2, …", icon: Cloud },
  ] as const;
</script>

<div class="space-y-6">
  <div>
    <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">Vault storage</h2>
    <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
      Choose where files uploaded to users' personal vaults are stored. File names and folders are
      kept in Pika's database; the file contents go to the backend below.
    </p>
  </div>

  {#if !loaded}
    <div class="py-10 flex justify-center"><Loader2 size={18} class="animate-spin text-slate-400" /></div>
  {:else}
    <form onsubmit={save} class="space-y-4">
      <div class={cardClass}>
        <div class="grid grid-cols-1 sm:grid-cols-3 gap-2" role="radiogroup" aria-label="Storage backend">
          {#each options as opt (opt.key)}
            {@const Icon = opt.icon}
            {@const active = form.backend === opt.key}
            <button
              type="button"
              role="radio"
              aria-checked={active}
              onclick={() => pickBackend(opt.key)}
              class="flex items-start gap-2.5 p-3 rounded-lg border text-left cursor-pointer transition-colors
                {active
                ? 'bg-accent-50 text-accent-700 border-accent-300 dark:bg-accent-900/40 dark:text-accent-300 dark:border-accent-700'
                : 'border-slate-200 dark:border-warm-700 hover:bg-slate-50 dark:hover:bg-warm-700 text-slate-700 dark:text-slate-200'}"
            >
              <Icon size={16} class="shrink-0 mt-0.5" />
              <span>
                <span class="block text-sm font-medium">{opt.label}</span>
                <span class="block text-xs {active ? 'opacity-80' : 'text-slate-500 dark:text-slate-400'}">{opt.desc}</span>
              </span>
            </button>
          {/each}
        </div>

        {#if form.backend === "local"}
          <div class="mt-4 space-y-3">
            <div>
              <label class={labelClass} for="vf-local-path">Directory</label>
              <input id="vf-local-path" class="{inputClass} font-mono" bind:value={form.local.path} placeholder="/var/lib/pika/vault-files" required />
              <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">
                Created on first use with owner-only permissions. Relative paths resolve against
                Pika's working directory. Include it in your backups — it is not part of Pika's
                database backup.
              </p>
            </div>
          </div>
        {:else if form.backend === "s3"}
          <div class="mt-4 grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div class="sm:col-span-2">
              <label class={labelClass} for="vf-s3-endpoint">Endpoint</label>
              <input id="vf-s3-endpoint" class="{inputClass} font-mono" bind:value={form.s3.endpoint} placeholder="https://s3.eu-central-1.amazonaws.com" required />
            </div>
            <div>
              <label class={labelClass} for="vf-s3-bucket">Bucket</label>
              <input id="vf-s3-bucket" class={inputClass} bind:value={form.s3.bucket} placeholder="pika-vault" required />
            </div>
            <div>
              <label class={labelClass} for="vf-s3-region">Region</label>
              <input id="vf-s3-region" class={inputClass} bind:value={form.s3.region} placeholder="us-east-1" />
            </div>
            <div>
              <label class={labelClass} for="vf-s3-prefix">Key prefix <span class="normal-case font-normal">(optional)</span></label>
              <input id="vf-s3-prefix" class="{inputClass} font-mono" bind:value={form.s3.prefix} placeholder="pika/" />
            </div>
            <div class="flex items-end pb-2">
              <label class="flex items-center gap-2 text-sm cursor-pointer text-slate-700 dark:text-slate-200">
                <input type="checkbox" class="h-4 w-4 rounded border-slate-300 dark:border-warm-600 text-accent-600 focus:ring-accent-500" bind:checked={form.s3.use_path_style} />
                Path-style URLs <span class="text-xs text-slate-500 dark:text-slate-400">(MinIO, most self-hosted)</span>
              </label>
            </div>
            <div>
              <label class={labelClass} for="vf-s3-ak">Access key ID</label>
              <input id="vf-s3-ak" class="{inputClass} font-mono" bind:value={form.s3.access_key_id} autocomplete="off" required />
            </div>
            <div>
              <label class={labelClass} for="vf-s3-sk">Secret access key</label>
              {#if secretStored && !replaceSecret}
                <div class="flex items-center gap-2">
                  <input id="vf-s3-sk" class="{inputClass} font-mono" value="•••••••• (stored)" disabled />
                  <button type="button" class="{btnSecondary} shrink-0" onclick={() => (replaceSecret = true)}>Replace</button>
                </div>
              {:else}
                <input id="vf-s3-sk" type="password" class="{inputClass} font-mono" bind:value={secret} autocomplete="new-password" required={!secretStored} placeholder={secretStored ? "Leave blank to keep the stored secret" : ""} />
              {/if}
              <p class="mt-1 text-xs text-slate-500 dark:text-slate-400">Encrypted at rest with the server key; never shown again.</p>
            </div>
            {#if !encryptionReady && keyStatus !== null}
              <div class="sm:col-span-2 bg-amber-50 dark:bg-amber-950/40 border border-amber-300 dark:border-amber-700 rounded p-3 text-xs flex gap-2">
                <AlertTriangle size={14} class="text-amber-700 dark:text-amber-300 shrink-0 mt-0.5" />
                <div class="text-amber-900 dark:text-amber-200">
                  {keyStatus.initialized ? "The server encryption key is locked." : "The server encryption key is not initialized."}
                  The S3 secret is stored encrypted, so it can't be saved until the key is
                  {keyStatus.initialized ? "unlocked" : "set up"} in
                  <a href="/settings/rotation" use:link class="underline font-medium">Key Rotation</a>.
                  Testing the connection still works.
                </div>
              </div>
            {/if}
            <div class="sm:col-span-2 bg-blue-50 dark:bg-blue-950/30 border border-blue-300 dark:border-blue-700 rounded p-3 text-xs flex gap-2">
              <Info size={14} class="text-blue-700 dark:text-blue-300 shrink-0 mt-0.5" />
              <div class="text-blue-900 dark:text-blue-200">
                Files are stored as-is (not end-to-end encrypted). Use an <strong>https</strong>
                endpoint, keep the bucket private, and enable server-side encryption on the bucket.
                The access key only needs <code>s3:GetObject</code>, <code>s3:PutObject</code>,
                <code>s3:DeleteObject</code> and <code>s3:ListBucket</code> on this bucket.
              </div>
            </div>
          </div>
        {/if}

        {#if backendChanged}
          <div class="mt-4 bg-amber-50 dark:bg-amber-950/40 border border-amber-300 dark:border-amber-700 rounded p-3 text-xs flex gap-2">
            <AlertTriangle size={14} class="text-amber-700 dark:text-amber-300 shrink-0 mt-0.5" />
            <div class="text-amber-900 dark:text-amber-200">
              Existing files are not migrated. Files uploaded to the previous backend stay listed
              but can't be downloaded until you switch back.
            </div>
          </div>
        {/if}

        {#if testResult}
          <div
            class="mt-4 rounded p-3 text-xs flex gap-2 border
              {testResult.ok
              ? 'bg-emerald-50 dark:bg-emerald-950/30 border-emerald-300 dark:border-emerald-700 text-emerald-900 dark:text-emerald-200'
              : 'bg-red-50 dark:bg-red-950/40 border-red-300 dark:border-red-700 text-red-900 dark:text-red-200'}"
          >
            {#if testResult.ok}
              <CheckCircle2 size={14} class="shrink-0 mt-0.5" />
              <span>Connection works — Pika can write, read and delete objects.</span>
            {:else}
              <AlertTriangle size={14} class="shrink-0 mt-0.5" />
              <span class="break-all">{testResult.message || "Connection failed"}</span>
            {/if}
          </div>
        {/if}
      </div>

      <div class="flex gap-2 justify-end">
        {#if form.backend !== ""}
          <button type="button" class={btnSecondary} onclick={test} disabled={testing}>
            {#if testing}<Loader2 size={12} class="inline animate-spin mr-1" />{/if}
            Test connection
          </button>
        {/if}
        <button type="submit" class={btnPrimary} disabled={saving}>
          {saving ? "Saving…" : "Save"}
        </button>
      </div>
    </form>
  {/if}
</div>
