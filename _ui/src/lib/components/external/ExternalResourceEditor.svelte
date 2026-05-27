<script lang="ts">
  // ExternalResourceEditor renders a single external resource in one of
  // three modes:
  //   - 'view'   read-only. Secret-bearing fields are masked.
  //   - 'edit'   editable. Pre-populated from an existing resource. Name
  //              is editable; on save the parent renames the map entry.
  //   - 'create' editable. Empty form, like the legacy Settings section.
  //
  // The component is intentionally a single file: every backend type
  // shares the same outer chrome (header, name input, type radio,
  // action buttons) and only the inner field set differs. Splitting the
  // type-specific blocks into separate files would add 8 trivial
  // components plus their props plumbing without simplifying anything.

  import { untrack } from "svelte";
  import { configStore } from "@/lib/store/config.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import {
    Save,
    X,
    Pencil,
    Trash2,
    Play,
    Plus,
    AlertCircle,
    CheckCircle2,
    Loader2,
  } from "lucide-svelte";
  import type { ExternalResource } from "@/lib/types/config";

  type Mode = "view" | "edit" | "create";
  type ResourceKind =
    | "http"
    | "vault"
    | "kubernetes"
    | "consul"
    | "etcd"
    | "aws"
    | "gcp"
    | "gcp-parameter"
    | "azure";

  type Props = {
    // Current name of the resource. In 'create' mode this is the empty
    // string; the user fills it in. Used as the map key.
    name: string;
    // The resource payload. In 'create' mode the parent should pass an
    // empty object {} and let the user pick a type.
    resource: ExternalResource;
    mode: Mode;
    // Fired after a successful save with the (possibly renamed) name so
    // the parent can re-select the new entry.
    onSaved?: (name: string) => void;
    // Fired after a successful delete. Parent should clear selection.
    onDeleted?: () => void;
    // Fired when the user cancels editing without saving. In 'create'
    // mode this should close the panel; in 'edit' mode it should revert
    // to 'view'.
    onCancel?: () => void;
    // Fired when the user clicks the Edit button in 'view' mode.
    onEdit?: () => void;
  };

  let { name, resource, mode, onSaved, onDeleted, onCancel, onEdit }: Props =
    $props();

  // ── Form state ────────────────────────────────────────────────────────
  // Initialised once from the props at mount time. The parent forces a
  // remount with {#key name + ':' + mode} whenever selection or mode
  // flips, so we don't need (and don't want) two-way reactivity between
  // the prop tree and these local editor fields — otherwise mid-edit
  // edits would be silently clobbered by parent re-renders.
  //
  // We wrap the prop reads in `untrack()` to make the "snapshot once,
  // never re-read" intent explicit to the Svelte 5 compiler. Without
  // it, the compiler emits a `state_referenced_locally` warning on the
  // bare `const snapName = name` form because that read pattern is
  // usually a bug (the const captures the initial prop value and never
  // updates). Here that's exactly what we want — the {#key} remount
  // takes care of reseeding — so untrack documents the choice instead
  // of silencing a real warning.
  const snapName = untrack(() => name);
  const snapResource = untrack(() => resource);

  let formName = $state(snapName);
  let formType = $state<ResourceKind>(detectKind(snapResource));

  // HTTP
  let httpUrl = $state(snapResource.http?.base_url ?? "");
  // Headers normalised into pairs for editing. The wire format is
  // Record<string,string[]> because a single header name can carry
  // multiple values (e.g., Set-Cookie); we flatten it on save.
  let httpHeaders = $state<Array<{ name: string; value: string }>>(
    flattenHeaders(snapResource.http?.header),
  );

  // Vault
  let vaultAddr = $state(snapResource.vault?.address ?? "");
  let vaultMount = $state(snapResource.vault?.mount ?? "secret");
  let vaultRoleId = $state(snapResource.vault?.app_role?.role_id ?? "");
  let vaultSecretId = $state(snapResource.vault?.app_role?.secret_id ?? "");
  let vaultAppRolePath = $state(
    snapResource.vault?.app_role?.app_role_base_path ?? "approle",
  );

  // Kubernetes
  let k8sMode = $state<"in-cluster" | "path" | "inline">(
    snapResource.kubernetes?.kubeconfig_content
      ? "inline"
      : snapResource.kubernetes?.kubeconfig
        ? "path"
        : "in-cluster",
  );
  let k8sPath = $state(snapResource.kubernetes?.kubeconfig ?? "");
  let k8sContent = $state(snapResource.kubernetes?.kubeconfig_content ?? "");

  // Consul
  let consulAddr = $state(snapResource.consul?.address ?? "");
  let consulToken = $state(snapResource.consul?.token ?? "");

  // etcd
  let etcdAddr = $state(snapResource.etcd?.address ?? "");
  let etcdUser = $state(snapResource.etcd?.username ?? "");
  let etcdPass = $state(snapResource.etcd?.password ?? "");

  // AWS
  let awsRegion = $state(snapResource.aws?.region ?? "us-east-1");
  let awsAccessKey = $state(snapResource.aws?.access_key ?? "");
  let awsSecretKey = $state(snapResource.aws?.secret_key ?? "");
  let awsService = $state<"secretsmanager" | "ssm">(
    (snapResource.aws?.service as "secretsmanager" | "ssm") ?? "secretsmanager",
  );

  // GCP
  let gcpJson = $state(snapResource.gcp?.service_account_json ?? "");

  // GCP Parameter Manager
  let gcpParamJson = $state(
    snapResource.gcp_parameter?.service_account_json ?? "",
  );
  let gcpParamLocation = $state(
    snapResource.gcp_parameter?.location ?? "global",
  );

  // Azure
  let azureVaultUrl = $state(snapResource.azure?.vault_url ?? "");
  let azureTenantId = $state(snapResource.azure?.tenant_id ?? "");
  let azureClientId = $state(snapResource.azure?.client_id ?? "");
  let azureClientSecret = $state(snapResource.azure?.client_secret ?? "");

  // ── Test connection state ─────────────────────────────────────────────
  let testing = $state(false);
  let testResult = $state<{
    ok: boolean;
    message?: string;
    sample?: string[];
  } | null>(null);

  // ── Confirm-delete state. Inline confirmation avoids a modal layer ─────
  let confirmDelete = $state(false);
  let deleting = $state(false);

  // ── Saving state to prevent double-submit ─────────────────────────────
  let saving = $state(false);

  // Read-only computed: which props are masked in view mode. We mask any
  // field that the user typed as a secret on creation (passwords, secret
  // keys, app role secret ID, kubeconfig content, service account JSON).
  const isReadOnly = $derived(mode === "view");

  function detectKind(r: ExternalResource): ResourceKind {
    if (r.vault) return "vault";
    if (r.kubernetes) return "kubernetes";
    if (r.consul) return "consul";
    if (r.etcd) return "etcd";
    if (r.aws) return "aws";
    if (r.gcp) return "gcp";
    if (r.gcp_parameter) return "gcp-parameter";
    if (r.azure) return "azure";
    // Default to http for create or empty resources.
    return "http";
  }

  function flattenHeaders(
    h: Record<string, string[]> | undefined,
  ): Array<{ name: string; value: string }> {
    if (!h) return [];
    const out: Array<{ name: string; value: string }> = [];
    for (const [name, values] of Object.entries(h)) {
      for (const value of values) {
        out.push({ name, value });
      }
    }
    return out;
  }

  function buildHeaderMap(): Record<string, string[]> | undefined {
    const out: Record<string, string[]> = {};
    for (const pair of httpHeaders) {
      const name = pair.name.trim();
      if (!name) continue;
      if (!out[name]) out[name] = [];
      out[name].push(pair.value);
    }
    return Object.keys(out).length > 0 ? out : undefined;
  }

  function addHttpHeaderRow() {
    httpHeaders = [...httpHeaders, { name: "", value: "" }];
  }

  function removeHttpHeaderRow(index: number) {
    httpHeaders = httpHeaders.filter((_, i) => i !== index);
  }

  // Assemble the wire-format resource from the current form state.
  // Returns null + toasts on validation failure.
  function buildResource(): ExternalResource | null {
    const r: ExternalResource = {} as ExternalResource;
    if (formType === "http") {
      if (!httpUrl.trim()) {
        addToast("HTTP URL is required", "alert");
        return null;
      }
      const headerMap = buildHeaderMap();
      r.http = {
        base_url: httpUrl.trim(),
        ...(headerMap ? { header: headerMap } : {}),
      };
    } else if (formType === "vault") {
      if (!vaultAddr.trim() || !vaultMount.trim()) {
        addToast("Vault address and mount are required", "alert");
        return null;
      }
      if (!vaultRoleId.trim() || !vaultSecretId.trim()) {
        addToast("AppRole Role ID and Secret ID are required", "alert");
        return null;
      }
      r.vault = {
        address: vaultAddr.trim(),
        mount: vaultMount.trim(),
        app_role: {
          role_id: vaultRoleId.trim(),
          secret_id: vaultSecretId.trim(),
          app_role_base_path: vaultAppRolePath.trim() || "approle",
        },
      };
    } else if (formType === "kubernetes") {
      const k8s: { kubeconfig?: string; kubeconfig_content?: string } = {};
      if (k8sMode === "path") {
        if (!k8sPath.trim()) {
          addToast("Kubeconfig path is required", "alert");
          return null;
        }
        k8s.kubeconfig = k8sPath.trim();
      } else if (k8sMode === "inline") {
        if (!k8sContent.trim()) {
          addToast("Kubeconfig content is required", "alert");
          return null;
        }
        k8s.kubeconfig_content = k8sContent;
      }
      r.kubernetes = k8s;
    } else if (formType === "consul") {
      if (!consulAddr.trim()) {
        addToast("Consul address is required", "alert");
        return null;
      }
      r.consul = {
        address: consulAddr.trim(),
        ...(consulToken.trim() ? { token: consulToken.trim() } : {}),
      };
    } else if (formType === "etcd") {
      if (!etcdAddr.trim()) {
        addToast("etcd address is required", "alert");
        return null;
      }
      r.etcd = {
        address: etcdAddr.trim(),
        ...(etcdUser.trim() ? { username: etcdUser.trim() } : {}),
        ...(etcdPass.trim() ? { password: etcdPass.trim() } : {}),
      };
    } else if (formType === "aws") {
      if (!awsRegion.trim() || !awsAccessKey.trim() || !awsSecretKey.trim()) {
        addToast(
          "AWS region, access key, and secret key are required",
          "alert",
        );
        return null;
      }
      r.aws = {
        region: awsRegion.trim(),
        access_key: awsAccessKey.trim(),
        secret_key: awsSecretKey.trim(),
        service: awsService,
      };
    } else if (formType === "gcp") {
      if (!gcpJson.trim()) {
        addToast("GCP service account JSON is required", "alert");
        return null;
      }
      r.gcp = { service_account_json: gcpJson.trim() };
    } else if (formType === "gcp-parameter") {
      if (!gcpParamJson.trim()) {
        addToast("GCP service account JSON is required", "alert");
        return null;
      }
      r.gcp_parameter = {
        service_account_json: gcpParamJson.trim(),
        ...(gcpParamLocation.trim() && gcpParamLocation.trim() !== "global"
          ? { location: gcpParamLocation.trim() }
          : {}),
      };
    } else if (formType === "azure") {
      if (
        !azureVaultUrl.trim() ||
        !azureTenantId.trim() ||
        !azureClientId.trim() ||
        !azureClientSecret.trim()
      ) {
        addToast(
          "Azure vault URL, tenant ID, client ID, and client secret are required",
          "alert",
        );
        return null;
      }
      r.azure = {
        vault_url: azureVaultUrl.trim(),
        tenant_id: azureTenantId.trim(),
        client_id: azureClientId.trim(),
        client_secret: azureClientSecret.trim(),
      };
    }
    return r;
  }

  async function handleSave() {
    if (saving) return;
    const trimmedName = formName.trim();
    if (!trimmedName) {
      addToast("Resource name is required", "alert");
      return;
    }
    const built = buildResource();
    if (!built) return;

    saving = true;
    try {
      if (mode === "create") {
        // In create mode we must not overwrite an existing entry with
        // the same name silently — the user expects "Add" to be additive.
        const existing = configStore.settings?.external?.[trimmedName];
        if (existing) {
          addToast(`Resource "${trimmedName}" already exists`, "alert");
          saving = false;
          return;
        }
        await configStore.saveExternalResource(trimmedName, built);
      } else if (mode === "edit") {
        if (trimmedName !== name) {
          // Rename first; this drops the old key and writes the value
          // under the new key. Then update the value in place.
          await configStore.renameExternalResource(name, trimmedName);
        }
        await configStore.saveExternalResource(trimmedName, built);
      }
      onSaved?.(trimmedName);
    } catch (err) {
      // saveSettings already toasts on failure; no need to double-toast.
      console.error("External save failed", err);
    } finally {
      saving = false;
    }
  }

  async function handleDelete() {
    if (deleting) return;
    deleting = true;
    try {
      await configStore.removeExternalResource(name);
      onDeleted?.();
    } catch (err) {
      console.error("External delete failed", err);
    } finally {
      deleting = false;
      confirmDelete = false;
    }
  }

  // Test runs against the *persisted* version of the resource. We don't
  // serialise the unsaved form to the server because that would require
  // a stateful "draft" endpoint. Make the constraint explicit in the UI:
  // disable Test while there are unsaved edits.
  async function handleTest() {
    if (testing) return;
    testing = true;
    testResult = null;
    try {
      const r = await configStore.testExternal(name);
      testResult = r;
    } finally {
      testing = false;
    }
  }

  // Type-specific summary strings for the view-mode read-out.
  function maskedSecret(s: string | undefined, visible = 4): string {
    if (!s) return "—";
    if (s.length <= visible) return "••••";
    return s.slice(0, visible) + "••••";
  }
