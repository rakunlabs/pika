<script lang="ts">
  import { onMount } from "svelte";
  import axios from "axios";
  import { AlertTriangle, CheckCircle2, KeyRound, RefreshCw, ShieldCheck, Upload } from "lucide-svelte";
  import { configStore } from "@/lib/store/config.svelte";
  import { addToast } from "@/lib/store/toast.svelte";
  import type { ServerTLSSettings, TLSCertificateStatus, TLSServerStatus } from "@/lib/types/config";

  let status = $state<TLSServerStatus | null>(null);
  let loading = $state(false);
  let savingPolicy = $state(false);
  let generating = $state(false);
  let uploading = $state(false);

  let commonName = $state("localhost");
  let sanText = $state("localhost\n127.0.0.1\n::1");
  let validDays = $state(3650);
  let manualCert = $state("");
  let manualKey = $state("");

  const policy = $derived(configStore.settings?.server_tls ?? {});
  const cert = $derived(status?.certificate ?? null);

  onMount(() => {
    void refresh();
  });

  async function refresh() {
    loading = true;
    try {
      if (!configStore.settings) {
        await configStore.loadSettings();
      }
      const { data } = await axios.get<TLSServerStatus>("/api/v1/tls/status");
      status = data;
    } catch (error: any) {
      addToast(error.response?.data?.message || "Failed to load HTTPS status", "alert");
    } finally {
      loading = false;
    }
  }

  function cleanPolicy(next: ServerTLSSettings): ServerTLSSettings {
    return {
      https_disabled: next.https_disabled || undefined,
      plain_http_enabled: next.plain_http_enabled || undefined,
    };
  }

  async function savePolicy(next: ServerTLSSettings) {
    if (next.https_disabled && !next.plain_http_enabled) {
      addToast("HTTP must be enabled before HTTPS can be disabled", "alert");
      return;
    }
    savingPolicy = true;
    try {
      await configStore.saveServerTLSSettings(cleanPolicy(next));
      await refresh();
    } finally {
      savingPolicy = false;
    }
  }

  function setHTTPSEnabled(enabled: boolean) {
    const next = { ...policy, https_disabled: !enabled };
    if (!enabled) next.plain_http_enabled = true;
    void savePolicy(next);
  }

  function setPlainHTTPEnabled(enabled: boolean) {
    const next = { ...policy, plain_http_enabled: enabled };
    if (!enabled && next.https_disabled) next.https_disabled = false;
    void savePolicy(next);
  }

  async function generateSelfSigned() {
    generating = true;
    try {
      const names = sanText
        .split(/\r?\n/)
        .map((v) => v.trim())
        .filter(Boolean);
      await axios.post("/api/v1/tls/self-signed", {
        common_name: commonName || undefined,
        dns_names: names,
        valid_days: validDays || undefined,
      });
      addToast("Self-signed certificate generated", "success");
      await refresh();
    } catch (error: any) {
      addToast(error.response?.data?.message || "Failed to generate certificate", "alert");
    } finally {
      generating = false;
    }
  }

  async function uploadManual() {
    if (!manualCert.trim() || !manualKey.trim()) {
      addToast("Certificate and private key PEM are required", "alert");
      return;
    }
    uploading = true;
    try {
      await axios.put("/api/v1/tls/manual", {
        cert_pem: manualCert,
        key_pem: manualKey,
      });
      manualCert = "";
      manualKey = "";
      addToast("Certificate uploaded", "success");
      await refresh();
    } catch (error: any) {
      addToast(error.response?.data?.message || "Failed to upload certificate", "alert");
    } finally {
      uploading = false;
    }
  }

  function expiryTone(c: TLSCertificateStatus | null): "ok" | "warn" | "bad" | "none" {
    if (!c?.loaded) return "none";
    if (c.days_remaining <= 14) return "bad";
    if (c.days_remaining <= 45) return "warn";
    return "ok";
  }

  function calloutClass(): string {
    if (!status?.process_enabled) {
      return "bg-amber-50 dark:bg-amber-950/40 border-amber-300 dark:border-amber-700 text-amber-900 dark:text-amber-200";
    }
    if (!status.https_enabled || status.plain_http_enabled) {
      return "bg-amber-50 dark:bg-amber-950/40 border-amber-300 dark:border-amber-700 text-amber-900 dark:text-amber-200";
    }
    return "bg-emerald-50 dark:bg-emerald-950/30 border-emerald-300 dark:border-emerald-700 text-emerald-900 dark:text-emerald-200";
  }

  function expiryClass(c: TLSCertificateStatus | null): string {
    const tone = expiryTone(c);
    if (tone === "bad") return "text-vermilion-600 dark:text-vermilion-400";
    if (tone === "warn") return "text-amber-600 dark:text-amber-400";
    if (tone === "ok") return "text-emerald-600 dark:text-emerald-400";
    return "text-slate-500 dark:text-slate-400";
  }
