<script lang="ts">
    import { confirmDialog } from "@/lib/store/confirm.svelte";
    import { apiErrorMessage } from "@/lib/api/client";
    import { configStore } from "@/lib/store/config.svelte";
    import { addToast } from "@/lib/store/toast.svelte";
    import { onMount } from "svelte";
    import {
        Plus,
        Trash2,
        Plug,
        Play,
        RefreshCw,
        AlertTriangle,
        ShieldCheck,
        Power,
        PowerOff,
    } from "lucide-svelte";
    import type {
        PublicEndpoint,
        PublicEndpointStatus,
        PublicEndpointTestResult,
    } from "@/lib/types/config";
    import EndpointFormModal from "@/lib/components/publicendpoints/EndpointFormModal.svelte";

    // ── Local state mirrors of the settings list and the live
    // diagnostic status so the UI can show a "running" badge next
    // to each entry without re-loading the whole settings document.
    let endpoints = $state<PublicEndpoint[]>([]);
    let statuses = $state<PublicEndpointStatus[]>([]);
    let loading = $state(false);

    // Modal state for create + edit. The modal clones the endpoint on
    // open, so the cancel path is trivial.
    let formOpen = $state(false);
    let editingEndpoint = $state<PublicEndpoint | null>(null);

    // Probe panel state (per-endpoint).
    let probeOpenFor = $state<string | null>(null);
    let probeKey = $state("");
    let probeVariant = $state("");
    let probeVersion = $state("");
    let probeRaw = $state(false);
    let probeFormat = $state("");
    // probeHeaders is an editable list of (name, value) pairs the
    // operator wants forwarded onto the synthetic request — auth
    // tokens, request-check policy headers (X-Tenant, etc.), or
    // anything else the live handler chain inspects.
    let probeHeaders = $state<{ name: string; value: string }[]>([]);
    let probeResult = $state<PublicEndpointTestResult | null>(null);
    let probing = $state(false);

    onMount(() => {
        void (async () => {
            if (!configStore.settings) {
                await configStore.loadSettings();
            }
            endpoints = [...(configStore.settings?.public_endpoints || [])];
            await refreshStatus();
        })();
    });

    async function refreshStatus() {
        loading = true;
        try {
            statuses = await configStore.listPublicEndpointStatus();
        } finally {
            loading = false;
        }
    }

    function statusFor(id: string): PublicEndpointStatus | undefined {
        return statuses.find((s) => s.id === id);
    }

    function openCreate() {
        editingEndpoint = null;
        formOpen = true;
    }

    function openEdit(ep: PublicEndpoint) {
        editingEndpoint = ep;
        formOpen = true;
    }

    async function deleteEndpoint(id: string) {
        if (
            !(await confirmDialog({
                title: "Delete this endpoint?",
                message: "The listener will stop immediately.",
                confirmLabel: "Delete",
                danger: true,
            }))
        ) {
            return;
        }
        const next = endpoints.filter((e) => e.id !== id);
        try {
            await configStore.savePublicEndpoints(next);
            endpoints = [
                ...(configStore.settings?.public_endpoints || next),
            ];
            await refreshStatus();
        } catch {
            /* toast handled in store */
        }
    }

    async function toggleEnabled(ep: PublicEndpoint) {
        const next = endpoints.map((e) =>
            e.id === ep.id ? { ...e, enabled: !e.enabled } : e,
        );
        try {
            await configStore.savePublicEndpoints(next);
            endpoints = [
                ...(configStore.settings?.public_endpoints || next),
            ];
            await refreshStatus();
        } catch {
            /* toast handled in store */
        }
    }

    function openProbe(ep: PublicEndpoint) {
        probeOpenFor = ep.id;
        probeKey = "";
        probeVariant = "";
        probeVersion = "";
        probeRaw = false;
        probeFormat = "";
        probeHeaders = [];
        probeResult = null;
    }

    function addProbeHeader() {
        probeHeaders.push({ name: "", value: "" });
    }
    function removeProbeHeader(idx: number) {
        probeHeaders.splice(idx, 1);
    }

    async function runProbe() {
        if (!probeOpenFor) return;
        probing = true;
        probeResult = null;
        try {
            const hdrs: Record<string, string> = {};
            for (const h of probeHeaders) {
                const name = h.name.trim();
                if (name) hdrs[name] = h.value;
            }
            probeResult = await configStore.testPublicEndpoint(probeOpenFor, {
                key: probeKey,
                variant: probeVariant || undefined,
                version: probeVersion || undefined,
                raw: probeRaw || undefined,
                format: probeFormat || undefined,
                headers: Object.keys(hdrs).length > 0 ? hdrs : undefined,
            });
        } catch (err) {
            const msg = apiErrorMessage(err, "Probe failed");
            addToast(msg, "alert");
        } finally {
            probing = false;
        }
    }

    function endpointScheme(ep: PublicEndpoint): string {
        return ep.tls?.enabled ? "https" : "http";
    }