</script>

<div class="flex flex-col h-full overflow-hidden bg-white dark:bg-warm-950">
  <!-- Header: name / type / actions -->
  <div
    class="flex items-center gap-3 px-5 py-3 border-b border-slate-200 dark:border-warm-700 shrink-0"
  >
    <div class="flex-1 min-w-0">
      {#if isReadOnly}
        <div class="flex items-center gap-2">
          <span
            class="text-base font-semibold text-slate-800 dark:text-slate-100 truncate"
            >{name}</span
          >
          <span
            class="px-1.5 py-0.5 text-[10px] font-medium rounded bg-slate-100 dark:bg-warm-800 text-slate-600 dark:text-slate-300 uppercase"
          >
            {formType}
          </span>
        </div>
      {:else}
        <input
          type="text"
          bind:value={formName}
          placeholder="Resource name (e.g., shared-config)"
          class="w-full px-2 py-1 text-base font-semibold border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
        />
      {/if}
    </div>

    <div class="flex items-center gap-1.5 shrink-0">
      {#if isReadOnly}
        <button
          class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer"
          onclick={() => onEdit?.()}
          title="Edit resource"
        >
          <Pencil size={12} /> Edit
        </button>
        <button
          class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-slate-700 dark:text-slate-200 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md hover:bg-slate-50 dark:hover:bg-warm-800 transition-colors cursor-pointer disabled:opacity-50"
          onclick={handleTest}
          disabled={testing}
          title="Probe the resource using its stored credentials"
        >
          {#if testing}
            <Loader2 size={12} class="animate-spin" />
          {:else}
            <Play size={12} />
          {/if}
          Test
        </button>
        {#if !confirmDelete}
          <button
            class="p-1.5 text-slate-400 dark:text-slate-500 hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-950/30 rounded transition-colors cursor-pointer"
            onclick={() => (confirmDelete = true)}
            title="Delete resource"
          >
            <Trash2 size={14} />
          </button>
        {:else}
          <span class="text-xs text-slate-600 dark:text-slate-300"
            >Really delete?</span
          >
          <button
            class="px-2 py-1 text-xs font-medium text-white bg-red-600 rounded hover:bg-red-700 transition-colors cursor-pointer disabled:opacity-50"
            onclick={handleDelete}
            disabled={deleting}
          >
            Delete
          </button>
          <button
            class="px-2 py-1 text-xs font-medium text-slate-700 dark:text-slate-200 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded hover:bg-slate-50 dark:hover:bg-warm-800 transition-colors cursor-pointer"
            onclick={() => (confirmDelete = false)}
          >
            Cancel
          </button>
        {/if}
      {:else}
        <button
          class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-white bg-accent-600 rounded-md hover:bg-accent-700 transition-colors cursor-pointer disabled:opacity-50"
          onclick={handleSave}
          disabled={saving}
        >
          {#if saving}
            <Loader2 size={12} class="animate-spin" />
          {:else}
            <Save size={12} />
          {/if}
          {mode === "create" ? "Create" : "Save"}
        </button>
        <button
          class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-slate-700 dark:text-slate-200 bg-white dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md hover:bg-slate-50 dark:hover:bg-warm-800 transition-colors cursor-pointer"
          onclick={() => onCancel?.()}
        >
          <X size={12} /> Cancel
        </button>
      {/if}
    </div>
  </div>

  <!-- Test result banner. Cleared whenever a new test runs. -->
  {#if testResult}
    <div
      class="px-5 py-2 border-b border-slate-200 dark:border-warm-700 shrink-0
                {testResult.ok
        ? 'bg-emerald-50 dark:bg-emerald-950/30'
        : 'bg-red-50 dark:bg-red-950/30'}"
    >
      <div class="flex items-start gap-2">
        {#if testResult.ok}
          <CheckCircle2
            size={16}
            class="text-emerald-600 dark:text-emerald-400 mt-0.5 shrink-0"
          />
        {:else}
          <AlertCircle
            size={16}
            class="text-red-600 dark:text-red-400 mt-0.5 shrink-0"
          />
        {/if}
        <div class="flex-1 min-w-0">
          <div
            class="text-xs font-medium {testResult.ok
              ? 'text-emerald-800 dark:text-emerald-200'
              : 'text-red-800 dark:text-red-200'}"
          >
            {testResult.message || (testResult.ok ? "OK" : "Failed")}
          </div>
          {#if testResult.sample && testResult.sample.length > 0}
            <div
              class="mt-1 max-h-32 overflow-y-auto text-[11px] font-mono text-slate-600 dark:text-slate-300 space-y-0.5"
            >
              {#each testResult.sample as line, i (i)}
                <div class="truncate">{line}</div>
              {/each}
            </div>
          {/if}
        </div>
        <button
          class="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 shrink-0 cursor-pointer"
          onclick={() => (testResult = null)}
          aria-label="Dismiss test result"
        >
          <X size={14} />
        </button>
      </div>
    </div>
  {/if}

  <!-- Body: type selector + per-type fields. Scrolls independently of header. -->
  <div class="flex-1 overflow-y-auto px-5 py-4">
    <!-- Type selection: only in create mode. Editing the type of an
         existing resource would force a complete re-entry of credentials
         and is more confusing than helpful — users should delete + add. -->
    {#if mode === "create"}
      <div class="mb-4">
        <span
          class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
          >Type</span
        >
        <div class="flex flex-wrap gap-3">
          {#each [{ value: "http", label: "HTTP" }, { value: "vault", label: "Vault" }, { value: "kubernetes", label: "Kubernetes" }, { value: "consul", label: "Consul" }, { value: "etcd", label: "etcd" }, { value: "aws", label: "AWS" }, { value: "gcp", label: "GCP Secret" }, { value: "gcp-parameter", label: "GCP Parameter" }, { value: "azure", label: "Azure" }] as opt (opt.value)}
            <label
              class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300 cursor-pointer"
            >
              <input
                type="radio"
                bind:group={formType}
                value={opt.value}
                class="text-accent-600"
              />
              {opt.label}
            </label>
          {/each}
        </div>
      </div>
    {/if}

    {#if formType === "http"}
      <div class="mb-4">
        <span
          class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
          >Base URL</span
        >
        {#if isReadOnly}
          <div
            class="px-3 py-2 text-sm font-mono text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
          >
            {httpUrl || "—"}
          </div>
        {:else}
          <input
            type="url"
            bind:value={httpUrl}
            placeholder="https://config-server.example.com/api/config"
            class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
          />
        {/if}
      </div>

      <div class="mb-4">
        <div class="flex items-center justify-between mb-1.5">
          <span class="text-xs font-medium text-slate-500 dark:text-slate-400"
            >Headers</span
          >
          {#if !isReadOnly}
            <button
              type="button"
              class="flex items-center gap-1 px-2 py-1 text-[11px] text-accent-700 dark:text-accent-300 bg-accent-50 dark:bg-accent-950/40 rounded hover:bg-accent-100 dark:hover:bg-accent-950/60 transition-colors cursor-pointer"
              onclick={addHttpHeaderRow}
            >
              <Plus size={10} /> Add header
            </button>
          {/if}
        </div>
        {#if httpHeaders.length === 0}
          <p class="text-[11px] text-slate-400 dark:text-slate-500 italic">
            No custom headers.
          </p>
        {:else}
          <div class="space-y-1.5">
            {#each httpHeaders as pair, i (i)}
              <div class="flex items-center gap-1.5">
                {#if isReadOnly}
                  <div
                    class="flex-1 px-2.5 py-1.5 text-xs font-mono bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md text-slate-700 dark:text-slate-200 truncate"
                  >
                    {pair.name}
                  </div>
                  <div
                    class="flex-1 px-2.5 py-1.5 text-xs font-mono bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md text-slate-700 dark:text-slate-200 truncate"
                  >
                    {maskedSecret(pair.value, 6)}
                  </div>
                {:else}
                  <input
                    type="text"
                    bind:value={pair.name}
                    placeholder="Header name"
                    class="flex-1 px-2.5 py-1.5 text-xs font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                  />
                  <input
                    type="text"
                    bind:value={pair.value}
                    placeholder="Value"
                    class="flex-1 px-2.5 py-1.5 text-xs font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
                  />
                  <button
                    type="button"
                    class="p-1 text-slate-400 hover:text-red-500 hover:bg-red-50 dark:hover:bg-red-950/30 rounded transition-colors cursor-pointer shrink-0"
                    onclick={() => removeHttpHeaderRow(i)}
                    title="Remove header"
                  >
                    <X size={12} />
                  </button>
                {/if}
              </div>
            {/each}
          </div>
        {/if}
      </div>
    {:else if formType === "vault"}
      <div class="grid grid-cols-2 gap-3 mb-4">
        <div>
          <span
            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
            >Address</span
          >
          {#if isReadOnly}
            <div
              class="px-3 py-2 text-sm font-mono text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
            >
              {vaultAddr || "—"}
            </div>
          {:else}
            <input
              type="url"
              bind:value={vaultAddr}
              placeholder="https://vault.example.com"
              class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
            />
          {/if}
        </div>
        <div>
          <span
            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
            >Mount</span
          >
          {#if isReadOnly}
            <div
              class="px-3 py-2 text-sm font-mono text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
            >
              {vaultMount || "—"}
            </div>
          {:else}
            <input
              type="text"
              bind:value={vaultMount}
              placeholder="secret"
              class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
            />
          {/if}
        </div>
      </div>

      <div class="pt-2 border-t border-slate-100 dark:border-warm-700 mb-3">
        <p class="text-xs font-medium text-slate-500 dark:text-slate-400">
          AppRole Authentication
        </p>
      </div>
      <div class="grid grid-cols-2 gap-3 mb-4">
        <div>
          <span
            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
            >Role ID</span
          >
          {#if isReadOnly}
            <div
              class="px-3 py-2 text-sm font-mono text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
            >
              {vaultRoleId || "—"}
            </div>
          {:else}
            <input
              type="text"
              bind:value={vaultRoleId}
              placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
              class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
            />
          {/if}
        </div>
        <div>
          <span
            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
            >Secret ID</span
          >
          {#if isReadOnly}
            <div
              class="px-3 py-2 text-sm font-mono text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
            >
              {maskedSecret(vaultSecretId)}
            </div>
          {:else}
            <input
              type="password"
              bind:value={vaultSecretId}
              placeholder="Secret ID"
              class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
            />
          {/if}
        </div>
      </div>
      <div class="mb-4">
        <span
          class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
          >AppRole Mount Path</span
        >
        {#if isReadOnly}
          <div
            class="px-3 py-2 text-sm font-mono text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
          >
            {vaultAppRolePath || "approle"}
          </div>
        {:else}
          <input
            type="text"
            bind:value={vaultAppRolePath}
            placeholder="approle"
            class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
          />
        {/if}
      </div>
    {:else if formType === "kubernetes"}
      <div class="mb-4">
        <span
          class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
          >Authentication</span
        >
        {#if isReadOnly}
          <div
            class="px-3 py-2 text-sm text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
          >
            {k8sMode === "in-cluster"
              ? "In-cluster (service account)"
              : k8sMode === "path"
                ? "Kubeconfig file path"
                : "Inline kubeconfig YAML"}
          </div>
        {:else}
          <div class="flex flex-wrap gap-3">
            <label
              class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300 cursor-pointer"
            >
              <input
                type="radio"
                bind:group={k8sMode}
                value="in-cluster"
                class="text-accent-600"
              /> In-cluster
            </label>
            <label
              class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300 cursor-pointer"
            >
              <input
                type="radio"
                bind:group={k8sMode}
                value="path"
                class="text-accent-600"
              /> File path
            </label>
            <label
              class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300 cursor-pointer"
            >
              <input
                type="radio"
                bind:group={k8sMode}
                value="inline"
                class="text-accent-600"
              /> Inline YAML
            </label>
          </div>
        {/if}
      </div>

      {#if k8sMode === "path"}
        <div class="mb-4">
          <span
            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
            >Kubeconfig Path</span
          >
          {#if isReadOnly}
            <div
              class="px-3 py-2 text-sm font-mono text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
            >
              {k8sPath || "—"}
            </div>
          {:else}
            <input
              type="text"
              bind:value={k8sPath}
              placeholder="/path/to/kubeconfig"
              class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
            />
          {/if}
        </div>
      {:else if k8sMode === "inline"}
        <div class="mb-4">
          <span
            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
            >Kubeconfig YAML</span
          >
          {#if isReadOnly}
            <!-- Mask kubeconfig content in view mode: it embeds tokens
                 and private keys. Show only its length. -->
            <div
              class="px-3 py-2 text-sm text-slate-500 dark:text-slate-400 italic bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
            >
              Inline kubeconfig ({k8sContent.length} chars, hidden)
            </div>
          {:else}
            <textarea
              bind:value={k8sContent}
              rows="10"
              spellcheck="false"
              class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10 resize-y"
            ></textarea>
          {/if}
        </div>
      {/if}

      <div
        class="p-3 bg-accent-50 dark:bg-accent-950/30 border border-accent-100 dark:border-accent-900 rounded-md"
      >
        <p class="text-[11px] text-accent-800 dark:text-accent-200">
          Inheritance path format: <code
            class="px-1 py-0.5 bg-white dark:bg-warm-900 border border-accent-200 dark:border-accent-800 rounded"
            >namespace/secret/name</code
          >
          or
          <code
            class="px-1 py-0.5 bg-white dark:bg-warm-900 border border-accent-200 dark:border-accent-800 rounded"
            >namespace/configmap/name</code
          >.
        </p>
      </div>
    {:else if formType === "consul"}
      <div class="mb-4">
        <span
          class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
          >Address</span
        >
        {#if isReadOnly}
          <div
            class="px-3 py-2 text-sm font-mono text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
          >
            {consulAddr || "—"}
          </div>
        {:else}
          <input
            type="url"
            bind:value={consulAddr}
            placeholder="http://consul.example.com:8500"
            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
          />
        {/if}
      </div>
      <div class="mb-4">
        <span
          class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
          >ACL Token (optional)</span
        >
        {#if isReadOnly}
          <div
            class="px-3 py-2 text-sm font-mono text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
          >
            {consulToken ? maskedSecret(consulToken) : "—"}
          </div>
        {:else}
          <input
            type="password"
            bind:value={consulToken}
            placeholder="Consul ACL token"
            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
          />
        {/if}
      </div>
    {:else if formType === "etcd"}
      <div class="mb-4">
        <span
          class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
          >Address</span
        >
        {#if isReadOnly}
          <div
            class="px-3 py-2 text-sm font-mono text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
          >
            {etcdAddr || "—"}
          </div>
        {:else}
          <input
            type="url"
            bind:value={etcdAddr}
            placeholder="http://etcd.example.com:2379"
            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
          />
        {/if}
      </div>
      <div class="grid grid-cols-2 gap-3 mb-4">
        <div>
          <span
            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
            >Username (optional)</span
          >
          {#if isReadOnly}
            <div
              class="px-3 py-2 text-sm text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
            >
              {etcdUser || "—"}
            </div>
          {:else}
            <input
              type="text"
              bind:value={etcdUser}
              placeholder="root"
              class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
            />
          {/if}
        </div>
        <div>
          <span
            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
            >Password (optional)</span
          >
          {#if isReadOnly}
            <div
              class="px-3 py-2 text-sm font-mono text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
            >
              {etcdPass ? maskedSecret(etcdPass) : "—"}
            </div>
          {:else}
            <input
              type="password"
              bind:value={etcdPass}
              placeholder="Password"
              class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
            />
          {/if}
        </div>
      </div>
    {:else if formType === "aws"}
      <div class="mb-4">
        <span
          class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
          >AWS Service</span
        >
        {#if isReadOnly}
          <div
            class="px-3 py-2 text-sm text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
          >
            {awsService === "ssm" ? "SSM Parameter Store" : "Secrets Manager"}
          </div>
        {:else}
          <div class="flex gap-3">
            <label
              class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300 cursor-pointer"
            >
              <input
                type="radio"
                bind:group={awsService}
                value="secretsmanager"
                class="text-accent-600"
              /> Secrets Manager
            </label>
            <label
              class="flex items-center gap-1.5 text-sm text-slate-600 dark:text-slate-300 cursor-pointer"
            >
              <input
                type="radio"
                bind:group={awsService}
                value="ssm"
                class="text-accent-600"
              /> SSM Parameter Store
            </label>
          </div>
        {/if}
      </div>
      <div class="mb-4">
        <span
          class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
          >Region</span
        >
        {#if isReadOnly}
          <div
            class="px-3 py-2 text-sm text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
          >
            {awsRegion || "—"}
          </div>
        {:else}
          <input
            type="text"
            bind:value={awsRegion}
            placeholder="us-east-1"
            class="w-full px-3 py-2 text-sm border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
          />
        {/if}
      </div>
      <div class="grid grid-cols-2 gap-3 mb-4">
        <div>
          <span
            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
            >Access Key ID</span
          >
          {#if isReadOnly}
            <div
              class="px-3 py-2 text-sm font-mono text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
            >
              {awsAccessKey || "—"}
            </div>
          {:else}
            <input
              type="text"
              bind:value={awsAccessKey}
              placeholder="AKIA..."
              class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
            />
          {/if}
        </div>
        <div>
          <span
            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
            >Secret Access Key</span
          >
          {#if isReadOnly}
            <div
              class="px-3 py-2 text-sm font-mono text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
            >
              {maskedSecret(awsSecretKey)}
            </div>
          {:else}
            <input
              type="password"
              bind:value={awsSecretKey}
              placeholder="Secret key"
              class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
            />
          {/if}
        </div>
      </div>
    {:else if formType === "gcp"}
      <div class="mb-4">
        <span
          class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
          >Service Account JSON Key</span
        >
        {#if isReadOnly}
          <!-- GCP service account JSON contains a private key. Show its
               size only; don't render the contents. -->
          <div
            class="px-3 py-2 text-sm text-slate-500 dark:text-slate-400 italic bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
          >
            Service account JSON ({gcpJson.length} chars, hidden)
          </div>
        {:else}
          <textarea
            bind:value={gcpJson}
            rows="6"
            placeholder={'{"type": "service_account", "project_id": "...", ...}'}
            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10 resize-y"
          ></textarea>
        {/if}
      </div>
    {:else if formType === "gcp-parameter"}
      <div class="mb-4">
        <span
          class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
          >Service Account JSON Key</span
        >
        {#if isReadOnly}
          <!-- Same handling as the Secret Manager variant: the JSON
               carries a private key; we only show its size. -->
          <div
            class="px-3 py-2 text-sm text-slate-500 dark:text-slate-400 italic bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
          >
            Service account JSON ({gcpParamJson.length} chars, hidden)
          </div>
        {:else}
          <textarea
            bind:value={gcpParamJson}
            rows="6"
            placeholder={'{"type": "service_account", "project_id": "...", ...}'}
            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10 resize-y"
          ></textarea>
          <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">
            Needs <code>roles/parametermanager.parameterAccessor</code> on the parameters.
          </p>
        {/if}
      </div>
      <div class="mb-4">
        <span
          class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
          >Location</span
        >
        {#if isReadOnly}
          <div
            class="px-3 py-2 text-sm font-mono text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
          >
            {gcpParamLocation || "global"}
          </div>
        {:else}
          <input
            type="text"
            bind:value={gcpParamLocation}
            placeholder="global"
            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
          />
          <p class="mt-1 text-[11px] text-slate-400 dark:text-slate-500">
            Defaults to <code>global</code>. Use a regional location (e.g.
            <code>us-central1</code>) only if your parameters are regional.
          </p>
        {/if}
      </div>
    {:else if formType === "azure"}
      <div class="mb-4">
        <span
          class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
          >Vault URL</span
        >
        {#if isReadOnly}
          <div
            class="px-3 py-2 text-sm font-mono text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
          >
            {azureVaultUrl || "—"}
          </div>
        {:else}
          <input
            type="url"
            bind:value={azureVaultUrl}
            placeholder="https://my-vault.vault.azure.net"
            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
          />
        {/if}
      </div>
      <div class="mb-4">
        <span
          class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
          >Tenant ID</span
        >
        {#if isReadOnly}
          <div
            class="px-3 py-2 text-sm font-mono text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
          >
            {azureTenantId || "—"}
          </div>
        {:else}
          <input
            type="text"
            bind:value={azureTenantId}
            placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
            class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
          />
        {/if}
      </div>
      <div class="grid grid-cols-2 gap-3 mb-4">
        <div>
          <span
            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
            >Client ID</span
          >
          {#if isReadOnly}
            <div
              class="px-3 py-2 text-sm font-mono text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
            >
              {azureClientId || "—"}
            </div>
          {:else}
            <input
              type="text"
              bind:value={azureClientId}
              placeholder="Application (client) ID"
              class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
            />
          {/if}
        </div>
        <div>
          <span
            class="block text-xs font-medium text-slate-500 dark:text-slate-400 mb-1.5"
            >Client Secret</span
          >
          {#if isReadOnly}
            <div
              class="px-3 py-2 text-sm font-mono text-slate-700 dark:text-slate-200 bg-slate-50 dark:bg-warm-900 border border-slate-200 dark:border-warm-700 rounded-md"
            >
              {maskedSecret(azureClientSecret)}
            </div>
          {:else}
            <input
              type="password"
              bind:value={azureClientSecret}
              placeholder="Client secret"
              class="w-full px-3 py-2 text-sm font-mono border border-slate-200 dark:border-warm-700 rounded-md bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 focus:outline-none focus:border-accent-500 focus:ring-2 focus:ring-accent-500/10"
            />
          {/if}
        </div>
      </div>
    {/if}

    {#if mode !== "create"}
      <div class="mt-6 pt-4 border-t border-slate-100 dark:border-warm-700">
        <p class="text-[11px] text-slate-400 dark:text-slate-500">
          Stored secrets are encrypted at rest when the server encryption key is
          set. Use Test to verify connectivity using the persisted credentials.
        </p>
      </div>
    {/if}
  </div>
</div>