</script>

<div class="space-y-6">
  <div class="flex items-start justify-between gap-4">
    <div>
      <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">
        Certificates
      </h2>
      <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
        Manage HTTPS for the main Pika UI/API and the certificate shared by HTTPS Endpoints.
      </p>
    </div>
    <button
      type="button"
      onclick={refresh}
      disabled={loading}
      class="px-3 py-1.5 text-xs rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 text-slate-700 dark:text-slate-200 cursor-pointer disabled:opacity-50 inline-flex items-center gap-1.5"
    >
      <RefreshCw size={14} class={loading ? "animate-spin" : ""} />
      Refresh
    </button>
  </div>

  <div class={`p-4 rounded-lg border flex items-start gap-3 ${calloutClass()}`}>
    {#if status?.https_enabled && !status?.plain_http_enabled}
      <ShieldCheck size={17} class="shrink-0 mt-0.5" />
    {:else}
      <AlertTriangle size={17} class="shrink-0 mt-0.5" />
    {/if}
    <div>
      <p class="text-sm font-medium m-0">
        {#if !status?.process_enabled}
          HTTPS is disabled by process config
        {:else if status.https_enabled && !status.plain_http_enabled}
          HTTPS-only mode is active
        {:else if status.https_enabled && status.plain_http_enabled}
          HTTPS is active and HTTP is also allowed
        {:else}
          HTTPS is disabled; HTTP is serving the main port
        {/if}
      </p>
      <p class="text-xs mt-0.5 m-0">
        {#if !status?.process_enabled}
          Set <code>PIKA_SERVER_TLS_ENABLED=true</code> and restart to use the managed certificate.
        {:else if status.plain_http_enabled || !status.https_enabled}
          Plain HTTP exposes session cookies and API traffic on the network. Use only behind a trusted proxy or private network.
        {:else}
          Plain HTTP requests on the main port are rejected with HTTP 426.
        {/if}
      </p>
    </div>
  </div>

  <section class="p-5 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm space-y-4">
    <div>
      <h3 class="text-sm font-semibold text-slate-800 dark:text-slate-100 flex items-center gap-1.5">
        <KeyRound size={14} class="text-accent-600 dark:text-accent-400" />
        Main listener policy
      </h3>
      <p class="text-xs text-slate-500 dark:text-slate-400 mt-1">
        These switches apply immediately to new connections on the admin UI/API port.
      </p>
    </div>

    <label class="flex items-start gap-3 cursor-pointer">
      <input
        type="checkbox"
        checked={!policy.https_disabled}
        disabled={savingPolicy || !status?.process_enabled}
        onchange={(e) => setHTTPSEnabled((e.currentTarget as HTMLInputElement).checked)}
        class="mt-0.5 h-4 w-4 rounded border-slate-300 dark:border-warm-600 text-accent-600 focus:ring-accent-500 cursor-pointer disabled:opacity-50"
      />
      <span>
        <span class="block text-sm font-medium text-slate-800 dark:text-slate-100">Serve HTTPS on the main port</span>
        <span class="block text-xs text-slate-500 dark:text-slate-400 mt-0.5">Default on. Disabling it automatically enables plaintext HTTP so you do not lock yourself out.</span>
      </span>
    </label>

    <label class="flex items-start gap-3 cursor-pointer">
      <input
        type="checkbox"
        checked={policy.plain_http_enabled === true || !status?.process_enabled}
        disabled={savingPolicy || !status?.process_enabled}
        onchange={(e) => setPlainHTTPEnabled((e.currentTarget as HTMLInputElement).checked)}
        class="mt-0.5 h-4 w-4 rounded border-slate-300 dark:border-warm-600 text-accent-600 focus:ring-accent-500 cursor-pointer disabled:opacity-50"
      />
      <span>
        <span class="block text-sm font-medium text-slate-800 dark:text-slate-100">Allow plaintext HTTP on the main port</span>
        <span class="block text-xs text-slate-500 dark:text-slate-400 mt-0.5">Useful behind a TLS-terminating reverse proxy. Avoid exposing this directly to untrusted networks.</span>
      </span>
    </label>
  </section>

  <section class="p-5 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm space-y-4">
    <div class="flex items-start justify-between gap-3">
      <div>
        <h3 class="text-sm font-semibold text-slate-800 dark:text-slate-100 flex items-center gap-1.5">
          <CheckCircle2 size={14} class={expiryClass(cert)} />
          Active certificate
        </h3>
        <p class="text-xs text-slate-500 dark:text-slate-400 mt-1">
          Used by the main HTTPS listener and any Endpoint with HTTPS enabled.
        </p>
      </div>
      {#if cert?.loaded}
        <span class={`text-xs font-medium ${expiryClass(cert)}`}>
          {cert.days_remaining} day{cert.days_remaining === 1 ? "" : "s"} remaining
        </span>
      {/if}
    </div>

    {#if cert?.loaded}
      <div class="grid gap-3 sm:grid-cols-2">
        <div class="p-3 rounded border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900">
          <p class="text-[11px] uppercase tracking-wider text-slate-500 dark:text-slate-400">Subject</p>
          <p class="mt-1 text-xs font-mono text-slate-700 dark:text-slate-200 break-all">{cert.subject}</p>
        </div>
        <div class="p-3 rounded border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900">
          <p class="text-[11px] uppercase tracking-wider text-slate-500 dark:text-slate-400">Issuer</p>
          <p class="mt-1 text-xs font-mono text-slate-700 dark:text-slate-200 break-all">{cert.issuer}</p>
        </div>
        <div class="p-3 rounded border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900">
          <p class="text-[11px] uppercase tracking-wider text-slate-500 dark:text-slate-400">Valid until</p>
          <p class="mt-1 text-xs font-mono text-slate-700 dark:text-slate-200">{cert.not_after ? new Date(cert.not_after).toLocaleString() : "N/A"}</p>
        </div>
        <div class="p-3 rounded border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900">
          <p class="text-[11px] uppercase tracking-wider text-slate-500 dark:text-slate-400">Fingerprint SHA-256</p>
          <p class="mt-1 text-xs font-mono text-slate-700 dark:text-slate-200 break-all">{cert.fingerprint_sha256}</p>
        </div>
      </div>
      <div class="text-xs text-slate-500 dark:text-slate-400 font-mono break-all">
        SAN: {[...(cert.dns_names ?? []), ...(cert.ip_addresses ?? [])].join(", ") || "none"}
      </div>
      <div class="text-[11px] text-slate-500 dark:text-slate-400 font-mono break-all">
        Files: {cert.cert_file} / {cert.key_file}
      </div>
    {:else}
      <div class="p-3 rounded border border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-950/40 text-xs text-amber-900 dark:text-amber-200">
        No certificate is loaded. Generate a self-signed certificate or upload a PEM pair.
      </div>
    {/if}
  </section>

  <section class="p-5 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm space-y-4">
    <div>
      <h3 class="text-sm font-semibold text-slate-800 dark:text-slate-100">Generate self-signed certificate</h3>
      <p class="text-xs text-slate-500 dark:text-slate-400 mt-1">Good for local/private deployments. Browsers will still show a trust warning until you trust the certificate.</p>
    </div>
    <div class="grid gap-3 sm:grid-cols-2">
      <label class="text-xs space-y-1 block">
        <span class="text-slate-600 dark:text-slate-300">Common name</span>
        <input bind:value={commonName} class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500" />
      </label>
      <label class="text-xs space-y-1 block">
        <span class="text-slate-600 dark:text-slate-300">Valid days</span>
        <input type="number" min="1" bind:value={validDays} class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500" />
      </label>
      <label class="text-xs space-y-1 block sm:col-span-2">
        <span class="text-slate-600 dark:text-slate-300">DNS names / IP addresses, one per line</span>
        <textarea rows="4" bind:value={sanText} class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500 font-mono"></textarea>
      </label>
    </div>
    <button type="button" onclick={generateSelfSigned} disabled={generating} class="px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer inline-flex items-center gap-1.5">
      <KeyRound size={13} />
      {generating ? "Generating..." : "Generate and activate"}
    </button>
  </section>

  <section class="p-5 bg-white dark:bg-warm-800 border border-slate-200 dark:border-warm-700 rounded-lg shadow-sm space-y-4">
    <div>
      <h3 class="text-sm font-semibold text-slate-800 dark:text-slate-100">Upload certificate</h3>
      <p class="text-xs text-slate-500 dark:text-slate-400 mt-1">Paste a PEM certificate chain and matching private key. New HTTPS connections use it immediately.</p>
    </div>
    <label class="text-xs space-y-1 block">
      <span class="text-slate-600 dark:text-slate-300">Certificate PEM</span>
      <textarea rows="6" bind:value={manualCert} class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500 font-mono" placeholder="-----BEGIN CERTIFICATE-----"></textarea>
    </label>
    <label class="text-xs space-y-1 block">
      <span class="text-slate-600 dark:text-slate-300">Private key PEM</span>
      <textarea rows="6" bind:value={manualKey} class="w-full px-3 py-2 text-sm rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-900 text-slate-800 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-accent-500 font-mono" placeholder="-----BEGIN PRIVATE KEY-----"></textarea>
    </label>
    <button type="button" onclick={uploadManual} disabled={uploading} class="px-3 py-1.5 text-xs rounded bg-accent-600 text-white font-medium hover:bg-accent-700 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer inline-flex items-center gap-1.5">
      <Upload size={13} />
      {uploading ? "Uploading..." : "Upload and activate"}
    </button>
  </section>
</div>