</script>

<div class="space-y-6">
    <div class="flex items-start justify-between gap-4">
        <div>
            <h2 class="text-lg font-semibold text-slate-800 dark:text-slate-100">
                Endpoints
            </h2>
            <p class="text-sm text-slate-500 dark:text-slate-400 mt-0.5">
                Expose pika config data on operator-defined HTTP ports, with
                optional per-request inspection / modification.
            </p>
        </div>
        <div class="flex gap-2">
            <button
                type="button"
                onclick={refreshStatus}
                class="px-3 py-1.5 text-xs rounded bg-slate-100 dark:bg-warm-800 hover:bg-slate-200 dark:hover:bg-warm-700 text-slate-700 dark:text-slate-200 cursor-pointer disabled:opacity-50 inline-flex items-center gap-1.5"
                disabled={loading}
            >
                <RefreshCw size={14} class={loading ? "animate-spin" : ""} />
                Refresh
            </button>
            <button
                type="button"
                onclick={openCreate}
                class="px-3 py-1.5 text-xs rounded bg-accent-600 hover:bg-accent-700 text-white cursor-pointer inline-flex items-center gap-1.5"
            >
                <Plus size={14} />
                New endpoint
            </button>
        </div>
    </div>

    {#if endpoints.length === 0}
        <div
            class="p-6 rounded-lg border border-dashed border-slate-300 dark:border-warm-700 bg-white dark:bg-warm-800 text-center"
        >
            <Plug size={20} class="mx-auto text-slate-400 dark:text-slate-500" />
            <p class="mt-2 text-sm text-slate-600 dark:text-slate-300">
                No endpoints configured yet.
            </p>
            <p class="text-xs text-slate-500 dark:text-slate-400 mt-1">
                Add one to serve direct config bytes, Consul KV compatibility,
                or a custom response shape.
            </p>
        </div>
    {:else}
        <div class="space-y-3">
            {#each endpoints as ep (ep.id || ep.name)}
                {@const status = statusFor(ep.id)}
                <div
                    class="p-4 rounded-lg border border-slate-200 dark:border-warm-700 bg-white dark:bg-warm-800"
                >
                    <div class="flex items-start justify-between gap-4">
                        <div class="min-w-0">
                            <div class="flex items-center gap-2 flex-wrap">
                                <h3
                                    class="text-sm font-semibold text-slate-800 dark:text-slate-100 truncate"
                                >
                                    {ep.name || "(unnamed)"}
                                </h3>
                                {#if !ep.enabled}
                                    <span
                                        class="text-[10px] px-1.5 py-0.5 rounded bg-slate-100 dark:bg-warm-700 text-slate-600 dark:text-slate-300"
                                        >disabled</span
                                    >
                                {:else if status?.running}
                                    <span
                                        class="text-[10px] px-1.5 py-0.5 rounded bg-emerald-100 dark:bg-emerald-900/40 text-emerald-700 dark:text-emerald-300 inline-flex items-center gap-1"
                                    >
                                        <ShieldCheck size={10} /> running
                                    </span>
                                {:else if status?.last_error}
                                    <span
                                        class="text-[10px] px-1.5 py-0.5 rounded bg-amber-100 dark:bg-amber-900/40 text-amber-800 dark:text-amber-300 inline-flex items-center gap-1"
                                    >
                                        <AlertTriangle size={10} /> bind failed
                                    </span>
                                {:else}
                                    <span
                                        class="text-[10px] px-1.5 py-0.5 rounded bg-slate-100 dark:bg-warm-700 text-slate-600 dark:text-slate-300"
                                        >pending</span
                                    >
                                {/if}
                                <span
                                    class="text-[10px] px-1.5 py-0.5 rounded bg-slate-100 dark:bg-warm-700 text-slate-700 dark:text-slate-200"
                                    >mode: {ep.mode}</span
                                >
                                <span
                                    class="text-[10px] px-1.5 py-0.5 rounded bg-slate-100 dark:bg-warm-700 text-slate-700 dark:text-slate-200"
                                    >auth: {ep.auth.mode}</span
                                >
                                {#if ep.tls?.enabled}
                                    <span
                                        class="text-[10px] px-1.5 py-0.5 rounded bg-emerald-100 dark:bg-emerald-900/40 text-emerald-700 dark:text-emerald-300"
                                        >https</span
                                    >
                                    {#if ep.tls.allow_http}
                                        <span
                                            class="text-[10px] px-1.5 py-0.5 rounded bg-amber-100 dark:bg-amber-900/40 text-amber-800 dark:text-amber-300"
                                            >http allowed</span
                                        >
                                    {/if}
                                {:else}
                                    <span
                                        class="text-[10px] px-1.5 py-0.5 rounded bg-amber-100 dark:bg-amber-900/40 text-amber-800 dark:text-amber-300"
                                        >http</span
                                    >
                                {/if}
                                {#if ep.request_check}
                                    <span
                                        class="text-[10px] px-1.5 py-0.5 rounded bg-indigo-100 dark:bg-indigo-900/40 text-indigo-700 dark:text-indigo-300"
                                        >request check</span
                                    >
                                {/if}
                            </div>
                            <p
                                class="text-xs text-slate-500 dark:text-slate-400 mt-1 font-mono"
                            >
                                {endpointScheme(ep)}://{ep.listen_host || "0.0.0.0"}:{ep.listen_port}{ep.base_path ===
                                "/"
                                    ? ""
                                    : ep.base_path}
                            </p>
                            {#if status?.last_error}
                                <p
                                    class="text-xs text-amber-700 dark:text-amber-300 mt-1 font-mono"
                                >
                                    {status.last_error}
                                </p>
                            {/if}
                        </div>
                        <div class="flex gap-1 shrink-0">
                            <button
                                type="button"
                                onclick={() => toggleEnabled(ep)}
                                class="p-1.5 rounded hover:bg-slate-100 dark:hover:bg-warm-700 text-slate-600 dark:text-slate-300 cursor-pointer"
                                title={ep.enabled ? "Disable" : "Enable"}
                                aria-label={ep.enabled ? "Disable endpoint" : "Enable endpoint"}
                            >
                                {#if ep.enabled}
                                    <PowerOff size={14} />
                                {:else}
                                    <Power size={14} />
                                {/if}
                            </button>
                            <button
                                type="button"
                                onclick={() => openProbe(ep)}
                                class="p-1.5 rounded hover:bg-slate-100 dark:hover:bg-warm-700 text-slate-600 dark:text-slate-300 cursor-pointer"
                                title="Test"
                                aria-label="Test endpoint"
                            >
                                <Play size={14} />
                            </button>
                            <button
                                type="button"
                                onclick={() => openEdit(ep)}
                                class="px-2 py-1 text-xs rounded bg-slate-100 dark:bg-warm-700 hover:bg-slate-200 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-200 cursor-pointer"
                            >
                                Edit
                            </button>
                            <button
                                type="button"
                                onclick={() => deleteEndpoint(ep.id)}
                                class="p-1.5 rounded hover:bg-vermilion-50 dark:hover:bg-vermilion-900/40 text-vermilion-600 dark:text-vermilion-400 cursor-pointer"
                                title="Delete"
                                aria-label="Delete endpoint"
                            >
                                <Trash2 size={14} />
                            </button>
                        </div>
                    </div>

                    {#if probeOpenFor === ep.id}
                        <div
                            class="mt-4 p-3 rounded border border-slate-200 dark:border-warm-700 bg-slate-50 dark:bg-warm-900 space-y-3"
                        >
                            <div class="flex items-center gap-2 flex-wrap">
                                <input
                                    type="text"
                                    bind:value={probeKey}
                                    placeholder="config/key/path"
                                    class="flex-1 min-w-[12rem] px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500 font-mono"
                                />
                                <input
                                    type="text"
                                    bind:value={probeVariant}
                                    placeholder="variant (optional)"
                                    class="w-32 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500"
                                />
                                <input
                                    type="text"
                                    bind:value={probeVersion}
                                    placeholder="version"
                                    class="w-24 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500"
                                />
                                <select
                                    bind:value={probeFormat}
                                    class="px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500"
                                >
                                    <option value="">format (auto)</option>
                                    <option value="json">json</option>
                                    <option value="yaml">yaml</option>
                                    <option value="toml">toml</option>
                                </select>
                                <label
                                    class="inline-flex items-center gap-1 text-xs text-slate-700 dark:text-slate-200"
                                >
                                    <input
                                        type="checkbox"
                                        bind:checked={probeRaw}
                                        class="rounded border-slate-300 dark:border-warm-600"
                                    /> raw
                                </label>
                                <button
                                    type="button"
                                    onclick={runProbe}
                                    disabled={probing}
                                    class="px-2 py-1 text-xs rounded bg-accent-600 hover:bg-accent-700 text-white cursor-pointer inline-flex items-center gap-1 disabled:opacity-50"
                                >
                                    <Play size={12} />
                                    Probe
                                </button>
                                <button
                                    type="button"
                                    onclick={() => (probeOpenFor = null)}
                                    class="text-xs text-slate-500 dark:text-slate-400 hover:underline cursor-pointer"
                                >
                                    Close
                                </button>
                            </div>

                            <div class="space-y-1.5">
                                <div class="flex items-center gap-2">
                                    <span class="text-[11px] uppercase tracking-wider text-slate-500 dark:text-slate-400">
                                        Request headers
                                    </span>
                                    <button
                                        type="button"
                                        onclick={addProbeHeader}
                                        class="px-1.5 py-0.5 text-[11px] rounded bg-slate-200 dark:bg-warm-700 hover:bg-slate-300 dark:hover:bg-warm-600 text-slate-700 dark:text-slate-200 cursor-pointer inline-flex items-center gap-1"
                                    >
                                        <Plus size={10} /> add
                                    </button>
                                    <span class="text-[10px] text-slate-400 dark:text-slate-500">
                                        forwarded onto the synthetic request — use this for auth tokens, X-Tenant, …
                                    </span>
                                </div>
                                {#each probeHeaders as h, i}
                                    <div class="flex gap-2 items-center">
                                        <input
                                            type="text"
                                            bind:value={h.name}
                                            placeholder="Header"
                                            class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500 font-mono"
                                        />
                                        <input
                                            type="text"
                                            bind:value={h.value}
                                            placeholder="value"
                                            class="flex-1 px-2 py-1 text-xs rounded border border-slate-300 dark:border-warm-600 bg-white dark:bg-warm-800 text-slate-800 dark:text-slate-100 focus:outline-none focus:ring-2 focus:ring-accent-500 font-mono"
                                        />
                                        <button
                                            type="button"
                                            onclick={() => removeProbeHeader(i)}
                                            class="p-1 rounded hover:bg-vermilion-50 dark:hover:bg-vermilion-900/40 text-vermilion-600 dark:text-vermilion-400 cursor-pointer"
                                            title="Remove"
                                            aria-label="Remove header"
                                        >
                                            <Trash2 size={12} />
                                        </button>
                                    </div>
                                {/each}
                            </div>
                            {#if probeResult}
                                <div class="text-xs space-y-1">
                                    <div
                                        class="font-mono text-slate-600 dark:text-slate-300"
                                    >
                                        HTTP {probeResult.status}
                                    </div>
                                    {#if probeResult.headers && Object.keys(probeResult.headers).length > 0}
                                        <div
                                            class="font-mono text-slate-500 dark:text-slate-400"
                                        >
                                            {#each Object.entries(probeResult.headers) as [k, v]}
                                                <div>{k}: {v}</div>
                                            {/each}
                                        </div>
                                    {/if}
                                    <pre
                                        class="text-[11px] p-2 rounded bg-slate-900 dark:bg-warm-950 text-slate-100 overflow-auto max-h-64">{probeResult.body}</pre>
                                </div>
                            {/if}
                        </div>
                    {/if}
                </div>
            {/each}
        </div>
    {/if}
</div>

{#if formOpen}
    <EndpointFormModal
        endpoint={editingEndpoint}
        {endpoints}
        onSaved={async (next) => {
            endpoints = next;
            formOpen = false;
            await refreshStatus();
        }}
        onClose={() => (formOpen = false)}
    />
{/if}
